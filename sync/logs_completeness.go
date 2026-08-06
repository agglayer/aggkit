package sync

import (
	"context"
	"errors"
	"time"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// maxArbitrationAttempts bounds the single-block re-query used to arbitrate a bloom-positive,
// zero-log block (see checkLogsCompleteness). Kept small: arbitration exists to distinguish a
// genuine eth_getLogs omission from a deterministic bloom false positive, not to compensate for a
// generally unhealthy RPC.
const maxArbitrationAttempts = 2

// checkLogsCompleteness defends against a silent eth_getLogs omission: during an L1 reorg a
// flaky/mixed RPC view can make eth_getLogs skip a log for a block while that block's header hash
// stays canonical, which is invisible to header-hash-based reorg detection by construction (this
// is exactly what happened in the bokuto 2026-08-05 incident). Block header logs blooms have no
// false negatives, so a block whose bloom is positive for one of d.addressesToQuery but for which
// unfilteredLogs contains no entry is *suspicious*.
//
// A bloom false positive is deterministic per (block, address): suspicion alone must never fail
// the range -- that would wedge the syncer forever on a false positive -- so every suspicion is
// arbitrated with a single-block re-query (arbitrateSuspectedOmission) before being trusted.
//
// The check only covers blocks strictly above lastFinalizedBlock. Verifying a block costs one
// HeaderByNumber call (this package has no batch machinery), which is prohibitive across large
// finalized catch-up ranges, while the mixed-view/reorg omission class this defends against lives
// near the chain tip; during steady tailing this window is small. Completeness of the finalized
// zone for legacy syncers (bridgesync, claimsync, l2gersync) is therefore a known gap -- their
// long-term fix is migrating to the multidownloader, which verifies both zones.
//
// It returns true when at least one suspected omission was arbitrated and confirmed genuine, in
// which case the caller must retry the whole range rather than accept it (see the comment at the
// call site in getEventsByBlockRangeWithRetry for why treating it like an ordinary empty range
// would be unsafe).
func (d *EVMDownloaderImplementation) checkLogsCompleteness(
	ctx context.Context, fromBlock, toBlock, lastFinalizedBlock uint64, unfilteredLogs []types.Log,
) bool {
	verifyFrom := fromBlock
	if lastFinalizedBlock >= verifyFrom {
		verifyFrom = lastFinalizedBlock + 1
	}
	if verifyFrom > toBlock {
		// Whole range is at or below the last finalized block: outside the verified window.
		return false
	}

	blocksWithLogs := make(map[uint64]struct{}, len(unfilteredLogs))
	for _, l := range unfilteredLogs {
		blocksWithLogs[l.BlockNumber] = struct{}{}
	}

	for blockNum := verifyFrom; blockNum <= toBlock; blockNum++ {
		if _, ok := blocksWithLogs[blockNum]; ok {
			// At least one unfiltered log already accounts for this block; nothing to check.
			continue
		}

		select {
		case <-ctx.Done():
			return false
		default:
		}

		header, canceled := d.getHeaderForCompletenessCheck(ctx, blockNum)
		if canceled {
			return false
		}

		// Nil bloom (retrieval path didn't provide it) or bloom negative: no assertion can be
		// made, or the bloom itself guarantees no queried address logged in this block. Either
		// way, not suspicious -- graceful degradation to "skip the check" for that block.
		if !header.BloomMightContainAddresses(d.addressesToQuery) {
			continue
		}

		if d.arbitrateSuspectedOmission(ctx, blockNum, header.Hash) {
			return true
		}
	}

	return false
}

// getHeaderForCompletenessCheck fetches the header for blockNum, retrying on the same transient
// errors as GetBlockHeader (block temporarily not found during a reorg, other transient RPC
// errors), but returning the raw *aggkittypes.BlockHeader so its LogsBloom is available.
func (d *EVMDownloaderImplementation) getHeaderForCompletenessCheck(
	ctx context.Context, blockNum uint64,
) (*aggkittypes.BlockHeader, bool) {
	attempts := 0
	for {
		header, err := d.ethClient.HeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNum))
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, true
			}
			if errors.Is(err, ethereum.NotFound) {
				// Block num can temporarily disappear from the execution client due to a reorg;
				// wait and retry rather than treating it as an error.
				d.log.Warnf("logs completeness check: block %d not found on the ethereum client: %v", blockNum, err)
				if d.rh.RetryAfterErrorPeriod != 0 {
					time.Sleep(d.rh.RetryAfterErrorPeriod)
				} else {
					time.Sleep(DefaultWaitPeriodBlockNotFound)
				}
				continue
			}

			attempts++
			d.log.Errorf("logs completeness check: error getting header for block %d, err: %v", blockNum, err)
			d.rh.Handle(ctx, "logsCompletenessGetHeader", attempts)
			continue
		}

		return header, false
	}
}

// arbitrateSuspectedOmission re-queries logs for a single block by hash to distinguish a genuine
// eth_getLogs omission from a deterministic bloom false positive. It returns true when a log
// matching blockHash is found (omission confirmed genuine) and also, conservatively, when every
// arbitration attempt fails to execute: an unverifiable suspicious block must trigger a range
// retry rather than be silently accepted, since silent acceptance is exactly the failure mode this
// check exists to prevent (same semantics as the multidownloader's arbitrateSuspiciousBlock). A
// successfully executed query with no matching logs is a bloom-false-positive verdict.
func (d *EVMDownloaderImplementation) arbitrateSuspectedOmission(
	ctx context.Context, blockNum uint64, blockHash common.Hash,
) bool {
	query := ethereum.FilterQuery{
		BlockHash: &blockHash,
		Addresses: d.addressesToQuery,
	}

	reachedVerdict := false
	for attempt := 1; attempt <= maxArbitrationAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		logs, err := d.ethClient.FilterLogs(ctx, query)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return false
			}
			d.log.Warnf("logs completeness check: arbitration query failed for block %d (attempt %d/%d): %v",
				blockNum, attempt, maxArbitrationAttempts, err)
			continue
		}

		for _, l := range logs {
			if l.BlockHash == blockHash {
				d.log.Warnf(
					"logs completeness check: confirmed eth_getLogs omission for block %d (hash %s): "+
						"bloom-positive block had 0 logs in the range query but arbitration found %d log(s)",
					blockNum, blockHash.Hex(), len(logs),
				)
				return true
			}
		}
		reachedVerdict = true
	}

	if !reachedVerdict {
		d.log.Warnf(
			"logs completeness check: could not arbitrate suspicious block %d (hash %s): all %d re-query "+
				"attempts failed; conservatively retrying the range",
			blockNum, blockHash.Hex(), maxArbitrationAttempts,
		)
		return true
	}

	d.log.Debugf(
		"logs completeness check: block %d (hash %s) was bloom-positive but arbitration found no matching logs "+
			"after %d attempt(s); treating as a deterministic bloom false positive",
		blockNum, blockHash.Hex(), maxArbitrationAttempts,
	)
	return false
}
