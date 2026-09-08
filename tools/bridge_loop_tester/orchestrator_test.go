package bridgelooptester_test

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"sync"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	"github.com/agglayer/aggkit/tools/bridge_loop_tester/mocks"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// bridgeAddressOf is the deterministic bridge address the test config uses for a network, so the
// preflight's "configured address agrees with what the proxy publishes" check has something real
// to compare.
func bridgeAddressOf(networkID uint32) common.Address {
	return common.BigToAddress(big.NewInt(int64(networkID) + 0xb00))
}

// signerAddressOf is the deterministic signing account of a network in the test config.
func signerAddressOf(networkID uint32) common.Address {
	return common.BigToAddress(big.NewInt(int64(networkID) + 0x5100))
}

// harness wires an Orchestrator over mocked networks, bridges and proxy, so the orchestrator's own
// behaviour (scheduling, retries, halting, state, pooling) is what is under test rather than the
// hop engine's.
type harness struct {
	t   *testing.T
	cfg *bridgelooptester.Config

	proxy    *mocks.Proxy
	clients  map[uint32]*mocks.NetworkClient
	bridges  map[uint32]*mocks.Bridge
	backends map[uint32]*mocks.EthBackend

	// clientBuilds counts how many NetworkClients were built per network, which is how the
	// "exactly one client per (network, signing key) pair" guarantee is asserted.
	clientBuilds map[uint32]int
	closes       map[uint32]int
	mu           sync.Mutex
}

// newHarness builds a 3-network config (0 = L1, 1 and 2 = L2s) with one closed ETH ring
// 0 -> 1 -> 2 -> 0, and mocks whose default expectations make the run-mode preflight pass.
func newHarness(t *testing.T) *harness {
	t.Helper()

	h := &harness{
		t:            t,
		proxy:        mocks.NewProxy(t),
		clients:      map[uint32]*mocks.NetworkClient{},
		bridges:      map[uint32]*mocks.Bridge{},
		backends:     map[uint32]*mocks.EthBackend{},
		clientBuilds: map[uint32]int{},
		closes:       map[uint32]int{},
	}

	h.cfg = &bridgelooptester.Config{
		Global: bridgelooptester.Global{
			ProxyURL:          "http://127.0.0.1:15601",
			LogLevel:          "debug",
			Iterations:        1,
			LoopDelay:         cfgtypes.NewDuration(time.Millisecond),
			HopTimeout:        cfgtypes.NewDuration(10 * time.Minute),
			PollInterval:      cfgtypes.NewDuration(time.Second),
			ManualGracePeriod: cfgtypes.NewDuration(time.Minute),
			HopAttempts:       3,
		},
		Networks: []bridgelooptester.Network{
			testNetwork(0, "L1"),
			testNetwork(1, "L2A"),
			testNetwork(2, "L2B"),
		},
		Loops: []bridgelooptester.Loop{ethRing("eth-ring")},
	}

	for _, networkID := range []uint32{0, 1, 2} {
		h.setupNetwork(networkID)
	}
	h.setupProxy()

	return h
}

// testNetwork builds one valid [[Networks]] entry.
func testNetwork(networkID uint32, name string) bridgelooptester.Network {
	return bridgelooptester.Network{
		NetworkID:  networkID,
		Name:       name,
		RPCURL:     fmt.Sprintf("http://127.0.0.1:1000%d", networkID),
		BridgeAddr: bridgeAddressOf(networkID),
		Signer: signertypes.SignerConfig{
			Method: signertypes.MethodLocal,
			Config: map[string]any{"path": fmt.Sprintf("/keystore/%d", networkID), "password": "x"},
		},
	}
}

// ethRing builds a closed ETH ring 0 -> 1 -> 2 -> 0 mixing auto and manual claims.
func ethRing(name string) bridgelooptester.Loop {
	return bridgelooptester.Loop{
		Name:    name,
		Asset:   bridgelooptester.AssetETH,
		Amount:  bridgelooptester.NewWeiAmount(1_000_000),
		Enabled: true,
		Hops: []bridgelooptester.Hop{
			{Source: 0, Destination: 1, Claim: bridgelooptester.ClaimAuto},
			{Source: 1, Destination: 2, Claim: bridgelooptester.ClaimManual},
			{Source: 2, Destination: 0, Claim: bridgelooptester.ClaimAuto},
		},
	}
}

// setupNetwork registers the client/bridge/backend mocks for one network with the loose default
// expectations a passing preflight needs.
func (h *harness) setupNetwork(networkID uint32) {
	h.t.Helper()

	client := mocks.NewNetworkClient(h.t)
	bridge := mocks.NewBridge(h.t)
	backend := mocks.NewEthBackend(h.t)

	name := h.cfg.Networks[networkID].Name
	client.EXPECT().NetworkID().Return(networkID).Maybe()
	client.EXPECT().Name().Return(name).Maybe()
	client.EXPECT().ChainID().Return(big.NewInt(int64(networkID) + 1000)).Maybe()
	client.EXPECT().From().Return(signerAddressOf(networkID)).Maybe()
	client.EXPECT().Backend().Return(backend).Maybe()
	client.EXPECT().NativeBalance(mock.Anything, mock.Anything).
		Return(big.NewInt(1_000_000_000_000_000_000), nil).Maybe()
	client.EXPECT().Close().Run(func() {
		h.mu.Lock()
		h.closes[networkID]++
		h.mu.Unlock()
	}).Maybe()

	bridge.EXPECT().Address().Return(bridgeAddressOf(networkID)).Maybe()
	bridge.EXPECT().NetworkID(mock.Anything).Return(networkID, nil).Maybe()
	bridge.EXPECT().GasTokenAddress(mock.Anything).Return(common.Address{}, nil).Maybe()
	bridge.EXPECT().WETHToken(mock.Anything).Return(common.Address{}, nil).Maybe()

	h.clients[networkID] = client
	h.bridges[networkID] = bridge
	h.backends[networkID] = backend
}

