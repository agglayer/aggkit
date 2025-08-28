package query

import (
	"context"
	"errors"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetImportedBridgeExitsForProver(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		claims        []bridgesync.Claim
		expectedExits []*agglayertypes.ImportedBridgeExitWithBlockNumber
		expectedError string
	}{
		{
			name: "success",
			claims: []bridgesync.Claim{
				{
					IsMessage:          false,
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					GlobalIndex:        big.NewInt(1),
					BlockNum:           1,
				},
				{
					IsMessage:          true,
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					GlobalIndex:        big.NewInt(2),
					BlockNum:           2,
				},
			},
			expectedExits: []*agglayertypes.ImportedBridgeExitWithBlockNumber{
				{
					ImportedBridgeExit: &agglayertypes.ImportedBridgeExit{
						BridgeExit: &agglayertypes.BridgeExit{
							LeafType: agglayertypes.LeafTypeAsset,
							TokenInfo: &agglayertypes.TokenInfo{
								OriginNetwork:      1,
								OriginTokenAddress: common.HexToAddress("0x123"),
							},
							DestinationNetwork: 2,
							DestinationAddress: common.HexToAddress("0x456"),
							Amount:             big.NewInt(100),
							Metadata:           crypto.Keccak256([]byte("metadata")),
						},
						GlobalIndex: &agglayertypes.GlobalIndex{
							MainnetFlag: false,
							RollupIndex: 0,
							LeafIndex:   1,
						},
					},
					BlockNumber: 1,
				},
				{
					ImportedBridgeExit: &agglayertypes.ImportedBridgeExit{
						BridgeExit: &agglayertypes.BridgeExit{
							LeafType: agglayertypes.LeafTypeMessage,
							TokenInfo: &agglayertypes.TokenInfo{
								OriginNetwork:      1,
								OriginTokenAddress: common.HexToAddress("0x123"),
							},
							DestinationNetwork: 2,
							DestinationAddress: common.HexToAddress("0x456"),
							Amount:             big.NewInt(100),
							Metadata:           crypto.Keccak256([]byte("metadata")),
						},
						GlobalIndex: &agglayertypes.GlobalIndex{
							MainnetFlag: false,
							RollupIndex: 0,
							LeafIndex:   2,
						},
					},
					BlockNumber: 2,
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log := log.WithFields("aggchain_proof_query", "TestGetImportedBridgeExitsForProver")
			query := &aggchainProofQuery{
				log: log,
			}

			exits, err := query.getImportedBridgeExitsForProver(tc.claims)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedExits, exits)
			}
		})
	}
}

