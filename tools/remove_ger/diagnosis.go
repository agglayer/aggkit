package remove_ger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
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
// If GER exists on L1 and force is false, returns ErrGERExistsOnL1.
func Diagnose(ctx context.Context, env *Env, gerHash common.Hash, force bool) (*DiagnosisResult, error) {
	result := &DiagnosisResult{
		InvalidGER: gerHash,
		Claims:     nil,
		Scenario:   ScenarioNoClaims,
	}

	// Step 1 — Validate GER on L1
	l1Timestamp, err := env.L1GERManager.GlobalExitRootMap(&bind.CallOpts{Context: ctx}, gerHash)
	if err != nil {
		return nil, fmt.Errorf("L1 globalExitRootMap: %w", err)
	}
	result.GERExistsOnL1 = l1Timestamp != nil && l1Timestamp.Sign() > 0
	if result.GERExistsOnL1 && !force {
		return nil, ErrGERExistsOnL1{GER: gerHash}
	}

	// Step 2 — Validate GER on L2
	l2Timestamp, err := env.L2GERManager.GlobalExitRootMap(&bind.CallOpts{Context: ctx}, gerHash)
	if err != nil {
		return nil, fmt.Errorf("L2 globalExitRootMap: %w", err)
	}
	result.GERTimestampL2 = l2Timestamp
	result.GERExistsOnL2 = l2Timestamp != nil && l2Timestamp.Sign() > 0
	if !result.GERExistsOnL2 {
		return result, nil
	}

	// Step 3 — Find claims using the GER (query L2 Bridge SQLite)
	claims, err := GetClaimsByGER(ctx, env.SQLite.BridgeL2, gerHash)
	if err != nil {
		return nil, fmt.Errorf("get claims by GER: %w", err)
	}
	if len(claims) == 0 {
		return result, nil
	}

	// Step 4 — Classify each claim
	result.Claims = make([]ClaimDiagnosis, 0, len(claims))
	for _, c := range claims {
		cd, err := classifyClaim(env, c)
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

// ErrGERExistsOnL1 is returned when the GER exists on L1 (not invalid) and --force was not set.
type ErrGERExistsOnL1 struct {
	GER common.Hash
}

func (e ErrGERExistsOnL1) Error() string {
	return fmt.Sprintf("GER %s exists on L1 (timestamp > 0); this may not be an invalid GER. Use --force to continue anyway", e.GER.Hex())
}

// GetClaimsByGER queries the L2 bridgesync DB for claims that used the given GER.
// Exported so E2E tests can use the same query/connection for wait and assertion as the tool.
// If the claim table does not exist yet (e.g. bridge sync not run), returns nil, nil (no claims).
func GetClaimsByGER(ctx context.Context, db *sql.DB, gerHash common.Hash) ([]*bridgesync.Claim, error) {
	const query = `SELECT * FROM claim WHERE global_exit_root = $1 ORDER BY block_num ASC, block_pos ASC`
	var claims []*bridgesync.Claim
	rows, err := db.QueryContext(ctx, query, gerHash.Hex())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		// Bridge sync may not have run yet; treat missing table as no claims (doc above).
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c := &bridgesync.Claim{}
		if err := meddler.Scan(rows, c); err != nil {
			return nil, err
		}
		claims = append(claims, c)
	}
	if err := rows.Err(); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return claims, nil
}

// classifyClaim classifies a single claim (A, B.1, B.2) using the runbook decision tree.
func classifyClaim(env *Env, claim *bridgesync.Claim) (ClaimDiagnosis, error) {
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

	// L1 origin: query L1 bridge at deposit_count
	var bridgeAtX bridgesync.Bridge
	err = meddler.QueryRow(env.SQLite.BridgeL1, &bridgeAtX,
		`SELECT * FROM bridge WHERE deposit_count = $1 AND origin_network = 0`, cd.DepositCount)
	if err != nil {
		if err == sql.ErrNoRows {
			return cd, nil // no bridge on L1 → Category A
		}
		return cd, err
	}

	// Compare content: leaf_type vs IsMessage, origin_network, origin_address, destination_network, destination_address, amount, metadata
	claimLeafType := uint8(0)
	if claim.IsMessage {
		claimLeafType = 1
	}
	if bridgeAtX.LeafType != claimLeafType ||
		bridgeAtX.OriginNetwork != claim.OriginNetwork ||
		bridgeAtX.OriginAddress != claim.OriginAddress ||
		bridgeAtX.DestinationNetwork != claim.DestinationNetwork ||
		bridgeAtX.DestinationAddress != claim.DestinationAddress ||
		!equalBigInt(bridgeAtX.Amount, claim.Amount) ||
		!equalBytes(bridgeAtX.Metadata, claim.Metadata) {
		return cd, nil // content mismatch → Category A
	}

	// Content matches. Search L1 for any bridge (and bridge_archive) with same content to detect B.2 (correct index elsewhere).
	type bridgeRow struct {
		DepositCount uint32 `meddler:"deposit_count"`
	}
	const contentMatchSQL = `SELECT deposit_count FROM bridge WHERE origin_network = 0 AND leaf_type = $1 AND origin_address = $2 AND destination_network = $3 AND destination_address = $4 AND amount = $5 AND metadata = $6`
	var matches []*bridgeRow
	amountStr := "0"
	if bridgeAtX.Amount != nil {
		amountStr = bridgeAtX.Amount.String()
	}
	err = meddler.QueryAll(env.SQLite.BridgeL1, &matches, contentMatchSQL,
		bridgeAtX.LeafType, bridgeAtX.OriginAddress.Hex(), bridgeAtX.DestinationNetwork, bridgeAtX.DestinationAddress.Hex(),
		amountStr, bridgeAtX.Metadata)
	if err != nil && err != sql.ErrNoRows {
		return cd, err
	}
	// Also check bridge_archive
	const contentMatchArchiveSQL = `SELECT deposit_count FROM bridge_archive WHERE origin_network = 0 AND leaf_type = $1 AND origin_address = $2 AND destination_network = $3 AND destination_address = $4 AND amount = $5 AND metadata = $6`
	var archiveMatches []*bridgeRow
	_ = meddler.QueryAll(env.SQLite.BridgeL1, &archiveMatches, contentMatchArchiveSQL,
		bridgeAtX.LeafType, bridgeAtX.OriginAddress.Hex(), bridgeAtX.DestinationNetwork, bridgeAtX.DestinationAddress.Hex(),
		amountStr, bridgeAtX.Metadata)
	for _, m := range archiveMatches {
		matches = append(matches, m)
	}

	// If any match has deposit_count != claim's → B.2
	for _, m := range matches {
		if m.DepositCount != cd.DepositCount {
			cd.Category = ScenarioCategoryB2
			cd.CorrectBridge = bridgeToBridgeData(&bridgeAtX)
			// Correct bridge is the one at the other deposit_count; we already have bridgeAtX at X. Find the one at m.DepositCount.
			var correctB bridgesync.Bridge
			err = meddler.QueryRow(env.SQLite.BridgeL1, &correctB,
				`SELECT * FROM bridge WHERE deposit_count = $1 AND origin_network = 0`, m.DepositCount)
			if err != nil {
				err = meddler.QueryRow(env.SQLite.BridgeL1, &correctB,
					`SELECT * FROM bridge_archive WHERE deposit_count = $1 AND origin_network = 0`, m.DepositCount)
			}
			if err == nil {
				cd.CorrectBridge = bridgeToBridgeData(&correctB)
			}
			return cd, nil
		}
	}

	// Same index (content matches, only at X). Compare GER → B.1 if GER differs.
	l1Leaf, err := getL1InfoLeafUntilBlock(env.SQLite.L1InfoTree, bridgeAtX.BlockNum)
	if err != nil {
		// If we can't get L1 leaf, treat as B.1 (same index, invalid GER implies wrong GER)
		cd.Category = ScenarioCategoryB1
		cd.CorrectBridge = bridgeToBridgeData(&bridgeAtX)
		return cd, nil
	}
	if claim.MainnetExitRoot != l1Leaf.MainnetExitRoot || claim.RollupExitRoot != l1Leaf.RollupExitRoot {
		cd.Category = ScenarioCategoryB1
		cd.CorrectBridge = bridgeToBridgeData(&bridgeAtX)
		return cd, nil
	}
	// GER matches — shouldn't happen for invalid GER; treat as B.1 anyway so we still have a recovery path
	cd.Category = ScenarioCategoryB1
	cd.CorrectBridge = bridgeToBridgeData(&bridgeAtX)
	return cd, nil
}

func bridgeToBridgeData(b *bridgesync.Bridge) *BridgeData {
	if b == nil {
		return nil
	}
	return &BridgeData{
		LeafType:           b.LeafType,
		OriginNetwork:      b.OriginNetwork,
		OriginAddress:      b.OriginAddress,
		DestinationNetwork: b.DestinationNetwork,
		DestinationAddress: b.DestinationAddress,
		Amount:             b.Amount,
		Metadata:           b.Metadata,
		DepositCount:       b.DepositCount,
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

// getL1InfoLeafUntilBlock returns the L1InfoTree leaf at or just after the given block (leaf with block_num >= blockNum, first by order).
func getL1InfoLeafUntilBlock(db *sql.DB, blockNum uint64) (*l1infotreesync.L1InfoTreeLeaf, error) {
	const query = `SELECT * FROM l1info_leaf WHERE block_num >= $1 ORDER BY block_num ASC, block_pos ASC LIMIT 1`
	leaf := &l1infotreesync.L1InfoTreeLeaf{}
	err := meddler.QueryRow(db, leaf, query, blockNum)
	if err != nil {
		return nil, err
	}
	return leaf, nil
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
	return "0x" + g.Text(16)
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
			fmt.Printf("  %d. Force emit corrected claim event for %s (forceEmitDetailedClaimEvent)\n", step, formatGlobalIndex(cd.GlobalIndex))
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
			fmt.Printf("  %d. Force emit corrected claim event for %s (forceEmitDetailedClaimEvent)\n", step, formatGlobalIndex(cd.GlobalIndex))
			step++
		}
	}

	fmt.Printf("  %d. Restore bridge (deactivateEmergencyState)\n", step)
}
