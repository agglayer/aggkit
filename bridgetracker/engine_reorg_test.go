package bridgetracker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	domainmocks "github.com/agglayer/aggkit/bridgetracker/domain/mocks"
	finalitymocks "github.com/agglayer/aggkit/bridgetracker/finality/mocks"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	reorgOriginHash = common.HexToHash("0xa1")
	reorgClaimTx    = common.HexToHash("0x0c")
	// errBridgeServiceReorg is what a bridge service answers while it resolves a reorg of its own
	errBridgeServiceReorg = errors.New(
		"503 Service Unavailable: a syncer is temporarily inconsistent (reorg being resolved)")
)

// reorgFacts drives the reorg ports of the engine (agglayer/aggkit#1970) through generated mocks
// whose answers read these fields, so a test changes the chain between ticks
type reorgFacts struct {
	mu sync.Mutex
	// canonical is the hash the origin chain has at any block; zero means reorgOriginHash
	canonical    common.Hash
	canonicalErr error
	originFinal  bool
	claimFinal   bool
	// injectedGone/injectedFinal/injectedErr answer InjectedGERPresent
	injectedGone  bool
	injectedFinal bool
	injectedErr   error
	// leafFinal/leafFinalErr answer L1InfoLeafFinalized; leafFinalCalls counts its calls
	leafFinal      bool
	leafFinalErr   error
	leafFinalCalls int

	finality *finalitymocks.Resolver
}

func (r *reorgFacts) set(update func(r *reorgFacts)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	update(r)
}

// sources wires the mocks reading r into f's engine sources
func (r *reorgFacts) sources(t *testing.T, f *fakeSources) EngineSources {
	t.Helper()
	sources := f.engineSources()

	origin := domainmocks.NewOriginBlockSource(t)
	origin.EXPECT().CanonicalBlockHash(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, uint32, uint64) (common.Hash, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.canonical == (common.Hash{}) {
				return reorgOriginHash, r.canonicalErr
			}
			return r.canonical, r.canonicalErr
		}).Maybe()

	r.finality = finalitymocks.NewResolver(t)
	r.finality.EXPECT().IsFinal(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, _ uint32, txHash common.Hash, _ uint64, _ *bool) (bool, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if txHash == testHash {
				return r.originFinal, nil
			}
			return r.claimFinal, nil
		}).Maybe()

	injected := domainmocks.NewInjectedGERChecker(t)
	injected.EXPECT().InjectedGERPresent(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, *domain.BridgeInfo, uint32) (*types.GERData, bool, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.injectedGone {
				return nil, r.injectedFinal, r.injectedErr
			}
			// where it is is not reported: never told moved, the stored injection stays
			return &types.GERData{}, r.injectedFinal, r.injectedErr
		}).Maybe()
	injected.EXPECT().L1InfoLeafFinalized(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, *domain.BridgeInfo, uint32) (bool, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.leafFinalCalls++
			return r.leafFinal, r.leafFinalErr
		}).Maybe()

	sources.OriginBlocks, sources.Finality, sources.InjectedGERs = origin, r.finality, injected
	return sources
}

// newReorgTestEngine wires an engine over store with the fakes plus the reorg mocks, on a fake clock
func newReorgTestEngine(
	t *testing.T, store *memoryRegistry, f *fakeSources, facts *reorgFacts,
) (*Engine, *time.Time) {
	t.Helper()
	engine, err := NewEngine(EngineConfig{UnresolvedTimeout: 20 * time.Second},
		log.WithFields("module", "engine_reorg_test"), store, facts.sources(t, f))
	require.NoError(t, err)
	clock := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	engine.now = func() time.Time { return clock }
	store.now = engine.now
	return engine, &clock
}

// l1ToL2ReorgBridge is an L1->L2 bridge carrying its origin block hash (the one CheckOrigin compares)
func l1ToL2ReorgBridge() *BridgeInfo {
	return &BridgeInfo{
		NetworkID: 0, LeafType: types.BridgeLeafTypeAsset, DestinationNetwork: 1, DepositCount: 7,
		BlockNumber: 500, BlockHash: reorgOriginHash, LogIndex: 1,
	}
}

