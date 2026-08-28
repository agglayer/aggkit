package bridgetracker

import (
	"context"
	"errors"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// errSourceDown is the canned transient failure tests inject on a fact source
var errSourceDown = errors.New("source down")

// settlementTxHash is the canned certificate settlement tx hash tests use to drive
// StepWaitL1SettledGER
var settlementTxHash = common.HexToHash("0x09")

// fakeSources implements the five engine fact ports with mutable canned answers, so each
// test drives the bridge lifecycle by changing the facts between ticks
type fakeSources struct {
	bridge    *BridgeInfo
	bridgeErr error

	originGER             *types.GERData
	originErr             error
	originLER             *types.LERUpdateResult
	originLERErr          error
	injectedGER           *types.GERData
	injectedErr           error
	injectedGERAtIndex    *types.GERData
	injectedGERAtIndexErr error
	l1InfoTreeIndex       *uint32
	l1InfoTreeIndexErr    error

	cert    *types.CertificateInclusionData
	certErr error

	claimed    bool
	claimedErr error
	claim      *types.ClaimResult
	claimErr   error

	settlement    *types.L1SettledGERResult
	settlementErr error
}

func (f *fakeSources) FindBridge(_ context.Context, _ TrackingID) (*BridgeInfo, error) {
	return f.bridge, f.bridgeErr
}

func (f *fakeSources) CertificateFor(_ context.Context, _ *BridgeInfo) (*types.CertificateInclusionData, error) {
	return f.cert, f.certErr
}

func (f *fakeSources) OriginGER(_ context.Context, _ *BridgeInfo) (*types.GERData, error) {
	return f.originGER, f.originErr
}

// FindFirstL1InfoTreeAfterBlock implements domain.WaitingGERUpdateSource, translating the canned
// originGER/originErr fixtures (kept shaped like OriginGER, for minimal test churn) into the
// port's own result type
func (f *fakeSources) FindFirstL1InfoTreeAfterBlock(
	_ context.Context, _ uint64, _ uint32,
) (*domain.ResultFindFirstL1InfoTreeAfterBlock, error) {
	if f.originErr != nil || f.originGER == nil {
		return nil, f.originErr
	}
	return &domain.ResultFindFirstL1InfoTreeAfterBlock{
		// LeafCount is the contract's 1-based deposit count: 1 here means the update landed at
		// leaf index 0, matching the zero-value types.GERUpdateResult.L1InfoTreeIndex tests expect
		LeafCount: 1, GER: *f.originGER.GER, BlockNumber: *f.originGER.BlockNumber,
	}, nil
}

func (f *fakeSources) OriginLER(_ context.Context, _ *BridgeInfo) (*types.LERUpdateResult, error) {
	return f.originLER, f.originLERErr
}

func (f *fakeSources) InjectedGER(_ context.Context, _ *BridgeInfo) (*types.GERData, error) {
	return f.injectedGER, f.injectedErr
}

func (f *fakeSources) InjectedGERAtIndex(
	_ context.Context, _ *BridgeInfo, _ uint32,
) (*types.GERData, error) {
	return f.injectedGERAtIndex, f.injectedGERAtIndexErr
}

func (f *fakeSources) L1InfoTreeIndexForGER(
	_ context.Context, _ *BridgeInfo, _ common.Hash,
) (*uint32, error) {
	return f.l1InfoTreeIndex, f.l1InfoTreeIndexErr
}

func (f *fakeSources) IsClaimed(_ context.Context, _ *BridgeInfo) (bool, error) {
	return f.claimed, f.claimedErr
}

func (f *fakeSources) ClaimFor(_ context.Context, _ *BridgeInfo) (*types.ClaimResult, error) {
	return f.claim, f.claimErr
}

func (f *fakeSources) SettlementGERUpdate(
	_ context.Context, _ *BridgeInfo, _ common.Hash,
) (*types.L1SettledGERResult, error) {
	return f.settlement, f.settlementErr
}

func (f *fakeSources) engineSources() EngineSources {
	return EngineSources{
		Bridges: f, Certificates: f, GERs: f, LERs: f, ClaimChecker: f, Claims: f, Settlement: f,
		WaitingGERUpdateSource: f,
	}
}

// newTestEngine wires an engine over a fresh in-memory registry, a fake clock and the fakes
func newTestEngine(t *testing.T, sources *fakeSources) (*Engine, *memoryRegistry, *time.Time) {
	t.Helper()

	store := newMemoryRegistry(0)
	engine, err := NewEngine(EngineConfig{UnresolvedTimeout: 20 * time.Second},
		log.WithFields("module", "engine_test"), store, sources.engineSources())
	require.NoError(t, err)

	clock := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	engine.now = func() time.Time { return clock }
	store.now = engine.now
	return engine, store, &clock
}

// mustRegister adds id to the supervised list, failing the test on error
func mustRegister(t *testing.T, store *memoryRegistry, id TrackingID) {
	t.Helper()

	_, err := store.Get(id, true)
	require.NoError(t, err)
}

// mustGet reads the current snapshot of id, failing the test on error
func mustGet(t *testing.T, store *memoryRegistry, id TrackingID) *domain.TrackingData {
	t.Helper()

	tracking, err := store.Get(id, false)
	require.NoError(t, err)
	return tracking
}

// mustGetTrackerActives reads the current active list, failing the test on error
func mustGetTrackerActives(t *testing.T, store *memoryRegistry) []*domain.TrackingData {
	t.Helper()

	active, err := store.GetTrackerActives(nil)
	require.NoError(t, err)
	return active
}

func l2ToL2Bridge() *BridgeInfo {
	return &BridgeInfo{
		NetworkID:          1,
		LeafType:           types.BridgeLeafTypeAsset,
		DestinationNetwork: 2,
		DepositCount:       7,
		BlockNumber:        1000,
		LogIndex:           2,
	}
}

func currentStep(t *testing.T, store *memoryRegistry) types.BridgeStep {
	t.Helper()

	tracking := mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	require.False(t, tracking.Failed())
	require.NotNil(t, tracking.Info())
	return tracking.AllSteps()[*tracking.StepIndex()].Step
}

func TestEngineNewValidation(t *testing.T) {
	f := &fakeSources{}
	logger := log.WithFields("module", "engine_test")

	_, err := NewEngine(EngineConfig{}, logger, nil, f.engineSources())
	require.ErrorContains(t, err, "SupervisedStore")

	sources := f.engineSources()
	sources.LERs = nil
	_, err = NewEngine(EngineConfig{}, logger, newMemoryRegistry(0), sources)
	require.ErrorContains(t, err, "LERSource")

	sources = f.engineSources()
	sources.ClaimChecker = nil
	_, err = NewEngine(EngineConfig{}, logger, newMemoryRegistry(0), sources)
	require.ErrorContains(t, err, "ClaimChecker")

	sources = f.engineSources()
	sources.Claims = nil
	_, err = NewEngine(EngineConfig{}, logger, newMemoryRegistry(0), sources)
	require.ErrorContains(t, err, "ClaimSource")

	sources = f.engineSources()
	sources.Settlement = nil
	_, err = NewEngine(EngineConfig{}, logger, newMemoryRegistry(0), sources)
	require.ErrorContains(t, err, "SettlementSource")
}

// TestEngineResolveTriggeredResolvesImmediately pins that resolveTriggered (the handler for a
// signal off the store's trigger channel, see Engine.Start) resolves the given bridge right
// away, the same way one iteration of tick would, without needing a poll round over the whole
// active list
func TestEngineResolveTriggeredResolvesImmediately(t *testing.T) {
	f := &fakeSources{bridge: l2ToL2Bridge()}
	engine, store, _ := newTestEngine(t, f)
	id := TrackingID{NetworkID: 1, TxHash: testHash}
	mustRegister(t, store, id)

	engine.resolveTriggered(t.Context(), id)

	tracking := mustGet(t, store, id)
	require.NotNil(t, tracking.Info(), "resolveTriggered must resolve the bridge, not just be a no-op")
}

// TestEngineResolveTriggeredIgnoresUnknownID pins that a signal for an id no longer in the
// supervised list (e.g. pruned in the meantime) is silently ignored, never panics
func TestEngineResolveTriggeredIgnoresUnknownID(t *testing.T) {
	f := &fakeSources{bridge: l2ToL2Bridge()}
	engine, _, _ := newTestEngine(t, f)

	require.NotPanics(t, func() {
		engine.resolveTriggered(t.Context(), TrackingID{NetworkID: 1, TxHash: testHash})
	})
}

// TestEngineStartResolvesTriggeredBridgeBeforeNextPoll pins the end-to-end wiring Engine.Start
// sets up over a Triggerable store: PollInterval is set far in the future, so if the trigger
// channel were not being watched the bridge would still read as bare Registered by the time
// GetAndAwait's own timeout elapses
func TestEngineStartResolvesTriggeredBridgeBeforeNextPoll(t *testing.T) {
	f := &fakeSources{bridge: l2ToL2Bridge()}
	engine, store, _ := newTestEngine(t, f)
	engine.cfg.PollInterval = time.Hour

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	engine.Start(ctx)

	tracking, err := store.GetAndAwait(TrackingID{NetworkID: 1, TxHash: testHash}, time.Second)
	require.NoError(t, err)
	require.NotNil(t, tracking.Info(),
		"the engine must resolve a freshly registered bridge via the trigger channel, not wait out PollInterval")
}

// TestEngineNotFound pins the give-up policy: the tx gets UnresolvedTimeout to show up (it
// may not be mined yet) and is then marked as terminally failed to resolve (TrackingStatus:
// Error, an exhausted ErrorStep) and dropped from the active list
func TestEngineNotFound(t *testing.T) {
	f := &fakeSources{bridgeErr: ErrBridgeTxNotFound}
	engine, store, clock := newTestEngine(t, f)

	mustRegister(t, store, TrackingID{NetworkID: 1, TxHash: testHash})

	// first miss: still tracked, still no status for clients (bridge_status: null)
	engine.tick(t.Context())
	tracking := mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus())
	require.Nil(t, tracking.Info())
	require.Nil(t, tracking.StepIndex())
	require.Nil(t, tracking.AllSteps())
	require.False(t, tracking.Failed())

	// UnresolvedTimeout elapses since the first miss (StartDate) -> gives up resolving the bridge
	*clock = clock.Add(engine.cfg.UnresolvedTimeout)
	engine.tick(t.Context())
	tracking = mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	require.Equal(t, types.TrackingStatusError, tracking.TrackingStatus())
	require.Nil(t, tracking.Info())
	require.Nil(t, tracking.StepIndex())
	require.Nil(t, tracking.AllSteps())
	require.True(t, tracking.Failed())
	errStep := tracking.Error()
	require.NotNil(t, errStep)
	require.Equal(t, types.StepErrorExhausted, errStep.ErrorType)
	require.Equal(t, 2, errStep.RetryCount)
	require.Empty(t, mustGetTrackerActives(t, store), "failed bridges must leave the active list")
}

