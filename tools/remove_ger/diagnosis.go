package remove_ger

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/agglayer/aggkit/bridgeservice/client"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

const (
	decimalBase = 10
	hexBase     = 16
)

// Scenario is the overall or per-claim classification from the runbook.
type Scenario string

const (
	ScenarioNoClaims   Scenario = "no_claims"
	ScenarioCategoryA  Scenario = "category_a"
	ScenarioCategoryB1 Scenario = "category_b1"
	ScenarioCategoryB2 Scenario = "category_b2"
)

// DiagnosisResult holds the result of the diagnosis phase.
type DiagnosisResult struct {
	InvalidGER     common.Hash
	GERExistsOnL1  bool
	GERExistsOnL2  bool
	GERTimestampL2 *big.Int
	Claims         []ClaimDiagnosis
	Scenario       Scenario
}

// ClaimDiagnosis holds the classification for a single claim.
type ClaimDiagnosis struct {
	GlobalIndex   *big.Int
	DepositCount  uint32
	OriginNetwork uint32
	Category      Scenario
	CorrectBridge *BridgeData // nil for Category A
}

// BridgeData holds L1 bridge fields needed for comparison and for CorrectBridge (B.1/B.2).
type BridgeData struct {
	LeafType           uint8
	OriginNetwork      uint32
	OriginAddress      common.Address
	DestinationNetwork uint32
	DestinationAddress common.Address
	Amount             *big.Int
	Metadata           []byte
	DepositCount       uint32
}

// Diagnose runs the diagnosis phase: validate GER on L1/L2, find claims by GER, classify each claim.
// If GER exists on L1 and force is false, returns GERExistsOnL1Error.
func Diagnose(ctx context.Context, env *Env, gerHash common.Hash, force bool) (*DiagnosisResult, error) {
	result := &DiagnosisResult{
		InvalidGER: gerHash,
		Claims:     nil,
		Scenario:   ScenarioNoClaims,
	}

	// Step 1 — Validate GER doesn't exist on L1
	l1Timestamp, err := env.L1GERManager.GlobalExitRootMap(&bind.CallOpts{Context: ctx}, gerHash)
	if err != nil {
		return nil, fmt.Errorf("L1 globalExitRootMap: %w", err)
	}
	result.GERExistsOnL1 = l1Timestamp != nil && l1Timestamp.Sign() > 0
	if result.GERExistsOnL1 && !force {
		return nil, GERExistsOnL1Error{GER: gerHash}
	}

	// Step 2 — Validate GER exists on L2
	l2Timestamp, err := env.L2GERManager.GlobalExitRootMap(&bind.CallOpts{Context: ctx}, gerHash)
	if err != nil {
		return nil, fmt.Errorf("L2 globalExitRootMap: %w", err)
	}
	result.GERTimestampL2 = l2Timestamp
	result.GERExistsOnL2 = l2Timestamp != nil && l2Timestamp.Sign() > 0
	if !result.GERExistsOnL2 {
		return result, nil
	}

	// Step 3 — Find claims using the GER (via bridge service)
	claims, err := GetClaimsByGER(ctx, env.BridgeService, env.L2NetworkID, gerHash)
	if err != nil {
		return nil, fmt.Errorf("get claims by GER: %w", err)
	}
	if len(claims) == 0 {
		return result, nil
	}

	// Step 4 — Classify each claim
	result.Claims = make([]ClaimDiagnosis, 0, len(claims))
	for _, c := range claims {
		cd, err := classifyClaim(ctx, env, c)
		if err != nil {
			return nil, fmt.Errorf("classify claim global_index=%s: %w", c.GlobalIndex.String(), err)
		}
		result.Claims = append(result.Claims, cd)
	}

	// Overall scenario = worst among all claims: A > B.2 > B.1 > NoClaims
	for _, cd := range result.Claims {
		switch cd.Category {
		case ScenarioCategoryA:
			result.Scenario = ScenarioCategoryA
			return result, nil
		case ScenarioCategoryB2:
			if result.Scenario != ScenarioCategoryA {
				result.Scenario = ScenarioCategoryB2
			}
		case ScenarioCategoryB1:
			if result.Scenario != ScenarioCategoryA && result.Scenario != ScenarioCategoryB2 {
				result.Scenario = ScenarioCategoryB1
			}
		}
	}
	return result, nil
}

