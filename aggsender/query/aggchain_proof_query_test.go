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
		expectedError   string
	}{
		{
			name:            "error getting finalized L1 info tree data",
			lastProvenBlock: 100,
			buildParams:     &types.CertificateBuildParams{L1InfoTreeLeafCount: 1, L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1")},
			mockFn: func(aggchainProofClient *mocks.AggchainProofClientInterface,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier,
				bridgeQuerier *mocks.BridgeQuerier) {
				l1InfoTreeDataQuerier.EXPECT().GetFinalizedL1InfoTreeData(ctx, common.HexToHash("0x1"), uint32(1)).Return(treetypes.Proof{}, nil, errors.New("some error"))
			},
			expectedError: "aggchainProverFlow - error getting finalized L1 Info tree data: some error",
		},
		{
			name:            "error getting injected GERs",
			lastProvenBlock: 100,
			buildParams:     &types.CertificateBuildParams{L1InfoTreeLeafCount: 2, L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"), ToBlock: 200, Claims: []bridgesync.Claim{{}}},
			mockFn: func(aggchainProofClient *mocks.AggchainProofClientInterface,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier,
				bridgeQuerier *mocks.BridgeQuerier) {
				l1InfoTreeDataQuerier.EXPECT().GetFinalizedL1InfoTreeData(ctx, common.HexToHash("0x1"), uint32(2)).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{},
					nil)
				gerQuerier.EXPECT().GetInjectedGERsProofs(ctx, common.HexToHash("0x1"), uint64(101), uint64(200)).Return(nil, errors.New("some error"))
			},
			expectedError: "aggchainProverFlow - error getting injected GERs proofs: some error",
		},
		{
			name:            "error getting aggchain proof",
			lastProvenBlock: 100,
			buildParams:     &types.CertificateBuildParams{L1InfoTreeLeafCount: 2, L1InfoTreeRootFromWhichToProve: common.HexToHash("0x123"), ToBlock: 200, Claims: []bridgesync.Claim{{GlobalIndex: big.NewInt(1)}}},
			mockFn: func(aggchainProofClient *mocks.AggchainProofClientInterface,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier,
				bridgeQuerier *mocks.BridgeQuerier) {
				l1InfoTreeDataQuerier.EXPECT().GetFinalizedL1InfoTreeData(ctx, common.HexToHash("0x123"), uint32(2)).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{},
					nil)
				gerQuerier.EXPECT().GetInjectedGERsProofs(ctx, common.HexToHash("0x123"), uint64(101), uint64(200)).Return(nil, nil)
				gerQuerier.EXPECT().GetRemovedGERsForRange(ctx, uint64(101), uint64(200)).Return(nil, nil)
				aggchainProofClient.EXPECT().GenerateAggchainProof(ctx, mock.Anything).
					Return(nil, errors.New("aggchain proof error"))
			},
			expectedError: "aggchainProverFlow - error fetching aggchain proof",
		},
		{
			name:            "success",
			lastProvenBlock: 100,
			buildParams:     &types.CertificateBuildParams{L1InfoTreeLeafCount: 2, L1InfoTreeRootFromWhichToProve: common.HexToHash("0x123"), ToBlock: 200, Claims: []bridgesync.Claim{{GlobalIndex: big.NewInt(1)}}},
			mockFn: func(aggchainProofClient *mocks.AggchainProofClientInterface,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier,
				bridgeQuerier *mocks.BridgeQuerier) {
				l1InfoTreeDataQuerier.EXPECT().GetFinalizedL1InfoTreeData(ctx, common.HexToHash("0x123"), uint32(2)).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{},
					nil)
				gerQuerier.EXPECT().GetInjectedGERsProofs(ctx, common.HexToHash("0x123"), uint64(101), uint64(200)).Return(nil, nil)
				gerQuerier.EXPECT().GetRemovedGERsForRange(ctx, uint64(101), uint64(200)).Return(nil, nil)
				aggchainProofClient.EXPECT().GenerateAggchainProof(ctx, mock.Anything).
					Return(&types.AggchainProof{
						LastProvenBlock: 100,
						EndBlock:        200,
						SP1StarkProof:   &types.SP1StarkProof{},
					}, nil)
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
			)

			proof, err := query.GenerateAggchainProof(ctx, tc.lastProvenBlock, tc.buildParams.ToBlock, tc.buildParams)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedProof, proof)
			}
		})
	}
}

