package exit_certificate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"golang.org/x/sync/errgroup"
)

const (
	// balanceOfSelector is the ERC20 balanceOf(address) function selector.
	balanceOfSelector = "0x70a08231"

	// tokenConcurrency limits how many tokens are scanned in parallel (Step B Phase 3).
	tokenConcurrency = 4

	// abiWordSize is the size of an ABI-encoded word in bytes.
	abiWordSize = 32
)

// RunStepB runs Step B1, B2, and B3 and returns the combined result.
// B1 classifies addresses and collects balances; B2 detects ERC-20 contracts;
// B3 fetches holder breakdowns for the contracts listed in ExtraERC20Contracts.
func RunStepB(ctx context.Context, cfg *Config, targetBlock uint64, stepA *StepAResult) (*StepBResult, error) {
	b1Result, err := RunStepB1(ctx, cfg, targetBlock, stepA)
	if err != nil {
		return nil, err
	}
	eoaAddrs := filterEOAs(stepA.Addresses, b1Result.ContractAddresses)
	b2Result, err := RunStepB2(ctx, cfg, targetBlock, b1Result.ContractAddresses, eoaAddrs, stepA.WrappedTokens)
	if err != nil {
		return nil, err
	}
	b3Result, err := RunStepB3(ctx, cfg, targetBlock, eoaAddrs, b2Result)
	if err != nil {
		return nil, err
	}
	log.Infof("STEP B complete: %d EOAs, %d token accumulations, %d ERC-20 detected, %d ERC-20 holder breakdowns",
		len(b1Result.EOABalances), len(b1Result.Accumulated),
		len(b2Result.DetectedERC20s), len(b3Result.Breakdowns))
	return &StepBResult{
		EOABalances:           b1Result.EOABalances,
		Accumulated:           b1Result.Accumulated,
		ContractAddresses:     b1Result.ContractAddresses,
		DetectedERC20s:        b2Result.DetectedERC20s,
		DiscardedERC20s:       b2Result.DiscardedERC20s,
		ERC20HolderBreakdowns: b3Result.Breakdowns,
	}, nil
}

// RunStepB1 classifies addresses as EOA vs contract, then collects ETH and wrapped
// token balances at targetBlock for all EOAs.
func RunStepB1(ctx context.Context, cfg *Config, targetBlock uint64, stepA *StepAResult) (*StepB1Result, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP B1 — EOA balance checking")
	log.Info("═══════════════════════════════════════════")

	rpcURL := cfg.L2RPCURL
	blockTag := toBlockTag(targetBlock)
	batchSize := cfg.Options.RPCBatchSize
	concurrency := cfg.Options.ConcurrencyLimit

	// Phase 1: classify EOA vs contract
	eoaAddrs, contractAddrs, err := classifyAddresses(ctx, rpcURL, stepA.Addresses, blockTag, batchSize, concurrency)
	if err != nil {
		return nil, fmt.Errorf("classify addresses: %w", err)
	}
	log.Infof("EOAs: %d, Contracts: %d", len(eoaAddrs), len(contractAddrs))

	// Phase 2: fetch ETH balances
	eoaEthBalances, err := fetchETHBalances(ctx, rpcURL, eoaAddrs, blockTag, batchSize, concurrency)
	if err != nil {
		return nil, fmt.Errorf("fetch ETH balances: %w", err)
	}
	log.Infof("ETH: %d EOAs with non-zero balance", len(eoaEthBalances))

	// Phase 3: fetch wrapped token balances (parallel across tokens)
	tokenBalances, err := fetchAllTokenBalances(
		ctx, rpcURL, stepA.WrappedTokens, eoaAddrs, blockTag, batchSize, concurrency,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch token balances: %w", err)
	}

	// Build outputs
	tokenLookup := make(map[common.Address]WrappedToken, len(stepA.WrappedTokens))
	for _, t := range stepA.WrappedTokens {
		tokenLookup[t.WrappedTokenAddress] = t
	}

	eoaBalances := buildEOABalances(eoaAddrs, eoaEthBalances, tokenBalances, tokenLookup)
	accumulated := buildAccumulated(eoaEthBalances, tokenBalances, tokenLookup)

	if err := checkGenesisBalances(
		ctx, rpcURL, eoaAddrs, contractAddrs, eoaEthBalances, blockTag, batchSize, concurrency,
		cfg.Options.GenesisPrefundETHWei,
	); err != nil {
		// A wrong genesisPrefundETHWei declaration is always fatal: Step F would subtract the wrong
		// amount from the native LBT entry. Only the preload itself can be accepted.
		if !cfg.Options.IgnoreGenesisBalance || errors.Is(err, errGenesisPrefundMismatch) {
			return nil, err
		}
		log.Warnf("Genesis balance check failed (ignoreGenesisBalance=true, continuing): %v", err)
	}

	log.Infof("STEP B1 complete: %d EOAs with balances, %d token accumulations",
		len(eoaBalances), len(accumulated))

	return &StepB1Result{
		EOABalances:       eoaBalances,
		Accumulated:       accumulated,
		ContractAddresses: contractAddrs,
	}, nil
}

