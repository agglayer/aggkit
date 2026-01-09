package query

import (
	"context"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/l1infotreesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var finalizedBlockBigInt = &aggkittypes.FinalizedBlock

func Test_GetFinalizedL1InfoTreeData(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name                         string
		finalizedL1InfoTreeRootHash  common.Hash
		finalizedL1InfoTreeLeafCount uint32
		mockFn                       func(*mocks.L1InfoTreeSyncer)
		expectedProof                treetypes.Proof
		expectedLeaf                 *l1infotreesync.L1InfoTreeLeaf
		expectedError                string
	}{
		{
			name:                         "error getting info by index",
			finalizedL1InfoTreeRootHash:  common.HexToHash("0x1"),
			finalizedL1InfoTreeLeafCount: 11,
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.EXPECT().GetInfoByIndex(ctx, uint32(10)).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting L1 Info tree leaf by index 10: some error",
		},
		{
			name:                         "error getting L1 Info tree merkle proof from index to root",
			finalizedL1InfoTreeRootHash:  common.HexToHash("0x1"),
			finalizedL1InfoTreeLeafCount: 11,
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.EXPECT().GetInfoByIndex(ctx, uint32(10)).Return(
					&l1infotreesync.L1InfoTreeLeaf{
						L1InfoTreeIndex: 0,
						Hash:            common.HexToHash("0x1"),
					},
					nil,
				)
				mockL1InfoTreeSyncer.EXPECT().GetL1InfoTreeMerkleProofFromIndexToRoot(ctx, uint32(10), common.HexToHash("0x1")).Return(treetypes.Proof{}, errors.New("some error"))
			},
			expectedError: "error getting L1 Info tree merkle proof from index 10 to root",
		},
		{
			name:                         "success",
			finalizedL1InfoTreeRootHash:  common.HexToHash("0x1"),
			finalizedL1InfoTreeLeafCount: 11,
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.EXPECT().GetInfoByIndex(ctx, uint32(10)).Return(
					&l1infotreesync.L1InfoTreeLeaf{
						L1InfoTreeIndex: 10,
						Hash:            common.HexToHash("0x2"),
					},
					nil,
				)
				mockL1InfoTreeSyncer.EXPECT().GetL1InfoTreeMerkleProofFromIndexToRoot(ctx, uint32(10), common.HexToHash("0x1")).Return(treetypes.Proof{}, nil)
			},
			expectedProof: treetypes.Proof{},
			expectedLeaf: &l1infotreesync.L1InfoTreeLeaf{
				L1InfoTreeIndex: 10,
				Hash:            common.HexToHash("0x2"),
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
			mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
			l1InfoTreeDataQuery, err := NewL1InfoTreeDataQuerier(mockL1Client, common.Address{}, mockL1InfoTreeSyncer, aggkittypes.FinalizedBlock)
			require.NoError(t, err)

			tc.mockFn(mockL1InfoTreeSyncer)

			proof, leaf, err := l1InfoTreeDataQuery.GetFinalizedL1InfoTreeData(ctx,
				tc.finalizedL1InfoTreeRootHash, tc.finalizedL1InfoTreeLeafCount)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedProof, proof)
				require.Equal(t, tc.expectedLeaf, leaf)
			}

			mockL1InfoTreeSyncer.AssertExpectations(t)
		})
	}
}

func Test_AggchainProverFlow_GetLatestProcessedFinalizedBlock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name          string
		mockFn        func(*mocks.L1InfoTreeSyncer, *aggkittypesmocks.BaseEthereumClienter)
		expectedBlock uint64
		expectedError string
	}{
		{
			name: "error getting latest finalized L1 block",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				mockL1Client.On("CustomHeaderByNumber", ctx, finalizedBlockBigInt).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting latest finalized L1 block: some error",
		},
		{
			name: "error getting latest processed block from l1infotreesyncer",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				l1Header := &aggkittypes.BlockHeader{Number: 10}
				mockL1Client.On("CustomHeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number).Return(uint64(0), common.Hash{}, errors.New("some error"))
			},
			expectedError: "error getting latest processed block from l1infotreesyncer: some error",
		},
		{
			name: "l1infotreesyncer did not process any block yet",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				l1Header := &aggkittypes.BlockHeader{Number: 10}
				mockL1Client.On("CustomHeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number).Return(uint64(0), common.Hash{}, nil)
			},
			expectedError: "l1infotreesyncer did not process any block yet",
		},
		{
			name: "error getting latest processed finalized block",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				l1Header := &aggkittypes.BlockHeader{Number: 10}
				mockL1Client.On("CustomHeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number).Return(uint64(9), common.Hash{}, nil)
				mockL1Client.On("CustomHeaderByNumber", ctx, aggkittypes.NewBlockNumber(9)).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting latest processed finalized block: 9: some error",
		},
		{
			name: "l1infotreesyncer returned a different hash for the latest finalized block",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				l1Header := &aggkittypes.BlockHeader{Number: 10, Hash: common.HexToHash("0xabc")}
				mockL1Client.On("CustomHeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number).Return(
					l1Header.Number, common.HexToHash("0x2"), nil)
			},
			expectedError: "l1infotreesyncer returned a different hash for the latest finalized block: 10. " +
				"Might be that syncer did not process a reorg yet.",
		},
		{
			name: "success",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				l1Header := &aggkittypes.BlockHeader{Number: 10}
				mockL1Client.On("CustomHeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number).Return(
					l1Header.Number, l1Header.Hash, nil)
			},
			expectedBlock: 10,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
			mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
			l1InfoTreeDataQuery, err := NewL1InfoTreeDataQuerier(mockL1Client, common.Address{}, mockL1InfoTreeSyncer, aggkittypes.FinalizedBlock)
			require.NoError(t, err)

			tc.mockFn(mockL1InfoTreeSyncer, mockL1Client)

			block, err := l1InfoTreeDataQuery.getLatestProcessedFinalizedBlock(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBlock, block)
			}

			mockL1InfoTreeSyncer.AssertExpectations(t)
			mockL1Client.AssertExpectations(t)
		})
	}
}

