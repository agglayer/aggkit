package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// Event topic hashes and function selectors for bridge contract interaction.
var (
	// keccak256("NewWrappedToken(uint32,address,address,bytes)")
	newWrappedTokenTopic = common.HexToHash("0x490e59a1701b938786ac72570a1efeac994a3dbe96e2e883e19e902ace6e6a39")
)

const (
	totalSupplySelector     = "0x18160ddd" // totalSupply()
	gasTokenAddressSelector = "0x3c351e10" // gasTokenAddress()
	gasTokenNetworkSelector = "0x3e197043" // gasTokenNetwork()
	wethTokenSelector       = "0xa25927e2" // WETHToken()
)

// RunStep0 generates the Local Balance Tree (LBT) by scanning the L2 bridge
// for NewWrappedToken events and fetching each token's totalSupply.
// This replaces the external getLBT tool.
func RunStep0(ctx context.Context, cfg *Config) ([]LBTEntry, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP 0 — Generate LBT (Local Balance Tree)")
	log.Info("═══════════════════════════════════════════")

	rpcURL := cfg.L2RPCURL
	bridgeAddr := cfg.L2BridgeAddress
	blockTag := toBlockTag(cfg.ResolvedTargetBlock)

	log.Infof("Bridge address: %s", bridgeAddr.Hex())
	log.Infof("Block number:   %d", cfg.ResolvedTargetBlock)

	// 1. Scan for NewWrappedToken events
	events := fetchNewWrappedTokenEvents(ctx, cfg)
	log.Infof("Found %d NewWrappedToken events", len(events))

	// 2. Fetch totalSupply for each token concurrently
	log.Infof("Fetching totalSupply for %d tokens...", len(events))
	entries, err := fetchTotalSupplies(
		ctx, rpcURL, events, blockTag,
		cfg.Options.RPCBatchSize, cfg.Options.ConcurrencyLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch total supplies: %w", err)
	}

	// 3. Native token unlocked balance
	if nativeEntry, err := computeNativeBalance(ctx, rpcURL, bridgeAddr, blockTag); err != nil {
		log.Warnf("Failed to compute native balance: %v", err)
	} else {
		entries = append(entries, *nativeEntry)
		log.Infof("Native token unlocked balance: %s", nativeEntry.Balance)
	}

	// 4. WETH token (only on chains with a custom gas token)
	if wethEntry, err := fetchWETHBalance(ctx, rpcURL, bridgeAddr, blockTag); err != nil {
		log.Infof("No WETH token on this chain (no custom gas token)")
	} else if wethEntry != nil {
		entries = append(entries, *wethEntry)
		log.Infof("WETH token balance: %s", wethEntry.Balance)
	}

	log.Infof("STEP 0 complete: %d LBT entries", len(entries))
	return entries, nil
}

// wrappedTokenEvent holds parsed NewWrappedToken event data.
type wrappedTokenEvent struct {
	OriginNetwork      uint32
	OriginTokenAddress common.Address
	WrappedTokenAddr   common.Address
}

// fetchNewWrappedTokenEvents scans for NewWrappedToken events via a worker pool.
func fetchNewWrappedTokenEvents(ctx context.Context, cfg *Config) []wrappedTokenEvent {
	blockRange := cfg.Options.BlockRange
	concurrency := cfg.Options.ConcurrencyLimit
	toBlock := cfg.ResolvedTargetBlock

	type blockRangeJob struct{ from, to uint64 }
	var jobs []blockRangeJob
	for start := uint64(0); start <= toBlock; start += uint64(blockRange) {
		end := min(start+uint64(blockRange)-1, toBlock)
		jobs = append(jobs, blockRangeJob{from: start, to: end})
	}

	log.Infof("Fetching NewWrappedToken events: blocks 0→%d, %d ranges, concurrency=%d",
		toBlock, len(jobs), concurrency)

	var allEvents []wrappedTokenEvent

	err := runWorkerPool(
		jobs, concurrency,
		func(j blockRangeJob) ([]wrappedTokenEvent, error) {
			return fetchWrappedTokenEventsInRange(ctx, cfg.L2RPCURL, cfg.L2BridgeAddress, j.from, j.to)
		},
		func(events []wrappedTokenEvent) {
			allEvents = append(allEvents, events...)
		},
		"NewWrappedToken",
	)
	if err != nil {
		log.Warnf("Some NewWrappedToken queries failed: %v", err)
	}

	return allEvents
}

