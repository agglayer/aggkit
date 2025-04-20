package query

import (
	"context"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/bridgesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestGetBridgesAndClaims(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	testCases := []struct {
		name            string
		fromBlock       uint64
		toBlock         uint64
		mockFn          func(*mocks.L2BridgeSyncer)
		expectedBridges []bridgesync.Bridge
		expectedClaims  []bridgesync.Claim
		expectedError   string
	}{
		{
			name:      "success - valid bridges and claims",
			fromBlock: 100,
			toBlock:   200,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetBridges(ctx, uint64(100), uint64(200)).Return([]bridgesync.Bridge{
					{BlockNum: 100, BlockPos: 1},
				}, nil)
				mockSyncer.EXPECT().GetClaims(ctx, uint64(100), uint64(200)).Return([]bridgesync.Claim{
					{BlockNum: 200, BlockPos: 1},
				}, nil)
			},
			expectedBridges: []bridgesync.Bridge{
				{BlockNum: 100, BlockPos: 1},
			},
			expectedClaims: []bridgesync.Claim{
				{BlockNum: 200, BlockPos: 1},
			},
		},
		{
			name:      "error - failed to fetch bridges",
			fromBlock: 100,
			toBlock:   200,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetBridges(ctx, uint64(100), uint64(200)).Return(nil, errors.New("some error"))
			},
			expectedBridges: nil,
			expectedClaims:  nil,
			expectedError:   "error getting bridges: some error",
		},
		{
			name:      "error - failed to fetch claims",
			fromBlock: 100,
			toBlock:   200,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetBridges(ctx, uint64(100), uint64(200)).Return([]bridgesync.Bridge{
					{BlockNum: 100, BlockPos: 1},
				}, nil)
				mockSyncer.EXPECT().GetClaims(ctx, uint64(100), uint64(200)).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting claims: some error",
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockSyncer := new(mocks.L2BridgeSyncer)
			mockSyncer.EXPECT().OriginNetwork().Return(1).Once()
			tc.mockFn(mockSyncer)

			bridgeQuerier := NewBridgeDataQuerier(mockSyncer)

			bridges, claims, err := bridgeQuerier.GetBridgesAndClaims(ctx, tc.fromBlock, tc.toBlock)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.Len(t, bridges, len(tc.expectedBridges))
				require.Len(t, claims, len(tc.expectedClaims))
				require.Equal(t, tc.expectedBridges, bridges)
				require.Equal(t, tc.expectedClaims, claims)
			}

			mockSyncer.AssertExpectations(t)
		})
	}
}

func TestGetExitRootByIndex(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	testCases := []struct {
		name          string
		index         uint32
		mockFn        func(*mocks.L2BridgeSyncer)
		expectedHash  common.Hash
		expectedError string
	}{
		{
			name:  "success - valid exit root",
			index: 1,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetExitRootByIndex(ctx, uint32(1)).Return(treetypes.Root{
					Hash: common.HexToHash("0x1234"),
				}, nil)
			},
			expectedHash: common.HexToHash("0x1234"),
		},
		{
			name:  "error - failed to fetch exit root",
			index: 2,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetExitRootByIndex(ctx, uint32(2)).Return(treetypes.Root{}, errors.New("some error"))
			},
			expectedError: "error getting exit root by index: 2. Error: some error",
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockSyncer := new(mocks.L2BridgeSyncer)
			mockSyncer.EXPECT().OriginNetwork().Return(1).Once()
			tc.mockFn(mockSyncer)

			bridgeQuerier := NewBridgeDataQuerier(mockSyncer)

			hash, err := bridgeQuerier.GetExitRootByIndex(ctx, tc.index)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedHash, hash)
			}

			mockSyncer.AssertExpectations(t)
		})
	}
}

func TestGetLastProcessedBlock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	testCases := []struct {
		name          string
		mockFn        func(*mocks.L2BridgeSyncer)
		expectedBlock uint64
		expectedError string
	}{
		{
			name: "success - valid last processed block",
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(150), nil)
			},
			expectedBlock: 150,
		},
		{
			name: "error - failed to fetch last processed block",
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), errors.New("some error"))
			},
			expectedError: "error getting last processed block: some error",
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockSyncer := new(mocks.L2BridgeSyncer)
			mockSyncer.EXPECT().OriginNetwork().Return(1).Once()
			tc.mockFn(mockSyncer)

			bridgeQuerier := NewBridgeDataQuerier(mockSyncer)

			block, err := bridgeQuerier.GetLastProcessedBlock(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBlock, block)
			}

			mockSyncer.AssertExpectations(t)
		})
	}
}

func TestOriginNetwork(t *testing.T) {
	t.Parallel()

	mockSyncer := new(mocks.L2BridgeSyncer)
	mockSyncer.EXPECT().OriginNetwork().Return(uint32(1)).Once()

	bridgeQuerier := NewBridgeDataQuerier(mockSyncer)

	originNetwork := bridgeQuerier.OriginNetwork()
	require.Equal(t, uint32(1), originNetwork)

	mockSyncer.AssertExpectations(t)
}