// setupProxy registers the read-only proxy calls the preflight makes.
func (h *harness) setupProxy() {
	h.t.Helper()

	h.proxy.EXPECT().Health(mock.Anything).
		Return(&trackertypes.HealthResponse{Status: "ok"}, nil).Maybe()

	for _, networkID := range []uint32{0, 1, 2} {
		contracts := bridgeservicetypes.PublicContractsConfig{}
		if networkID == 0 {
			contracts.L1.BridgeAddr = bridgeservicetypes.Address(bridgeAddressOf(0).Hex())
		} else {
			contracts.L2.BridgeAddr = bridgeservicetypes.Address(bridgeAddressOf(networkID).Hex())
		}
		h.proxy.EXPECT().BridgeAddresses(mock.Anything, networkID).
			Return(&bridgeservicetypes.PublicConfigResponse{
				NetworkID: networkID,
				Contracts: contracts,
			}, nil).Maybe()
	}
}

// deps returns OrchestratorDeps wired to the harness's mocks, with hopRunner as every loop's
// HopRunner.
func (h *harness) deps(hopRunner bridgelooptester.HopRunner, store bridgelooptester.StateStore) bridgelooptester.OrchestratorDeps {
	return bridgelooptester.OrchestratorDeps{
		Logger: log.GetDefaultLogger(),
		Proxy:  h.proxy,
		Store:  store,
		NewNetworkClientFn: func(
			_ context.Context, cfg bridgelooptester.Network, _ aggkitcommon.Logger,
		) (bridgelooptester.NetworkClient, error) {
			h.mu.Lock()
			h.clientBuilds[cfg.NetworkID]++
			h.mu.Unlock()

			return h.clients[cfg.NetworkID], nil
		},
		NewBridgeFn: func(
			client bridgelooptester.NetworkClient, _ common.Address,
		) (bridgelooptester.Bridge, error) {
			return h.bridges[client.NetworkID()], nil
		},
		NewHopRunnerFn: func(string) (bridgelooptester.HopRunner, error) { return hopRunner, nil },
	}
}

// fakeHopRunner is a scriptable HopRunner: it records every request and answers from a per-call
// function, so a test can make a specific hop of a specific cycle fail in a specific way.
type fakeHopRunner struct {
	mu       sync.Mutex
	requests []bridgelooptester.HopRequest
	respond  func(req bridgelooptester.HopRequest, call int) (*bridgelooptester.HopResult, error)
	calls    int
}

// RunHop implements bridgelooptester.HopRunner.
func (f *fakeHopRunner) RunHop(
	_ context.Context, req bridgelooptester.HopRequest,
) (*bridgelooptester.HopResult, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.requests = append(f.requests, req)
	respond := f.respond
	f.mu.Unlock()

	if respond != nil {
		return respond(req, call)
	}

	return successResult(req), nil
}

// snapshot returns the recorded requests.
func (f *fakeHopRunner) snapshot() []bridgelooptester.HopRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]bridgelooptester.HopRequest(nil), f.requests...)
}

// successResult builds the HopResult a successful hop would produce, populated richly enough that
// the report aggregation can be asserted on (per-phase timings, leaf indices, claim actor).
func successResult(req bridgelooptester.HopRequest) *bridgelooptester.HopResult {
	claimedBy := bridgelooptester.ClaimActorExternal
	if req.Hop.Claim == bridgelooptester.ClaimManual {
		claimedBy = bridgelooptester.ClaimActorTool
	}
	startedAt := time.Now().Add(-time.Second)

	return &bridgelooptester.HopResult{
		LoopName:             req.LoopName,
		Iteration:            req.Iteration,
		HopIndex:             req.HopIndex,
		Source:               req.Hop.Source,
		Destination:          req.Hop.Destination,
		ClaimMode:            req.Hop.Claim,
		Asset:                req.Asset,
		Amount:               req.Amount,
		DepositCount:         uint32(req.HopIndex) + 1,
		L1InfoTreeIndex:      10,
		InjectedLeafIndex:    11,
		InjectedLeafAdvanced: true,
		ClaimedBy:            claimedBy,
		Outcome:              bridgelooptester.HopOutcomeSuccess,
		FinalState:           bridgelooptester.HopStateVerified,
		States: []bridgelooptester.HopState{
			bridgelooptester.HopStatePending, bridgelooptester.HopStateVerified,
		},
		Phases: []bridgelooptester.HopPhaseTiming{
			{Phase: bridgelooptester.PhaseBridge, StartedAt: startedAt, Duration: 300 * time.Millisecond},
			{Phase: bridgelooptester.PhaseVerifyBalance, StartedAt: startedAt, Duration: time.Millisecond},
		},
		StartedAt:       startedAt,
		FinishedAt:      startedAt.Add(time.Second),
		Duration:        time.Second,
		BalanceVerified: true,
		Checkpoint: bridgelooptester.HopCheckpoint{
			State:        bridgelooptester.HopStateVerified,
			BridgeTxHash: common.BigToHash(big.NewInt(int64(req.HopIndex) + 1)),
			DepositCount: uint32(req.HopIndex) + 1,
		},
	}
}

// failedResult builds the HopResult a failed hop attempt would produce, checkpointed at state.
func failedResult(
	req bridgelooptester.HopRequest, state bridgelooptester.HopState, err error,
) *bridgelooptester.HopResult {
	result := successResult(req)
	result.Outcome = bridgelooptester.HopOutcomeFailed
	result.FinalState = bridgelooptester.HopStateFailed
	result.Err = err
	result.ErrMessage = err.Error()
	result.Checkpoint.State = state
	result.ClaimedBy = bridgelooptester.ClaimActorNone
	result.ClaimModeViolated = errors.Is(err, bridgelooptester.ErrClaimModeViolation)

	var gateErr *bridgelooptester.DeadlineExceededError
	if errors.As(err, &gateErr) {
		result.StalledGate = gateErr.Gate
	}

	return result
}

