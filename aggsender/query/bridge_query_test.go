package query

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestGetBridgesAndClaims(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	testCases := []struct {
		name                          string
		fromBlock                     uint64
		toBlock                       uint64
		mockFn                        func(*mocks.L2BridgeSyncer)
		mockBridgeL2SovereignReaderFn func(*mocks.BridgeL2SovereignReader)
		expectedBridges               []bridgesync.Bridge
		expectedClaims                []bridgesync.Claim
		expectedError                 string
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
			mockBridgeL2SovereignReaderFn: func(mockReader *mocks.BridgeL2SovereignReader) {
				// No expectations needed for this test case
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
			mockBridgeL2SovereignReaderFn: func(mockReader *mocks.BridgeL2SovereignReader) {
				// No expectations needed for this test case
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
			mockBridgeL2SovereignReaderFn: func(mockReader *mocks.BridgeL2SovereignReader) {
				// No expectations needed for this test case
			},
			expectedError: "error getting claims: some error",
		},
		{
			name:      "no bridges and claims - empty cert",
			fromBlock: 100,
			toBlock:   200,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetBridges(ctx, uint64(100), uint64(200)).Return(nil, nil)
				mockSyncer.EXPECT().GetClaims(ctx, uint64(100), uint64(200)).Return(nil, nil)
			},
			mockBridgeL2SovereignReaderFn: func(mockReader *mocks.BridgeL2SovereignReader) {
				// No expectations needed for this test case
			},
			expectedBridges: nil,
			expectedClaims:  nil,
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockSyncer := new(mocks.L2BridgeSyncer)
			mockSyncer.EXPECT().OriginNetwork().Return(1).Once()
			tc.mockFn(mockSyncer)

			bridgeL2SovereignReader := new(mocks.BridgeL2SovereignReader)
			tc.mockBridgeL2SovereignReaderFn(bridgeL2SovereignReader)

			bridgeQuerier := NewBridgeDataQuerier(nil, mockSyncer, 0, bridgeL2SovereignReader)

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
		name                          string
		index                         uint32
		mockFn                        func(*mocks.L2BridgeSyncer)
		mockBridgeL2SovereignReaderFn func(*mocks.BridgeL2SovereignReader)
		expectedHash                  common.Hash
		expectedError                 string
	}{
		{
			name:  "success - valid exit root",
			index: 1,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetExitRootByIndex(ctx, uint32(1)).Return(treetypes.Root{
					Hash: common.HexToHash("0x1234"),
				}, nil)
			},
			mockBridgeL2SovereignReaderFn: func(mockReader *mocks.BridgeL2SovereignReader) {
				// No expectations needed for this test case
			},
			expectedHash: common.HexToHash("0x1234"),
		},
		{
			name:  "error - failed to fetch exit root",
			index: 2,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetExitRootByIndex(ctx, uint32(2)).Return(treetypes.Root{}, errors.New("some error"))
			},
			mockBridgeL2SovereignReaderFn: func(mockReader *mocks.BridgeL2SovereignReader) {
				// No expectations needed for this test case
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

			bridgeL2SovereignReader := new(mocks.BridgeL2SovereignReader)
			tc.mockBridgeL2SovereignReaderFn(bridgeL2SovereignReader)

			bridgeQuerier := NewBridgeDataQuerier(nil, mockSyncer, 0, bridgeL2SovereignReader)

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
		name                          string
		mockFn                        func(*mocks.L2BridgeSyncer)
		mockBridgeL2SovereignReaderFn func(*mocks.BridgeL2SovereignReader)
		expectedBlock                 uint64
		expectedError                 string
	}{
		{
			name: "success - valid last processed block",
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(150), nil)
			},
			mockBridgeL2SovereignReaderFn: func(mockReader *mocks.BridgeL2SovereignReader) {
				// No expectations needed for this test case
			},
			expectedBlock: 150,
		},
		{
			name: "error - failed to fetch last processed block",
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), errors.New("some error"))
			},
			mockBridgeL2SovereignReaderFn: func(mockReader *mocks.BridgeL2SovereignReader) {
				// No expectations needed for this test case
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

			bridgeL2SovereignReader := new(mocks.BridgeL2SovereignReader)
			tc.mockBridgeL2SovereignReaderFn(bridgeL2SovereignReader)

			bridgeQuerier := NewBridgeDataQuerier(nil, mockSyncer, 0, bridgeL2SovereignReader)

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

	bridgeL2SovereignReader := new(mocks.BridgeL2SovereignReader)

	bridgeQuerier := NewBridgeDataQuerier(nil, mockSyncer, 0, bridgeL2SovereignReader)

	originNetwork := bridgeQuerier.OriginNetwork()
	require.Equal(t, uint32(1), originNetwork)

	mockSyncer.AssertExpectations(t)
}

