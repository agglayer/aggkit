package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// fakeFacts is a canned BridgeFacts that records which facts were queried, to assert
// ResolveSteps stops at the first unmet milestone and skips facts for already-Done steps
type fakeFacts struct {
	originGER   *types.GERData
	originLER   *types.LERUpdateResult
	certificate *types.CertificateData
	injectedGER *types.GERData
	claim       *types.ClaimResult

	originGERErr   error
	originLERErr   error
	certificateErr error
	injectedGERErr error
	claimErr       error

	queried []string
}

func (f *fakeFacts) OriginGER(_ context.Context, _ *BridgeInfo) (*types.GERData, error) {
	f.queried = append(f.queried, "originGER")
	return f.originGER, f.originGERErr
}

func (f *fakeFacts) OriginLER(_ context.Context, _ *BridgeInfo) (*types.LERUpdateResult, error) {
	f.queried = append(f.queried, "originLER")
	return f.originLER, f.originLERErr
}

func (f *fakeFacts) Certificate(_ context.Context, _ *BridgeInfo) (*types.CertificateData, error) {
	f.queried = append(f.queried, "certificate")
	return f.certificate, f.certificateErr
}

func (f *fakeFacts) InjectedGER(_ context.Context, _ *BridgeInfo) (*types.GERData, error) {
	f.queried = append(f.queried, "injectedGER")
	return f.injectedGER, f.injectedGERErr
}

func (f *fakeFacts) ClaimFor(_ context.Context, _ *BridgeInfo) (*types.ClaimResult, error) {
	f.queried = append(f.queried, "claimFor")
	return f.claim, f.claimErr
}

var resolveStepsTestID = TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x01")}

// newTracking seeds a fresh (PendingPath) or given path for bridgeType, wrapped as TrackingData
func newTracking(bridgeType types.BridgeType, prevSteps []types.BridgeStepPath, now time.Time) *TrackingData {
	steps := prevSteps
	if steps == nil {
		steps = PendingPath(bridgeType, now)
	}
	return NewTrackingData(resolveStepsTestID, TrackingBridgeTx{}, steps)
}

