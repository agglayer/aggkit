package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// RunStepA1 collects all tx hashes from L2 blocks in the configured range.
func RunStepA1(ctx context.Context, cfg *Config) (*StepA1Result, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP A1 — Collect tx hashes")
	log.Info("═══════════════════════════════════════════")

	txHashes, err := collectTxHashes(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("collect tx hashes: %w", err)
	}

	log.Infof("STEP A1 complete: %d tx hashes", len(txHashes))
	return &StepA1Result{TxHashes: txHashes}, nil
}

// RunStepA2 traces the given tx hashes and returns all touched addresses.
func RunStepA2(ctx context.Context, cfg *Config, txHashes []common.Hash) (*StepAResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP A2 — Trace transactions (prestateTracer)")
	log.Info("═══════════════════════════════════════════")

	if len(txHashes) == 0 {
		log.Info("STEP A2 complete: 0 unique addresses (no transactions)")
		return &StepAResult{}, nil
	}

	addresses, err := traceTransactions(ctx, cfg.L2RPCURL, txHashes, cfg.Options.ConcurrencyLimit)
	if err != nil {
		return nil, fmt.Errorf("trace transactions: %w", err)
	}

	log.Infof("STEP A2 complete: %d unique addresses", len(addresses))
	return &StepAResult{Addresses: addresses}, nil
}


func collectTxHashes(ctx context.Context, cfg *Config) ([]common.Hash, error) {
	rpcURL := cfg.L2RPCURL
	batchSize := cfg.Options.RPCBatchSize
	concurrency := cfg.Options.ConcurrencyLimit

	nonEmptyBlocks, err := scanBlockHeaders(ctx, rpcURL, cfg.Options.L2StartBlock, cfg.ResolvedTargetBlock, batchSize, concurrency)
	if err != nil {
		return nil, err
	}
	if len(nonEmptyBlocks) == 0 {
		return nil, nil
	}

	return extractTxHashes(ctx, rpcURL, nonEmptyBlocks, batchSize, concurrency)
}

func scanBlockHeaders(
	ctx context.Context, rpcURL string, startBlock, targetBlock uint64, batchSize, concurrency int,
) ([]uint64, error) {
	totalBlocks := targetBlock - startBlock + 1
	log.Infof("Phase 1: Scanning %d blocks [ %d to %d ] to get blockHeaders (concurrency=%d, batchSize=%d)...",
		totalBlocks, startBlock, targetBlock, concurrency, batchSize)

	headerCalls := make([]RPCCall, totalBlocks)
	for b := startBlock; b <= targetBlock; b++ {
		headerCalls[b-startBlock] = RPCCall{
			Method: "eth_getBlockByNumber",
			Params: []any{toBlockTag(b), false},
		}
	}

	headerResults, err := concurrentBatchRPC(ctx, rpcURL, headerCalls, batchSize, concurrency, "L2 RPC/blockHeaders")
	if err != nil {
		return nil, fmt.Errorf("phase 1 batch RPC: %w", err)
	}

	var nonEmptyBlocks []uint64
	for _, result := range headerResults {
		if result == nil {
			continue
		}
		var block struct {
			Number       string   `json:"number"`
			Transactions []string `json:"transactions"`
		}
		err = json.Unmarshal(result, &block)
		if err != nil {
			log.Warnf("Failed to unmarshal block header: %v", err)
			continue
		}
		if err == nil && len(block.Transactions) > 0 {
			nonEmptyBlocks = append(nonEmptyBlocks, hexToUint64(block.Number))
		}
	}

	log.Infof("Phase 1 complete: %d non-empty blocks out of %d", len(nonEmptyBlocks), totalBlocks)
	return nonEmptyBlocks, nil
}

func extractTxHashes(
	ctx context.Context, rpcURL string, nonEmptyBlocks []uint64, batchSize, concurrency int,
) ([]common.Hash, error) {
	log.Infof("Phase 2: Fetching transactions from %d non-empty blocks...", len(nonEmptyBlocks))

	txCalls := make([]RPCCall, len(nonEmptyBlocks))
	for i, blockNum := range nonEmptyBlocks {
		txCalls[i] = RPCCall{
			Method: "eth_getBlockByNumber",
			Params: []any{toBlockTag(blockNum), true},
		}
	}

	txResults, err := concurrentBatchRPC(ctx, rpcURL, txCalls, batchSize, concurrency, "L2 RPC/blocksWithTxs")
	if err != nil {
		return nil, fmt.Errorf("phase 2 batch RPC: %w", err)
	}

	txHashes := parseTxHashesFromResults(txResults)
	log.Infof("Phase 2 complete: %d tx hashes", len(txHashes))
	return txHashes, nil
}

func parseTxHashesFromResults(results []json.RawMessage) []common.Hash {
	var hashes []common.Hash
	for _, result := range results {
		if result == nil {
			continue
		}
		var block struct {
			Transactions []struct {
				Hash string `json:"hash"`
			} `json:"transactions"`
		}
		if json.Unmarshal(result, &block) != nil {
			continue
		}
		for _, tx := range block.Transactions {
			if tx.Hash != "" {
				hashes = append(hashes, common.HexToHash(tx.Hash))
			}
		}
	}
	return hashes
}

// traceTransactions traces all transactions via a worker pool.
func traceTransactions(
	ctx context.Context, rpcURL string,
	txHashes []common.Hash, concurrency int,
) ([]common.Address, error) {
	totalTx := len(txHashes)
	log.Infof("Phase 3: Tracing %d transactions (concurrency=%d)...", totalTx, concurrency)

	addressSet := make(map[common.Address]struct{})

	err := runWorkerPool(
		txHashes, concurrency,
		func(hash common.Hash) ([]common.Address, error) {
			return traceOneTransaction(ctx, rpcURL, hash)
		},
		func(addrs []common.Address) {
			for _, addr := range addrs {
				addressSet[addr] = struct{}{}
			}
		},
		"Traces",
	)
	if err != nil {
		return nil, fmt.Errorf("phase 3 trace failures: %w", err)
	}

	log.Infof("Phase 3 complete: %d unique addresses from %d traces", len(addressSet), totalTx)

	delete(addressSet, common.Address{})

	addresses := make([]common.Address, 0, len(addressSet))
	for addr := range addressSet {
		addresses = append(addresses, addr)
	}
	sort.Slice(addresses, func(i, j int) bool {
		return strings.ToLower(addresses[i].Hex()) < strings.ToLower(addresses[j].Hex())
	})
	return addresses, nil
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
