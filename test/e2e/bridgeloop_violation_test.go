package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// This file holds the negative counterpart of TestBridgeLoopFullCycle: the one case in which
// tools/bridge_loop_tester's headline assertion is supposed to *fail*.
//
// It runs in its own CI matrix group (anvil-2chains / bridge-loop-violation) rather than alongside
// TestBridgeLoopFullCycle, for the reason test-go-e2e.yml already states for the autoclaim group: it
// restarts aggkit-002, strands value mid-ring and adds claim traffic on network 2, and sharing one
// go-test process with an unrelated test lets that state leak into it.
//
// What checks that this test restores the env it mutates is TestMain's post-test bridge
// health-check, which runs in the same process once the suite passes: that ring is 0 -> 1 -> 2 -> 0
// with *every* hop Claim = "manual", so its 1 -> 2 hop asserts that nothing claims a deposit landing
// on network 2. A leaked Auto Claim service there makes it report a claim-mode violation and
// log.Fatalf. (TestBridgeLoopFullCycle would not catch that: its only hop into network 2 is the
// "auto" one, and it enables Auto Claim itself anyway.)

const (
	// bridgeLoopViolationAmount is the ETH the violating ring moves on its hops: 0.1 ETH. The ring
	// never closes by design - the run halts on hop 1 - so this is a one-way trip out of the L1
	// pool key, which is why it is an order of magnitude below what TestBridgeLoopFullCycle moves.
	// It is still two orders of magnitude above the hop engine's native gas slack (1e15 wei), so
	// the hop that does succeed can still tell "the value arrived" from "a claim's gas was paid".
	bridgeLoopViolationAmount = 100_000_000_000_000_000 // 1e17

	// bridgeLoopViolationGrace is Global.ManualGracePeriod for this test: the window in which the
	// violating hop asserts nothing claims its deposit, and therefore the window in which the
	// network-2 Auto Claim service has to claim it for the violation to be observed at all.
	//
	// It is the one timing this test cannot shorten aggressively. Too short and the tool would
	// reach the end of the grace period first, claim the deposit itself and report a *success* -
	// a false green. Measured against this env the autoclaim service claims within a couple of
	// seconds of the claim proof becoming available, so 60s is roughly an order of magnitude of
	// headroom, and it costs real time only on the preceding hop (which sits it out in full).
	bridgeLoopViolationGrace = 60 * time.Second

	// bridgeLoopViolationHopTimeout is the total budget of one hop. It has to comfortably exceed
	// bridgeLoopViolationGrace plus the cross-network settlement wait an L2-sourced hop pays (an
	// L2's local exit root must settle to L1 through agglayer before its claim proof exists).
	bridgeLoopViolationHopTimeout = 10 * time.Minute

	// bridgeLoopViolationTestTimeout bounds the whole test. The assertion itself lands in ~2
	// minutes; the headroom is there so a stalled gate fails with the tool's own diagnosis rather
	// than with a bare context deadline.
	bridgeLoopViolationTestTimeout = 20 * time.Minute

	// bridgeLoopViolationHopAttempts is Global.HopAttempts (and OrchestratorDeps.HopAttempts) for
	// this run, deliberately > 1. A claim-mode violation is a test result and never a transient,
	// so it must not be retried - and only a run that *would* have retried a transient can prove
	// that. HopsRetried == 0 below is the assertion this constant exists for.
	bridgeLoopViolationHopAttempts = 3

	// bridgeLoopViolationLoopName names the single loop this test configures. It appears in every
	// log line the tool emits for the run.
	bridgeLoopViolationLoopName = "claim-mode-violation-ring"
)

