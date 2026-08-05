package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// fakeFacts is a canned BridgeFacts that records which facts were queried, to assert
// ResolveSteps stops at the first unmet milestone and skips facts for already-Done steps
type fakeFacts struct {
	originGER          *types.GERData
	originLER          *types.LERUpdateResult
	certificate        *types.CertificateInclusionData
	injectedGER        *types.GERData
	injectedGERAtIndex *types.GERData
	l1InfoTreeIndex    *uint32
	claim              *types.ClaimResult
	settlement         *types.L1SettledGERResult

	originGERErr          error
	originLERErr          error
	certificateErr        error
	injectedGERErr        error
	injectedGERAtIndexErr error
	l1InfoTreeIndexErr    error
	claimErr              error
	settlementErr         error

	queried []string
}

// originGERLeafCount is the canned leaf count fakeFacts.FindFirstL1InfoTreeAfterBlock resolves
// every originGER fixture to, giving GERUpdateResult.L1InfoTreeIndex a fixed, non-zero value
// (leafCount - 1) that WaitingGERInjectionResolver's previous-step lookup can read back
const originGERLeafCount = 6

// FindFirstL1InfoTreeAfterBlock implements WaitingGERUpdateSource, translating the canned
// originGER fixture (kept shaped like BridgeFacts.OriginGER used to be, for minimal test churn)
// into the port's own result type
func (f *fakeFacts) FindFirstL1InfoTreeAfterBlock(
	_ context.Context, _ uint64, _ uint32,
) (*ResultFindFirstL1InfoTreeAfterBlock, error) {
	f.queried = append(f.queried, "originGER")
	if f.originGERErr != nil || f.originGER == nil {
		return nil, f.originGERErr
	}
	return &ResultFindFirstL1InfoTreeAfterBlock{
		LeafCount:   originGERLeafCount,
		GER:         *f.originGER.GER,
		BlockNumber: *f.originGER.BlockNumber,
	}, nil
}

func (f *fakeFacts) OriginLER(_ context.Context, _ *BridgeInfo) (*types.LERUpdateResult, error) {
	f.queried = append(f.queried, "originLER")
	return f.originLER, f.originLERErr
}

func (f *fakeFacts) CertificateFor(_ context.Context, _ *BridgeInfo) (*types.CertificateInclusionData, error) {
	f.queried = append(f.queried, "certificate")
	return f.certificate, f.certificateErr
}

func (f *fakeFacts) InjectedGER(_ context.Context, _ *BridgeInfo) (*types.GERData, error) {
	f.queried = append(f.queried, "injectedGER")
	return f.injectedGER, f.injectedGERErr
}

func (f *fakeFacts) InjectedGERAtIndex(
	_ context.Context, _ *BridgeInfo, _ uint32,
) (*types.GERData, error) {
	f.queried = append(f.queried, "injectedGERAtIndex")
	return f.injectedGERAtIndex, f.injectedGERAtIndexErr
}

func (f *fakeFacts) L1InfoTreeIndexForGER(
	_ context.Context, _ *BridgeInfo, _ common.Hash,
) (*uint32, error) {
	f.queried = append(f.queried, "l1InfoTreeIndexForGER")
	return f.l1InfoTreeIndex, f.l1InfoTreeIndexErr
}

func (f *fakeFacts) ClaimFor(_ context.Context, _ *BridgeInfo) (*types.ClaimResult, error) {
	f.queried = append(f.queried, "claimFor")
	return f.claim, f.claimErr
}

func (f *fakeFacts) SettlementGERUpdate(
	_ context.Context, _ *BridgeInfo, _ common.Hash,
) (*types.L1SettledGERResult, error) {
	f.queried = append(f.queried, "settlementGERUpdate")
	return f.settlement, f.settlementErr
}

var resolveStepsTestID = TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x01")}

var errFakeUpdateStep = errors.New("fake update step error")

