package rpc

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	mocks "github.com/agglayer/aggkit/rpc/mocks"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetFirstL1InfoTreeIndexForL1Bridge(t *testing.T) {
	type testCase struct {
		description   string
		setupMocks    func()
		depositCount  uint32
		expectedIndex uint32
		expectedErr   error
	}
	ctx := context.Background()
	networkID := uint32(1)
	b := newBridgeWithMocks(t, networkID)
	fooErr := errors.New("foo")
	firstL1Info := &l1infotreesync.L1InfoTreeLeaf{
		BlockNumber:     10,
		MainnetExitRoot: common.HexToHash("alfa"),
	}
	lastL1Info := &l1infotreesync.L1InfoTreeLeaf{
		BlockNumber:     1000,
		MainnetExitRoot: common.HexToHash("alfa"),
	}
	mockHappyPath := func() {
		// to make this work, assume that block number == l1 info tree index == deposit count
		b.l1InfoTree.On("GetLastInfo").
			Return(lastL1Info, nil).
			Once()
		b.l1InfoTree.On("GetFirstInfo").
			Return(firstL1Info, nil).
			Once()
		infoAfterBlock := &l1infotreesync.L1InfoTreeLeaf{}
		b.l1InfoTree.On("GetFirstInfoAfterBlock", mock.Anything).
			Run(func(args mock.Arguments) {
				blockNum, ok := args.Get(0).(uint64)
				require.True(t, ok)
				infoAfterBlock.L1InfoTreeIndex = uint32(blockNum)
				infoAfterBlock.BlockNumber = blockNum
				infoAfterBlock.MainnetExitRoot = common.BytesToHash(aggkitcommon.Uint32ToBytes(uint32(blockNum)))
			}).
			Return(infoAfterBlock, nil)
		rootByLER := &tree.Root{}
		b.bridgeL1.On("GetRootByLER", ctx, mock.Anything).
			Run(func(args mock.Arguments) {
				ler, ok := args.Get(1).(common.Hash)
				require.True(t, ok)
				index := aggkitcommon.BytesToUint32(ler.Bytes()[28:]) // hash is 32 bytes, uint32 is just 4
				if ler == common.HexToHash("alfa") {
					index = uint32(lastL1Info.BlockNumber)
				}
				rootByLER.Index = index
			}).
			Return(rootByLER, nil)
	}
	testCases := []testCase{
		{
			description: "error on GetLastInfo",
			setupMocks: func() {
				b.l1InfoTree.On("GetLastInfo").
					Return(nil, fooErr).
					Once()
			},
			depositCount:  11,
			expectedIndex: 0,
			expectedErr:   fooErr,
		},
		{
			description: "error on first GetRootByLER",
			setupMocks: func() {
				b.l1InfoTree.On("GetLastInfo").
					Return(lastL1Info, nil).
					Once()
				b.bridgeL1.On("GetRootByLER", ctx, lastL1Info.MainnetExitRoot).
					Return(&tree.Root{}, fooErr).
					Once()
			},
			depositCount:  11,
			expectedIndex: 0,
			expectedErr:   fooErr,
		},
		{
			description: "not included yet",
			setupMocks: func() {
				b.l1InfoTree.On("GetLastInfo").
					Return(lastL1Info, nil).
					Once()
				b.bridgeL1.On("GetRootByLER", ctx, lastL1Info.MainnetExitRoot).
					Return(&tree.Root{Index: 10}, nil).
					Once()
			},
			depositCount:  11,
			expectedIndex: 0,
			expectedErr:   ErrNotOnL1Info,
		},
		{
			description: "error on GetFirstInfo",
			setupMocks: func() {
				b.l1InfoTree.On("GetLastInfo").
					Return(lastL1Info, nil).
					Once()
				b.bridgeL1.On("GetRootByLER", ctx, lastL1Info.MainnetExitRoot).
					Return(&tree.Root{Index: 13}, nil).
					Once()
				b.l1InfoTree.On("GetFirstInfo").
					Return(nil, fooErr).
					Once()
			},
			depositCount:  11,
			expectedIndex: 0,
			expectedErr:   fooErr,
		},
		{
			description: "error on GetFirstInfoAfterBlock",
			setupMocks: func() {
				b.l1InfoTree.On("GetLastInfo").
					Return(lastL1Info, nil).
					Once()
				b.bridgeL1.On("GetRootByLER", ctx, lastL1Info.MainnetExitRoot).
					Return(&tree.Root{Index: 13}, nil).
					Once()
				b.l1InfoTree.On("GetFirstInfo").
					Return(firstL1Info, nil).
					Once()
				b.l1InfoTree.On("GetFirstInfoAfterBlock", mock.Anything).
					Return(nil, fooErr).
					Once()
			},
			depositCount:  11,
			expectedIndex: 0,
			expectedErr:   fooErr,
		},
		{
			description: "error on GetRootByLER (inside binnary search)",
			setupMocks: func() {
				b.l1InfoTree.On("GetLastInfo").
					Return(lastL1Info, nil).
					Once()
				b.bridgeL1.On("GetRootByLER", ctx, lastL1Info.MainnetExitRoot).
					Return(&tree.Root{Index: 13}, nil).
					Once()
				b.l1InfoTree.On("GetFirstInfo").
					Return(firstL1Info, nil).
					Once()
				b.l1InfoTree.On("GetFirstInfoAfterBlock", mock.Anything).
					Return(firstL1Info, nil).
					Once()
				b.bridgeL1.On("GetRootByLER", ctx, mock.Anything).
					Return(&tree.Root{}, fooErr).
					Once()
			},
			depositCount:  11,
			expectedIndex: 0,
			expectedErr:   fooErr,
		},
		{
			description:   "happy path 1",
			setupMocks:    mockHappyPath,
			depositCount:  10,
			expectedIndex: 10,
			expectedErr:   nil,
		},
		{
			description:   "happy path 2",
			setupMocks:    mockHappyPath,
			depositCount:  11,
			expectedIndex: 11,
			expectedErr:   nil,
		},
		{
			description:   "happy path 3",
			setupMocks:    mockHappyPath,
			depositCount:  333,
			expectedIndex: 333,
			expectedErr:   nil,
		},
		{
			description:   "happy path 4",
			setupMocks:    mockHappyPath,
			depositCount:  420,
			expectedIndex: 420,
			expectedErr:   nil,
		},
		{
			description:   "happy path 5",
			setupMocks:    mockHappyPath,
			depositCount:  69,
			expectedIndex: 69,
			expectedErr:   nil,
		},
	}

	for _, tc := range testCases {
		log.Debugf("running test case: %s", tc.description)
		tc.setupMocks()
		actualIndex, err := b.bridge.getFirstL1InfoTreeIndexForL1Bridge(ctx, tc.depositCount)
		require.Equal(t, tc.expectedErr, err)
		require.Equal(t, tc.expectedIndex, actualIndex)
	}
}

