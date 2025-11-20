package query

import (
	"context"
	"errors"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_GetInjectedGERsProofs(t *testing.T) {
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
				mockChainGERReader.EXPECT().GetInjectedGERsForRange(ctx, uint64(1), uint64(10)).Return(map[common.Hash]l2gersync.GlobalExitRootInfo{
					common.HexToHash("0x1"): {GlobalExitRoot: common.HexToHash("0x1")},
				}, nil)
				mockL1InfoTreeQuery.EXPECT().GetProofForGER(ctx, common.HexToHash("0x1"), common.HexToHash("0x2")).Return(nil, treetypes.Proof{}, errors.New("some error"))
			},
			expectedError: "error getting proof for GER: 0x0000000000000000000000000000000000000000000000000000000000000001: some error",
		},
		{
			name: "success",
			mockFn: func(mockChainGERReader *mocks.ChainGERReader, mockL1InfoTreeQuery *mocks.L1InfoTreeDataQuerier) {
				blockPos := uint64(0)
				mockChainGERReader.EXPECT().GetInjectedGERsForRange(ctx, uint64(1), uint64(10)).Return(map[common.Hash]l2gersync.GlobalExitRootInfo{
					common.HexToHash("0x1"): {GlobalExitRoot: common.HexToHash("0x1"), BlockNum: 111, BlockPosition: &blockPos},
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
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockGERReader := mocks.NewChainGERReader(t)
			mockL1InfoTreeQuery := mocks.NewL1InfoTreeDataQuerier(t)
			gerQuerier := NewL2GERDataQuerier(mockL1InfoTreeQuery, mockGERReader)

			tc.mockFn(mockGERReader, mockL1InfoTreeQuery)

			proofs, err := gerQuerier.GetInjectedGERsProofs(ctx, common.HexToHash("0x2"), 1, 10)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedProofs, proofs)
			}

			mockGERReader.AssertExpectations(t)
			mockL1InfoTreeQuery.AssertExpectations(t)
		})
	}
}

func Test_GetRemovedGERsForRange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name          string
		mockFn        func(*mocks.ChainGERReader)
		expectedGERs  []*agglayertypes.RemovedGER
		expectedError string
	}{
		{
			name: "error getting removed GERs for range",
			mockFn: func(mockChainGERReader *mocks.ChainGERReader) {
				mockChainGERReader.EXPECT().GetRemovedGERsForRange(ctx, uint64(1), uint64(10)).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting removed GERs for range 1 : 10: some error",
		},
		{
			name: "success with empty result",
			mockFn: func(mockChainGERReader *mocks.ChainGERReader) {
				mockChainGERReader.EXPECT().GetRemovedGERsForRange(ctx, uint64(1), uint64(10)).Return([]*agglayertypes.RemovedGER{}, nil)
			},
			expectedGERs: []*agglayertypes.RemovedGER{},
		},
		{
			name: "success with single removed GER",
			mockFn: func(mockChainGERReader *mocks.ChainGERReader) {
				mockChainGERReader.EXPECT().GetRemovedGERsForRange(ctx, uint64(1), uint64(10)).Return([]*agglayertypes.RemovedGER{
					{
						GlobalExitRoot: common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef12"),
						BlockNumber:    5,
						LogIndex:       2,
					},
				}, nil)
			},
			expectedGERs: []*agglayertypes.RemovedGER{
				{
					GlobalExitRoot: common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef12"),
					BlockNumber:    5,
					LogIndex:       2,
				},
			},
		},
		{
			name: "success with multiple removed GERs",
			mockFn: func(mockChainGERReader *mocks.ChainGERReader) {
				mockChainGERReader.EXPECT().GetRemovedGERsForRange(ctx, uint64(1), uint64(10)).Return([]*agglayertypes.RemovedGER{
					{
						GlobalExitRoot: common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111"),
						BlockNumber:    3,
						LogIndex:       0,
					},
					{
						GlobalExitRoot: common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"),
						BlockNumber:    7,
						LogIndex:       1,
					},
					{
						GlobalExitRoot: common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333"),
						BlockNumber:    9,
						LogIndex:       3,
					},
				}, nil)
			},
			expectedGERs: []*agglayertypes.RemovedGER{
				{
					GlobalExitRoot: common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111"),
					BlockNumber:    3,
					LogIndex:       0,
				},
				{
					GlobalExitRoot: common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"),
					BlockNumber:    7,
					LogIndex:       1,
				},
				{
					GlobalExitRoot: common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333"),
					BlockNumber:    9,
					LogIndex:       3,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockGERReader := mocks.NewChainGERReader(t)
			mockL1InfoTreeQuery := mocks.NewL1InfoTreeDataQuerier(t)
			gerQuerier := NewL2GERDataQuerier(mockL1InfoTreeQuery, mockGERReader)

			tc.mockFn(mockGERReader)

			removedGERs, err := gerQuerier.GetRemovedGERsForRange(ctx, 1, 10)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedGERs, removedGERs)
			}

			mockGERReader.AssertExpectations(t)
		})
	}
}

