package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Event topic hashes and function selectors for bridge contract interaction.
var (
	// keccak256("NewWrappedToken(uint32,address,address,bytes)")
	newWrappedTokenTopic = common.HexToHash("0x490e59a1701b938786ac72570a1efeac994a3dbe96e2e883e19e902ace6e6a39")
	// keccak256("SetSovereignTokenAddress(uint32,address,address,bool)")
	// Fires when the bridge manager remaps an origin token to a sovereign ERC-20 address,
	// overriding the original wrapped address set by NewWrappedToken.
	setSovereignTokenTopic = common.HexToHash("0xdbe8a5da6a7a916d9adfda9160167a0f8a3da415ee6610e810e753853597fce7")
)

const (
	totalSupplySelector = "0x18160ddd" // totalSupply()
	wethTokenSelector   = "0xa25927e2" // WETHToken()
)

// RunStep0 generates the Local Balance Tree (LBT) by scanning the L2 bridge
// for NewWrappedToken events and fetching each token's totalSupply.
// This replaces the external getLBT tool.
func RunStep0(ctx context.Context, cfg *Config) (*Step0Result, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP 0 — Generate LBT (Local Balance Tree)")
	log.Info("═══════════════════════════════════════════")

	blockNum, err := resolveTargetBlockNumber(ctx, cfg.L2RPCURL, cfg.TargetBlock)
	if err != nil {
		return nil, fmt.Errorf("resolve target block: %w", err)
	}
	if !cfg.TargetBlock.IsConstant() {
		log.Infof("Resolved targetBlock=%q → %d", cfg.TargetBlock.String(), blockNum)
	}

	rpcURL := cfg.L2RPCURL
	bridgeAddr := cfg.L2BridgeAddress

	blockTag := toBlockTag(blockNum)

	log.Infof("Bridge address: %s", bridgeAddr.Hex())
	log.Infof("Block number:   %d", blockNum)

	// 1. Scan for NewWrappedToken events
	events := fetchNewWrappedTokenEvents(ctx, cfg, blockNum)
	log.Infof("Found %d NewWrappedToken events", len(events))

	// 2. Apply SetSovereignTokenAddress overrides: if the bridge manager remapped an origin
	// token to a different ERC-20 after the original NewWrappedToken event, use the sovereign
	// address instead. This keeps the LBT's wrapped addresses consistent with what
	// getTokenWrappedAddress() returns on the live contract.
	events, err = applySovereignTokenOverrides(ctx, cfg, blockNum, events)
	if err != nil {
		return nil, fmt.Errorf("apply sovereign token overrides: %w", err)
	}

	// 3. Fetch totalSupply for each token concurrently
	log.Infof("Fetching totalSupply for %d tokens...", len(events))
	entries, err := fetchTotalSupplies(
		ctx, rpcURL, events, blockTag,
		cfg.Options.RPCBatchSize, cfg.Options.ConcurrencyLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch total supplies: %w", err)
	}

	// 3. Native token unlocked balance
	var nativeEntry *LBTEntry
	if nativeEntry, err = computeNativeBalance(ctx, rpcURL, bridgeAddr, blockTag); err != nil {
		log.Warnf("Failed to compute native balance: %v", err)
	} else {
		entries = append(entries, *nativeEntry)
		log.Infof("Native token unlocked balance: %s", nativeEntry.Balance)
		log.Infof("Native token info - OriginNetwork: %d, OriginTokenAddress: %s",
			nativeEntry.OriginNetwork, nativeEntry.OriginTokenAddress.Hex())
	}
	// 4. WETH token (only on chains with a custom gas token)
	if wethEntry, err := fetchWETHBalance(ctx, rpcURL, bridgeAddr, blockTag); err != nil {
		log.Infof("No WETH token on this chain (no custom gas token)")
	} else if wethEntry != nil {
		entries = append(entries, *wethEntry)
		log.Infof("WETH token %s balance: %s", wethEntry.WrappedTokenAddress.Hex(), wethEntry.Balance)
	}

	log.Infof("STEP 0 complete: %d LBT entries", len(entries))
	return &Step0Result{TargetBlock: blockNum, Entries: entries}, nil
}