// fetchWrappedTokenEventsInRange fetches NewWrappedToken logs in a single block range.
func fetchWrappedTokenEventsInRange(
	ctx context.Context, rpcURL string, bridgeAddr common.Address,
	fromBlock, toBlock uint64,
) ([]wrappedTokenEvent, error) {
	result, err := singleRPC(ctx, rpcURL, "eth_getLogs", []any{
		map[string]any{
			"address":   bridgeAddr.Hex(),
			"topics":    []string{newWrappedTokenTopic.Hex()},
			"fromBlock": toBlockTag(fromBlock),
			"toBlock":   toBlockTag(toBlock),
		},
	}, defaultRetries)
	if err != nil {
		return nil, err
	}

	var logs []struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(result, &logs); err != nil {
		return nil, fmt.Errorf("unmarshal logs: %w", err)
	}

	events := make([]wrappedTokenEvent, 0, len(logs))
	for _, lg := range logs {
		ev, err := decodeNewWrappedTokenEvent(lg.Data)
		if err != nil {
			log.Warnf("Failed to decode NewWrappedToken event: %v", err)
			continue
		}
		events = append(events, ev)
	}
	return events, nil
}

// decodeNewWrappedTokenEvent decodes ABI-encoded NewWrappedToken event data.
// Layout: originNetwork(uint32) | originTokenAddress(address) | wrappedTokenAddress(address) | metadata(bytes)
func decodeNewWrappedTokenEvent(dataHex string) (wrappedTokenEvent, error) {
	data := common.FromHex(dataHex)
	const minDataLen = 96
	if len(data) < minDataLen {
		return wrappedTokenEvent{}, fmt.Errorf("data too short: %d bytes", len(data))
	}

	originNetwork, err := safeUint32(new(big.Int).SetBytes(data[0:32]))
	if err != nil {
		return wrappedTokenEvent{}, fmt.Errorf("originNetwork: %w", err)
	}

	return wrappedTokenEvent{
		OriginNetwork:      originNetwork,
		OriginTokenAddress: common.BytesToAddress(data[32:64]),
		WrappedTokenAddr:   common.BytesToAddress(data[64:96]),
	}, nil
}

// fetchTotalSupplies queries totalSupply() for each token via concurrentBatchRPC.
func fetchTotalSupplies(
	ctx context.Context, rpcURL string,
	events []wrappedTokenEvent, blockTag string,
	rpcBatchSize, concurrency int,
) ([]LBTEntry, error) {
	if len(events) == 0 {
		return nil, nil
	}

	calls := make([]RPCCall, len(events))
	for i, ev := range events {
		calls[i] = RPCCall{
			Method: "eth_call",
			Params: []any{
				map[string]string{"to": ev.WrappedTokenAddr.Hex(), "data": totalSupplySelector},
				blockTag,
			},
		}
	}

	batchSize := min(max(len(calls)/concurrency, 1), rpcBatchSize)
	results, err := concurrentBatchRPC(ctx, rpcURL, calls, batchSize, concurrency, "L2 RPC/totalSupply")
	if err != nil {
		return nil, err
	}

	entries := make([]LBTEntry, 0, len(events))
	for i, result := range results {
		supply := unmarshalHexBigInt(result)
		if supply == nil {
			supply = new(big.Int)
		}
		entries = append(entries, LBTEntry{
			WrappedTokenAddress: events[i].WrappedTokenAddr,
			OriginNetwork:       events[i].OriginNetwork,
			OriginTokenAddress:  events[i].OriginTokenAddress,
			Balance:             supply.String(),
		})
	}
	return entries, nil
}