// driveToReadyToClaim makes the fakes report every L1->L2 milestone up to the claim
func driveToReadyToClaim(f *fakeSources) {
	ger := common.HexToHash("0x0a")
	blockNumber := uint64(10)
	f.originGER = &types.GERData{NetworkID: 0, GER: &ger, BlockNumber: &blockNumber, LERType: types.LERTypeMainnet}
	injectedBlock := uint64(600)
	f.injectedGERAtIndex = &types.GERData{NetworkID: 1, GER: &ger, LERType: types.LERTypeNA, BlockNumber: &injectedBlock}
	leafIndex := uint32(3)
	f.l1InfoTreeIndexForBridge = &leafIndex
}

// driveToClaimed makes the fakes report the claim too
func driveToClaimed(f *fakeSources) {
	driveToReadyToClaim(f)
	f.claimed = true
	f.claim = &types.ClaimResult{ClaimTx: reorgClaimTx, BlockNumber: 700, BlockTimestamp: 1700000700}
}

func mustGetFrom(t *testing.T, store *memoryRegistry, id TrackingID) *domain.TrackingData {
	t.Helper()
	tracking, err := store.Get(id, false)
	require.NoError(t, err)
	return tracking
}

func currentStepID(tracking *domain.TrackingData) types.BridgeStep {
	return tracking.AllSteps()[*tracking.StepIndex()].Step
}

// receiveUpdate returns the latest pending update on ch, failing if there is none
func receiveUpdate(t *testing.T, ch <-chan *domain.TrackingData) *domain.TrackingData {
	t.Helper()
	select {
	case update := <-ch:
		return update
	default:
		require.FailNow(t, "expected an update to be published")
		return nil
	}
}

// TestEngineReorgMovesBackwardsAndNotifies walks an L1->L2 bridge forward to claimed (not final),
// then back to readyToClaim when the claim is gone, back to pending when the GER injection is gone
// too, and forward again to claimed_finalized — every backward move published to the subscriber
// exactly like a forward one
func TestEngineReorgMovesBackwardsAndNotifies(t *testing.T) {
	id := TrackingID{NetworkID: 0, TxHash: testHash}
	f := &fakeSources{bridge: l1ToL2ReorgBridge()}
	facts := &reorgFacts{}
	store := newMemoryRegistry(0)
	engine, _ := newReorgTestEngine(t, store, f, facts)

	updates, unsubscribe, err := store.Subscribe(id)
	require.NoError(t, err)
	defer unsubscribe()

	driveToClaimed(f)
	engine.tick(t.Context())
	tracking := receiveUpdate(t, updates)
	require.Equal(t, types.TrackerClaimStatusClaimed, tracking.ClaimStatus())
	require.Equal(t, types.TrackingStatusRunning, tracking.TrackingStatus(), "claim not final: still supervised")
	require.Len(t, mustGetTrackerActives(t, store), 1)

	// the claim is reorged out of the destination
	f.claimed, f.claim = false, nil
	engine.tick(t.Context())
	tracking = receiveUpdate(t, updates)
	require.Equal(t, types.TrackerClaimStatusReadyToClaim, tracking.ClaimStatus())
	require.Equal(t, types.StepWaitingClaim, currentStepID(tracking))

	// the GER injection is reorged out too
	facts.set(func(r *reorgFacts) { r.injectedGone = true })
	f.injectedGERAtIndex = nil
	engine.tick(t.Context())
	tracking = receiveUpdate(t, updates)
	require.Equal(t, types.TrackerClaimStatusPending, tracking.ClaimStatus())
	require.Equal(t, types.StepWaitingGERInjection, currentStepID(tracking))

	// everything comes back, and the claim is final this time: closed for good
	facts.set(func(r *reorgFacts) { r.injectedGone, r.claimFinal = false, true })
	driveToClaimed(f)
	engine.tick(t.Context())
	tracking = receiveUpdate(t, updates)
	require.Equal(t, types.TrackerClaimStatusClaimedFinalized, tracking.ClaimStatus())
	require.Equal(t, types.TrackingStatusFinished, tracking.TrackingStatus())
	require.True(t, tracking.Info().BlockFinalized, "a final claim implies a final origin bridge")
	require.Empty(t, mustGetTrackerActives(t, store))
}