// TestBridgeLoopClaimModeViolation asserts that tools/bridge_loop_tester detects, reports and
// refuses to retry a real claim-mode violation: a hop configured Claim = "manual" - which asserts
// that nothing claims its deposit - whose deposit an autoclaim service claims inside the grace
// period.
//
// This is the tool's central diagnostic claim (an autoclaim service is active on a route that was
// configured to have none), and TestBridgeLoopFullCycle only ever observes it holding. Here it is
// induced for real, against real services:
//
//   - Auto Claim is enabled on the network-2 (aggkit-002) node exactly as TestAutoClaimL2ToL2AllowAll
//     and TestBridgeLoopFullCycle enable it - the L2ToLx detector plus one network-2 claimer, with
//     the original config restored on cleanup;
//   - the ring 0 -> 1 -> 2 -> 0 declares its L2A -> L2B hop Claim = "manual", which is the hop
//     TestBridgeLoopFullCycle declares "auto". Nothing else about the topology changes, so the only
//     difference between a pass there and a violation here is the expectation the config states.
//
// What is asserted: the run fails, the loop halts with halt_class = claim-mode-violation, the
// violating hop was attempted exactly once despite HopAttempts = 3, the ring is reported as not
// closed with the value stranded in flight, the halt is persisted in the state file, and every layer
// of the tool's output - the hop's error, the loop's error and the error the run returns - carries
// the same diagnosis, naming the bridge transaction whose deposit was claimed, the claim
// transaction and the claimant address recovered from its signature (or stating plainly that those
// two are unknown, which the destination's claim record sometimes leaves them - see
// identifiedClaimant).
func TestBridgeLoopClaimModeViolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	env := loadBridgeLoopTestEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), bridgeLoopViolationTestTimeout)
	defer cancel()

	// The L2ToLx detector with AllowedOrigins = [0, 1] and a single network-2 claimer: it claims
	// anything bridged from network 1 into network 2, which is exactly what hop 1 of the ring below
	// declares nothing may claim. enableAutoClaimL2ToL2 restores aggkit-002's original config and
	// restarts the service on cleanup, so the env is handed back with Auto Claim off - which matters
	// here more than anywhere else, since a leaked claimer would make TestMain's post-test health
	// ring (all hops "manual", including 1 -> 2) report a false claim-mode violation and log.Fatalf.
	enableAutoClaimL2ToL2(t, ctx, env, "allow-all")
	waitForBridgeServiceSynced(ctx, t)

	keys := checkoutBridgeLoopKeys(t, env)
	cfg := newBridgeLoopConfig(t, env, keys)

	// One ETH loop over the same ring TestBridgeLoopFullCycle walks, with hop 1 (1 -> 2) declared
	// "manual" instead of "auto". Hop 0's and hop 2's destinations (networks 1 and 0) have no
	// claimer at all, so hop 0 succeeds normally and the run reaches hop 1 with the value where it
	// belongs; hop 2 must never run, because the loop halts on hop 1.
	cfg.Loops = []bridgelooptester.Loop{{
		Name:    bridgeLoopViolationLoopName,
		Asset:   bridgelooptester.AssetETH,
		Amount:  bridgelooptester.NewWeiAmount(bridgeLoopViolationAmount),
		Enabled: true,
		Hops: []bridgelooptester.Hop{
			{Source: 0, Destination: env.L2.NetworkID, Claim: bridgelooptester.ClaimManual},
			{Source: env.L2.NetworkID, Destination: env.L2B.NetworkID, Claim: bridgelooptester.ClaimManual},
			{Source: env.L2B.NetworkID, Destination: 0, Claim: bridgelooptester.ClaimManual},
		},
	}}
	cfg.Global.Iterations = 1
	cfg.Global.HopTimeout = cfgtypes.Duration{Duration: bridgeLoopViolationHopTimeout}
	cfg.Global.ManualGracePeriod = cfgtypes.Duration{Duration: bridgeLoopViolationGrace}
	cfg.Global.HopAttempts = bridgeLoopViolationHopAttempts

	logger := log.WithFields("test", "bridge-loop-violation")

	// Same environment precondition as TestBridgeLoopFullCycle: this env runs no batcher, so an
	// L2-sourced hop needs an unrelated claim to land on that L2 after its bridge or its local exit
	// root never settles to L1 and the violating hop never even gets a claim proof to race for.
	_, stopPriming := startBridgeLoopPriming(t, ctx, env, keys, logger)
	defer stopPriming()

	orchestrator, err := bridgelooptester.NewOrchestrator(ctx, cfg, bridgelooptester.OrchestratorDeps{
		Logger:      logger,
		HopAttempts: bridgeLoopViolationHopAttempts,
	})
	require.NoError(t, err, "build the bridge_loop_tester orchestrator")
	t.Cleanup(orchestrator.Close)

	report, runErr := orchestrator.Run(ctx)
	require.NotNil(t, report, "Run must return a report even when it fails")
	logBridgeLoopReport(t, report)

	require.Error(t, runErr, "a claim-mode violation must make Run return an error")
	t.Logf("bridge_loop_tester run error: %v", runErr)

	violated := assertBridgeLoopViolationReport(t, env, report)
	assertBridgeLoopViolationEvidence(t, report, runErr, violated)
	assertBridgeLoopViolationState(t, cfg, report)
}