func TestRunDrivesEveryHopOfEveryCycleForEveryEnabledLoop(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 2
	h.cfg.Loops = append(h.cfg.Loops, bridgelooptester.Loop{
		Name:    "disabled-ring",
		Asset:   bridgelooptester.AssetETH,
		Amount:  bridgelooptester.NewWeiAmount(1),
		Enabled: false, // Enabled has no implicit true-default: this loop must not be driven.
		Hops: []bridgelooptester.Hop{
			{Source: 0, Destination: 1, Claim: bridgelooptester.ClaimAuto},
			{Source: 1, Destination: 0, Claim: bridgelooptester.ClaimAuto},
		},
	})

	runner := &fakeHopRunner{}
	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(runner, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.NoError(t, err)
	require.NotNil(t, report)

	// Only the enabled loop ran, for exactly Iterations cycles of 3 hops each.
	require.Len(t, report.Loops, 1)
	require.Equal(t, "eth-ring", report.Loops[0].Name)
	require.Equal(t, uint64(2), report.Loops[0].CyclesAttempted)
	require.Equal(t, uint64(2), report.Loops[0].CyclesCompleted)
	require.Len(t, report.Loops[0].Cycles, 2)
	require.Len(t, report.AllHops(), 6)
	// The per-cycle timings reach the caller (a named result, so the defer that stamps them is not
	// stamping a copy).
	for _, cycle := range report.Loops[0].Cycles {
		require.False(t, cycle.FinishedAt.IsZero())
		require.Equal(t, cycle.FinishedAt.Sub(cycle.StartedAt), cycle.Duration)
	}
	require.Equal(t, 6, report.Totals.HopsAttempted)
	require.Equal(t, 6, report.Totals.HopsSucceeded)
	require.Zero(t, report.Totals.HopsFailed)
	require.Equal(t, uint64(2), report.Totals.CyclesCompleted)
	require.True(t, report.Succeeded())
	require.False(t, report.Cancelled)

	// The hops ran in configured order, cycle after cycle.
	requests := runner.snapshot()
	require.Len(t, requests, 6)
	for i, req := range requests {
		require.Equal(t, i%3, req.HopIndex)
		require.Equal(t, h.cfg.Loops[0].Hops[i%3], req.Hop)
		require.Equal(t, uint64(i/3)+1, req.Iteration)
		require.Nil(t, req.Resume, "a fresh hop must not be handed a resume checkpoint")
	}

	// No detail is lost in the aggregation: the fields S10 asserts on survive.
	first := report.Loops[0].Cycles[0].Hops[0]
	require.True(t, first.InjectedLeafAdvanced)
	require.Equal(t, bridgelooptester.ClaimActorExternal, first.ClaimedBy)
	require.Len(t, first.Phases, 2)
	require.Equal(t, bridgelooptester.PhaseBridge, first.Phases[0].Phase)
	require.Equal(t, 300*time.Millisecond, first.Phases[0].Duration)
	require.Equal(t, 6, report.Totals.InjectedLeafAdvanced)
	require.Equal(t, 4, report.Totals.AutoClaimHops)
	require.Equal(t, 2, report.Totals.ManualClaimHops)
	require.Equal(t, 2, report.Totals.ClaimedByTool)
	require.Equal(t, 4, report.Totals.ClaimedExternally)

	// The ring closed, so the value is back where it started and nothing is stranded.
	require.False(t, report.Loops[0].ValueLocation.Stranded)
	require.Equal(t, uint32(0), report.Loops[0].ValueLocation.NetworkID)
	require.Zero(t, report.Totals.StrandedLoops)
}

// TestNetworkPoolBuildsExactlyOneClientPerNetwork pins the nonce-serialization guarantee: SendTx
// serializes nonces per NetworkClient instance, so a second client for the same key on the same
// network would collide and wedge the account.
func TestNetworkPoolBuildsExactlyOneClientPerNetwork(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 3
	// A second loop over the same networks: both loops must share the one client per network.
	h.cfg.Loops = append(h.cfg.Loops, ethRing("second-ring"))

	runner := &fakeHopRunner{}
	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(runner, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)

	_, err = orchestrator.Run(context.Background())
	require.NoError(t, err)

	h.mu.Lock()
	builds := map[uint32]int{}
	for networkID, count := range h.clientBuilds {
		builds[networkID] = count
	}
	h.mu.Unlock()
	require.Equal(t, map[uint32]int{0: 1, 1: 1, 2: 1}, builds,
		"exactly one NetworkClient per (network, signing key) pair, shared by every loop")

	orchestrator.Close()
	h.mu.Lock()
	closes := map[uint32]int{}
	for networkID, count := range h.closes {
		closes[networkID] = count
	}
	h.mu.Unlock()
	require.Equal(t, map[uint32]int{0: 1, 1: 1, 2: 1}, closes, "every pooled client is closed once")
}

func TestNetworkPoolRefusesTwoKeysForOneNetwork(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	// Same NetworkID, different signer: the pool cannot decide which key a hop should use. (The
	// config validator rejects duplicate NetworkIDs too; this is the pool's own defence.)
	duplicate := testNetwork(1, "L2A-other")
	duplicate.Signer.Config = map[string]any{"path": "/keystore/other", "password": "y"}
	cfg := *h.cfg
	cfg.Networks = append([]bridgelooptester.Network{}, h.cfg.Networks...)
	cfg.Networks = append(cfg.Networks, duplicate)

	_, err := bridgelooptester.NewOrchestrator(
		context.Background(), &cfg, h.deps(&fakeHopRunner{}, bridgelooptester.NoopStateStore{}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate NetworkID")
}

func TestRunRetriesTransientHopFailureResumingFromItsCheckpoint(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 1

	transient := &bridgelooptester.DeadlineExceededError{
		Gate:     "claim-proof",
		Detail:   "network_id=1 leaf_index=11 deposit_count=2",
		Deadline: time.Minute,
	}

	runner := &fakeHopRunner{}
	runner.respond = func(
		req bridgelooptester.HopRequest, _ int,
	) (*bridgelooptester.HopResult, error) {
		// Hop 1 fails on its first attempt only.
		if req.HopIndex == 1 && req.Resume == nil {
			return failedResult(req, bridgelooptester.HopStateFetchingClaimProof, transient), transient
		}

		return successResult(req), nil
	}

	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(runner, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.NoError(t, err)
	require.True(t, report.Loops[0].Cycles[0].RingClosed)
	require.False(t, report.Loops[0].Halted)
	require.Equal(t, uint64(1), report.Loops[0].CyclesCompleted)

	// Four attempts for three hops: hop 1 was retried, and the retry resumed from the failed
	// attempt's checkpoint rather than starting the hop over (which would bridge twice).
	requests := runner.snapshot()
	require.Len(t, requests, 4)
	require.Equal(t, 1, requests[1].HopIndex)
	require.Nil(t, requests[1].Resume)
	require.Equal(t, 1, requests[2].HopIndex)
	require.NotNil(t, requests[2].Resume)
	require.Equal(t, bridgelooptester.HopStateFetchingClaimProof, requests[2].Resume.State)

	require.Equal(t, 4, report.Totals.HopsAttempted)
	require.Equal(t, 3, report.Totals.HopsSucceeded)
	require.Equal(t, 1, report.Totals.HopsFailed)
	require.Equal(t, 1, report.Totals.HopsRetried)
	require.Equal(t, "claim-proof", report.AllHops()[1].StalledGate)
}

// TestRunAbandonsCycleAfterExhaustingRetriesAndResumesRingNextCycle is the "does not silently drop
// a broken ring" half of the failure policy: a hop that will not succeed ends its cycle, but the
// loop keeps its cursor and the next cycle resumes at that hop rather than starting over at hop 0
// (which would try to spend value that is stranded elsewhere).
func TestRunAbandonsCycleAfterExhaustingRetriesAndResumesRingNextCycle(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 2
	transient := fmt.Errorf("the destination RPC refused the connection")

	runner := &fakeHopRunner{}
	var failuresLeft = 3 // exactly HopAttempts failures, so cycle 1 gives up on hop 1
	runner.respond = func(
		req bridgelooptester.HopRequest, _ int,
	) (*bridgelooptester.HopResult, error) {
		if req.HopIndex == 1 && failuresLeft > 0 {
			failuresLeft--

			return failedResult(req, bridgelooptester.HopStateBridged, transient), transient
		}

		return successResult(req), nil
	}

	store := bridgelooptester.NoopStateStore{}
	orchestrator, err := bridgelooptester.NewOrchestrator(context.Background(), h.cfg, h.deps(runner, store))
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.NoError(t, err, "a transient failure must never halt the loop")

	require.Len(t, report.Loops[0].Cycles, 2)
	require.False(t, report.Loops[0].Cycles[0].RingClosed)
	require.Equal(t, bridgelooptester.FailureTransient, report.Loops[0].Cycles[0].FailureClass)
	require.True(t, report.Loops[0].Cycles[1].RingClosed)
	require.False(t, report.Loops[0].Halted)

	// The second cycle started at the stranded hop, not at hop 0.
	require.Equal(t, 1, report.Loops[0].Cycles[1].StartHopIndex)
	require.Equal(t, 1, report.Loops[0].Cycles[1].Hops[0].HopIndex)

	// Cycle 1: hop 0 ok, hop 1 failed 3 times. Cycle 2: hops 1 and 2.
	require.Len(t, report.Loops[0].Cycles[0].Hops, 4)
	require.Len(t, report.Loops[0].Cycles[1].Hops, 2)
	require.Equal(t, uint64(1), report.Loops[0].CyclesCompleted)
	require.Equal(t, uint64(2), report.Loops[0].CyclesAttempted)
}

// TestRunHonoursConfiguredHopAttempts pins that the orchestrator reads Global.HopAttempts from the
// config rather than always retrying the hard-coded default of 3: a config that sets it to 5 must
// retry a persistently-transient hop 5 times (not 3) before abandoning the cycle.
func TestRunHonoursConfiguredHopAttempts(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 1
	h.cfg.Global.HopAttempts = 5
	transient := fmt.Errorf("the destination RPC refused the connection")

	runner := &fakeHopRunner{}
	runner.respond = func(
		req bridgelooptester.HopRequest, _ int,
	) (*bridgelooptester.HopResult, error) {
		if req.HopIndex == 1 {
			return failedResult(req, bridgelooptester.HopStateBridged, transient), transient
		}

		return successResult(req), nil
	}

	store := bridgelooptester.NoopStateStore{}
	orchestrator, err := bridgelooptester.NewOrchestrator(context.Background(), h.cfg, h.deps(runner, store))
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.NoError(t, err, "a transient failure must never halt the loop")

	require.Len(t, report.Loops[0].Cycles, 1)
	// hop 0 succeeded once, hop 1 was attempted Global.HopAttempts (5) times, all failing.
	require.Len(t, report.Loops[0].Cycles[0].Hops, 6)
	require.Equal(t, 6, report.Totals.HopsAttempted)
	require.Equal(t, 1, report.Totals.HopsSucceeded)
	require.Equal(t, 5, report.Totals.HopsFailed)
	require.Equal(t, 4, report.Totals.HopsRetried)
}

func TestRunHaltsLoopOnClaimModeViolation(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 5

	violation := &bridgelooptester.ClaimModeViolationError{
		Expected:         bridgelooptester.ClaimManual,
		Observed:         bridgelooptester.ClaimActorExternal,
		Source:           1,
		Destination:      2,
		DepositCount:     7,
		GlobalIndex:      big.NewInt(4294967303),
		GracePeriod:      time.Minute,
		BridgeTxHash:     common.HexToHash("0xb0"),
		ClaimTxHash:      common.HexToHash("0xc0"),
		ClaimFromAddress: common.HexToAddress("0xaa"),
		ProofAvailable:   true,
	}

	runner := &fakeHopRunner{}
	runner.respond = func(
		req bridgelooptester.HopRequest, _ int,
	) (*bridgelooptester.HopResult, error) {
		if req.HopIndex != 1 {
			return successResult(req), nil
		}
		return failedResult(req, bridgelooptester.HopStateAwaitingClaim, violation), violation
	}

	statePath := filepath.Join(t.TempDir(), "state.json")
	store, err := bridgelooptester.NewFileStateStore(statePath)
	require.NoError(t, err)

	orchestrator, err := bridgelooptester.NewOrchestrator(context.Background(), h.cfg, h.deps(runner, store))
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.Error(t, err, "a claim-mode violation is a test failure and must surface as one")
	require.Contains(t, err.Error(), "claim-mode-violation")

	require.True(t, report.Loops[0].Halted)
	require.Equal(t, bridgelooptester.FailureClaimMode, report.Loops[0].HaltClass)
	require.Equal(t, 1, report.Totals.ClaimModeViolations)
	require.Equal(t, 1, report.Totals.LoopsHalted)
	require.False(t, report.Succeeded())

	// It is never retried: exactly two attempts happened (hop 0 succeeded, hop 1 violated once).
	require.Len(t, runner.snapshot(), 2)

	// The loop stopped after the first cycle even though Iterations was 5.
	require.Equal(t, uint64(1), report.Loops[0].CyclesAttempted)

	// The value is reported as stranded on the failing hop's source network, in flight.
	require.True(t, report.Loops[0].ValueLocation.Stranded)
	require.Equal(t, uint32(1), report.Loops[0].ValueLocation.NetworkID)
	require.Contains(t, report.Loops[0].ValueLocation.Detail, "STRANDED")

	// The halt is persisted, so a restart does not paper over the test failure...
	persisted, err := store.Load(context.Background())
	require.NoError(t, err)
	require.True(t, persisted.Loop("eth-ring").Halted)
	require.Equal(t, bridgelooptester.FailureClaimMode, persisted.Loop("eth-ring").HaltClass)

	// ...and a fresh orchestrator over the same state file refuses to re-drive it.
	restarted, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(&fakeHopRunner{}, store))
	require.NoError(t, err)
	defer restarted.Close()

	restartReport, err := restarted.Run(context.Background())
	require.Error(t, err)
	require.True(t, restartReport.Loops[0].Halted)
	require.Empty(t, restartReport.Loops[0].Cycles, "a halted loop must not run another cycle")
}

func TestRunHaltsLoopOnAmbiguousResumeAndReportsTheEvidence(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 4

	ambiguous := &bridgelooptester.AmbiguousResumeError{
		State:         bridgelooptester.HopStateBridging,
		Source:        1,
		Destination:   2,
		Account:       signerAddressOf(1),
		Amount:        big.NewInt(1_000_000),
		BridgeTxHash:  common.HexToHash("0xdeadbeef"),
		BridgeTxNonce: 41,
		AccountNonce:  42,
		PendingNonce:  42,
		ReceiptWait:   2 * time.Minute,
	}

	runner := &fakeHopRunner{}
	runner.respond = func(
		req bridgelooptester.HopRequest, _ int,
	) (*bridgelooptester.HopResult, error) {
		if req.HopIndex != 1 {
			return successResult(req), nil
		}

		return failedResult(req, bridgelooptester.HopStateBridging, ambiguous), ambiguous
	}

	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(runner, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.Error(t, err)
	require.True(t, report.Loops[0].Halted)
	require.Equal(t, bridgelooptester.FailureAmbiguousResume, report.Loops[0].HaltClass)
	require.Equal(t, 1, report.Totals.AmbiguousResumes)

	// Never retried, never skipped: the hop was attempted exactly once and the loop stopped.
	require.Len(t, runner.snapshot(), 2)
	require.Equal(t, uint64(1), report.Loops[0].CyclesAttempted)

	// The full decision evidence survives into the report through the hop's error message.
	require.Contains(t, report.Loops[0].Err, "deadbeef")
	require.Contains(t, report.Loops[0].Err, "nonce")
}

func TestRunHaltsLoopOnInsufficientBalance(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 4

	shortfall := &bridgelooptester.InsufficientBalanceError{
		Network:          "L2A",
		NetworkID:        1,
		Account:          signerAddressOf(1),
		Asset:            bridgelooptester.AssetETH,
		Required:         big.NewInt(1_000_000),
		Available:        big.NewInt(1),
		MinNativeReserve: big.NewInt(500),
	}

	runner := &fakeHopRunner{}
	runner.respond = func(
		req bridgelooptester.HopRequest, _ int,
	) (*bridgelooptester.HopResult, error) {
		if req.HopIndex != 1 {
			return successResult(req), nil
		}

		return failedResult(req, bridgelooptester.HopStatePending, shortfall), shortfall
	}

	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(runner, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.Error(t, err)
	require.True(t, report.Loops[0].Halted)
	require.Equal(t, bridgelooptester.FailureInsufficientBalance, report.Loops[0].HaltClass)
	require.Len(t, runner.snapshot(), 2, "an unfundable hop must not be retried for days")

	// Nothing was bridged for that hop, so the value never left its source network.
	require.True(t, report.Loops[0].ValueLocation.Stranded)
	require.False(t, report.Loops[0].ValueLocation.InFlight)
}

func TestRunStopsOnContextCancellationAndFlushesState(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 0 // forever, so only the cancellation can end the run
	h.cfg.Global.LoopDelay = cfgtypes.NewDuration(time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := &fakeHopRunner{}
	runner.respond = func(
		req bridgelooptester.HopRequest, call int,
	) (*bridgelooptester.HopResult, error) {
		if call >= 5 {
			cancel()
		}

		return successResult(req), nil
	}

	statePath := filepath.Join(t.TempDir(), "state.json")
	store, err := bridgelooptester.NewFileStateStore(statePath)
	require.NoError(t, err)

	orchestrator, err := bridgelooptester.NewOrchestrator(ctx, h.cfg, h.deps(runner, store))
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(ctx)
	require.NoError(t, err, "a cancelled run is not a test failure")
	require.True(t, report.Cancelled)
	require.True(t, report.Succeeded())
	require.False(t, report.Loops[0].Halted)
	require.NotEmpty(t, report.Loops[0].Cycles)

	// The state was flushed on the way out and is resumable.
	persisted, err := store.Load(context.Background())
	require.NoError(t, err)
	require.False(t, persisted.UpdatedAt.IsZero())
	require.NotZero(t, persisted.Loop("eth-ring").CyclesAttempted)
}

func TestRunResumesAStrandedRingFromThePersistedCursor(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 1

	statePath := filepath.Join(t.TempDir(), "state.json")
	store, err := bridgelooptester.NewFileStateStore(statePath)
	require.NoError(t, err)

	// A previous run left the ring stranded mid-hop-1, with a bridge transaction already signed.
	seeded := bridgelooptester.NewState()
	record := seeded.Loop("eth-ring")
	record.HopIndex = 1
	record.ValueNetwork = 1
	record.CyclesAttempted = 4
	record.CyclesCompleted = 3
	record.InFlight = &bridgelooptester.HopCheckpoint{
		State:         bridgelooptester.HopStateBridging,
		BridgeTxHash:  common.HexToHash("0xfeed"),
		BridgeTxNonce: 12,
	}
	require.NoError(t, store.Save(context.Background(), seeded))

	runner := &fakeHopRunner{}
	orchestrator, err := bridgelooptester.NewOrchestrator(context.Background(), h.cfg, h.deps(runner, store))
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.NoError(t, err)

	requests := runner.snapshot()
	require.Len(t, requests, 2, "a resumed ring runs only the hops it had left")
	require.Equal(t, 1, requests[0].HopIndex)
	require.NotNil(t, requests[0].Resume, "the in-flight checkpoint must be handed back to the engine")
	require.Equal(t, bridgelooptester.HopStateBridging, requests[0].Resume.State)
	require.Equal(t, common.HexToHash("0xfeed"), requests[0].Resume.BridgeTxHash)
	require.Equal(t, uint64(12), requests[0].Resume.BridgeTxNonce)
	require.Equal(t, 2, requests[1].HopIndex)
	require.Nil(t, requests[1].Resume)

	require.Equal(t, 1, report.Loops[0].Cycles[0].StartHopIndex)
	require.True(t, report.Loops[0].Cycles[0].RingClosed)
	require.Equal(t, uint64(5), report.Loops[0].Cycles[0].Iteration,
		"the iteration counter is cumulative across restarts")

	persisted, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Zero(t, persisted.Loop("eth-ring").HopIndex)
	require.Nil(t, persisted.Loop("eth-ring").InFlight)
	require.Equal(t, uint64(4), persisted.Loop("eth-ring").CyclesCompleted)
}

// TestRunDiscardsAResumeCheckpointForADifferentHop guards against resuming the wrong deposit: a
// checkpoint recorded while the value sat on another network cannot belong to the hop about to run.
func TestRunDiscardsAResumeCheckpointForADifferentHop(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 1

	store, err := bridgelooptester.NewFileStateStore(filepath.Join(t.TempDir(), "state.json"))
	require.NoError(t, err)

	seeded := bridgelooptester.NewState()
	record := seeded.Loop("eth-ring")
	record.HopIndex = 0
	record.ValueNetwork = 2 // the value is recorded on network 2, but hop 0's source is network 0
	record.InFlight = &bridgelooptester.HopCheckpoint{
		State:        bridgelooptester.HopStateBridged,
		BridgeTxHash: common.HexToHash("0xstale"),
	}
	require.NoError(t, store.Save(context.Background(), seeded))

	runner := &fakeHopRunner{}
	orchestrator, err := bridgelooptester.NewOrchestrator(context.Background(), h.cfg, h.deps(runner, store))
	require.NoError(t, err)
	defer orchestrator.Close()

	_, err = orchestrator.Run(context.Background())
	require.NoError(t, err)

	require.Nil(t, runner.snapshot()[0].Resume,
		"a checkpoint whose value network does not match the hop's source must be discarded")
}

func TestRunResumeHaltedRedrivesAHaltedLoop(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 1

	store, err := bridgelooptester.NewFileStateStore(filepath.Join(t.TempDir(), "state.json"))
	require.NoError(t, err)

	seeded := bridgelooptester.NewState()
	record := seeded.Loop("eth-ring")
	record.Halted = true
	record.HaltClass = bridgelooptester.FailureClaimMode
	record.LastError = "an earlier run saw a claim-mode violation"
	require.NoError(t, store.Save(context.Background(), seeded))

	runner := &fakeHopRunner{}
	deps := h.deps(runner, store)
	deps.ResumeHalted = true

	orchestrator, err := bridgelooptester.NewOrchestrator(context.Background(), h.cfg, deps)
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.NoError(t, err)
	require.False(t, report.Loops[0].Halted)
	require.Len(t, runner.snapshot(), 3)
}

func TestRunDryRunSubmitsNothingAndReportsAPlan(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.DryRun = true
	h.cfg.Global.Iterations = 3

	// A hop runner that must never be called: DryRun submits no transaction at all.
	runner := &fakeHopRunner{}
	runner.respond = func(
		bridgelooptester.HopRequest, int,
	) (*bridgelooptester.HopResult, error) {
		t.Fatal("DryRun must not run a hop")

		return nil, nil
	}

	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(runner, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.NoError(t, err)
	require.True(t, report.DryRun)
	require.Empty(t, runner.snapshot())
	require.Len(t, report.Loops, 1)
	require.Empty(t, report.Loops[0].Cycles)
	require.Len(t, report.Loops[0].Plan, 3)
	require.True(t, report.Loops[0].Plan[0].Fundable)
	require.Equal(t, "0->1", report.Loops[0].Plan[0].Route())
	require.Equal(t, signerAddressOf(0), report.Loops[0].Plan[0].From)
}

func TestRunRefusesWhenThePreflightRefuses(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	// Gap G3: network 2's gas token is not ether, so the ETH ring touching it cannot run.
	h.bridges[2] = mocks.NewBridge(t)
	h.bridges[2].EXPECT().Address().Return(bridgeAddressOf(2)).Maybe()
	h.bridges[2].EXPECT().NetworkID(mock.Anything).Return(uint32(2), nil).Maybe()
	h.bridges[2].EXPECT().GasTokenAddress(mock.Anything).
		Return(common.HexToAddress("0xfeefee"), nil).Maybe()
	h.bridges[2].EXPECT().WETHToken(mock.Anything).
		Return(common.HexToAddress("0xwe7h"), nil).Maybe()

	runner := &fakeHopRunner{}
	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(runner, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "preflight refused")
	require.Contains(t, err.Error(), "gasTokenAddress() is not the zero address")
	require.Empty(t, runner.snapshot(), "no hop may run when the preflight refuses")
	require.NotNil(t, report.Preflight)
	require.False(t, report.Preflight.OK())
}

func TestNewOrchestratorRejectsAnInvalidConfig(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Loops[0].Enabled = false

	_, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(&fakeHopRunner{}, bridgelooptester.NoopStateStore{}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "no loop is enabled")
}

func TestRunRequiresAConfig(t *testing.T) {
	t.Parallel()

	_, err := bridgelooptester.Run(context.Background(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "config is required")
}

func TestClassifyHopFailure(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		err  error
		want bridgelooptester.FailureClass
	}{
		{name: "no error", err: nil, want: bridgelooptester.FailureNone},
		{name: "cancelled", err: context.Canceled, want: bridgelooptester.FailureCancelled},
		{name: "deadline", err: context.DeadlineExceeded, want: bridgelooptester.FailureCancelled},
		{
			name: "claim mode violation",
			err:  fmt.Errorf("wrapped: %w", &bridgelooptester.ClaimModeViolationError{}),
			want: bridgelooptester.FailureClaimMode,
		},
		{
			name: "ambiguous resume",
			err:  fmt.Errorf("wrapped: %w", &bridgelooptester.AmbiguousResumeError{}),
			want: bridgelooptester.FailureAmbiguousResume,
		},
		{
			name: "insufficient balance",
			err:  fmt.Errorf("wrapped: %w", &bridgelooptester.InsufficientBalanceError{}),
			want: bridgelooptester.FailureInsufficientBalance,
		},
		{
			name: "gate deadline is transient",
			err:  &bridgelooptester.DeadlineExceededError{Gate: "claim-proof"},
			want: bridgelooptester.FailureTransient,
		},
		{
			name: "balance mismatch is transient",
			err:  &bridgelooptester.BalanceMismatchError{},
			want: bridgelooptester.FailureTransient,
		},
		{name: "anything else is transient", err: fmt.Errorf("rpc broke"), want: bridgelooptester.FailureTransient},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, bridgelooptester.ClassifyHopFailure(tc.err))
		})
	}
}

func TestFailureClassFatal(t *testing.T) {
	t.Parallel()

	require.False(t, bridgelooptester.FailureNone.Fatal())
	require.False(t, bridgelooptester.FailureTransient.Fatal())
	require.False(t, bridgelooptester.FailureCancelled.Fatal())
	require.True(t, bridgelooptester.FailureClaimMode.Fatal())
	require.True(t, bridgelooptester.FailureAmbiguousResume.Fatal())
	require.True(t, bridgelooptester.FailureInsufficientBalance.Fatal())
	require.True(t, bridgelooptester.FailureConfiguration.Fatal())
	require.True(t, bridgelooptester.FailureClass("something-new").Fatal(),
		"an unknown class must be treated as fatal rather than retried blindly")
}

// TestOrchestratorDefaultHopRunnerWiresRealHopEngine exercises the production HopRunner wiring
// that every other orchestrator test bypasses with a fakeHopRunner: NewOrchestrator's default
// newHopRunner (Orchestrator.newHopEngine), which renders the client pool through hopNetworks and
// builds a real *HopEngine backed by Orchestrator.persistCheckpoint. A wiring bug here (wrong
// network map, wrong loop threaded into the persistence closure, wrong timings) would only ever
// surface once something drives the tool against a live network - this is the cheap place to
// catch it.
func TestOrchestratorDefaultHopRunnerWiresRealHopEngine(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 1

	// Hop 0 (network 0 -> 1) never succeeds; a plain error classifies as transient, so it is
	// retried defaultHopAttempts times and then abandons the cycle without halting the loop -
	// exercising the real engine's HopStateBridging checkpoint (persisted before signing) without
	// needing to mock a whole transaction lifecycle.
	bridgeErr := errors.New("boom: rpc rejected the bridge submission")
	h.bridges[0].EXPECT().BridgeAssetNative(mock.Anything, mock.Anything).Return(nil, bridgeErr)

	statePath := filepath.Join(t.TempDir(), "state.json")
	store, err := bridgelooptester.NewFileStateStore(statePath)
	require.NoError(t, err)

	deps := bridgelooptester.OrchestratorDeps{
		Logger: log.GetDefaultLogger(),
		Proxy:  h.proxy,
		Store:  store,
		NewNetworkClientFn: func(
			_ context.Context, cfg bridgelooptester.Network, _ aggkitcommon.Logger,
		) (bridgelooptester.NetworkClient, error) {
			return h.clients[cfg.NetworkID], nil
		},
		NewBridgeFn: func(
			client bridgelooptester.NetworkClient, _ common.Address,
		) (bridgelooptester.Bridge, error) {
			return h.bridges[client.NetworkID()], nil
		},
		// NewHopRunnerFn is deliberately left nil: this test's whole point is to drive
		// Orchestrator.newHopEngine, not a test double.
	}

	orchestrator, err := bridgelooptester.NewOrchestrator(context.Background(), h.cfg, deps)
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.NoError(t, err, "a transient hop failure must never halt the loop or surface as a run error")
	require.False(t, report.Loops[0].Halted)
	require.Equal(t, 3, report.Totals.HopsAttempted, "the real engine must have been retried hopAttempts times")
	require.Equal(t, 3, report.Totals.HopsFailed)

	// The checkpoint observed through the exported State() snapshot was written by the real
	// HopEngine calling Orchestrator.persistCheckpoint, proving hopNetworks/newHopEngine/
	// persistCheckpoint/State are wired together correctly, not just the fakeHopRunner path every
	// other test in this file exercises.
	state := orchestrator.State()
	require.NotNil(t, state)
	inFlight := state.Loop("eth-ring").InFlight
	require.NotNil(t, inFlight, "the real hop engine must have checkpointed its bridging attempt")
	require.Equal(t, bridgelooptester.HopStateBridging, inFlight.State)
	require.Equal(t, common.Hash{}, inFlight.BridgeTxHash,
		"the mocked Bridge never reached signing, so the checkpoint must still show the pre-signing hash")
}

// TestOrchestratorHaltsLoopWhenHopRunnerCannotBeBuilt covers runLoop's other halt path (the loop's
// HopRunner factory itself fails) and Orchestrator.haltLoop, neither of which any other test in
// this file reaches - every other halt scenario is a hop failing after the runner was built.
func TestOrchestratorHaltsLoopWhenHopRunnerCannotBeBuilt(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 1

	statePath := filepath.Join(t.TempDir(), "state.json")
	store, err := bridgelooptester.NewFileStateStore(statePath)
	require.NoError(t, err)

	buildErr := errors.New("boom: could not construct the hop engine")
	deps := h.deps(&fakeHopRunner{}, store)
	deps.NewHopRunnerFn = func(string) (bridgelooptester.HopRunner, error) { return nil, buildErr }

	orchestrator, err := bridgelooptester.NewOrchestrator(context.Background(), h.cfg, deps)
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.Error(t, err)
	require.True(t, report.Loops[0].Halted)
	require.Equal(t, bridgelooptester.FailureConfiguration, report.Loops[0].HaltClass)
	require.Contains(t, report.Loops[0].Err, "boom: could not construct the hop engine")

	persisted, err := store.Load(context.Background())
	require.NoError(t, err)
	require.True(t, persisted.Loop("eth-ring").Halted)
	require.Equal(t, bridgelooptester.FailureConfiguration, persisted.Loop("eth-ring").HaltClass)
}

// TestRunDryRunReportsAnERC20Plan covers the ERC20 half of DryRun planning (planERC20Hop,
// resolveTokenOn): TestRunDryRunSubmitsNothingAndReportsAPlan only exercises the ETH branch of
// planHop, leaving the token-resolution path untested at the orchestrator level (the hop *engine*
// covers ERC20 balance handling separately, in hop_test.go).
func TestRunDryRunReportsAnERC20Plan(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.DryRun = true
	h.cfg.Global.Iterations = 1

	originNetwork := uint32(0)
	tokenAddr := common.HexToAddress("0x9000000000000000000000000000000000000e")
	wrappedAddr := common.HexToAddress("0x9000000000000000000000000000000000000f")
	h.cfg.Loops = []bridgelooptester.Loop{{
		Name:               "erc20-ring",
		Asset:              bridgelooptester.AssetERC20,
		Amount:             bridgelooptester.NewWeiAmount(500),
		Enabled:            true,
		TokenOriginNetwork: &originNetwork,
		Hops: []bridgelooptester.Hop{
			{Source: 0, Destination: 1, Claim: bridgelooptester.ClaimAuto},
			{Source: 1, Destination: 0, Claim: bridgelooptester.ClaimManual},
		},
	}}

	// Network 1 is not the token's origin, so resolveTokenOn asks its bridge for the wrapped
	// representation - both when network 1 is the hop's destination (hop 0) and when it is the
	// hop's source (hop 1).
	h.bridges[1].EXPECT().GetTokenWrappedAddress(mock.Anything, originNetwork, tokenAddr).
		Return(wrappedAddr, nil)

	statePath := filepath.Join(t.TempDir(), "state.json")
	store, err := bridgelooptester.NewFileStateStore(statePath)
	require.NoError(t, err)
	seeded := bridgelooptester.NewState()
	seeded.SetToken(&bridgelooptester.TokenState{
		LoopName:      "erc20-ring",
		OriginNetwork: originNetwork,
		Address:       tokenAddr,
	})
	require.NoError(t, store.Save(context.Background(), seeded))

	tokenMocks := map[uint32]*mocks.Token{}
	deps := h.deps(&fakeHopRunner{}, store)
	deps.NewTokenFn = func(client bridgelooptester.NetworkClient, _ common.Address) (bridgelooptester.Token, error) {
		tok, ok := tokenMocks[client.NetworkID()]
		if !ok {
			tok = mocks.NewToken(t)
			tok.EXPECT().BalanceOf(mock.Anything, mock.Anything).Return(big.NewInt(1_000), nil)
			tokenMocks[client.NetworkID()] = tok
		}

		return tok, nil
	}

	orchestrator, err := bridgelooptester.NewOrchestrator(context.Background(), h.cfg, deps)
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.NoError(t, err)
	require.Len(t, report.Loops, 1)
	require.Len(t, report.Loops[0].Plan, 2)

	hop0 := report.Loops[0].Plan[0]
	require.Equal(t, tokenAddr, hop0.SourceTokenAddress, "network 0 is the token's origin")
	require.Equal(t, wrappedAddr, hop0.DestinationTokenAddress, "network 1 holds the wrapped token")
	require.True(t, hop0.Fundable)
	require.Empty(t, hop0.Note)

	hop1 := report.Loops[0].Plan[1]
	require.Equal(t, wrappedAddr, hop1.SourceTokenAddress, "hop 1 originates from the wrapped side")
	require.Equal(t, tokenAddr, hop1.DestinationTokenAddress, "network 0 is the token's origin")
	require.True(t, hop1.Fundable)
}
