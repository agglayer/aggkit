package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-rpc/rpc"
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

var (
	fooErrMsg = "foo"
	barErrMsg = "bar"
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
	fooErr := errors.New(fooErrMsg)
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
		b.l1InfoTree.EXPECT().GetLastInfo().
			Return(lastL1Info, nil).
			Once()
		b.l1InfoTree.EXPECT().GetFirstInfo().
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
				b.l1InfoTree.EXPECT().GetLastInfo().
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
				b.l1InfoTree.EXPECT().GetLastInfo().
					Return(lastL1Info, nil).
					Once()
				b.bridgeL1.EXPECT().GetRootByLER(ctx, lastL1Info.MainnetExitRoot).
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
				b.l1InfoTree.EXPECT().GetLastInfo().
					Return(lastL1Info, nil).
					Once()
				b.bridgeL1.EXPECT().GetRootByLER(ctx, lastL1Info.MainnetExitRoot).
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
				b.l1InfoTree.EXPECT().GetLastInfo().
					Return(lastL1Info, nil).
					Once()
				b.bridgeL1.EXPECT().GetRootByLER(ctx, lastL1Info.MainnetExitRoot).
					Return(&tree.Root{Index: 13}, nil).
					Once()
				b.l1InfoTree.EXPECT().GetFirstInfo().
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
				b.l1InfoTree.EXPECT().GetLastInfo().
					Return(lastL1Info, nil).
					Once()
				b.bridgeL1.EXPECT().GetRootByLER(ctx, lastL1Info.MainnetExitRoot).
					Return(&tree.Root{Index: 13}, nil).
					Once()
				b.l1InfoTree.EXPECT().GetFirstInfo().
					Return(firstL1Info, nil).
					Once()
				b.l1InfoTree.EXPECT().GetFirstInfoAfterBlock(mock.Anything).
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
				b.l1InfoTree.EXPECT().GetLastInfo().
					Return(lastL1Info, nil).
					Once()
				b.bridgeL1.EXPECT().GetRootByLER(ctx, lastL1Info.MainnetExitRoot).
					Return(&tree.Root{Index: 13}, nil).
					Once()
				b.l1InfoTree.EXPECT().GetFirstInfo().
					Return(firstL1Info, nil).
					Once()
				b.l1InfoTree.EXPECT().GetFirstInfoAfterBlock(mock.Anything).
					Return(firstL1Info, nil).
					Once()
				b.bridgeL1.EXPECT().GetRootByLER(ctx, mock.Anything).
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
		log.Debugf("running test case: %s(tc.description)")
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
		ExitRoot:    common.HexToHash("a1fa"),
	}
	lastVerified := &l1infotreesync.VerifyBatches{
		BlockNumber: 1000,
		ExitRoot:    common.HexToHash("a1fa"),
	}
	mockHappyPath := func() {
		// to make this work, assume that block number == l1 info tree index == deposit count
		b.l1InfoTree.EXPECT().GetLastVerifiedBatches(networkID).
			Return(lastVerified, nil).
			Once()
		b.l1InfoTree.EXPECT().GetFirstVerifiedBatches(networkID).
			Return(firstVerified, nil).
			Once()
		verifiedAfterBlock := &l1infotreesync.VerifyBatches{}
		b.l1InfoTree.On("GetFirstVerifiedBatchesAfterBlock", networkID, mock.Anything).
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
				if ler == common.HexToHash("a1fa") {
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
				b.l1InfoTree.EXPECT().GetLastVerifiedBatches(networkID).
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
				b.l1InfoTree.EXPECT().GetLastVerifiedBatches(networkID).
					Return(lastVerified, nil).
					Once()
				b.bridgeL2.EXPECT().GetRootByLER(ctx, lastVerified.ExitRoot).
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
				b.l1InfoTree.EXPECT().GetLastVerifiedBatches(networkID).
					Return(lastVerified, nil).
					Once()
				b.bridgeL2.EXPECT().GetRootByLER(ctx, lastVerified.ExitRoot).
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
				b.l1InfoTree.EXPECT().GetLastVerifiedBatches(networkID).
					Return(lastVerified, nil).
					Once()
				b.bridgeL2.EXPECT().GetRootByLER(ctx, lastVerified.ExitRoot).
					Return(&tree.Root{Index: 13}, nil).
					Once()
				b.l1InfoTree.EXPECT().GetFirstVerifiedBatches(networkID).
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
				b.l1InfoTree.EXPECT().GetLastVerifiedBatches(networkID).
					Return(lastVerified, nil).
					Once()
				b.bridgeL2.EXPECT().GetRootByLER(ctx, lastVerified.ExitRoot).
					Return(&tree.Root{Index: 13}, nil).
					Once()
				b.l1InfoTree.EXPECT().GetFirstVerifiedBatches(networkID).
					Return(firstVerified, nil).
					Once()
				b.l1InfoTree.EXPECT().GetFirstVerifiedBatchesAfterBlock(networkID, mock.Anything).
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
				b.l1InfoTree.EXPECT().GetLastVerifiedBatches(networkID).
					Return(lastVerified, nil).
					Once()
				b.bridgeL2.EXPECT().GetRootByLER(ctx, lastVerified.ExitRoot).
					Return(&tree.Root{Index: 13}, nil).
					Once()
				b.l1InfoTree.EXPECT().GetFirstVerifiedBatches(networkID).
					Return(firstVerified, nil).
					Once()
				b.l1InfoTree.EXPECT().GetFirstVerifiedBatchesAfterBlock(networkID, mock.Anything).
					Return(firstVerified, nil).
					Once()
				b.bridgeL2.EXPECT().GetRootByLER(ctx, mock.Anything).
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
		log.Debugf("running test case: %s(tc.description)")
		tc.setupMocks()
		actualIndex, err := b.bridge.getFirstL1InfoTreeIndexForL2Bridge(ctx, tc.depositCount)
		require.Equal(t, tc.expectedErr, err)
		require.Equal(t, tc.expectedIndex, actualIndex)
	}
}

