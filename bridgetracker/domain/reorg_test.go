package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	domainmocks "github.com/agglayer/aggkit/bridgetracker/domain/mocks"
	finalitymocks "github.com/agglayer/aggkit/bridgetracker/finality/mocks"
	trackermocks "github.com/agglayer/aggkit/bridgetracker/mocks"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	reorgTxHash    = common.HexToHash("0xb1")
	reorgBlockHash = common.HexToHash("0xbb")
	reorgClaimTx   = common.HexToHash("0xc1")
	reorgNow       = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	// errReorgInProgress is what the bridge service answers while it resolves a reorg of its own
	errReorgInProgress = errors.New("503 Service Unavailable: a syncer is temporarily inconsistent (reorg being resolved)")
	reorgLogger        = log.WithFields("module", "reorg_test")
)

// reorgCheckers holds strict mocks: a test only sets the expectations of the calls it allows, so
// any other call fails it
type reorgCheckers struct {
	origin   *domainmocks.OriginBlockSource
	finality *finalitymocks.Resolver
	claimed  *trackermocks.ClaimChecker
	claims   *trackermocks.ClaimSource
	injected *domainmocks.InjectedGERChecker
	leaves   *trackermocks.GERSource
}

func newReorgCheckers(t *testing.T) *reorgCheckers {
	t.Helper()
	return &reorgCheckers{
		origin:   domainmocks.NewOriginBlockSource(t),
		finality: finalitymocks.NewResolver(t),
		claimed:  trackermocks.NewClaimChecker(t),
		claims:   trackermocks.NewClaimSource(t),
		injected: domainmocks.NewInjectedGERChecker(t),
		leaves:   trackermocks.NewGERSource(t),
	}
}

func (c *reorgCheckers) ports() domain.ReorgCheckers {
	return domain.ReorgCheckers{
		OriginBlocks: c.origin, Finality: c.finality, ClaimChecker: c.claimed, Claims: c.claims,
		InjectedGERs: c.injected, Leaves: c.leaves,
	}
}

func bridgeFor(bridgeType types.BridgeType) *domain.BridgeInfo {
	info := &domain.BridgeInfo{
		LeafType: types.BridgeLeafTypeAsset, DepositCount: 7, BlockNumber: 1000, BlockHash: reorgBlockHash,
	}
	switch bridgeType {
	case types.BridgeTypeL1ToL2:
		info.NetworkID, info.DestinationNetwork = 0, 1
	case types.BridgeTypeL2ToL1:
		info.NetworkID, info.DestinationNetwork = 1, 0
	default:
		info.NetworkID, info.DestinationNetwork = 1, 2
	}
	return info
}

// trackingAt builds a resolved tracking of bridgeType whose steps before current are Done (with
// the results the reorg checks read) and current is InProgress. current StepClaimed with
// claimDone true means every step Done, the claim not final
func trackingAt(bridgeType types.BridgeType, current types.BridgeStep, claimDone bool) *domain.TrackingData {
	info := bridgeFor(bridgeType)
	leafIndex := uint32(5)
	start := reorgNow.Add(-time.Hour)
	path := domain.ExpectedPath(bridgeType)
	steps := make([]domain.BridgeStepPath, len(path))
	reached := false
	for i, stepID := range path {
		step := domain.BridgeStepPath{Step: stepID, Status: types.StepStatusPending}
		switch {
		case stepID == current && (!claimDone || stepID != types.StepClaimed):
			reached = true
			step.Status = types.StepStatusInProgress
			step.StartDate = &start
		case !reached:
			step.Status = types.StepStatusDone
			step.StartDate, step.EndDate = &start, &start
			switch stepID {
			case types.StepWaitingGERUpdate:
				step.SetResult(&types.GERUpdateResult{L1InfoTreeIndex: leafIndex})
			case types.StepWaitL1SettledGER:
				step.SetResult(&types.L1SettledGERResult{L1InfoTreeIndex: &leafIndex})
			case types.StepWaitingGERInjection:
				step.SetResult(&types.InjectedGERResult{})
			case types.StepWaitingL1InfoLeafAvailable:
				step.SetResult(&types.L1InfoLeafAvailableResult{L1InfoTreeIndex: leafIndex})
			case types.StepClaimed:
				step.SetResult(&types.ClaimResult{ClaimTx: reorgClaimTx, BlockNumber: 3000, BlockTimestamp: 1700003000})
			}
		}
		steps[i] = step
	}
	return domain.NewTrackingData(
		domain.TrackingID{NetworkID: info.NetworkID, TxHash: reorgTxHash}, domain.TrackingBridgeTx{Info: info}, steps)
}