// assertBridgeLoopViolationReport asserts the shape of the halted run - the totals, the loop, the
// cycle and the hop attempts - and returns the violating hop's result for the evidence assertions.
//
// The load-bearing counters are HopsRetried (a violation is never retried) and the number of hop
// attempts recorded in the cycle (hop 0 once, hop 1 once, hop 2 not at all): together they say the
// tool stopped the ring at the violation instead of either retrying it or walking past it.
func assertBridgeLoopViolationReport(
	t *testing.T, env *envs.Env, report *bridgelooptester.Report,
) *bridgelooptester.HopResult {
	t.Helper()

	require.False(t, report.Succeeded(), "the run must not report success")
	require.False(t, report.Cancelled, "the run must halt on the violation, not be cancelled")

	totals := report.Totals
	require.Equal(t, 1, totals.ClaimModeViolations, "exactly one claim-mode violation")
	require.Equal(t, 1, totals.LoopsRun)
	require.Equal(t, 1, totals.LoopsHalted, "the violating loop must be halted")
	require.Equal(t, 0, totals.HopsRetried,
		"a claim-mode violation is a test result and must never be retried, yet HopAttempts was %d",
		bridgeLoopViolationHopAttempts)
	require.Equal(t, 2, totals.HopsAttempted, "hop 0 and hop 1 only: hop 2 must never be reached")
	require.Equal(t, 1, totals.HopsSucceeded, "hop 0 succeeds")
	require.Equal(t, 1, totals.HopsFailed, "hop 1 fails")
	require.Equal(t, 2, totals.ManualClaimHops,
		"the totals count hop attempts, and every hop of this ring is declared manual")
	require.Equal(t, 0, totals.AutoClaimHops)
	require.Equal(t, 1, totals.ClaimedByTool, "the tool claims hop 0 and nothing else")
	// ClaimedExternally counts only the hops whose claimant the tool could actually identify, which
	// depends on the destination serving a claim record - see identifiedClaimant.
	require.LessOrEqual(t, totals.ClaimedExternally, 1)
	require.Equal(t, 1, totals.StrandedLoops, "a halted mid-ring loop leaves its value stranded")
	require.Equal(t, uint64(0), totals.CyclesCompleted, "no cycle may be reported as completed")
	require.Equal(t, uint64(1), totals.CyclesAttempted)

	require.Len(t, report.Loops, 1)
	loop := report.Loops[0]
	require.Equal(t, bridgeLoopViolationLoopName, loop.Name)
	require.True(t, loop.Halted, "the loop must be halted")
	require.Equal(t, bridgelooptester.FailureClaimMode, loop.HaltClass,
		"the halt class is what a CI log or dashboard keys on")
	require.Equal(t, uint64(0), loop.CyclesCompleted)

	require.Len(t, loop.Cycles, 1, "Iterations = 1 yields exactly one cycle")
	cycle := loop.Cycles[0]
	require.False(t, cycle.RingClosed, "the ring cannot have closed")
	require.Equal(t, bridgelooptester.FailureClaimMode, cycle.FailureClass)
	require.Len(t, cycle.Hops, 2,
		"hop 0 succeeded and hop 1 violated exactly once; a third entry would mean either a retry "+
			"of hop 1 or that the loop walked past the violation into hop 2")

	require.Equal(t, bridgelooptester.HopOutcomeSuccess, cycle.Hops[0].Outcome,
		"hop 0 must succeed: %s", cycle.Hops[0].ErrMessage)
	require.False(t, cycle.Hops[0].ClaimModeViolated, "hop 0 has no claimer on its destination")

	// The value is stranded in flight on the violating hop, and the detail must not contradict the
	// violation itself: the state file records how far the tool got, not what the destination bridge
	// says, so it can only report that *this tool* had not claimed the deposit.
	require.True(t, loop.ValueLocation.Stranded, "the halted loop's value is stranded")
	require.True(t, loop.ValueLocation.InFlight, "the deposit is in flight, not sitting on a network")
	require.Equal(t, 1, loop.ValueLocation.HopIndex, "the value is stranded on the violating hop")
	require.Contains(t, loop.ValueLocation.Detail, "or was claimed by something else",
		"the stranded-value detail must not assert the deposit went unclaimed, which is exactly what "+
			"the violation it accompanies disproves")

	violated := cycle.Hops[1]
	require.Equal(t, 1, violated.HopIndex, "the violating hop is hop 1")
	require.Equal(t, env.L2.NetworkID, violated.Source)
	require.Equal(t, env.L2B.NetworkID, violated.Destination)
	require.Equal(t, bridgelooptester.HopOutcomeFailed, violated.Outcome)
	require.True(t, violated.ClaimModeViolated, "the hop must be flagged as a claim-mode violation")
	require.Equal(t, bridgelooptester.ClaimManual, violated.ClaimMode)
	require.NotEqual(t, bridgelooptester.ClaimActorTool, violated.ClaimedBy,
		"the tool submitted no claim of its own, so it must never be credited with this one")
	require.Contains(t,
		[]bridgelooptester.ClaimActor{bridgelooptester.ClaimActorExternal, bridgelooptester.ClaimActorUnknown},
		violated.ClaimedBy,
		"a deposit the destination bridge reports as claimed was claimed by someone: the actor is either "+
			"identified (external) or explicitly unidentified (unknown), never absent")
	require.Equal(t, common.Hash{}, violated.ClaimTxHash,
		"the tool must not have submitted a claim of its own")
	require.Equal(t, bridgeLoopViolationGrace, violated.GracePeriod,
		"the hop must record the grace period whose assertion was violated")

	return violated
}

