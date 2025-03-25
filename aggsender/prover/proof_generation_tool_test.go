package prover

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestGenerateAggchainProof(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setupMocks func(
			ctx context.Context,
			mockL2Syncer *mocks.L2BridgeSyncer,
			mockAggchainProofClient *mocks.AggchainProofClientInterface,
			mockFlow *mocks.AggchainProofFlow,
		)
		expectedError string
		expectedProof []byte
	}{
		{
			name: "Success",
			setupMocks: func(ctx context.Context,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockAggchainProofClient *mocks.AggchainProofClientInterface,
				mockFlow *mocks.AggchainProofFlow,
			) {
				mockL2Syncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(20), nil)
				mockL2Syncer.EXPECT().GetClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{}, nil)
				mockFlow.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(treetypes.Proof{}, &l1infotreesync.L1InfoTreeLeaf{}, &treetypes.Root{}, nil)
				mockFlow.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(&treetypes.Root{}, []bridgesync.Claim{}).Return(nil)
				mockFlow.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{}, uint64(1), uint64(10)).Return(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber{}, nil)
				mockFlow.EXPECT().GetImportedBridgeExitsForProver([]bridgesync.Claim{}).Return([]*agglayertypes.ImportedBridgeExitWithBlockNumber{}, nil)
				mockAggchainProofClient.EXPECT().GenerateAggchainProof(uint64(1), uint64(10), common.Hash{},
					l1infotreesync.L1InfoTreeLeaf{}, agglayertypes.MerkleProof{
						Root:  common.Hash{},
						Proof: treetypes.Proof{},
					}, map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber{},
					[]*agglayertypes.ImportedBridgeExitWithBlockNumber{}).Return(&types.AggchainProof{Proof: []byte("proof")}, nil)
			},
			expectedProof: []byte("proof"),
		},
		{
			name: "Failure_GetLastProcessedBlock",
			setupMocks: func(ctx context.Context,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockAggchainProofClient *mocks.AggchainProofClientInterface,
				mockFlow *mocks.AggchainProofFlow,
			) {
				mockL2Syncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), errors.New("test error"))
			},
			expectedError: "error getting last processed block from l2: test error",
		},
		{
			name: "Failure_GetClaims",
			setupMocks: func(ctx context.Context,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockAggchainProofClient *mocks.AggchainProofClientInterface,
				mockFlow *mocks.AggchainProofFlow,
			) {
				mockL2Syncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(20), nil)
				mockL2Syncer.EXPECT().GetClaims(ctx, uint64(1), uint64(10)).Return(nil, errors.New("test error"))
			},
			expectedError: "error getting claims (imported bridge exits)",
		},
		{
			name: "Failure_GetFinalizedL1InfoTreeData",
			setupMocks: func(ctx context.Context,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockAggchainProofClient *mocks.AggchainProofClientInterface,
				mockFlow *mocks.AggchainProofFlow,
			) {
				mockL2Syncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(20), nil)
				mockL2Syncer.EXPECT().GetClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{}, nil)
				mockFlow.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(treetypes.Proof{}, nil, nil, errors.New("test error"))
			},
			expectedError: "error getting finalized L1 Info tree data: test error",
		},
		{
			name: "Failure_CheckIfClaimsArePartOfFinalizedL1InfoTree",
			setupMocks: func(ctx context.Context,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockAggchainProofClient *mocks.AggchainProofClientInterface,
				mockFlow *mocks.AggchainProofFlow,
			) {
				mockL2Syncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(20), nil)
				mockL2Syncer.EXPECT().GetClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{}, nil)
				mockFlow.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(treetypes.Proof{}, &l1infotreesync.L1InfoTreeLeaf{}, &treetypes.Root{}, nil)
				mockFlow.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(&treetypes.Root{}, []bridgesync.Claim{}).Return(errors.New("test error"))
			},
			expectedError: "error checking if claims are part of finalized L1 Info tree root",
		},
		{
			name: "Failure_GetImportedBridgeExitsForProver",
			setupMocks: func(ctx context.Context,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockAggchainProofClient *mocks.AggchainProofClientInterface,
				mockFlow *mocks.AggchainProofFlow,
			) {
				mockL2Syncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(20), nil)
				mockL2Syncer.EXPECT().GetClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{}, nil)
				mockFlow.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(treetypes.Proof{}, &l1infotreesync.L1InfoTreeLeaf{}, &treetypes.Root{}, nil)
				mockFlow.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(&treetypes.Root{}, []bridgesync.Claim{}).Return(nil)
				mockFlow.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{}, uint64(1), uint64(10)).Return(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber{}, nil)
				mockFlow.EXPECT().GetImportedBridgeExitsForProver([]bridgesync.Claim{}).Return(nil, errors.New("test error"))
			},
			expectedError: "error getting imported bridge exits for prover: test error",
		},
		{
			name: "Failure_GenerateAggchainProof",
			setupMocks: func(ctx context.Context,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockAggchainProofClient *mocks.AggchainProofClientInterface,
				mockFlow *mocks.AggchainProofFlow,
			) {
				mockL2Syncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(20), nil)
				mockL2Syncer.EXPECT().GetClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{}, nil)
				mockFlow.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(treetypes.Proof{}, &l1infotreesync.L1InfoTreeLeaf{}, &treetypes.Root{}, nil)
				mockFlow.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(&treetypes.Root{}, []bridgesync.Claim{}).Return(nil)
				mockFlow.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{}, uint64(1), uint64(10)).Return(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber{}, nil)
				mockFlow.EXPECT().GetImportedBridgeExitsForProver([]bridgesync.Claim{}).Return([]*agglayertypes.ImportedBridgeExitWithBlockNumber{}, nil)
				mockAggchainProofClient.EXPECT().GenerateAggchainProof(uint64(1), uint64(10), common.Hash{},
					l1infotreesync.L1InfoTreeLeaf{}, agglayertypes.MerkleProof{
						Root:  common.Hash{},
						Proof: treetypes.Proof{},
					}, map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber{},
					[]*agglayertypes.ImportedBridgeExitWithBlockNumber{}).Return(nil, errors.New("test error"))
			},
			expectedError: "error fetching aggchain proof for block range 1 : 10: test error",
		},
		{
			name: "Failure_LastL2BlockSyncedLowerThanFromBlock",
			setupMocks: func(ctx context.Context,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockAggchainProofClient *mocks.AggchainProofClientInterface,
				mockFlow *mocks.AggchainProofFlow,
			) {
				mockL2Syncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), nil)
			},
			expectedError: "last L2 block synced 0 is less than from block requested 1",
		},
		{
			name: "Failure_GetInjectedGERsProofs",
			setupMocks: func(ctx context.Context,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockAggchainProofClient *mocks.AggchainProofClientInterface,
				mockFlow *mocks.AggchainProofFlow,
			) {
				mockL2Syncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(20), nil)
				mockL2Syncer.EXPECT().GetClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{}, nil)
				mockFlow.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(treetypes.Proof{}, &l1infotreesync.L1InfoTreeLeaf{}, &treetypes.Root{}, nil)
				mockFlow.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(&treetypes.Root{}, []bridgesync.Claim{}).Return(nil)
				mockFlow.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{}, uint64(1), uint64(10)).Return(nil, errors.New("test error"))
			},
			expectedError: "error getting injected GERs proofs: test error",
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			fromBlock := uint64(1)
			toBlock := uint64(10)

			mockLogger := log.WithFields("test", tt.name)
			mockL2Syncer := mocks.NewL2BridgeSyncer(t)
			mockAggchainProofClient := mocks.NewAggchainProofClientInterface(t)
			mockFlow := mocks.NewAggchainProofFlow(t)

			tool := &AggchainProofGenerationTool{
				logger:              mockLogger,
				l2Syncer:            mockL2Syncer,
				aggchainProofClient: mockAggchainProofClient,
				flow:                mockFlow,
			}

			tt.setupMocks(ctx, mockL2Syncer, mockAggchainProofClient, mockFlow)

			proof, err := tool.GenerateAggchainProof(ctx, fromBlock, toBlock)
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