// TestEngineL2ToL1FinalLeafIsNotCheckedAgain pins #1987 through the engine: at readyToClaim an
// L2->L1 bridge asks whether its L1 info leaf is final on every tick, the answer is persisted and
// published once it is, and from then on the leaf is not looked at anymore
func TestEngineL2ToL1FinalLeafIsNotCheckedAgain(t *testing.T) {
	id := TrackingID{NetworkID: 1, TxHash: testHash}
	f := &fakeSources{bridge: l2ToL1Bridge()}
	facts := &reorgFacts{}
	store := newMemoryRegistry(0)
	engine, _ := newReorgTestEngine(t, store, f, facts)
	mustRegister(t, store, id)

	f.originLER = &types.LERUpdateResult{NetworkID: 1, LER: common.HexToHash("0x0a"), BlockNumber: 10}
	settledBlockNumber, settledBlockTimestamp := uint64(1500), uint64(1700001500)
	f.cert = &types.CertificateInclusionData{
		CertificateData: types.CertificateData{
			CertificateID: common.HexToHash("0x01"), Status: agglayertypes.Settled,
			SettlementTxHash: &settlementTxHash,
			BlockNumber:      &settledBlockNumber, BlockTimestamp: &settledBlockTimestamp,
		},
	}
	leafIndex := uint32(7)
	f.settlement = &types.L1SettledGERResult{
		TxHash: settlementTxHash, SettlementBlockNumber: 2000, GER: common.HexToHash("0x0b"),
		L1InfoTreeIndex:                   &leafIndex,
		HasVerifyBatchesTrustedAggregator: true, HasUpdateL1InfoTree: true,
	}
	f.l1InfoTreeIndexForBridge = &leafIndex
	leafStep := func() domain.BridgeStepPath {
		for _, step := range mustGetFrom(t, store, id).AllSteps() {
			if step.Step == types.StepWaitingL1InfoLeafAvailable {
				return step
			}
		}
		require.FailNow(t, "no L1 info leaf step")
		return domain.BridgeStepPath{}
	}

	engine.tick(t.Context())
	require.Equal(t, types.TrackerClaimStatusReadyToClaim, mustGetFrom(t, store, id).ClaimStatus())

	// not final, then a failing check: asked on every tick, nothing moves
	engine.tick(t.Context())
	facts.set(func(r *reorgFacts) { r.leafFinalErr = errBridgeServiceReorg })
	engine.tick(t.Context())
	require.False(t, leafStep().BlockFinalized)
	require.Equal(t, types.TrackerClaimStatusReadyToClaim, mustGetFrom(t, store, id).ClaimStatus())
	facts.set(func(r *reorgFacts) {
		require.GreaterOrEqual(t, r.leafFinalCalls, 2)
		r.leafFinal, r.leafFinalErr = true, nil
	})

	updates, unsubscribe, err := store.Subscribe(id)
	require.NoError(t, err)
	defer unsubscribe()
	engine.tick(t.Context())
	published := receiveUpdate(t, updates)
	require.Same(t, mustGetFrom(t, store, id), published, "the leaf turning final is persisted and published")
	require.True(t, leafStep().BlockFinalized)
	var callsWhenFinal int
	facts.set(func(r *reorgFacts) { callsWhenFinal = r.leafFinalCalls })

	// final: neither the index nor the finality is asked again, so a leaf that "disappears"
	// from the answers moves nothing
	f.l1InfoTreeIndexForBridge, f.l1InfoTreeIndexForBridgeErr = nil, errBridgeServiceReorg
	for range 3 {
		engine.tick(t.Context())
	}
	require.Equal(t, types.TrackerClaimStatusReadyToClaim, mustGetFrom(t, store, id).ClaimStatus())
	require.True(t, leafStep().BlockFinalized)
	facts.set(func(r *reorgFacts) { require.Equal(t, callsWhenFinal, r.leafFinalCalls) })
}

