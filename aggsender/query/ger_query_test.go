package query

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggoracle/chaingerreader"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/l1infotreesync"
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

			mockGERReader := mocks.NewChainGERReader(t)
			mockL1InfoTreeQuery := mocks.NewL1InfoTreeDataQuerier(t)
			gerQuerier := NewGERDataQuerier(mockL1InfoTreeQuery, mockGERReader)

			tc.mockFn(mockGERReader, mockL1InfoTreeQuery)

			proofs, err := gerQuerier.GetInjectedGERsProofs(ctx, &treetypes.Root{Hash: common.HexToHash("0x2"), Index: 10}, 1, 10)
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
