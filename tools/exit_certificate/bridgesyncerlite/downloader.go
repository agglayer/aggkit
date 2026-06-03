package bridgesyncerlite

import (
	"context"
	"fmt"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/sync/errgroup"
)

// progressLogInterval is how often fetchBridges reports parallel-fetch progress with an ETA.
const progressLogInterval = 5 * time.Second

// percentMultiplier converts a [0,1] fraction to a percentage for progress logging.
const percentMultiplier = 100

var (
	// bridgeEventSignature is the only event this syncer ingests.
	bridgeEventSignature = crypto.Keccak256Hash([]byte(
		"BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)",
	))

	// forbiddenEventSignatures are events whose presence means the bridge state cannot be
	// reconstructed from BridgeEvent logs alone (token remappings, legacy migrations, LET
	// rollbacks/advances). Detecting any of them aborts the sync.
	forbiddenEventSignatures = buildForbiddenEventSignatures()
)

func buildForbiddenEventSignatures() map[common.Hash]string {
	sigs := map[string]string{
		"SetSovereignTokenAddress(uint32,address,address,bool)": "SetSovereignTokenAddress",
		"MigrateLegacyToken(address,address,address,uint256)":   "MigrateLegacyToken",
		"RemoveLegacySovereignTokenAddress(address)":            "RemoveLegacySovereignTokenAddress",
		"BackwardLET(uint256,bytes32,uint256,bytes32)":          "BackwardLET",
		"ForwardLET(uint256,bytes32,uint256,bytes32,bytes)":     "ForwardLET",
	}
	out := make(map[common.Hash]string, len(sigs))
	for sig, name := range sigs {
		out[crypto.Keccak256Hash([]byte(sig))] = name
	}
	return out
}

// fetchBridges reads every BridgeEvent emitted by the bridge contract in [fromBlock, toBlock],
// splitting the range into BlockChunkSize-sized windows and querying them in parallel (bounded by
// Concurrency). It returns the parsed leaves unsorted; the caller orders them by deposit count.
// If any forbidden event is seen in any window, the whole fetch is aborted with an error.
func (s *BridgeSyncerLite) fetchBridges(ctx context.Context, fromBlock, toBlock uint64) ([]BridgeLeaf, error) {
	if s.client == nil {
		return nil, fmt.Errorf("fetching bridges requires an RPC-backed syncer (set Config.RPCURL)")
	}
	if fromBlock > toBlock {
		return nil, fmt.Errorf("invalid block range: fromBlock %d > toBlock %d", fromBlock, toBlock)
	}

	type window struct{ from, to uint64 }
	var windows []window
	for from := fromBlock; from <= toBlock; from += s.cfg.BlockChunkSize {
		to := min(from+s.cfg.BlockChunkSize-1, toBlock)
		windows = append(windows, window{from, to})
	}

	// Report progress with an ETA while the windows are fetched in parallel. Log an initial line up
	// front (so there's always feedback that the fetch started, even if it finishes before the first
	// tick) and then periodic progress; the summary line is logged once everything is done.
	s.log.Infof("fetching BridgeEvent logs [%d..%d] in %d windows of %d blocks (concurrency %d)...",
		fromBlock, toBlock, len(windows), s.cfg.BlockChunkSize, s.cfg.Concurrency)
	var completed atomic.Int64
	start := time.Now()
	progressCtx, stopProgress := context.WithCancel(ctx)
	defer stopProgress()
	go s.reportFetchProgress(progressCtx, start, &completed, int64(len(windows)), fromBlock, toBlock)

	results := make([][]BridgeLeaf, len(windows))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(s.cfg.Concurrency)
	for i, w := range windows {
		g.Go(func() error {
			leaves, err := s.fetchWindow(gctx, w.from, w.to)
			if err != nil {
				return fmt.Errorf("fetch logs for blocks [%d..%d]: %w", w.from, w.to, err)
			}
			results[i] = leaves
			completed.Add(1)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	stopProgress()

	total := 0
	for _, r := range results {
		total += len(r)
	}
	all := make([]BridgeLeaf, 0, total)
	for _, r := range results {
		all = append(all, r...)
	}
	s.log.Infof("fetched %d BridgeEvent logs from blocks [%d..%d] in %s",
		len(all), fromBlock, toBlock, time.Since(start).Truncate(time.Second))
	return all, nil
}

// reportFetchProgress periodically logs how many block windows have been fetched and an ETA for the
// rest, extrapolating from the average time per completed window. It returns when ctx is cancelled
// (fetchBridges cancels it once g.Wait returns). It stays quiet until at least one window completes
// and never logs once everything is done (the caller logs the final summary).
func (s *BridgeSyncerLite) reportFetchProgress(
	ctx context.Context, start time.Time, completed *atomic.Int64, total int64, fromBlock, toBlock uint64,
) {
	ticker := time.NewTicker(progressLogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			done := completed.Load()
			if done == 0 || done >= total {
				continue
			}
			elapsed := time.Since(start)
			eta := time.Duration(float64(elapsed) / float64(done) * float64(total-done))
			s.log.Infof("fetching BridgeEvent logs [%d..%d]: %d/%d windows (%.1f%%), elapsed %s, ETA %s",
				fromBlock, toBlock, done, total, float64(done)/float64(total)*percentMultiplier,
				elapsed.Truncate(time.Second), eta.Truncate(time.Second))
		}
	}
}

// fetchWindow reads all logs the bridge contract emitted in [from, to] (no topic filter, so a
// single query surfaces both BridgeEvents and any forbidden event), parses the BridgeEvents into
// leaves and aborts on the first forbidden event.
func (s *BridgeSyncerLite) fetchWindow(ctx context.Context, from, to uint64) ([]BridgeLeaf, error) {
	logs, err := s.client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(to),
		Addresses: []common.Address{s.cfg.BridgeAddr},
	})
	if err != nil {
		return nil, err
	}
	return classifyLogs(s.contract, logs, s.cfg.IgnoreUnsupportedL2Events, s.log)
}

