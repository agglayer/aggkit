package flows

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_AggchainProverFlow_getCertificateTypeToGenerate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                 string
		optimisticModeReturn bool
		optimisticModeError  error
		expectedType         types.CertificateType
	}{
		{
			name:                 "optimistic mode is on",
			optimisticModeReturn: true,
			expectedType:         types.CertificateTypeOptimistic,
			optimisticModeError:  nil,
		},
		{
			name:                 "optimistic mode is off",
			optimisticModeReturn: false,
			expectedType:         types.CertificateTypeFEP,
			optimisticModeError:  nil,
		},
		{
			name:                 "optimistic mode error",
			optimisticModeReturn: false,
			expectedType:         types.CertificateTypeFEP,
			optimisticModeError:  errors.New("optimistic mode error"),
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data := NewAggchainProverFlowTestData(t,
				NewAggchainProverFlowConfigDefault(),
				NewBaseFlowConfigDefault())
			data.mockOptimisticModeQuerier.EXPECT().IsOptimisticModeOn().Return(tc.optimisticModeReturn, tc.optimisticModeError).Once()
			certificateType, err := data.sut.getCertificateTypeToGenerate()
			if tc.optimisticModeError != nil {
				require.ErrorContains(t, err, tc.optimisticModeError.Error())
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedType, certificateType)
			}
		})
	}
}

// This test checks the case of previous cert in DB typeCert != the new one that must be generated.
// the key part of it is the call to GetCertificateBuildParamsInternal that means that are getting
// a new block range and is not taking advantage of previous proofs
func Test_AggchainProverFlow_PreviousCertNotSameTypeItRecalculateCertificate(t *testing.T) {
	data := NewAggchainProverFlowTestData(t,
		NewAggchainProverFlowConfigDefault(),
		NewBaseFlowConfigDefault())
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
	data.mockFlowBase.EXPECT().GetCertificateBuildParamsInternal(data.ctx, true, types.CertificateTypeFEP).Return(
		nextCert, nil).Once()
	// After the function verifyBuildParamsAndGenerateProof calls to baseFlow.VerifyBuildParams()
	data.mockFlowBase.EXPECT().VerifyBuildParams(mock.Anything).Return(nil).Once()
	// GenerateAggchainProof get data for calling prover
	data.mockL1InfoTreeQuerier.EXPECT().GetFinalizedL1InfoTreeData(data.ctx).Return(treetypes.Proof{},
		&l1infotreesync.L1InfoTreeLeaf{}, &treetypes.Root{}, nil).Once()
	data.mockL1InfoTreeQuerier.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(mock.Anything, mock.Anything).
		Return(nil).Once()
	data.mockGERQuerier.EXPECT().GetInjectedGERsProofs(data.ctx, mock.Anything, nextCert.FromBlock, nextCert.ToBlock).
		Return(nil, nil)
	// Now calls to aggkit-prover service:
	data.mockAggchainProofClient.EXPECT().GenerateAggchainProof(mock.Anything).Return(&types.AggchainProof{
		SP1StarkProof: &types.SP1StarkProof{
			Proof: []byte("proof"),
		},
	}, nil)

	res, err := data.sut.GetCertificateBuildParams(data.ctx)
	require.NoError(t, err)
	require.Equal(t, types.CertificateTypeFEP, res.CertificateType)
}

