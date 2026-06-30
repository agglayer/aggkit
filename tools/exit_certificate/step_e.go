package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var bridgeEventTopic = crypto.Keccak256Hash(
	[]byte("BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)"),
)

// isClaimedSelector is the 4-byte ABI selector for isClaimed(uint32,uint32).
// keccak256("isClaimed(uint32,uint32)")[:4]
const isClaimedSelector = "0xcc461632"

// sourceBridgeNetworkMainnet is the sourceBridgeNetwork value for L1 (mainnet) deposits.
// isClaimed(leafIndex, sourceBridgeNetwork) uses 0 for mainnet.
const sourceBridgeNetworkMainnet = 0

// RunStepE finds unclaimed L1→L2 bridge deposits and reports them.
//
// Approach:
//  1. Scan L1 bridge for BridgeEvent where destinationNetwork == L2 networkId
//  2. For each deposit, call isClaimed(depositCount, 0) on the L2 bridge contract
//  3. Message deposits (leaf_type=1) are saved separately and never added to the certificate.
//  4. Asset deposits (leaf_type=0): if none, the certificate is passed through unchanged.
//     If ignoreUnclaimed=true, detected deposits are logged but the certificate is unchanged.
//     If ignoreUnclaimed=false and any assets are found, the step errors (Merkle proofs not yet implemented).
func RunStepE(
	ctx context.Context, cfg *Config,
	certificate *agglayertypes.Certificate,
) (*StepEResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP E — Unclaimed L1→L2 bridge deposits")
	log.Info("═══════════════════════════════════════════")

	l1LatestBlock, err := resolveL1LatestBlock(ctx, cfg)
	if err != nil {
		return nil, err
	}

	l1Deposits, err := fetchL1BridgeEvents(ctx, cfg, l1LatestBlock)
	if err != nil {
		return nil, err
	}
	log.Infof("L1→L2 deposits found: %d", len(l1Deposits))

	claimedSet, err := checkClaimedBatch(ctx, cfg, l1Deposits)
	if err != nil {
		return nil, fmt.Errorf("check isClaimed: %w", err)
	}
	log.Infof("Already claimed on L2: %d", len(claimedSet))

	unclaimed := filterUnclaimedDeposits(l1Deposits, claimedSet)
	unclaimedAssets, unclaimedMessages := splitByLeafType(unclaimed)
	log.Infof("Unclaimed L1→L2 deposits: %d  (asset=%d, messages=%d)",
		len(unclaimed), len(unclaimedAssets), len(unclaimedMessages))

	if cfg.Options.BridgeServiceURL != "" {
		log.Infof("step E: checking pending bridges from bridge service %s", cfg.Options.BridgeServiceURL)
		if err := checkBridgeServicePendingBridges(ctx, cfg, unclaimedAssets); err != nil {
			return nil, fmt.Errorf("bridge service pending bridges check: %w", err)
		}
	} else {
		log.Info("Bridge service URL not configured — skipping bridge service pending bridges check")
	}

	if len(unclaimedMessages) > 0 {
		log.Infof("⚠️ Unclaimed message deposits (leaf_type=1, excluded from certificate): %d", len(unclaimedMessages))
	} else {
		log.Info("✅ No unclaimed message deposits found")
	}
	logUnclaimedAssetSummary(ctx, cfg, unclaimedAssets)

	if len(unclaimedAssets) == 0 {
		log.Info("STEP E complete (no unclaimed asset deposits)")
		return &StepEResult{
			UnclaimedBridges:  unclaimedAssets,
			UnclaimedMessages: unclaimedMessages,
			FinalCertificate:  certificate,
		}, nil
	}
	if cfg.Options.IgnoreUnclaimed {
		log.Info("STEP E complete (certificate unchanged) ignored unclaimed deposits")
		return &StepEResult{
			UnclaimedBridges:  unclaimedAssets,
			UnclaimedMessages: unclaimedMessages,
			FinalCertificate:  certificate,
		}, nil
	}

	return &StepEResult{
			UnclaimedBridges:  unclaimedAssets,
			UnclaimedMessages: unclaimedMessages,
			FinalCertificate:  nil,
		}, fmt.Errorf(
			"unclaimed deposits not supported, require to implement merkle proofs "+
				"(disable with options.ignoreUnclaimed=true or claim the deposits on L2): %d unclaimed asset deposit(s)",
			len(unclaimedAssets),
		)
}