func claimedTracking(bridgeType types.BridgeType) *domain.TrackingData {
	return trackingAt(bridgeType, types.StepClaimed, true)
}

// stepOf returns the step stepID of tracking
func stepOf(t *testing.T, tracking *domain.TrackingData, stepID types.BridgeStep) domain.BridgeStepPath {
	t.Helper()
	for _, step := range tracking.AllSteps() {
		if step.Step == stepID {
			return step
		}
	}
	require.FailNow(t, "step not in path", "%s", stepID)
	return domain.BridgeStepPath{}
}

func currentStepOf(t *testing.T, tracking *domain.TrackingData) types.BridgeStep {
	t.Helper()
	return tracking.AllSteps()[*tracking.StepIndex()].Step
}

// requireRewoundTo asserts tracking was moved back to stepID: reopened InProgress with nothing of
// its previous completion left, and every later step back to Pending
func requireRewoundTo(t *testing.T, tracking *domain.TrackingData, stepID types.BridgeStep) {
	t.Helper()
	require.Equal(t, stepID, currentStepOf(t, tracking))
	after := false
	for _, step := range tracking.AllSteps() {
		switch {
		case step.Step == stepID:
			after = true
			require.Equal(t, types.StepStatusInProgress, step.Status)
			require.NotNil(t, step.StartDate, "a reopened step keeps when it started")
			require.Nil(t, step.EndDate)
			require.Nil(t, step.Result())
			require.Nil(t, step.Error)
			require.False(t, step.BlockFinalized)
		case after:
			require.Equal(t, domain.BridgeStepPath{Step: step.Step, Status: types.StepStatusPending}, step,
				"%s: back to pending as if never reached", step.Step)
		default:
			require.Equal(t, types.StepStatusDone, step.Status, "%s: before the rewind point, untouched", step.Step)
		}
	}
}

var allBridgeTypes = []types.BridgeType{types.BridgeTypeL1ToL2, types.BridgeTypeL2ToL1, types.BridgeTypeL2ToL2}

// readinessStep is the earliest step ReverifySteps re-checks at readyToClaim on each path: the GER
// injection where there is one, the L1 info leaf otherwise (L2->L1)
func readinessStep(bridgeType types.BridgeType) types.BridgeStep {
	if bridgeType == types.BridgeTypeL2ToL1 {
		return types.StepWaitingL1InfoLeafAvailable
	}
	return types.StepWaitingGERInjection
}

// expectReadinessPresent allows the readiness checks of bridgeType at readyToClaim and answers
// that everything is still there, not final
func (c *reorgCheckers) expectReadinessPresent(bridgeType types.BridgeType) {
	if bridgeType != types.BridgeTypeL2ToL1 {
		c.injected.EXPECT().InjectedGERPresent(mock.Anything, mock.Anything, uint32(5)).Return(true, false, nil).Once()
	}
	index := uint32(5)
	c.leaves.EXPECT().L1InfoTreeIndexForBridge(mock.Anything, mock.Anything).Return(&index, nil).Once()
}