// assertBridgeLoopViolationEvidence asserts the violation is diagnosable from the tool's output
// alone: the typed error is reachable and correctly filled in, and the offending claim transaction
// and the claimant address appear verbatim in every message a caller sees - the hop's own error, the
// loop's error, and the error Run returns (which is what the CLI prints).
//
// The claimant address is the part worth guarding: the proxy's /bridge/v1/claims never populates
// from_address, so the tool recovers the sender from the claim transaction's own signature. Without
// that recovery the violation would name no culprit, and an operator would be told only that
// "something" claimed the deposit.
func assertBridgeLoopViolationEvidence(
	t *testing.T,
	report *bridgelooptester.Report,
	runErr error,
	violated *bridgelooptester.HopResult,
) {
	t.Helper()

	var violation *bridgelooptester.ClaimModeViolationError
	require.True(t, errors.As(violated.Err, &violation),
		"the hop error must be a *ClaimModeViolationError, got %T: %v", violated.Err, violated.Err)
	require.True(t, errors.Is(violated.Err, bridgelooptester.ErrClaimModeViolation),
		"the error must satisfy errors.Is(err, ErrClaimModeViolation) so a caller can classify it "+
			"without unwrapping the concrete type")
	require.Equal(t, bridgelooptester.ClaimManual, violation.Expected)
	require.Equal(t, violated.ClaimedBy, violation.Observed,
		"the error and the hop result must agree on who claimed the deposit")
	require.Equal(t, violated.Source, violation.Source)
	require.Equal(t, violated.Destination, violation.Destination)
	require.Equal(t, violated.DepositCount, violation.DepositCount)
	require.Equal(t, bridgeLoopViolationGrace, violation.GracePeriod)
	require.NotEqual(t, common.Hash{}, violation.BridgeTxHash,
		"the violation must name the bridge transaction whose deposit was claimed")

	claimTx := violation.ClaimTxHash
	claimFrom := violation.ClaimFromAddress
	require.Equal(t, claimTx, violated.ExternalClaimTxHash,
		"the hop result and the error must agree on the offending claim transaction")
	require.Equal(t, claimFrom, violated.ExternalClaimFromAddress,
		"the hop result and the error must agree on the claimant")

	t.Logf("induced claim-mode violation: route=%s deposit_count=%d global_index=%s bridge_tx=%s "+
		"claimed_by=%s claim_tx=%s claim_from=%s proof_available=%t final_state=%s",
		violated.Route(), violated.DepositCount, violated.GlobalIndex, violated.BridgeTxHash,
		violated.ClaimedBy, claimTx, claimFrom, violation.ProofAvailable, violated.FinalState)
	t.Logf("violating hop error: %s", violated.ErrMessage)

	// Whether the culprit can be named at all is not the tool's decision: the claimant is recovered
	// from the claim transaction, and the transaction's hash comes from the destination's claim
	// record, which this env does not always serve (see the comment on identifiedClaimant below).
	// What must hold either way is that the hop result and the error agree, and that every layer a
	// caller might read carries the same account of the violation - the diagnosis must not stop at
	// whichever layer that caller happens to see.
	identified := identifiedClaimant(t, violated, claimTx, claimFrom)

	require.Len(t, report.Loops, 1)
	outputs := []struct {
		label   string
		message string
	}{
		{label: "the hop's error message", message: violated.ErrMessage},
		{label: "the loop's error", message: report.Loops[0].Err},
		{label: "the run's aggregate error (what the CLI prints)", message: runErr.Error()},
	}
	for _, output := range outputs {
		require.NotEmpty(t, output.message, "%s must not be empty", output.label)
		require.Contains(t, output.message, violation.BridgeTxHash.String(),
			"%s must name the bridge transaction whose deposit was claimed", output.label)
		require.Contains(t, output.message, "autoclaim service appears to be active",
			"%s must say what the violation means, not only that one happened", output.label)
		if identified {
			require.Contains(t, output.message, claimTx.String(),
				"%s must name the offending claim transaction", output.label)
			require.Contains(t, output.message, claimFrom.String(),
				"%s must name the recovered claimant address", output.label)

			continue
		}
		// The tool must say plainly that it could not identify the claim, never quietly omit it.
		require.Contains(t, output.message, "claim tx unknown",
			"%s must state that the claim transaction is unknown rather than omit it", output.label)
		require.Contains(t, output.message, "from none",
			"%s must state that the claimant is unknown rather than omit it", output.label)
	}
}