func TestWaitForSyncerToCatchUp(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name                          string
		delayBetweenRetries           time.Duration
		block                         uint64
		mockFn                        func(*mocks.L2BridgeSyncer)
		mockBridgeL2SovereignReaderFn func(*mocks.BridgeL2SovereignReader)
		expectedError                 string
	}{
		{
			name:  "fail to get last processed block",
			block: 100,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), errors.New("some error")).Once()
			},
			expectedError: "bridgeDataQuerier - error getting last processed block: some error",
		},
		{
			name:                "success - delay between retries is zero",
			delayBetweenRetries: 0,
			block:               10,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(100), nil).Once()
			},
		},
		{
			name:                "success - after multiple retries",
			block:               10,
			delayBetweenRetries: 10 * time.Millisecond,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				mockSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), nil).Times(3)
				mockSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil).Once()
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mockSyncer := mocks.NewL2BridgeSyncer(t)
			if tc.mockFn != nil {
				tc.mockFn(mockSyncer)
			}

			bridgeQuerier := &bridgeDataQuerier{
				log:                 log.WithFields("test", "TestWaitForSyncerToCatchUp"),
				bridgeSyncer:        mockSyncer,
				delayBetweenRetries: tc.delayBetweenRetries,
			}

			err := bridgeQuerier.WaitForSyncerToCatchUp(ctx, tc.block)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
			}

			mockSyncer.AssertExpectations(t)
		})
	}
}