func TestCheckOrigin(t *testing.T) {
	t.Run("final origin: nothing is asked", func(t *testing.T) {
		checkers := newReorgCheckers(t)
		tracking := trackingAt(types.BridgeTypeL2ToL1, types.StepPendingInclusion, false)
		info := *tracking.Info()
		info.BlockFinalized = true
		tracking = domain.NewTrackingData(tracking.ID(), domain.TrackingBridgeTx{Info: &info}, tracking.AllSteps())

		updated, reorged := domain.CheckOrigin(t.Context(), reorgLogger, checkers.ports(), tracking)
		require.False(t, reorged)
		require.Same(t, tracking, updated)
	})

	t.Run("still canonical, not final yet: unchanged", func(t *testing.T) {
		checkers := newReorgCheckers(t)
		tracking := trackingAt(types.BridgeTypeL2ToL1, types.StepPendingInclusion, false)
		checkers.origin.EXPECT().CanonicalBlockHash(mock.Anything, uint32(1), uint64(1000)).Return(reorgBlockHash, nil).Once()
		checkers.finality.EXPECT().IsFinal(mock.Anything, uint32(1), reorgTxHash, uint64(1000), (*bool)(nil)).
			Return(false, nil).Once()

		updated, reorged := domain.CheckOrigin(t.Context(), reorgLogger, checkers.ports(), tracking)
		require.False(t, reorged)
		require.Same(t, tracking, updated)
		require.False(t, updated.Info().BlockFinalized)
	})

	t.Run("still canonical and final: block_finalized set", func(t *testing.T) {
		checkers := newReorgCheckers(t)
		tracking := trackingAt(types.BridgeTypeL2ToL1, types.StepPendingInclusion, false)
		checkers.origin.EXPECT().CanonicalBlockHash(mock.Anything, uint32(1), uint64(1000)).Return(reorgBlockHash, nil).Once()
		checkers.finality.EXPECT().IsFinal(mock.Anything, uint32(1), reorgTxHash, uint64(1000), (*bool)(nil)).
			Return(true, nil).Once()

		updated, reorged := domain.CheckOrigin(t.Context(), reorgLogger, checkers.ports(), tracking)
		require.False(t, reorged)
		require.True(t, updated.Info().BlockFinalized)
		require.False(t, tracking.Info().BlockFinalized, "the previous snapshot is never modified in place")
		require.Equal(t, tracking.AllSteps(), updated.AllSteps())
	})

	t.Run("hash changed: reorged out", func(t *testing.T) {
		checkers := newReorgCheckers(t)
		tracking := trackingAt(types.BridgeTypeL2ToL1, types.StepPendingInclusion, false)
		checkers.origin.EXPECT().CanonicalBlockHash(mock.Anything, uint32(1), uint64(1000)).
			Return(common.HexToHash("0xcc"), nil).Once()

		updated, reorged := domain.CheckOrigin(t.Context(), reorgLogger, checkers.ports(), tracking)
		require.True(t, reorged)
		require.Same(t, tracking, updated)

		reset := domain.ResetBridgeTx(updated)
		require.Nil(t, reset.Info())
		require.Nil(t, reset.AllSteps())
		require.Equal(t, types.TrackingStatusRegistered, reset.TrackingStatus())
	})

	t.Run("transient errors keep the state", func(t *testing.T) {
		checkers := newReorgCheckers(t)
		tracking := trackingAt(types.BridgeTypeL2ToL1, types.StepPendingInclusion, false)
		checkers.origin.EXPECT().CanonicalBlockHash(mock.Anything, uint32(1), uint64(1000)).
			Return(common.Hash{}, errors.New("rpc down")).Once()

		updated, reorged := domain.CheckOrigin(t.Context(), reorgLogger, checkers.ports(), tracking)
		require.False(t, reorged)
		require.Same(t, tracking, updated)

		checkers.origin.EXPECT().CanonicalBlockHash(mock.Anything, uint32(1), uint64(1000)).Return(reorgBlockHash, nil).Once()
		checkers.finality.EXPECT().IsFinal(mock.Anything, uint32(1), reorgTxHash, uint64(1000), (*bool)(nil)).
			Return(false, errors.New("rpc down")).Once()
		updated, reorged = domain.CheckOrigin(t.Context(), reorgLogger, checkers.ports(), tracking)
		require.False(t, reorged)
		require.Same(t, tracking, updated)
	})
}