// TestEngineFinalTrackingIsNeverReevaluated pins the closing rule: once claimed_finalized, nothing
// of the tracking is asked again, whatever the sources say afterwards
func TestEngineFinalTrackingIsNeverReevaluated(t *testing.T) {
	id := TrackingID{NetworkID: 0, TxHash: testHash}
	f := &fakeSources{bridge: l1ToL2ReorgBridge()}
	facts := &reorgFacts{claimFinal: true}
	store := newMemoryRegistry(0)
	engine, _ := newReorgTestEngine(t, store, f, facts)
	mustRegister(t, store, id)

	driveToClaimed(f)
	engine.tick(t.Context())
	final := mustGetFrom(t, store, id)
	require.Equal(t, types.TrackerClaimStatusClaimedFinalized, final.ClaimStatus())
	finalityCalls := len(facts.finality.Calls)

	// a later "reorg" the tracker must not even look at
	f.claimed, f.claim, f.claimedErr = false, nil, errBridgeServiceReorg
	f.bridgeErr = ErrBridgeTxNotFound
	facts.set(func(r *reorgFacts) { r.canonical, r.injectedGone = common.HexToHash("0xff"), true })
	for range 3 {
		engine.tick(t.Context())
	}
	require.Same(t, final, mustGetFrom(t, store, id), "never re-evaluated, never rewritten")
	require.Len(t, facts.finality.Calls, finalityCalls)
	require.Empty(t, mustGetTrackerActives(t, store))
}

// TestEngineClaimedNotFinalStaysSupervisedPastRetention pins that a claim that is not final keeps
// the tracking supervised however long it takes: it is not terminal, so the retention window
// never starts and PruneTerminal never drops it
func TestEngineClaimedNotFinalStaysSupervisedPastRetention(t *testing.T) {
	id := TrackingID{NetworkID: 0, TxHash: testHash}
	f := &fakeSources{bridge: l1ToL2ReorgBridge()}
	facts := &reorgFacts{}
	store := newMemoryRegistry(0)
	engine, clock := newReorgTestEngine(t, store, f, facts)
	mustRegister(t, store, id)

	driveToClaimed(f)
	engine.tick(t.Context())
	require.Equal(t, types.TrackerClaimStatusClaimed, mustGetFrom(t, store, id).ClaimStatus())

	*clock = clock.Add(3 * engine.cfg.RetentionPeriod)
	mustRegister(t, store, id) // a client still polling it (keeps the idle timeout away)
	engine.tick(t.Context())
	tracking := mustGetFrom(t, store, id)
	require.Equal(t, types.TrackerClaimStatusClaimed, tracking.ClaimStatus())
	require.Len(t, mustGetTrackerActives(t, store), 1)

	// final at last: terminal, then forgotten once its own retention elapses
	facts.set(func(r *reorgFacts) { r.claimFinal = true })
	engine.tick(t.Context())
	require.Equal(t, types.TrackerClaimStatusClaimedFinalized, mustGetFrom(t, store, id).ClaimStatus())
	*clock = clock.Add(engine.cfg.RetentionPeriod + time.Second)
	engine.tick(t.Context())
	_, err := store.Get(id, false)
	require.ErrorIs(t, err, domain.ErrTrackingNotFound)
}