func TestGetTokenMappings(t *testing.T) {
	l2NetworkID := uint32(10)
	bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

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
				Metadata:            common.Hex2Bytes("abcd"),
				Calldata:            common.Hex2Bytes("efabcd"),
			},
		}

		bridgeMocks.bridgeL1.EXPECT().GetTokenMappings(mock.Anything, page, pageSize).
			Return(tokenMappings, len(tokenMappings), nil)

		result, err := bridgeMocks.bridge.GetTokenMappings(0, &page, &pageSize)
		require.NoError(t, err)
		require.NotNil(t, result)

		tokenMappingsResult, ok := result.(*TokenMappingsResult)
		require.True(t, ok)
		require.Equal(t, tokenMappings, tokenMappingsResult.TokenMappings)
		require.Equal(t, len(tokenMappingsResult.TokenMappings), tokenMappingsResult.Count)

		actualJSON, marshalErr := json.Marshal(tokenMappingsResult.TokenMappings)
		require.NoError(t, marshalErr)

		expectedJSON, marshalErr := json.Marshal(tokenMappings)
		require.NoError(t, marshalErr)

		require.JSONEq(t, string(expectedJSON), string(actualJSON))

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
				Type:                bridgesync.SovereignToken,
				IsNotMintable:       true,
				Metadata:            []byte("metadata"),
			},
		}

		bridgeMocks.bridgeL2.EXPECT().GetTokenMappings(mock.Anything, page, pageSize).
			Return(tokenMappings, len(tokenMappings), nil)

		result, err := bridgeMocks.bridge.GetTokenMappings(l2NetworkID, &page, &pageSize)
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
		require.Equal(t, rpc.InvalidRequestErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("failed to get token mappings, unsupported network %d", unsupportedNetworkID))
		require.Nil(t, result)
	})

	t.Run("GetTokenMappings for L1 network failed", func(t *testing.T) {
		bridgeMocks.bridgeL1.EXPECT().GetTokenMappings(mock.Anything, mock.Anything, mock.Anything).Return(nil, 0, errors.New(fooErrMsg))

		result, err := bridgeMocks.bridge.GetTokenMappings(mainnetNetworkID, nil, nil)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("failed to get token mappings for the L1 network, error: %s", fooErrMsg))
		require.Nil(t, result)
	})

	t.Run("GetTokenMappings for L2 network failed", func(t *testing.T) {
		bridgeMocks.bridgeL2.EXPECT().GetTokenMappings(mock.Anything, mock.Anything, mock.Anything).Return(nil, 0, errors.New(barErrMsg))

		result, err := bridgeMocks.bridge.GetTokenMappings(l2NetworkID, nil, nil)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("failed to get token mappings for the L2 network (ID=%d), error: %s",
			l2NetworkID, barErrMsg))
		require.Nil(t, result)
	})
}