func TestReverifyStepsClaimGoneBackToReadyToClaim(t *testing.T) {
	for _, bridgeType := range allBridgeTypes {
		t.Run(bridgeType.String(), func(t *testing.T) {
			checkers := newReorgCheckers(t)
			tracking := claimedTracking(bridgeType)
			require.Equal(t, types.TrackerClaimStatusClaimed, tracking.ClaimStatus())
			checkers.claimed.EXPECT().IsClaimed(mock.Anything, tracking.Info()).Return(false, nil).Once()
			checkers.expectReadinessPresent(bridgeType)

			updated := domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow)
			requireRewoundTo(t, updated, types.StepWaitingClaim)
			require.Equal(t, types.TrackerClaimStatusReadyToClaim, updated.ClaimStatus())
			require.Equal(t, types.TrackingStatusRunning, updated.TrackingStatus())
		})
	}
}

func TestReverifyStepsClaimAndReadinessGoneBackToPending(t *testing.T) {
	for _, bridgeType := range allBridgeTypes {
		t.Run(bridgeType.String(), func(t *testing.T) {
			checkers := newReorgCheckers(t)
			tracking := claimedTracking(bridgeType)
			checkers.claimed.EXPECT().IsClaimed(mock.Anything, tracking.Info()).Return(false, nil).Once()
			if bridgeType == types.BridgeTypeL2ToL1 {
				checkers.leaves.EXPECT().L1InfoTreeIndexForBridge(mock.Anything, tracking.Info()).Return(nil, nil).Once()
			} else {
				checkers.injected.EXPECT().InjectedGERPresent(mock.Anything, tracking.Info(), uint32(5)).
					Return(false, false, nil).Once()
			}

			updated := domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow)
			requireRewoundTo(t, updated, readinessStep(bridgeType))
			require.Equal(t, types.TrackerClaimStatusPending, updated.ClaimStatus())
		})
	}
}

func TestReverifyStepsReadyToClaimInjectionOrLeafGone(t *testing.T) {
	for _, bridgeType := range allBridgeTypes {
		t.Run(bridgeType.String()+"/first readiness step gone", func(t *testing.T) {
			checkers := newReorgCheckers(t)
			tracking := trackingAt(bridgeType, types.StepWaitingClaim, false)
			require.Equal(t, types.TrackerClaimStatusReadyToClaim, tracking.ClaimStatus())
			if bridgeType == types.BridgeTypeL2ToL1 {
				checkers.leaves.EXPECT().L1InfoTreeIndexForBridge(mock.Anything, tracking.Info()).Return(nil, nil).Once()
			} else {
				checkers.injected.EXPECT().InjectedGERPresent(mock.Anything, tracking.Info(), uint32(5)).
					Return(false, false, nil).Once()
			}

			updated := domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow)
			requireRewoundTo(t, updated, readinessStep(bridgeType))
			require.Equal(t, types.TrackerClaimStatusPending, updated.ClaimStatus())
		})
	}

	for _, bridgeType := range []types.BridgeType{types.BridgeTypeL1ToL2, types.BridgeTypeL2ToL2} {
		t.Run(bridgeType.String()+"/injection still there, leaf gone", func(t *testing.T) {
			checkers := newReorgCheckers(t)
			tracking := trackingAt(bridgeType, types.StepWaitingClaim, false)
			checkers.injected.EXPECT().InjectedGERPresent(mock.Anything, tracking.Info(), uint32(5)).
				Return(true, false, nil).Once()
			checkers.leaves.EXPECT().L1InfoTreeIndexForBridge(mock.Anything, tracking.Info()).Return(nil, nil).Once()

			updated := domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow)
			requireRewoundTo(t, updated, types.StepWaitingL1InfoLeafAvailable)
		})
	}
}

func TestReverifyStepsReadyToClaimStillThere(t *testing.T) {
	for _, bridgeType := range allBridgeTypes {
		t.Run(bridgeType.String(), func(t *testing.T) {
			checkers := newReorgCheckers(t)
			tracking := trackingAt(bridgeType, types.StepWaitingClaim, false)
			checkers.expectReadinessPresent(bridgeType)

			updated := domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow)
			require.Same(t, tracking, updated)
		})
	}
}