// GERExistsOnL1Error is returned when the GER exists on L1 (not invalid) and --force was not set.
type GERExistsOnL1Error struct {
	GER common.Hash
}

func (e GERExistsOnL1Error) Error() string {
	return fmt.Sprintf(
		"GER %s exists on L1 (timestamp > 0); this may not be an invalid GER. Use --force to continue anyway",
		e.GER.Hex(),
	)
}

// GetClaimsByGER queries the bridge service for DetailedClaimEvent claims that used the given GER.
// networkID specifies which network to query (0 for L1, L2 network ID otherwise).
// Exported so E2E tests can use the same query for wait and assertion as the tool.
func GetClaimsByGER(
	ctx context.Context, bridgeService *client.Client, networkID uint32, gerHash common.Hash,
) ([]*bridgesync.Claim, error) {
	res, err := bridgeService.GetClaimsByGER(ctx, networkID, gerHash.Hex())
	if err != nil {
		return nil, fmt.Errorf("GetClaimsByGER: %w", err)
	}
	if res == nil || len(res.Claims) == 0 {
		return nil, nil
	}
	claims := make([]*bridgesync.Claim, 0, len(res.Claims))
	for _, cr := range res.Claims {
		claims = append(claims, claimResponseToClaim(cr))
	}
	return claims, nil
}

// claimResponseToClaim converts a bridge service ClaimResponse to a bridgesync.Claim.
func claimResponseToClaim(r *bridgetypes.ClaimResponse) *bridgesync.Claim {
	globalIndex, _ := new(big.Int).SetString(string(r.GlobalIndex), decimalBase)
	amount, _ := new(big.Int).SetString(string(r.Amount), decimalBase)
	return &bridgesync.Claim{
		BlockNum:           r.BlockNum,
		BlockTimestamp:     r.BlockTimestamp,
		TxHash:             common.HexToHash(string(r.TxHash)),
		GlobalIndex:        globalIndex,
		OriginNetwork:      r.OriginNetwork,
		OriginAddress:      common.HexToAddress(string(r.OriginAddress)),
		DestinationAddress: common.HexToAddress(string(r.DestinationAddress)),
		DestinationNetwork: r.DestinationNetwork,
		Amount:             amount,
		MainnetExitRoot:    common.HexToHash(string(r.MainnetExitRoot)),
		RollupExitRoot:     common.HexToHash(string(r.RollupExitRoot)),
		GlobalExitRoot:     common.HexToHash(string(r.GlobalExitRoot)),
		Metadata:           decodeMetadataHex(r.Metadata),
		IsMessage:          r.IsMessage,
		Type:               bridgesync.DetailedClaimEvent,
	}
}

// bridgeResponseToBridgeData converts a bridge service BridgeResponse to BridgeData.
func bridgeResponseToBridgeData(b *bridgetypes.BridgeResponse) *BridgeData {
	if b == nil {
		return nil
	}
	amount, _ := new(big.Int).SetString(string(b.Amount), decimalBase)
	return &BridgeData{
		LeafType:           b.LeafType,
		OriginNetwork:      b.OriginNetwork,
		OriginAddress:      common.HexToAddress(string(b.OriginAddress)),
		DestinationNetwork: b.DestinationNetwork,
		DestinationAddress: common.HexToAddress(string(b.DestinationAddress)),
		Amount:             amount,
		Metadata:           decodeMetadataHex(b.Metadata),
		DepositCount:       b.DepositCount,
	}
}

