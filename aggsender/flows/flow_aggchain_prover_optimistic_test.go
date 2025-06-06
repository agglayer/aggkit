package flows

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_AggchainProverFlow_getCertificateTypeToGenerate(t *testing.T) {
	t.Parallel()

	type optimisticModeCase struct {
		name                string
		optimisticMode      bool
		optimisticModeError error
		expectedType        types.CertificateType
		expectedError       string
	}

	testCases := []optimisticModeCase{
		{
			name:           "optimistic mode is ON",
			optimisticMode: true,
			expectedType:   types.CertificateTypeOptimistic,
		},
		{
			name:           "optimistic mode is OFF",
			optimisticMode: false,
			expectedType:   types.CertificateTypeFEP,
		},
		{
			name:                "error from optimisticModeQuerier",
			optimisticModeError: errors.New("some error"),
			expectedType:        types.CertificateTypeUnknown,
			expectedError:       "getCertificateTypeToGenerate - error getting optimistic mode: some error",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mockOptimisticModeQuerier := mocks.NewOptimisticModeQuerier(t)
			if tc.optimisticModeError != nil {
				mockOptimisticModeQuerier.EXPECT().IsOptimisticModeOn().Return(false, tc.optimisticModeError).Once()
			} else {
				mockOptimisticModeQuerier.EXPECT().IsOptimisticModeOn().Return(tc.optimisticMode, nil).Once()
			}

			flow := &AggchainProverFlow{
				optimisticModeQuerier: mockOptimisticModeQuerier,
			}

			certType, err := flow.getCertificateTypeToGenerate()
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
				require.Equal(t, tc.expectedType, certType)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedType, certType)
			}
		})
	}
}

// This test checks the case of previous cert in DB typeCert != the new one that must be generated.
// the key part of it is the call to GetCertificateBuildParamsInternal that means that are getting
// a new block range and is not taking advantage of previous proofs
func Test_AggchainProverFlow_PreviousCertNotSameTypeItRecalculateCertificate(t *testing.T) {
	data := NewAggchainProverFlowTestData(t,
		NewAggchainProverFlowConfigDefault())
	lastCert := &types.CertificateHeader{
		Height:    3,
		FromBlock: 10,
		ToBlock:   50,
		Status:    agglayertypes.InError,
		CertType:  types.CertificateTypeUnknown,
	}
	lastCertProof := &types.AggchainProof{
		LastProvenBlock: 9,
	}
	nextCert := &types.CertificateBuildParams{
		FromBlock:       10,
		ToBlock:         70,
		CertificateType: types.CertificateTypeFEP,
	}
	data.mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(data.ctx).Return(lastCert, lastCertProof, nil).Once()
	// optimisticMode = off so it will generate a FEP certificate
	data.mockOptimisticModeQuerier.EXPECT().IsOptimisticModeOn().Return(false, nil).Once()
	// then because last cert type doesnt match is going to act as a new one
	// requesting to GetCertificateBuildParamsInternal to create a new cert
	data.mockCertBuilder.EXPECT().GetCertificateBuildParams(data.ctx, true, types.CertificateTypeFEP).Return(
		nextCert, nil).Once()
	// After the function verifyBuildParamsAndGenerateProof calls to baseFlow.VerifyBuildParams()
	data.mockCertVerifier.EXPECT().VerifyBuildParams(mock.Anything).Return(nil).Once()
	// Now calls to aggkit-prover service:
	data.mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(mock.Anything, uint64(9), uint64(70), mock.Anything).Return(&types.AggchainProof{
		SP1StarkProof: &types.SP1StarkProof{
			Proof: []byte("proof"),
		},
	}, &treetypes.Root{}, nil)

	res, err := data.sut.GetCertificateBuildParams(data.ctx)
	require.NoError(t, err)
	require.Equal(t, types.CertificateTypeFEP, res.CertificateType)
}

type AggchainProverFlowTestData struct {
	mockStorage               *mocks.AggSenderStorage
	mockL2BridgeQuerier       *mocks.BridgeQuerier
	mockOptimisticModeQuerier *mocks.OptimisticModeQuerier
	mockSigner                *mocks.Signer
	mockCertBuilder           *mocks.CertificateBuilder
	mockCertVerifier          *mocks.CertificateBuildVerifier
	mockAggchainProofQuerier  *mocks.AggchainProofQuerier
	ctx                       context.Context

	sut *AggchainProverFlow
}

func NewAggchainProverFlowTestData(t *testing.T,
	cfg AggchainProverFlowConfig) *AggchainProverFlowTestData {
	t.Helper()
	res := &AggchainProverFlowTestData{
		mockStorage:               mocks.NewAggSenderStorage(t),
		mockL2BridgeQuerier:       mocks.NewBridgeQuerier(t),
		mockOptimisticModeQuerier: mocks.NewOptimisticModeQuerier(t),
		mockSigner:                mocks.NewSigner(t),
		mockCertBuilder:           mocks.NewCertificateBuilder(t),
		mockCertVerifier:          mocks.NewCertificateBuildVerifier(t),
		mockAggchainProofQuerier:  mocks.NewAggchainProofQuerier(t),
		ctx:                       context.TODO(),
	}

	res.sut = NewAggchainProverFlow(
		log.WithFields("flowManager", "AggchainProverFlowTestData"),
		cfg,
		res.mockStorage,
		res.mockL2BridgeQuerier,
		res.mockSigner,
		res.mockOptimisticModeQuerier,
		res.mockCertBuilder,
		res.mockCertVerifier,
		res.mockAggchainProofQuerier,
	)

	return res
}
