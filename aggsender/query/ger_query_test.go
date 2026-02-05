package query

import (
	"context"
	"errors"
	"math"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
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
				mockChainGERReader.EXPECT().GetInjectedGERsForRange(ctx, uint64(1), uint64(10)).Return(map[common.Hash]l2gersync.GlobalExitRootInfo{
					common.HexToHash("0x1"): {GlobalExitRoot: common.HexToHash("0x1"), BlockNum: 111, BlockPosition: 0},
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
		{
			name: "success with removed GER (dummy info and proof)",
			mockFn: func(mockChainGERReader *mocks.ChainGERReader, mockL1InfoTreeQuery *mocks.L1InfoTreeDataQuerier) {
				gerHash := common.HexToHash("0xabc123")
				mockChainGERReader.EXPECT().GetInjectedGERsForRange(ctx, uint64(1), uint64(10)).Return(map[common.Hash]l2gersync.GlobalExitRootInfo{
					gerHash: {
						GlobalExitRoot:  gerHash,
						L1InfoTreeIndex: math.MaxUint32,
						BlockNum:        222,
						BlockPosition:   5,
						Removed:         true,
					},
				}, nil)
				// GetProofForGER should NOT be called when L1InfoTreeIndex is MaxUint32
			},
			expectedProofs: map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber{
				common.HexToHash("0xabc123"): {
					BlockNumber: 222,
					LogIndex:    5,
					ProvenInsertedGERLeaf: agglayertypes.ProvenInsertedGER{
						ProofGERToL1Root: &agglayertypes.MerkleProof{
							Proof: treetypes.Proof{},
							Root:  common.HexToHash("0x2"),
						},
						L1Leaf: &agglayertypes.L1InfoTreeLeaf{
							L1InfoTreeIndex: math.MaxUint32,
							RollupExitRoot:  common.HexToHash("0x0"),
							MainnetExitRoot: common.HexToHash("0x0"),
							Inner: &agglayertypes.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0xabc123"),
								BlockHash:      common.HexToHash("0x0"),
								Timestamp:      0,
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

			mockGERReader := mocks.NewChainGERReader(t)
			mockL1InfoTreeQuery := mocks.NewL1InfoTreeDataQuerier(t)
			gerQuerier := NewGERDataQuerier(mockL1InfoTreeQuery, mockGERReader)

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
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockGERReader := mocks.NewChainGERReader(t)
			mockL1InfoTreeQuery := mocks.NewL1InfoTreeDataQuerier(t)
			gerQuerier := NewGERDataQuerier(mockL1InfoTreeQuery, mockGERReader)

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
