package domain

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// fakeFacts is a canned BridgeFacts that records which facts were queried, to assert
// DeriveStep stops at the first unmet milestone
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

func (f *fakeFacts) OriginGER(_ context.Context) (*types.GERData, error) {
	f.queried = append(f.queried, "originGER")
	return f.originGER, f.originGERErr
}

func (f *fakeFacts) OriginLER(_ context.Context) (*types.LERUpdateResult, error) {
	f.queried = append(f.queried, "originLER")
	return f.originLER, f.originLERErr
}

func (f *fakeFacts) Certificate(_ context.Context) (*types.CertificateData, error) {
	f.queried = append(f.queried, "certificate")
	return f.certificate, f.certificateErr
}

func (f *fakeFacts) InjectedGER(_ context.Context) (*types.GERData, error) {
	f.queried = append(f.queried, "injectedGER")
	return f.injectedGER, f.injectedGERErr
}

func (f *fakeFacts) ClaimFor(_ context.Context) (*types.ClaimResult, error) {
	f.queried = append(f.queried, "claimFor")
	return f.claim, f.claimErr
}

func TestDeriveStep(t *testing.T) {
	t.Parallel()

	ger := common.Hash{1}
	blockNumber := uint64(100)
	originGER := &types.GERData{NetworkID: 0, GER: &ger, BlockNumber: &blockNumber}
	originLER := &types.LERUpdateResult{NetworkID: 1, LER: common.Hash{2}, BlockNumber: 200}
	injectedGER := &types.GERData{NetworkID: 2, GER: &common.Hash{1}}
	settledCert := &types.CertificateData{Status: agglayertypes.Settled}
	claim := &types.ClaimResult{ClaimTx: common.Hash{3}, BlockNumber: 300}

	testCases := []struct {
		name                string
		originNetwork       uint32
		destinationNetwork  uint32
		facts               fakeFacts
		expectedStep        types.BridgeStep
		expectedQueried     []string
		expectedGERUpdate   *types.GERUpdateResult
		expectedLERUpdate   *types.LERUpdateResult
		expectedCertificate *types.CertificateData
		expectedInjectedGER *types.InjectedGERResult
		expectedClaim       *types.ClaimResult
	}{
		{
			name:               "L1 origin, no origin GER -> WaitingGERUpdate, later facts not queried",
			originNetwork:      0,
			destinationNetwork: 1,
			facts:              fakeFacts{},
			expectedStep:       types.StepWaitingGERUpdate,
			expectedQueried:    []string{"originGER"},
		},
		{
			name:               "L1->L2 never queries certificate or LER",
			originNetwork:      0,
			destinationNetwork: 1,
			facts:              fakeFacts{originGER: originGER},
			expectedStep:       types.StepWaitingGERInjection,
			expectedQueried:    []string{"originGER", "injectedGER"},
			expectedGERUpdate:  &types.GERUpdateResult{GER: ger, BlockNumber: blockNumber},
		},
		{
			name:               "L2 origin, no origin LER -> WaitingLERUpdate, later facts not queried",
			originNetwork:      1,
			destinationNetwork: 0,
			facts:              fakeFacts{},
			expectedStep:       types.StepWaitingLERUpdate,
			expectedQueried:    []string{"originLER"},
		},
		{
			name:               "L2 origin without certificate -> PendingInclusion",
			originNetwork:      1,
			destinationNetwork: 0,
			facts:              fakeFacts{originLER: originLER},
			expectedStep:       types.StepPendingInclusion,
			expectedQueried:    []string{"originLER", "certificate"},
			expectedLERUpdate:  originLER,
		},
		{
			name:               "certificate pending -> CertificatePending",
			originNetwork:      1,
			destinationNetwork: 0,
			facts: fakeFacts{
				originLER:   originLER,
				certificate: &types.CertificateData{Status: agglayertypes.Pending},
			},
			expectedStep:      types.StepCertificatePending,
			expectedQueried:   []string{"originLER", "certificate"},
			expectedLERUpdate: originLER,
		},
		{
			name:               "certificate proven -> CertificateProcessing",
			originNetwork:      1,
			destinationNetwork: 0,
			facts: fakeFacts{
				originLER:   originLER,
				certificate: &types.CertificateData{Status: agglayertypes.Proven},
			},
			expectedStep:      types.StepCertificateProcessing,
			expectedQueried:   []string{"originLER", "certificate"},
			expectedLERUpdate: originLER,
		},
		{
			name:               "certificate in error -> CertificateProcessing",
			originNetwork:      1,
			destinationNetwork: 0,
			facts: fakeFacts{
				originLER:   originLER,
				certificate: &types.CertificateData{Status: agglayertypes.InError},
			},
			expectedStep:      types.StepCertificateProcessing,
			expectedQueried:   []string{"originLER", "certificate"},
			expectedLERUpdate: originLER,
		},
		{
			name:                "L2->L1 settled skips injection and is claimable",
			originNetwork:       1,
			destinationNetwork:  0,
			facts:               fakeFacts{originLER: originLER, certificate: settledCert},
			expectedStep:        types.StepWaitingClaim,
			expectedQueried:     []string{"originLER", "certificate", "claimFor"},
			expectedLERUpdate:   originLER,
			expectedCertificate: settledCert,
		},
		{
			name:                "L2->L2 without injected GER -> WaitingGERInjection",
			originNetwork:       1,
			destinationNetwork:  2,
			facts:               fakeFacts{originLER: originLER, certificate: settledCert},
			expectedStep:        types.StepWaitingGERInjection,
			expectedQueried:     []string{"originLER", "certificate", "injectedGER"},
			expectedLERUpdate:   originLER,
			expectedCertificate: settledCert,
		},
		{
			name:               "L2->L2 claimed -> Claimed with Claim set",
			originNetwork:      1,
			destinationNetwork: 2,
			facts: fakeFacts{
				originLER:   originLER,
				certificate: settledCert,
				injectedGER: injectedGER,
				claim:       claim,
			},
			expectedStep:        types.StepClaimed,
			expectedQueried:     []string{"originLER", "certificate", "injectedGER", "claimFor"},
			expectedLERUpdate:   originLER,
			expectedCertificate: settledCert,
			expectedInjectedGER: &types.InjectedGERResult{GER: common.Hash{1}},
			expectedClaim:       claim,
		},
		{
			name:                "L1->L2 claimed",
			originNetwork:       0,
			destinationNetwork:  1,
			facts:               fakeFacts{originGER: originGER, injectedGER: injectedGER, claim: claim},
			expectedStep:        types.StepClaimed,
			expectedQueried:     []string{"originGER", "injectedGER", "claimFor"},
			expectedGERUpdate:   &types.GERUpdateResult{GER: ger, BlockNumber: blockNumber},
			expectedInjectedGER: &types.InjectedGERResult{GER: common.Hash{1}},
			expectedClaim:       claim,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := DeriveStep(context.Background(),
				tc.originNetwork, tc.destinationNetwork, &tc.facts)
			require.NoError(t, err)
			require.Equal(t, tc.expectedStep, res.Step)
			require.Equal(t, tc.expectedQueried, tc.facts.queried)
			require.Equal(t, tc.expectedGERUpdate, res.GERUpdate)
			require.Equal(t, tc.expectedLERUpdate, res.LERUpdate)
			require.Equal(t, tc.expectedCertificate, res.Certificate)
			require.Equal(t, tc.expectedInjectedGER, res.InjectedGER)
			require.Equal(t, tc.expectedClaim, res.Claim)
		})
	}
}