func TestGetUnsetClaimsForBlockRange(t *testing.T) {
	t.Parallel()

	// Helper function to create the correct string representation of GlobalIndex
	// The actual code uses big.NewInt(1).String() which returns "1", not a formatted string
	createGlobalIndexString := func(index int64) string {
		return big.NewInt(index).String()
	}

	ctx := context.Background()
	testCases := []struct {
		name                          string
		fromBlock                     uint64
		toBlock                       uint64
		mockFn                        func(*mocks.L2BridgeSyncer)
		mockBridgeL2SovereignReaderFn func(*mocks.BridgeL2SovereignReader)
		expectedUnclaims              []agglayertypes.Unclaim
		expectedError                 string
	}{
		{
			name:      "success - valid unclaims with proper hash calculation",
			fromBlock: 100,
			toBlock:   200,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				// Mock GetClaimByGlobalIndex for the first unclaim
				mockSyncer.EXPECT().GetClaimByGlobalIndex(
					ctx, createGlobalIndexString(1),
				).Return(bridgesync.Claim{
					BlockNum:           150,
					BlockPos:           1,
					GlobalIndex:        big.NewInt(1),
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					IsMessage:          false,
					// Add required fields for the hash calculation
					GlobalExitRoot:      common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111"),
					MainnetExitRoot:     common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"),
					RollupExitRoot:      common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333"),
					ProofLocalExitRoot:  treetypes.Proof{},
					ProofRollupExitRoot: treetypes.Proof{},
				}, nil).Once()
			},
			mockBridgeL2SovereignReaderFn: func(mockReader *mocks.BridgeL2SovereignReader) {
				// Mock GetUnsetClaimsForBlockRange to return one unclaim
				mockReader.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(100), uint64(200)).Return([]*bridgesynctypes.Unclaim{
					{
						GlobalIndex: big.NewInt(1),
						BlockNumber: 150,
						BlockIndex:  1,
					},
				}, nil).Once()
			},
			expectedUnclaims: []agglayertypes.Unclaim{
				{
					BlockNumber: 150,
					BlockIndex:  1,
				},
			},
		},
		{
			name:      "success - empty unclaims",
			fromBlock: 100,
			toBlock:   200,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				// No expectations needed for empty unclaims
			},
			mockBridgeL2SovereignReaderFn: func(mockReader *mocks.BridgeL2SovereignReader) {
				mockReader.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(100), uint64(200)).Return([]*bridgesynctypes.Unclaim{}, nil).Once()
			},
			expectedUnclaims: []agglayertypes.Unclaim{},
		},
		{
			name:      "error - failed to get unclaim block range",
			fromBlock: 100,
			toBlock:   200,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				// No expectations needed for this error case
			},
			mockBridgeL2SovereignReaderFn: func(mockReader *mocks.BridgeL2SovereignReader) {
				mockReader.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(100), uint64(200)).Return(nil, errors.New("failed to read from contract")).Once()
			},
			expectedError: "failed to get unclaim block range: failed to read from contract",
		},
		{
			name:      "error - failed to get claim by global index",
			fromBlock: 100,
			toBlock:   200,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				// Mock GetClaimByGlobalIndex to return an error
				mockSyncer.EXPECT().GetClaimByGlobalIndex(
					ctx, createGlobalIndexString(1),
				).Return(bridgesync.Claim{}, errors.New("claim not found")).Once()
			},
			mockBridgeL2SovereignReaderFn: func(mockReader *mocks.BridgeL2SovereignReader) {
				mockReader.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(100), uint64(200)).Return([]*bridgesynctypes.Unclaim{
					{
						GlobalIndex: big.NewInt(1),
						BlockNumber: 150,
						BlockIndex:  1,
					},
				}, nil).Once()
			},
			expectedError: "failed to get claim by global index: claim not found",
		},
		{
			name:      "error - failed to convert claim to imported bridge exit",
			fromBlock: 100,
			toBlock:   200,
			mockFn: func(mockSyncer *mocks.L2BridgeSyncer) {
				// Mock GetClaimByGlobalIndex to return a claim with invalid global index
				mockSyncer.EXPECT().GetClaimByGlobalIndex(
					ctx, createGlobalIndexString(1),
				).Return(bridgesync.Claim{
					BlockNum:           150,
					BlockPos:           1,
					GlobalIndex:        nil, // This will cause conversion to fail
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					IsMessage:          false,
				}, nil).Once()
			},
			mockBridgeL2SovereignReaderFn: func(mockReader *mocks.BridgeL2SovereignReader) {
				mockReader.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(100), uint64(200)).Return([]*bridgesynctypes.Unclaim{
					{
						GlobalIndex: big.NewInt(1),
						BlockNumber: 150,
						BlockIndex:  1,
					},
				}, nil).Once()
			},
			expectedError: "failed to convert claim to imported bridge exit: error decoding global index: global index is nil",
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockSyncer := new(mocks.L2BridgeSyncer)
			mockSyncer.EXPECT().OriginNetwork().Return(1).Once()
			tc.mockFn(mockSyncer)

			bridgeL2SovereignReader := new(mocks.BridgeL2SovereignReader)
			tc.mockBridgeL2SovereignReaderFn(bridgeL2SovereignReader)

			bridgeQuerier := NewBridgeDataQuerier(nil, mockSyncer, 0, bridgeL2SovereignReader)

			unclaims, err := bridgeQuerier.GetUnsetClaimsForBlockRange(ctx, tc.fromBlock, tc.toBlock)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Len(t, unclaims, len(tc.expectedUnclaims))

				// For successful cases, verify the structure and that hashes are properly calculated
				for i, unclaim := range unclaims {
					require.Equal(t, tc.expectedUnclaims[i].BlockNumber, unclaim.BlockNumber)
					require.Equal(t, tc.expectedUnclaims[i].BlockIndex, unclaim.BlockIndex)
					require.NotEqual(t, common.Hash{}, unclaim.UnclaimHash) // Hash should not be empty

					// Verify that the hash is a valid hash (not all zeros)
					require.NotEqual(t, common.Hash{}, unclaim.UnclaimHash)
				}
			}

			mockSyncer.AssertExpectations(t)
			bridgeL2SovereignReader.AssertExpectations(t)
		})
	}
}