func resolveL1LatestBlock(ctx context.Context, cfg *Config) (uint64, error) {
	latestResult, err := singleRPC(ctx, cfg.L1RPCURL, "eth_blockNumber", nil, defaultRetries)
	if err != nil {
		return 0, fmt.Errorf("get L1 latest block: %w", err)
	}
	var latestHex string
	if err := json.Unmarshal(latestResult, &latestHex); err != nil {
		return 0, fmt.Errorf("parse L1 latest block: %w", err)
	}
	block := hexToUint64(latestHex)
	log.Infof("L1 latest block: %d, scanning from %d", block, cfg.Options.L1StartBlock)
	return block, nil
}

// checkClaimedBatch calls isClaimed(depositCount, 0) on the L2 bridge for each deposit.
//
// isClaimed inputs:
//   - leafIndex = depositCount from the BridgeEvent
//   - sourceBridgeNetwork = 0 (mainnet), because the deposit originates from L1
//
// The contract internally computes:
//
//	globalIndex = leafIndex + sourceBridgeNetwork * 2^32
//
// With sourceBridgeNetwork=0 this simplifies to globalIndex = leafIndex.
func checkClaimedBatch(
	ctx context.Context, cfg *Config, deposits []L1Deposit,
) (map[uint32]struct{}, error) {
	if len(deposits) == 0 {
		return nil, nil
	}

	calls := make([]RPCCall, len(deposits))
	for i, dep := range deposits {
		calls[i] = RPCCall{
			Method: "eth_call",
			Params: []any{
				map[string]string{
					"to":   cfg.L2BridgeAddress.Hex(),
					"data": encodeIsClaimed(dep.DepositCount, sourceBridgeNetworkMainnet),
				},
				"latest",
			},
		}
	}

	results, err := concurrentBatchRPC(
		ctx, cfg.L2RPCURL, calls, cfg.Options.RPCBatchSize, cfg.Options.ConcurrencyLimit,
		"L2 RPC/isClaimed",
	)
	if err != nil {
		return nil, fmt.Errorf("batch isClaimed: %w", err)
	}

	return parseClaimedResults(results, deposits), nil
}

// encodeIsClaimed ABI-encodes isClaimed(uint32 leafIndex, uint32 sourceBridgeNetwork).
func encodeIsClaimed(leafIndex, sourceBridgeNetwork uint32) string {
	data := make([]byte, 4+64) //nolint:mnd
	copy(data[0:4], common.FromHex(isClaimedSelector))
	new(big.Int).SetUint64(uint64(leafIndex)).FillBytes(data[4:36])
	new(big.Int).SetUint64(uint64(sourceBridgeNetwork)).FillBytes(data[36:68])
	return "0x" + common.Bytes2Hex(data)
}

func parseClaimedResults(results []json.RawMessage, deposits []L1Deposit) map[uint32]struct{} {
	claimed := make(map[uint32]struct{})
	for i, result := range results {
		if result == nil {
			continue
		}
		var hex string
		if json.Unmarshal(result, &hex) != nil {
			continue
		}
		val := hexToBigInt(hex)
		if val.Sign() > 0 {
			claimed[deposits[i].DepositCount] = struct{}{}
		}
	}
	return claimed
}

func filterUnclaimedDeposits(
	l1Deposits []L1Deposit, claimedSet map[uint32]struct{},
) []L1Deposit {
	var unclaimed []L1Deposit
	for _, dep := range l1Deposits {
		if _, ok := claimedSet[dep.DepositCount]; !ok {
			unclaimed = append(unclaimed, dep)
		}
	}
	return unclaimed
}

// splitByLeafType partitions deposits into assets (leaf_type=0) and messages (leaf_type=1).
func splitByLeafType(deposits []L1Deposit) (assets, messages []L1Deposit) {
	for _, dep := range deposits {
		if bridgetypes.LeafType(dep.LeafType) == bridgetypes.LeafTypeMessage {
			messages = append(messages, dep)
		} else {
			assets = append(assets, dep)
		}
	}
	return
}