func TestDeriveStepErrors(t *testing.T) {
	t.Parallel()

	originLER := &types.LERUpdateResult{NetworkID: 1, LER: common.Hash{2}, BlockNumber: 200}
	settledCert := &types.CertificateData{Status: agglayertypes.Settled}
	factsErr := errors.New("source down")

	testCases := []struct {
		name               string
		originNetwork      uint32
		destinationNetwork uint32
		facts              fakeFacts
		expectedErr        string
	}{
		{
			name:               "origin GER error",
			originNetwork:      0,
			destinationNetwork: 1,
			facts:              fakeFacts{originGERErr: factsErr},
			expectedErr:        "origin GER",
		},
		{
			name:               "origin LER error",
			originNetwork:      1,
			destinationNetwork: 0,
			facts:              fakeFacts{originLERErr: factsErr},
			expectedErr:        "origin LER",
		},
		{
			name:               "certificate error",
			originNetwork:      1,
			destinationNetwork: 0,
			facts:              fakeFacts{originLER: originLER, certificateErr: factsErr},
			expectedErr:        "certificate",
		},
		{
			name:               "injected GER error",
			originNetwork:      0,
			destinationNetwork: 1,
			facts:              fakeFacts{originGER: &types.GERData{GER: &common.Hash{1}, BlockNumber: new(uint64)}, injectedGERErr: factsErr},
			expectedErr:        "injected GER",
		},
		{
			name:               "claim status error",
			originNetwork:      1,
			destinationNetwork: 0,
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

			_, err := DeriveStep(context.Background(),
				tc.originNetwork, tc.destinationNetwork, &tc.facts)
			require.ErrorIs(t, err, factsErr)
			require.ErrorContains(t, err, tc.expectedErr)
		})
	}
}
