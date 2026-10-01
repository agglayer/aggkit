package bridgedetector

import (
	"context"
	"fmt"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
)

// StartBlockResolution is the outcome of resolving a bridge detector's configured start-block field
// (L1ToL2BridgeDetector.StartBlock or L2ToLxBridgeDetector.StartL1Block).
type StartBlockResolution struct {
	// Block is the concrete start block to hand to the detector.
	Block uint64
}

// ResolveStartBlock resolves a bridge detector's start-block config field into a concrete block
// number:
//
//   - An explicit value (configured != nil), including 0, is used verbatim: no RPC call, no
//     clamping. This is what makes the change backwards compatible -- a deployment that already
//     pins a start block keeps behaving exactly as it did before this field became a pointer.
//   - An unset value (configured == nil) is resolved by binary-searching L1 block headers for the
//     highest block at or before "now - lookback" (about log2(head-minBlock) header lookups), then
//     clamped to [minBlock, head] so it can never predate the contract this detector cares about
//     (minBlock) nor exceed the current chain head.
//
// It deliberately does not estimate the block number from an average block-time constant: a fixed
// seconds-per-block assumption has been observed, in an orchestration tool in this same codebase, to
// be wrong enough (12.0s assumed vs. 12.512s actual on Sepolia) to skew a multi-hour estimate by
// over ten hours. Reading real timestamps back from the chain avoids that class of error entirely.
func ResolveStartBlock(
	ctx context.Context,
	l1Client aggkittypes.EthClienter,
	configured *uint64,
	lookback time.Duration,
	minBlock uint64,
	now time.Time,
	logger aggkitcommon.Logger,
	detectorName string,
) (StartBlockResolution, error) {
	if configured != nil {
		if *configured == 0 && logger != nil {
			logger.Warnf(
				"autoclaim %s: start block explicitly set to 0 (genesis); this scans the entire "+
					"chain history from block 0 and may take a very long time", detectorName)
		}
		return StartBlockResolution{Block: *configured}, nil
	}

	block, blockTime, err := resolveBlockAtLookback(ctx, l1Client, lookback, minBlock, now)
	if err != nil {
		return StartBlockResolution{}, fmt.Errorf(
			"resolve %s start block from %s lookback: %w", detectorName, lookback, err)
	}

	if logger != nil {
		logger.Infof(
			"autoclaim %s: no start block configured; resolved block %d (unix timestamp %d) from a "+
				"%s lookback ending %s UTC",
			detectorName, block, blockTime,
			lookback, now.UTC().Format(time.RFC3339))
		logger.Warnf(
			"autoclaim %s: bridges originating before block %d will never be autoclaimed and "+
				"require manual claiming", detectorName, block)
	}

	return StartBlockResolution{Block: block}, nil
}

// resolveBlockAtLookback binary-searches L1 block headers for the highest block whose timestamp is
// at or before now-lookback, then clamps the result to [minBlock, head]. If the head itself is at or below minBlock (a
// misconfigured floor), the head is returned as-is: there is nothing newer to resolve to.
func resolveBlockAtLookback(
	ctx context.Context,
	l1Client aggkittypes.EthClienter,
	lookback time.Duration,
	minBlock uint64,
	now time.Time,
) (block, blockTime uint64, err error) {
	head, err := l1Client.CustomHeaderByNumber(ctx, &aggkittypes.LatestBlock)
	if err != nil {
		return 0, 0, fmt.Errorf("get L1 head header: %w", err)
	}
	if head.Number <= minBlock {
		return head.Number, head.Time, nil
	}

	var targetTime uint64
	if nowUnix := now.Unix(); nowUnix > int64(lookback.Seconds()) {
		targetTime = uint64(nowUnix) - uint64(lookback.Seconds())
	}

	minHeader, err := headerAt(ctx, l1Client, minBlock)
	if err != nil {
		return 0, 0, err
	}
	if minHeader.Time >= targetTime {
		// The lookback window predates (or starts exactly at) minBlock itself: clamp to the floor.
		return minHeader.Number, minHeader.Time, nil
	}

	// Binary search (minBlock, head.Number] for the highest block whose timestamp is <= targetTime.
	// Using an upper mid keeps the search converging without risking a uint64 underflow at 0.
	lo, hi := minBlock, head.Number
	best := minHeader
	for lo < hi {
		mid := lo + (hi-lo+1)/2 //nolint:mnd
		h, err := headerAt(ctx, l1Client, mid)
		if err != nil {
			return 0, 0, err
		}
		if h.Time <= targetTime {
			lo = mid
			best = h
		} else {
			hi = mid - 1
		}
	}

	return best.Number, best.Time, nil
}

func headerAt(ctx context.Context, l1Client aggkittypes.EthClienter, block uint64) (*aggkittypes.BlockHeader, error) {
	h, err := l1Client.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(block))
	if err != nil {
		return nil, fmt.Errorf("get header at block %d: %w", block, err)
	}
	return h, nil
}