// logUnclaimedAssetSummary logs a single summary line plus one line per token group
// showing the total amount. Token names are fetched from the origin-network RPC.
// Native tokens (zero address) are displayed with amounts converted from wei to ETH.
func logUnclaimedAssetSummary(ctx context.Context, cfg *Config, assets []L1Deposit) {
	if len(assets) == 0 {
		return
	}

	type tokenKey struct {
		originNetwork uint32
		originAddress common.Address
	}

	totals := make(map[tokenKey]*big.Int)
	for _, dep := range assets {
		key := tokenKey{dep.OriginNetwork, dep.OriginAddress}
		if totals[key] == nil {
			totals[key] = new(big.Int)
		}
		if dep.Amount != nil {
			totals[key].Add(totals[key], dep.Amount)
		}
	}

	// Sort keys for deterministic output: by network then address.
	keys := make([]tokenKey, 0, len(totals))
	for k := range totals {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].originNetwork != keys[j].originNetwork {
			return keys[i].originNetwork < keys[j].originNetwork
		}
		return keys[i].originAddress.Hex() < keys[j].originAddress.Hex()
	})

	log.Warnf("⚠️  %d unclaimed asset deposit(s):", len(assets))
	for _, key := range keys {
		total := totals[key]
		name, decimals := fetchTokenInfo(ctx, cfg, key.originNetwork, key.originAddress)
		log.Infof("    %s (network=%d): %s (raw %s)",
			name, key.originNetwork, formatTokenAmount(total, decimals), total.String())
	}
}

// fetchTokenInfo returns the token name and decimals for a given origin token.
// For native tokens (zero address) it returns ("ETH", 18) without any RPC call.
// For ERC-20s it calls name() and decimals() using the appropriate RPC URL.
func fetchTokenInfo(
	ctx context.Context, cfg *Config, originNetwork uint32, originAddress common.Address,
) (name string, decimals uint8) {
	if originAddress == (common.Address{}) {
		if originNetwork == 0 {
			return "ETH", ethDecimals
		}
		return fmt.Sprintf("native(net=%d)", originNetwork), ethDecimals
	}

	var rpcURL string
	switch originNetwork {
	case 0:
		rpcURL = cfg.L1RPCURL
	case cfg.L2NetworkID:
		rpcURL = cfg.L2RPCURL
	}

	shortAddr := originAddress.Hex()[:10] + "…"

	if rpcURL == "" {
		return shortAddr, 0
	}

	name = fetchTokenName(ctx, rpcURL, originAddress)
	if name == "" {
		name = shortAddr
	}
	decimals = fetchTokenDecimals(ctx, rpcURL, originAddress)
	return name, decimals
}

const (
	abiSelectorName     = "0x06fdde03" // keccak256("name()")[:4]
	abiSelectorDecimals = "0x313ce567" // keccak256("decimals()")[:4]
)

func fetchTokenName(ctx context.Context, rpcURL string, addr common.Address) string {
	result, err := singleRPC(ctx, rpcURL, "eth_call", []any{
		map[string]string{"to": addr.Hex(), "data": abiSelectorName},
		"latest",
	}, defaultRetries)
	if err != nil {
		return ""
	}
	var hex string
	if json.Unmarshal(result, &hex) != nil {
		return ""
	}
	return decodeABIString(common.FromHex(hex))
}

func fetchTokenDecimals(ctx context.Context, rpcURL string, addr common.Address) uint8 {
	result, err := singleRPC(ctx, rpcURL, "eth_call", []any{
		map[string]string{"to": addr.Hex(), "data": abiSelectorDecimals},
		"latest",
	}, defaultRetries)
	if err != nil {
		return 0
	}
	var hex string
	if json.Unmarshal(result, &hex) != nil {
		return 0
	}
	data := common.FromHex(hex)
	if len(data) < abiWordBytes {
		return 0
	}
	d, err := safeUint8(new(big.Int).SetBytes(data[len(data)-abiWordBytes:]))
	if err != nil {
		return 0
	}
	return d
}

// decodeABIString decodes an ABI-encoded string return value (offset + length + data).
func decodeABIString(data []byte) string {
	// Layout: 32-byte offset | 32-byte length | UTF-8 bytes
	if len(data) < twoABIWords {
		return ""
	}
	strLen := new(big.Int).SetBytes(data[32:64]).Uint64()
	if 64+strLen > uint64(len(data)) {
		return ""
	}
	return string(data[64 : 64+strLen])
}