// testResolvers wires every step to a resolver reading through f, mirroring
// bridgetracker.createResolvers but backed by the single canned fakeFacts double
func testResolvers(f *fakeFacts) map[types.BridgeStep]StepResolver {
	return map[types.BridgeStep]StepResolver{
		types.StepWaitingGERUpdate:    NewWaitingGERUpdateResolver(log.NewLoggerNil(), f),
		types.StepWaitingLERUpdate:    NewWaitingLERUpdateResolver(f),
		types.StepPendingInclusion:    NewPendingInclusionResolver(f),
		types.StepCertificatePending:  NewCertificatePendingResolver(f),
		types.StepWaitL1SettledGER:    NewWaitL1SettledGERResolver(f, f),
		types.StepWaitingGERInjection: NewWaitingGERInjectionResolver(f),
		types.StepWaitingClaim:        NewWaitingClaimResolver(f),
	}
}

// newTracking seeds a fresh (PendingPath) or given path for bridgeType, wrapped as TrackingData.
// Info is set to a zero BridgeInfo (not nil): ResolveSteps only ever runs on a resolved bridge
// (BridgeTx().IsDone(), see its doc), and WaitingGERUpdateResolver reads tracking.Info() directly
func newTracking(bridgeType types.BridgeType, prevSteps []BridgeStepPath, now time.Time) *TrackingData {
	steps := prevSteps
	if steps == nil {
		steps = PendingPath(bridgeType, now)
	}
	return NewTrackingData(resolveStepsTestID, TrackingBridgeTx{Info: &BridgeInfo{}}, steps)
}