// filterEOAs returns all addresses in addrs that do not appear in contracts.
func filterEOAs(addrs, contracts []common.Address) []common.Address {
	contractSet := make(map[common.Address]struct{}, len(contracts))
	for _, c := range contracts {
		contractSet[c] = struct{}{}
	}
	eoas := make([]common.Address, 0, len(addrs)-len(contracts))
	for _, a := range addrs {
		if _, isContract := contractSet[a]; !isContract {
			eoas = append(eoas, a)
		}
	}
	return eoas
}

func padLeft(s string, length int) string {
	if len(s) >= length {
		return s
	}
	return fmt.Sprintf("%s%s", string(make([]byte, length-len(s))), s)
}

// sumBalances returns the sum of all values in a map[common.Address]*big.Int.
func sumBalances(balances map[common.Address]*big.Int) *big.Int {
	total := new(big.Int)
	for _, bal := range balances {
		total.Add(total, bal)
	}
	return total
}

// errGenesisPrefundMismatch marks a declared options.genesisPrefundETHWei that does not match the
// detected genesis ETH preload total. It is never suppressed by ignoreGenesisBalance: a wrong
// declaration would make the Step F native-LBT subtraction silently wrong.
var errGenesisPrefundMismatch = errors.New(
	"options.genesisPrefundETHWei does not match the detected genesis ETH preload total")

// checkGenesisBalances fetches ETH balances at block 0 for EOAs and contracts and returns
// an error if any account has a non-zero genesis balance, since that indicates a genesis
// preload that would inflate the exit certificate totals. When declaredPrefundWei is set, it also
// verifies that it equals the detected genesis preload total, returning errGenesisPrefundMismatch
// otherwise.
func checkGenesisBalances(
	ctx context.Context, rpcURL string,
	eoaAddrs, contractAddrs []common.Address,
	eoaEthBalances map[common.Address]*big.Int,
	blockTag string, batchSize, concurrency int,
	declaredPrefundWei string,
) error {
	scBalances, err := fetchETHBalances(ctx, rpcURL, contractAddrs, blockTag, batchSize, concurrency)
	if err != nil {
		return fmt.Errorf("fetch contract ETH balances: %w", err)
	}
	genesisBalances, err := fetchETHBalances(ctx, rpcURL, eoaAddrs, toBlockTag(0), batchSize, concurrency)
	if err != nil {
		return fmt.Errorf("fetch genesis ETH balances: %w", err)
	}
	if err := checkDeclaredGenesisPrefund(declaredPrefundWei, sumBalances(genesisBalances),
		len(genesisBalances)); err != nil {
		return err
	}
	if len(genesisBalances) == 0 {
		return nil
	}
	for addr, bal := range genesisBalances {
		log.Infof("🚨🚨🚨 Genesis ETH preload detected for %s: %s wei", addr.Hex(), bal.String())
	}
	genesisSumStr := sumBalances(genesisBalances).String()
	eoaEthSumStr := sumBalances(eoaEthBalances).String()
	scBalancesStr := sumBalances(scBalances).String()
	totalBalance := new(big.Int).Add(sumBalances(eoaEthBalances), sumBalances(scBalances))
	diffStr := new(big.Int).Sub(totalBalance, sumBalances(genesisBalances)).String()
	maxLen := max(len(genesisSumStr), len(eoaEthSumStr), len(diffStr), len(scBalancesStr))
	log.Infof("Genesis ETH preload total: %s wei (%d accounts)", padLeft(genesisSumStr, maxLen), len(genesisBalances))
	log.Infof("Total EOA ETH            : %s wei (%d accounts)", padLeft(eoaEthSumStr, maxLen), len(eoaEthBalances))
	log.Infof("Total contract ETH       : %s wei (%d accounts)", padLeft(scBalancesStr, maxLen), len(scBalances))
	log.Infof("                           -------------------------------")
	log.Infof("Total genesis subtraction: %s wei (%d accounts)", padLeft(diffStr, maxLen), len(eoaEthBalances))
	return fmt.Errorf(
		"genesis ETH preload detected in %d accounts: pre-funding accounts at genesis is not allowed; "+
			"set options.ignoreGenesisBalance=true to accept the preload and continue",
		len(genesisBalances),
	)
}

