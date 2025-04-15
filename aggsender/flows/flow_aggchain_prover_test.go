package flows

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggoracle/chaingerreader"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_AggchainProverFlow_GetCertificateBuildParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	finalizedL1Root := common.HexToHash("0x1")

	ibe1 := &agglayertypes.ImportedBridgeExit{
		BridgeExit: &agglayertypes.BridgeExit{
			LeafType:  0,
			TokenInfo: &agglayertypes.TokenInfo{},
		},
		GlobalIndex: &agglayertypes.GlobalIndex{
			LeafIndex: 1,
		},
	}

	ibe2 := &agglayertypes.ImportedBridgeExit{
		BridgeExit: &agglayertypes.BridgeExit{
			LeafType:  0,
			TokenInfo: &agglayertypes.TokenInfo{},
		},
		GlobalIndex: &agglayertypes.GlobalIndex{
			LeafIndex: 2,
		},
	}

	testCases := []struct {
		name   string
		mockFn func(*mocks.AggSenderStorage,
			*mocks.L2BridgeSyncer,
			*mocks.AggchainProofClientInterface,
			*mocks.L1InfoTreeDataQuerier,
			*mocks.ChainGERReader,
		)
		expectedParams *types.CertificateBuildParams
		expectedError  string
	}{
		{
			name: "error getting last sent certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier,
				mockChainGERReader *mocks.ChainGERReader) {
				mockStorage.EXPECT().GetLastSentCertificate().Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name: "resend InError certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier,
				mockChainGERReader *mocks.ChainGERReader) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := calculateGER(mer, rer)
				mockStorage.EXPECT().GetLastSentCertificate().Return(&types.CertificateInfo{
					FromBlock:               1,
					ToBlock:                 10,
					Status:                  agglayertypes.InError,
					FinalizedL1InfoTreeRoot: &finalizedL1Root,
					AggchainProof: &types.AggchainProof{
						SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
						LastProvenBlock: 1,
						EndBlock:        10,
					},
				}, nil)
				mockL2Syncer.EXPECT().GetBridgesPublished(ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.EXPECT().GetClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{
					{
						GlobalIndex:     big.NewInt(1),
						GlobalExitRoot:  ger,
						MainnetExitRoot: mer,
						RollupExitRoot:  rer,
					}}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:  1,
				ToBlock:    10,
				RetryCount: 1,
				Bridges:    []bridgesync.Bridge{{}},
				Claims: []bridgesync.Claim{{
					GlobalIndex:     big.NewInt(1),
					RollupExitRoot:  common.HexToHash("0x1"),
					MainnetExitRoot: common.HexToHash("0x2"),
					GlobalExitRoot:  calculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
				}},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				AggchainProof: &types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
					LastProvenBlock: 1,
					EndBlock:        10,
				},
				LastSentCertificate: &types.CertificateInfo{
					FromBlock:               1,
					ToBlock:                 10,
					Status:                  agglayertypes.InError,
					FinalizedL1InfoTreeRoot: &finalizedL1Root,
					AggchainProof: &types.AggchainProof{
						SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
						LastProvenBlock: 1,
						EndBlock:        10,
					},
				},
			},
		},
		{
			name: "error fetching aggchain proof for new certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier,
				mockChainGERReader *mocks.ChainGERReader) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := calculateGER(mer, rer)
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockStorage.EXPECT().GetLastSentCertificate().Return(nil, nil).Twice()
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2Syncer.EXPECT().GetBridgesPublished(ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.EXPECT().GetClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{
					{
						GlobalIndex:     big.NewInt(1),
						GlobalExitRoot:  ger,
						MainnetExitRoot: mer,
						RollupExitRoot:  rer,
					}}, nil)
				mockL1InfoDataQuery.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					&treetypes.Root{
						Hash:  common.HexToHash("0x1"),
						Index: 10,
					},
					nil,
				)
				mockL1InfoDataQuery.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(mock.Anything, mock.Anything).Return(nil)
				mockChainGERReader.EXPECT().GetInjectedGERsForRange(ctx, uint64(1), uint64(10)).Return(map[common.Hash]chaingerreader.InjectedGER{}, nil)
				mockProverClient.EXPECT().GenerateAggchainProof(uint64(0), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: treetypes.Proof{},
					}, make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, 0),
					[]*agglayertypes.ImportedBridgeExitWithBlockNumber{{ImportedBridgeExit: ibe1}}).Return(nil, errors.New("some error"))
			},
			expectedError: "error fetching aggchain proof for lastProvenBlock: 0, maxEndBlock: 10: some error",
		},
		{
			name: "success fetching aggchain proof for new certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier,
				mockChainGERReader *mocks.ChainGERReader) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := calculateGER(mer, rer)
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockStorage.EXPECT().GetLastSentCertificate().Return(&types.CertificateInfo{ToBlock: 5}, nil).Twice()
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2Syncer.EXPECT().GetBridgesPublished(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.EXPECT().GetClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Claim{{
					GlobalIndex:     big.NewInt(1),
					GlobalExitRoot:  ger,
					MainnetExitRoot: mer,
					RollupExitRoot:  rer,
				}}, nil)
				mockL1InfoDataQuery.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					&treetypes.Root{
						Hash:  common.HexToHash("0x1"),
						Index: 10,
					},
					nil,
				)
				mockL1InfoDataQuery.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(mock.Anything, mock.Anything).Return(nil)
				mockChainGERReader.EXPECT().GetInjectedGERsForRange(ctx, uint64(6), uint64(10)).Return(map[common.Hash]chaingerreader.InjectedGER{}, nil)
				mockProverClient.EXPECT().GenerateAggchainProof(uint64(5), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: treetypes.Proof{},
					}, make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, 0),
					[]*agglayertypes.ImportedBridgeExitWithBlockNumber{{ImportedBridgeExit: ibe1}}).Return(&types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{Proof: []byte("some-proof")}, LastProvenBlock: 6, EndBlock: 10}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:           6,
				ToBlock:             10,
				RetryCount:          0,
				LastSentCertificate: &types.CertificateInfo{ToBlock: 5},
				Bridges:             []bridgesync.Bridge{{}},
				Claims: []bridgesync.Claim{{
					GlobalIndex:     big.NewInt(1),
					RollupExitRoot:  common.HexToHash("0x1"),
					MainnetExitRoot: common.HexToHash("0x2"),
					GlobalExitRoot:  calculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
				}},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				AggchainProof: &types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
					LastProvenBlock: 6,
					EndBlock:        10,
				},
				CreatedAt: uint32(time.Now().UTC().Unix()),
			},
		},
		{
			name: "success fetching aggchain proof for new certificate - aggchain prover returns smaller range",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier,
				mockChainGERReader *mocks.ChainGERReader) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := calculateGER(mer, rer)
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockStorage.EXPECT().GetLastSentCertificate().Return(&types.CertificateInfo{ToBlock: 5}, nil).Twice()
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2Syncer.EXPECT().GetBridgesPublished(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{
					{BlockNum: 6}, {BlockNum: 10}}, nil)
				mockL2Syncer.EXPECT().GetClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Claim{
					{BlockNum: 8, GlobalIndex: big.NewInt(1), GlobalExitRoot: ger, MainnetExitRoot: mer, RollupExitRoot: rer},
					{BlockNum: 9, GlobalIndex: big.NewInt(2), GlobalExitRoot: ger, MainnetExitRoot: mer, RollupExitRoot: rer}}, nil)
				mockL1InfoDataQuery.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					&treetypes.Root{
						Hash:  common.HexToHash("0x1"),
						Index: 10,
					},
					nil,
				)
				mockL1InfoDataQuery.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(mock.Anything, mock.Anything).Return(nil)
				mockChainGERReader.EXPECT().GetInjectedGERsForRange(ctx, uint64(6), uint64(10)).Return(map[common.Hash]chaingerreader.InjectedGER{}, nil)
				mockProverClient.EXPECT().GenerateAggchainProof(uint64(5), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: treetypes.Proof{},
					}, make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, 0),
					[]*agglayertypes.ImportedBridgeExitWithBlockNumber{
						{ImportedBridgeExit: ibe1, BlockNumber: 8},
						{ImportedBridgeExit: ibe2, BlockNumber: 9},
					}).Return(&types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{Proof: []byte("some-proof")}, LastProvenBlock: 6, EndBlock: 8}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:           6,
				ToBlock:             8,
				RetryCount:          0,
				LastSentCertificate: &types.CertificateInfo{ToBlock: 5},
				Bridges:             []bridgesync.Bridge{{BlockNum: 6}},
				Claims: []bridgesync.Claim{{
					BlockNum:        8,
					GlobalIndex:     big.NewInt(1),
					RollupExitRoot:  common.HexToHash("0x1"),
					MainnetExitRoot: common.HexToHash("0x2"),
					GlobalExitRoot:  calculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
				}},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				AggchainProof: &types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
					LastProvenBlock: 6,
					EndBlock:        8,
				},
				CreatedAt: uint32(time.Now().UTC().Unix()),
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockAggchainProofClient := mocks.NewAggchainProofClientInterface(t)
			mockStorage := mocks.NewAggSenderStorage(t)
			mockL2Syncer := mocks.NewL2BridgeSyncer(t)
			mockChainGERReader := mocks.NewChainGERReader(t)
			mockL1InfoTreeDataQuerier := mocks.NewL1InfoTreeDataQuerier(t)
			aggchainFlow := &AggchainProverFlow{
				gerReader:           mockChainGERReader,
				aggchainProofClient: mockAggchainProofClient,
				baseFlow: &baseFlow{
					l1InfoTreeDataQuerier: mockL1InfoTreeDataQuerier,
					l2Syncer:              mockL2Syncer,
					storage:               mockStorage,
					log:                   log.WithFields("flowManager", "Test_AggchainProverFlow_GetCertificateBuildParams"),
				},
			}

			tc.mockFn(mockStorage, mockL2Syncer, mockAggchainProofClient, mockL1InfoTreeDataQuerier, mockChainGERReader)

			params, err := aggchainFlow.GetCertificateBuildParams(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedParams, params)
			}

			mockStorage.AssertExpectations(t)
			mockL2Syncer.AssertExpectations(t)
			mockL1InfoTreeDataQuerier.AssertExpectations(t)
			mockL1InfoTreeDataQuerier.AssertExpectations(t)
			mockAggchainProofClient.AssertExpectations(t)
		})
	}
}