func TestGetLegacyTokenMigrations(t *testing.T) {
	l2NetworkID := uint32(10)
	bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

	t.Run("GetLegacyTokenMigrations for L1 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)
		tokenMigrations := []*bridgesync.LegacyTokenMigration{
			{
				BlockNum:            1,
				BlockPos:            1,
				BlockTimestamp:      1617184800,
				TxHash:              common.HexToHash("0x1"),
				Sender:              common.HexToAddress("0x2"),
				LegacyTokenAddress:  common.HexToAddress("0x3"),
				UpdatedTokenAddress: common.HexToAddress("0x4"),
				Amount:              big.NewInt(100),
				Calldata:            common.Hex2Bytes("efabcd"),
			},
		}

		bridgeMocks.bridgeL1.EXPECT().GetLegacyTokenMigrations(mock.Anything, page, pageSize).
			Return(tokenMigrations, len(tokenMigrations), nil)

		result, err := bridgeMocks.bridge.GetLegacyTokenMigrations(0, &page, &pageSize)
		require.NoError(t, err)
		require.NotNil(t, result)

		tokenMigrationsRes, ok := result.(*LegacyTokenMigrationsResult)
		require.True(t, ok)
		require.Equal(t, tokenMigrations, tokenMigrationsRes.TokenMigrations)
		require.Equal(t, len(tokenMigrationsRes.TokenMigrations), tokenMigrationsRes.Count)

		actualJSON, marshalErr := json.Marshal(tokenMigrationsRes.TokenMigrations)
		require.NoError(t, marshalErr)

		expectedJSON, marshalErr := json.Marshal(tokenMigrations)
		require.NoError(t, marshalErr)

		require.JSONEq(t, string(expectedJSON), string(actualJSON))

		bridgeMocks.bridgeL1.AssertExpectations(t)
	})

	t.Run("GetLegacyTokenMigrations for L2 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		tokenMigrations := []*bridgesync.LegacyTokenMigration{
			{
				BlockNum:            1,
				BlockPos:            1,
				BlockTimestamp:      1617184800,
				TxHash:              common.HexToHash("0x10"),
				Sender:              common.HexToAddress("0x20"),
				LegacyTokenAddress:  common.HexToAddress("0x30"),
				UpdatedTokenAddress: common.HexToAddress("0x40"),
				Amount:              big.NewInt(10),
			},
		}

		bridgeMocks.bridgeL2.EXPECT().GetLegacyTokenMigrations(mock.Anything, page, pageSize).
			Return(tokenMigrations, len(tokenMigrations), nil)

		result, err := bridgeMocks.bridge.GetLegacyTokenMigrations(l2NetworkID, &page, &pageSize)
		require.NoError(t, err)
		require.NotNil(t, result)

		tokenMigrationsRes, ok := result.(*LegacyTokenMigrationsResult)
		require.True(t, ok)
		require.Equal(t, tokenMigrations, tokenMigrationsRes.TokenMigrations)
		require.Equal(t, len(tokenMigrations), tokenMigrationsRes.Count)

		bridgeMocks.bridgeL2.AssertExpectations(t)
	})

	t.Run("GetLegacyTokenMigrations with unsupported network", func(t *testing.T) {
		unsupportedNetworkID := uint32(999)

		result, err := bridgeMocks.bridge.GetLegacyTokenMigrations(unsupportedNetworkID, nil, nil)
		require.Equal(t, rpc.InvalidRequestErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("failed to get legacy token migrations, unsupported network %d", unsupportedNetworkID))
		require.Nil(t, result)
	})

	t.Run("GetLegacyTokenMigrations for L1 network failed", func(t *testing.T) {
		bridgeMocks.bridgeL1.EXPECT().
			GetLegacyTokenMigrations(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(fooErrMsg))

		result, err := bridgeMocks.bridge.GetLegacyTokenMigrations(mainnetNetworkID, nil, nil)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("failed to get legacy token migrations for the L1 network, error: %s", fooErrMsg))
		require.Nil(t, result)
	})

	t.Run("GetLegacyTokenMigrations for L2 network failed", func(t *testing.T) {
		bridgeMocks.bridgeL2.EXPECT().
			GetLegacyTokenMigrations(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(barErrMsg))

		result, err := bridgeMocks.bridge.GetLegacyTokenMigrations(l2NetworkID, nil, nil)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("failed to get legacy token migrations for L2 network (ID=%d), error: %s",
			l2NetworkID, barErrMsg))
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

func (b *bridgeWithMocks) setBridgeL1(l1Bridger *mocks.Bridger) {
	b.bridgeL1 = l1Bridger
	b.bridge.bridgeL1 = l1Bridger
}

func (b *bridgeWithMocks) setBridgeL2(l2Bridger *mocks.Bridger) {
	b.bridgeL2 = l2Bridger
	b.bridge.bridgeL2 = l2Bridger
}

func TestGetBridges(t *testing.T) {
	l2NetworkID := uint32(10)
	bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

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
					Metadata:           common.Hex2Bytes("deadbeef"),
					Calldata:           common.Hex2Bytes("abcd"),
				},
				BridgeHash: common.HexToHash("0x1"),
			},
		}

		bridgeMocks.bridgeL1.EXPECT().
			GetBridgesPaged(mock.Anything, page, pageSize, mock.Anything).
			Return(bridges, len(bridges), nil)

		result, err := bridgeMocks.bridge.GetBridges(0, &page, &pageSize, nil)
		require.NoError(t, err)
		require.NotNil(t, result)

		bridgesResult, ok := result.(BridgesResult)
		require.True(t, ok)
		require.Equal(t, bridges, bridgesResult.Bridges)
		require.Equal(t, len(bridgesResult.Bridges), bridgesResult.Count)

		actualJSON, marshalErr := json.Marshal(bridgesResult.Bridges)
		require.NoError(t, marshalErr)

		expectedJSON, marshalErr := json.Marshal(bridges)
		require.NoError(t, marshalErr)

		require.JSONEq(t, string(expectedJSON), string(actualJSON))

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
				BridgeHash: common.HexToHash("0x1"),
			},
		}

		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesPaged(mock.Anything, page, pageSize, mock.Anything).
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
		require.Equal(t, rpc.InvalidRequestErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("this client does not support network %d", unsupportedNetworkID))
		require.Nil(t, result)
	})

	t.Run("GetBridges for L1 network failed", func(t *testing.T) {
		bridgeMocks.bridgeL1.EXPECT().
			GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(fooErrMsg))

		result, err := bridgeMocks.bridge.GetBridges(mainnetNetworkID, nil, nil, nil)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("failed to get bridges for the L1 network, error: %s", fooErrMsg))
		require.Nil(t, result)
	})

	t.Run("GetBridges for L2 network failed", func(t *testing.T) {
		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(barErrMsg))

		result, err := bridgeMocks.bridge.GetBridges(l2NetworkID, nil, nil, nil)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("failed to get bridges for the L2 network (ID=%d), error: %s",
			l2NetworkID, barErrMsg))
		require.Nil(t, result)
	})
}