// TestEngineRetentionAndRetry pins the retry path for a bridge the tracker gave up on: the
// terminal Error stays queryable for RetentionPeriod (so pollers observe it), then the tick
// janitor forgets the entry, and a new request for the same tx re-registers it from scratch —
// this time resolving normally
func TestEngineRetentionAndRetry(t *testing.T) {
	f := &fakeSources{bridgeErr: ErrBridgeTxNotFound}
	engine, store, clock := newTestEngine(t, f)
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	mustRegister(t, store, id)
	engine.tick(t.Context())
	*clock = clock.Add(engine.cfg.UnresolvedTimeout)
	engine.tick(t.Context())
	require.True(t, mustGet(t, store, id).Failed())

	// within the retention window the terminal Error stays queryable
	*clock = clock.Add(engine.cfg.RetentionPeriod / 2)
	engine.tick(t.Context())
	require.Equal(t, types.TrackingStatusError, mustGet(t, store, id).TrackingStatus())

	// past the window the tick janitor forgets the entry: it no longer occupies memory
	*clock = clock.Add(engine.cfg.RetentionPeriod)
	engine.tick(t.Context())
	require.Zero(t, store.GetNumTracker())
	_, err := store.Get(id, false)
	require.ErrorIs(t, err, domain.ErrTrackingNotFound)

	// the retry: the tx is mined by now; asking again re-registers it and it resolves
	f.bridgeErr = nil
	f.bridge = l2ToL2Bridge()
	tracking, err := store.Get(id, true)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus())
	engine.tick(t.Context())
	require.Equal(t, types.StepWaitingLERUpdate, currentStep(t, store))
}