func Test_AggchainProverFlow_GetInjectedGERsProofs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name           string
		mockFn         func(*mocks.ChainGERReader, *mocks.L1InfoTreeDataQuerier)
		expectedProofs map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber
		expectedError  string
	}{
		{
			name: "error getting injected GERs for range",
			mockFn: func(mockChainGERReader *mocks.ChainGERReader, mockL1InfoTreeQuery *mocks.L1InfoTreeDataQuerier) {
				mockChainGERReader.EXPECT().GetInjectedGERsForRange(ctx, uint64(1), uint64(10)).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting injected GERs for range 1 : 10: some error",
		},
		{
			name: "error getting proof for GER",
			mockFn: func(mockChainGERReader *mocks.ChainGERReader, mockL1InfoTreeQuery *mocks.L1InfoTreeDataQuerier) {
				mockChainGERReader.EXPECT().GetInjectedGERsForRange(ctx, uint64(1), uint64(10)).Return(map[common.Hash]chaingerreader.InjectedGER{
					common.HexToHash("0x1"): {GlobalExitRoot: common.HexToHash("0x1")},
				}, nil)
				mockL1InfoTreeQuery.EXPECT().GetProofForGER(ctx, common.HexToHash("0x1"), common.HexToHash("0x2")).Return(nil, treetypes.Proof{}, errors.New("some error"))
			},
			expectedError: "error getting proof for GER: 0x0000000000000000000000000000000000000000000000000000000000000001: some error",
		},
		{
			name: "success",
			mockFn: func(mockChainGERReader *mocks.ChainGERReader, mockL1InfoTreeQuery *mocks.L1InfoTreeDataQuerier) {
				mockChainGERReader.EXPECT().GetInjectedGERsForRange(ctx, uint64(1), uint64(10)).Return(map[common.Hash]chaingerreader.InjectedGER{
					common.HexToHash("0x1"): {GlobalExitRoot: common.HexToHash("0x1"), BlockNumber: 111},
				}, nil)
				mockL1InfoTreeQuery.EXPECT().GetProofForGER(ctx, common.HexToHash("0x1"), common.HexToHash("0x2")).Return(
					&l1infotreesync.L1InfoTreeLeaf{
						L1InfoTreeIndex:   1,
						BlockNumber:       111,
						PreviousBlockHash: common.HexToHash("0x22"),
						Timestamp:         112,
						MainnetExitRoot:   common.HexToHash("0x11"),
						RollupExitRoot:    common.HexToHash("0x33"),
						GlobalExitRoot:    common.HexToHash("0x1"),
					},
					treetypes.Proof{},
					nil,
				)
			},
			expectedProofs: map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber{
				common.HexToHash("0x1"): {
					BlockNumber: 111,
					ProvenInsertedGERLeaf: agglayertypes.ProvenInsertedGER{
						ProofGERToL1Root: &agglayertypes.MerkleProof{
							Proof: treetypes.Proof{},
							Root:  common.HexToHash("0x2"),
						},
						L1Leaf: &agglayertypes.L1InfoTreeLeaf{
							L1InfoTreeIndex: 1,
							RollupExitRoot:  common.HexToHash("0x33"),
							MainnetExitRoot: common.HexToHash("0x11"),
							Inner: &agglayertypes.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0x1"),
								BlockHash:      common.HexToHash("0x22"),
								Timestamp:      112,
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockChainGERReader := mocks.NewChainGERReader(t)
			mockL1InfoTreeQuery := mocks.NewL1InfoTreeDataQuerier(t)
			aggchainFlow := &AggchainProverFlow{
				gerReader: mockChainGERReader,
				baseFlow: &baseFlow{
					l1InfoTreeDataQuerier: mockL1InfoTreeQuery,
					log:                   log.WithFields("flowManager", "Test_AggchainProverFlow_GetInjectedGERsProofs"),
				},
			}

			tc.mockFn(mockChainGERReader, mockL1InfoTreeQuery)

			proofs, err := aggchainFlow.getInjectedGERsProofs(ctx, &treetypes.Root{Hash: common.HexToHash("0x2"), Index: 10}, 1, 10)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedProofs, proofs)
			}

			mockChainGERReader.AssertExpectations(t)
			mockL1InfoTreeQuery.AssertExpectations(t)
		})
	}
}

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
							Metadata:           []byte("metadata"),
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
							Metadata:           []byte("metadata"),
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

			flow := &AggchainProverFlow{
				baseFlow: &baseFlow{
					log: log.WithFields("flowManager", "TestGetImportedBridgeExitsForProver"),
				},
			}

			exits, err := flow.getImportedBridgeExitsForProver(tc.claims)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedExits, exits)
			}
		})
	}
}