// formatTokenAmount formats an amount using the token's decimals.
// The fractional part is shown with full precision (trailing zeros stripped).
// If decimals == 0 the raw integer is shown.
func formatTokenAmount(amount *big.Int, decimals uint8) string {
	if amount == nil {
		return "0"
	}
	if decimals == 0 {
		return amount.String() + " (raw)"
	}
	divisor := new(big.Int).Exp(big.NewInt(decimalBase), big.NewInt(int64(decimals)), nil)
	whole := new(big.Int).Quo(amount, divisor)
	remainder := new(big.Int).Mod(amount, divisor)

	if remainder.Sign() == 0 {
		return whole.String()
	}
	// Pad remainder with leading zeros to fill all decimal places, then strip trailing zeros.
	frac := remainder.String()
	if len(frac) < int(decimals) {
		frac = strings.Repeat("0", int(decimals)-len(frac)) + frac
	}
	frac = strings.TrimRight(frac, "0")
	return whole.String() + "." + frac
}

func mergeCertificate(
	certificate *agglayertypes.Certificate,
	newExits []*agglayertypes.BridgeExit,
	newImportedExits []*agglayertypes.ImportedBridgeExit,
) *agglayertypes.Certificate {
	allExits := make([]*agglayertypes.BridgeExit, 0,
		len(certificate.BridgeExits)+len(newExits))
	allExits = append(allExits, certificate.BridgeExits...)
	allExits = append(allExits, newExits...)

	allImported := make([]*agglayertypes.ImportedBridgeExit, 0,
		len(certificate.ImportedBridgeExits)+len(newImportedExits))
	allImported = append(allImported, certificate.ImportedBridgeExits...)
	allImported = append(allImported, newImportedExits...)

	return &agglayertypes.Certificate{
		NetworkID:           certificate.NetworkID,
		Height:              certificate.Height,
		PrevLocalExitRoot:   certificate.PrevLocalExitRoot,
		NewLocalExitRoot:    certificate.NewLocalExitRoot,
		BridgeExits:         allExits,
		ImportedBridgeExits: allImported,
	}
}

const (
	bridgeSvcPageSize = 1000
	// BridgeServiceTypeAggkit selects the aggkit bridge service API (/bridge/v1/bridges).
	BridgeServiceTypeAggkit = "aggkit"
	// BridgeServiceTypeZkevm selects the zkevm-bridge-service API (/pending-bridges).
	BridgeServiceTypeZkevm = "zkevm"
	// leafTypeAsset is the leaf_type value for asset (ERC-20 / native) bridge deposits.
	leafTypeAsset uint32 = 0
)

// checkBridgeServicePendingBridges fetches the pending-bridges set from the configured bridge
// service (aggkit or zkevm) and compares it against the unclaimed deposits found on L1.
func checkBridgeServicePendingBridges(ctx context.Context, cfg *Config, unclaimed []L1Deposit) error {
	baseURL := strings.TrimRight(cfg.Options.BridgeServiceURL, "/")

	var label string
	var svcCounts map[uint32]struct{}

	switch cfg.Options.BridgeServiceType {
	case BridgeServiceTypeZkevm:
		label = "zkevm bridge service"
		log.Infof("Querying zkevm bridge service for pending bridges (url=%s, l2NetworkID=%d)", baseURL, cfg.L2NetworkID)
		var fetchErr error
		svcCounts, fetchErr = fetchZkevmPendingBridges(ctx, baseURL, cfg.L2NetworkID, leafTypeAsset)
		if fetchErr != nil {
			return fetchErr
		}
	default:
		label = "aggkit bridge service"
		log.Infof("Querying aggkit bridge service for pending bridges (url=%s, l2NetworkID=%d)", baseURL, cfg.L2NetworkID)
		var fetchErr error
		svcCounts, fetchErr = fetchAggkitPendingBridges(ctx, cfg, baseURL, leafTypeAsset)
		if fetchErr != nil {
			return fetchErr
		}
	}

	return reportPendingDiscrepancies(label, unclaimed, svcCounts)
}