// TestReverifyStepsFinalInjectionIsNotCheckedAgain pins the cost bound of readyToClaim: an
// injection reported final finalizes it and the L1 info leaf after it, and neither is asked again
func TestReverifyStepsFinalInjectionIsNotCheckedAgain(t *testing.T) {
	checkers := newReorgCheckers(t)
	tracking := trackingAt(types.BridgeTypeL1ToL2, types.StepWaitingClaim, false)
	checkers.injected.EXPECT().InjectedGERPresent(mock.Anything, tracking.Info(), uint32(5)).Return(true, true, nil).Once()

	updated := domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow)
	require.True(t, stepOf(t, updated, types.StepWaitingGERInjection).BlockFinalized)
	require.True(t, stepOf(t, updated, types.StepWaitingL1InfoLeafAvailable).BlockFinalized)
	require.Equal(t, types.StepWaitingClaim, currentStepOf(t, updated))

	// strict mocks: no further expectation, any check would fail the test
	again := domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), updated, reorgNow)
	require.Same(t, updated, again)
}

func TestReverifyStepsTransientErrorsKeepState(t *testing.T) {
	t.Run("isClaimed fails", func(t *testing.T) {
		checkers := newReorgCheckers(t)
		tracking := claimedTracking(types.BridgeTypeL1ToL2)
		checkers.claimed.EXPECT().IsClaimed(mock.Anything, mock.Anything).Return(false, errors.New("rpc down")).Once()

		require.Same(t, tracking, domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow))
	})

	t.Run("claim record answers 503 (reorg being resolved)", func(t *testing.T) {
		checkers := newReorgCheckers(t)
		tracking := claimedTracking(types.BridgeTypeL1ToL2)
		checkers.claimed.EXPECT().IsClaimed(mock.Anything, mock.Anything).Return(true, nil).Once()
		checkers.claims.EXPECT().ClaimFor(mock.Anything, mock.Anything).Return(nil, errReorgInProgress).Once()

		updated := domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow)
		require.Same(t, tracking, updated)
		require.Equal(t, types.TrackerClaimStatusClaimed, updated.ClaimStatus())
	})

	t.Run("claim record not indexed (anymore) while isClaimed is true", func(t *testing.T) {
		checkers := newReorgCheckers(t)
		tracking := claimedTracking(types.BridgeTypeL1ToL2)
		checkers.claimed.EXPECT().IsClaimed(mock.Anything, mock.Anything).Return(true, nil).Once()
		checkers.claims.EXPECT().ClaimFor(mock.Anything, mock.Anything).Return(nil, nil).Once()

		require.Same(t, tracking, domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow))
	})

	t.Run("injection check answers 503", func(t *testing.T) {
		checkers := newReorgCheckers(t)
		tracking := trackingAt(types.BridgeTypeL2ToL2, types.StepWaitingClaim, false)
		checkers.injected.EXPECT().InjectedGERPresent(mock.Anything, mock.Anything, uint32(5)).
			Return(false, false, errReorgInProgress).Once()

		require.Same(t, tracking, domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow))
	})

	t.Run("leaf check answers 503", func(t *testing.T) {
		checkers := newReorgCheckers(t)
		tracking := trackingAt(types.BridgeTypeL2ToL1, types.StepWaitingClaim, false)
		checkers.leaves.EXPECT().L1InfoTreeIndexForBridge(mock.Anything, mock.Anything).Return(nil, errReorgInProgress).Once()

		require.Same(t, tracking, domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow))
	})
}