func TestGenerateOptimisticAggchainProof(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name          string
		mockFn        func(*mocks.LocalExitRootQuery, *mocks.OptimisticSigner, *mocks.AggchainProofClientInterface)
		buildParams   *types.CertificateBuildParams
		request       *types.AggchainProofRequest
		expectedProof *types.AggchainProof
		expectedError string
	}{
		{
			name:          "build params is nil",
			buildParams:   nil,
			expectedError: "generateOptimisticAggchainProof - certBuildParams is nil",
		},
		{
			name: "error getting local exit root",
			mockFn: func(lerQuery *mocks.LocalExitRootQuery,
				optimisticSigner *mocks.OptimisticSigner,
				aggchainProofClient *mocks.AggchainProofClientInterface) {
				lerQuery.EXPECT().GetNewLocalExitRoot(ctx, mock.Anything).Return(common.Hash{}, errors.New("some error"))
			},
			buildParams:   &types.CertificateBuildParams{},
			expectedError: "generateOptimisticAggchainProof - error getting new local exit root: some error",
		},
		{
			name: "error signing aggchain proof request",
			mockFn: func(lerQuery *mocks.LocalExitRootQuery,
				optimisticSigner *mocks.OptimisticSigner,
				aggchainProofClient *mocks.AggchainProofClientInterface) {
				lerQuery.EXPECT().GetNewLocalExitRoot(ctx, mock.Anything).Return(common.HexToHash("0x123"), nil)
				optimisticSigner.EXPECT().Sign(ctx, mock.Anything, common.HexToHash("0x123"), mock.Anything).
					Return(nil, "", errors.New("signing error"))
			},
			buildParams:   &types.CertificateBuildParams{},
			request:       &types.AggchainProofRequest{},
			expectedError: "generateOptimisticAggchainProof - error signing aggchain proof request: signing error",
		},
		{
			name: "error generating optimistic aggchain proof",
			mockFn: func(lerQuery *mocks.LocalExitRootQuery,
				optimisticSigner *mocks.OptimisticSigner,
				aggchainProofClient *mocks.AggchainProofClientInterface) {
				lerQuery.EXPECT().GetNewLocalExitRoot(ctx, mock.Anything).Return(common.HexToHash("0x123"), nil)
				optimisticSigner.EXPECT().Sign(ctx, mock.Anything, common.HexToHash("0x123"), mock.Anything).
					Return([]byte("signature"), "extraData", nil)
				aggchainProofClient.EXPECT().GenerateOptimisticAggchainProof(mock.Anything, mock.Anything).
					Return(nil, errors.New("aggchain proof error"))
			},
			buildParams:   &types.CertificateBuildParams{},
			request:       &types.AggchainProofRequest{},
			expectedError: "generateOptimisticAggchainProof - error request aggkit-prover optimistic: aggchain proof error",
		},
		{
			name: "success",
			mockFn: func(lerQuery *mocks.LocalExitRootQuery,
				optimisticSigner *mocks.OptimisticSigner,
				aggchainProofClient *mocks.AggchainProofClientInterface) {
				lerQuery.EXPECT().GetNewLocalExitRoot(ctx, mock.Anything).Return(common.HexToHash("0x123"), nil)
				optimisticSigner.EXPECT().Sign(ctx, mock.Anything, common.HexToHash("0x123"), mock.Anything).
					Return([]byte("signature"), "extraData", nil)
				aggchainProofClient.EXPECT().GenerateOptimisticAggchainProof(mock.Anything, []byte("signature")).
					Return(&types.AggchainProof{
						LastProvenBlock: 100,
						EndBlock:        200,
						CustomChainData: []byte("custom data"),
					}, nil)
			},
			buildParams: &types.CertificateBuildParams{},
			request:     &types.AggchainProofRequest{},
			expectedProof: &types.AggchainProof{
				LastProvenBlock: 100,
				EndBlock:        200,
				CustomChainData: []byte("custom data"),
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lerQuery := mocks.NewLocalExitRootQuery(t)
			optimisticSigner := mocks.NewOptimisticSigner(t)
			aggchainProofClient := mocks.NewAggchainProofClientInterface(t)
			if tc.mockFn != nil {
				tc.mockFn(lerQuery, optimisticSigner, aggchainProofClient)
			}

			log := log.WithFields("aggchain_proof_query", "TestGenerateOptimisticAggchainProof")
			query := NewAggchainProofQuery(
				log,
				aggchainProofClient,
				nil, // l1InfoTreeDataQuerier
				optimisticSigner,
				lerQuery,
				nil, // gerQuerier
				nil, // bridgeQuerier
				nil, // bridgeL2SovereignReader
			)

			proof, err := query.generateOptimisticAggchainProof(ctx, tc.buildParams, tc.request)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedProof, proof)
			}
		})
	}
}

