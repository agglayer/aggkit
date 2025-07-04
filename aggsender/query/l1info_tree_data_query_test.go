package query

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

func Test_GetFinalizedL1InfoTreeData(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name          string
		mockFn        func(*mocks.L1InfoTreeSyncer, *aggkittypesmocks.BaseEthereumClienter)
		expectedProof treetypes.Proof
		expectedLeaf  *l1infotreesync.L1InfoTreeLeaf
		expectedRoot  *treetypes.Root
		expectedError string
	}{
		{
			name: "error getting latest processed finalized block",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting latest processed finalized block",
		},
		{
			name: "error getting latest info until block num",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(l1Header.Number.Uint64(), l1Header.Hash(), nil)
				mockL1InfoTreeSyncer.On("GetLatestInfoUntilBlock", ctx, l1Header.Number.Uint64()).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting latest l1 info tree info until block num 10: some error",
		},
		{
			name: "error getting L1 Info tree root by index",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(l1Header.Number.Uint64(), l1Header.Hash(), nil)
				mockL1InfoTreeSyncer.On("GetLatestInfoUntilBlock", ctx, l1Header.Number.Uint64()).Return(
					&l1infotreesync.L1InfoTreeLeaf{
						L1InfoTreeIndex: 0,
						Hash:            common.HexToHash("0x1"),
					},
					nil,
				)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeRootByIndex", ctx, uint32(0)).Return(treetypes.Root{}, errors.New("some error"))
			},
			expectedError: "error getting L1 Info tree root by index 0: some error",
		},
		{
			name: "error getting L1 Info tree merkle proof from index to root",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(l1Header.Number.Uint64(), l1Header.Hash(), nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(l1Header.Number.Uint64(), l1Header.Hash(), nil)
				mockL1InfoTreeSyncer.On("GetLatestInfoUntilBlock", ctx, l1Header.Number.Uint64()).Return(
					&l1infotreesync.L1InfoTreeLeaf{
						L1InfoTreeIndex: 0,
						Hash:            common.HexToHash("0x1"),
					},
					nil,
				)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeRootByIndex", ctx, uint32(0)).Return(treetypes.Root{
					Hash: common.HexToHash("0x1"),
				}, nil)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", ctx, uint32(0), common.HexToHash("0x1")).Return(treetypes.Proof{}, errors.New("some error"))
			},
			expectedError: "error getting L1 Info tree merkle proof from index 0 to root",
		},
		{
			name: "success",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(l1Header.Number.Uint64(), l1Header.Hash(), nil)
				mockL1InfoTreeSyncer.On("GetLatestInfoUntilBlock", ctx, l1Header.Number.Uint64()).Return(
					&l1infotreesync.L1InfoTreeLeaf{
						L1InfoTreeIndex: 0,
						Hash:            common.HexToHash("0x2"),
					},
					nil,
				)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeRootByIndex", ctx, uint32(0)).Return(treetypes.Root{
					Hash: common.HexToHash("0x1"),
				}, nil)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", ctx, uint32(0), common.HexToHash("0x1")).Return(treetypes.Proof{}, nil)
			},
			expectedProof: treetypes.Proof{},
			expectedLeaf:  &l1infotreesync.L1InfoTreeLeaf{Hash: common.HexToHash("0x2")},
			expectedRoot:  &treetypes.Root{Index: 0, Hash: common.HexToHash("0x1")},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
			mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
			l1InfoTreeDataQuery := NewL1InfoTreeDataQuerier(mockL1Client, mockL1InfoTreeSyncer)

			tc.mockFn(mockL1InfoTreeSyncer, mockL1Client)

			proof, leaf, root, err := l1InfoTreeDataQuery.GetFinalizedL1InfoTreeData(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedProof, proof)
				require.Equal(t, tc.expectedLeaf, leaf)
				require.Equal(t, tc.expectedRoot, root)
			}

			mockL1InfoTreeSyncer.AssertExpectations(t)
			mockL1Client.AssertExpectations(t)
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
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting latest finalized L1 block: some error",
		},
		{
			name: "error getting latest processed block from l1infotreesyncer",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(uint64(0), common.Hash{}, errors.New("some error"))
			},
			expectedError: "error getting latest processed block from l1infotreesyncer: some error",
		},
		{
			name: "l1infotreesyncer did not process any block yet",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(uint64(0), common.Hash{}, nil)
			},
			expectedError: "l1infotreesyncer did not process any block yet",
		},
		{
			name: "error getting latest processed finalized block",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(uint64(9), common.Hash{}, nil)
				mockL1Client.On("HeaderByNumber", ctx, big.NewInt(9)).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting latest processed finalized block: 9: some error",
		},
		{
			name: "l1infotreesyncer returned a different hash for the latest finalized block",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(
					l1Header.Number.Uint64(), common.HexToHash("0x2"), nil)
			},
			expectedError: "l1infotreesyncer returned a different hash for the latest finalized block: 10. " +
				"Might be that syncer did not process a reorg yet.",
		},
		{
			name: "success",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *aggkittypesmocks.BaseEthereumClienter) {
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(
					l1Header.Number.Uint64(), l1Header.Hash(), nil)
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
			l1InfoTreeDataQuery := NewL1InfoTreeDataQuerier(mockL1Client, mockL1InfoTreeSyncer)

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
			l1InfoTreeDataQuery := NewL1InfoTreeDataQuerier(nil, mockL1InfoTreeSyncer)

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

func Test_CheckIfClaimsArePartOfFinalizedL1InfoTree(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		mockFn        func(*mocks.L1InfoTreeSyncer)
		finalizedRoot *treetypes.Root
		claims        []bridgesync.Claim
		expectedError string
	}{
		{
			name: "error getting claim info by global exit root",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(nil, errors.New("some error"))
			},
			finalizedRoot: &treetypes.Root{Index: 0},
			claims: []bridgesync.Claim{
				{GlobalExitRoot: common.HexToHash("0x1")},
			},
			expectedError: "error getting claim info by global exit root: 0x0000000000000000000000000000000000000000000000000000000000000001: some error",
		},
		{
			name: "claim L1 Info tree index higher than finalized root index",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(&l1infotreesync.L1InfoTreeLeaf{L1InfoTreeIndex: 1}, nil)
			},
			finalizedRoot: &treetypes.Root{Index: 0, Hash: common.HexToHash("0x2")},
			claims: []bridgesync.Claim{
				{GlobalExitRoot: common.HexToHash("0x1")},
			},
			expectedError: "claim with global exit root: 0x0000000000000000000000000000000000000000000000000000000000000001 has L1 Info tree index: 1 higher than the last finalized l1 info tree root: 0x0000000000000000000000000000000000000000000000000000000000000002 index: 0",
		},
		{
			name: "success",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(&l1infotreesync.L1InfoTreeLeaf{L1InfoTreeIndex: 0}, nil)
			},
			finalizedRoot: &treetypes.Root{Index: 1},
			claims: []bridgesync.Claim{
				{GlobalExitRoot: common.HexToHash("0x1")},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
			l1InfoTreeDataQuery := NewL1InfoTreeDataQuerier(nil, mockL1InfoTreeSyncer)

			tc.mockFn(mockL1InfoTreeSyncer)

			err := l1InfoTreeDataQuery.CheckIfClaimsArePartOfFinalizedL1InfoTree(tc.finalizedRoot, tc.claims)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
			}

			mockL1InfoTreeSyncer.AssertExpectations(t)
		})
	}
}