// identifiedClaimant reports whether the tool managed to name the offending claim, and asserts that
// the two halves of that naming move together: either both the claim transaction and the claimant
// address are known, or neither is.
//
// It is a condition rather than a requirement because the tool cannot always name them, and that is
// deliberate: the claimant is recovered from the claim transaction's own signature, and the
// transaction's hash comes from the destination's GET /bridge/v1/claims record (the proxy never
// populates from_address, so there is no other source). The on-chain isClaimed read that decides the
// hop has no such dependency, so a hop whose claim record the destination has not served is still a
// real, correctly-detected violation - the tool degrades the attribution to ClaimActorUnknown rather
// than failing, by design.
//
// Measured on anvil-2chains this record is usually served within a poll or two, but in roughly one
// run in four it is never served at all: the tool waited its full budget (verified live with that
// budget raised to five minutes) and GET /bridge/v1/claims still answered "not found" for a deposit
// the destination bridge reported as claimed. That is a claim-syncer/bridge-service behaviour
// outside this tool, so requiring the attribution unconditionally here would make the test flaky for
// something it does not test. TestBridgeLoopFullCycle's ClaimedExternally assertion has the same
// exposure.
func identifiedClaimant(
	t *testing.T, violated *bridgelooptester.HopResult, claimTx common.Hash, claimFrom common.Address,
) bool {
	t.Helper()

	if violated.ClaimedBy == bridgelooptester.ClaimActorExternal {
		require.NotEqual(t, common.Hash{}, claimTx,
			"an external claim the tool identified must name the claim transaction it identified it from")
		require.NotEqual(t, common.Address{}, claimFrom,
			"an external claim the tool identified must name the claimant recovered from that transaction")

		return true
	}

	require.Equal(t, bridgelooptester.ClaimActorUnknown, violated.ClaimedBy)
	require.Equal(t, common.Hash{}, claimTx,
		"an unattributed claim must not carry a claim transaction: that is what would have named it")
	require.Equal(t, common.Address{}, claimFrom, "an unattributed claim must not carry a claimant")
	t.Logf("the destination did not serve a claim record for global_index=%s on network %d, so the "+
		"tool could not name the claimant; the violation itself is unaffected (it rests on the "+
		"destination bridge's own isClaimed read)", violated.GlobalIndex, violated.Destination)

	return false
}

// assertBridgeLoopViolationState asserts the halt is persisted, not just returned. A restart must
// find the loop marked halted with its class and its error, so it refuses to re-drive the ring
// silently (only `run --resume-halted` does that deliberately), and must find the violating hop
// still in flight so the value is not reported as home.
func assertBridgeLoopViolationState(
	t *testing.T, cfg *bridgelooptester.Config, report *bridgelooptester.Report,
) {
	t.Helper()

	raw, err := os.ReadFile(cfg.Global.StatePath)
	require.NoError(t, err, "the run must have persisted its state file at %s", cfg.Global.StatePath)
	var onDisk bridgelooptester.State
	require.NoError(t, json.Unmarshal(raw, &onDisk), "parse the persisted state file")
	require.Equal(t, bridgelooptester.StateVersion, onDisk.Version)

	record := onDisk.Loops[bridgeLoopViolationLoopName]
	require.NotNil(t, record, "the state file must hold a record for loop %q", bridgeLoopViolationLoopName)
	require.True(t, record.Halted, "the halt must survive a restart, or a restart would paper over it")
	require.Equal(t, bridgelooptester.FailureClaimMode, record.HaltClass)
	require.NotEmpty(t, record.LastError, "the persisted record must carry the violation's message")
	require.Equal(t, uint64(0), record.CyclesCompleted)
	require.Equal(t, uint64(1), record.CyclesAttempted)
	require.Equal(t, 1, record.HopIndex,
		"the resume cursor must stay on the violating hop, where the value actually is")
	require.NotNil(t, record.InFlight,
		"the violating hop must still be recorded as in flight: its value never reached the ring's origin")

	require.Len(t, report.Loops, 1)
	require.Equal(t, report.Loops[0].Err, record.LastError,
		"the persisted error and the reported one must be the same diagnosis")
}