//nolint:duplicate
func TestGenerateAggchainProof(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name   string
		mockFn func(
			*mocks.AggchainProofClientInterface,
			*mocks.L1InfoTreeDataQuerier,
			*mocks.GERQuerier,
			*mocks.BridgeQuerier)
		lastProvenBlock uint64
		buildParams     *types.CertificateBuildParams
		expectedProof   *types.AggchainProof
		expectedRoot    *treetypes.Root
		expectedError   string
	}{
		{
			name:            "error getting finalized L1 info tree data",
			lastProvenBlock: 100,
			buildParams:     &types.CertificateBuildParams{},
			mockFn: func(aggchainProofClient *mocks.AggchainProofClientInterface,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier,
				bridgeQuerier *mocks.BridgeQuerier) {
				l1InfoTreeDataQuerier.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(treetypes.Proof{}, nil, nil, errors.New("some error"))
			},
			expectedError: "aggchainProverFlow - error getting finalized L1 Info tree data: some error",
		},
		{
			name:            "error checking claims in finalized L1 info tree root",
			lastProvenBlock: 100,
			buildParams:     &types.CertificateBuildParams{Claims: []bridgesync.Claim{{}}},
			mockFn: func(aggchainProofClient *mocks.AggchainProofClientInterface,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier,
				bridgeQuerier *mocks.BridgeQuerier) {
				l1InfoTreeDataQuerier.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{},
					&treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1},
					nil)
				l1InfoTreeDataQuerier.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(
					&treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1},
					[]bridgesync.Claim{{}},
				).Return(errors.New("some error"))
			},
			expectedError: "aggchainProverFlow - error checking if claims are part of finalized L1 Info tree root",
		},
		{
			name:            "error getting injected GERs",
			lastProvenBlock: 100,
			buildParams:     &types.CertificateBuildParams{ToBlock: 200, Claims: []bridgesync.Claim{{}}},
			mockFn: func(aggchainProofClient *mocks.AggchainProofClientInterface,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier,
				bridgeQuerier *mocks.BridgeQuerier) {
				l1InfoTreeDataQuerier.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{},
					&treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1},
					nil)
				l1InfoTreeDataQuerier.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(
					&treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1},
					[]bridgesync.Claim{{}},
				).Return(nil)
				gerQuerier.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1}, uint64(101), uint64(200)).Return(nil, errors.New("some error"))
			},
			expectedError: "aggchainProverFlow - error getting injected GERs proofs: some error",
		},
		{
			name:            "error getting aggchain proof",
			lastProvenBlock: 100,
			buildParams:     &types.CertificateBuildParams{ToBlock: 200, Claims: []bridgesync.Claim{{GlobalIndex: big.NewInt(1)}}},
			mockFn: func(aggchainProofClient *mocks.AggchainProofClientInterface,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier,
				bridgeQuerier *mocks.BridgeQuerier) {
				l1InfoTreeDataQuerier.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{},
					&treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1},
					nil)
				l1InfoTreeDataQuerier.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(
					&treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1},
					[]bridgesync.Claim{{GlobalIndex: big.NewInt(1)}},
				).Return(nil)
				gerQuerier.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1}, uint64(101), uint64(200)).Return(nil, nil)
				gerQuerier.EXPECT().GetRemovedGERsBlockDetails(ctx, uint64(101), uint64(200)).Return(nil, nil)
				bridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(101), uint64(200)).Return(nil, nil)
				aggchainProofClient.EXPECT().GenerateAggchainProof(ctx, mock.Anything).
					Return(nil, errors.New("aggchain proof error"))
			},
			expectedError: "aggchainProverFlow - error fetching aggchain proof",
		},
		{
			name:            "success",
			lastProvenBlock: 100,
			buildParams:     &types.CertificateBuildParams{ToBlock: 200, Claims: []bridgesync.Claim{{GlobalIndex: big.NewInt(1)}}},
			mockFn: func(aggchainProofClient *mocks.AggchainProofClientInterface,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier,
				bridgeQuerier *mocks.BridgeQuerier) {
				l1InfoTreeDataQuerier.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{},
					&treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1},
					nil)
				l1InfoTreeDataQuerier.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(
					&treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1},
					[]bridgesync.Claim{{GlobalIndex: big.NewInt(1)}},
				).Return(nil)
				gerQuerier.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1}, uint64(101), uint64(200)).Return(nil, nil)
				gerQuerier.EXPECT().GetRemovedGERsBlockDetails(ctx, uint64(101), uint64(200)).Return(nil, nil)
				bridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(101), uint64(200)).Return(nil, nil)
				aggchainProofClient.EXPECT().GenerateAggchainProof(ctx, mock.Anything).
					Return(&types.AggchainProof{
						LastProvenBlock: 100,
						EndBlock:        200,
						SP1StarkProof:   &types.SP1StarkProof{},
					}, nil)
			},
			expectedRoot: &treetypes.Root{
				Hash:  common.HexToHash("0x123"),
				Index: 1,
			},
			expectedProof: &types.AggchainProof{
				LastProvenBlock: 100,
				EndBlock:        200,
				SP1StarkProof:   &types.SP1StarkProof{},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			aggchainProofClient := mocks.NewAggchainProofClientInterface(t)
			l1InfoTreeDataQuerier := mocks.NewL1InfoTreeDataQuerier(t)
			gerQuerier := mocks.NewGERQuerier(t)
			bridgeQuerier := mocks.NewBridgeQuerier(t)
			if tc.mockFn != nil {
				tc.mockFn(aggchainProofClient, l1InfoTreeDataQuerier, gerQuerier, bridgeQuerier)
			}

			log := log.WithFields("aggchain_proof_query", "TestGenerateAggchainProof")
			query := NewAggchainProofQuery(
				log,
				aggchainProofClient,
				l1InfoTreeDataQuerier,
				nil, // optimisticSigner
				nil, // lerQuerier
				gerQuerier,
				bridgeQuerier,
				nil, // bridgeL2SovereignReader
			)

			proof, root, err := query.GenerateAggchainProof(ctx, tc.lastProvenBlock, tc.buildParams.ToBlock, tc.buildParams)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedProof, proof)
				require.Equal(t, tc.expectedRoot, root)
			}
		})
	}
}

