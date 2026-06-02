package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestInvalidGERInjectionB2_PP is the op-pp / Go-stack port of the legacy bats test
// "Test invalid GER injection case B2 (PP mode)"
// (agglayer/e2e: e2e/tests/aggkit/latest-n-injected-ger.bats).
//
// SCOPE — what this ports:
// It migrates ONLY the "case B2 (PP mode)" scenario. The actual B2 scenario is implemented by the
// shared helper testRemoveGER_CategoryB2 (in removeger_test.go), which already performs the exact
// B2 lifecycle the bats case exercises, programmatically and more robustly than the manual
// cast/REST steps in bats:
//
//	real L1 bridge (no L2 claim)
//	  -> build a fake single-leaf merkle proof at a WRONG deposit_count (buildFakeMerkleProofForWrongDepositCount)
//	  -> the fake root produces an invalid (reorged-on-L1) GER
//	  -> inject that invalid GER on L2 (injectInvalidGER, aggoracle key)
//	  -> claim the real bridge data at the wrong deposit_count, verified under the fake GER
//	  -> detect the invalid GER from aggkit logs (runbook-aligned)
//	  -> diagnose remove_ger.ScenarioCategoryB2
//	  -> recover: freeze -> remove GER -> unset wrong claim -> set correct claim -> force-emit -> restore
//	  -> assert GER removed, wrong claim unset, correct claim set, no emergency state.
//
// What makes this B2 (vs B1): the claim is made at a WRONG deposit_count via a fake merkle proof,
// simulating an L1 reorg that moved a bridge to a different index, so recovery must compute the
// CORRECT bridge/deposit_count and re-set the claim there. The bats case did the same logical thing
// with hardcoded GERs/proofs/global-indexes and two injected GERs; the Go helper captures the
// semantically meaningful B2 invariant without duplicating those brittle hardcoded artifacts.
//
// This entry point exists so the migrated bats case has its own named test target
// (go test -run TestInvalidGERInjectionB2_PP) while reusing — not duplicating — the proven B2
// pipeline. It delegates to testRemoveGER_CategoryB2 and uses the same timeouts/patterns
// (30-min context, 6-min log-detection, 10-min recovery); it does NOT shorten or alter them.
//
// DELIBERATELY NOT MIGRATED (left on the old bats stack), from the same latest-n-injected-ger.bats:
//   - "Test invalid GER injection case B2 (FEP mode)" — FEP mode; out of scope (this stack is op-pp/PP only).
//   - "Test invalid GER injection case A (PP mode)" — bats-skipped; hardcoded GER + claim proofs that must
//     run independently on a fresh setup. (Category A is separately covered on the Go stack by
//     TestRemoveGER_CategoryA, but this specific hardcoded bats case is not ported.)
//   - "Test invalid GER injection case A (FEP mode)" — bats-skipped; same hardcoded reason, plus FEP mode.
//   - "Inject LatestBlock-N GER - A case PP (another test)" — bats-skipped; requires standing up an anvil
//     fork of L1 plus a separate aggkit bridge service; anvil-fork case, out of scope here.
//
// CLEANUP / STATE RESTORE (mutating test): the body is wrapped in withCleanEmergencyState so any
// emergency state newly introduced by the scenario is restored via SovereignAdmin; the delegated
// testRemoveGER_CategoryB2 also has its own deferred emergency-state restore, and the B2 recovery
// itself removes the injected GER (no extra GER mutation is added here). The test ends with
// assertNetworkHealthy so the shared env is left verified healthy for subsequent tests.
func TestInvalidGERInjectionB2_PP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	withCleanEmergencyState(ctx, t, env, func() {
		// The full bats "case B2 (PP mode)" lifecycle is implemented by this shared helper.
		testRemoveGER_CategoryB2(t)
	})

	// Verify the shared env is left healthy (CheckEnv + bridge service health).
	assertNetworkHealthy(ctx, t, env)
}