func TestGetClaims(t *testing.T) {
	l2NetworkID := uint32(10)
	bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

	t.Run("GetClaims for L1 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)
		claims := []*bridgesync.ClaimResponse{
			{
				BlockNum:           1,
				GlobalIndex:        big.NewInt(1),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x1"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0x2"),
				Amount:             common.Big0,
			},
		}

		bridgeMocks.bridgeL1.EXPECT().GetClaimsPaged(mock.Anything, page, pageSize).
			Return(claims, len(claims), nil)

		result, err := bridgeMocks.bridge.GetClaims(0, &page, &pageSize)
		require.NoError(t, err)
		require.NotNil(t, result)

		claimsResult, ok := result.(ClaimsResult)
		require.True(t, ok)
		require.Equal(t, claims, claimsResult.Claims)
		require.Equal(t, len(claimsResult.Claims), claimsResult.Count)

		bridgeMocks.bridgeL1.AssertExpectations(t)
	})

	t.Run("GetClaims for L2 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)
		Claims := []*bridgesync.ClaimResponse{
			{
				BlockNum:           1,
				GlobalIndex:        big.NewInt(1),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x1"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0x2"),
				Amount:             common.Big0,
			},
		}

		bridgeMocks.bridge.networkID = 10

		bridgeMocks.bridgeL2.EXPECT().GetClaimsPaged(mock.Anything, page, pageSize).
			Return(Claims, len(Claims), nil)

		result, err := bridgeMocks.bridge.GetClaims(10, &page, &pageSize)
		require.NoError(t, err)
		require.NotNil(t, result)

		claimsResult, ok := result.(ClaimsResult)
		require.True(t, ok)
		require.Equal(t, Claims, claimsResult.Claims)
		require.Equal(t, len(claimsResult.Claims), claimsResult.Count)

		bridgeMocks.bridgeL2.AssertExpectations(t)
	})

	t.Run("GetClaims with unsupported network", func(t *testing.T) {
		unsupportedNetworkID := uint32(999)

		result, err := bridgeMocks.bridge.GetClaims(unsupportedNetworkID, nil, nil)
		require.Equal(t, rpc.InvalidRequestErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("this client does not support network %d", unsupportedNetworkID))
		require.Nil(t, result)
	})

	t.Run("GetClaims for L1 network failed", func(t *testing.T) {
		bridgeMocks.bridgeL1.EXPECT().GetClaimsPaged(mock.Anything, mock.Anything, mock.Anything).Return(nil, 0, errors.New(fooErrMsg))

		result, err := bridgeMocks.bridge.GetClaims(mainnetNetworkID, nil, nil)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("failed to get claims for the L1 network, error: %s", fooErrMsg))
		require.Nil(t, result)
	})

	t.Run("GetClaims for L2 network failed", func(t *testing.T) {
		bridgeMocks.bridgeL2.EXPECT().GetClaimsPaged(mock.Anything, mock.Anything, mock.Anything).Return(nil, 0, errors.New(barErrMsg))

		result, err := bridgeMocks.bridge.GetClaims(l2NetworkID, nil, nil)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("failed to get claims for the L2 network (ID=%d), error: %s",
			l2NetworkID, barErrMsg))
		require.Nil(t, result)
	})
}