func TestReverifyStepsClaimFinalized(t *testing.T) {
	for _, bridgeType := range allBridgeTypes {
		t.Run(bridgeType.String(), func(t *testing.T) {
			checkers := newReorgCheckers(t)
			tracking := claimedTracking(bridgeType)
			info := tracking.Info()
			reported := true
			checkers.claimed.EXPECT().IsClaimed(mock.Anything, info).Return(true, nil).Once()
			checkers.claims.EXPECT().ClaimFor(mock.Anything, info).Return(&types.ClaimResult{
				ClaimTx: reorgClaimTx, BlockNumber: 3000, BlockTimestamp: 1700003000, ReportedFinalized: &reported,
			}, nil).Once()
			checkers.finality.EXPECT().IsFinal(mock.Anything, info.DestinationNetwork, reorgClaimTx, uint64(3000), &reported).
				Return(true, nil).Once()

			updated := domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow)
			require.True(t, updated.IsFinal())
			require.Equal(t, types.TrackerClaimStatusClaimedFinalized, updated.ClaimStatus())
			require.Equal(t, types.TrackingStatusFinished, updated.TrackingStatus())
			require.True(t, updated.IsTerminal())
			require.True(t, updated.Info().BlockFinalized, "a final claim implies the origin bridge is final")

			// TestReverifyStepsFinalTrackingIsNeverReevaluated: strict mocks, no expectation left
			require.Same(t, updated, domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), updated, reorgNow))
			again, reorged := domain.CheckOrigin(t.Context(), reorgLogger, checkers.ports(), updated)
			require.False(t, reorged)
			require.Same(t, updated, again)
			require.Same(t, updated, domain.FinalizeClaim(t.Context(), reorgLogger, checkers.ports(), updated))
		})
	}
}

func TestReverifyStepsClaimNotFinalYet(t *testing.T) {
	checkers := newReorgCheckers(t)
	tracking := claimedTracking(types.BridgeTypeL2ToL1)
	notFinal := false
	checkers.claimed.EXPECT().IsClaimed(mock.Anything, mock.Anything).Return(true, nil).Once()
	checkers.claims.EXPECT().ClaimFor(mock.Anything, mock.Anything).Return(&types.ClaimResult{
		ClaimTx: reorgClaimTx, BlockNumber: 3000, BlockTimestamp: 1700003000, ReportedFinalized: &notFinal,
	}, nil).Once()
	checkers.finality.EXPECT().IsFinal(mock.Anything, uint32(0), reorgClaimTx, uint64(3000), &notFinal).
		Return(false, nil).Once()

	updated := domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow)
	require.Same(t, tracking, updated, "same claim, not final: nothing to write")
	require.Equal(t, types.TrackerClaimStatusClaimed, updated.ClaimStatus())
	require.False(t, updated.IsTerminal())
}

// TestReverifyStepsClaimReincludedInAnotherBlock pins that the claim tx re-included elsewhere
// replaces the stored claim (result and dates), still not final
func TestReverifyStepsClaimReincludedInAnotherBlock(t *testing.T) {
	checkers := newReorgCheckers(t)
	tracking := claimedTracking(types.BridgeTypeL1ToL2)
	moved := &types.ClaimResult{ClaimTx: reorgClaimTx, BlockNumber: 3005, BlockTimestamp: 1700003005}
	checkers.claimed.EXPECT().IsClaimed(mock.Anything, mock.Anything).Return(true, nil).Once()
	checkers.claims.EXPECT().ClaimFor(mock.Anything, mock.Anything).Return(moved, nil).Once()
	checkers.finality.EXPECT().IsFinal(mock.Anything, uint32(1), reorgClaimTx, uint64(3005), (*bool)(nil)).
		Return(false, nil).Once()

	updated := domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow)
	claimed := stepOf(t, updated, types.StepClaimed)
	require.Equal(t, types.StepStatusDone, claimed.Status)
	require.Equal(t, moved, claimed.Result())
	require.Equal(t, time.Unix(1700003005, 0).UTC(), *claimed.EndDate)
	require.Equal(t, types.TrackerClaimStatusClaimed, updated.ClaimStatus())
	require.Equal(t, uint64(3000), stepOf(t, tracking, types.StepClaimed).ResultClaim.BlockNumber,
		"the previous snapshot is never modified in place")
}