// decodeMetadataHex decodes a "0x..."-prefixed hex string to bytes. Returns nil for empty/invalid input.
func decodeMetadataHex(s string) []byte {
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

// classifyClaim classifies a single claim (A, B.1, B.2) using the runbook decision tree.
func classifyClaim(ctx context.Context, env *Env, claim *bridgesync.Claim) (ClaimDiagnosis, error) {
	cd := ClaimDiagnosis{
		GlobalIndex:   claim.GlobalIndex,
		OriginNetwork: claim.OriginNetwork,
		Category:      ScenarioCategoryA,
	}

	mainnetFlag, rollupIndex, localExitRootIndex, err := bridgesync.DecodeGlobalIndex(claim.GlobalIndex)
	if err != nil {
		return cd, fmt.Errorf("decode global index: %w", err)
	}
	cd.DepositCount = localExitRootIndex
	if mainnetFlag {
		cd.OriginNetwork = 0
	} else {
		cd.OriginNetwork = rollupIndex + 1
	}

	// Non-L1 origin: assume Category A (runbook)
	if cd.OriginNetwork != 0 {
		return cd, nil
	}

	claimLeafType := uint8(0)
	if claim.IsMessage {
		claimLeafType = 1
	}

	log.Infof("[classify] claim global_index=%s deposit_count=%d origin_network=%d"+
		" leaf_type=%d origin_addr=%s dest_net=%d dest_addr=%s amount=%s metadata_len=%d",
		claim.GlobalIndex.String(), cd.DepositCount, cd.OriginNetwork, claimLeafType,
		claim.OriginAddress.Hex(), claim.DestinationNetwork, claim.DestinationAddress.Hex(),
		claim.Amount.String(), len(claim.Metadata))

	// L1 origin: query L1 bridge at deposit_count via bridge service
	bridgeResp, err := env.BridgeService.GetBridgeByDepositCount(ctx, 0, cd.DepositCount)
	if err != nil {
		if !isNotFound(err) {
			return cd, fmt.Errorf("get bridge by deposit count %d: %w", cd.DepositCount, err)
		}
		log.Infof("[classify] no bridge at deposit_count=%d (not found), searching by content", cd.DepositCount)
		// No bridge at the claimed deposit_count. Search by claim content to detect B.2
		// (bridge exists at a different deposit_count — typical in reorg / wrong-index scenarios).
		return classifyByClaimContent(ctx, env, claim, claimLeafType, cd)
	}
	bridgeAtX := bridgeResponseToBridgeData(bridgeResp)
	log.Infof("[classify] bridge at deposit_count=%d: leaf_type=%d origin_net=%d"+
		" origin_addr=%s dest_net=%d dest_addr=%s amount=%s metadata_len=%d",
		cd.DepositCount, bridgeAtX.LeafType, bridgeAtX.OriginNetwork, bridgeAtX.OriginAddress.Hex(),
		bridgeAtX.DestinationNetwork, bridgeAtX.DestinationAddress.Hex(),
		bridgeAtX.Amount.String(), len(bridgeAtX.Metadata))

	// Compare content: leaf_type vs IsMessage, origin_network, origin_address,
	// destination_network, destination_address, amount, metadata
	if bridgeAtX.LeafType != claimLeafType ||
		bridgeAtX.OriginNetwork != claim.OriginNetwork ||
		bridgeAtX.OriginAddress != claim.OriginAddress ||
		bridgeAtX.DestinationNetwork != claim.DestinationNetwork ||
		bridgeAtX.DestinationAddress != claim.DestinationAddress ||
		!equalBigInt(bridgeAtX.Amount, claim.Amount) ||
		!equalBytes(bridgeAtX.Metadata, claim.Metadata) {
		// Content mismatch at this deposit_count. Search by claim content to detect B.2.
		return classifyByClaimContent(ctx, env, claim, claimLeafType, cd)
	}

	// Content matches at deposit_count X. Search for other bridges with same content to detect B.2.
	contentRes, err := env.BridgeService.GetBridgesByContent(ctx, client.GetBridgesByContentParams{
		NetworkID:          0,
		LeafType:           bridgeAtX.LeafType,
		OriginAddress:      bridgeAtX.OriginAddress.Hex(),
		DestinationNetwork: bridgeAtX.DestinationNetwork,
		DestinationAddress: bridgeAtX.DestinationAddress.Hex(),
		Amount:             bridgeAtX.Amount,
		Metadata:           bridgeAtX.Metadata,
	})
	if err != nil {
		return cd, fmt.Errorf("get bridges by content: %w", err)
	}

	// If any match has deposit_count != claim's → B.2
	for _, m := range contentRes.Bridges {
		if m.DepositCount != cd.DepositCount {
			// Correct bridge is the one at the other deposit_count
			correctResp, err := env.BridgeService.GetBridgeByDepositCount(ctx, 0, m.DepositCount)
			if err == nil {
				cd.CorrectBridge = bridgeResponseToBridgeData(correctResp)
			} else {
				cd.CorrectBridge = bridgeResponseToBridgeData(bridgeResp)
			}
			cd.Category = ScenarioCategoryB2
			return cd, nil
		}
	}

	// Same index (content matches, only at X). Compare GER → B.1 if GER differs.
	l1Leaf, err := getL1InfoLeafByDepositCount(ctx, env.BridgeService, cd.DepositCount)
	if err != nil {
		// If we can't get L1 leaf, treat as B.1 (same index, invalid GER implies wrong GER)
		cd.Category = ScenarioCategoryB1
		cd.CorrectBridge = bridgeAtX
		return cd, nil
	}
	if claim.MainnetExitRoot != l1Leaf.MainnetExitRoot || claim.RollupExitRoot != l1Leaf.RollupExitRoot {
		cd.Category = ScenarioCategoryB1
		cd.CorrectBridge = bridgeAtX
		return cd, nil
	}
	// GER matches — shouldn't happen for invalid GER; treat as B.1 anyway so we still have a recovery path
	cd.Category = ScenarioCategoryB1
	cd.CorrectBridge = bridgeAtX
	return cd, nil
}

// classifyByClaimContent handles classification when there is no valid bridge at the claim's deposit_count
// (either not found or content mismatch). It searches all L1 bridges with the same content fields as the
// claim. If a match is found at a different deposit_count, the claim is B.2. Otherwise Category A.
func classifyByClaimContent(
	ctx context.Context, env *Env, claim *bridgesync.Claim, claimLeafType uint8, cd ClaimDiagnosis,
) (ClaimDiagnosis, error) {
	cd.Category = ScenarioCategoryA // default

	log.Infof("[classifyByContent] searching bridges: leaf_type=%d origin_addr=%s"+
		" dest_net=%d dest_addr=%s amount=%s metadata=%x",
		claimLeafType, claim.OriginAddress.Hex(), claim.DestinationNetwork,
		claim.DestinationAddress.Hex(), claim.Amount.String(), claim.Metadata)

	contentRes, err := env.BridgeService.GetBridgesByContent(ctx, client.GetBridgesByContentParams{
		NetworkID:          0,
		LeafType:           claimLeafType,
		OriginAddress:      claim.OriginAddress.Hex(),
		DestinationNetwork: claim.DestinationNetwork,
		DestinationAddress: claim.DestinationAddress.Hex(),
		Amount:             claim.Amount,
		Metadata:           claim.Metadata,
	})
	if err != nil {
		log.Infof("[classifyByContent] GetBridgesByContent error (falling back to category_a): %v", err)
		// Content search failed; fall back to Category A
		return cd, nil
	}

	log.Infof("[classifyByContent] GetBridgesByContent returned %d bridges (claim deposit_count=%d)",
		len(contentRes.Bridges), cd.DepositCount)
	for i, m := range contentRes.Bridges {
		log.Infof("[classifyByContent] bridge[%d]: deposit_count=%d origin_addr=%s dest_addr=%s amount=%s",
			i, m.DepositCount, m.OriginAddress, m.DestinationAddress, m.Amount)
		if m.DepositCount != cd.DepositCount {
			// A bridge with identical content exists at a different deposit_count → B.2
			correctResp, err := env.BridgeService.GetBridgeByDepositCount(ctx, 0, m.DepositCount)
			if err == nil {
				cd.CorrectBridge = bridgeResponseToBridgeData(correctResp)
			}
			cd.Category = ScenarioCategoryB2
			return cd, nil
		}
	}

	log.Infof("[classifyByContent] no bridge at different deposit_count → category_a")
	return cd, nil // Category A: no matching bridge found at any deposit_count
}

// isNotFound returns true if the error is a client.ErrNotFound sentinel.
func isNotFound(err error) bool {
	return err != nil && err.Error() == client.ErrNotFound.Error()
}

// getL1InfoLeafByDepositCount uses the two-step bridge service lookup to find the L1InfoTree leaf
// that first includes the given L1 bridge deposit_count in its MainnetExitRoot.
// Step 1: GetL1InfoTreeIndex(ctx, 0, depositCount) → leafIndex
// Step 2: GetInjectedL1InfoLeaf(ctx, 0, leafIndex) → leaf response
func getL1InfoLeafByDepositCount(
	ctx context.Context, bsc *client.Client, depositCount uint32,
) (*l1infotreesync.L1InfoTreeLeaf, error) {
	leafIndex, err := bsc.GetL1InfoTreeIndex(ctx, 0, int(depositCount))
	if err != nil {
		return nil, fmt.Errorf("get L1 info tree index for deposit_count=%d: %w", depositCount, err)
	}
	resp, err := bsc.GetInjectedL1InfoLeaf(ctx, 0, int(leafIndex))
	if err != nil {
		return nil, fmt.Errorf("get L1 info leaf at index=%d: %w", leafIndex, err)
	}
	return l1InfoLeafResponseToLeaf(resp), nil
}

// l1InfoLeafResponseToLeaf converts a bridge service L1InfoTreeLeafResponse to l1infotreesync.L1InfoTreeLeaf.
func l1InfoLeafResponseToLeaf(r *bridgetypes.L1InfoTreeLeafResponse) *l1infotreesync.L1InfoTreeLeaf {
	return &l1infotreesync.L1InfoTreeLeaf{
		BlockNumber:       r.BlockNumber,
		BlockPosition:     r.BlockPosition,
		L1InfoTreeIndex:   r.L1InfoTreeIndex,
		PreviousBlockHash: common.HexToHash(string(r.PreviousBlockHash)),
		Timestamp:         r.Timestamp,
		MainnetExitRoot:   common.HexToHash(string(r.MainnetExitRoot)),
		RollupExitRoot:    common.HexToHash(string(r.RollupExitRoot)),
		GlobalExitRoot:    common.HexToHash(string(r.GlobalExitRoot)),
		Hash:              common.HexToHash(string(r.Hash)),
	}
}

func equalBigInt(a, b *big.Int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Cmp(b) == 0
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// PrintDiagnosis prints a human-readable diagnosis summary and recovery plan to stdout.
func PrintDiagnosis(result *DiagnosisResult) {
	fmt.Println("=== Remove GER Diagnosis ===")
	fmt.Println()
	fmt.Printf("Invalid GER: %s\n", result.InvalidGER.Hex())
	if result.GERExistsOnL1 {
		fmt.Println("  L1: FOUND (may not be invalid — consider without --force)")
	} else {
		fmt.Println("  L1: NOT FOUND (confirmed invalid)")
	}
	if result.GERExistsOnL2 {
		ts := "0"
		if result.GERTimestampL2 != nil {
			ts = result.GERTimestampL2.String()
		}
		fmt.Printf("  L2: EXISTS (timestamp: %s)\n", ts)
	} else {
		fmt.Println("  L2: NOT FOUND (nothing to do)")
	}
	fmt.Println()

	n := len(result.Claims)
	fmt.Printf("Claims using this GER: %d\n", n)
	for i, cd := range result.Claims {
		fmt.Printf("\n  Claim %d:\n", i+1)
		fmt.Printf("    Global Index: %s\n", formatGlobalIndex(cd.GlobalIndex))
		fmt.Printf("    Origin Network: %d", cd.OriginNetwork)
		if cd.OriginNetwork == 0 {
			fmt.Print(" (L1)")
		}
		fmt.Println()
		fmt.Printf("    Deposit Count: %d\n", cd.DepositCount)
		fmt.Printf("    Category: %s\n", categoryDescription(cd.Category))
	}
	fmt.Println()
	fmt.Printf("Overall Scenario: %s\n", scenarioDescription(result.Scenario))
	fmt.Println()
	fmt.Println("=== Recovery Plan ===")
	fmt.Println()
	printRecoveryPlanSteps(result)
}

func formatGlobalIndex(g *big.Int) string {
	if g == nil {
		return "0x0"
	}
	return "0x" + g.Text(hexBase)
}

func categoryDescription(s Scenario) string {
	switch s {
	case ScenarioCategoryA:
		return "A (under-collateralization — bridge does not exist on L1 or content mismatch)"
	case ScenarioCategoryB1:
		return "B.1 (GER mismatch, same index — bridge exists on L1 with correct content)"
	case ScenarioCategoryB2:
		return "B.2 (GER and index mismatch — bridge exists on L1 at different deposit_count)"
	default:
		return string(s)
	}
}

func scenarioDescription(s Scenario) string {
	switch s {
	case ScenarioNoClaims:
		return "NoClaims (no claims to recover)"
	case ScenarioCategoryA:
		return "Category A (most restrictive)"
	case ScenarioCategoryB1:
		return "Category B.1"
	case ScenarioCategoryB2:
		return "Category B.2 (most complex)"
	default:
		return string(s)
	}
}

func printRecoveryPlanSteps(result *DiagnosisResult) {
	fmt.Println("The following steps will be executed:")
	step := 1
	fmt.Printf("  %d. Freeze bridge (activateEmergencyState)\n", step)
	step++
	fmt.Printf("  %d. Remove GER %s (removeGlobalExitRoots)\n", step, result.InvalidGER.Hex())
	step++

	switch result.Scenario {
	case ScenarioNoClaims:
		// no unset/set/emit
	case ScenarioCategoryA:
		for _, cd := range result.Claims {
			fmt.Printf("  %d. Unset claim %s (unsetMultipleClaims)\n", step, formatGlobalIndex(cd.GlobalIndex))
			step++
		}
	case ScenarioCategoryB1:
		for _, cd := range result.Claims {
			fmt.Printf("  %d. Force emit corrected claim event for %s (forceEmitDetailedClaimEvent)\n",
				step, formatGlobalIndex(cd.GlobalIndex))
			step++
		}
	case ScenarioCategoryB2:
		for _, cd := range result.Claims {
			fmt.Printf("  %d. Unset claim %s (unsetMultipleClaims)\n", step, formatGlobalIndex(cd.GlobalIndex))
			step++
		}
		fmt.Printf("  %d. Set claims with correct global indexes (setMultipleClaims)\n", step)
		step++
		for _, cd := range result.Claims {
			fmt.Printf("  %d. Force emit corrected claim event for %s (forceEmitDetailedClaimEvent)\n",
				step, formatGlobalIndex(cd.GlobalIndex))
			step++
		}
	}

	fmt.Printf("  %d. Restore bridge (deactivateEmergencyState)\n", step)
}