func TestResolveSteps(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

	ger := common.Hash{1}
	blockNumber := uint64(100)
	originGER := &types.GERData{NetworkID: 0, GER: &ger, BlockNumber: &blockNumber}
	originGERLeafIndex := uint32(originGERLeafCount - 1)
	originLER := &types.LERUpdateResult{NetworkID: 1, LER: common.Hash{2}, BlockNumber: 200}
	injectedGER := &types.GERData{NetworkID: 2, GER: &common.Hash{1}}
	settlementTxHash := common.Hash{4}
	settledCertData := types.CertificateData{Status: agglayertypes.Settled, SettlementTxHash: &settlementTxHash}
	settledCert := &types.CertificateInclusionData{CertificateData: settledCertData}
	settlementLeafIndex := uint32(7)
	settlementResult := &types.L1SettledGERResult{
		TxHash: settlementTxHash, BlockNumber: 400, L1InfoTreeIndex: &settlementLeafIndex,
		HasVerifyBatchesTrustedAggregator: true, HasUpdateL1InfoTree: true,
	}
	claim := &types.ClaimResult{ClaimTx: common.Hash{3}, BlockNumber: 300}

	testCases := []struct {
		name            string
		bridgeType      types.BridgeType
		facts           fakeFacts
		prevSteps       []BridgeStepPath
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
			expectedQueried: []string{"originGER", "injectedGERAtIndex"},
			resultOf:        types.StepWaitingGERUpdate,
			result:          &types.GERUpdateResult{L1InfoTreeIndex: originGERLeafIndex, GER: ger, BlockNumber: blockNumber},
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
				originLER: originLER,
				certificate: &types.CertificateInclusionData{
					CertificateData: types.CertificateData{Status: agglayertypes.Pending},
				},
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
				originLER: originLER,
				certificate: &types.CertificateInclusionData{
					CertificateData: types.CertificateData{Status: agglayertypes.Proven},
				},
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
				originLER: originLER,
				certificate: &types.CertificateInclusionData{
					CertificateData: types.CertificateData{Status: agglayertypes.InError},
				},
			},
			expectedStep:    types.StepCertificatePending,
			expectedQueried: []string{"originLER", "certificate", "certificate"},
			resultOf:        types.StepCertificatePending,
			result:          &types.CertificateData{Status: agglayertypes.InError},
		},
		{
			name:         "L2->L1 settled but settlement tx not confirmed on L1 yet -> WaitL1SettledGER",
			bridgeType:   types.BridgeTypeL2ToL1,
			facts:        fakeFacts{originLER: originLER, certificate: settledCert},
			expectedStep: types.StepWaitL1SettledGER,
			expectedQueried: []string{
				"originLER", "certificate", "certificate", "settlementGERUpdate",
			},
			resultOf: types.StepCertificatePending,
			result:   &settledCertData,
		},
		{
			name:       "L2->L1 settlement confirmed on L1 skips injection and is claimable",
			bridgeType: types.BridgeTypeL2ToL1,
			facts: fakeFacts{
				originLER: originLER, certificate: settledCert, settlement: settlementResult,
			},
			expectedStep: types.StepWaitingClaim,
			expectedQueried: []string{
				"originLER", "certificate", "certificate", "settlementGERUpdate", "claimFor",
			},
			resultOf: types.StepWaitL1SettledGER,
			result:   settlementResult,
		},
		{
			name:       "L2->L2 settlement confirmed, without injected GER -> WaitingGERInjection",
			bridgeType: types.BridgeTypeL2ToL2,
			facts: fakeFacts{
				originLER: originLER, certificate: settledCert, settlement: settlementResult,
			},
			expectedStep: types.StepWaitingGERInjection,
			expectedQueried: []string{
				"originLER", "certificate", "certificate", "settlementGERUpdate", "injectedGERAtIndex",
			},
		},
		{
			name:       "L2->L2 claimed -> Claimed, terminal, with Claim result",
			bridgeType: types.BridgeTypeL2ToL2,
			facts: fakeFacts{
				originLER:          originLER,
				certificate:        settledCert,
				settlement:         settlementResult,
				injectedGERAtIndex: injectedGER,
				claim:              claim,
			},
			expectedStep: types.StepClaimed,
			expectedQueried: []string{
				"originLER", "certificate", "certificate", "settlementGERUpdate",
				"injectedGERAtIndex", "claimFor",
			},
			resultOf: types.StepWaitingClaim,
			result:   claim,
		},
		{
			// UpdateL1InfoTreeV2 did not fire: the settlement evidence is confirmed but its GER
			// has not been resolved to a leaf index yet, so the step stays put
			name:       "L2->L2 settlement confirmed via V1 only, leaf index not resolved yet",
			bridgeType: types.BridgeTypeL2ToL2,
			facts: fakeFacts{
				originLER:   originLER,
				certificate: settledCert,
				settlement: &types.L1SettledGERResult{
					TxHash: settlementTxHash, BlockNumber: 400,
					HasVerifyBatchesTrustedAggregator: true, HasUpdateL1InfoTree: true,
				},
			},
			expectedStep: types.StepWaitL1SettledGER,
			expectedQueried: []string{
				"originLER", "certificate", "certificate", "settlementGERUpdate", "l1InfoTreeIndexForGER",
			},
			resultOf: types.StepWaitL1SettledGER,
			result: &types.L1SettledGERResult{
				TxHash: settlementTxHash, BlockNumber: 400,
				HasVerifyBatchesTrustedAggregator: true, HasUpdateL1InfoTree: true,
			},
		},
		{
			// UpdateL1InfoTreeV2 did not fire, but the extra GER -> leaf lookup just resolved:
			// the step completes with the leaf index filled in, and moves the bridge on
			name:       "L2->L2 settlement confirmed via V1 only, leaf index resolved -> WaitingGERInjection",
			bridgeType: types.BridgeTypeL2ToL2,
			facts: fakeFacts{
				originLER:   originLER,
				certificate: settledCert,
				settlement: &types.L1SettledGERResult{
					TxHash: settlementTxHash, BlockNumber: 400,
					HasVerifyBatchesTrustedAggregator: true, HasUpdateL1InfoTree: true,
				},
				l1InfoTreeIndex: &settlementLeafIndex,
			},
			expectedStep: types.StepWaitingGERInjection,
			expectedQueried: []string{
				"originLER", "certificate", "certificate", "settlementGERUpdate",
				"l1InfoTreeIndexForGER", "injectedGERAtIndex",
			},
			resultOf: types.StepWaitL1SettledGER,
			result: &types.L1SettledGERResult{
				TxHash: settlementTxHash, BlockNumber: 400, L1InfoTreeIndex: &settlementLeafIndex,
				HasVerifyBatchesTrustedAggregator: true, HasUpdateL1InfoTree: true,
			},
		},
		{
			name:            "L1->L2 claimed",
			bridgeType:      types.BridgeTypeL1ToL2,
			facts:           fakeFacts{originGER: originGER, injectedGERAtIndex: injectedGER, claim: claim},
			expectedStep:    types.StepClaimed,
			expectedQueried: []string{"originGER", "injectedGERAtIndex", "claimFor"},
		},
		{
			name:       "L1->L2 with GER update already done skips OriginGER",
			bridgeType: types.BridgeTypeL1ToL2,
			facts:      fakeFacts{},
			prevSteps: []BridgeStepPath{
				{
					Step: types.StepWaitingGERUpdate, Status: types.StepStatusDone,
					ResultGerUpdate: &types.GERUpdateResult{
						L1InfoTreeIndex: originGERLeafIndex, GER: ger, BlockNumber: blockNumber,
					},
				},
				{Step: types.StepWaitingGERInjection, Status: types.StepStatusInProgress},
				{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
				{Step: types.StepClaimed, Status: types.StepStatusPending},
			},
			expectedStep:    types.StepWaitingGERInjection,
			expectedQueried: []string{"injectedGERAtIndex"},
		},
		{
			name:       "L2->L2 with every milestone done but the claim only queries the claim",
			bridgeType: types.BridgeTypeL2ToL2,
			facts:      fakeFacts{claim: claim},
			prevSteps: []BridgeStepPath{
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
			prevSteps: []BridgeStepPath{
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

			result, err := ResolveSteps(context.Background(), log.NewLoggerNil(), testResolvers(&tc.facts), tracking, now)
			require.NoError(t, err)
			require.Equal(t, tc.expectedQueried, tc.facts.queried)

			idx := result.StepIndex()
			require.NotNil(t, idx)
			require.Equal(t, tc.expectedStep, result.AllSteps()[*idx].Step)

			if tc.resultOf != 0 || tc.result != nil {
				sp := result.AllSteps()[indexOfStep(result.AllSteps(), tc.resultOf)]
				require.Equal(t, tc.result, sp.Result())
			}

			if tc.facts.certificate != nil {
				if pIdx := indexOfStep(result.AllSteps(), types.StepPendingInclusion); pIdx >= 0 {
					if pi := result.AllSteps()[pIdx]; pi.Status == types.StepStatusDone {
						require.Equal(t, &types.PendingInclusionResult{
							CertificateID: tc.facts.certificate.CertificateID,
							NewLER:        tc.facts.certificate.NewLocalExitRoot,
							PreviousLER:   tc.facts.certificate.PreviousLocalExitRoot,
						}, pi.Result(), "PendingInclusion carries the certificate ID and LER transition once met")
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
	settlementTxHash := common.Hash{4}
	settledCert := &types.CertificateInclusionData{
		CertificateData: types.CertificateData{Status: agglayertypes.Settled, SettlementTxHash: &settlementTxHash},
	}
	settlementLeafIndex := uint32(7)
	settlementResult := &types.L1SettledGERResult{
		TxHash: settlementTxHash, L1InfoTreeIndex: &settlementLeafIndex,
		HasVerifyBatchesTrustedAggregator: true, HasUpdateL1InfoTree: true,
	}
	factsErr := errors.New("source down")

	testCases := []struct {
		name         string
		bridgeType   types.BridgeType
		facts        fakeFacts
		expectedErr  string
		expectedStep types.BridgeStep
	}{
		{
			name:         "origin GER error",
			bridgeType:   types.BridgeTypeL1ToL2,
			facts:        fakeFacts{originGERErr: factsErr},
			expectedErr:  "origin GER",
			expectedStep: types.StepWaitingGERUpdate,
		},
		{
			name:         "origin LER error",
			bridgeType:   types.BridgeTypeL2ToL1,
			facts:        fakeFacts{originLERErr: factsErr},
			expectedErr:  "origin LER",
			expectedStep: types.StepWaitingLERUpdate,
		},
		{
			name:         "certificate error",
			bridgeType:   types.BridgeTypeL2ToL1,
			facts:        fakeFacts{originLER: originLER, certificateErr: factsErr},
			expectedErr:  "certificate",
			expectedStep: types.StepPendingInclusion,
		},
		{
			name:       "injected GER error",
			bridgeType: types.BridgeTypeL1ToL2,
			facts: fakeFacts{
				originGER:             &types.GERData{GER: &common.Hash{1}, BlockNumber: new(uint64)},
				injectedGERAtIndexErr: factsErr,
			},
			expectedErr:  "injected GER",
			expectedStep: types.StepWaitingGERInjection,
		},
		{
			name:       "settlement GER update error",
			bridgeType: types.BridgeTypeL2ToL1,
			facts: fakeFacts{
				originLER:     originLER,
				certificate:   settledCert,
				settlementErr: factsErr,
			},
			expectedErr:  "settlement GER update",
			expectedStep: types.StepWaitL1SettledGER,
		},
		{
			name:       "claim status error",
			bridgeType: types.BridgeTypeL2ToL1,
			facts: fakeFacts{
				originLER:   originLER,
				certificate: settledCert,
				settlement:  settlementResult,
				claimErr:    factsErr,
			},
			expectedErr:  "claim status",
			expectedStep: types.StepWaitingClaim,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tracking := newTracking(tc.bridgeType, nil, now)

			result, err := ResolveSteps(context.Background(), log.NewLoggerNil(), testResolvers(&tc.facts), tracking, now)
			require.ErrorIs(t, err, factsErr)
			require.ErrorContains(t, err, tc.expectedErr)

			// any step resolved earlier this same tick (e.g. "certificate error": LER resolves
			// before Certificate fails) stays Done — only the step whose resolver actually
			// failed is marked, so retries are counted against it, not whatever was current at
			// entry
			idx := indexOfStep(result.AllSteps(), tc.expectedStep)
			require.GreaterOrEqual(t, idx, 0)
			errStep := result.AllSteps()[idx]
			require.Equal(t, types.StepStatusError, errStep.Status)
			require.NotNil(t, errStep.Error)
			require.Equal(t, types.StepErrorTransient, errStep.Error.ErrorType)
			require.Equal(t, 1, errStep.Error.RetryCount)
			require.Contains(t, errStep.Error.Description[0], tc.expectedErr)

			for i, sp := range result.AllSteps() {
				switch {
				case i == idx:
					continue
				case i < idx:
					require.Equal(t, types.StepStatusDone, sp.Status, "steps resolved earlier this tick stay Done")
				default:
					require.Equal(t, tracking.AllSteps()[i], sp, "steps not yet reached stay untouched")
				}
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
		result := UpdateStep(tracking, 0, nil, false, nil, t2)

		require.Same(t, tracking, result)
	})

	t.Run("completing a step closes it and opens the next one", func(t *testing.T) {
		t.Parallel()

		tracking := newTracking(types.BridgeTypeL1ToL2, nil, t1)
		gerUpdate := &types.GERUpdateResult{GER: common.Hash{1}, BlockNumber: 100}
		advanced := UpdateStep(tracking, 0, gerUpdate, true, nil, t2)

		steps := advanced.AllSteps()
		require.Equal(t, []BridgeStepPath{
			{
				Step: types.StepWaitingGERUpdate, Status: types.StepStatusDone,
				StartDate: &t1, EndDate: &t2, ResultGerUpdate: gerUpdate,
			},
			{Step: types.StepWaitingGERInjection, Status: types.StepStatusInProgress, StartDate: &t2},
			{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
			{Step: types.StepClaimed, Status: types.StepStatusPending},
		}, steps)
	})

	t.Run("terminal step completes the moment it is reached", func(t *testing.T) {
		t.Parallel()

		tracking := newTracking(types.BridgeTypeL1ToL2, []BridgeStepPath{
			{Step: types.StepWaitingGERUpdate, Status: types.StepStatusDone, EndDate: &t1},
			{Step: types.StepWaitingGERInjection, Status: types.StepStatusDone, EndDate: &t1},
			{Step: types.StepWaitingClaim, Status: types.StepStatusInProgress, StartDate: &t1},
			{Step: types.StepClaimed, Status: types.StepStatusPending},
		}, t1)

		claim := &types.ClaimResult{ClaimTx: common.Hash{2}, BlockNumber: 200}
		advanced := UpdateStep(tracking, 2, claim, true, nil, t2)

		last := advanced.AllSteps()[len(advanced.AllSteps())-1]
		require.Equal(t, types.StepClaimed, last.Step)
		require.Equal(t, types.StepStatusDone, last.Status)
		require.Equal(t, &t2, last.StartDate)
		require.Equal(t, &t2, last.EndDate)
	})

	t.Run("a successful check clears a previous transient error even without progress", func(t *testing.T) {
		t.Parallel()

		tracking := newTracking(types.BridgeTypeL1ToL2, []BridgeStepPath{
			{
				Step: types.StepWaitingGERUpdate, Status: types.StepStatusError, StartDate: &t1,
				Error: &types.ErrorStep{ErrorType: types.StepErrorTransient, RetryCount: 2},
			},
			{Step: types.StepWaitingGERInjection, Status: types.StepStatusPending},
			{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
			{Step: types.StepClaimed, Status: types.StepStatusPending},
		}, t1)

		advanced := UpdateStep(tracking, 0, nil, false, nil, t2)

		require.NotSame(t, tracking, advanced, "the error clearing must be recorded")
		require.Nil(t, advanced.AllSteps()[0].Error)
		require.Equal(t, types.StepStatusInProgress, advanced.AllSteps()[0].Status)
		require.Equal(t, &t1, advanced.AllSteps()[0].StartDate, "the step's original start time is preserved")
	})

	t.Run("attaches a result even though the step is not complete yet", func(t *testing.T) {
		t.Parallel()

		tracking := newTracking(types.BridgeTypeL2ToL1, []BridgeStepPath{
			{Step: types.StepWaitingLERUpdate, Status: types.StepStatusDone, EndDate: &t1},
			{Step: types.StepPendingInclusion, Status: types.StepStatusDone, EndDate: &t1},
			{Step: types.StepCertificatePending, Status: types.StepStatusInProgress, StartDate: &t1},
			{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
			{Step: types.StepClaimed, Status: types.StepStatusPending},
		}, t1)

		cert := &types.CertificateData{Status: agglayertypes.Pending}
		advanced := UpdateStep(tracking, 2, cert, false, nil, t2)

		require.NotSame(t, tracking, advanced)
		sp := advanced.AllSteps()[2]
		require.Equal(t, types.StepStatusInProgress, sp.Status, "still waiting, not complete")
		require.Equal(t, cert, sp.Result(), "the current (unsettled) certificate is visible while waiting")
		require.Equal(t, &t1, sp.StartDate, "unaffected by the result update")
	})

	t.Run("no progress once the same result has already been recorded", func(t *testing.T) {
		t.Parallel()

		tracking := newTracking(types.BridgeTypeL2ToL1, []BridgeStepPath{
			{Step: types.StepWaitingLERUpdate, Status: types.StepStatusDone, EndDate: &t1},
			{Step: types.StepPendingInclusion, Status: types.StepStatusDone, EndDate: &t1},
			{
				Step: types.StepCertificatePending, Status: types.StepStatusInProgress, StartDate: &t1,
				ResultCertificateData: &types.CertificateData{Status: agglayertypes.Pending},
			},
			{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
			{Step: types.StepClaimed, Status: types.StepStatusPending},
		}, t1)

		result := UpdateStep(tracking, 2, &types.CertificateData{Status: agglayertypes.Pending}, false, nil, t2)

		require.Same(t, tracking, result, "an equal result is not a change worth republishing")
	})

	t.Run("a non-nil stepErr marks the step as failed instead of completing it", func(t *testing.T) {
		t.Parallel()

		tracking := newTracking(types.BridgeTypeL1ToL2, []BridgeStepPath{
			{Step: types.StepWaitingGERUpdate, Status: types.StepStatusInProgress, StartDate: &t1},
			{Step: types.StepWaitingGERInjection, Status: types.StepStatusPending},
			{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
			{Step: types.StepClaimed, Status: types.StepStatusPending},
		}, t1)

		advanced := UpdateStep(tracking, 0, nil, true, errFakeUpdateStep, t2)

		sp := advanced.AllSteps()[0]
		require.Equal(t, types.StepStatusError, sp.Status, "stepErr takes over regardless of complete")
		require.NotNil(t, sp.Error)
		require.Equal(t, types.StepErrorTransient, sp.Error.ErrorType)
		require.Equal(t, 1, sp.Error.RetryCount)
		require.Equal(t, []string{errFakeUpdateStep.Error()}, sp.Error.Description)
	})

	t.Run("a repeated stepErr accumulates onto the previous retry count and description", func(t *testing.T) {
		t.Parallel()

		tracking := newTracking(types.BridgeTypeL1ToL2, []BridgeStepPath{
			{
				Step: types.StepWaitingGERUpdate, Status: types.StepStatusError, StartDate: &t1,
				Error: &types.ErrorStep{
					ErrorType: types.StepErrorTransient, RetryCount: 1,
					Description: []string{errFakeUpdateStep.Error()},
				},
			},
			{Step: types.StepWaitingGERInjection, Status: types.StepStatusPending},
			{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
			{Step: types.StepClaimed, Status: types.StepStatusPending},
		}, t1)

		advanced := UpdateStep(tracking, 0, nil, false, errFakeUpdateStep, t2)

		sp := advanced.AllSteps()[0]
		require.Equal(t, 2, sp.Error.RetryCount)
		require.Equal(t, []string{errFakeUpdateStep.Error(), errFakeUpdateStep.Error()}, sp.Error.Description)
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

	tracking := newTracking(types.BridgeTypeL2ToL1, []BridgeStepPath{
		{Step: types.StepWaitingLERUpdate, Status: types.StepStatusDone, EndDate: &t1},
		{Step: types.StepPendingInclusion, Status: types.StepStatusInProgress, StartDate: &t1},
		{Step: types.StepCertificatePending, Status: types.StepStatusPending},
		{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
		{Step: types.StepClaimed, Status: types.StepStatusPending},
	}, t1)

	cert := &types.CertificateInclusionData{
		CertificateData: types.CertificateData{CertificateID: common.Hash{9}, Status: agglayertypes.Settled},
	}
	facts := &fakeFacts{certificate: cert}

	result, err := ResolveSteps(context.Background(), log.NewLoggerNil(), testResolvers(facts), tracking, t2)
	require.NoError(t, err)
	require.Equal(t, []string{"certificate", "certificate", "claimFor"}, facts.queried)

	steps := result.AllSteps()
	require.Equal(t, types.StepStatusDone, steps[1].Status, "PendingInclusion skipped straight through")
	require.Equal(t, &t2, steps[1].EndDate)
	require.Equal(t, &types.PendingInclusionResult{CertificateID: cert.CertificateID}, steps[1].Result())
	require.Equal(t, types.StepStatusDone, steps[2].Status)
	require.Equal(t, &t2, steps[2].EndDate)
	require.Equal(t, &cert.CertificateData, steps[2].Result())
	require.Equal(t, types.StepStatusInProgress, steps[3].Status, "WaitingClaim opens next")
}