// TestEngineIdleTimeout pins that the tick janitor forgets a bridge nobody has accessed since
// registration, even if it never reaches a terminal state: RetentionPeriod alone would keep an
// active-but-abandoned bridge supervised forever, since it is never Failed nor Finished
func TestEngineIdleTimeout(t *testing.T) {
	f := &fakeSources{bridge: l2ToL2Bridge()}
	engine, store, clock := newTestEngine(t, f)
	engine.cfg.IdleTimeout = 5 * time.Minute
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	mustRegister(t, store, id)
	engine.tick(t.Context())
	require.Equal(t, 1, store.GetNumTracker())

	// within the idle window (lastAccess still the registration above), the bridge stays
	// supervised however many ticks run — it never resolves to a terminal state on its own
	*clock = clock.Add(engine.cfg.IdleTimeout / 2)
	engine.tick(t.Context())
	require.Equal(t, 1, store.GetNumTracker())

	// past the window, with nobody having read it since registration, the tick janitor forgets
	// it — even though it never reached a terminal state
	*clock = clock.Add(engine.cfg.IdleTimeout)
	engine.tick(t.Context())
	require.Zero(t, store.GetNumTracker())
}

// TestEngineIdleTimeoutExtendedByAccess pins that reading the bridge resets the idle window, so
// a client that keeps polling never sees its own bridge idle-evicted out from under it
func TestEngineIdleTimeoutExtendedByAccess(t *testing.T) {
	f := &fakeSources{bridge: l2ToL2Bridge()}
	engine, store, clock := newTestEngine(t, f)
	engine.cfg.IdleTimeout = 5 * time.Minute
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	mustRegister(t, store, id)
	engine.tick(t.Context())

	*clock = clock.Add(engine.cfg.IdleTimeout / 2)
	mustGet(t, store, id) // the client polls: this bumps lastAccess to the current clock
	engine.tick(t.Context())
	require.Equal(t, 1, store.GetNumTracker())

	// another half window elapses: still short of a full IdleTimeout since the poll above
	*clock = clock.Add(engine.cfg.IdleTimeout / 2)
	engine.tick(t.Context())
	require.Equal(t, 1, store.GetNumTracker(), "the poll above should have reset the idle window")
}

