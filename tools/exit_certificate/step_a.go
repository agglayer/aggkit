package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// RunStepA collects all touched addresses from genesis to targetBlock using
// debug_traceTransaction with prestateTracer + diffMode.
// Blocks are scanned in windows of Options.StepAWindowSize to bound peak memory usage:
// at most one window of block headers and their tx hashes are in memory at a time.
func RunStepA(ctx context.Context, cfg *Config, targetBlock uint64) (*StepAResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP A — Collect addresses (prestateTracer)")
	log.Info("═══════════════════════════════════════════")

	if targetBlock < cfg.Options.L2StartBlock {
		return nil, fmt.Errorf("targetBlock %d is before l2StartBlock %d", targetBlock, cfg.Options.L2StartBlock)
	}

	windowSize := uint64(cfg.Options.StepAWindowSize)
	totalBlocks := targetBlock - cfg.Options.L2StartBlock + 1
	log.Infof("Scanning %d blocks in windows of %d (L2 %d → %d)...",
		totalBlocks, windowSize, cfg.Options.L2StartBlock, targetBlock)

	finalAddrs := make(map[common.Address]struct{})
	var allFailed []FailedTrace
	stepStart := time.Now()

	for start := cfg.Options.L2StartBlock; start <= targetBlock; start += windowSize {
		end := min(start+windowSize-1, targetBlock)

		hashes, err := scanBlockHeaders(ctx, cfg.L2RPCURL, start, end,
			cfg.Options.RPCBatchSize, cfg.Options.ConcurrencyLimit)
		if err != nil {
			return nil, fmt.Errorf("scan blocks [%d-%d]: %w", start, end, err)
		}

		if len(hashes) == 0 {
			continue
		}

		addrs, failed, err := traceTransactions(ctx, cfg.L2RPCURL, hashes,
			cfg.Options.ConcurrencyLimit, cfg.Options.ContinueOnTraceError)
		if err != nil {
			return nil, fmt.Errorf("trace transactions [%d-%d]: %w", start, end, err)
		}

		for _, addr := range addrs {
			finalAddrs[addr] = struct{}{}
		}
		allFailed = append(allFailed, failed...)

		blocksProcessed := end - cfg.Options.L2StartBlock + 1
		elapsed := time.Since(stepStart)
		blocksPerSec := float64(blocksProcessed) / elapsed.Seconds()
		remaining := targetBlock - end
		var eta string
		if blocksPerSec > 0 {
			eta = (time.Duration(float64(remaining)/blocksPerSec) * time.Second).Round(time.Second).String()
		} else {
			eta = "—"
		}
		log.Infof("Progress: %d/%d blocks (%.1f%%) — %.0f blocks/s — ETA %s",
			blocksProcessed, totalBlocks,
			float64(blocksProcessed)/float64(totalBlocks)*percentMultiplier,
			blocksPerSec, eta)
	}

	delete(finalAddrs, common.Address{})

	if len(finalAddrs) == 0 && len(allFailed) == 0 {
		log.Info("STEP A complete: 0 unique addresses (no transactions found)")
		return &StepAResult{}, nil
	}

	addresses := make([]common.Address, 0, len(finalAddrs))
	for addr := range finalAddrs {
		addresses = append(addresses, addr)
	}
	sort.Slice(addresses, func(i, j int) bool {
		return strings.ToLower(addresses[i].Hex()) < strings.ToLower(addresses[j].Hex())
	})

	if len(allFailed) > 0 {
		log.Warnf("STEP A complete: %d unique addresses (%d trace failures skipped)", len(addresses), len(allFailed))
	} else {
		log.Infof("STEP A complete: %d unique addresses", len(addresses))
	}
	return &StepAResult{Addresses: addresses, FailedTraces: allFailed}, nil
}

func scanBlockHeaders(
	ctx context.Context, rpcURL string, startBlock, targetBlock uint64, batchSize, concurrency int,
) ([]common.Hash, error) {
	totalBlocks := targetBlock - startBlock + 1
	log.Infof("Scanning %d blocks [ %d to %d ] for tx hashes (concurrency=%d, batchSize=%d)...",
		totalBlocks, startBlock, targetBlock, concurrency, batchSize)

	calls := make([]RPCCall, totalBlocks)
	for b := startBlock; b <= targetBlock; b++ {
		calls[b-startBlock] = RPCCall{
			Method: "eth_getBlockByNumber",
			Params: []any{toBlockTag(b), false},
		}
	}

	results, err := concurrentBatchRPC(ctx, rpcURL, calls, batchSize, concurrency, "STEP A: L2 RPC/blockHeaders")
	if err != nil {
		return nil, fmt.Errorf("scan block headers: %w", err)
	}

	var hashes []common.Hash
	for _, result := range results {
		if result == nil {
			continue
		}
		var block struct {
			Transactions []string `json:"transactions"`
		}
		if err := json.Unmarshal(result, &block); err != nil {
			log.Warnf("Failed to unmarshal block header: %v", err)
			continue
		}
		for _, h := range block.Transactions {
			hashes = append(hashes, common.HexToHash(h))
		}
	}

	log.Infof("Scan complete: %d tx hashes from %d blocks", len(hashes), totalBlocks)
	return hashes, nil
}