// reportPendingDiscrepancies compares the set of deposit counts reported by the bridge service
// against the set from the L1 scan and returns an error describing any differences.
func reportPendingDiscrepancies(label string, unclaimed []L1Deposit, svcCounts map[uint32]struct{}) error {
	scanSet := make(map[uint32]struct{}, len(unclaimed))
	for _, dep := range unclaimed {
		scanSet[dep.DepositCount] = struct{}{}
	}

	var inSvcOnly, inScanOnly []uint32
	for dc := range svcCounts {
		if _, ok := scanSet[dc]; !ok {
			inSvcOnly = append(inSvcOnly, dc)
		}
	}
	for dc := range scanSet {
		if _, ok := svcCounts[dc]; !ok {
			inScanOnly = append(inScanOnly, dc)
		}
	}

	if len(inSvcOnly) == 0 && len(inScanOnly) == 0 {
		log.Infof("%s pending bridges match L1 scan (%d unclaimed deposit(s))", label, len(unclaimed))
		return nil
	}

	sort.Slice(inSvcOnly, func(i, j int) bool { return inSvcOnly[i] < inSvcOnly[j] })
	sort.Slice(inScanOnly, func(i, j int) bool { return inScanOnly[i] < inScanOnly[j] })

	var parts []string
	if len(inSvcOnly) > 0 {
		parts = append(parts,
			fmt.Sprintf("%s reports %d deposit(s) not found by L1 scan: depositCounts=%v",
				label, len(inSvcOnly), inSvcOnly))
	}
	if len(inScanOnly) > 0 {
		parts = append(parts,
			fmt.Sprintf("L1 scan found %d deposit(s) not reported by %s: depositCounts=%v",
				len(inScanOnly), label, inScanOnly))
	}
	return fmt.Errorf("bridge service pending bridges mismatch: %s", strings.Join(parts, "; "))
}

// ── aggkit bridge service ────────────────────────────────────────────────────

// aggkitBridgeEntry is a minimal bridge event from the aggkit bridge service REST API.
type aggkitBridgeEntry struct {
	LeafType           uint8  `json:"leaf_type"`
	OriginNetwork      uint32 `json:"origin_network"`
	OriginAddress      string `json:"origin_address"`
	DestinationNetwork uint32 `json:"destination_network"`
	DestinationAddress string `json:"destination_address"`
	Amount             string `json:"amount"`
	Metadata           string `json:"metadata"`
	DepositCount       uint32 `json:"deposit_count"`
	TxHash             string `json:"tx_hash"`
	BlockNum           uint64 `json:"block_num"`
}

type aggkitBridgesResult struct {
	Bridges []*aggkitBridgeEntry `json:"bridges"`
	Count   int                  `json:"count"`
}

// fetchAggkitPendingBridges fetches unclaimed deposits from the aggkit bridge service
// (GET /bridge/v1/bridges?network_id=0&leaf_type=<leafType> + isClaimed check) and returns the set of deposit counts.
func fetchAggkitPendingBridges(
	ctx context.Context, cfg *Config, baseURL string, leafType uint32,
) (map[uint32]struct{}, error) {
	var matching []*aggkitBridgeEntry
	for page := 1; ; page++ {
		reqURL := fmt.Sprintf("%s/bridge/v1/bridges?network_id=0&leaf_type=%d&page_number=%d&page_size=%d",
			baseURL, leafType, page, bridgeSvcPageSize)

		body, err := httpGetJSON(ctx, reqURL)
		if err != nil {
			return nil, fmt.Errorf("aggkit bridge service page %d: %w", page, err)
		}

		var result aggkitBridgesResult
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse aggkit bridge service response page %d: %w", page, err)
		}

		for _, b := range result.Bridges {
			if b.DestinationNetwork == cfg.L2NetworkID {
				matching = append(matching, b)
			}
		}
		log.Infof("Aggkit bridge service page %d: %d entries, %d targeting L2", page, len(result.Bridges), len(matching))

		if len(result.Bridges) < bridgeSvcPageSize {
			break
		}
	}

	deposits := make([]L1Deposit, len(matching))
	for i, b := range matching {
		deposits[i] = L1Deposit{DepositCount: b.DepositCount}
	}
	claimedSet, err := checkClaimedBatch(ctx, cfg, deposits)
	if err != nil {
		return nil, fmt.Errorf("isClaimed check for aggkit bridge service entries: %w", err)
	}

	svcCounts := make(map[uint32]struct{})
	for _, b := range matching {
		if _, ok := claimedSet[b.DepositCount]; !ok {
			svcCounts[b.DepositCount] = struct{}{}
		}
	}

	return svcCounts, nil
}

// ── zkevm bridge service ─────────────────────────────────────────────────────

