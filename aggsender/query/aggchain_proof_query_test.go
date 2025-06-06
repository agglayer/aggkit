package query

import (
	"context"
	"errors"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/converters"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
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
			name: "error getting imported bridge exits",
			claims: []bridgesync.Claim{
				{
					IsMessage:          false,
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					GlobalIndex:        new(big.Int).SetBytes([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}),
				},
			},
			expectedError: "aggchainProverFlow - error converting claim to imported bridge exit",
		},
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
				log:                         log,
				importedBridgeExitConverter: converters.NewImportedBridgeExitConverter(log, nil),
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
				nil, // importedBridgeExitConverter
				nil, // l1InfoTreeDataQuerier
				optimisticSigner,
				lerQuery,
				nil, // gerQuerier
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
			*mocks.ImportedBridgeExitConverter,
			*mocks.L1InfoTreeDataQuerier,
			*mocks.GERQuerier)
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
				importedBridgeExitsConverter *mocks.ImportedBridgeExitConverter,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier) {
				l1InfoTreeDataQuerier.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(treetypes.Proof{}, nil, nil, errors.New("some error"))
			},
			expectedError: "aggchainProverFlow - error getting finalized L1 Info tree data: some error",
		},
		{
			name:            "error checking claims in finalized L1 info tree root",
			lastProvenBlock: 100,
			buildParams:     &types.CertificateBuildParams{Claims: []bridgesync.Claim{{}}},
			mockFn: func(aggchainProofClient *mocks.AggchainProofClientInterface,
				importedBridgeExitsConverter *mocks.ImportedBridgeExitConverter,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier) {
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
				importedBridgeExitsConverter *mocks.ImportedBridgeExitConverter,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier) {
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
			name:            "error getting converting imported bridge exits",
			lastProvenBlock: 100,
			buildParams:     &types.CertificateBuildParams{ToBlock: 200, Claims: []bridgesync.Claim{{}}},
			mockFn: func(aggchainProofClient *mocks.AggchainProofClientInterface,
				importedBridgeExitsConverter *mocks.ImportedBridgeExitConverter,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier) {
				l1InfoTreeDataQuerier.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{},
					&treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1},
					nil)
				l1InfoTreeDataQuerier.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(
					&treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1},
					[]bridgesync.Claim{{}},
				).Return(nil)
				gerQuerier.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1}, uint64(101), uint64(200)).Return(nil, nil)
				importedBridgeExitsConverter.EXPECT().ConvertToImportedBridgeExitWithoutClaimData(mock.Anything).Return(nil, errors.New("some error"))
			},
			expectedError: "aggchainProverFlow - error getting imported bridge exits for prover",
		},
		{
			name:            "error getting aggchain proof",
			lastProvenBlock: 100,
			buildParams:     &types.CertificateBuildParams{ToBlock: 200, Claims: []bridgesync.Claim{{}}},
			mockFn: func(aggchainProofClient *mocks.AggchainProofClientInterface,
				importedBridgeExitsConverter *mocks.ImportedBridgeExitConverter,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier) {
				l1InfoTreeDataQuerier.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{},
					&treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1},
					nil)
				l1InfoTreeDataQuerier.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(
					&treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1},
					[]bridgesync.Claim{{}},
				).Return(nil)
				gerQuerier.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1}, uint64(101), uint64(200)).Return(nil, nil)
				importedBridgeExitsConverter.EXPECT().ConvertToImportedBridgeExitWithoutClaimData(mock.Anything).Return(nil, nil)
				aggchainProofClient.EXPECT().GenerateAggchainProof(mock.Anything).
					Return(nil, errors.New("aggchain proof error"))
			},
			expectedError: "aggchainProverFlow - error fetching aggchain proof",
		},
		{
			name:            "success",
			lastProvenBlock: 100,
			buildParams:     &types.CertificateBuildParams{ToBlock: 200, Claims: []bridgesync.Claim{{}}},
			mockFn: func(aggchainProofClient *mocks.AggchainProofClientInterface,
				importedBridgeExitsConverter *mocks.ImportedBridgeExitConverter,
				l1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier,
				gerQuerier *mocks.GERQuerier) {
				l1InfoTreeDataQuerier.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{},
					&treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1},
					nil)
				l1InfoTreeDataQuerier.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(
					&treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1},
					[]bridgesync.Claim{{}},
				).Return(nil)
				gerQuerier.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{Hash: common.HexToHash("0x123"), Index: 1}, uint64(101), uint64(200)).Return(nil, nil)
				importedBridgeExitsConverter.EXPECT().ConvertToImportedBridgeExitWithoutClaimData(mock.Anything).Return(nil, nil)
				aggchainProofClient.EXPECT().GenerateAggchainProof(mock.Anything).
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
			importedBridgeExitsConverter := mocks.NewImportedBridgeExitConverter(t)
			gerQuerier := mocks.NewGERQuerier(t)
			if tc.mockFn != nil {
				tc.mockFn(aggchainProofClient, importedBridgeExitsConverter, l1InfoTreeDataQuerier, gerQuerier)
			}

			log := log.WithFields("aggchain_proof_query", "TestGenerateAggchainProof")
			query := NewAggchainProofQuery(
				log,
				aggchainProofClient,
				importedBridgeExitsConverter, // importedBridgeExitConverter
				l1InfoTreeDataQuerier,
				nil, // optimisticSigner
				nil, // lerQuerier
				gerQuerier,
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