func Test_NewL1GERDataQuerier(t *testing.T) {
	t.Run("error initializing L1 GER manager contract", func(t *testing.T) {
		createAgglayerGERL1func = func(_ common.Address, _ aggkittypes.BaseEthereumClienter) (types.AgglayerGER, error) {
			return nil, errors.New("some error")
		}

		_, err := NewL1GERDataQuerier(
			common.HexToAddress("0x1"),
			aggkittypes.FinalizedBlock,
			aggkittypesmocks.NewBaseEthereumClienter(t),
		)
		require.ErrorContains(t, err, "failed to initialize L1 GER manager contract: some error")
	})

	t.Run("success", func(t *testing.T) {
		createAgglayerGERL1func = func(_ common.Address, _ aggkittypes.BaseEthereumClienter) (types.AgglayerGER, error) {
			return mocks.NewAgglayerGER(t), nil
		}

		gerQuerier, err := NewL1GERDataQuerier(
			common.HexToAddress("0x1"),
			aggkittypes.FinalizedBlock,
			aggkittypesmocks.NewBaseEthereumClienter(t),
		)
		require.NoError(t, err)
		require.NotNil(t, gerQuerier)
	})
}

func Test_L1GERDataQuerier_DoesGERExistOnContract(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		ger           common.Hash
		blockFinality *aggkittypes.BlockNumberFinality
		mockFn        func(*mocks.AgglayerGER, *aggkittypesmocks.BaseEthereumClienter)
		expectedExist bool
		expectedError string
	}{
		{
			name: "error getting block number for finality",
			ger:  common.HexToHash("0x1"),
			mockFn: func(mockAgglayerGER *mocks.AgglayerGER, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				mockL1Client.EXPECT().HeaderByNumber(
					t.Context(),
					big.NewInt(int64(aggkittypes.Finalized)),
				).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting block number for finality FinalizedBlock: some error",
		},
		{
			name: "error querying GER existence on contract",
			ger:  common.HexToHash("0x1"),
			mockFn: func(mockAgglayerGER *mocks.AgglayerGER, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				mockL1Client.EXPECT().HeaderByNumber(
					t.Context(),
					big.NewInt(int64(aggkittypes.Finalized)),
				).Return(&ethtypes.Header{Number: big.NewInt(100)}, nil)
				mockAgglayerGER.EXPECT().GlobalExitRootMap(
					&bind.CallOpts{
						Context:     t.Context(),
						BlockNumber: big.NewInt(100),
					},
					mock.Anything,
				).Return(nil, errors.New("some error"))
			},
			expectedError: "error querying GER existence on contract: some error",
		},
		{
			name: "GER does not exist",
			ger:  common.HexToHash("0x1"),
			mockFn: func(mockAgglayerGER *mocks.AgglayerGER, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				mockL1Client.EXPECT().HeaderByNumber(
					t.Context(),
					big.NewInt(int64(aggkittypes.Finalized)),
				).Return(&ethtypes.Header{Number: big.NewInt(100)}, nil)
				mockAgglayerGER.EXPECT().GlobalExitRootMap(
					&bind.CallOpts{
						Context:     t.Context(),
						BlockNumber: big.NewInt(100),
					},
					mock.Anything,
				).Return(common.Big0, nil)
			},
			expectedExist: false,
		},
		{
			name: "GER exists",
			ger:  common.HexToHash("0x1"),
			mockFn: func(mockAgglayerGER *mocks.AgglayerGER, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				mockL1Client.EXPECT().HeaderByNumber(
					t.Context(),
					big.NewInt(int64(aggkittypes.Finalized)),
				).Return(&ethtypes.Header{Number: big.NewInt(100)}, nil)
				mockAgglayerGER.EXPECT().GlobalExitRootMap(
					&bind.CallOpts{
						Context:     t.Context(),
						BlockNumber: big.NewInt(100),
					},
					mock.Anything,
				).Return(big.NewInt(12345), nil)
			},
			expectedExist: true,
		},
		{
			name: "GER exists with block finality with offset",
			ger:  common.HexToHash("0x1"),
			blockFinality: &aggkittypes.BlockNumberFinality{
				Block:  aggkittypes.Latest,
				Offset: -6,
			},
			mockFn: func(mockAgglayerGER *mocks.AgglayerGER, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				mockL1Client.EXPECT().HeaderByNumber(
					t.Context(),
					(*big.Int)(nil),
				).Return(&ethtypes.Header{Number: big.NewInt(200)}, nil)
				mockAgglayerGER.EXPECT().GlobalExitRootMap(
					&bind.CallOpts{
						Context:     t.Context(),
						BlockNumber: big.NewInt(194),
					},
					mock.Anything,
				).Return(big.NewInt(67890), nil)
			},
			expectedExist: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockAgglayerGER := mocks.NewAgglayerGER(t)
			mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)

			blockFinality := aggkittypes.FinalizedBlock
			if tc.blockFinality != nil {
				blockFinality = *tc.blockFinality
			}

			gerQuerier := &l1GERDataQuerier{
				blockFinality: blockFinality,
				agglayerGER:   mockAgglayerGER,
				l1Client:      mockL1Client,
			}

			if tc.mockFn != nil {
				tc.mockFn(mockAgglayerGER, mockL1Client)
			}

			exists, err := gerQuerier.DoesGERExistOnContract(t.Context(), tc.ger)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedExist, exists)
			}

			mockAgglayerGER.AssertExpectations(t)
			mockL1Client.AssertExpectations(t)
		})
	}
}