func TestConvertUnclaimsToAgglayerUnclaims(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		unclaims         []bridgesynctypes.Unclaim
		expectedUnclaims []*agglayertypes.Unclaim
		expectedError    string
	}{
		{
			name:             "empty map",
			unclaims:         []bridgesynctypes.Unclaim{},
			expectedUnclaims: []*agglayertypes.Unclaim{},
		},
		{
			name: "single unclaim with mainnet flag true",
			unclaims: []bridgesynctypes.Unclaim{
				{
					GlobalIndex: bridgesync.GenerateGlobalIndex(true, 0, 5),
					BlockNumber: 100,
					LogIndex:    2,
				},
			},
			expectedUnclaims: []*agglayertypes.Unclaim{
				{
					GlobalIndex: &agglayertypes.GlobalIndex{
						MainnetFlag: true,
						RollupIndex: 0,
						LeafIndex:   5,
					},
					BlockNumber: 100,
					LogIndex:    2,
				},
			},
		},
		{
			name: "single unclaim with mainnet flag false and rollup index",
			unclaims: []bridgesynctypes.Unclaim{
				{
					GlobalIndex: bridgesync.GenerateGlobalIndex(false, 3, 7),
					BlockNumber: 200,
					LogIndex:    1,
				},
			},
			expectedUnclaims: []*agglayertypes.Unclaim{
				{
					GlobalIndex: &agglayertypes.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 3,
						LeafIndex:   7,
					},
					BlockNumber: 200,
					LogIndex:    1,
				},
			},
		},
		{
			name: "multiple unclaims with different configurations",
			unclaims: []bridgesynctypes.Unclaim{
				{
					GlobalIndex: bridgesync.GenerateGlobalIndex(true, 0, 1),
					BlockNumber: 100,
					LogIndex:    0,
				},
				{
					GlobalIndex: bridgesync.GenerateGlobalIndex(false, 5, 10),
					BlockNumber: 150,
					LogIndex:    3,
				},
				{
					GlobalIndex: bridgesync.GenerateGlobalIndex(false, 0, 0),
					BlockNumber: 200,
					LogIndex:    1,
				},
			},
			expectedUnclaims: []*agglayertypes.Unclaim{
				{
					GlobalIndex: &agglayertypes.GlobalIndex{
						MainnetFlag: true,
						RollupIndex: 0,
						LeafIndex:   1,
					},
					BlockNumber: 100,
					LogIndex:    0,
				},
				{
					GlobalIndex: &agglayertypes.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 5,
						LeafIndex:   10,
					},
					BlockNumber: 150,
					LogIndex:    3,
				},
				{
					GlobalIndex: &agglayertypes.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 0,
						LeafIndex:   0,
					},
					BlockNumber: 200,
					LogIndex:    1,
				},
			},
		},
		{
			name: "unclaim with zero global index",
			unclaims: []bridgesynctypes.Unclaim{
				{
					GlobalIndex: big.NewInt(0),
					BlockNumber: 100,
					LogIndex:    0,
				},
			},
			expectedUnclaims: []*agglayertypes.Unclaim{
				{
					GlobalIndex: &agglayertypes.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 0,
						LeafIndex:   0,
					},
					BlockNumber: 100,
					LogIndex:    0,
				},
			},
		},
		{
			name: "unclaim with large values",
			unclaims: []bridgesynctypes.Unclaim{
				{
					GlobalIndex: bridgesync.GenerateGlobalIndex(false, 4294967295, 4294967295), // max uint32 values
					BlockNumber: 999999,
					LogIndex:    65535,
				},
			},
			expectedUnclaims: []*agglayertypes.Unclaim{
				{
					GlobalIndex: &agglayertypes.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 4294967295,
						LeafIndex:   4294967295,
					},
					BlockNumber: 999999,
					LogIndex:    65535,
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log := log.WithFields("aggchain_proof_query", "TestConvertUnclaimsToAgglayerUnclaims")
			query := &aggchainProofQuery{
				log: log,
			}

			unclaims, err := query.convertUnclaimsToAgglayerUnclaims(tc.unclaims)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
				require.Nil(t, unclaims)
			} else {
				require.NoError(t, err)
				require.Len(t, unclaims, len(tc.expectedUnclaims))

				// Sort both slices by BlockNumber for consistent comparison
				// since map iteration order is not guaranteed
				sortUnclaimsByBlockNumber(t, unclaims)
				sortUnclaimsByBlockNumber(t, tc.expectedUnclaims)

				for i, expected := range tc.expectedUnclaims {
					require.Equal(t, expected.GlobalIndex.MainnetFlag, unclaims[i].GlobalIndex.MainnetFlag)
					require.Equal(t, expected.GlobalIndex.RollupIndex, unclaims[i].GlobalIndex.RollupIndex)
					require.Equal(t, expected.GlobalIndex.LeafIndex, unclaims[i].GlobalIndex.LeafIndex)
					require.Equal(t, expected.BlockNumber, unclaims[i].BlockNumber)
					require.Equal(t, expected.LogIndex, unclaims[i].LogIndex)
				}
			}
		})
	}
}

// Helper function to sort unclaims by BlockNumber for consistent comparison
func sortUnclaimsByBlockNumber(t *testing.T, unclaims []*agglayertypes.Unclaim) {
	t.Helper()

	for i := 0; i < len(unclaims); i++ {
		for j := i + 1; j < len(unclaims); j++ {
			if unclaims[i].BlockNumber > unclaims[j].BlockNumber {
				unclaims[i], unclaims[j] = unclaims[j], unclaims[i]
			}
		}
	}
}