func Test_AggchainProverFlow_IfGenerateOptimisticCertCallsToAggkitProverSpecificEndpoint(t *testing.T) {
	data := NewAggchainProverFlowTestData(t, NewAggchainProverFlowConfigDefault(), NewBaseFlowConfigDefault())
	lastCert := &types.CertificateHeader{
		Height:    3,
		FromBlock: 10,
		ToBlock:   50,
		Status:    agglayertypes.InError,
		CertType:  types.CertificateTypeUnknown,
	}

	nextCert := &types.CertificateBuildParams{
		FromBlock:           10,
		ToBlock:             70,
		CertificateType:     types.CertificateTypeOptimistic,
		LastSentCertificate: lastCert,
	}

	data.mockL1InfoTreeQuerier.EXPECT().GetFinalizedL1InfoTreeData(data.ctx).Return(treetypes.Proof{},
		&l1infotreesync.L1InfoTreeLeaf{}, &treetypes.Root{}, nil).Once()
	data.mockL1InfoTreeQuerier.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(mock.Anything, mock.Anything).
		Return(nil).Once()
	data.mockGERQuerier.EXPECT().GetInjectedGERsProofs(data.ctx, mock.Anything, nextCert.FromBlock, nextCert.ToBlock).
		Return(nil, nil)
	// Specific calls for a optmistic proof:
	data.mockFlowBase.EXPECT().GetNewLocalExitRoot(data.ctx, nextCert).Return(common.Hash{}, nil).Once()
	signature := []byte("signature")
	data.mockOptimisticSigner.EXPECT().Sign(data.ctx, mock.Anything, mock.Anything, nextCert).Return(
		signature, "extra_data", nil).Once()
	// Now calls to aggkit-prover service:
	data.mockAggchainProofClient.EXPECT().GenerateOptimisticAggchainProof(mock.Anything, signature).Return(&types.AggchainProof{
		SP1StarkProof: &types.SP1StarkProof{
			Proof: []byte("proof"),
		},
	}, nil)

	proof, root, err := data.sut.GenerateAggchainProof(data.ctx, nextCert.FromBlock-1, nextCert.ToBlock, nextCert)
	require.NoError(t, err)
	require.NotNil(t, proof)
	require.NotNil(t, root)
}

type AggchainProverFlowTestData struct {
	mockAggchainProofClient   *mocks.AggchainProofClientInterface
	mockStorage               *mocks.AggSenderStorage
	mockL2BridgeQuerier       *mocks.BridgeQuerier
	mockL1InfoTreeQuerier     *mocks.L1InfoTreeDataQuerier
	mockGERQuerier            *mocks.GERQuerier
	mockL1Client              *mocks.EthClient
	mockOptimisticModeQuerier *mocks.OptimisticModeQuerier
	mockSigner                *mocks.Signer
	mockOptimisticSigner      *mocks.OptimisticSigner
	mockFlowBase              *mocks.AggsenderFlowBaser
	ctx                       context.Context

	sut *AggchainProverFlow
}

func NewAggchainProverFlowTestData(t *testing.T,
	cfg AggchainProverFlowConfig,
	cfgBase BaseFlowConfig) *AggchainProverFlowTestData {
	t.Helper()
	res := &AggchainProverFlowTestData{
		mockAggchainProofClient:   mocks.NewAggchainProofClientInterface(t),
		mockStorage:               mocks.NewAggSenderStorage(t),
		mockL2BridgeQuerier:       mocks.NewBridgeQuerier(t),
		mockL1InfoTreeQuerier:     mocks.NewL1InfoTreeDataQuerier(t),
		mockGERQuerier:            mocks.NewGERQuerier(t),
		mockL1Client:              mocks.NewEthClient(t),
		mockOptimisticModeQuerier: mocks.NewOptimisticModeQuerier(t),
		mockSigner:                mocks.NewSigner(t),
		mockOptimisticSigner:      mocks.NewOptimisticSigner(t),
		mockFlowBase:              mocks.NewAggsenderFlowBaser(t),
		ctx:                       context.TODO(),
	}

	// Simulate the access to baseFlow variables
	res.mockFlowBase.EXPECT().StartL2Block().Return(cfgBase.StartL2Block).Maybe()

	res.sut = NewAggchainProverFlow(
		log.WithFields("flowManager", "AggchainProverFlowTestData"),
		res.mockFlowBase,
		cfg,
		res.mockAggchainProofClient,
		res.mockStorage,
		res.mockL1InfoTreeQuerier,
		res.mockL2BridgeQuerier,
		res.mockGERQuerier,
		res.mockL1Client,
		res.mockSigner,
		res.mockOptimisticModeQuerier,
		res.mockOptimisticSigner,
	)

	return res
}