// TestEngineNotABridge pins that a tx which exists but is not a bridge transaction (reverted,
// or emitted no BridgeEvent) is marked as terminally failed immediately, with no retries: the
// receipt is already final, so waiting cannot change the outcome
func TestEngineNotABridge(t *testing.T) {
	f := &fakeSources{bridgeErr: ErrBridgeTxNotABridge}
	engine, store, _ := newTestEngine(t, f)

	mustRegister(t, store, TrackingID{NetworkID: 1, TxHash: testHash})

	engine.tick(t.Context())
	tracking := mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	require.Equal(t, types.TrackingStatusError, tracking.TrackingStatus())
	require.Nil(t, tracking.Info())
	require.True(t, tracking.Failed())
	errStep := tracking.Error()
	require.NotNil(t, errStep)
	require.Equal(t, types.StepErrorPermanent, errStep.ErrorType)
	require.Equal(t, 0, errStep.RetryCount, "no retries: an already-mined tx cannot change on retry")
	require.Empty(t, mustGetTrackerActives(t, store), "failed bridges must leave the active list")
}

// TestEngineSourceUnavailable pins that a network with no source configured to resolve it
// (e.g. no JSON-RPC client) is marked as terminally failed immediately, with no retries: the
// gap is a static configuration fact, so waiting cannot change the outcome
func TestEngineSourceUnavailable(t *testing.T) {
	f := &fakeSources{bridgeErr: ErrSourceUnavailable}
	engine, store, _ := newTestEngine(t, f)

	mustRegister(t, store, TrackingID{NetworkID: 1, TxHash: testHash})

	engine.tick(t.Context())
	tracking := mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	require.Equal(t, types.TrackingStatusError, tracking.TrackingStatus())
	require.Nil(t, tracking.Info())
	require.True(t, tracking.Failed())
	errStep := tracking.Error()
	require.NotNil(t, errStep)
	require.Equal(t, types.StepErrorPermanent, errStep.ErrorType)
	require.Equal(t, 0, errStep.RetryCount, "no retries: a missing source cannot change on retry")
	require.Empty(t, mustGetTrackerActives(t, store), "failed bridges must leave the active list")
}

// TestEngineResolveErrorAccumulatesAndClears pins that a FindBridge failure other than
// ErrBridgeTxNotFound / ErrBridgeTxNotABridge is recorded on the bridge (retry count
// accumulating across ticks) without marking it as failed, and clears once FindBridge
// succeeds
func TestEngineResolveErrorAccumulatesAndClears(t *testing.T) {
	f := &fakeSources{bridgeErr: context.DeadlineExceeded}
	engine, store, _ := newTestEngine(t, f)
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	mustRegister(t, store, id)

	engine.tick(t.Context())
	tracking := mustGet(t, store, id)
	require.False(t, tracking.Failed())
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus())
	errStep := tracking.Error()
	require.NotNil(t, errStep)
	require.Equal(t, types.StepErrorTransient, errStep.ErrorType)
	require.Equal(t, 1, errStep.RetryCount)
	require.Contains(t, errStep.Description[0], "context deadline exceeded")

	engine.tick(t.Context())
	tracking = mustGet(t, store, id)
	errStep = tracking.Error()
	require.Equal(t, 2, errStep.RetryCount)
	require.Len(t, errStep.Description, 2)
	require.NotEmpty(t, mustGetTrackerActives(t, store), "a transient error must not drop the bridge from the active list")

	f.bridgeErr = nil
	f.bridge = l2ToL2Bridge()
	engine.tick(t.Context())
	tracking = mustGet(t, store, id)
	require.False(t, tracking.Failed())
	require.Nil(t, tracking.BridgeTx().Error, "a successful resolution clears the accumulated error")
}

// TestEngineTransientErrorEventuallyGivesUp pins that a persistent transient FindBridge
// failure (anything other than ErrBridgeTxNotFound) is also given up on once UnresolvedTimeout
// elapses since it was first seen unresolved — unlike a bare retry counter, the timeout
// applies uniformly regardless of why the bridge never resolved
func TestEngineTransientErrorEventuallyGivesUp(t *testing.T) {
	f := &fakeSources{bridgeErr: context.DeadlineExceeded}
	engine, store, clock := newTestEngine(t, f)
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	mustRegister(t, store, id)

	engine.tick(t.Context())
	require.False(t, mustGet(t, store, id).Failed())

	*clock = clock.Add(engine.cfg.UnresolvedTimeout)
	engine.tick(t.Context())

	tracking := mustGet(t, store, id)
	require.True(t, tracking.Failed())
	require.Equal(t, types.TrackingStatusError, tracking.TrackingStatus())
	errStep := tracking.Error()
	require.NotNil(t, errStep)
	require.Equal(t, types.StepErrorExhausted, errStep.ErrorType)
	require.Equal(t, 2, errStep.RetryCount)
	require.Empty(t, mustGetTrackerActives(t, store), "failed bridges must leave the active list")
}