// checkDeclaredGenesisPrefund verifies that the declared options.genesisPrefundETHWei (when set)
// equals the detected genesis ETH preload total, so the amount Step F subtracts from the native LBT
// entry is known to be the real preload. genesisSum covers the EOA addresses collected in Step A.
func checkDeclaredGenesisPrefund(declaredWei string, genesisSum *big.Int, accounts int) error {
	if declaredWei == "" {
		return nil
	}
	declared, ok := new(big.Int).SetString(declaredWei, decimalBase)
	if !ok {
		// LoadConfig validates the format, so this is only reachable with a hand-built Config.
		return fmt.Errorf("invalid options.genesisPrefundETHWei %q: must be a base-10 integer amount in Wei",
			declaredWei)
	}
	if declared.Cmp(genesisSum) != 0 {
		return fmt.Errorf("%w: declared %s wei but detected %s wei across %d accounts",
			errGenesisPrefundMismatch, declared, genesisSum, accounts)
	}
	log.Infof("✅ options.genesisPrefundETHWei (%s wei) matches the genesis ETH preload total", declared)
	return nil
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
//
// It fails fast: if any token's balanceOf batch errors, the first error is returned and the
// remaining scans are cancelled. Silently dropping a failed token would leave it absent from the
// balance map, making Step C treat its entire LBT supply as SC-locked and misroute the whole
// supply to exitAddress, excluding the real EOA holders from the certificate.
func fetchAllTokenBalances(
	ctx context.Context, rpcURL string, tokens []WrappedToken,
	eoaAddresses []common.Address, blockTag string, batchSize, concurrency int,
) (map[common.Address]map[common.Address]*big.Int, error) {
	log.Infof("Fetching balances for %d wrapped tokens × %d EOAs...", len(tokens), len(eoaAddresses))

	var mu sync.Mutex
	tokenBalances := make(map[common.Address]map[common.Address]*big.Int)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(tokenConcurrency)

	for _, token := range tokens {
		tok := token
		g.Go(func() error {
			balances, err := fetchTokenBalances(
				gctx, rpcURL, tok.WrappedTokenAddress,
				eoaAddresses, blockTag, batchSize, concurrency,
			)
			if err != nil {
				return fmt.Errorf("fetch balances for token %s: %w", tok.WrappedTokenAddress.Hex(), err)
			}
			if len(balances) > 0 {
				mu.Lock()
				tokenBalances[tok.WrappedTokenAddress] = balances
				mu.Unlock()
				log.Infof("  Token %s...: %d holders", tok.WrappedTokenAddress.Hex()[:12], len(balances))
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return tokenBalances, nil
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

	result = append(result, AccumulatedBalance{
		WrappedTokenAddress: common.Address{},
		TotalBalance:        sumBalances(ethBalances).String(),
	})

	for tokenAddr, holders := range tokenBalances {
		info := tokenLookup[tokenAddr]
		result = append(result, AccumulatedBalance{
			WrappedTokenAddress: tokenAddr,
			OriginNetwork:       info.OriginNetwork,
			OriginTokenAddress:  info.OriginTokenAddress,
			TotalBalance:        sumBalances(holders).String(),
		})
	}

	return result
}