func TestGetLastReorgEvent(t *testing.T) {
	l2NetworkID := uint32(10)
	bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

	t.Run("GetLastReorgEvent for L1 network", func(t *testing.T) {
		reorgEvent := &bridgesync.LastReorg{
			DetectedAt: 1710000000,
			FromBlock:  100,
			ToBlock:    200,
		}

		bridgeMocks.bridgeL1.EXPECT().GetLastReorgEvent(mock.Anything).Return(reorgEvent, nil)

		result, err := bridgeMocks.bridge.GetLastReorgEvent(0)
		require.NoError(t, err)
		require.NotNil(t, result)

		actualReorgEvent, ok := result.(*bridgesync.LastReorg)
		require.True(t, ok)
		require.Equal(t, reorgEvent, actualReorgEvent)

		bridgeMocks.bridgeL1.AssertExpectations(t)
	})

	t.Run("GetLastReorgEvent for L2 network", func(t *testing.T) {
		reorgEvent := &bridgesync.LastReorg{
			DetectedAt: 1710000001,
			FromBlock:  200,
			ToBlock:    300,
		}

		bridgeMocks.bridgeL2.EXPECT().GetLastReorgEvent(mock.Anything).Return(reorgEvent, nil)

		result, err := bridgeMocks.bridge.GetLastReorgEvent(10)
		require.NoError(t, err)
		require.NotNil(t, result)

		actualReorgEvent, ok := result.(*bridgesync.LastReorg)
		require.True(t, ok)
		require.Equal(t, reorgEvent, actualReorgEvent)

		bridgeMocks.bridgeL2.AssertExpectations(t)
	})

	t.Run("GetLastReorgEvent with unsupported network", func(t *testing.T) {
		unsupportedNetworkID := uint32(999)

		result, err := bridgeMocks.bridge.GetLastReorgEvent(unsupportedNetworkID)
		require.Equal(t, rpc.InvalidRequestErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("this client does not support network %d", unsupportedNetworkID))
		require.Nil(t, result)
	})

	t.Run("GetLastReorgEvent for L1 network failed", func(t *testing.T) {
		bridgeMocks.setBridgeL1(mocks.NewBridger(t))
		bridgeMocks.bridgeL1.EXPECT().GetLastReorgEvent(mock.Anything).Return(nil, errors.New(fooErrMsg))

		result, err := bridgeMocks.bridge.GetLastReorgEvent(mainnetNetworkID)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("failed to get last reorg event for the L1 network, error: %s", fooErrMsg))
		require.Nil(t, result)
	})

	t.Run("GetLastReorgEvent for L2 network failed", func(t *testing.T) {
		bridgeMocks.setBridgeL2(mocks.NewBridger(t))
		bridgeMocks.bridgeL2.EXPECT().GetLastReorgEvent(mock.Anything).Return(nil, errors.New(barErrMsg))

		result, err := bridgeMocks.bridge.GetLastReorgEvent(l2NetworkID)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("failed to get last reorg event for the L2 network (ID=%d), error: %s", l2NetworkID, barErrMsg))
		require.Nil(t, result)
	})
}
