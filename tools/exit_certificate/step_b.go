package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

const (
	// balanceOfSelector is the ERC20 balanceOf(address) function selector.
	balanceOfSelector = "0x70a08231"

	// tokenConcurrency limits how many tokens are scanned in parallel (Step B Phase 3).
	tokenConcurrency = 4

	// abiWordSize is the size of an ABI-encoded word in bytes.
	abiWordSize = 32
)

// RunStepB classifies addresses as EOA vs contract, then collects ETH and wrapped
// token balances at targetBlock for all EOAs.
func RunStepB(ctx context.Context, cfg *Config, stepA *StepAResult) (*StepBResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP B — EOA balance checking")
	log.Info("═══════════════════════════════════════════")

	rpcURL := cfg.L2RPCURL
	blockTag := toBlockTag(cfg.ResolvedTargetBlock)
	batchSize := cfg.Options.RPCBatchSize
	concurrency := cfg.Options.ConcurrencyLimit

	// Phase 1: classify EOA vs contract
	eoaAddrs, contractAddrs, err := classifyAddresses(ctx, rpcURL, stepA.Addresses, blockTag, batchSize, concurrency)
	if err != nil {
		return nil, fmt.Errorf("classify addresses: %w", err)
	}
	log.Infof("EOAs: %d, Contracts: %d", len(eoaAddrs), len(contractAddrs))

	// Phase 2: fetch ETH balances
	ethBalances, err := fetchETHBalances(ctx, rpcURL, eoaAddrs, blockTag, batchSize, concurrency)
	if err != nil {
		return nil, fmt.Errorf("fetch ETH balances: %w", err)
	}
	log.Infof("ETH: %d EOAs with non-zero balance", len(ethBalances))

	// Phase 3: fetch wrapped token balances (parallel across tokens)
	tokenBalances := fetchAllTokenBalances(ctx, rpcURL, stepA.WrappedTokens, eoaAddrs, blockTag, batchSize, concurrency)

	// Build outputs
	tokenLookup := make(map[common.Address]WrappedToken, len(stepA.WrappedTokens))
	for _, t := range stepA.WrappedTokens {
		tokenLookup[t.WrappedTokenAddress] = t
	}

	eoaBalances := buildEOABalances(eoaAddrs, ethBalances, tokenBalances, tokenLookup)
	accumulated := buildAccumulated(ethBalances, tokenBalances, tokenLookup)

	log.Infof("STEP B complete: %d EOAs with balances, %d token accumulations",
		len(eoaBalances), len(accumulated))

	return &StepBResult{
		EOABalances:       eoaBalances,
		Accumulated:       accumulated,
		ContractAddresses: contractAddrs,
	}, nil
}

// classifyAddresses separates addresses into EOA and contract via eth_getCode.
func classifyAddresses(
	ctx context.Context, rpcURL string, addresses []common.Address,
	blockTag string, batchSize, concurrency int,
) (eoas, contracts []common.Address, err error) {
	log.Infof("Classifying %d addresses (EOA vs contract)...", len(addresses))

	calls := make([]RPCCall, len(addresses))
	for i, addr := range addresses {
		calls[i] = RPCCall{Method: "eth_getCode", Params: []any{addr.Hex(), blockTag}}
	}

	results, err := concurrentBatchRPC(ctx, rpcURL, calls, batchSize, concurrency, "L2 RPC/getCode")
	if err != nil {
		return nil, nil, fmt.Errorf("batch getCode: %w", err)
	}

	for idx, result := range results {
		addr := addresses[idx]
		if isEOAResult(result) {
			eoas = append(eoas, addr)
		} else {
			contracts = append(contracts, addr)
		}
	}

	log.Infof("  Classification complete: EOAs: %d, Contracts: %d", len(eoas), len(contracts))
	return eoas, contracts, nil
}

// isEOAResult returns true if the eth_getCode result indicates an EOA (no code).
func isEOAResult(result json.RawMessage) bool {
	if result == nil {
		return true
	}
	var code string
	if json.Unmarshal(result, &code) != nil {
		return true
	}
	return code == "" || code == "0x"
}

// fetchETHBalances queries eth_getBalance for all addresses concurrently.
func fetchETHBalances(
	ctx context.Context, rpcURL string, addresses []common.Address,
	blockTag string, batchSize, concurrency int,
) (map[common.Address]*big.Int, error) {
	log.Infof("Fetching ETH balances for %d EOAs...", len(addresses))

	calls := make([]RPCCall, len(addresses))
	for i, addr := range addresses {
		calls[i] = RPCCall{Method: "eth_getBalance", Params: []any{addr.Hex(), blockTag}}
	}

	results, err := concurrentBatchRPC(ctx, rpcURL, calls, batchSize, concurrency, "L2 RPC/getBalance")
	if err != nil {
		return nil, fmt.Errorf("batch getBalance: %w", err)
	}

	balances := make(map[common.Address]*big.Int)
	for idx, result := range results {
		bal := unmarshalHexBigInt(result)
		if bal != nil && bal.Sign() > 0 {
			balances[addresses[idx]] = bal
		}
	}

	log.Infof("  ETH balances complete: %d non-zero", len(balances))
	return balances, nil
}