// zkevmDeposit matches the JSON-encoded Deposit message returned by the zkevm-bridge-service
// gRPC gateway (field names are lowerCamelCase per protobuf JSON encoding).
type zkevmDeposit struct {
	LeafType      uint32 `json:"leaf_type"`
	OrigNet       uint32 `json:"orig_net"`
	OrigAddr      string `json:"orig_addr"`
	Amount        string `json:"amount"`
	DestNet       uint32 `json:"dest_net"`
	DestAddr      string `json:"dest_addr"`
	BlockNum      string `json:"block_num"`
	DepositCnt    uint32 `json:"deposit_cnt"`
	NetworkID     uint32 `json:"network_id"`
	TxHash        string `json:"tx_hash"`
	ClaimTxHash   string `json:"claim_tx_hash"`
	Metadata      string `json:"metadata"`
	ReadyForClaim bool   `json:"ready_for_claim"`
	GlobalIndex   string `json:"global_index"`
}

type zkevmPendingBridgesResponse struct {
	Deposits []*zkevmDeposit `json:"deposits"`
	TotalCnt string          `json:"total_cnt"`
}

// checkZkevmPendingBridges fetches pending (unclaimed, ready-to-claim) deposits from the
// zkevm-bridge-service (GET /pending-bridges, both leaf types) and compares against the L1 scan.
// fetchZkevmPendingBridges pages through GET /pending-bridges for the given destNet and leafType
// and returns the set of deposit counts reported as pending by the zkevm bridge service.
func fetchZkevmPendingBridges(
	ctx context.Context, baseURL string, destNet, leafType uint32,
) (map[uint32]struct{}, error) {
	svcCounts := make(map[uint32]struct{})

	var offset uint32
	for {
		reqURL := fmt.Sprintf("%s/pending-bridges?dest_net=%d&leaf_type=%d&limit=%d&offset=%d",
			baseURL, destNet, leafType, bridgeSvcPageSize, offset)

		body, err := httpGetJSON(ctx, reqURL)
		if err != nil {
			return nil, fmt.Errorf("zkevm bridge service (leaf_type=%d, offset=%d): %w", leafType, offset, err)
		}
		var result zkevmPendingBridgesResponse
		if err := json.Unmarshal(body, &result); err != nil {
			log.Infof("Response body: %s", string(body))
			return nil, fmt.Errorf("parse zkevm bridge service response (leaf_type=%d): %w", leafType, err)
		}
		totalCnt, err := strconv.ParseUint(result.TotalCnt, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse total_cnt %q (leaf_type=%d): %w", result.TotalCnt, leafType, err)
		}

		for _, d := range result.Deposits {
			svcCounts[d.DepositCnt] = struct{}{}
		}
		log.Infof("Zkevm bridge service leaf_type=%d offset=%d: %d/%d deposits",
			leafType, offset, len(result.Deposits), totalCnt)

		offset += uint32(len(result.Deposits))
		if len(result.Deposits) == 0 || uint64(offset) >= totalCnt {
			break
		}
	}

	return svcCounts, nil
}

// fetchL1BridgeEvents scans L1 for BridgeEvents using a worker pool.
func fetchL1BridgeEvents(
	ctx context.Context, cfg *Config, l1LatestBlock uint64,
) ([]L1Deposit, error) {
	fromBlock := cfg.Options.L1StartBlock
	blockRange := cfg.Options.BlockRange
	concurrency := cfg.Options.ConcurrencyLimit

	if l1LatestBlock < fromBlock {
		return nil, nil
	}

	type blockRangeJob struct{ from, to uint64 }
	var jobs []blockRangeJob
	for start := fromBlock; start <= l1LatestBlock; start += uint64(blockRange) {
		end := min(start+uint64(blockRange)-1, l1LatestBlock)
		jobs = append(jobs, blockRangeJob{from: start, to: end})
	}

	log.Infof("Fetching L1 BridgeEvents: blocks %d→%d, %d ranges, concurrency=%d",
		fromBlock, l1LatestBlock, len(jobs), concurrency)

	var allDeposits []L1Deposit

	err := runWorkerPool(
		ctx, jobs, concurrency,
		func(j blockRangeJob) ([]L1Deposit, error) {
			return fetchBridgeEventsInRange(
				ctx, cfg.L1RPCURL, cfg.L1BridgeAddress, cfg.L2NetworkID, j.from, j.to,
			)
		},
		func(deposits []L1Deposit) {
			allDeposits = append(allDeposits, deposits...)
		},
		"L1 BridgeEvent",
	)
	if err != nil {
		return nil, fmt.Errorf("L1 BridgeEvent scan: %w", err)
	}

	log.Infof("L1 BridgeEvent: %d events found", len(allDeposits))
	return allDeposits, nil
}