// computeNativeBalance computes: balance(bridge, block 0) - balance(bridge, targetBlock).
func computeNativeBalance(
	ctx context.Context, rpcURL string,
	bridgeAddr common.Address, blockTag string,
) (*LBTEntry, error) {
	calls := []RPCCall{
		{Method: "eth_getBalance", Params: []any{bridgeAddr.Hex(), "0x0"}},
		{Method: "eth_getBalance", Params: []any{bridgeAddr.Hex(), blockTag}},
	}

	results, err := batchRPC(ctx, rpcURL, calls, defaultRetries)
	if err != nil {
		return nil, err
	}

	initBalance := unmarshalHexBigInt(results[0])
	if initBalance == nil {
		initBalance = new(big.Int)
	}
	currentBalance := unmarshalHexBigInt(results[1])
	if currentBalance == nil {
		currentBalance = new(big.Int)
	}

	unlocked := new(big.Int).Sub(initBalance, currentBalance)
	if unlocked.Sign() < 0 {
		unlocked = new(big.Int)
	}

	gasTokenNetwork, gasTokenAddress, err := fetchGasTokenInfo(ctx, rpcURL, bridgeAddr, blockTag)
	if err != nil {
		gasTokenNetwork = 0
		gasTokenAddress = common.Address{}
	}

	return &LBTEntry{
		WrappedTokenAddress: common.Address{},
		OriginNetwork:       gasTokenNetwork,
		OriginTokenAddress:  gasTokenAddress,
		Balance:             unlocked.String(),
	}, nil
}

// fetchGasTokenInfo calls gasTokenNetwork() and gasTokenAddress() on the bridge.
func fetchGasTokenInfo(
	ctx context.Context, rpcURL string,
	bridgeAddr common.Address, blockTag string,
) (uint32, common.Address, error) {
	bridgeHex := bridgeAddr.Hex()
	calls := []RPCCall{
		{Method: "eth_call", Params: []any{
			map[string]string{"to": bridgeHex, "data": gasTokenNetworkSelector}, blockTag,
		}},
		{Method: "eth_call", Params: []any{
			map[string]string{"to": bridgeHex, "data": gasTokenAddressSelector}, blockTag,
		}},
	}

	results, err := batchRPC(ctx, rpcURL, calls, defaultRetries)
	if err != nil {
		return 0, common.Address{}, err
	}

	var network uint32
	if n := unmarshalHexBigInt(results[0]); n != nil {
		network = uint32(n.Uint64())
	}

	var addr common.Address
	if results[1] != nil {
		var hex string
		if json.Unmarshal(results[1], &hex) == nil {
			addr = common.HexToAddress(hex)
		}
	}

	return network, addr, nil
}

// fetchWETHBalance calls WETHToken() and fetches its totalSupply if non-zero.
func fetchWETHBalance(
	ctx context.Context, rpcURL string,
	bridgeAddr common.Address, blockTag string,
) (*LBTEntry, error) {
	result, err := singleRPC(ctx, rpcURL, "eth_call", []any{
		map[string]string{"to": bridgeAddr.Hex(), "data": wethTokenSelector},
		blockTag,
	}, defaultRetries)
	if err != nil {
		return nil, err
	}

	var hex string
	if err := json.Unmarshal(result, &hex); err != nil {
		return nil, fmt.Errorf("parse WETH address: %w", err)
	}

	wethAddr := common.HexToAddress(hex)
	if wethAddr == (common.Address{}) {
		return nil, nil
	}

	supplyResult, err := singleRPC(ctx, rpcURL, "eth_call", []any{
		map[string]string{"to": wethAddr.Hex(), "data": totalSupplySelector},
		blockTag,
	}, defaultRetries)
	if err != nil {
		return nil, fmt.Errorf("fetch WETH totalSupply: %w", err)
	}

	supply := unmarshalHexBigInt(supplyResult)
	if supply == nil {
		supply = new(big.Int)
	}

	return &LBTEntry{
		WrappedTokenAddress: wethAddr,
		OriginNetwork:       0,
		OriginTokenAddress:  common.Address{},
		Balance:             supply.String(),
	}, nil
}