func Test_AggchainProverFlow_getLastProvenBlock(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		fromBlock      uint64
		startL2Block   uint64
		expectedResult uint64
	}{
		{
			name:           "fromBlock is 0, return startL2Block",
			fromBlock:      0,
			startL2Block:   1,
			expectedResult: 1,
		},
		{
			name:           "fromBlock is 0, startL2Block is 0",
			fromBlock:      0,
			startL2Block:   0,
			expectedResult: 0,
		},
		{
			name:           "fromBlock is greater than 0",
			fromBlock:      10,
			startL2Block:   1,
			expectedResult: 9,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			flow := &AggchainProverFlow{
				baseFlow: &baseFlow{
					startL2Block: tc.startL2Block,
				},
			}

			result := flow.getLastProvenBlock(tc.fromBlock)
			require.Equal(t, tc.expectedResult, result)
		})
	}
}

func Test_AggchainProverFlow_BuildCertificate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	createdAt := time.Now().UTC()

	testCases := []struct {
		name           string
		mockFn         func(*mocks.L2BridgeSyncer)
		buildParams    *types.CertificateBuildParams
		expectedError  string
		expectedResult *agglayertypes.Certificate
	}{
		{
			name: "error building certificate",
			mockFn: func(mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.EXPECT().GetExitRootByIndex(mock.Anything, uint32(0)).Return(treetypes.Root{}, errors.New("some error"))
			},
			buildParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        10,
				Bridges:                        []bridgesync.Bridge{{}},
				Claims:                         []bridgesync.Claim{},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
			},
			expectedError: "error getting exit root by index",
		},
		{
			name: "success building certificate",
			mockFn: func(mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.EXPECT().OriginNetwork().Return(uint32(1))
			},
			buildParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        10,
				Bridges:                        []bridgesync.Bridge{},
				Claims:                         []bridgesync.Claim{},
				CreatedAt:                      uint32(createdAt.Unix()),
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				AggchainProof: &types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{
						Proof:   []byte("some-proof"),
						Version: "0.1",
						Vkey:    []byte("some-vkey"),
					},
					LastProvenBlock: 1,
					EndBlock:        10,
					CustomChainData: []byte("some-data"),
					LocalExitRoot:   common.HexToHash("0x1"),
					AggchainParams:  common.HexToHash("0x2"),
					Context: map[string][]byte{
						"key1": []byte("value1"),
					},
				},
			},
			expectedResult: &agglayertypes.Certificate{
				NetworkID:           1,
				Height:              0,
				NewLocalExitRoot:    zeroLER,
				CustomChainData:     []byte("some-data"),
				Metadata:            types.NewCertificateMetadata(1, 9, uint32(createdAt.Unix())).ToHash(),
				BridgeExits:         []*agglayertypes.BridgeExit{},
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{},
				PrevLocalExitRoot:   zeroLER,
				AggchainData: &agglayertypes.AggchainDataProof{
					Proof:          []byte("some-proof"),
					Version:        "0.1",
					Vkey:           []byte("some-vkey"),
					AggchainParams: common.HexToHash("0x2"),
					Context: map[string][]byte{
						"key1": []byte("value1"),
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL2Syncer := mocks.NewL2BridgeSyncer(t)
			if tc.mockFn != nil {
				tc.mockFn(mockL2Syncer)
			}

			aggchainFlow := &AggchainProverFlow{
				baseFlow: &baseFlow{
					log:      log.WithFields("flowManager", "Test_AggchainProverFlow_BuildCertificate"),
					l2Syncer: mockL2Syncer,
				},
			}

			certificate, err := aggchainFlow.BuildCertificate(ctx, tc.buildParams)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.NotNil(t, certificate)
				require.Equal(t, tc.expectedResult, certificate)
			}
		})
	}
}