// TestEngineNotFoundCounterResets pins that a bridge which resolves before UnresolvedTimeout
// elapses is tracked normally, regardless of how many misses it took to get there
func TestEngineNotFoundCounterResets(t *testing.T) {
	f := &fakeSources{bridgeErr: ErrBridgeTxNotFound}
	engine, store, _ := newTestEngine(t, f)

	mustRegister(t, store, TrackingID{NetworkID: 1, TxHash: testHash})

	engine.tick(t.Context())

	// the bridge appears before the second miss: tracked normally from here on
	f.bridgeErr = nil
	f.bridge = l2ToL2Bridge()
	engine.tick(t.Context())

	tracking := mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	require.False(t, tracking.Failed())
	require.NotNil(t, tracking.Info())
	require.Equal(t, types.StepWaitingLERUpdate, tracking.AllSteps()[*tracking.StepIndex()].Step)
}

// TestEngineLifecycleL2ToL2 walks a bridge through the full L2->L2' path, checking the step
// the store serves after each fact change
func TestEngineLifecycleL2ToL2(t *testing.T) {
	f := &fakeSources{bridge: l2ToL2Bridge()}
	engine, store, _ := newTestEngine(t, f)

	mustRegister(t, store, TrackingID{NetworkID: 1, TxHash: testHash})

	engine.tick(t.Context())
	require.Equal(t, types.StepWaitingLERUpdate, currentStep(t, store))

	f.originLER = &types.LERUpdateResult{NetworkID: 1, LER: common.HexToHash("0x0a"), BlockNumber: 10}
	engine.tick(t.Context())
	require.Equal(t, types.StepPendingInclusion, currentStep(t, store))

	f.cert = &types.CertificateInclusionData{
		CertificateData: types.CertificateData{CertificateID: common.HexToHash("0x01"), Status: agglayertypes.Pending},
	}
	engine.tick(t.Context())
	require.Equal(t, types.StepCertificatePending, currentStep(t, store))

	f.cert = &types.CertificateInclusionData{
		CertificateData: types.CertificateData{CertificateID: common.HexToHash("0x01"), Status: agglayertypes.InError},
	}
	engine.tick(t.Context())
	require.Equal(t, types.StepCertificatePending, currentStep(t, store),
		"an unsettled certificate status change stays at CertificatePending, it does not move the step")
	inError := mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	require.Equal(t, &f.cert.CertificateData, inError.AllSteps()[*inError.StepIndex()].Result(),
		"the certificate's current, not yet settled, status is visible while waiting")

	f.cert = &types.CertificateInclusionData{
		CertificateData: types.CertificateData{
			CertificateID: common.HexToHash("0x02"), Status: agglayertypes.Settled,
			SettlementTxHash: &settlementTxHash,
		},
	}
	engine.tick(t.Context())
	tracking := mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	require.False(t, tracking.Failed())
	info, allSteps := tracking.Info(), tracking.AllSteps()
	require.Equal(t, types.StepWaitL1SettledGER, allSteps[*tracking.StepIndex()].Step,
		"settled but the settlement tx is not confirmed on L1 yet")
	require.Equal(t, uint64(1000), info.BlockNumber, "the creating BridgeEvent's block/log position is carried through")
	require.Equal(t, uint32(2), info.LogIndex)
	// the certificate step reports the settled certificate as its result once it completes
	for _, sp := range allSteps {
		if sp.Step == types.StepCertificatePending {
			require.Equal(t, &f.cert.CertificateData, sp.Result())
		}
	}

	settlementLeafIndex := uint32(7)
	f.settlement = &types.L1SettledGERResult{
		TxHash: settlementTxHash, SettlementBlockNumber: 2000, GER: common.HexToHash("0x0b"),
		L1InfoTreeIndex:                   &settlementLeafIndex,
		HasVerifyBatchesTrustedAggregator: true, HasUpdateL1InfoTree: true,
	}
	engine.tick(t.Context())
	tracking = mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	allSteps = tracking.AllSteps()
	require.Equal(t, types.StepWaitingGERInjection, allSteps[*tracking.StepIndex()].Step)
	for _, sp := range allSteps {
		if sp.Step == types.StepWaitL1SettledGER {
			require.Equal(t, f.settlement, sp.Result())
		}
	}

	injectedGER := common.HexToHash("0x04")
	injectedGERBlockNumber := uint64(200)
	injectedGERTimestamp := uint64(1700000000)
	f.injectedGERAtIndex = &types.GERData{
		NetworkID: 2, GER: &injectedGER, LERType: types.LERTypeLocal,
		BlockNumber: &injectedGERBlockNumber, BlockTimestamp: &injectedGERTimestamp,
	}
	engine.tick(t.Context())
	tracking = mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	allSteps = tracking.AllSteps()
	require.Equal(t, types.StepWaitingClaim, currentStep(t, store))
	for _, sp := range allSteps {
		if sp.Step == types.StepWaitingGERInjection {
			require.Equal(t, &types.InjectedGERResult{
				GER: injectedGER, BlockNumber: injectedGERBlockNumber, BlockTimestamp: injectedGERTimestamp,
			}, sp.Result())
		}
	}

	f.claimed = true
	f.claim = &types.ClaimResult{ClaimTx: common.HexToHash("0x03"), BlockNumber: 30}
	engine.tick(t.Context())
	tracking = mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	require.False(t, tracking.Failed())
	allSteps = tracking.AllSteps()
	require.Equal(t, types.StepClaimed, allSteps[*tracking.StepIndex()].Step)
	final := allSteps[len(allSteps)-1]
	require.Equal(t, types.StepClaimed, final.Step)
	require.Equal(t, types.StepStatusDone, final.Status, "Claimed is done once the bridge service confirms the claim")

	for _, sp := range allSteps {
		if sp.Step == types.StepClaimed {
			require.Equal(t, f.claim, sp.Result())
		}
	}

	// claimed bridges leave the active list on the next round
	engine.tick(t.Context())
	require.Empty(t, mustGetTrackerActives(t, store))
}

