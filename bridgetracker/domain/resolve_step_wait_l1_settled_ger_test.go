package domain

import (
	"context"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// spySettlement is a SettlementSource recording the tx hash it was last called with, so tests
// can pin exactly which settlement WaitL1SettledGERResolver ended up using
type spySettlement struct {
	calledWith common.Hash
	result     *types.L1SettledGERResult
	err        error
}

func (s *spySettlement) SettlementGERUpdate(
	_ context.Context, _ *BridgeInfo, settlementTxHash common.Hash,
) (*types.L1SettledGERResult, error) {
	s.calledWith = settlementTxHash
	return s.result, s.err
}

// noopGERIndex is an L1InfoTreeIndexSource that is never expected to be called in these tests
// (every fixture's settlement result already carries L1InfoTreeIndex)
type noopGERIndex struct{}

func (noopGERIndex) L1InfoTreeIndexForGER(context.Context, *BridgeInfo, common.Hash) (*uint32, error) {
	panic("L1InfoTreeIndexForGER should not be called in this test")
}

// spyHistory is a SettlementHistorySource with canned answers and a call counter for Covers, so
// tests can assert it is skipped entirely when PreviousLER is nil
type spyHistory struct {
	covers                   bool
	coversErr                error
	coversCalls              int
	earliestTxHash           *common.Hash
	earliestProgress         *types.SettlementSearchProgress
	earliestErr              error
	earliestCalledWithBlock  uint64
	earliestCalledWithResume *types.SettlementSearchProgress
}

func (h *spyHistory) Covers(context.Context, *BridgeInfo, common.Hash) (bool, error) {
	h.coversCalls++
	return h.covers, h.coversErr
}

func (h *spyHistory) EarliestSettlementTxCovering(
	_ context.Context, _ *BridgeInfo, fromBlock uint64, resume *types.SettlementSearchProgress,
) (*common.Hash, *types.SettlementSearchProgress, error) {
	h.earliestCalledWithBlock = fromBlock
	h.earliestCalledWithResume = resume
	return h.earliestTxHash, h.earliestProgress, h.earliestErr
}

// waitL1SettledGERTestID is a fixed TrackingID for these tests
var waitL1SettledGERTestID = TrackingID{NetworkID: 5, TxHash: common.HexToHash("0x05")}

// newWaitL1SettledGERTracking builds a *TrackingData with exactly the two steps
// Resolve/exactSettlementTxHash read: StepPendingInclusion (previousLER) and
// StepCertificatePending (cert)
func newWaitL1SettledGERTracking(previousLER *common.Hash, cert *types.CertificateData) *TrackingData {
	steps := []BridgeStepPath{
		{Step: types.StepPendingInclusion, Status: types.StepStatusDone, ResultPendingInclusion: &types.PendingInclusionResult{
			PreviousLER: previousLER,
		}},
		{Step: types.StepCertificatePending, Status: types.StepStatusDone, ResultCertificateData: cert},
		{Step: types.StepWaitL1SettledGER, Status: types.StepStatusInProgress},
	}
	return NewTrackingData(waitL1SettledGERTestID, TrackingBridgeTx{Info: &BridgeInfo{}}, steps)
}

func settledCertFixture(settlementTx common.Hash, blockNumber uint64) *types.CertificateData {
	return &types.CertificateData{
		SettlementTxHash: &settlementTx,
		BlockNumber:      &blockNumber,
	}
}

var certFixtureSettlementTx = common.HexToHash("0xf00d")
var certFixtureBlockNumber = uint64(500)

// TestWaitL1SettledGERResolverNormalPath proves that when StepPendingInclusion's PreviousLER
// does not yet cover the bridge -- the tracked certificate is the one that first included it --
// the resolver uses that certificate's own settlement tx hash unchanged, exactly as before
// issue #1817's exact-settlement search existed
func TestWaitL1SettledGERResolverNormalPath(t *testing.T) {
	previousLER := common.HexToHash("0xaaaa")
	tracking := newWaitL1SettledGERTracking(&previousLER, settledCertFixture(certFixtureSettlementTx, certFixtureBlockNumber))

	settlement := &spySettlement{result: &types.L1SettledGERResult{L1InfoTreeIndex: ptrUint32(3)}}
	history := &spyHistory{covers: false}
	resolver := NewWaitL1SettledGERResolver(settlement, noopGERIndex{}, history)

	result, err := resolver.Resolve(log.NewLoggerNil(), t.Context(), tracking, 0)
	require.NoError(t, err)
	require.Equal(t, settlement.result, result)
	require.Equal(t, certFixtureSettlementTx, settlement.calledWith)
	require.Equal(t, 1, history.coversCalls)
	require.False(t, settlement.result.UsedEarlierSettlement)
}

// TestWaitL1SettledGERResolverFirstCertificateSkipsCoverageCheck proves that a network's very
// first certificate (PreviousLER nil) never even calls Covers: there is nothing earlier that
// could possibly cover the bridge, so the check would be pointless
func TestWaitL1SettledGERResolverFirstCertificateSkipsCoverageCheck(t *testing.T) {
	tracking := newWaitL1SettledGERTracking(nil, settledCertFixture(certFixtureSettlementTx, certFixtureBlockNumber))

	settlement := &spySettlement{result: &types.L1SettledGERResult{L1InfoTreeIndex: ptrUint32(3)}}
	history := &spyHistory{covers: true} // would say yes if asked -- must never be asked
	resolver := NewWaitL1SettledGERResolver(settlement, noopGERIndex{}, history)

	result, err := resolver.Resolve(log.NewLoggerNil(), t.Context(), tracking, 0)
	require.NoError(t, err)
	require.Equal(t, settlement.result, result)
	require.Equal(t, certFixtureSettlementTx, settlement.calledWith)
	require.Zero(t, history.coversCalls)
	require.False(t, settlement.result.UsedEarlierSettlement)
}

// TestWaitL1SettledGERResolverUsesExactEarlierSettlement is the #1817 fix itself: when
// PreviousLER already covers the bridge -- the tracked certificate is not the exact one that
// first included it -- the resolver looks up and uses the earlier, exact settlement's tx hash
// instead of the tracked certificate's own (too recent) one
func TestWaitL1SettledGERResolverUsesExactEarlierSettlement(t *testing.T) {
	previousLER := common.HexToHash("0xaaaa")
	tracking := newWaitL1SettledGERTracking(&previousLER, settledCertFixture(certFixtureSettlementTx, certFixtureBlockNumber))

	exactTxHash := common.HexToHash("0xbeef")
	settlement := &spySettlement{result: &types.L1SettledGERResult{L1InfoTreeIndex: ptrUint32(3)}}
	history := &spyHistory{covers: true, earliestTxHash: &exactTxHash}
	resolver := NewWaitL1SettledGERResolver(settlement, noopGERIndex{}, history)

	result, err := resolver.Resolve(log.NewLoggerNil(), t.Context(), tracking, 0)
	require.NoError(t, err)
	require.Equal(t, settlement.result, result)
	require.Equal(t, exactTxHash, settlement.calledWith)
	require.NotEqual(t, certFixtureSettlementTx, settlement.calledWith)
	require.Equal(t, certFixtureBlockNumber, history.earliestCalledWithBlock)
	require.True(t, settlement.result.UsedEarlierSettlement, "StartDate reads this back to avoid a negative duration")
}

// TestWaitL1SettledGERResolverStartDatePinnedWhenEarlierSettlementUsed proves that once an
// earlier, already-covering settlement was swapped in (result.UsedEarlierSettlement, see issue
// #1817), StartDate pins the step's own start to this same EndDate instead of leaving the
// chained (later) StepCertificatePending value in place -- otherwise the step could read as
// ending before it started. The normal path (UsedEarlierSettlement false) is untouched, keeping
// whatever StartDate the step was already chained to
func TestWaitL1SettledGERResolverStartDatePinnedWhenEarlierSettlementUsed(t *testing.T) {
	resolver := NewWaitL1SettledGERResolver(&spySettlement{}, noopGERIndex{}, &spyHistory{})

	blockTimestamp := uint64(1700000000)
	swapped := &types.L1SettledGERResult{SettlementBlockTimestamp: blockTimestamp, UsedEarlierSettlement: true}
	require.Equal(t, blockTime(blockTimestamp), resolver.StartDate(&BridgeInfo{}, swapped))

	normal := &types.L1SettledGERResult{SettlementBlockTimestamp: blockTimestamp, UsedEarlierSettlement: false}
	require.Nil(t, resolver.StartDate(&BridgeInfo{}, normal), "normal path keeps the chained StartDate")
}

// TestWaitL1SettledGERResolverExactSettlementNotResolvedYet proves the step stays pending
// (ErrStepPending) while EarliestSettlementTxCovering's search has not found the transition yet,
// instead of falling back to the tracked certificate's own (known-too-recent) settlement
func TestWaitL1SettledGERResolverExactSettlementNotResolvedYet(t *testing.T) {
	previousLER := common.HexToHash("0xaaaa")
	tracking := newWaitL1SettledGERTracking(&previousLER, settledCertFixture(certFixtureSettlementTx, certFixtureBlockNumber))

	settlement := &spySettlement{}
	history := &spyHistory{covers: true, earliestTxHash: nil}
	resolver := NewWaitL1SettledGERResolver(settlement, noopGERIndex{}, history)

	_, err := resolver.Resolve(log.NewLoggerNil(), t.Context(), tracking, 0)
	require.ErrorIs(t, err, ErrStepPending)
	require.Equal(t, common.Hash{}, settlement.calledWith) // never called
}

// TestWaitL1SettledGERResolverCertBlockNumberNilStaysPending proves that, even once PreviousLER
// is known to cover the bridge, the resolver waits for the tracked certificate's own settlement
// block to become visible on L1 before anchoring the backwards search on it
func TestWaitL1SettledGERResolverCertBlockNumberNilStaysPending(t *testing.T) {
	previousLER := common.HexToHash("0xaaaa")
	cert := &types.CertificateData{SettlementTxHash: &certFixtureSettlementTx} // BlockNumber nil
	tracking := newWaitL1SettledGERTracking(&previousLER, cert)

	settlement := &spySettlement{}
	history := &spyHistory{covers: true}
	resolver := NewWaitL1SettledGERResolver(settlement, noopGERIndex{}, history)

	_, err := resolver.Resolve(log.NewLoggerNil(), t.Context(), tracking, 0)
	require.ErrorIs(t, err, ErrStepPending)
}

// TestWaitL1SettledGERResolverPersistsSearchProgress proves that when
// EarliestSettlementTxCovering's backwards search needs more than one engine tick to finish (see
// types.SettlementSearchProgress), Resolve persists that progress as this step's own Result and
// stays pending, instead of erroring or falling back to the tracked certificate's own
// (known-too-recent) settlement
func TestWaitL1SettledGERResolverPersistsSearchProgress(t *testing.T) {
	previousLER := common.HexToHash("0xaaaa")
	tracking := newWaitL1SettledGERTracking(&previousLER, settledCertFixture(certFixtureSettlementTx, certFixtureBlockNumber))

	progress := &types.SettlementSearchProgress{NextToBlock: 42}
	settlement := &spySettlement{}
	history := &spyHistory{covers: true, earliestProgress: progress}
	resolver := NewWaitL1SettledGERResolver(settlement, noopGERIndex{}, history)

	result, err := resolver.Resolve(log.NewLoggerNil(), t.Context(), tracking, 2)
	require.ErrorIs(t, err, ErrStepPending)
	require.Equal(t, progress, result)
	require.Equal(t, common.Hash{}, settlement.calledWith) // never called
}

// TestWaitL1SettledGERResolverResumesSearchFromPersistedProgress proves that a previously
// persisted search progress -- this step's own prior Result -- is read back and threaded into
// EarliestSettlementTxCovering as resume, instead of restarting the search from scratch every tick
func TestWaitL1SettledGERResolverResumesSearchFromPersistedProgress(t *testing.T) {
	previousLER := common.HexToHash("0xaaaa")
	steps := []BridgeStepPath{
		{Step: types.StepPendingInclusion, Status: types.StepStatusDone, ResultPendingInclusion: &types.PendingInclusionResult{
			PreviousLER: &previousLER,
		}},
		{
			Step: types.StepCertificatePending, Status: types.StepStatusDone,
			ResultCertificateData: settledCertFixture(certFixtureSettlementTx, certFixtureBlockNumber),
		},
		{
			Step: types.StepWaitL1SettledGER, Status: types.StepStatusInProgress,
			ResultSettlementSearch: &types.SettlementSearchProgress{NextToBlock: 42},
		},
	}
	tracking := NewTrackingData(waitL1SettledGERTestID, TrackingBridgeTx{Info: &BridgeInfo{}}, steps)

	exactTxHash := common.HexToHash("0xbeef")
	settlement := &spySettlement{result: &types.L1SettledGERResult{L1InfoTreeIndex: ptrUint32(3)}}
	history := &spyHistory{covers: true, earliestTxHash: &exactTxHash}
	resolver := NewWaitL1SettledGERResolver(settlement, noopGERIndex{}, history)

	result, err := resolver.Resolve(log.NewLoggerNil(), t.Context(), tracking, 2)
	require.NoError(t, err)
	require.Equal(t, settlement.result, result)
	require.Equal(t, &types.SettlementSearchProgress{NextToBlock: 42}, history.earliestCalledWithResume)
}

// TestWaitL1SettledGERResolverCoversErrorPropagates proves a transient failure checking
// coverage is surfaced as an error (retried by the engine), not silently treated as "normal path"
func TestWaitL1SettledGERResolverCoversErrorPropagates(t *testing.T) {
	previousLER := common.HexToHash("0xaaaa")
	tracking := newWaitL1SettledGERTracking(&previousLER, settledCertFixture(certFixtureSettlementTx, certFixtureBlockNumber))

	errCovers := errors.New("covers check failed")
	settlement := &spySettlement{}
	history := &spyHistory{coversErr: errCovers}
	resolver := NewWaitL1SettledGERResolver(settlement, noopGERIndex{}, history)

	_, err := resolver.Resolve(log.NewLoggerNil(), t.Context(), tracking, 0)
	require.ErrorIs(t, err, errCovers)
}

func ptrUint32(v uint32) *uint32 { return &v }