func TestGetFirstL1InfoTreeIndexForL2Bridge(t *testing.T) {
	type testCase struct {
		description   string
		setupMocks    func()
		depositCount  uint32
		expectedIndex uint32
		expectedErr   error
	}
	ctx := context.Background()
	networkID := uint32(2)
	b := newBridgeWithMocks(t, networkID)
	fooErr := errors.New("foo")
	firstVerified := &l1infotreesync.VerifyBatches{
		BlockNumber: 10,
		ExitRoot:    common.HexToHash("alfa"),
	}
	lastVerified := &l1infotreesync.VerifyBatches{
		BlockNumber: 1000,
		ExitRoot:    common.HexToHash("alfa"),
	}
	mockHappyPath := func() {
		// to make this work, assume that block number == l1 info tree index == deposit count
		b.l1InfoTree.On("GetLastVerifiedBatches", uint32(1)).
			Return(lastVerified, nil).
			Once()
		b.l1InfoTree.On("GetFirstVerifiedBatches", uint32(1)).
			Return(firstVerified, nil).
			Once()
		verifiedAfterBlock := &l1infotreesync.VerifyBatches{}
		b.l1InfoTree.On("GetFirstVerifiedBatchesAfterBlock", uint32(1), mock.Anything).
			Run(func(args mock.Arguments) {
				blockNum, ok := args.Get(1).(uint64)
				require.True(t, ok)
				verifiedAfterBlock.BlockNumber = blockNum
				verifiedAfterBlock.ExitRoot = common.BytesToHash(aggkitcommon.Uint32ToBytes(uint32(blockNum)))
				verifiedAfterBlock.RollupExitRoot = common.BytesToHash(aggkitcommon.Uint32ToBytes(uint32(blockNum)))
			}).
			Return(verifiedAfterBlock, nil)
		rootByLER := &tree.Root{}
		b.bridgeL2.On("GetRootByLER", ctx, mock.Anything).
			Run(func(args mock.Arguments) {
				ler, ok := args.Get(1).(common.Hash)
				require.True(t, ok)
				index := aggkitcommon.BytesToUint32(ler.Bytes()[28:]) // hash is 32 bytes, uint32 is just 4
				if ler == common.HexToHash("alfa") {
					index = uint32(lastVerified.BlockNumber)
				}
				rootByLER.Index = index
			}).
			Return(rootByLER, nil)
		info := &l1infotreesync.L1InfoTreeLeaf{}
		b.l1InfoTree.On("GetFirstL1InfoWithRollupExitRoot", mock.Anything).
			Run(func(args mock.Arguments) {
				exitRoot, ok := args.Get(0).(common.Hash)
				require.True(t, ok)
				index := aggkitcommon.BytesToUint32(exitRoot.Bytes()[28:]) // hash is 32 bytes, uint32 is just 4
				info.L1InfoTreeIndex = index
			}).
			Return(info, nil).
			Once()
	}
	testCases := []testCase{
		{
			description: "error on GetLastVerified",
			setupMocks: func() {
				b.l1InfoTree.On("GetLastVerifiedBatches", uint32(1)).
					Return(nil, fooErr).
					Once()
			},
			depositCount:  11,
			expectedIndex: 0,
			expectedErr:   fooErr,
		},
		{
			description: "error on first GetRootByLER",
			setupMocks: func() {
				b.l1InfoTree.On("GetLastVerifiedBatches", uint32(1)).
					Return(lastVerified, nil).
					Once()
				b.bridgeL2.On("GetRootByLER", ctx, lastVerified.ExitRoot).
					Return(&tree.Root{}, fooErr).
					Once()
			},
			depositCount:  11,
			expectedIndex: 0,
			expectedErr:   fooErr,
		},
		{
			description: "not included yet",
			setupMocks: func() {
				b.l1InfoTree.On("GetLastVerifiedBatches", uint32(1)).
					Return(lastVerified, nil).
					Once()
				b.bridgeL2.On("GetRootByLER", ctx, lastVerified.ExitRoot).
					Return(&tree.Root{Index: 10}, nil).
					Once()
			},
			depositCount:  11,
			expectedIndex: 0,
			expectedErr:   ErrNotOnL1Info,
		},
		{
			description: "error on GetFirstVerified",
			setupMocks: func() {
				b.l1InfoTree.On("GetLastVerifiedBatches", uint32(1)).
					Return(lastVerified, nil).
					Once()
				b.bridgeL2.On("GetRootByLER", ctx, lastVerified.ExitRoot).
					Return(&tree.Root{Index: 13}, nil).
					Once()
				b.l1InfoTree.On("GetFirstVerifiedBatches", uint32(1)).
					Return(nil, fooErr).
					Once()
			},
			depositCount:  11,
			expectedIndex: 0,
			expectedErr:   fooErr,
		},
		{
			description: "error on GetFirstVerifiedBatchesAfterBlock",
			setupMocks: func() {
				b.l1InfoTree.On("GetLastVerifiedBatches", uint32(1)).
					Return(lastVerified, nil).
					Once()
				b.bridgeL2.On("GetRootByLER", ctx, lastVerified.ExitRoot).
					Return(&tree.Root{Index: 13}, nil).
					Once()
				b.l1InfoTree.On("GetFirstVerifiedBatches", uint32(1)).
					Return(firstVerified, nil).
					Once()
				b.l1InfoTree.On("GetFirstVerifiedBatchesAfterBlock", uint32(1), mock.Anything).
					Return(nil, fooErr).
					Once()
			},
			depositCount:  11,
			expectedIndex: 0,
			expectedErr:   fooErr,
		},
		{
			description: "error on GetRootByLER (inside binnary search)",
			setupMocks: func() {
				b.l1InfoTree.On("GetLastVerifiedBatches", uint32(1)).
					Return(lastVerified, nil).
					Once()
				b.bridgeL2.On("GetRootByLER", ctx, lastVerified.ExitRoot).
					Return(&tree.Root{Index: 13}, nil).
					Once()
				b.l1InfoTree.On("GetFirstVerifiedBatches", uint32(1)).
					Return(firstVerified, nil).
					Once()
				b.l1InfoTree.On("GetFirstVerifiedBatchesAfterBlock", uint32(1), mock.Anything).
					Return(firstVerified, nil).
					Once()
				b.bridgeL2.On("GetRootByLER", ctx, mock.Anything).
					Return(&tree.Root{}, fooErr).
					Once()
			},
			depositCount:  11,
			expectedIndex: 0,
			expectedErr:   fooErr,
		},
		{
			description:   "happy path 1",
			setupMocks:    mockHappyPath,
			depositCount:  10,
			expectedIndex: 10,
			expectedErr:   nil,
		},
		{
			description:   "happy path 2",
			setupMocks:    mockHappyPath,
			depositCount:  11,
			expectedIndex: 11,
			expectedErr:   nil,
		},
		{
			description:   "happy path 3",
			setupMocks:    mockHappyPath,
			depositCount:  333,
			expectedIndex: 333,
			expectedErr:   nil,
		},
		{
			description:   "happy path 4",
			setupMocks:    mockHappyPath,
			depositCount:  420,
			expectedIndex: 420,
			expectedErr:   nil,
		},
		{
			description:   "happy path 5",
			setupMocks:    mockHappyPath,
			depositCount:  69,
			expectedIndex: 69,
			expectedErr:   nil,
		},
	}

	for _, tc := range testCases {
		log.Debugf("running test case: %s", tc.description)
		tc.setupMocks()
		actualIndex, err := b.bridge.getFirstL1InfoTreeIndexForL2Bridge(ctx, tc.depositCount)
		require.Equal(t, tc.expectedErr, err)
		require.Equal(t, tc.expectedIndex, actualIndex)
	}
}