// traceTransactions traces all transactions via a worker pool and returns deduplicated addresses.
// When continueOnError is true, failed traces are collected in failedTraces instead of aborting.
// The returned slice is not sorted; callers are responsible for final ordering.
func traceTransactions(
	ctx context.Context, rpcURL string,
	txHashes []common.Hash, concurrency int, continueOnError bool,
) (addresses []common.Address, failedTraces []FailedTrace, err error) {
	totalTx := len(txHashes)
	log.Infof("Tracing %d transactions (concurrency=%d)...", totalTx, concurrency)

	// When continueOnError=false we cancel the derived context on the first failure so
	// in-flight workers abort their HTTP calls immediately instead of tracing every
	// remaining transaction before the error is returned.
	traceCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	addressSet := make(map[common.Address]struct{})
	var mu sync.Mutex
	var failed []FailedTrace

	// firstTraceErr captures the original failure before context.Canceled errors from
	// aborted workers arrive — ensuring the caller sees a meaningful error message.
	var firstTraceErr error

	poolErr := runWorkerPool(
		traceCtx, txHashes, concurrency,
		func(hash common.Hash) ([]common.Address, error) {
			addrs, traceErr := traceOneTransaction(traceCtx, rpcURL, hash)
			if traceErr != nil {
				if continueOnError {
					mu.Lock()
					failed = append(failed, FailedTrace{Hash: hash, Error: traceErr.Error()})
					mu.Unlock()
					log.Warnf("Trace failed for %s (skipping): %v", hash.Hex(), traceErr)
					return nil, nil
				}
				log.Errorf("Trace failed for %s : %v", hash.Hex(), traceErr)
				mu.Lock()
				if firstTraceErr == nil {
					firstTraceErr = traceErr
				}
				mu.Unlock()
				cancel() // abort in-flight workers
				return addrs, traceErr
			}
			return addrs, nil
		},
		func(addrs []common.Address) {
			for _, addr := range addrs {
				addressSet[addr] = struct{}{}
			}
		},
		"Traces",
	)
	if poolErr != nil {
		if firstTraceErr != nil {
			return nil, nil, fmt.Errorf("trace failures: %w", firstTraceErr)
		}
		return nil, nil, fmt.Errorf("trace failures: %w", poolErr)
	}

	log.Infof("Traced %d txs: %d unique addresses", totalTx, len(addressSet))

	addresses = make([]common.Address, 0, len(addressSet))
	for addr := range addressSet {
		addresses = append(addresses, addr)
	}
	return addresses, failed, nil
}

// traceOneTransaction traces a single transaction with prestateTracer (diffMode)
// and returns all addresses found in the pre and post state.
func traceOneTransaction(ctx context.Context, rpcURL string, txHash common.Hash) ([]common.Address, error) {
	result, err := singleRPC(ctx, rpcURL, "debug_traceTransaction", []any{
		txHash.Hex(),
		map[string]any{
			"tracer":       "prestateTracer",
			"tracerConfig": map[string]any{"diffMode": true},
		},
	}, defaultRetries)
	if err != nil {
		return nil, fmt.Errorf("trace transaction %s: %w", txHash.Hex(), err)
	}

	var trace struct {
		Pre  map[string]any `json:"pre"`
		Post map[string]any `json:"post"`
	}
	if err := json.Unmarshal(result, &trace); err != nil {
		return nil, fmt.Errorf("unmarshal trace for transaction %s: %w", txHash.Hex(), err)
	}

	addrSet := make(map[common.Address]struct{}, len(trace.Pre)+len(trace.Post))
	for addr := range trace.Pre {
		addrSet[common.HexToAddress(addr)] = struct{}{}
	}
	for addr := range trace.Post {
		addrSet[common.HexToAddress(addr)] = struct{}{}
	}

	addresses := make([]common.Address, 0, len(addrSet))
	for addr := range addrSet {
		addresses = append(addresses, addr)
	}
	return addresses, nil
}
