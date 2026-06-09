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

// RunStepA runs Step A1 followed by Step A2 and returns the combined result.
// Step A1 collects touched addresses via debug_traceTransaction (prestateTracer + diffMode).
// Step A2 recovers additional addresses from tx receipts for any traces that failed in A1.
func RunStepA(ctx context.Context, cfg *Config, targetBlock uint64) (*StepAResult, error) {
	a1Result, err := RunStepA1(ctx, cfg, targetBlock)
	if err != nil {
		return nil, err
	}
	a2Result, err := RunStepA2(ctx, cfg, a1Result.FailedTraces)
	if err != nil {
		return nil, err
	}
	combined := mergeAddresses(a1Result.Addresses, a2Result.Addresses)
	log.Infof("STEP A complete: %d addresses (A1: %d, A2 new: %d)",
		len(combined), len(a1Result.Addresses), len(combined)-len(a1Result.Addresses))
	return &StepAResult{
		Addresses:    combined,
		FailedTraces: a1Result.FailedTraces,
	}, nil
}

// RunStepA1 collects all touched addresses from genesis to targetBlock using
// debug_traceTransaction with prestateTracer + diffMode.
// Blocks are scanned in windows of Options.StepAWindowSize to bound peak memory usage:
// at most one window of block headers and their tx hashes are in memory at a time.
func RunStepA1(ctx context.Context, cfg *Config, targetBlock uint64) (*StepAResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP A1 — Collect addresses (prestateTracer)")
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
			cfg.Options.ConcurrencyLimit, cfg.Options.IgnoreOnTraceError)
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
		log.Info("STEP A1 complete: 0 unique addresses (no transactions found)")
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
		log.Warnf("STEP A1 complete: %d unique addresses (%d trace failures — run step A2 to recover)",
			len(addresses), len(allFailed))
	} else {
		log.Infof("STEP A1 complete: %d unique addresses", len(addresses))
	}
	return &StepAResult{Addresses: addresses, FailedTraces: allFailed}, nil
}

// RunStepA2 recovers addresses from tx receipts for traces that failed in Step A1.
// For each FailedTrace it calls eth_getTransactionReceipt and extracts all addresses
// found in the receipt: sender (from), recipient (to), created contract, and log emitters.
// Failed receipt fetches are logged as warnings and skipped rather than aborting.
func RunStepA2(ctx context.Context, cfg *Config, failedTraces []FailedTrace) (*StepA2Result, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP A2 — Recover addresses from tx receipts")
	log.Info("═══════════════════════════════════════════")

	if len(failedTraces) == 0 {
		log.Info("STEP A2 complete: no failed traces — nothing to process")
		return &StepA2Result{}, nil
	}

	log.Infof("Processing %d failed traces via eth_getTransactionReceipt...", len(failedTraces))

	hashes := make([]common.Hash, len(failedTraces))
	for i, ft := range failedTraces {
		hashes[i] = ft.Hash
	}

	addrSet := make(map[common.Address]struct{})

	err := runWorkerPool(
		ctx, hashes, cfg.Options.ConcurrencyLimit,
		func(hash common.Hash) ([]common.Address, error) {
			addrs, fetchErr := receiptAddresses(ctx, cfg.L2RPCURL, hash)
			if fetchErr != nil {
				log.Warnf("STEP A2: receipt failed for %s (skipping): %v", hash.Hex(), fetchErr)
				return nil, nil
			}
			return addrs, nil
		},
		func(addrs []common.Address) {
			for _, addr := range addrs {
				addrSet[addr] = struct{}{}
			}
		},
		"Receipts",
	)
	if err != nil {
		return nil, fmt.Errorf("fetch receipts: %w", err)
	}

	delete(addrSet, common.Address{})

	addresses := make([]common.Address, 0, len(addrSet))
	for addr := range addrSet {
		addresses = append(addresses, addr)
	}
	sort.Slice(addresses, func(i, j int) bool {
		return strings.ToLower(addresses[i].Hex()) < strings.ToLower(addresses[j].Hex())
	})

	log.Infof("STEP A2 complete: %d addresses recovered from %d failed traces", len(addresses), len(failedTraces))
	return &StepA2Result{Addresses: addresses}, nil
}

// receiptAddresses fetches eth_getTransactionReceipt for hash and returns all addresses
// found in the receipt: sender (from), recipient (to), created contract, and log emitters.
func receiptAddresses(ctx context.Context, rpcURL string, hash common.Hash) ([]common.Address, error) {
	result, err := singleRPC(ctx, rpcURL, "eth_getTransactionReceipt", []any{hash.Hex()}, defaultRetries)
	if err != nil {
		return nil, fmt.Errorf("receipt %s: %w", hash.Hex(), err)
	}

	if len(result) == 0 || string(result) == "null" {
		return nil, fmt.Errorf("receipt for %s is null", hash.Hex())
	}

	var receipt struct {
		From            string  `json:"from"`
		To              *string `json:"to"`
		ContractAddress *string `json:"contractAddress"`
		Logs            []struct {
			Address string `json:"address"`
		} `json:"logs"`
	}
	if err := json.Unmarshal(result, &receipt); err != nil {
		return nil, fmt.Errorf("unmarshal receipt %s: %w", hash.Hex(), err)
	}

	addrSet := make(map[common.Address]struct{})
	addHex := func(s string) {
		if s == "" || s == "0x" {
			return
		}
		addr := common.HexToAddress(s)
		if addr != (common.Address{}) {
			addrSet[addr] = struct{}{}
		}
	}

	addHex(receipt.From)
	if receipt.To != nil {
		addHex(*receipt.To)
	}
	if receipt.ContractAddress != nil {
		addHex(*receipt.ContractAddress)
	}
	for _, l := range receipt.Logs {
		addHex(l.Address)
	}

	addresses := make([]common.Address, 0, len(addrSet))
	for addr := range addrSet {
		addresses = append(addresses, addr)
	}
	return addresses, nil
}

// mergeAddresses deduplicates and sorts the union of two address slices.
func mergeAddresses(a, b []common.Address) []common.Address {
	seen := make(map[common.Address]struct{}, len(a)+len(b))
	for _, addr := range a {
		seen[addr] = struct{}{}
	}
	for _, addr := range b {
		seen[addr] = struct{}{}
	}
	delete(seen, common.Address{})

	merged := make([]common.Address, 0, len(seen))
	for addr := range seen {
		merged = append(merged, addr)
	}
	sort.Slice(merged, func(i, j int) bool {
		return strings.ToLower(merged[i].Hex()) < strings.ToLower(merged[j].Hex())
	})
	return merged
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