func TestGetTokenMappings(t *testing.T) {
	networkID := uint32(10)
	bridgeMocks := newBridgeWithMocks(t, networkID)

	t.Run("GetTokenMappings for L1 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)
		tokenMappings := []*bridgesync.TokenMapping{
			{
				BlockNum:            1,
				BlockPos:            1,
				BlockTimestamp:      1617184800,
				TxHash:              common.HexToHash("0x1"),
				OriginNetwork:       1,
				OriginTokenAddress:  common.HexToAddress("0x1"),
				WrappedTokenAddress: common.HexToAddress("0x2"),
				Metadata:            []byte("metadata"),
			},
		}

		bridgeMocks.bridgeL1.On("GetTokenMappings", mock.Anything, page, pageSize).
			Return(tokenMappings, len(tokenMappings), nil)

		result, err := bridgeMocks.bridge.GetTokenMappings(0, &page, &pageSize)
		require.NoError(t, err)
		require.NotNil(t, result)

		tokenMappingsResult, ok := result.(*TokenMappingsResult)
		require.True(t, ok)
		require.Equal(t, tokenMappings, tokenMappingsResult.TokenMappings)
		require.Equal(t, len(tokenMappingsResult.TokenMappings), tokenMappingsResult.Count)

		bridgeMocks.bridgeL1.AssertExpectations(t)
	})

	t.Run("GetTokenMappings for L2 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		tokenMappings := []*bridgesync.TokenMapping{
			{
				BlockNum:            1,
				BlockPos:            1,
				BlockTimestamp:      1617184800,
				TxHash:              common.HexToHash("0x1"),
				OriginNetwork:       1,
				OriginTokenAddress:  common.HexToAddress("0x1"),
				WrappedTokenAddress: common.HexToAddress("0x2"),
				Metadata:            []byte("metadata"),
			},
		}

		bridgeMocks.bridgeL2.On("GetTokenMappings", mock.Anything, page, pageSize).
			Return(tokenMappings, len(tokenMappings), nil)

		result, err := bridgeMocks.bridge.GetTokenMappings(networkID, &page, &pageSize)
		require.NoError(t, err)
		require.NotNil(t, result)

		tokenMappingsResult, ok := result.(*TokenMappingsResult)
		require.True(t, ok)
		require.Equal(t, tokenMappings, tokenMappingsResult.TokenMappings)
		require.Equal(t, len(tokenMappings), tokenMappingsResult.Count)

		bridgeMocks.bridgeL2.AssertExpectations(t)
	})

	t.Run("GetTokenMappings with unsupported network", func(t *testing.T) {
		unsupportedNetworkID := uint32(999)

		result, err := bridgeMocks.bridge.GetTokenMappings(unsupportedNetworkID, nil, nil)
		require.ErrorContains(t, err, fmt.Sprintf("failed to get token mappings, unsupported network %d", unsupportedNetworkID))
		require.Nil(t, result)
	})
}