// fetchAllTokenBalances scans all wrapped tokens in parallel (limited by tokenConcurrency).
func fetchAllTokenBalances(
	ctx context.Context, rpcURL string, tokens []WrappedToken,
	eoaAddresses []common.Address, blockTag string, batchSize, concurrency int,
) map[common.Address]map[common.Address]*big.Int {
	log.Infof("Fetching balances for %d wrapped tokens × %d EOAs...", len(tokens), len(eoaAddresses))

	var mu sync.Mutex
	tokenBalances := make(map[common.Address]map[common.Address]*big.Int)
	sem := make(chan struct{}, tokenConcurrency)

	var wg sync.WaitGroup
	for _, token := range tokens {
		wg.Add(1)
		sem <- struct{}{}
		go func(tok WrappedToken) {
			defer wg.Done()
			defer func() { <-sem }()

			balances, err := fetchTokenBalances(
				ctx, rpcURL, tok.WrappedTokenAddress,
				eoaAddresses, blockTag, batchSize, concurrency,
			)
			if err != nil {
				log.Warnf("Failed to fetch balances for token %s: %v", tok.WrappedTokenAddress.Hex(), err)
				return
			}
			if len(balances) > 0 {
				mu.Lock()
				tokenBalances[tok.WrappedTokenAddress] = balances
				mu.Unlock()
				log.Infof("  Token %s...: %d holders", tok.WrappedTokenAddress.Hex()[:12], len(balances))
			}
		}(token)
	}
	wg.Wait()

	return tokenBalances
}

// fetchTokenBalances queries ERC20 balanceOf for all EOAs for a single token.
func fetchTokenBalances(
	ctx context.Context, rpcURL string, tokenAddr common.Address,
	eoaAddresses []common.Address, blockTag string, batchSize, concurrency int,
) (map[common.Address]*big.Int, error) {
	calls := make([]RPCCall, len(eoaAddresses))
	for i, addr := range eoaAddresses {
		calls[i] = RPCCall{
			Method: "eth_call",
			Params: []any{
				map[string]string{
					"to":   tokenAddr.Hex(),
					"data": encodeBalanceOf(addr),
				},
				blockTag,
			},
		}
	}

	results, err := concurrentBatchRPC(ctx, rpcURL, calls, batchSize, concurrency, "L2 RPC/balanceOf")
	if err != nil {
		return nil, fmt.Errorf("batch balanceOf: %w", err)
	}

	balances := make(map[common.Address]*big.Int)
	for idx, result := range results {
		bal := unmarshalHexBigInt(result)
		if bal != nil && bal.Sign() > 0 {
			balances[eoaAddresses[idx]] = bal
		}
	}
	return balances, nil
}

// encodeBalanceOf ABI-encodes a balanceOf(address) call.
func encodeBalanceOf(addr common.Address) string {
	return balanceOfSelector + common.Bytes2Hex(common.LeftPadBytes(addr.Bytes(), abiWordSize))
}

// unmarshalHexBigInt extracts a *big.Int from a JSON-encoded hex string RPC result.
// Returns nil for absent/empty/zero results.
func unmarshalHexBigInt(result json.RawMessage) *big.Int {
	if result == nil {
		return nil
	}
	var hex string
	if json.Unmarshal(result, &hex) != nil || hex == "" || hex == "0x" {
		return nil
	}
	return hexToBigInt(hex)
}

// buildEOABalances assembles per-address balance records.
func buildEOABalances(
	eoaAddrs []common.Address,
	ethBalances map[common.Address]*big.Int,
	tokenBalances map[common.Address]map[common.Address]*big.Int,
	tokenLookup map[common.Address]WrappedToken,
) []EOABalance {
	var result []EOABalance
	for _, addr := range eoaAddrs {
		if entry, ok := buildSingleEOABalance(addr, ethBalances, tokenBalances, tokenLookup); ok {
			result = append(result, entry)
		}
	}
	return result
}

func buildSingleEOABalance(
	addr common.Address,
	ethBalances map[common.Address]*big.Int,
	tokenBalances map[common.Address]map[common.Address]*big.Int,
	tokenLookup map[common.Address]WrappedToken,
) (EOABalance, bool) {
	entry := EOABalance{Address: addr, ETHBalance: "0"}

	if bal, ok := ethBalances[addr]; ok {
		entry.ETHBalance = bal.String()
	}

	for tokenAddr, holders := range tokenBalances {
		if bal, ok := holders[addr]; ok && bal.Sign() > 0 {
			info := tokenLookup[tokenAddr]
			entry.Tokens = append(entry.Tokens, EOATokenBalance{
				WrappedTokenAddress: tokenAddr,
				OriginNetwork:       info.OriginNetwork,
				OriginTokenAddress:  info.OriginTokenAddress,
				Balance:             bal.String(),
			})
		}
	}

	if entry.ETHBalance == "0" && len(entry.Tokens) == 0 {
		return EOABalance{}, false
	}
	return entry, true
}

// buildAccumulated sums balances per token across all EOAs.
func buildAccumulated(
	ethBalances map[common.Address]*big.Int,
	tokenBalances map[common.Address]map[common.Address]*big.Int,
	tokenLookup map[common.Address]WrappedToken,
) []AccumulatedBalance {
	result := make([]AccumulatedBalance, 0, len(tokenBalances)+1)

	totalETH := new(big.Int)
	for _, bal := range ethBalances {
		totalETH.Add(totalETH, bal)
	}
	result = append(result, AccumulatedBalance{
		WrappedTokenAddress: common.Address{},
		TotalBalance:        totalETH.String(),
	})

	for tokenAddr, holders := range tokenBalances {
		total := new(big.Int)
		for _, bal := range holders {
			total.Add(total, bal)
		}
		info := tokenLookup[tokenAddr]
		result = append(result, AccumulatedBalance{
			WrappedTokenAddress: tokenAddr,
			OriginNetwork:       info.OriginNetwork,
			OriginTokenAddress:  info.OriginTokenAddress,
			TotalBalance:        total.String(),
		})
	}

	return result
}
