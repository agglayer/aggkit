package bridgetracker

import (
	"context"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// fakeSources implements the five engine fact ports with mutable canned answers, so each
// test drives the bridge lifecycle by changing the facts between ticks
type fakeSources struct {
	bridge    *BridgeInfo
	bridgeErr error

	originGER    *types.GERData
	originErr    error
	originLER    *types.LERUpdateResult
	originLERErr error
	injectedGER  *types.GERData
	injectedErr  error

	cert    *types.CertificateData
	certErr error

	claim    *types.ClaimResult
	claimErr error
}

func (f *fakeSources) FindBridge(_ context.Context, _ uint32, _ common.Hash) (*BridgeInfo, error) {
	return f.bridge, f.bridgeErr
}

func (f *fakeSources) CertificateFor(_ context.Context, _ *BridgeInfo) (*types.CertificateData, error) {
	return f.cert, f.certErr
}

func (f *fakeSources) OriginGER(_ context.Context, _ *BridgeInfo) (*types.GERData, error) {
	return f.originGER, f.originErr
}

func (f *fakeSources) OriginLER(_ context.Context, _ *BridgeInfo) (*types.LERUpdateResult, error) {
	return f.originLER, f.originLERErr
}

func (f *fakeSources) InjectedGER(_ context.Context, _ *BridgeInfo) (*types.GERData, error) {
	return f.injectedGER, f.injectedErr
}

func (f *fakeSources) ClaimFor(_ context.Context, _ *BridgeInfo) (*types.ClaimResult, error) {
	return f.claim, f.claimErr
}

func (f *fakeSources) engineSources() EngineSources {
	return EngineSources{Bridges: f, Certificates: f, GERs: f, LERs: f, Claims: f}
}

// newTestEngine wires an engine over a fresh in-memory registry, a fake clock and the fakes
func newTestEngine(t *testing.T, sources *fakeSources) (*Engine, *memoryRegistry, *time.Time) {
	t.Helper()

	store := newMemoryRegistry()
	engine, err := NewEngine(EngineConfig{NotFoundAfter: 2},
		log.WithFields("module", "engine_test"), store, sources.engineSources())
	require.NoError(t, err)

	clock := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	engine.now = func() time.Time { return clock }
	return engine, store, &clock
}

func l2ToL2Bridge() *BridgeInfo {
	return &BridgeInfo{
		Key:                BridgeKey{NetworkID: 1, TxHash: testHash},
		LeafType:           types.BridgeLeafTypeAsset,
		DestinationNetwork: 2,
		DepositCount:       7,
		BlockNumber:        1000,
		LogIndex:           2,
	}
}

func currentStep(t *testing.T, store *memoryRegistry) types.BridgeStep {
	t.Helper()

	_, status, stepIndex, allSteps, errData := store.Register(1, testHash)
	require.Nil(t, errData)
	require.NotNil(t, status)
	return allSteps[*stepIndex].Step
}

func TestEngineNewValidation(t *testing.T) {
	f := &fakeSources{}
	logger := log.WithFields("module", "engine_test")

	_, err := NewEngine(EngineConfig{}, logger, nil, f.engineSources())
	require.ErrorContains(t, err, "SupervisedStore")

	sources := f.engineSources()
	sources.LERs = nil
	_, err = NewEngine(EngineConfig{}, logger, newMemoryRegistry(), sources)
	require.ErrorContains(t, err, "LERSource")

	sources = f.engineSources()
	sources.Claims = nil
	_, err = NewEngine(EngineConfig{}, logger, newMemoryRegistry(), sources)
	require.ErrorContains(t, err, "ClaimSource")
}

// TestEngineNotFound pins the give-up policy: the tx gets NotFoundAfter chances (it may not
// be mined yet) and is then marked as terminally failed to resolve (TrackingStatus: Error,
// an exhausted ErrorStep) and dropped from the engine state
func TestEngineNotFound(t *testing.T) {
	f := &fakeSources{bridgeErr: ErrBridgeTxNotFound}
	engine, store, _ := newTestEngine(t, f)

	store.Register(1, testHash)

	// first miss: still tracked, still no status for clients (bridge_status: null)
	engine.tick(t.Context())
	trackingStatus, status, stepIndex, allSteps, errStep := store.Register(1, testHash)
	require.Equal(t, types.TrackingStatusRegistered, trackingStatus)
	require.Nil(t, status)
	require.Nil(t, stepIndex)
	require.Nil(t, allSteps)
	require.Nil(t, errStep)

	// second consecutive miss reaches NotFoundAfter=2 -> gives up resolving the bridge
	engine.tick(t.Context())
	trackingStatus, status, stepIndex, allSteps, errStep = store.Register(1, testHash)
	require.Equal(t, types.TrackingStatusError, trackingStatus)
	require.Nil(t, status)
	require.Nil(t, stepIndex)
	require.Nil(t, allSteps)
	require.NotNil(t, errStep)
	require.Equal(t, types.StepErrorExhausted, errStep.ErrorType)
	require.Equal(t, 2, errStep.RetryCount)
	require.Empty(t, engine.tracked, "failed bridges must leave the engine state")
}

// TestEngineNotFoundCounterResets pins that a transient error between misses does not
// accumulate towards the 404
func TestEngineNotFoundCounterResets(t *testing.T) {
	f := &fakeSources{bridgeErr: ErrBridgeTxNotFound}
	engine, store, _ := newTestEngine(t, f)

	store.Register(1, testHash)

	engine.tick(t.Context())

	// the bridge appears before the second miss: tracked normally from here on
	f.bridgeErr = nil
	f.bridge = l2ToL2Bridge()
	engine.tick(t.Context())

	_, status, stepIndex, allSteps, errData := store.Register(1, testHash)
	require.Nil(t, errData)
	require.NotNil(t, status)
	require.Equal(t, types.StepWaitingLERUpdate, allSteps[*stepIndex].Step)
}

// TestEngineLifecycleL2ToL2 walks a bridge through the full L2->L2' path, checking the step
// the store serves after each fact change
func TestEngineLifecycleL2ToL2(t *testing.T) {
	f := &fakeSources{bridge: l2ToL2Bridge()}
	engine, store, _ := newTestEngine(t, f)

	store.Register(1, testHash)

	engine.tick(t.Context())
	require.Equal(t, types.StepWaitingLERUpdate, currentStep(t, store))

	f.originLER = &types.LERUpdateResult{NetworkID: 1, LER: common.HexToHash("0x0a"), BlockNumber: 10}
	engine.tick(t.Context())
	require.Equal(t, types.StepPendingInclusion, currentStep(t, store))

	f.cert = &types.CertificateData{CertificateID: common.HexToHash("0x01"), Status: agglayertypes.Pending}
	engine.tick(t.Context())
	require.Equal(t, types.StepCertificatePending, currentStep(t, store))

	f.cert = &types.CertificateData{CertificateID: common.HexToHash("0x01"), Status: agglayertypes.InError}
	engine.tick(t.Context())
	require.Equal(t, types.StepCertificateProcessing, currentStep(t, store))

	f.cert = &types.CertificateData{CertificateID: common.HexToHash("0x02"), Status: agglayertypes.Settled}
	engine.tick(t.Context())
	_, status, stepIndex, allSteps, errData := store.Register(1, testHash)
	require.Nil(t, errData)
	require.Equal(t, types.StepWaitingGERInjection, allSteps[*stepIndex].Step)
	require.Equal(t, uint64(1000), status.BlockNumber, "the creating BridgeEvent's block/log position is carried through")
	require.Equal(t, uint32(2), status.LogIndex)
	// the certificate step reports the settled certificate as its result once it completes
	for _, sp := range allSteps {
		if sp.Step == types.StepCertificateProcessing {
			require.Equal(t, f.cert, sp.Result)
		}
	}

	f.injectedGER = &types.GERData{NetworkID: 2, LERType: types.LERTypeLocal}
	engine.tick(t.Context())
	require.Equal(t, types.StepWaitingClaim, currentStep(t, store))

	f.claim = &types.ClaimResult{ClaimTx: common.HexToHash("0x03"), BlockNumber: 30}
	engine.tick(t.Context())
	_, _, stepIndex, allSteps, errData = store.Register(1, testHash)
	require.Nil(t, errData)
	require.Equal(t, types.StepClaimed, allSteps[*stepIndex].Step)
	final := allSteps[len(allSteps)-1]
	require.Equal(t, types.StepClaimed, final.Step)
	require.Equal(t, types.StepStatusDone, final.Status, "Claimed is terminal: done, not inProgress")

	for _, sp := range allSteps {
		if sp.Step == types.StepWaitingClaim {
			require.Equal(t, f.claim, sp.Result)
		}
	}

	// claimed bridges leave the active set and the engine state on the next round
	engine.tick(t.Context())
	require.Empty(t, engine.tracked)
	require.Empty(t, store.ActiveBridges())
}

// TestEngineL1ToL2Path pins that L1-originated bridges skip the certificate and LER steps
func TestEngineL1ToL2Path(t *testing.T) {
	f := &fakeSources{bridge: &BridgeInfo{
		Key:                BridgeKey{NetworkID: 0, TxHash: testHash},
		LeafType:           types.BridgeLeafTypeMessage,
		DestinationNetwork: 1,
		BlockNumber:        500,
		LogIndex:           1,
	}}
	engine, store, _ := newTestEngine(t, f)

	store.Register(0, testHash)
	ger := common.HexToHash("0x0a")
	blockNumber := uint64(10)
	f.originGER = &types.GERData{NetworkID: 0, GER: &ger, BlockNumber: &blockNumber, LERType: types.LERTypeMainnet}
	engine.tick(t.Context())

	_, status, stepIndex, allSteps, errData := store.Register(0, testHash)
	require.Nil(t, errData)
	require.Equal(t, types.BridgeTypeL1ToL2, status.BridgeType)
	require.Equal(t, types.BridgeLeafTypeMessage, status.BridgeLeafType)
	require.Equal(t, uint64(500), status.BlockNumber)
	require.Equal(t, uint32(1), status.LogIndex)
	require.Equal(t, types.StepWaitingGERInjection, allSteps[*stepIndex].Step)
	for _, sp := range allSteps {
		require.NotEqual(t, types.StepWaitingLERUpdate, sp.Step)
		require.NotEqual(t, types.StepPendingInclusion, sp.Step)
		require.NotEqual(t, types.StepCertificatePending, sp.Step)
		require.NotEqual(t, types.StepCertificateProcessing, sp.Step)
		if sp.Step == types.StepWaitingGERUpdate {
			require.Equal(t, &types.GERUpdateResult{GER: ger, BlockNumber: blockNumber}, sp.Result)
		}
	}
}

// TestEngineNoChangeNoRepublish pins the change detection: identical facts across rounds
// must not re-publish (WebSocket clients would receive duplicate status messages)
func TestEngineNoChangeNoRepublish(t *testing.T) {
	f := &fakeSources{bridge: l2ToL2Bridge()}
	engine, store, _ := newTestEngine(t, f)

	store.Register(1, testHash)
	updates, unsubscribe := store.Subscribe(1, testHash)
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

	store.Register(1, testHash)

	start := *clock
	engine.tick(t.Context())

	// time passes with no fact change: StartDate must not move
	*clock = clock.Add(time.Minute)
	engine.tick(t.Context())
	_, _, stepIndex, allSteps, errData := store.Register(1, testHash)
	require.Nil(t, errData)
	require.Equal(t, 0, *stepIndex)
	require.Equal(t, types.StepWaitingLERUpdate, allSteps[0].Step)
	require.Equal(t, start, *allSteps[0].StartDate)
	require.Nil(t, allSteps[0].EndDate)

	// transition: previous step closes at the new observation time, next one opens
	transition := clock.Add(time.Minute)
	*clock = transition
	f.originLER = &types.LERUpdateResult{NetworkID: 1, LER: common.HexToHash("0x0a"), BlockNumber: 10}
	engine.tick(t.Context())

	_, _, stepIndex, allSteps, errData = store.Register(1, testHash)
	require.Nil(t, errData)
	require.Equal(t, 1, *stepIndex, "current step moved on to WaitingLERUpdate's successor")
	require.Equal(t, types.StepStatusDone, allSteps[0].Status)
	require.Equal(t, start, *allSteps[0].StartDate)
	require.Equal(t, transition, *allSteps[0].EndDate)
	require.Equal(t, types.StepStatusInProgress, allSteps[1].Status)
	require.Equal(t, transition, *allSteps[1].StartDate)
}

// TestEngineTransientErrorKeepsState pins that a transient source failure neither publishes
// nor loses the resolved bridge facts
func TestEngineTransientErrorKeepsState(t *testing.T) {
	f := &fakeSources{bridge: l2ToL2Bridge()}
	engine, store, _ := newTestEngine(t, f)

	store.Register(1, testHash)
	engine.tick(t.Context())
	require.Equal(t, types.StepWaitingLERUpdate, currentStep(t, store))

	f.originLERErr = context.DeadlineExceeded
	engine.tick(t.Context())
	require.Equal(t, types.StepWaitingLERUpdate, currentStep(t, store))
	require.NotNil(t, engine.tracked[BridgeKey{NetworkID: 1, TxHash: testHash}].info,
		"transient errors must not drop the resolved bridge info")
}