// fetchBridgeEventsInRange fetches BridgeEvent logs in a single block range.
func fetchBridgeEventsInRange(
	ctx context.Context, rpcURL string, bridgeAddress common.Address,
	l2NetworkID uint32, fromBlock, toBlock uint64,
) ([]L1Deposit, error) {
	result, err := singleRPC(ctx, rpcURL, "eth_getLogs", []any{
		map[string]any{
			"address":   bridgeAddress.Hex(),
			"topics":    []string{bridgeEventTopic.Hex()},
			"fromBlock": toBlockTag(fromBlock),
			"toBlock":   toBlockTag(toBlock),
		},
	}, defaultRetries)
	if err != nil {
		return nil, err
	}

	var logs []struct {
		Data            string `json:"data"`
		BlockNumber     string `json:"blockNumber"`
		TransactionHash string `json:"transactionHash"`
	}
	if err := json.Unmarshal(result, &logs); err != nil {
		return nil, fmt.Errorf("unmarshal logs: %w", err)
	}

	var deposits []L1Deposit
	for _, lg := range logs {
		dep, err := decodeBridgeEvent(lg.Data, lg.BlockNumber, lg.TransactionHash)
		if err != nil {
			continue
		}
		if dep.DestinationNetwork == l2NetworkID {
			deposits = append(deposits, dep)
		}
	}
	return deposits, nil
}

// decodeBridgeEvent decodes ABI-encoded BridgeEvent data.
// Layout: leafType | originNetwork | originAddress | destNetwork |
//
//	destAddress | amount | metadataOffset | depositCount | metadata...
func decodeBridgeEvent(
	dataHex, blockNumberHex, txHashHex string,
) (L1Deposit, error) {
	data := common.FromHex(dataHex)
	const minDataLen = 256
	if len(data) < minDataLen {
		return L1Deposit{}, fmt.Errorf("data too short: %d bytes", len(data))
	}

	metadataOffset := new(big.Int).SetBytes(data[192:224]).Uint64()
	metadata, err := extractMetadata(data, metadataOffset)
	if err != nil {
		return L1Deposit{}, err
	}

	return parseBridgeFields(data, metadata, blockNumberHex, txHashHex)
}

func parseBridgeFields(
	data, metadata []byte, blockNumberHex, txHashHex string,
) (L1Deposit, error) {
	leafType, err := safeUint8(new(big.Int).SetBytes(data[0:32]))
	if err != nil {
		return L1Deposit{}, fmt.Errorf("leafType: %w", err)
	}
	originNetwork, err := safeUint32(new(big.Int).SetBytes(data[32:64]))
	if err != nil {
		return L1Deposit{}, fmt.Errorf("originNetwork: %w", err)
	}
	destNetwork, err := safeUint32(new(big.Int).SetBytes(data[96:128]))
	if err != nil {
		return L1Deposit{}, fmt.Errorf("destNetwork: %w", err)
	}
	depositCount, err := safeUint32(new(big.Int).SetBytes(data[224:256]))
	if err != nil {
		return L1Deposit{}, fmt.Errorf("depositCount: %w", err)
	}

	return L1Deposit{
		LeafType:           leafType,
		OriginNetwork:      originNetwork,
		OriginAddress:      common.BytesToAddress(data[64:96]),
		DestinationNetwork: destNetwork,
		DestinationAddress: common.BytesToAddress(data[128:160]),
		Amount:             new(big.Int).SetBytes(data[160:192]),
		Metadata:           metadata,
		DepositCount:       depositCount,
		BlockNumber:        hexToUint64(blockNumberHex),
		TxHash:             common.HexToHash(txHashHex),
	}, nil
}

func extractMetadata(data []byte, metadataOffset uint64) ([]byte, error) {
	const abiWordSize = 32
	if metadataOffset+abiWordSize > uint64(len(data)) {
		return nil, nil
	}
	metadataLen := new(big.Int).SetBytes(
		data[metadataOffset : metadataOffset+abiWordSize],
	).Uint64()
	if metadataLen > maxMetadataSize {
		return nil, fmt.Errorf(
			"metadata too large: %d bytes (max %d)", metadataLen, maxMetadataSize,
		)
	}
	metadataStart := metadataOffset + abiWordSize
	if metadataStart+metadataLen > uint64(len(data)) {
		return nil, nil
	}
	metadata := make([]byte, metadataLen)
	copy(metadata, data[metadataStart:metadataStart+metadataLen])
	return metadata, nil
}