type bridgeWithMocks struct {
	bridge       *BridgeEndpoints
	sponsor      *mocks.ClaimSponsorer
	l1InfoTree   *mocks.L1InfoTreer
	injectedGERs *mocks.LastGERer
	bridgeL1     *mocks.Bridger
	bridgeL2     *mocks.Bridger
}

func newBridgeWithMocks(t *testing.T, networkID uint32) bridgeWithMocks {
	t.Helper()
	b := bridgeWithMocks{
		sponsor:      mocks.NewClaimSponsorer(t),
		l1InfoTree:   mocks.NewL1InfoTreer(t),
		injectedGERs: mocks.NewLastGERer(t),
		bridgeL1:     mocks.NewBridger(t),
		bridgeL2:     mocks.NewBridger(t),
	}
	logger := log.WithFields("module", "bridgerpc")
	b.bridge = NewBridgeEndpoints(
		logger, 0, 0, networkID, b.sponsor, b.l1InfoTree, b.injectedGERs, b.bridgeL1, b.bridgeL2,
	)
	return b
}

func TestGetBridges(t *testing.T) {
	networkID := uint32(10)
	bridgeMocks := newBridgeWithMocks(t, networkID)

	t.Run("GetBridges for L1 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)
		bridges := []*bridgesync.BridgeResponse{
			{
				Bridge: bridgesync.Bridge{
					BlockNum:           1,
					BlockPos:           1,
					LeafType:           1,
					OriginNetwork:      0,
					OriginAddress:      common.HexToAddress("0x1"),
					DestinationNetwork: 10,
					DestinationAddress: common.HexToAddress("0x2"),
					Amount:             common.Big0,
					DepositCount:       0,
					Metadata:           []byte("metadata"),
				},
			},
		}

		bridgeMocks.bridgeL1.On("GetBridgesPaged", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(bridges, len(bridges), nil)

		result, err := bridgeMocks.bridge.GetBridges(0, &page, &pageSize, nil)
		require.NoError(t, err)
		require.NotNil(t, result)

		bridgesResult, ok := result.(BridgesResult)
		require.True(t, ok)
		require.Equal(t, bridges, bridgesResult.Bridges)
		require.Equal(t, len(bridgesResult.Bridges), bridgesResult.Count)

		bridgeMocks.bridgeL1.AssertExpectations(t)
	})

	t.Run("GetBridges for L2 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)
		bridges := []*bridgesync.BridgeResponse{
			{
				Bridge: bridgesync.Bridge{
					BlockNum:           1,
					BlockPos:           1,
					LeafType:           1,
					OriginNetwork:      0,
					OriginAddress:      common.HexToAddress("0x1"),
					DestinationNetwork: 10,
					DestinationAddress: common.HexToAddress("0x2"),
					Amount:             common.Big0,
					DepositCount:       0,
					Metadata:           []byte("metadata"),
				},
			},
		}
		bridgeMocks.bridge.networkID = 10

		bridgeMocks.bridgeL2.On("GetBridgesPaged", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(bridges, len(bridges), nil)

		result, err := bridgeMocks.bridge.GetBridges(10, &page, &pageSize, nil)
		require.NoError(t, err)
		require.NotNil(t, result)

		bridgesResult, ok := result.(BridgesResult)
		require.True(t, ok)
		require.Equal(t, bridges, bridgesResult.Bridges)
		require.Equal(t, len(bridgesResult.Bridges), bridgesResult.Count)

		bridgeMocks.bridgeL2.AssertExpectations(t)
	})

	t.Run("GetBridges with unsupported network", func(t *testing.T) {
		unsupportedNetworkID := uint32(999)

		result, err := bridgeMocks.bridge.GetBridges(unsupportedNetworkID, nil, nil, nil)
		require.ErrorContains(t, err, fmt.Sprintf("this client does not support network %d", unsupportedNetworkID))
		require.Nil(t, result)
	})
}