// TestEngineIncrementalResolution pins that resolution is incremental: once the bridge tx and
// a milestone step are resolved and persisted, later ticks skip their sources entirely — the
// engine only queries the facts the bridge is still waiting on
func TestEngineIncrementalResolution(t *testing.T) {
	f := &fakeSources{bridge: l2ToL2Bridge()}
	engine, store, _ := newTestEngine(t, f)

	mustRegister(t, store, TrackingID{NetworkID: 1, TxHash: testHash})

	// walk the bridge up to WaitingClaim: every milestone but the claim is done
	f.originLER = &types.LERUpdateResult{NetworkID: 1, LER: common.HexToHash("0x0a"), BlockNumber: 10}
	f.cert = &types.CertificateInclusionData{
		CertificateData: types.CertificateData{
			CertificateID: common.HexToHash("0x01"), Status: agglayertypes.Settled,
			SettlementTxHash: &settlementTxHash,
		},
	}
	settlementLeafIndex := uint32(7)
	f.settlement = &types.L1SettledGERResult{
		TxHash: settlementTxHash, GER: common.HexToHash("0x0b"), L1InfoTreeIndex: &settlementLeafIndex,
		HasVerifyBatchesTrustedAggregator: true, HasUpdateL1InfoTree: true,
	}
	injectedGER := common.HexToHash("0x04")
	f.injectedGERAtIndex = &types.GERData{NetworkID: 2, GER: &injectedGER, LERType: types.LERTypeLocal}
	engine.tick(t.Context())
	require.Equal(t, types.StepWaitingClaim, currentStep(t, store))

	// break every already-resolved source: if any of them were queried again, FindBridge would
	// fail the tx resolution and DeriveStep would persist a step-level error
	f.bridgeErr = ErrBridgeTxNotFound
	f.bridge = nil
	f.originLERErr = errSourceDown
	f.originLER = nil
	f.certErr = errSourceDown
	f.cert = nil
	f.settlementErr = errSourceDown
	f.settlement = nil
	f.injectedGERAtIndexErr = errSourceDown
	f.injectedGERAtIndex = nil

	engine.tick(t.Context())
	tracking := mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	require.Nil(t, tracking.Error(), "done milestones must not be re-queried")
	require.Equal(t, types.StepWaitingClaim, currentStep(t, store))

	// the bridge still finishes through the only remaining facts, the claim status and its record
	f.claimed = true
	f.claim = &types.ClaimResult{ClaimTx: common.HexToHash("0x03"), BlockNumber: 30}
	engine.tick(t.Context())
	tracking = mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	require.Equal(t, types.TrackingStatusFinished, tracking.TrackingStatus())
}

// TestEngineL1ToL2Path pins that L1-originated bridges skip the certificate and LER steps
func TestEngineL1ToL2Path(t *testing.T) {
	f := &fakeSources{bridge: &BridgeInfo{
		NetworkID:          0,
		LeafType:           types.BridgeLeafTypeMessage,
		DestinationNetwork: 1,
		BlockNumber:        500,
		LogIndex:           1,
	}}
	engine, store, _ := newTestEngine(t, f)

	mustRegister(t, store, TrackingID{NetworkID: 0, TxHash: testHash})
	ger := common.HexToHash("0x0a")
	blockNumber := uint64(10)
	f.originGER = &types.GERData{NetworkID: 0, GER: &ger, BlockNumber: &blockNumber, LERType: types.LERTypeMainnet}
	engine.tick(t.Context())

	tracking := mustGet(t, store, TrackingID{NetworkID: 0, TxHash: testHash})
	require.False(t, tracking.Failed())
	info, allSteps := tracking.Info(), tracking.AllSteps()
	require.Equal(t, types.BridgeTypeL1ToL2, info.BridgeType())
	require.Equal(t, types.BridgeLeafTypeMessage, info.LeafType)
	require.Equal(t, uint64(500), info.BlockNumber)
	require.Equal(t, uint32(1), info.LogIndex)
	require.Equal(t, types.StepWaitingGERInjection, allSteps[*tracking.StepIndex()].Step)
	for _, sp := range allSteps {
		require.NotEqual(t, types.StepWaitingLERUpdate, sp.Step)
		require.NotEqual(t, types.StepPendingInclusion, sp.Step)
		require.NotEqual(t, types.StepCertificatePending, sp.Step)
		require.NotEqual(t, types.StepWaitL1SettledGER, sp.Step)
		if sp.Step == types.StepWaitingGERUpdate {
			require.Equal(t, &types.GERUpdateResult{GER: ger, BlockNumber: blockNumber}, sp.Result())
		}
	}
}