func TestFilterClaimsWithUnsetClaims(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		claims         []bridgesync.Claim
		unsetClaims    []*bridgesynctypes.Unclaim
		expectedClaims []bridgesync.Claim
		description    string
	}{
		{
			name: "no unset claims - all claims should remain",
			claims: []bridgesync.Claim{
				{
					BlockNum:    100,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1),
				},
				{
					BlockNum:    101,
					BlockPos:    0,
					GlobalIndex: big.NewInt(2),
				},
			},
			unsetClaims: []*bridgesynctypes.Unclaim{},
			expectedClaims: []bridgesync.Claim{
				{
					BlockNum:    100,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1),
				},
				{
					BlockNum:    101,
					BlockPos:    0,
					GlobalIndex: big.NewInt(2),
				},
			},
			description: "When there are no unset claims, all claims should remain unfiltered",
		},
		{
			name: "claim with unset claim higher block number - should be filtered out",
			claims: []bridgesync.Claim{
				{
					BlockNum:    100,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1),
				},
			},
			unsetClaims: []*bridgesynctypes.Unclaim{
				{
					GlobalIndex: big.NewInt(1),
					BlockNumber: 200,
					BlockIndex:  0,
				},
			},
			expectedClaims: []bridgesync.Claim{},
			description:    "When a claim has a corresponding unset claim with higher block number, it should be filtered out",
		},
		{
			name: "claim with unset claim same block but higher position - should be filtered out",
			claims: []bridgesync.Claim{
				{
					BlockNum:    100,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1),
				},
			},
			unsetClaims: []*bridgesynctypes.Unclaim{
				{
					GlobalIndex: big.NewInt(1),
					BlockNumber: 100,
					BlockIndex:  1,
				},
			},
			expectedClaims: []bridgesync.Claim{},
			description:    "When a claim has a corresponding unset claim with same block number but higher position, it should be filtered out",
		},
		{
			name: "claim with unset claim lower block number - should remain",
			claims: []bridgesync.Claim{
				{
					BlockNum:    200,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1),
				},
			},
			unsetClaims: []*bridgesynctypes.Unclaim{
				{
					GlobalIndex: big.NewInt(1),
					BlockNumber: 100,
					BlockIndex:  0,
				},
			},
			expectedClaims: []bridgesync.Claim{
				{
					BlockNum:    200,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1),
				},
			},
			description: "When a claim has a corresponding unset claim with lower block number, it should remain (unset claim is from previous certificate)",
		},
		{
			name: "claim with unset claim same block same position - should remain",
			claims: []bridgesync.Claim{
				{
					BlockNum:    100,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1),
				},
			},
			unsetClaims: []*bridgesynctypes.Unclaim{
				{
					GlobalIndex: big.NewInt(1),
					BlockNumber: 100,
					BlockIndex:  0,
				},
			},
			expectedClaims: []bridgesync.Claim{
				{
					BlockNum:    100,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1),
				},
			},
			description: "When a claim has a corresponding unset claim with same block number and position, it should remain (not considered unset)",
		},
		{
			name: "multiple claims with mixed scenarios",
			claims: []bridgesync.Claim{
				{
					BlockNum:    100,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1),
				},
				{
					BlockNum:    101,
					BlockPos:    0,
					GlobalIndex: big.NewInt(2),
				},
				{
					BlockNum:    102,
					BlockPos:    0,
					GlobalIndex: big.NewInt(3),
				},
				{
					BlockNum:    103,
					BlockPos:    0,
					GlobalIndex: big.NewInt(4),
				},
			},
			unsetClaims: []*bridgesynctypes.Unclaim{
				{
					GlobalIndex: big.NewInt(1),
					BlockNumber: 200,
					BlockIndex:  0,
				},
				{
					GlobalIndex: big.NewInt(3),
					BlockNumber: 102,
					BlockIndex:  1,
				},
			},
			expectedClaims: []bridgesync.Claim{
				{
					BlockNum:    101,
					BlockPos:    0,
					GlobalIndex: big.NewInt(2),
				},
				{
					BlockNum:    103,
					BlockPos:    0,
					GlobalIndex: big.NewInt(4),
				},
			},
			description: "Claims 1 and 3 should be filtered out (have unset claims), claims 2 and 4 should remain",
		},
		{
			name: "claim re-claimed after being unset - should remain",
			claims: []bridgesync.Claim{
				{
					BlockNum:    100,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1),
				},
				{
					BlockNum:    200,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1), // Same global index, re-claimed
				},
			},
			unsetClaims: []*bridgesynctypes.Unclaim{
				{
					GlobalIndex: big.NewInt(1),
					BlockNumber: 150,
					BlockIndex:  0,
				},
			},
			expectedClaims: []bridgesync.Claim{
				{
					BlockNum:    200,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1), // Re-claimed claim should remain
				},
			},
			description: "When a claim is re-claimed after being unset, the re-claimed version should remain",
		},
		{
			name: "complex scenario with multiple global indices",
			claims: []bridgesync.Claim{
				// Global index 1: claimed at block 100, unset at block 150, re-claimed at block 200
				{
					BlockNum:    100,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1),
				},
				{
					BlockNum:    200,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1),
				},
				// Global index 2: claimed at block 101, unset at block 160
				{
					BlockNum:    101,
					BlockPos:    0,
					GlobalIndex: big.NewInt(2),
				},
				// Global index 3: claimed at block 102, no unset claim
				{
					BlockNum:    102,
					BlockPos:    0,
					GlobalIndex: big.NewInt(3),
				},
				// Global index 4: claimed at block 103, unset at block 103 but higher position
				{
					BlockNum:    103,
					BlockPos:    0,
					GlobalIndex: big.NewInt(4),
				},
			},
			unsetClaims: []*bridgesynctypes.Unclaim{
				{
					GlobalIndex: big.NewInt(1),
					BlockNumber: 150,
					BlockIndex:  0,
				},
				{
					GlobalIndex: big.NewInt(2),
					BlockNumber: 160,
					BlockIndex:  0,
				},
				{
					GlobalIndex: big.NewInt(4),
					BlockNumber: 103,
					BlockIndex:  1,
				},
			},
			expectedClaims: []bridgesync.Claim{
				{
					BlockNum:    200,
					BlockPos:    0,
					GlobalIndex: big.NewInt(1), // Re-claimed after being unset
				},
				{
					BlockNum:    102,
					BlockPos:    0,
					GlobalIndex: big.NewInt(3), // No unset claim
				},
			},
			description: "Complex scenario testing multiple global indices with various unset claim patterns",
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log := log.WithFields("aggchain_proof_query", "TestFilterClaimsWithUnsetClaims")
			query := &aggchainProofQuery{
				log: log,
			}

			filteredClaims := query.filterClaimsWithUnsetClaims(tc.claims, tc.unsetClaims)

			// Verify the number of claims matches expected
			require.Equal(t, len(tc.expectedClaims), len(filteredClaims),
				"Expected %d claims, got %d. %s", len(tc.expectedClaims), len(filteredClaims), tc.description)

			// Verify each expected claim is present in filtered claims
			for _, expectedClaim := range tc.expectedClaims {
				found := false
				for _, filteredClaim := range filteredClaims {
					if filteredClaim.BlockNum == expectedClaim.BlockNum &&
						filteredClaim.BlockPos == expectedClaim.BlockPos &&
						filteredClaim.GlobalIndex.Cmp(expectedClaim.GlobalIndex) == 0 {
						found = true
						break
					}
				}
				require.True(t, found, "Expected claim not found in filtered claims: BlockNum=%d, BlockPos=%d, GlobalIndex=%s. %s",
					expectedClaim.BlockNum, expectedClaim.BlockPos, expectedClaim.GlobalIndex, tc.description)
			}
		})
	}
}
