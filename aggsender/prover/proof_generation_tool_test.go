package prover

import (
	"context"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	claimsynctypesmocks "github.com/agglayer/aggkit/claimsync/types/mocks"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGenerateAggchainProof(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setupMocks func(
			ctx context.Context,
			mockL2Syncer *mocks.L2BridgeSyncer,
			mockL2ClaimSyncer *claimsynctypesmocks.ClaimSyncer,
			mockAggchainProofClient *mocks.AggchainProofClientInterface,
			mockFlow *mocks.AggchainProofFlow,
		)
		expectedError string
		expectedProof *types.SP1StarkProof
	}{
		{
			name: "Success",
			setupMocks: func(ctx context.Context,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockL2ClaimSyncer *claimsynctypesmocks.ClaimSyncer,
				mockAggchainProofClient *mocks.AggchainProofClientInterface,
				mockFlow *mocks.AggchainProofFlow,
			) {
				mockL2Syncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(20), true, nil)
				mockL2ClaimSyncer.EXPECT().GetClaims(ctx, uint64(1), uint64(10)).Return([]claimsynctypes.Claim{}, nil)
				certBuildParams := &types.CertificateBuildParams{
					Claims: []claimsynctypes.Claim{},
				}
				mockFlow.EXPECT().GenerateAggchainProof(ctx, uint64(0), uint64(10), certBuildParams).Return(
					&types.AggchainProof{SP1StarkProof: &types.SP1StarkProof{Proof: []byte("proof")}}, nil)
			},
			expectedProof: &types.SP1StarkProof{Proof: []byte("proof")},
		},
		{
			name: "Failure_GetLastProcessedBlock",
			setupMocks: func(ctx context.Context,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockL2ClaimSyncer *claimsynctypesmocks.ClaimSyncer,
				mockAggchainProofClient *mocks.AggchainProofClientInterface,
				mockFlow *mocks.AggchainProofFlow,
			) {
				mockL2Syncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, errors.New("test error"))
			},
			expectedError: "error getting last processed block from l2: test error",
		},
		{
			name: "Failure_GetClaims",
			setupMocks: func(ctx context.Context,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockL2ClaimSyncer *claimsynctypesmocks.ClaimSyncer,
				mockAggchainProofClient *mocks.AggchainProofClientInterface,
				mockFlow *mocks.AggchainProofFlow,
			) {
				mockL2Syncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(20), true, nil)
				mockL2ClaimSyncer.EXPECT().GetClaims(ctx, uint64(1), uint64(10)).Return(nil, errors.New("test error"))
			},
			expectedError: "error getting claims (imported bridge exits)",
		},
		{
			name: "Failure_GenerateAggchainProof",
			setupMocks: func(ctx context.Context,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockL2ClaimSyncer *claimsynctypesmocks.ClaimSyncer,
				mockAggchainProofClient *mocks.AggchainProofClientInterface,
				mockFlow *mocks.AggchainProofFlow,
			) {
				mockL2Syncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(20), true, nil)
				mockL2ClaimSyncer.EXPECT().GetClaims(ctx, uint64(1), uint64(10)).Return([]claimsynctypes.Claim{}, nil)
				certBuildParams := &types.CertificateBuildParams{
					Claims: []claimsynctypes.Claim{},
				}
				mockFlow.EXPECT().GenerateAggchainProof(ctx, uint64(0), uint64(10), certBuildParams).Return(
					nil, errors.New("test error"))
			},
			expectedError: "error generating Aggchain proof",
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			lastProvenBlock := uint64(0)
			toBlock := uint64(10)

			mockLogger := log.WithFields("test", tt.name)
			mockL2Syncer := mocks.NewL2BridgeSyncer(t)
			mockL2ClaimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
			mockAggchainProofClient := mocks.NewAggchainProofClientInterface(t)
			mockFlow := mocks.NewAggchainProofFlow(t)

			tool := &AggchainProofGenerationTool{
				logger:              mockLogger,
				l2Syncer:            mockL2Syncer,
				l2ClaimSyncer:       mockL2ClaimSyncer,
				aggchainProofClient: mockAggchainProofClient,
				flow:                mockFlow,
			}

			tt.setupMocks(ctx, mockL2Syncer, mockL2ClaimSyncer, mockAggchainProofClient, mockFlow)

			proof, err := tool.GenerateAggchainProof(ctx, lastProvenBlock, toBlock)
			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedProof, proof)
			}
		})
	}
}

func TestGetRPCServices(t *testing.T) {
	t.Parallel()

	mockLogger := log.WithFields("test", "GetRPCServices")
	mockL2Syncer := mocks.NewL2BridgeSyncer(t)
	mockAggchainProofClient := mocks.NewAggchainProofClientInterface(t)
	mockFlow := mocks.NewAggchainProofFlow(t)

	tool := &AggchainProofGenerationTool{
		logger:              mockLogger,
		l2Syncer:            mockL2Syncer,
		aggchainProofClient: mockAggchainProofClient,
		flow:                mockFlow,
	}

	services := tool.GetRPCServices()

	require.Len(t, services, 1)
	require.Equal(t, "aggkit", services[0].Name)
	require.NotNil(t, services[0].Service)
}

func TestOptimisticModeQuerierAlwaysOff(t *testing.T) {
	t.Parallel()

	o := &OptimisticModeQuerierAlwaysOff{}
	on, err := o.IsOptimisticModeOn()
	require.NoError(t, err)
	require.False(t, on)
}

func TestNewAggchainProofGenerationTool(t *testing.T) {
	mockL2Syncer := mocks.NewL2BridgeSyncer(t)
	mockL2ClaimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
	mockL2Client := aggkittypesmocks.NewBaseEthereumClienter(t)
	mockL1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	mockL1Client.EXPECT().CodeAt(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	mockL2Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	mockL2Client.EXPECT().CodeAt(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
	mockL1InfoTreeSyncer.EXPECT().Finality().Return(aggkittypes.FinalizedBlock).Maybe()

	_, err := NewAggchainProofGenerationTool(context.TODO(), log.WithFields("module", "test"),
		Config{AggkitProverClient: aggkitgrpc.DefaultConfig()}, mockL1Client, mockL2Client, mockL2Syncer, mockL2ClaimSyncer, mockL1InfoTreeSyncer)
	require.Error(t, err)

	cfg := Config{
		AggkitProverClient:   aggkitgrpc.DefaultConfig(),
		GlobalExitRootL2Addr: common.HexToAddress("0xbeef"),
	}

	_, err = NewAggchainProofGenerationTool(context.TODO(), log.WithFields("module", "test"),
		cfg, mockL1Client, mockL2Client, mockL2Syncer, mockL2ClaimSyncer, mockL1InfoTreeSyncer)
	require.ErrorContains(t, err, "L2 GER reader")
}