func Test_GetProofForGER(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name          string
		ger           common.Hash
		root          common.Hash
		mockFn        func(*mocks.L1InfoTreeSyncer)
		expectedLeaf  *l1infotreesync.L1InfoTreeLeaf
		expectedProof treetypes.Proof
		expectedError string
	}{
		{
			name: "error getting info by global exit root",
			ger:  common.HexToHash("0x1"),
			root: common.HexToHash("0x2"),
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting info by global exit root: some error",
		},
		{
			name: "error getting L1 Info tree merkle proof for GER",
			ger:  common.HexToHash("0x1"),
			root: common.HexToHash("0x2"),
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(
					&l1infotreesync.L1InfoTreeLeaf{
						L1InfoTreeIndex: 0,
						Hash:            common.HexToHash("0x3"),
					}, nil,
				)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", ctx, uint32(0), common.HexToHash("0x2")).Return(treetypes.Proof{}, errors.New("some error"))
			},
			expectedError: "error getting L1 Info tree merkle proof for GER: some error",
		},
		{
			name: "success",
			ger:  common.HexToHash("0x1"),
			root: common.HexToHash("0x2"),
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(
					&l1infotreesync.L1InfoTreeLeaf{
						L1InfoTreeIndex: 0,
						Hash:            common.HexToHash("0x3"),
					}, nil,
				)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", ctx, uint32(0), common.HexToHash("0x2")).Return(treetypes.Proof{}, nil)
			},
			expectedLeaf: &l1infotreesync.L1InfoTreeLeaf{
				L1InfoTreeIndex: 0,
				Hash:            common.HexToHash("0x3"),
			},
			expectedProof: treetypes.Proof{},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
			l1InfoTreeDataQuery, err := NewL1InfoTreeDataQuerier(nil, common.Address{}, mockL1InfoTreeSyncer, aggkittypes.FinalizedBlock)
			require.NoError(t, err)

			tc.mockFn(mockL1InfoTreeSyncer)

			leaf, proof, err := l1InfoTreeDataQuery.GetProofForGER(ctx, tc.ger, tc.root)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedLeaf, leaf)
				require.Equal(t, tc.expectedProof, proof)
			}

			mockL1InfoTreeSyncer.AssertExpectations(t)
		})
	}
}

func Test_IsGERFinalized(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                     string
		ger                      common.Hash
		finalizedL1InfoLeafCount uint32
		mockFn                   func(*mocks.L1InfoTreeSyncer)
		expectedResult           bool
		expectedError            string
	}{
		{
			name:                     "error getting info by global exit root",
			ger:                      common.HexToHash("0x1"),
			finalizedL1InfoLeafCount: 10,
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name:                     "no L1 Info tree leaf found for global exit root",
			ger:                      common.HexToHash("0x1"),
			finalizedL1InfoLeafCount: 10,
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(nil, nil)
			},
			expectedError: "no L1 Info tree leaf found for global exit root 0x0000000000000000000000000000000000000000000000000000000000000001",
		},
		{
			name:                     "GER is finalized - leaf index equals finalized count minus 1",
			ger:                      common.HexToHash("0x1"),
			finalizedL1InfoLeafCount: 10,
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(
					&l1infotreesync.L1InfoTreeLeaf{
						L1InfoTreeIndex: 9,
						Hash:            common.HexToHash("0x2"),
					}, nil,
				)
			},
			expectedResult: true,
		},
		{
			name:                     "GER is finalized - leaf index less than finalized count minus 1",
			ger:                      common.HexToHash("0x1"),
			finalizedL1InfoLeafCount: 10,
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(
					&l1infotreesync.L1InfoTreeLeaf{
						L1InfoTreeIndex: 5,
						Hash:            common.HexToHash("0x2"),
					}, nil,
				)
			},
			expectedResult: true,
		},
		{
			name:                     "GER is not finalized - leaf index greater than finalized count minus 1",
			ger:                      common.HexToHash("0x1"),
			finalizedL1InfoLeafCount: 10,
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(
					&l1infotreesync.L1InfoTreeLeaf{
						L1InfoTreeIndex: 10,
						Hash:            common.HexToHash("0x2"),
					}, nil,
				)
			},
			expectedResult: false,
		},
		{
			name:                     "edge case - finalized count is 1",
			ger:                      common.HexToHash("0x1"),
			finalizedL1InfoLeafCount: 1,
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(
					&l1infotreesync.L1InfoTreeLeaf{
						L1InfoTreeIndex: 0,
						Hash:            common.HexToHash("0x2"),
					}, nil,
				)
			},
			expectedResult: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
			l1InfoTreeDataQuery, err := NewL1InfoTreeDataQuerier(nil, common.Address{}, mockL1InfoTreeSyncer, aggkittypes.FinalizedBlock)
			require.NoError(t, err)

			tc.mockFn(mockL1InfoTreeSyncer)

			result, err := l1InfoTreeDataQuery.IsGERFinalized(tc.ger, tc.finalizedL1InfoLeafCount)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedResult, result)
			}

			mockL1InfoTreeSyncer.AssertExpectations(t)
		})
	}
}