// classifyLogs turns a batch of bridge-contract logs into BridgeLeaves: BridgeEvents are parsed and
// kept, and every other event is ignored. A forbidden event aborts with an error unless
// ignoreUnsupported is set, in which case it is logged as a warning and skipped (the reconstructed
// tree may then be incorrect). logger may be nil when ignoreUnsupported is false.
func classifyLogs(
	contract *agglayerbridge.Agglayerbridge, logs []types.Log, ignoreUnsupported bool, logger *log.Logger,
) ([]BridgeLeaf, error) {
	out := make([]BridgeLeaf, 0, len(logs))
	for i := range logs {
		l := logs[i]
		if len(l.Topics) == 0 {
			continue
		}
		topic := l.Topics[0]
		if name, forbidden := forbiddenEventSignatures[topic]; forbidden {
			if !ignoreUnsupported {
				return nil, fmt.Errorf("unsupported %s event detected at block %d (tx %s, log index %d): "+
					"bridge state cannot be reconstructed from BridgeEvent logs alone",
					name, l.BlockNumber, l.TxHash.Hex(), l.Index)
			}
			if logger != nil {
				logger.Warnf("unsupported %s event detected at block %d (tx %s, log index %d); "+
					"ignoring it because ignoreUnsupportedL2Events is set — the reconstructed bridge "+
					"state and NewLocalExitRoot may be incorrect",
					name, l.BlockNumber, l.TxHash.Hex(), l.Index)
			}
			continue
		}
		if topic != bridgeEventSignature {
			// any other event from the bridge contract is irrelevant to the exit tree
			continue
		}
		leaf, err := parseBridgeEvent(contract, l)
		if err != nil {
			return nil, err
		}
		out = append(out, leaf)
	}
	return out, nil
}

// parseBridgeEvent decodes a BridgeEvent log into a BridgeLeaf using the contract binding.
func parseBridgeEvent(contract *agglayerbridge.Agglayerbridge, l types.Log) (BridgeLeaf, error) {
	event, err := contract.ParseBridgeEvent(l)
	if err != nil {
		return BridgeLeaf{}, fmt.Errorf("parse BridgeEvent log (tx %s, log index %d): %w", l.TxHash.Hex(), l.Index, err)
	}
	return BridgeLeaf{
		BlockNum:           l.BlockNumber,
		BlockPos:           uint64(l.Index),
		LeafType:           event.LeafType,
		OriginNetwork:      event.OriginNetwork,
		OriginAddress:      event.OriginAddress,
		DestinationNetwork: event.DestinationNetwork,
		DestinationAddress: event.DestinationAddress,
		Amount:             event.Amount,
		Metadata:           event.Metadata,
		DepositCount:       event.DepositCount,
		TxHash:             l.TxHash,
	}, nil
}