// wrappedTokenEvent holds parsed NewWrappedToken event data.
type wrappedTokenEvent struct {
	OriginNetwork      uint32
	OriginTokenAddress common.Address
	WrappedTokenAddr   common.Address
	// LegacyAddrs holds previous wrapped addresses replaced by SetSovereignTokenAddress overrides.
	LegacyAddrs []common.Address
}

// fetchNewWrappedTokenEvents scans for NewWrappedToken events via a worker pool.
func fetchNewWrappedTokenEvents(ctx context.Context, cfg *Config, toBlock uint64) []wrappedTokenEvent {
	blockRange := cfg.Options.BlockRange
	concurrency := cfg.Options.ConcurrencyLimit

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
		ctx, jobs, concurrency,
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

// eventLogEntry is a raw eth_getLogs entry: the data hex plus the log's chain position,
// so callers can restore chronological order after concurrent range scans.
type eventLogEntry struct {
	Data        string
	BlockNumber uint64
	LogIndex    uint64
}

// fetchEventLogsInRange calls eth_getLogs for one topic on bridgeAddr and returns the raw
// data hex strings along with each log's block number and log index.
func fetchEventLogsInRange(
	ctx context.Context, rpcURL string, bridgeAddr common.Address,
	topic common.Hash, fromBlock, toBlock uint64,
) ([]eventLogEntry, error) {
	result, err := singleRPC(ctx, rpcURL, "eth_getLogs", []any{
		map[string]any{
			"address":   bridgeAddr.Hex(),
			"topics":    []string{topic.Hex()},
			"fromBlock": toBlockTag(fromBlock),
			"toBlock":   toBlockTag(toBlock),
		},
	}, defaultRetries)
	if err != nil {
		return nil, err
	}
	var logs []struct {
		Data        string `json:"data"`
		BlockNumber string `json:"blockNumber"`
		LogIndex    string `json:"logIndex"`
	}
	if err := json.Unmarshal(result, &logs); err != nil {
		return nil, fmt.Errorf("unmarshal logs: %w", err)
	}
	entries := make([]eventLogEntry, len(logs))
	for i, lg := range logs {
		entries[i] = eventLogEntry{
			Data:        lg.Data,
			BlockNumber: hexToUint64(lg.BlockNumber),
			LogIndex:    hexToUint64(lg.LogIndex),
		}
	}
	return entries, nil
}

// fetchWrappedTokenEventsInRange fetches NewWrappedToken logs in a single block range.
func fetchWrappedTokenEventsInRange(
	ctx context.Context, rpcURL string, bridgeAddr common.Address,
	fromBlock, toBlock uint64,
) ([]wrappedTokenEvent, error) {
	logData, err := fetchEventLogsInRange(ctx, rpcURL, bridgeAddr, newWrappedTokenTopic, fromBlock, toBlock)
	if err != nil {
		return nil, err
	}
	events := make([]wrappedTokenEvent, 0, len(logData))
	for _, lg := range logData {
		ev, err := decodeNewWrappedTokenEvent(lg.Data)
		if err != nil {
			log.Warnf("Failed to decode NewWrappedToken event: %v", err)
			continue
		}
		events = append(events, ev)
	}
	return events, nil
}

// applySovereignTokenOverrides scans SetSovereignTokenAddress events and updates the wrapped
// token address for any origin tokens that have been remapped by the bridge manager.
// When setSovereignTokenAddress is called on the bridge, getTokenWrappedAddress returns the
// sovereign address instead of the original wrapped one, so the LBT must reflect the same.
func applySovereignTokenOverrides(
	ctx context.Context, cfg *Config, toBlock uint64, events []wrappedTokenEvent,
) ([]wrappedTokenEvent, error) {
	overrides, err := fetchSetSovereignTokenEvents(ctx, cfg, toBlock)
	if err != nil {
		return nil, fmt.Errorf("fetch SetSovereignTokenAddress events: %w", err)
	}
	if len(overrides) == 0 {
		return events, nil
	}

	// Build override history: (originNetwork, originToken) → sovereign addresses in
	// chronological order (overrides are sorted by block/log position). The last entry is the
	// live sovereign address; earlier ones become legacy addresses so downstream steps still
	// query balances held on every historical sovereign token.
	type originKey struct {
		network uint32
		addr    common.Address
	}
	overrideHistory := make(map[originKey][]common.Address, len(overrides))
	for _, ov := range overrides {
		if ov.SovereignAddr != (common.Address{}) {
			k := originKey{ov.OriginNetwork, ov.OriginTokenAddress}
			overrideHistory[k] = append(overrideHistory[k], ov.SovereignAddr)
		}
	}

	// Track which origin tokens we've seen so we can add new entries for tokens that only
	// appear in SetSovereignTokenAddress (no prior NewWrappedToken event).
	seen := make(map[originKey]bool, len(events))
	result := make([]wrappedTokenEvent, 0, len(events))
	for _, ev := range events {
		k := originKey{ev.OriginNetwork, ev.OriginTokenAddress}
		seen[k] = true
		if history, ok := overrideHistory[k]; ok {
			sovereign := history[len(history)-1]
			log.Infof("SetSovereignTokenAddress override for origin(network=%d addr=%s): %s → %s (%d override(s))",
				ev.OriginNetwork, ev.OriginTokenAddress.Hex(), ev.WrappedTokenAddr.Hex(), sovereign.Hex(), len(history))
			ev.LegacyAddrs = mergeLegacyAddrs(ev.LegacyAddrs, ev.WrappedTokenAddr, history)
			ev.WrappedTokenAddr = sovereign
		}
		result = append(result, ev)
	}

	// Add entries for sovereign tokens without a prior NewWrappedToken event.
	for _, ov := range overrides {
		k := originKey{ov.OriginNetwork, ov.OriginTokenAddress}
		if !seen[k] && ov.SovereignAddr != (common.Address{}) {
			history := overrideHistory[k]
			sovereign := history[len(history)-1]
			log.Infof("SetSovereignTokenAddress new entry: origin(network=%d addr=%s) → %s",
				ov.OriginNetwork, ov.OriginTokenAddress.Hex(), sovereign.Hex())
			result = append(result, wrappedTokenEvent{
				OriginNetwork:      ov.OriginNetwork,
				OriginTokenAddress: ov.OriginTokenAddress,
				WrappedTokenAddr:   sovereign,
				LegacyAddrs:        mergeLegacyAddrs(nil, common.Address{}, history),
			})
			seen[k] = true
		}
	}

	return result, nil
}

// mergeLegacyAddrs appends the original wrapped address (when non-zero) and every intermediate
// sovereign address from history to legacy, deduplicated and excluding the live address
// (the last history entry) — a token remapped back to a previous address must not list its
// current address as legacy.
func mergeLegacyAddrs(legacy []common.Address, original common.Address, history []common.Address) []common.Address {
	live := history[len(history)-1]
	present := make(map[common.Address]bool, len(legacy)+len(history))
	for _, a := range legacy {
		present[a] = true
	}
	candidates := history[:len(history)-1]
	if original != (common.Address{}) {
		candidates = append([]common.Address{original}, candidates...)
	}
	for _, a := range candidates {
		if a != live && !present[a] {
			legacy = append(legacy, a)
			present[a] = true
		}
	}
	return legacy
}

// sovereignTokenOverride holds data decoded from a SetSovereignTokenAddress event.
// BlockNumber/LogIndex record the event's chain position: the concurrent range scan collects
// results in completion order, so overrides must be re-sorted chronologically before applying
// them — otherwise a stale remap could win over the latest one (AET-16).
type sovereignTokenOverride struct {
	OriginNetwork      uint32
	OriginTokenAddress common.Address
	SovereignAddr      common.Address
	BlockNumber        uint64
	LogIndex           uint64
}

// fetchSetSovereignTokenEvents scans for SetSovereignTokenAddress events via a worker pool.
func fetchSetSovereignTokenEvents(ctx context.Context, cfg *Config, toBlock uint64) ([]sovereignTokenOverride, error) {
	blockRange := cfg.Options.BlockRange
	concurrency := cfg.Options.ConcurrencyLimit

	type blockRangeJob struct{ from, to uint64 }
	var jobs []blockRangeJob
	for start := uint64(0); start <= toBlock; start += uint64(blockRange) {
		end := min(start+uint64(blockRange)-1, toBlock)
		jobs = append(jobs, blockRangeJob{from: start, to: end})
	}

	var allOverrides []sovereignTokenOverride
	if err := runWorkerPool(
		ctx, jobs, concurrency,
		func(j blockRangeJob) ([]sovereignTokenOverride, error) {
			return fetchSetSovereignTokenEventsInRange(ctx, cfg.L2RPCURL, cfg.L2BridgeAddress, j.from, j.to)
		},
		func(ovs []sovereignTokenOverride) {
			allOverrides = append(allOverrides, ovs...)
		},
		"SetSovereignTokenAddress",
	); err != nil {
		return nil, err
	}

	// The worker pool appends results in completion order; restore chronological (chain) order
	// so a later remap of the same origin token always wins over an earlier one.
	sort.Slice(allOverrides, func(i, j int) bool {
		if allOverrides[i].BlockNumber != allOverrides[j].BlockNumber {
			return allOverrides[i].BlockNumber < allOverrides[j].BlockNumber
		}
		return allOverrides[i].LogIndex < allOverrides[j].LogIndex
	})

	log.Infof("Found %d SetSovereignTokenAddress overrides", len(allOverrides))
	return allOverrides, nil
}

// fetchSetSovereignTokenEventsInRange fetches SetSovereignTokenAddress logs in a single block range.
func fetchSetSovereignTokenEventsInRange(
	ctx context.Context, rpcURL string, bridgeAddr common.Address,
	fromBlock, toBlock uint64,
) ([]sovereignTokenOverride, error) {
	logData, err := fetchEventLogsInRange(ctx, rpcURL, bridgeAddr, setSovereignTokenTopic, fromBlock, toBlock)
	if err != nil {
		return nil, err
	}
	overrides := make([]sovereignTokenOverride, 0, len(logData))
	for _, lg := range logData {
		ov, err := decodeSetSovereignTokenEvent(lg.Data)
		if err != nil {
			log.Warnf("Failed to decode SetSovereignTokenAddress event: %v", err)
			continue
		}
		ov.BlockNumber = lg.BlockNumber
		ov.LogIndex = lg.LogIndex
		overrides = append(overrides, ov)
	}
	return overrides, nil
}

// decodeSetSovereignTokenEvent decodes ABI-encoded SetSovereignTokenAddress event data.
// Layout: originNetwork(uint32) | originTokenAddress(address) | sovereignTokenAddress(address) | isNotMintable(bool)
func decodeSetSovereignTokenEvent(dataHex string) (sovereignTokenOverride, error) {
	data := common.FromHex(dataHex)
	const minDataLen = 96
	if len(data) < minDataLen {
		return sovereignTokenOverride{}, fmt.Errorf("data too short: %d bytes", len(data))
	}

	originNetwork, err := safeUint32(new(big.Int).SetBytes(data[0:32]))
	if err != nil {
		return sovereignTokenOverride{}, fmt.Errorf("originNetwork: %w", err)
	}

	return sovereignTokenOverride{
		OriginNetwork:      originNetwork,
		OriginTokenAddress: common.BytesToAddress(data[32:64]),
		SovereignAddr:      common.BytesToAddress(data[64:96]),
	}, nil
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
// For events that have LegacyAddrs (replaced by SetSovereignTokenAddress), it also fetches
// totalSupply for each legacy address and populates LBTEntry.LegacyTokens.
func fetchTotalSupplies(
	ctx context.Context, rpcURL string,
	events []wrappedTokenEvent, blockTag string,
	rpcBatchSize, concurrency int,
) ([]LBTEntry, error) {
	if len(events) == 0 {
		return nil, nil
	}

	// Build a flat call list: first all current wrapped addresses, then all legacy ones.
	// We record where legacy calls start per event so we can reconstruct the results.
	type legacySlice struct{ start, count int }
	legacyIndex := make([]legacySlice, len(events))
	calls := make([]RPCCall, 0, len(events))
	for _, ev := range events {
		calls = append(calls, RPCCall{
			Method: "eth_call",
			Params: []any{
				map[string]string{"to": ev.WrappedTokenAddr.Hex(), "data": totalSupplySelector},
				blockTag,
			},
		})
	}
	legacyStart := len(calls)
	for i, ev := range events {
		legacyIndex[i] = legacySlice{start: legacyStart, count: len(ev.LegacyAddrs)}
		for _, legacyAddr := range ev.LegacyAddrs {
			calls = append(calls, RPCCall{
				Method: "eth_call",
				Params: []any{
					map[string]string{"to": legacyAddr.Hex(), "data": totalSupplySelector},
					blockTag,
				},
			})
			legacyStart++
		}
	}

	batchSize := min(max(len(calls)/concurrency, 1), rpcBatchSize)
	results, err := concurrentBatchRPC(ctx, rpcURL, calls, batchSize, concurrency, "L2 RPC/totalSupply")
	if err != nil {
		return nil, err
	}

	entries := make([]LBTEntry, 0, len(events))
	for i, result := range results[:len(events)] {
		supply := unmarshalHexBigInt(result)
		if supply == nil {
			supply = new(big.Int)
		}
		entry := LBTEntry{
			WrappedTokenAddress: events[i].WrappedTokenAddr,
			OriginNetwork:       events[i].OriginNetwork,
			OriginTokenAddress:  events[i].OriginTokenAddress,
			Balance:             supply.String(),
		}
		ls := legacyIndex[i]
		for j := 0; j < ls.count; j++ {
			legacySupply := unmarshalHexBigInt(results[ls.start+j])
			if legacySupply == nil {
				legacySupply = new(big.Int)
			}
			entry.LegacyTokens = append(entry.LegacyTokens, LegacyToken{
				Address: events[i].LegacyAddrs[j],
				Balance: legacySupply.String(),
			})
		}
		entries = append(entries, entry)
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

	gasTokenNetwork, gasTokenAddress, err := fetchGasTokenInfo(ctx, rpcURL, bridgeAddr)
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
	bridgeAddr common.Address,
) (uint32, common.Address, error) {
	l2Client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		msg := fmt.Sprintf("dial L2 RPC (%s): %v", rpcURL, err)
		log.Infof("❌ %s", msg)
		return 0, common.Address{}, err
	}
	defer l2Client.Close()
	caller, err := agglayerbridgel2.NewAgglayerbridgel2Caller(bridgeAddr, l2Client)
	if err != nil {
		msg := fmt.Sprintf("create bridge caller (addr=%s): %v", bridgeAddr.Hex(), err)
		log.Infof("❌ %s", msg)
		return 0, common.Address{}, err
	}
	gasTokenNetwork, err := caller.GasTokenNetwork(&bind.CallOpts{Context: ctx})
	if err != nil {
		msg := fmt.Sprintf("query bridge GasTokenNetwork(): %v", err)
		log.Infof("❌ %s", msg)
		return 0, common.Address{}, err
	}
	gasTokenAddr, err := caller.GasTokenAddress(&bind.CallOpts{Context: ctx})
	if err != nil {
		msg := fmt.Sprintf("query bridge GasTokenAddress(): %v", err)
		log.Infof("❌ %s", msg)
		return 0, common.Address{}, err
	}

	return gasTokenNetwork, gasTokenAddr, nil
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

// resolveTargetBlockNumber resolves a BlockNumberFinality to a concrete block number.
// Constant finalities are returned directly; named finalities (latest, finalized, safe,
// pending) and any configured offset are resolved via the L2 RPC.
func resolveTargetBlockNumber(
	ctx context.Context, rpcURL string, finality aggkittypes.BlockNumberFinality,
) (uint64, error) {
	if finality.IsConstant() {
		return finality.Specific, nil
	}
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return 0, fmt.Errorf("dial L2 RPC: %w", err)
	}
	defer client.Close()
	return finality.BlockNumber(ctx, &ethClientAdapter{client})
}

// ethClientAdapter wraps *ethclient.Client to satisfy aggkittypes.CustomEthereumClienter
// for the sole purpose of resolving a BlockNumberFinality to a concrete block number.
type ethClientAdapter struct {
	*ethclient.Client
}

func (a *ethClientAdapter) CustomHeaderByNumber(
	ctx context.Context, number *aggkittypes.BlockNumberFinality,
) (*aggkittypes.BlockHeader, error) {
	bigInt := number.ToBigInt()
	if number.HasOffset() {
		base, err := a.HeaderByNumber(ctx, number.Block.ToBigInt())
		if err != nil {
			return nil, err
		}
		bigInt = new(big.Int).SetUint64(number.CalculateBlockNumber(base.Number.Uint64()))
	}
	header, err := a.HeaderByNumber(ctx, bigInt)
	if err != nil {
		return nil, err
	}
	return &aggkittypes.BlockHeader{Number: header.Number.Uint64()}, nil
}

func (a *ethClientAdapter) RetrieveBlockHeaders(
	_ context.Context, _ []uint64, _ int,
) (*aggkittypes.BlockHeadersResult, error) {
	return nil, fmt.Errorf("not implemented")
}