func TestResolveSteps(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

	ger := common.Hash{1}
	blockNumber := uint64(100)
	originGER := &types.GERData{NetworkID: 0, GER: &ger, BlockNumber: &blockNumber}
	originLER := &types.LERUpdateResult{NetworkID: 1, LER: common.Hash{2}, BlockNumber: 200}
	injectedGER := &types.GERData{NetworkID: 2, GER: &common.Hash{1}}
	settledCert := &types.CertificateData{Status: agglayertypes.Settled}
	claim := &types.ClaimResult{ClaimTx: common.Hash{3}, BlockNumber: 300}

	testCases := []struct {
		name            string
		bridgeType      types.BridgeType
		facts           fakeFacts
		prevSteps       []types.BridgeStepPath
		expectedStep    types.BridgeStep
		expectedQueried []string
		// resultOf, if set, is checked against the Result of the named step in the outcome
		resultOf types.BridgeStep
		result   any
	}{
		{
			name:            "L1 origin, no origin GER -> WaitingGERUpdate, later facts not queried",
			bridgeType:      types.BridgeTypeL1ToL2,
			facts:           fakeFacts{},
			expectedStep:    types.StepWaitingGERUpdate,
			expectedQueried: []string{"originGER"},
		},
		{
			name:            "L1->L2 never queries certificate or LER",
			bridgeType:      types.BridgeTypeL1ToL2,
			facts:           fakeFacts{originGER: originGER},
			expectedStep:    types.StepWaitingGERInjection,
			expectedQueried: []string{"originGER", "injectedGER"},
			resultOf:        types.StepWaitingGERUpdate,
			result:          &types.GERUpdateResult{GER: ger, BlockNumber: blockNumber},
		},
		{
			name:            "L2 origin, no origin LER -> WaitingLERUpdate, later facts not queried",
			bridgeType:      types.BridgeTypeL2ToL1,
			facts:           fakeFacts{},
			expectedStep:    types.StepWaitingLERUpdate,
			expectedQueried: []string{"originLER"},
		},
		{
			name:            "L2 origin without certificate -> PendingInclusion",
			bridgeType:      types.BridgeTypeL2ToL1,
			facts:           fakeFacts{originLER: originLER},
			expectedStep:    types.StepPendingInclusion,
			expectedQueried: []string{"originLER", "certificate"},
			resultOf:        types.StepWaitingLERUpdate,
			result:          originLER,
		},
		{
			// a fresh path completes PendingInclusion and lands on CertificatePending in the
			// same call, so certificate is queried twice: PendingInclusionResolver and
			// CertificatePendingResolver each fetch it independently, nothing is cached
			// between them
			name:       "certificate pending -> CertificatePending, cert shown while waiting",
			bridgeType: types.BridgeTypeL2ToL1,
			facts: fakeFacts{
				originLER:   originLER,
				certificate: &types.CertificateData{Status: agglayertypes.Pending},
			},
			expectedStep:    types.StepCertificatePending,
			expectedQueried: []string{"originLER", "certificate", "certificate"},
			resultOf:        types.StepCertificatePending,
			result:          &types.CertificateData{Status: agglayertypes.Pending},
		},
		{
			name:       "certificate proven -> stays at CertificatePending, cert shown while waiting",
			bridgeType: types.BridgeTypeL2ToL1,
			facts: fakeFacts{
				originLER:   originLER,
				certificate: &types.CertificateData{Status: agglayertypes.Proven},
			},
			expectedStep:    types.StepCertificatePending,
			expectedQueried: []string{"originLER", "certificate", "certificate"},
			resultOf:        types.StepCertificatePending,
			result:          &types.CertificateData{Status: agglayertypes.Proven},
		},
		{
			name:       "certificate in error -> stays at CertificatePending, cert shown while waiting",
			bridgeType: types.BridgeTypeL2ToL1,
			facts: fakeFacts{
				originLER:   originLER,
				certificate: &types.CertificateData{Status: agglayertypes.InError},
			},
			expectedStep:    types.StepCertificatePending,
			expectedQueried: []string{"originLER", "certificate", "certificate"},
			resultOf:        types.StepCertificatePending,
			result:          &types.CertificateData{Status: agglayertypes.InError},
		},
		{
			name:            "L2->L1 settled skips injection and is claimable",
			bridgeType:      types.BridgeTypeL2ToL1,
			facts:           fakeFacts{originLER: originLER, certificate: settledCert},
			expectedStep:    types.StepWaitingClaim,
			expectedQueried: []string{"originLER", "certificate", "certificate", "claimFor"},
			resultOf:        types.StepCertificatePending,
			result:          settledCert,
		},
		{
			name:            "L2->L2 without injected GER -> WaitingGERInjection",
			bridgeType:      types.BridgeTypeL2ToL2,
			facts:           fakeFacts{originLER: originLER, certificate: settledCert},
			expectedStep:    types.StepWaitingGERInjection,
			expectedQueried: []string{"originLER", "certificate", "certificate", "injectedGER"},
		},
		{
			name:       "L2->L2 claimed -> Claimed, terminal, with Claim result",
			bridgeType: types.BridgeTypeL2ToL2,
			facts: fakeFacts{
				originLER:   originLER,
				certificate: settledCert,
				injectedGER: injectedGER,
				claim:       claim,
			},
			expectedStep:    types.StepClaimed,
			expectedQueried: []string{"originLER", "certificate", "certificate", "injectedGER", "claimFor"},
			resultOf:        types.StepWaitingClaim,
			result:          claim,
		},
		{
			name:            "L1->L2 claimed",
			bridgeType:      types.BridgeTypeL1ToL2,
			facts:           fakeFacts{originGER: originGER, injectedGER: injectedGER, claim: claim},
			expectedStep:    types.StepClaimed,
			expectedQueried: []string{"originGER", "injectedGER", "claimFor"},
		},
		{
			name:       "L1->L2 with GER update already done skips OriginGER",
			bridgeType: types.BridgeTypeL1ToL2,
			facts:      fakeFacts{},
			prevSteps: []types.BridgeStepPath{
				{Step: types.StepWaitingGERUpdate, Status: types.StepStatusDone},
				{Step: types.StepWaitingGERInjection, Status: types.StepStatusInProgress},
				{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
				{Step: types.StepClaimed, Status: types.StepStatusPending},
			},
			expectedStep:    types.StepWaitingGERInjection,
			expectedQueried: []string{"injectedGER"},
		},
		{
			name:       "L2->L2 with every milestone done but the claim only queries the claim",
			bridgeType: types.BridgeTypeL2ToL2,
			facts:      fakeFacts{claim: claim},
			prevSteps: []types.BridgeStepPath{
				{Step: types.StepWaitingLERUpdate, Status: types.StepStatusDone},
				{Step: types.StepPendingInclusion, Status: types.StepStatusDone},
				{Step: types.StepCertificatePending, Status: types.StepStatusDone},
				{Step: types.StepWaitingGERInjection, Status: types.StepStatusDone},
				{Step: types.StepWaitingClaim, Status: types.StepStatusInProgress},
				{Step: types.StepClaimed, Status: types.StepStatusPending},
			},
			expectedStep:    types.StepClaimed,
			expectedQueried: []string{"claimFor"},
			resultOf:        types.StepWaitingClaim,
			result:          claim,
		},
		{
			name:       "an in-progress step is still queried",
			bridgeType: types.BridgeTypeL2ToL1,
			facts:      fakeFacts{},
			prevSteps: []types.BridgeStepPath{
				{Step: types.StepWaitingLERUpdate, Status: types.StepStatusInProgress},
				{Step: types.StepPendingInclusion, Status: types.StepStatusPending},
				{Step: types.StepCertificatePending, Status: types.StepStatusPending},
				{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
				{Step: types.StepClaimed, Status: types.StepStatusPending},
			},
			expectedStep:    types.StepWaitingLERUpdate,
			expectedQueried: []string{"originLER"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tracking := newTracking(tc.bridgeType, tc.prevSteps, now)

			result, err := ResolveSteps(context.Background(), &tc.facts, tracking, now)
			require.NoError(t, err)
			require.Equal(t, tc.expectedQueried, tc.facts.queried)

			idx := result.StepIndex()
			require.NotNil(t, idx)
			require.Equal(t, tc.expectedStep, result.AllSteps()[*idx].Step)

			if tc.resultOf != 0 || tc.result != nil {
				sp := result.AllSteps()[indexOfStep(result.AllSteps(), tc.resultOf)]
				require.Equal(t, tc.result, sp.Result)
			}

			if tc.facts.certificate != nil {
				if pIdx := indexOfStep(result.AllSteps(), types.StepPendingInclusion); pIdx >= 0 {
					if pi := result.AllSteps()[pIdx]; pi.Status == types.StepStatusDone {
						require.Equal(t, tc.facts.certificate.CertificateID, pi.Result,
							"PendingInclusion carries the certificate ID once met")
					}
				}
			}
		})
	}
}

func TestResolveStepsErrors(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	originLER := &types.LERUpdateResult{NetworkID: 1, LER: common.Hash{2}, BlockNumber: 200}
	settledCert := &types.CertificateData{Status: agglayertypes.Settled}
	factsErr := errors.New("source down")

	testCases := []struct {
		name        string
		bridgeType  types.BridgeType
		facts       fakeFacts
		expectedErr string
	}{
		{
			name:        "origin GER error",
			bridgeType:  types.BridgeTypeL1ToL2,
			facts:       fakeFacts{originGERErr: factsErr},
			expectedErr: "origin GER",
		},
		{
			name:        "origin LER error",
			bridgeType:  types.BridgeTypeL2ToL1,
			facts:       fakeFacts{originLERErr: factsErr},
			expectedErr: "origin LER",
		},
		{
			name:        "certificate error",
			bridgeType:  types.BridgeTypeL2ToL1,
			facts:       fakeFacts{originLER: originLER, certificateErr: factsErr},
			expectedErr: "certificate",
		},
		{
			name:       "injected GER error",
			bridgeType: types.BridgeTypeL1ToL2,
			facts: fakeFacts{
				originGER:      &types.GERData{GER: &common.Hash{1}, BlockNumber: new(uint64)},
				injectedGERErr: factsErr,
			},
			expectedErr: "injected GER",
		},
		{
			name:       "claim status error",
			bridgeType: types.BridgeTypeL2ToL1,
			facts: fakeFacts{
				originLER:   originLER,
				certificate: settledCert,
				claimErr:    factsErr,
			},
			expectedErr: "claim status",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tracking := newTracking(tc.bridgeType, nil, now)

			result, err := ResolveSteps(context.Background(), &tc.facts, tracking, now)
			require.ErrorIs(t, err, factsErr)
			require.ErrorContains(t, err, tc.expectedErr)

			// the error is attributed to the step that was current before this call, even if a
			// cascade (e.g. "certificate error": LER resolves before Certificate fails) reached
			// further this same tick — that progress is discarded, not just left unpersisted
			idx := tracking.StepIndex()
			require.NotNil(t, idx)
			errStep := result.AllSteps()[*idx]
			require.Equal(t, types.StepStatusError, errStep.Status)
			require.NotNil(t, errStep.Error)
			require.Equal(t, types.StepErrorTransient, errStep.Error.ErrorType)
			require.Equal(t, 1, errStep.Error.RetryCount)
			require.Contains(t, errStep.Error.Description[0], tc.expectedErr)

			for i, sp := range result.AllSteps() {
				if i == *idx {
					continue
				}
				require.Equal(t, tracking.AllSteps()[i], sp, "no partial cascade may survive on a later step's error")
			}
		})
	}
}

func TestUpdateStep(t *testing.T) {
	t.Parallel()

	t1 := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)

	t.Run("no progress and no error returns the same snapshot", func(t *testing.T) {
		t.Parallel()

		tracking := newTracking(types.BridgeTypeL1ToL2, nil, t1)
		result := UpdateStep(tracking, 0, nil, false, t2)

		require.Same(t, tracking, result)
	})

	t.Run("completing a step closes it and opens the next one", func(t *testing.T) {
		t.Parallel()

		tracking := newTracking(types.BridgeTypeL1ToL2, nil, t1)
		gerUpdate := &types.GERUpdateResult{GER: common.Hash{1}, BlockNumber: 100}
		advanced := UpdateStep(tracking, 0, gerUpdate, true, t2)

		steps := advanced.AllSteps()
		require.Equal(t, []types.BridgeStepPath{
			{Step: types.StepWaitingGERUpdate, Status: types.StepStatusDone, StartDate: &t1, EndDate: &t2, Result: gerUpdate},
			{Step: types.StepWaitingGERInjection, Status: types.StepStatusInProgress, StartDate: &t2},
			{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
			{Step: types.StepClaimed, Status: types.StepStatusPending},
		}, steps)
	})

	t.Run("terminal step completes the moment it is reached", func(t *testing.T) {
		t.Parallel()

		tracking := newTracking(types.BridgeTypeL1ToL2, []types.BridgeStepPath{
			{Step: types.StepWaitingGERUpdate, Status: types.StepStatusDone, EndDate: &t1},
			{Step: types.StepWaitingGERInjection, Status: types.StepStatusDone, EndDate: &t1},
			{Step: types.StepWaitingClaim, Status: types.StepStatusInProgress, StartDate: &t1},
			{Step: types.StepClaimed, Status: types.StepStatusPending},
		}, t1)

		claim := &types.ClaimResult{ClaimTx: common.Hash{2}, BlockNumber: 200}
		advanced := UpdateStep(tracking, 2, claim, true, t2)

		last := advanced.AllSteps()[len(advanced.AllSteps())-1]
		require.Equal(t, types.StepClaimed, last.Step)
		require.Equal(t, types.StepStatusDone, last.Status)
		require.Equal(t, &t2, last.StartDate)
		require.Equal(t, &t2, last.EndDate)
	})

	t.Run("a successful check clears a previous transient error even without progress", func(t *testing.T) {
		t.Parallel()

		tracking := newTracking(types.BridgeTypeL1ToL2, []types.BridgeStepPath{
			{
				Step: types.StepWaitingGERUpdate, Status: types.StepStatusError, StartDate: &t1,
				Error: &types.ErrorStep{ErrorType: types.StepErrorTransient, RetryCount: 2},
			},
			{Step: types.StepWaitingGERInjection, Status: types.StepStatusPending},
			{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
			{Step: types.StepClaimed, Status: types.StepStatusPending},
		}, t1)

		advanced := UpdateStep(tracking, 0, nil, false, t2)

		require.NotSame(t, tracking, advanced, "the error clearing must be recorded")
		require.Nil(t, advanced.AllSteps()[0].Error)
		require.Equal(t, types.StepStatusInProgress, advanced.AllSteps()[0].Status)
		require.Equal(t, &t1, advanced.AllSteps()[0].StartDate, "the step's original start time is preserved")
	})

	t.Run("attaches a result even though the step is not complete yet", func(t *testing.T) {
		t.Parallel()

		tracking := newTracking(types.BridgeTypeL2ToL1, []types.BridgeStepPath{
			{Step: types.StepWaitingLERUpdate, Status: types.StepStatusDone, EndDate: &t1},
			{Step: types.StepPendingInclusion, Status: types.StepStatusDone, EndDate: &t1},
			{Step: types.StepCertificatePending, Status: types.StepStatusInProgress, StartDate: &t1},
			{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
			{Step: types.StepClaimed, Status: types.StepStatusPending},
		}, t1)

		cert := &types.CertificateData{Status: agglayertypes.Pending}
		advanced := UpdateStep(tracking, 2, cert, false, t2)

		require.NotSame(t, tracking, advanced)
		sp := advanced.AllSteps()[2]
		require.Equal(t, types.StepStatusInProgress, sp.Status, "still waiting, not complete")
		require.Equal(t, cert, sp.Result, "the current (unsettled) certificate is visible while waiting")
		require.Equal(t, &t1, sp.StartDate, "unaffected by the result update")
	})

	t.Run("no progress once the same result has already been recorded", func(t *testing.T) {
		t.Parallel()

		tracking := newTracking(types.BridgeTypeL2ToL1, []types.BridgeStepPath{
			{Step: types.StepWaitingLERUpdate, Status: types.StepStatusDone, EndDate: &t1},
			{Step: types.StepPendingInclusion, Status: types.StepStatusDone, EndDate: &t1},
			{
				Step: types.StepCertificatePending, Status: types.StepStatusInProgress, StartDate: &t1,
				Result: &types.CertificateData{Status: agglayertypes.Pending},
			},
			{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
			{Step: types.StepClaimed, Status: types.StepStatusPending},
		}, t1)

		result := UpdateStep(tracking, 2, &types.CertificateData{Status: agglayertypes.Pending}, false, t2)

		require.Same(t, tracking, result, "an equal result is not a change worth republishing")
	})
}

// TestCertificateResolverSkipsWaypoints pins that a certificate observed already Settled, with
// PendingInclusion still current, completes both PendingInclusion (a plain waypoint, dated, no
// result) and CertificatePending itself (dated, carrying the settled certificate) in the same
// call — querying Certificate twice, once per step, since PendingInclusionResolver and
// CertificatePendingResolver each fetch it independently
func TestCertificateResolverSkipsWaypoints(t *testing.T) {
	t.Parallel()

	t1 := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)

	tracking := newTracking(types.BridgeTypeL2ToL1, []types.BridgeStepPath{
		{Step: types.StepWaitingLERUpdate, Status: types.StepStatusDone, EndDate: &t1},
		{Step: types.StepPendingInclusion, Status: types.StepStatusInProgress, StartDate: &t1},
		{Step: types.StepCertificatePending, Status: types.StepStatusPending},
		{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
		{Step: types.StepClaimed, Status: types.StepStatusPending},
	}, t1)

	cert := &types.CertificateData{CertificateID: common.Hash{9}, Status: agglayertypes.Settled}
	facts := &fakeFacts{certificate: cert}

	result, err := ResolveSteps(context.Background(), facts, tracking, t2)
	require.NoError(t, err)
	require.Equal(t, []string{"certificate", "certificate", "claimFor"}, facts.queried)

	steps := result.AllSteps()
	require.Equal(t, types.StepStatusDone, steps[1].Status, "PendingInclusion skipped straight through")
	require.Equal(t, &t2, steps[1].EndDate)
	require.Equal(t, cert.CertificateID, steps[1].Result)
	require.Equal(t, types.StepStatusDone, steps[2].Status)
	require.Equal(t, &t2, steps[2].EndDate)
	require.Equal(t, cert, steps[2].Result)
	require.Equal(t, types.StepStatusInProgress, steps[3].Status, "WaitingClaim opens next")
}