// TestEngineTransientErrorsDoNotMoveBackwards pins that a bridge service 503 (reorg being
// resolved) or an RPC failure is never read as "gone": the state is kept, and checked again
func TestEngineTransientErrorsDoNotMoveBackwards(t *testing.T) {
	id := TrackingID{NetworkID: 0, TxHash: testHash}
	f := &fakeSources{bridge: l1ToL2ReorgBridge()}
	facts := &reorgFacts{}
	store := newMemoryRegistry(0)
	engine, _ := newReorgTestEngine(t, store, f, facts)
	mustRegister(t, store, id)

	driveToClaimed(f)
	engine.tick(t.Context())
	claimed := mustGetFrom(t, store, id)
	require.Equal(t, types.TrackerClaimStatusClaimed, claimed.ClaimStatus())

	f.claimedErr = errors.New("rpc down")
	engine.tick(t.Context())
	require.Same(t, claimed, mustGetFrom(t, store, id), "isClaimed() failing: nothing written")

	f.claimedErr, f.claimErr = nil, errBridgeServiceReorg
	facts.set(func(r *reorgFacts) { r.canonicalErr = errors.New("rpc down") })
	engine.tick(t.Context())
	require.Same(t, claimed, mustGetFrom(t, store, id), "claim record answering 503: nothing written")

	// at readyToClaim, the injection check answering 503 does not send it back to pending
	f.claimErr = nil
	f.claimed, f.claim = false, nil
	facts.set(func(r *reorgFacts) { r.canonicalErr, r.injectedErr = nil, errBridgeServiceReorg })
	engine.tick(t.Context())
	tracking := mustGetFrom(t, store, id)
	require.Equal(t, types.TrackerClaimStatusReadyToClaim, tracking.ClaimStatus())
	engine.tick(t.Context())
	require.Equal(t, types.TrackerClaimStatusReadyToClaim, mustGetFrom(t, store, id).ClaimStatus())
}

// TestEngineOriginReorgedOut pins the origin bridge reorged out while tracked, re-checked on the
// engine tick (not only on Get) with the in-memory registry: the tracking is discarded back to
// registered and resolved again, ending up at the bridge's new location once it is re-included in
// another block. TestEngineOriginReorgedOutSQLite (package bridgetracker_test) does the same over
// the SQLite registry
func TestEngineOriginReorgedOut(t *testing.T) {
	id := TrackingID{NetworkID: 0, TxHash: testHash}
	f := &fakeSources{bridge: l1ToL2ReorgBridge()}
	facts := &reorgFacts{}
	store := newMemoryRegistry(0)
	engine, _ := newReorgTestEngine(t, store, f, facts)
	updates, unsubscribe, err := store.Subscribe(id)
	require.NoError(t, err)
	defer unsubscribe()

	driveToReadyToClaim(f)
	engine.tick(t.Context())
	tracking := receiveUpdate(t, updates)
	require.Equal(t, types.TrackerClaimStatusReadyToClaim, tracking.ClaimStatus())
	require.False(t, tracking.Info().BlockFinalized)

	// the origin block is replaced and the tx is not mined again (yet)
	newHash := common.HexToHash("0xa2")
	facts.set(func(r *reorgFacts) { r.canonical = newHash })
	f.bridge, f.bridgeErr = nil, ErrBridgeTxNotFound
	engine.tick(t.Context())
	tracking = receiveUpdate(t, updates)
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus())
	require.Equal(t, types.TrackerClaimStatusPending, tracking.ClaimStatus())
	require.Nil(t, tracking.Info())
	require.Nil(t, tracking.AllSteps(), "the path of the reorged-out bridge is dropped")
	require.Len(t, mustGetTrackerActives(t, store), 1, "still supervised: FindBridge keeps looking for it")

	// re-included in another block: tracked at its new location, from scratch
	moved := l1ToL2ReorgBridge()
	moved.BlockNumber, moved.BlockHash = 503, newHash
	f.bridge, f.bridgeErr = moved, nil
	facts.set(func(r *reorgFacts) { r.originFinal = true })
	engine.tick(t.Context())
	tracking = receiveUpdate(t, updates)
	require.Equal(t, uint64(503), tracking.Info().BlockNumber)
	require.Equal(t, newHash, tracking.Info().BlockHash)
	require.True(t, tracking.Info().BlockFinalized, "bridge_status.block_finalized of the new location")
	require.Equal(t, types.TrackerClaimStatusReadyToClaim, tracking.ClaimStatus())
}