// TestEngineNoChangeNoRepublish pins the change detection: identical facts across rounds
// must not re-publish (WebSocket clients would receive duplicate status messages)
func TestEngineNoChangeNoRepublish(t *testing.T) {
	f := &fakeSources{bridge: l2ToL2Bridge()}
	engine, store, _ := newTestEngine(t, f)

	mustRegister(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	updates, unsubscribe, err := store.Subscribe(TrackingID{NetworkID: 1, TxHash: testHash})
	require.NoError(t, err)
	defer unsubscribe()

	engine.tick(t.Context())
	<-updates // first status

	engine.tick(t.Context())
	select {
	case update := <-updates:
		t.Fatalf("unexpected re-publish without changes: %+v", update)
	default:
	}
}

// TestEngineStepDates pins the date carrying: a step keeps its StartDate while in progress
// and gets its EndDate stamped on transition
func TestEngineStepDates(t *testing.T) {
	f := &fakeSources{bridge: l2ToL2Bridge()}
	engine, store, clock := newTestEngine(t, f)

	mustRegister(t, store, TrackingID{NetworkID: 1, TxHash: testHash})

	start := *clock
	engine.tick(t.Context())

	// time passes with no fact change: StartDate must not move
	*clock = clock.Add(time.Minute)
	engine.tick(t.Context())
	tracking := mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	require.False(t, tracking.Failed())
	allSteps := tracking.AllSteps()
	require.Equal(t, 0, *tracking.StepIndex())
	require.Equal(t, types.StepWaitingLERUpdate, allSteps[0].Step)
	require.Equal(t, start, *allSteps[0].StartDate)
	require.Nil(t, allSteps[0].EndDate)

	// transition: previous step closes at the new observation time, next one opens
	transition := clock.Add(time.Minute)
	*clock = transition
	f.originLER = &types.LERUpdateResult{NetworkID: 1, LER: common.HexToHash("0x0a"), BlockNumber: 10}
	engine.tick(t.Context())

	tracking = mustGet(t, store, TrackingID{NetworkID: 1, TxHash: testHash})
	require.False(t, tracking.Failed())
	allSteps = tracking.AllSteps()
	require.Equal(t, 1, *tracking.StepIndex(), "current step moved on to WaitingLERUpdate's successor")
	require.Equal(t, types.StepStatusDone, allSteps[0].Status)
	require.Equal(t, start, *allSteps[0].StartDate)
	require.Equal(t, transition, *allSteps[0].EndDate)
	require.Equal(t, types.StepStatusInProgress, allSteps[1].Status)
	require.Equal(t, transition, *allSteps[1].StartDate)
}

// TestEngineResolutionPublishesFullRoute pins that resolving the bridge tx publishes its
// whole expected path at once (all steps pending, the first in progress) — the resolved type
// reveals the route, so clients see the full way to walk even if every fact source is down
// and no milestone can be derived yet
func TestEngineResolutionPublishesFullRoute(t *testing.T) {
	f := &fakeSources{bridge: l2ToL2Bridge(), originLERErr: errSourceDown}
	engine, store, _ := newTestEngine(t, f)
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	mustRegister(t, store, id)
	engine.tick(t.Context())

	tracking := mustGet(t, store, id)
	require.NotNil(t, tracking.Info(), "the tx resolved even though the step derivation failed")
	allSteps := tracking.AllSteps()
	expectedPath := domain.ExpectedPath(types.BridgeTypeL2ToL2)
	require.Len(t, allSteps, len(expectedPath), "the whole route is visible from resolution")
	for i, sp := range allSteps {
		require.Equal(t, expectedPath[i], sp.Step)
	}
	// the first step carries the derivation failure; the rest of the route is pending
	require.Equal(t, types.StepStatusError, allSteps[0].Status)
	require.NotNil(t, allSteps[0].Error)
	for _, sp := range allSteps[1:] {
		require.Equal(t, types.StepStatusPending, sp.Status)
	}

	// the source recovers: the route is walked normally from where it stood
	f.originLERErr = nil
	f.originLER = &types.LERUpdateResult{NetworkID: 1, LER: common.HexToHash("0x0a"), BlockNumber: 10}
	engine.tick(t.Context())
	require.Equal(t, types.StepPendingInclusion, currentStep(t, store))
}

// TestEngineTransientErrorKeepsState pins that a transient source failure neither publishes
// nor loses the resolved bridge facts, and that it is persisted as a step-level error whose
// retry count accumulates across ticks and clears once the source recovers
func TestEngineTransientErrorKeepsState(t *testing.T) {
	f := &fakeSources{bridge: l2ToL2Bridge()}
	engine, store, _ := newTestEngine(t, f)
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	mustRegister(t, store, id)
	engine.tick(t.Context())
	require.Equal(t, types.StepWaitingLERUpdate, currentStep(t, store))

	f.originLERErr = context.DeadlineExceeded
	engine.tick(t.Context())
	require.Equal(t, types.StepWaitingLERUpdate, currentStep(t, store))
	tracking := mustGet(t, store, id)
	require.NotNil(t, tracking.Info(), "transient errors must not drop the resolved bridge info")

	errStep := tracking.AllSteps()[*tracking.StepIndex()]
	require.Equal(t, types.StepStatusError, errStep.Status)
	require.Equal(t, types.StepErrorTransient, errStep.Error.ErrorType)
	require.Equal(t, 1, errStep.Error.RetryCount)
	require.Contains(t, errStep.Error.Description[0], "context deadline exceeded")

	// a second consecutive failure accumulates onto the same step's retry count
	engine.tick(t.Context())
	tracking = mustGet(t, store, id)
	errStep = tracking.AllSteps()[*tracking.StepIndex()]
	require.Equal(t, 2, errStep.Error.RetryCount)
	require.Len(t, errStep.Error.Description, 2)

	// the source recovers: the step-level error clears on its own (BuildSteps never carries
	// an old step's Error forward)
	f.originLERErr = nil
	f.originLER = &types.LERUpdateResult{NetworkID: 1, LER: common.HexToHash("0x0a"), BlockNumber: 10}
	engine.tick(t.Context())
	tracking = mustGet(t, store, id)
	require.Equal(t, types.StepPendingInclusion, currentStep(t, store))
	for _, sp := range tracking.AllSteps() {
		require.Nil(t, sp.Error)
	}
}

// countingStore wraps a real memoryRegistry, counting UpdateTrackingBridgeTx calls — the only
// SupervisedStore method that notifies subscribers (see registry.go) — so a test can assert the
// engine commits a bridge's resolved facts and its computed steps together, in a single call,
// rather than a premature notification (Info populated, steps still the bare just-seeded path)
// followed by a second one once the steps are actually computed.
type countingStore struct {
	*memoryRegistry
	txCommits int
}

func (s *countingStore) UpdateTrackingBridgeTx(id TrackingID, tx domain.TrackingBridgeTx) error {
	s.txCommits++
	return s.memoryRegistry.UpdateTrackingBridgeTx(id, tx)
}

// TestEngineFirstResolutionCommitsInfoAndStepsTogether guards against the bug where a bridge's
// first successful FindBridge was persisted (and subscribers notified) immediately, ahead of
// computeAllSteps actually deriving its steps — a fast subscriber could observe BridgeStatus
// populated while AllSteps still held the bare, uncomputed seed. There must be exactly one
// UpdateTrackingBridgeTx call for the tick that resolves the bridge, carrying both Info and the
// already-computed steps.
func TestEngineFirstResolutionCommitsInfoAndStepsTogether(t *testing.T) {
	// No originGER configured: the first step (StepWaitingGERUpdate) resolves to ErrStepPending,
	// so AllSteps stays byte-for-byte identical to the freshly-seeded pending path — the case that
	// would skip persisting altogether without the tx-level change check (see resolveBridgeStep).
	f := &fakeSources{bridge: &BridgeInfo{
		NetworkID: 0, LeafType: types.BridgeLeafTypeMessage, DestinationNetwork: 1,
		BlockNumber: 500, LogIndex: 1,
	}}
	store := &countingStore{memoryRegistry: newMemoryRegistry(0)}
	engine, err := NewEngine(EngineConfig{UnresolvedTimeout: 20 * time.Second},
		log.WithFields("module", "engine_test"), store, f.engineSources())
	require.NoError(t, err)

	id := TrackingID{NetworkID: 0, TxHash: testHash}
	mustRegister(t, store.memoryRegistry, id)
	engine.tick(t.Context())

	require.Equal(t, 1, store.txCommits,
		"info and computed steps must land in exactly one commit, not a premature one followed by another")

	tracking := mustGet(t, store.memoryRegistry, id)
	require.NotNil(t, tracking.Info(), "the single commit must already carry the resolved bridge info")
	require.NotNil(t, tracking.AllSteps(), "the single commit must already carry the seeded/computed steps")
}
