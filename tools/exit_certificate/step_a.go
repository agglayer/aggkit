package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// RunStepA collects all touched addresses from genesis to targetBlock using
// debug_traceTransaction with prestateTracer + diffMode.
func RunStepA(ctx context.Context, cfg *Config) (*StepAResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP A — Collect addresses (prestateTracer)")
	log.Info("═══════════════════════════════════════════")

	txHashes, err := collectTxHashes(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("collect tx hashes: %w", err)
	}
	if len(txHashes) == 0 {
		log.Info("STEP A complete: 0 unique addresses (no transactions found)")
		return &StepAResult{}, nil
	}

	addresses, failedTraces, err := traceTransactions(ctx, cfg.L2RPCURL, txHashes, cfg.Options.ConcurrencyLimit, cfg.Options.ContinueOnTraceError)
	if err != nil {
		return nil, fmt.Errorf("trace transactions: %w", err)
	}

	if len(failedTraces) > 0 {
		log.Warnf("STEP A complete: %d unique addresses (%d trace failures skipped)", len(addresses), len(failedTraces))
	} else {
		log.Infof("STEP A complete: %d unique addresses", len(addresses))
	}
	return &StepAResult{Addresses: addresses, FailedTraces: failedTraces}, nil
}

func collectTxHashes(ctx context.Context, cfg *Config) ([]common.Hash, error) {
	return scanBlockHeaders(ctx, cfg.L2RPCURL, cfg.Options.L2StartBlock, cfg.ResolvedTargetBlock,
		cfg.Options.RPCBatchSize, cfg.Options.ConcurrencyLimit)
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

	results, err := concurrentBatchRPC(ctx, rpcURL, calls, batchSize, concurrency, "L2 RPC/blockHeaders")
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

// traceTransactions traces all transactions via a worker pool.
// When continueOnError is true, failed traces are collected in failedTraces instead of aborting.
func traceTransactions(
	ctx context.Context, rpcURL string,
	txHashes []common.Hash, concurrency int, continueOnError bool,
) (addresses []common.Address, failedTraces []common.Hash, err error) {
	totalTx := len(txHashes)
	log.Infof("Phase 3: Tracing %d transactions (concurrency=%d)...", totalTx, concurrency)

	addressSet := make(map[common.Address]struct{})
	var mu sync.Mutex
	var failed []common.Hash

	poolErr := runWorkerPool(
		txHashes, concurrency,
		func(hash common.Hash) ([]common.Address, error) {
			addrs, traceErr := traceOneTransaction(ctx, rpcURL, hash)
			if traceErr != nil && continueOnError {
				mu.Lock()
				failed = append(failed, hash)
				mu.Unlock()
				log.Warnf("Trace failed for %s (skipping): %v", hash.Hex(), traceErr)
				return nil, nil
			}
			return addrs, traceErr
		},
		func(addrs []common.Address) {
			for _, addr := range addrs {
				addressSet[addr] = struct{}{}
			}
		},
		"Traces",
	)
	if poolErr != nil {
		return nil, nil, fmt.Errorf("phase 3 trace failures: %w", poolErr)
	}

	log.Infof("Phase 3 complete: %d unique addresses from %d traces", len(addressSet), totalTx)

	delete(addressSet, common.Address{})

	addresses = make([]common.Address, 0, len(addressSet))
	for addr := range addressSet {
		addresses = append(addresses, addr)
	}
	sort.Slice(addresses, func(i, j int) bool {
		return strings.ToLower(addresses[i].Hex()) < strings.ToLower(addresses[j].Hex())
	})
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