// TestEngineOriginReorgedOutNeverReincluded pins what the tracker reports for a tracked tx that a
// reorg removes for good: the tracking goes back to registered with a fresh UnresolvedTimeout
// (counted from the reorg, not from when the tx was first tracked) and, once that elapses without
// the tx being mined again, ends in the terminal error of a tx that does not exist, with nothing
// left of the bridge that had been resolved
func TestEngineOriginReorgedOutNeverReincluded(t *testing.T) {
	id := TrackingID{NetworkID: 0, TxHash: testHash}
	f := &fakeSources{bridge: l1ToL2ReorgBridge()}
	facts := &reorgFacts{}
	store := newMemoryRegistry(0)
	engine, clock := newReorgTestEngine(t, store, f, facts)
	updates, unsubscribe, err := store.Subscribe(id)
	require.NoError(t, err)
	defer unsubscribe()

	driveToReadyToClaim(f)
	engine.tick(t.Context())
	tracking := receiveUpdate(t, updates)
	require.Equal(t, types.TrackerClaimStatusReadyToClaim, tracking.ClaimStatus())

	// tracked for longer than UnresolvedTimeout before the reorg: the timeout of the first
	// resolution must not count against the tx once it is reorged out
	*clock = clock.Add(3 * engine.cfg.UnresolvedTimeout)
	facts.set(func(r *reorgFacts) { r.canonical = common.HexToHash("0xa2") })
	f.bridge, f.bridgeErr = nil, ErrBridgeTxNotFound
	engine.tick(t.Context())
	tracking = receiveUpdate(t, updates)
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus(), "a fresh UnresolvedTimeout starts at the reorg")
	require.Equal(t, types.TrackerClaimStatusPending, tracking.ClaimStatus())
	require.Nil(t, tracking.Info())
	require.Nil(t, tracking.AllSteps())
	require.False(t, tracking.Failed())
	require.Len(t, mustGetTrackerActives(t, store), 1)

	// still missing just before the timeout: still looked for
	*clock = clock.Add(engine.cfg.UnresolvedTimeout - time.Second)
	engine.tick(t.Context())
	tracking = mustGetFrom(t, store, id)
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus())
	require.False(t, tracking.Failed())

	// never re-included: given up on, like any tx that does not exist on the network
	*clock = clock.Add(time.Second)
	engine.tick(t.Context())
	tracking = mustGetFrom(t, store, id)
	require.Equal(t, types.TrackingStatusError, tracking.TrackingStatus())
	require.Equal(t, types.TrackerClaimStatusError, tracking.ClaimStatus())
	require.Nil(t, tracking.Info(), "the bridge resolved before the reorg must not come back")
	require.Nil(t, tracking.StepIndex())
	require.Nil(t, tracking.AllSteps())
	require.True(t, tracking.Failed())
	require.NotNil(t, tracking.Error())
	require.Equal(t, types.StepErrorExhausted, tracking.Error().ErrorType)
	require.Empty(t, mustGetTrackerActives(t, store), "a tx given up on leaves the active list")
}

// TestEngineOriginReincludedInAnotherBlockSameTick pins the common case: the reorged-out bridge
// tx is already re-mined when the reorg is seen, so it moves to its new block in the same tick
func TestEngineOriginReincludedInAnotherBlockSameTick(t *testing.T) {
	id := TrackingID{NetworkID: 0, TxHash: testHash}
	f := &fakeSources{bridge: l1ToL2ReorgBridge()}
	facts := &reorgFacts{}
	store := newMemoryRegistry(0)
	engine, _ := newReorgTestEngine(t, store, f, facts)
	mustRegister(t, store, id)
	driveToReadyToClaim(f)
	engine.tick(t.Context())
	require.Equal(t, uint64(500), mustGetFrom(t, store, id).Info().BlockNumber)

	newHash := common.HexToHash("0xa3")
	moved := l1ToL2ReorgBridge()
	moved.BlockNumber, moved.BlockHash = 501, newHash
	f.bridge = moved
	facts.set(func(r *reorgFacts) { r.canonical = common.HexToHash("0xa4") })
	engine.tick(t.Context())
	tracking := mustGetFrom(t, store, id)
	require.Equal(t, uint64(501), tracking.Info().BlockNumber)
	require.Equal(t, newHash, tracking.Info().BlockHash)
	require.Equal(t, types.TrackerClaimStatusReadyToClaim, tracking.ClaimStatus(), "every step re-resolved at once")
}