// TestReverifyStepsClaimOpenedButGone pins the claim fallback's half-way state: StepClaimed opened
// (isClaimed was true) but its record not fetched yet, and the claim reorged out meanwhile
func TestReverifyStepsClaimOpenedButGone(t *testing.T) {
	checkers := newReorgCheckers(t)
	tracking := trackingAt(types.BridgeTypeL2ToL1, types.StepClaimed, false)
	require.Equal(t, types.TrackerClaimStatusPending, tracking.ClaimStatus())
	checkers.claimed.EXPECT().IsClaimed(mock.Anything, mock.Anything).Return(false, nil).Once()
	checkers.expectReadinessPresent(types.BridgeTypeL2ToL1)

	updated := domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow)
	requireRewoundTo(t, updated, types.StepWaitingClaim)

	// still claimed: the forward resolution fetches the record, nothing else is asked here
	checkers.claimed.EXPECT().IsClaimed(mock.Anything, mock.Anything).Return(true, nil).Once()
	require.Same(t, tracking, domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow))
}

// TestReverifyStepsClaimGoneReopensSkippedSteps pins a claim found through the claimed-bridge
// fallback (the readiness steps Skipped, StepClaimed opened) and then reorged out: the skipped
// steps were only inferred from the claim, so the tracking goes back to the first of them instead
// of staying readyToClaim with nothing verified
func TestReverifyStepsClaimGoneReopensSkippedSteps(t *testing.T) {
	for _, bridgeType := range allBridgeTypes {
		t.Run(bridgeType.String(), func(t *testing.T) {
			checkers := newReorgCheckers(t)
			base := trackingAt(bridgeType, types.StepClaimed, false)
			steps := append([]domain.BridgeStepPath(nil), base.AllSteps()...)
			skipping := false
			for i, step := range steps {
				if step.Step == readinessStep(bridgeType) {
					skipping = true
				}
				if step.Step == types.StepClaimed {
					break
				}
				if skipping {
					steps[i] = domain.BridgeStepPath{Step: step.Step, Status: types.StepStatusSkipped}
				}
			}
			tracking := domain.NewTrackingData(base.ID(), base.BridgeTx(), steps)
			checkers.claimed.EXPECT().IsClaimed(mock.Anything, tracking.Info()).Return(false, nil).Once()

			// no readiness check is asked: the skipped steps are resolved again by the forward path
			updated := domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow)
			requireRewoundTo(t, updated, readinessStep(bridgeType))
			require.Equal(t, types.TrackerClaimStatusPending, updated.ClaimStatus())
		})
	}
}

func TestReverifyStepsBeforeReadyToClaimChecksNothing(t *testing.T) {
	checkers := newReorgCheckers(t)
	tracking := trackingAt(types.BridgeTypeL2ToL2, types.StepWaitingL1InfoLeafAvailable, false)
	require.Same(t, tracking, domain.ReverifySteps(t.Context(), reorgLogger, checkers.ports(), tracking, reorgNow))
}

// TestFinalizeClaimUsesTheReportedValue pins the tick the claim is first found: the block_finalized
// the bridge service sent with it is used right away, without fetching the claim again
func TestFinalizeClaimUsesTheReportedValue(t *testing.T) {
	checkers := newReorgCheckers(t)
	tracking := claimedTracking(types.BridgeTypeL1ToL2)
	reported := true
	steps := append([]domain.BridgeStepPath(nil), tracking.AllSteps()...)
	steps[len(steps)-1].SetResult(&types.ClaimResult{
		ClaimTx: reorgClaimTx, BlockNumber: 3000, ReportedFinalized: &reported,
	})
	tracking = domain.NewTrackingData(tracking.ID(), tracking.BridgeTx(), steps)
	checkers.finality.EXPECT().IsFinal(mock.Anything, uint32(1), reorgClaimTx, uint64(3000), &reported).
		Return(true, nil).Once()

	updated := domain.FinalizeClaim(t.Context(), reorgLogger, checkers.ports(), tracking)
	require.Equal(t, types.TrackerClaimStatusClaimedFinalized, updated.ClaimStatus())
	require.True(t, updated.Info().BlockFinalized)
}

func TestStepsForTx(t *testing.T) {
	steps := []domain.BridgeStepPath{{Step: types.StepClaimed}}
	require.Nil(t, domain.StepsForTx(domain.TrackingBridgeTx{}, steps))
	require.Equal(t, steps, domain.StepsForTx(domain.TrackingBridgeTx{Info: &domain.BridgeInfo{}}, steps))
}