// TestEngineOriginBlockFinalized pins bridge_status.block_finalized: set as soon as the origin block
// is final, published, and the origin is not checked anymore afterwards
func TestEngineOriginBlockFinalized(t *testing.T) {
	id := TrackingID{NetworkID: 0, TxHash: testHash}
	f := &fakeSources{bridge: l1ToL2ReorgBridge()}
	facts := &reorgFacts{}
	store := newMemoryRegistry(0)
	engine, _ := newReorgTestEngine(t, store, f, facts)
	mustRegister(t, store, id)

	engine.tick(t.Context())
	require.False(t, mustGetFrom(t, store, id).Info().BlockFinalized)

	facts.set(func(r *reorgFacts) { r.originFinal = true })
	engine.tick(t.Context())
	require.True(t, mustGetFrom(t, store, id).Info().BlockFinalized)

	// final origin: a changed hash is not even looked at anymore
	facts.set(func(r *reorgFacts) { r.canonical = common.HexToHash("0xff") })
	engine.tick(t.Context())
	tracking := mustGetFrom(t, store, id)
	require.NotNil(t, tracking.Info())
	require.Equal(t, uint64(500), tracking.Info().BlockNumber)
}

// TestEngineOriginReorgedOutKeepsLogSelector pins that the reorg paths work per bridge event:
// a tracking selecting one BridgeEvent of its tx is reset when its origin
// is reorged out and re-resolved under the very same selector, never falling back to the tx-only
// identity (which would fail as ambiguous on a multi-bridge tx)
func TestEngineOriginReorgedOutKeepsLogSelector(t *testing.T) {
	id := TrackingID{NetworkID: 0, TxHash: testHash, Log: domain.LogIndex(1)}
	f := &fakeSources{bridge: l1ToL2ReorgBridge()}
	facts := &reorgFacts{}
	store := newMemoryRegistry(0)
	engine, _ := newReorgTestEngine(t, store, f, facts)
	mustRegister(t, store, id)
	driveToReadyToClaim(f)
	engine.tick(t.Context())
	require.Equal(t, types.TrackerClaimStatusReadyToClaim, mustGetFrom(t, store, id).ClaimStatus())

	// the origin block is replaced and the tx is not mined again (yet): reset, same id
	newHash := common.HexToHash("0xa5")
	facts.set(func(r *reorgFacts) { r.canonical = newHash })
	f.bridge, f.bridgeErr = nil, ErrBridgeTxNotFound
	engine.tick(t.Context())
	tracking := mustGetFrom(t, store, id)
	require.Equal(t, id, tracking.ID())
	require.Nil(t, tracking.Info())

	moved := l1ToL2ReorgBridge()
	moved.BlockNumber, moved.BlockHash = 503, newHash
	f.bridge, f.bridgeErr = moved, nil
	engine.tick(t.Context())
	tracking = mustGetFrom(t, store, id)
	require.Equal(t, uint64(503), tracking.Info().BlockNumber)
	require.Equal(t, 1, store.GetNumTracker(), "no tx-only entry was created along the way")

	f.findBridgeMu.Lock()
	defer f.findBridgeMu.Unlock()
	require.NotEmpty(t, f.findBridgeIDs)
	for _, asked := range f.findBridgeIDs {
		require.Equal(t, id, asked, "every (re-)resolution asks for the selected event")
	}
}
