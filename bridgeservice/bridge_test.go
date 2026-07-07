//nolint:lll,gosec // test file; long mock/assertion lines and test timestamp conversions are not security-sensitive
package bridgeservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	mocks "github.com/agglayer/aggkit/bridgeservice/mocks"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	"github.com/agglayer/aggkit/log"
	merkletree "github.com/agglayer/aggkit/tree"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	fooErrMsg   = "foo"
	barErrMsg   = "bar"
	l2NetworkID = uint32(10)
)

type bridgeWithMocks struct {
	bridge         *BridgeService
	router         *gin.Engine
	upgradeQuerier *mocks.AgglayerManagerUpgradeQuerier
	l1InfoTree     *mocks.L1InfoTreeSyncer
	injectedGERs   *mocks.L2GERSyncer
	bridgeL1       *mocks.Bridger
	claimL1        *mocks.Claimer
	bridgeL2       *mocks.Bridger
	claimL2        *mocks.Claimer
}

func newBridgeWithMocks(t *testing.T, networkID uint32) bridgeWithMocks {
	t.Helper()
	gin.SetMode(gin.TestMode)
	b := bridgeWithMocks{
		upgradeQuerier: mocks.NewAgglayerManagerUpgradeQuerier(t),
		l1InfoTree:     mocks.NewL1InfoTreeSyncer(t),
		injectedGERs:   mocks.NewL2GERSyncer(t),
		bridgeL1:       mocks.NewBridger(t),
		claimL1:        mocks.NewClaimer(t),
		bridgeL2:       mocks.NewBridger(t),
		claimL2:        mocks.NewClaimer(t),
	}
	logger := log.WithFields("module", "test bridge service")
	cfg := &Config{
		Logger:       logger,
		ReadTimeout:  0,
		WriteTimeout: 0,
		NetworkID:    networkID,
	}
	b.bridge = New(cfg, b.upgradeQuerier, b.l1InfoTree, b.injectedGERs,
		b.bridgeL1, b.claimL1, b.bridgeL2, b.claimL2)
	b.router = gin.New()
	b.bridge.RegisterRoutes(b.router)
	return b
}

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
			description: "error on first GetRootByLER and GetLastRoot",
			setupMocks: func() {
				b.l1InfoTree.EXPECT().GetLastInfo().
					Return(lastL1Info, nil).
					Once()
				b.bridgeL1.EXPECT().GetRootByLER(ctx, lastL1Info.MainnetExitRoot).
					Return(&tree.Root{}, fooErr).
					Once()
				// With the fallback logic, it will try GetLastRoot when GetRootByLER fails
				b.bridgeL1.EXPECT().GetLastRoot(ctx).
					Return(nil, fooErr).
					Once()
			},
			depositCount:  11,
			expectedIndex: 0,
			expectedErr:   fmt.Errorf("failed to get last root for L1: %w", fooErr),
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
			description: "error on GetRootByLER (inside binary search)",
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
		{
			description: "nil L1 syncer",
			setupMocks: func() {
				b.bridge.bridgeL1 = nil
			},
			depositCount:  100,
			expectedIndex: 0,
			expectedErr:   errors.New("L1 bridge syncer is not available"),
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
			description: "non not found error on first GetRootByLER",
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
			expectedErr:   fmt.Errorf("failed to get root by LER for L2: %w", fooErr),
		},
		{
			description: "latest verified LER missing and last local L2 root is behind deposit",
			setupMocks: func() {
				b.l1InfoTree.EXPECT().GetLastVerifiedBatches(networkID).
					Return(lastVerified, nil).
					Once()
				b.bridgeL2.EXPECT().GetRootByLER(ctx, lastVerified.ExitRoot).
					Return(&tree.Root{}, db.ErrNotFound).
					Once()
				b.bridgeL2.EXPECT().GetLastRoot(ctx).
					Return(&tree.Root{}, nil).
					Once()
			},
			depositCount:  11,
			expectedIndex: 0,
			expectedErr:   ErrNotOnL1Info,
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
			description: "error on GetRootByLER (inside binary search)",
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
		{
			description: "nil L2 syncer",
			setupMocks: func() {
				b.bridge.bridgeL2 = nil
			},
			depositCount:  100,
			expectedIndex: 0,
			expectedErr:   errors.New("L2 bridge syncer is not available"),
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

func TestGetFirstL1InfoTreeIndexForL2Bridge_MissingLatestLERDoesNotUseL2BlockAsL1Block(t *testing.T) {
	ctx := context.Background()
	networkID := uint32(2)
	b := newBridgeWithMocks(t, networkID)

	validExitRoot := common.HexToHash("0x1000")
	validRollupExitRoot := common.HexToHash("0x2000")
	missingExitRoot := common.HexToHash("0x3000")
	missingRollupExitRoot := common.HexToHash("0x4000")

	firstVerified := &l1infotreesync.VerifyBatches{
		BlockNumber:    10,
		ExitRoot:       validExitRoot,
		RollupExitRoot: validRollupExitRoot,
	}
	lastVerified := &l1infotreesync.VerifyBatches{
		BlockNumber:    100,
		ExitRoot:       missingExitRoot,
		RollupExitRoot: missingRollupExitRoot,
	}

	b.l1InfoTree.EXPECT().GetLastVerifiedBatches(networkID).
		Return(lastVerified, nil).
		Once()
	b.bridgeL2.EXPECT().GetRootByLER(ctx, missingExitRoot).
		Return(&tree.Root{}, db.ErrNotFound).
		Once()
	b.bridgeL2.EXPECT().GetLastRoot(ctx).
		Return(&tree.Root{Index: 10, BlockNum: 1_000_000}, nil).
		Once()
	b.l1InfoTree.EXPECT().GetFirstVerifiedBatches(networkID).
		Return(firstVerified, nil).
		Once()

	verifiedAfterBlock := &l1infotreesync.VerifyBatches{}
	b.l1InfoTree.On("GetFirstVerifiedBatchesAfterBlock", networkID, mock.Anything).
		Run(func(args mock.Arguments) {
			blockNum, ok := args.Get(1).(uint64)
			require.True(t, ok)
			require.LessOrEqual(t, blockNum, lastVerified.BlockNumber)
			if blockNum <= firstVerified.BlockNumber {
				*verifiedAfterBlock = *firstVerified
			} else {
				*verifiedAfterBlock = *lastVerified
			}
		}).
		Return(verifiedAfterBlock, nil)

	b.bridgeL2.EXPECT().GetRootByLER(ctx, validExitRoot).
		Return(&tree.Root{Index: 10}, nil).
		Once()
	b.bridgeL2.On("GetRootByLER", ctx, missingExitRoot).
		Return(&tree.Root{}, db.ErrNotFound)

	expectedInfo := &l1infotreesync.L1InfoTreeLeaf{L1InfoTreeIndex: 77}
	b.l1InfoTree.EXPECT().GetFirstL1InfoWithRollupExitRoot(validRollupExitRoot).
		Return(expectedInfo, nil).
		Once()

	actualIndex, err := b.bridge.getFirstL1InfoTreeIndexForL2Bridge(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, expectedInfo.L1InfoTreeIndex, actualIndex)
}

func TestGetBridgesHandler(t *testing.T) {
	t.Run("GetBridges for L1 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		expectedBridges := []*bridgesync.Bridge{
			{
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
				TxnSender:          common.HexToAddress("0x5555555555555555555555555555555555555555"),
				ToAddress:          common.HexToAddress("0xF9D64d54D32EE2BDceAAbFA60C4C438E224427d0"),
			},
		}

		bridgeResponses := make([]*bridgetypes.BridgeResponse, 0, len(expectedBridges))
		for _, bridge := range expectedBridges {
			bridgeResponses = append(bridgeResponses, NewBridgeResponse(bridge, mainnetNetworkID, 0))
		}

		bridgeMocks.bridgeL1.EXPECT().
			GetBridgesPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything, mock.Anything).
			Return(expectedBridges, len(expectedBridges), nil)

		bridgeMocks.upgradeQuerier.EXPECT().
			GetUpgradeBlock(mock.Anything, mock.Anything).
			Return(uint64(0)).
			Once()

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(mainnetNetworkID))
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.BridgesResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		require.Equal(t, bridgeResponses, response.Bridges)
		require.Equal(t, len(expectedBridges), response.Count)

		// Verify to_address is present in the response
		require.NotNil(t, response.Bridges)
		require.Len(t, response.Bridges, 1)
		require.Equal(t, bridgetypes.Address("0xF9D64d54D32EE2BDceAAbFA60C4C438E224427d0"), response.Bridges[0].ToAddress)
	})

	t.Run("GetBridges for L1 network error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL1.EXPECT().
			GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, fmt.Errorf("L1 network error"))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(mainnetNetworkID))
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get bridges for the L1 network")
	})

	t.Run("GetBridges for L2 network error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, fmt.Errorf("L2 network error"))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get bridges for the L2 network")
	})

	t.Run("GetBridges for L2 network", func(t *testing.T) {
		const etrogBlockUpgrade = uint64(100)
		page := uint32(1)
		pageSize := uint32(10)

		expectedBridges := []*bridgesync.Bridge{
			{
				BlockNum:           1,
				BlockPos:           1,
				LeafType:           1,
				OriginNetwork:      10,
				OriginAddress:      common.HexToAddress("0x2"),
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x3"),
				Amount:             common.Big0,
				DepositCount:       1,
				Metadata:           []byte("metadata"),
			},
		}

		bridgeResponses := make([]*bridgetypes.BridgeResponse, 0, len(expectedBridges))
		for _, bridge := range expectedBridges {
			bridgeResponses = append(bridgeResponses, NewBridgeResponse(bridge, l2NetworkID, etrogBlockUpgrade))
		}

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything, mock.Anything).
			Return(expectedBridges, len(expectedBridges), nil)

		bridgeMocks.upgradeQuerier.EXPECT().
			GetUpgradeBlock(mock.Anything, mock.Anything).
			Return(etrogBlockUpgrade).
			Once()

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.BridgesResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		require.Equal(t, bridgeResponses, response.Bridges)
		require.Equal(t, len(expectedBridges), response.Count)
	})

	t.Run("GetBridges with unsupported network", func(t *testing.T) {
		unsupportedNetworkID := uint32(999)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{networkIDParam: []string{fmt.Sprintf("%d", unsupportedNetworkID)}}
		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("unsupported network id: %d", unsupportedNetworkID))
	})

	t.Run("GetBridges invalid network id", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{networkIDParam: []string{"foo"}}
		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
	})

	t.Run("GetBridges invalid page number parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{
			networkIDParam:  []string{strconv.Itoa(mainnetNetworkID)},
			pageNumberParam: []string{"foo"},
		}
		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", pageNumberParam))
	})

	t.Run("GetBridges invalid deposit count parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{
			networkIDParam:    []string{strconv.Itoa(mainnetNetworkID)},
			depositCountParam: []string{"foo"},
		}
		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", depositCountParam))
	})

	t.Run("GetBridges invalid network ids parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{
			networkIDParam:  []string{strconv.Itoa(mainnetNetworkID)},
			networkIDsParam: []string{"foo", "bar"},
		}
		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDsParam))
	})

	t.Run("GetBridges for L1 network with nil L1 syncer", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL1 = nil

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(mainnetNetworkID))
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)

		var response gin.H
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, "L1 bridge syncer is not available", response["error"])
	})

	t.Run("GetBridges for L2 network with nil L2 syncer", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL2 = nil

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)

		var response gin.H
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, "L2 bridge syncer is not available", response["error"])
	})
}

func TestGetClaimsHandler(t *testing.T) {
	t.Run("GetClaims for L1 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		expectedClaims := []*claimsynctypes.Claim{
			{
				BlockNum:           1,
				GlobalIndex:        big.NewInt(1),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x1"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0x2"),
				Amount:             common.Big0,
				MainnetExitRoot:    common.HexToHash("0xdefc...789"),
			},
		}
		claimsResp := aggkitcommon.MapSlice(expectedClaims, func(claim *claimsynctypes.Claim) *bridgetypes.ClaimResponse {
			return NewClaimResponse(claim, false)
		})

		bridgeMocks.claimL1.EXPECT().
			GetClaimsPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything).
			Return(expectedClaims, len(expectedClaims), nil)

		queryParams := url.Values{
			networkIDParam:  []string{fmt.Sprintf("%d", mainnetNetworkID)},
			pageNumberParam: []string{"1"},
			pageSizeParam:   []string{"10"},
		}

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, claimsResp, response.Claims)
		require.Equal(t, len(expectedClaims), response.Count)
	})

	t.Run("GetClaims for L2 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		expectedClaims := []*claimsynctypes.Claim{
			{
				BlockNum:           1,
				GlobalIndex:        big.NewInt(1),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x1"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0x2"),
				Amount:             common.Big0,
				MainnetExitRoot:    common.HexToHash("0xdefc...789"),
			},
		}
		claimsResp := aggkitcommon.MapSlice(expectedClaims, func(claim *claimsynctypes.Claim) *bridgetypes.ClaimResponse {
			return NewClaimResponse(claim, false)
		})

		bridgeMocks.bridge.networkID = 10
		bridgeMocks.claimL2.EXPECT().
			GetClaimsPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything).
			Return(expectedClaims, len(expectedClaims), nil)

		query := url.Values{}
		query.Set(networkIDParam, "10")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, claimsResp, response.Claims)
		require.Equal(t, len(expectedClaims), response.Count)
	})

	t.Run("GetClaims with unsupported network", func(t *testing.T) {
		unsupportedNetworkID := 999
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		query := url.Values{}
		query.Set(networkIDParam, strconv.Itoa(unsupportedNetworkID))

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("unsupported network id: %d", unsupportedNetworkID))
	})

	t.Run("GetClaims for L1 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.claimL1.EXPECT().
			GetClaimsPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(fooErrMsg))

		query := url.Values{}
		query.Set(networkIDParam, "0")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get claims for the L1 network")
	})

	t.Run("GetClaims for L2 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.claimL2.EXPECT().
			GetClaimsPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(barErrMsg))

		query := url.Values{}
		query.Set(networkIDParam, "10")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get claims for the L2 network")
	})

	t.Run("GetClaims for L2 network failed invalid network id", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		query := url.Values{}
		query.Set(networkIDParam, "foo")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
	})

	t.Run("GetClaims for L2 network failed invalid network ids", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		query := url.Values{}
		query.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")
		query.Set(networkIDsParam, "foo,bar")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDsParam))
	})

	t.Run("GetClaims for L2 network failed invalid global index", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		query := url.Values{}
		query.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")
		query.Set(globalIndexParam, "invalid")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", globalIndexParam))
	})

	t.Run("GetClaims for L2 network failed invalid page number parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		query := url.Values{}
		query.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		query.Set(pageNumberParam, "invalid")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", pageNumberParam))
	})

	t.Run("GetClaims for L1 network with include_all_fields=true", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		// Create claims with proof data
		expectedClaims := []*claimsynctypes.Claim{
			{
				BlockNum:           1,
				GlobalIndex:        big.NewInt(1),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x1"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0x2"),
				Amount:             common.Big0,
				MainnetExitRoot:    common.HexToHash("0xdefc...789"),
				RollupExitRoot:     common.HexToHash("0xabc...123"),
				GlobalExitRoot:     common.HexToHash("0x456...def"),
				BlockTimestamp:     1617184800,
				Metadata:           []byte("metadata"),
				ProofLocalExitRoot: tree.Proof{
					common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111"),
					common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"),
				},
				ProofRollupExitRoot: tree.Proof{
					common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333"),
					common.HexToHash("0x4444444444444444444444444444444444444444444444444444444444444444"),
				},
			},
		}
		claimsResp := aggkitcommon.MapSlice(expectedClaims, func(claim *claimsynctypes.Claim) *bridgetypes.ClaimResponse {
			return NewClaimResponse(claim, true)
		})

		bridgeMocks.claimL1.EXPECT().
			GetClaimsPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything).
			Return(expectedClaims, len(expectedClaims), nil)

		queryParams := url.Values{
			networkIDParam:   []string{fmt.Sprintf("%d", mainnetNetworkID)},
			pageNumberParam:  []string{"1"},
			pageSizeParam:    []string{"10"},
			includeAllFields: []string{"true"},
		}

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, claimsResp, response.Claims)
		require.Equal(t, len(expectedClaims), response.Count)

		// Verify that proof fields are populated
		require.NotNil(t, response.Claims[0].ProofLocalExitRoot)
		require.NotNil(t, response.Claims[0].ProofRollupExitRoot)
		require.Len(t, *response.Claims[0].ProofLocalExitRoot, 32)  // Proof is always 32 elements
		require.Len(t, *response.Claims[0].ProofRollupExitRoot, 32) // Proof is always 32 elements
		require.Equal(t, bridgetypes.Hash("0x1111111111111111111111111111111111111111111111111111111111111111"), (*response.Claims[0].ProofLocalExitRoot)[0])
		require.Equal(t, bridgetypes.Hash("0x2222222222222222222222222222222222222222222222222222222222222222"), (*response.Claims[0].ProofLocalExitRoot)[1])
		require.Equal(t, bridgetypes.Hash("0x3333333333333333333333333333333333333333333333333333333333333333"), (*response.Claims[0].ProofRollupExitRoot)[0])
		require.Equal(t, bridgetypes.Hash("0x4444444444444444444444444444444444444444444444444444444444444444"), (*response.Claims[0].ProofRollupExitRoot)[1])

		// Verify that remaining elements are zero values (empty hashes)
		for i := 2; i < 32; i++ {
			require.Equal(t, bridgetypes.Hash("0x0000000000000000000000000000000000000000000000000000000000000000"), (*response.Claims[0].ProofLocalExitRoot)[i])
			require.Equal(t, bridgetypes.Hash("0x0000000000000000000000000000000000000000000000000000000000000000"), (*response.Claims[0].ProofRollupExitRoot)[i])
		}
	})

	t.Run("GetClaims for L2 network with include_all_fields=true", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		// Create claims with proof data
		expectedClaims := []*claimsynctypes.Claim{
			{
				BlockNum:           1,
				GlobalIndex:        big.NewInt(1),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x1"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0x2"),
				Amount:             common.Big0,
				MainnetExitRoot:    common.HexToHash("0xdefc...789"),
				RollupExitRoot:     common.HexToHash("0xabc...123"),
				GlobalExitRoot:     common.HexToHash("0x456...def"),
				BlockTimestamp:     1617184800,
				Metadata:           []byte("metadata"),
				ProofLocalExitRoot: tree.Proof{
					common.HexToHash("0x5555555555555555555555555555555555555555555555555555555555555555"),
					common.HexToHash("0x6666666666666666666666666666666666666666666666666666666666666666"),
				},
				ProofRollupExitRoot: tree.Proof{
					common.HexToHash("0x7777777777777777777777777777777777777777777777777777777777777777"),
					common.HexToHash("0x8888888888888888888888888888888888888888888888888888888888888888"),
				},
			},
		}
		claimsResp := aggkitcommon.MapSlice(expectedClaims, func(claim *claimsynctypes.Claim) *bridgetypes.ClaimResponse {
			return NewClaimResponse(claim, true)
		})

		bridgeMocks.bridge.networkID = 10
		bridgeMocks.claimL2.EXPECT().
			GetClaimsPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything).
			Return(expectedClaims, len(expectedClaims), nil)

		query := url.Values{}
		query.Set(networkIDParam, "10")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")
		query.Set(includeAllFields, "true")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, claimsResp, response.Claims)
		require.Equal(t, len(expectedClaims), response.Count)

		// Verify that proof fields are populated
		require.NotNil(t, response.Claims[0].ProofLocalExitRoot)
		require.NotNil(t, response.Claims[0].ProofRollupExitRoot)
		require.Len(t, *response.Claims[0].ProofLocalExitRoot, 32)  // Proof is always 32 elements
		require.Len(t, *response.Claims[0].ProofRollupExitRoot, 32) // Proof is always 32 elements
		require.Equal(t, bridgetypes.Hash("0x5555555555555555555555555555555555555555555555555555555555555555"), (*response.Claims[0].ProofLocalExitRoot)[0])
		require.Equal(t, bridgetypes.Hash("0x6666666666666666666666666666666666666666666666666666666666666666"), (*response.Claims[0].ProofLocalExitRoot)[1])
		require.Equal(t, bridgetypes.Hash("0x7777777777777777777777777777777777777777777777777777777777777777"), (*response.Claims[0].ProofRollupExitRoot)[0])
		require.Equal(t, bridgetypes.Hash("0x8888888888888888888888888888888888888888888888888888888888888888"), (*response.Claims[0].ProofRollupExitRoot)[1])

		// Verify that remaining elements are zero values (empty hashes)
		for i := 2; i < 32; i++ {
			require.Equal(t, bridgetypes.Hash("0x0000000000000000000000000000000000000000000000000000000000000000"), (*response.Claims[0].ProofLocalExitRoot)[i])
			require.Equal(t, bridgetypes.Hash("0x0000000000000000000000000000000000000000000000000000000000000000"), (*response.Claims[0].ProofRollupExitRoot)[i])
		}
	})

	t.Run("GetClaims with include_all_fields=false (default behavior)", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		// Create claims with proof data
		expectedClaims := []*claimsynctypes.Claim{
			{
				BlockNum:           1,
				GlobalIndex:        big.NewInt(1),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x1"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0x2"),
				Amount:             common.Big0,
				MainnetExitRoot:    common.HexToHash("0xdefc...789"),
				RollupExitRoot:     common.HexToHash("0xabc...123"),
				GlobalExitRoot:     common.HexToHash("0x456...def"),
				BlockTimestamp:     1617184800,
				Metadata:           []byte("metadata"),
				ProofLocalExitRoot: tree.Proof{
					common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111"),
					common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"),
				},
				ProofRollupExitRoot: tree.Proof{
					common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333"),
					common.HexToHash("0x4444444444444444444444444444444444444444444444444444444444444444"),
				},
			},
		}
		claimsResp := aggkitcommon.MapSlice(expectedClaims, func(claim *claimsynctypes.Claim) *bridgetypes.ClaimResponse {
			return NewClaimResponse(claim, false)
		})

		bridgeMocks.claimL1.EXPECT().
			GetClaimsPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything).
			Return(expectedClaims, len(expectedClaims), nil)

		queryParams := url.Values{
			networkIDParam:   []string{fmt.Sprintf("%d", mainnetNetworkID)},
			pageNumberParam:  []string{"1"},
			pageSizeParam:    []string{"10"},
			includeAllFields: []string{"false"},
		}

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, claimsResp, response.Claims)
		require.Equal(t, len(expectedClaims), response.Count)

		// Verify that proof fields are NOT populated
		require.Nil(t, response.Claims[0].ProofLocalExitRoot)
		require.Nil(t, response.Claims[0].ProofRollupExitRoot)
	})

	t.Run("GetClaims with invalid include_all_fields parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		query := url.Values{}
		query.Set(networkIDParam, "0")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")
		query.Set(includeAllFields, "invalid")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "invalid include_all_fields parameter")
	})

	t.Run("GetClaims for L1 network with nil L1 syncer", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL1 = nil

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(mainnetNetworkID))
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)

		var response gin.H
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, "L1 bridge syncer is not available", response["error"])
	})

	t.Run("GetClaims for L2 network with nil L2 syncer", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL2 = nil

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)

		var response gin.H
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, "L2 bridge syncer is not available", response["error"])
	})

	t.Run("GetClaims count with compaction - multiple claims same global_index", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		// Create 2 claims with the same global_index (should be compacted to 1)
		globalIndex, _ := new(big.Int).SetString("18446744073709551617", 10)
		expectedClaims := []*claimsynctypes.Claim{
			{
				BlockNum:           1,
				GlobalIndex:        globalIndex,
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x1"),
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x2"),
				Amount:             common.Big0,
				MainnetExitRoot:    common.HexToHash("0xdefc...789"),
			},
		}

		expectedCount := 1
		claimsResp := aggkitcommon.MapSlice(expectedClaims, func(claim *claimsynctypes.Claim) *bridgetypes.ClaimResponse {
			return NewClaimResponse(claim, false)
		})
		bridgeMocks.claimL1.EXPECT().
			GetClaimsPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything).
			Return(expectedClaims, expectedCount, nil)

		queryParams := url.Values{
			networkIDParam:  []string{fmt.Sprintf("%d", mainnetNetworkID)},
			pageNumberParam: []string{"1"},
			pageSizeParam:   []string{"10"},
		}

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, claimsResp, response.Claims)
		require.Equal(t, expectedCount, response.Count)
	})

	t.Run("GetClaims count with unset_claim - all claims counted", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		// Create 3 claims with the same global_index but with unset_claim (all should be returned)
		expectedClaims := []*claimsynctypes.Claim{
			{
				BlockNum:           1,
				GlobalIndex:        big.NewInt(100),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x1"),
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x2"),
				Amount:             common.Big0,
				MainnetExitRoot:    common.HexToHash("0xdefc...789"),
			},
			{
				BlockNum:           2,
				GlobalIndex:        big.NewInt(100),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x1"),
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x2"),
				Amount:             common.Big0,
				MainnetExitRoot:    common.HexToHash("0xdefc...789"),
			},
			{
				BlockNum:           3,
				GlobalIndex:        big.NewInt(100),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x1"),
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x2"),
				Amount:             common.Big0,
				MainnetExitRoot:    common.HexToHash("0xdefc...789"),
			},
		}

		// The count should be 3 (all claims, no compaction when unset_claim exists)
		expectedCount := 3
		claimsResp := aggkitcommon.MapSlice(expectedClaims, func(claim *claimsynctypes.Claim) *bridgetypes.ClaimResponse {
			return NewClaimResponse(claim, false)
		})

		bridgeMocks.claimL1.EXPECT().
			GetClaimsPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything).
			Return(expectedClaims, expectedCount, nil)

		queryParams := url.Values{
			networkIDParam:  []string{fmt.Sprintf("%d", mainnetNetworkID)},
			pageNumberParam: []string{"1"},
			pageSizeParam:   []string{"10"},
		}

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, claimsResp, response.Claims)
		require.Equal(t, expectedCount, response.Count)
	})

	t.Run("GetClaims count with mixed scenarios - compaction and unset_claim", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		// Mixed scenario:
		// - global_index=100: 2 claims, no unset_claim → compacted to 1
		// - global_index=200: 3 claims, has unset_claim → all 3 returned
		// - global_index=300: 1 claim, no unset_claim → 1 returned
		expectedClaims := []*claimsynctypes.Claim{
			{
				BlockNum:           1,
				GlobalIndex:        big.NewInt(100),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x1"),
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x2"),
				Amount:             common.Big0,
				MainnetExitRoot:    common.HexToHash("0xdefc...789"),
			},
			{
				BlockNum:           2,
				GlobalIndex:        big.NewInt(200),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x3"),
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x4"),
				Amount:             common.Big0,
				MainnetExitRoot:    common.HexToHash("0xabc...123"),
			},
			{
				BlockNum:           3,
				GlobalIndex:        big.NewInt(200),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x3"),
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x4"),
				Amount:             common.Big0,
				MainnetExitRoot:    common.HexToHash("0xabc...123"),
			},
			{
				BlockNum:           4,
				GlobalIndex:        big.NewInt(200),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x3"),
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x4"),
				Amount:             common.Big0,
				MainnetExitRoot:    common.HexToHash("0xabc...123"),
			},
			{
				BlockNum:           5,
				GlobalIndex:        big.NewInt(300),
				OriginNetwork:      0,
				OriginAddress:      common.HexToAddress("0x5"),
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x6"),
				Amount:             common.Big0,
				MainnetExitRoot:    common.HexToHash("0x456...def"),
			},
		}

		// Expected count: 1 (compacted) + 3 (all with unset_claim) + 1 (single) = 5
		expectedCount := 5
		claimsResp := aggkitcommon.MapSlice(expectedClaims, func(claim *claimsynctypes.Claim) *bridgetypes.ClaimResponse {
			return NewClaimResponse(claim, false)
		})

		bridgeMocks.claimL1.EXPECT().
			GetClaimsPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything).
			Return(expectedClaims, expectedCount, nil)

		queryParams := url.Values{
			networkIDParam:  []string{fmt.Sprintf("%d", mainnetNetworkID)},
			pageNumberParam: []string{"1"},
			pageSizeParam:   []string{"10"},
		}

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, claimsResp, response.Claims)
		require.Equal(t, expectedCount, response.Count)
	})
}

func TestGetUnsetClaimsHandler(t *testing.T) {
	t.Run("GetUnsetClaims for L2 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		expectedUnsetClaims := []*claimsynctypes.UnsetClaim{
			{
				BlockNum:                  1,
				BlockPos:                  1,
				TxHash:                    common.HexToHash("0x1234567890abcdef"),
				GlobalIndex:               big.NewInt(1000000),
				UnsetGlobalIndexHashChain: common.HexToHash("0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757"),
				CreatedAt:                 1617184800,
			},
		}

		bridgeMocks.claimL2.EXPECT().
			GetUnsetClaimsPaged(mock.Anything, page, pageSize, mock.Anything).
			Return(expectedUnsetClaims, len(expectedUnsetClaims), nil)

		queryParams := url.Values{}
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/unset-claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.UnsetClaimsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		require.Equal(t, len(expectedUnsetClaims), response.Count)
		require.Len(t, response.UnsetClaims, len(expectedUnsetClaims))
		require.Equal(t, expectedUnsetClaims[0].BlockNum, response.UnsetClaims[0].BlockNum)
		require.Equal(t, expectedUnsetClaims[0].GlobalIndex.String(), string(response.UnsetClaims[0].GlobalIndex))
	})

	t.Run("GetUnsetClaims for L2 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.claimL2.EXPECT().
			GetUnsetClaimsPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(barErrMsg))

		queryParams := url.Values{}
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/unset-claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("failed to get unset claims for the L2 network (ID=%d)", l2NetworkID))
	})

	t.Run("GetUnsetClaims with nil L2 syncer", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL2 = nil

		queryParams := url.Values{}
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/unset-claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)

		var response gin.H
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, "L2 bridge syncer is not available", response["error"])
	})
}

func TestGetSetClaimsHandler(t *testing.T) {
	t.Run("GetSetClaims for L2 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		expectedSetClaims := []*claimsynctypes.SetClaim{
			{
				BlockNum:    1,
				BlockPos:    1,
				TxHash:      common.HexToHash("0x1234567890abcdef"),
				GlobalIndex: big.NewInt(1000000),
				CreatedAt:   1617184800,
			},
		}

		bridgeMocks.claimL2.EXPECT().
			GetSetClaimsPaged(mock.Anything, page, pageSize, mock.Anything).
			Return(expectedSetClaims, len(expectedSetClaims), nil)

		queryParams := url.Values{}
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/set-claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.SetClaimsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		require.Equal(t, len(expectedSetClaims), response.Count)
		require.Len(t, response.SetClaims, len(expectedSetClaims))
		require.Equal(t, expectedSetClaims[0].BlockNum, response.SetClaims[0].BlockNum)
		require.Equal(t, expectedSetClaims[0].GlobalIndex.String(), string(response.SetClaims[0].GlobalIndex))
		require.Equal(t, expectedSetClaims[0].CreatedAt, response.SetClaims[0].CreatedAt)
	})

	t.Run("GetSetClaims for L2 network with global_index filter", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)
		globalIndex := big.NewInt(2000000)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		expectedSetClaims := []*claimsynctypes.SetClaim{
			{
				BlockNum:    2,
				BlockPos:    0,
				TxHash:      common.HexToHash("0xabcdef1234567890"),
				GlobalIndex: globalIndex,
				CreatedAt:   1617184900,
			},
		}

		bridgeMocks.claimL2.EXPECT().
			GetSetClaimsPaged(mock.Anything, page, pageSize, globalIndex).
			Return(expectedSetClaims, len(expectedSetClaims), nil)

		queryParams := url.Values{}
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")
		queryParams.Set(globalIndexParam, globalIndex.String())

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/set-claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.SetClaimsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		require.Equal(t, len(expectedSetClaims), response.Count)
		require.Len(t, response.SetClaims, len(expectedSetClaims))
		require.Equal(t, expectedSetClaims[0].BlockNum, response.SetClaims[0].BlockNum)
		require.Equal(t, expectedSetClaims[0].GlobalIndex.String(), string(response.SetClaims[0].GlobalIndex))
	})

	t.Run("GetSetClaims for L2 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.claimL2.EXPECT().
			GetSetClaimsPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(barErrMsg))

		queryParams := url.Values{}
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/set-claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("failed to get set claims for the L2 network (ID=%d)", l2NetworkID))
	})

	t.Run("GetSetClaims with nil L2 syncer", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL2 = nil

		queryParams := url.Values{}
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/set-claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)

		var response gin.H
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, "L2 bridge syncer is not available", response["error"])
	})

	t.Run("GetSetClaims with invalid global_index parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")
		queryParams.Set(globalIndexParam, "invalid")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/set-claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", globalIndexParam))
	})

	t.Run("GetSetClaims with invalid page number parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(pageNumberParam, "invalid")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/set-claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", pageNumberParam))
	})

	t.Run("GetSetClaims with invalid page size parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "invalid")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/set-claims?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", pageSizeParam))
	})
}

func TestGetRemoveGEREventsHandler(t *testing.T) {
	t.Run("GetRemoveGEREvents - get all events", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		expectedEvents := []*l2gersync.RemoveGEREvent{
			{
				GlobalExitRoot: common.HexToHash("0xabc123"),
				BlockNum:       100,
				BlockPos:       0,
				CreatedAt:      1617184800,
			},
			{
				GlobalExitRoot: common.HexToHash("0xdef456"),
				BlockNum:       101,
				BlockPos:       1,
				CreatedAt:      1617184900,
			},
		}

		bridgeMocks.injectedGERs.EXPECT().
			GetRemoveGEREvents(mock.Anything, (*common.Hash)(nil), uint32(50)).
			Return(expectedEvents, nil)

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/removed-gers", BridgeV1Prefix))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.RemoveGEREventsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		require.Equal(t, len(expectedEvents), response.Count)
		require.Len(t, response.RemoveGEREvents, len(expectedEvents))
		require.Equal(t, expectedEvents[0].BlockPos, response.RemoveGEREvents[0].BlockPos)
		require.Equal(t, expectedEvents[0].GlobalExitRoot.Hex(), string(response.RemoveGEREvents[0].GlobalExitRoot))
	})

	t.Run("GetRemoveGEREvents by global_exit_root", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		targetGER := common.HexToHash("0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757")

		expectedEvents := []*l2gersync.RemoveGEREvent{
			{
				GlobalExitRoot: targetGER,
				BlockNum:       200,
				BlockPos:       0,
				CreatedAt:      1617185000,
			},
		}

		bridgeMocks.injectedGERs.EXPECT().
			GetRemoveGEREvents(mock.Anything, &targetGER, mock.AnythingOfType("uint32")).
			Return(expectedEvents, nil)

		queryParams := url.Values{}
		queryParams.Set("global_exit_root", targetGER.Hex())

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/removed-gers?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.RemoveGEREventsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		require.Equal(t, 1, response.Count)
		require.Equal(t, targetGER.Hex(), string(response.RemoveGEREvents[0].GlobalExitRoot))
	})

	t.Run("GetRemoveGEREvents invalid global_exit_root", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set("global_exit_root", "invalid_hash")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/removed-gers?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "invalid global_exit_root parameter")
	})

	t.Run("GetRemoveGEREvents service failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.injectedGERs.EXPECT().
			GetRemoveGEREvents(mock.Anything, (*common.Hash)(nil), uint32(50)).
			Return(nil, errors.New("database error"))

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/removed-gers", BridgeV1Prefix))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get remove GER events")
	})

	t.Run("GetRemoveGEREvents with nil injectedGERs", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.injectedGERs = nil

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/removed-gers", BridgeV1Prefix))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)

		var response gin.H
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, "L2 GER syncer is not available", response["error"])
	})

	t.Run("GetRemoveGEREvents with custom limit", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		expectedEvents := []*l2gersync.RemoveGEREvent{
			{
				GlobalExitRoot: common.HexToHash("0xabc123"),
				BlockNum:       100,
				BlockPos:       0,
				CreatedAt:      1617184800,
			},
		}

		bridgeMocks.injectedGERs.EXPECT().
			GetRemoveGEREvents(mock.Anything, (*common.Hash)(nil), uint32(10)).
			Return(expectedEvents, nil)

		queryParams := url.Values{}
		queryParams.Set("limit", "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/removed-gers?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.RemoveGEREventsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		require.Equal(t, len(expectedEvents), response.Count)
		require.Len(t, response.RemoveGEREvents, len(expectedEvents))
	})

	t.Run("GetRemoveGEREvents with invalid limit", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set("limit", "0")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/removed-gers?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "limit must be greater than 0")
	})
}

func TestIsValidHexHash(t *testing.T) {
	t.Run("valid hex hash with 0x prefix", func(t *testing.T) {
		validHash := "0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757"
		require.True(t, isValidHexHash(validHash))
	})

	t.Run("invalid length", func(t *testing.T) {
		shortHash := "0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d7"
		require.False(t, isValidHexHash(shortHash))
	})

	t.Run("valid hex hash without 0x prefix", func(t *testing.T) {
		noPrefix := "27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757"
		require.True(t, isValidHexHash(noPrefix))
	})

	t.Run("invalid hex characters", func(t *testing.T) {
		invalidChars := "0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d75g"
		require.False(t, isValidHexHash(invalidChars))
	})
}

func TestGetTokenMappingsHandler(t *testing.T) {
	t.Run("GetTokenMappingsHandler for L1 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
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
			},
		}
		tokenMappingsResp := aggkitcommon.MapSlice(tokenMappings, NewTokenMappingResponse)

		bridgeMocks.bridgeL1.EXPECT().GetTokenMappings(mock.Anything, page, pageSize, mock.Anything).
			Return(tokenMappings, len(tokenMappings), nil)

		query := url.Values{}
		query.Set(networkIDParam, "0")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.TokenMappingsResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, len(tokenMappings), response.Count)
		require.Equal(t, tokenMappingsResp, response.TokenMappings)

		bridgeMocks.bridgeL1.AssertExpectations(t)
	})

	t.Run("GetTokenMappingsHandler for L2 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
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
				Type:                bridgetypes.SovereignToken,
				IsNotMintable:       true,
			},
		}
		tokenMappingsResp := aggkitcommon.MapSlice(tokenMappings, NewTokenMappingResponse)

		bridgeMocks.bridgeL2.EXPECT().GetTokenMappings(mock.Anything, page, pageSize, mock.Anything).
			Return(tokenMappings, len(tokenMappings), nil)

		query := url.Values{}
		query.Set(networkIDParam, "10")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.TokenMappingsResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, len(tokenMappings), response.Count)
		require.Equal(t, tokenMappingsResp, response.TokenMappings)

		bridgeMocks.bridgeL2.AssertExpectations(t)
	})

	t.Run("GetTokenMappingsHandler with unsupported network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		query := url.Values{}
		query.Set(networkIDParam, "999")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "unsupported network id: 999")
	})

	t.Run("GetTokenMappingsHandler for L1 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL1.EXPECT().GetTokenMappings(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(fooErrMsg))

		query := url.Values{}
		query.Set(networkIDParam, "0")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("failed to fetch token mappings: %s", fooErrMsg))
	})

	t.Run("GetTokenMappingsHandler for L2 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL2.EXPECT().GetTokenMappings(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(barErrMsg))

		query := url.Values{}
		query.Set(networkIDParam, "10")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("failed to fetch token mappings: %s", barErrMsg))
	})

	t.Run("GetTokenMappingsHandler for L2 network failed invalid network id", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		query := url.Values{}
		query.Set(networkIDParam, "foo")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
	})

	t.Run("GetTokenMappingsHandler for L1 network with valid origin_token_address", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)
		originTokenAddr := "0x1234567890abcdef1234567890abcdef12345678"

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		tokenMappings := []*bridgesync.TokenMapping{
			{
				BlockNum:            1,
				BlockPos:            1,
				BlockTimestamp:      1617184800,
				TxHash:              common.HexToHash("0x1"),
				OriginNetwork:       1,
				OriginTokenAddress:  common.HexToAddress(originTokenAddr),
				WrappedTokenAddress: common.HexToAddress("0x2"),
				Metadata:            common.Hex2Bytes("abcd"),
			},
		}
		tokenMappingsResp := aggkitcommon.MapSlice(tokenMappings, NewTokenMappingResponse)

		bridgeMocks.bridgeL1.EXPECT().GetTokenMappings(mock.Anything, page, pageSize, originTokenAddr).
			Return(tokenMappings, len(tokenMappings), nil)

		query := url.Values{}
		query.Set(networkIDParam, "0")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")
		query.Set("origin_token_address", originTokenAddr)

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.TokenMappingsResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, len(tokenMappings), response.Count)
		require.Equal(t, tokenMappingsResp, response.TokenMappings)
		require.Equal(t, common.HexToAddress(originTokenAddr).String(), string(response.TokenMappings[0].OriginTokenAddress))

		bridgeMocks.bridgeL1.AssertExpectations(t)
	})

	t.Run("GetTokenMappingsHandler for L2 network with valid origin_token_address", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)
		originTokenAddr := "0xabcdef1234567890abcdef1234567890abcdef12"

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		tokenMappings := []*bridgesync.TokenMapping{
			{
				BlockNum:            1,
				BlockPos:            1,
				BlockTimestamp:      1617184800,
				TxHash:              common.HexToHash("0x1"),
				OriginNetwork:       1,
				OriginTokenAddress:  common.HexToAddress(originTokenAddr),
				WrappedTokenAddress: common.HexToAddress("0x2"),
				Metadata:            []byte("metadata"),
				Type:                bridgetypes.SovereignToken,
				IsNotMintable:       true,
			},
		}
		tokenMappingsResp := aggkitcommon.MapSlice(tokenMappings, NewTokenMappingResponse)

		bridgeMocks.bridgeL2.EXPECT().GetTokenMappings(mock.Anything, page, pageSize, originTokenAddr).
			Return(tokenMappings, len(tokenMappings), nil)

		query := url.Values{}
		query.Set(networkIDParam, "10")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")
		query.Set("origin_token_address", originTokenAddr)

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, query.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.TokenMappingsResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, len(tokenMappings), response.Count)
		require.Equal(t, tokenMappingsResp, response.TokenMappings)
		require.Equal(t, common.HexToAddress(originTokenAddr).String(), string(response.TokenMappings[0].OriginTokenAddress))

		bridgeMocks.bridgeL2.AssertExpectations(t)
	})

	t.Run("GetTokenMappings for L1 network with nil L1 syncer", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL1 = nil

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(mainnetNetworkID))
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)

		var response gin.H
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, "L1 bridge syncer is not available", response["error"])
	})

	t.Run("GetTokenMappings for L2 network with nil L2 syncer", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL2 = nil

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)

		var response gin.H
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, "L2 bridge syncer is not available", response["error"])
	})
}

func TestGetLegacyTokenMigrationsHandler(t *testing.T) {
	t.Run("GetLegacyTokenMigrations for L1 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

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
			},
		}
		tokenMigrationsResp := aggkitcommon.MapSlice(tokenMigrations, NewTokenMigrationResponse)

		bridgeMocks.bridgeL1.EXPECT().
			GetLegacyTokenMigrations(mock.Anything, page, pageSize).
			Return(tokenMigrations, len(tokenMigrations), nil)

		queryParams := url.Values{}
		queryParams.Set("network_id", "0")
		queryParams.Set("page_number", "1")
		queryParams.Set("page_size", "10")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/legacy-token-migrations?%s", BridgeV1Prefix, queryParams.Encode()))

		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.LegacyTokenMigrationsResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, len(tokenMigrations), response.Count)
		require.Equal(t, tokenMigrationsResp, response.TokenMigrations)

		bridgeMocks.bridgeL1.AssertExpectations(t)
	})

	t.Run("GetLegacyTokenMigrations for L2 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

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
		tokenMigrationsResp := aggkitcommon.MapSlice(tokenMigrations, NewTokenMigrationResponse)

		bridgeMocks.bridgeL2.EXPECT().
			GetLegacyTokenMigrations(mock.Anything, page, pageSize).
			Return(tokenMigrations, len(tokenMigrations), nil)

		queryParams := url.Values{}
		queryParams.Set("network_id", fmt.Sprintf("%d", l2NetworkID))
		queryParams.Set("page_number", "1")
		queryParams.Set("page_size", "10")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/legacy-token-migrations?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.LegacyTokenMigrationsResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, len(tokenMigrations), response.Count)
		require.Equal(t, tokenMigrationsResp, response.TokenMigrations)

		bridgeMocks.bridgeL2.AssertExpectations(t)
	})

	t.Run("GetLegacyTokenMigrations with unsupported network", func(t *testing.T) {
		unsupportedNetworkID := uint32(999)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set("network_id", fmt.Sprintf("%d", unsupportedNetworkID))

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/legacy-token-migrations?%s", BridgeV1Prefix, queryParams.Encode()))

		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("unsupported network id: %d", unsupportedNetworkID))
	})

	t.Run("GetLegacyTokenMigrations for L1 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL1.EXPECT().
			GetLegacyTokenMigrations(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set("network_id", "0")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/legacy-token-migrations?%s", BridgeV1Prefix, queryParams.Encode()))

		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), fooErrMsg)
	})

	t.Run("GetLegacyTokenMigrations for L2 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL2.EXPECT().
			GetLegacyTokenMigrations(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(barErrMsg))

		queryParams := url.Values{}
		queryParams.Set("network_id", fmt.Sprintf("%d", l2NetworkID))

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/legacy-token-migrations?%s", BridgeV1Prefix, queryParams.Encode()))

		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), barErrMsg)
	})

	t.Run("GetLegacyTokenMigrations for L2 network failed invalid network id", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{
			networkIDParam:  []string{"foo"},
			pageNumberParam: []string{"1"},
			pageSizeParam:   []string{"10"},
		}

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/legacy-token-migrations?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
	})

	t.Run("GetLegacyTokenMigrations for L1 network with nil L1 syncer", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL1 = nil

		queryParams := url.Values{}
		queryParams.Set("network_id", "0")
		queryParams.Set("page_number", "1")
		queryParams.Set("page_size", "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/legacy-token-migrations?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)

		var response gin.H
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, "L1 bridge syncer is not available", response["error"])
	})

	t.Run("GetLegacyTokenMigrations for L2 network with nil L2 syncer", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL2 = nil

		queryParams := url.Values{}
		queryParams.Set("network_id", fmt.Sprintf("%d", l2NetworkID))
		queryParams.Set("page_number", "1")
		queryParams.Set("page_size", "10")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/legacy-token-migrations?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)

		var response gin.H
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, "L2 bridge syncer is not available", response["error"])
	})
}

func TestL1InfoTreeIndexForBridgeHandler(t *testing.T) {
	depositCount := uint32(10)
	expectedIndex := uint32(42)
	blockNum := uint64(50)

	t.Run("Success L1 network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.l1InfoTree.EXPECT().
			GetLastInfo().
			Return(
				&l1infotreesync.L1InfoTreeLeaf{
					MainnetExitRoot: common.HexToHash("0xabc"),
					L1InfoTreeIndex: expectedIndex,
					BlockNumber:     blockNum,
				},
				nil)
		bridgeMocks.l1InfoTree.EXPECT().GetFirstInfo().Return(&l1infotreesync.L1InfoTreeLeaf{BlockNumber: 0}, nil)
		bridgeMocks.l1InfoTree.EXPECT().GetFirstInfoAfterBlock(mock.Anything).
			Return(
				&l1infotreesync.L1InfoTreeLeaf{
					MainnetExitRoot: common.HexToHash("0xabc"),
					L1InfoTreeIndex: expectedIndex,
				}, nil)

		bridgeMocks.bridgeL1.EXPECT().
			GetRootByLER(mock.Anything, mock.Anything).
			Return(&tree.Root{
				Index:    depositCount,
				BlockNum: blockNum,
			}, nil)

		queryParams := url.Values{}
		queryParams.Set("network_id", "0")
		queryParams.Set("deposit_count", fmt.Sprintf("%d", depositCount))

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/l1-info-tree-index?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response uint32
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, expectedIndex, response)

		bridgeMocks.l1InfoTree.AssertExpectations(t)
		bridgeMocks.bridgeL1.AssertExpectations(t)
	})

	t.Run("Success L2 network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.l1InfoTree.EXPECT().
			GetLastVerifiedBatches(mock.Anything).
			Return(&l1infotreesync.VerifyBatches{}, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetFirstVerifiedBatches(mock.Anything).
			Return(&l1infotreesync.VerifyBatches{}, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetFirstVerifiedBatchesAfterBlock(mock.Anything, mock.Anything).
			Return(&l1infotreesync.VerifyBatches{}, nil)

		bridgeMocks.bridgeL2.EXPECT().GetRootByLER(mock.Anything, mock.Anything).Return(
			&tree.Root{
				Index:    depositCount,
				BlockNum: blockNum,
			}, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetFirstL1InfoWithRollupExitRoot(mock.Anything).
			Return(
				&l1infotreesync.L1InfoTreeLeaf{
					L1InfoTreeIndex: expectedIndex,
					BlockNumber:     blockNum,
				}, nil)

		queryParams := url.Values{}
		queryParams.Set("network_id", fmt.Sprintf("%d", l2NetworkID))
		queryParams.Set("deposit_count", fmt.Sprintf("%d", depositCount))

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/l1-info-tree-index?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response uint32
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, expectedIndex, response)

		bridgeMocks.bridgeL2.AssertExpectations(t)
		bridgeMocks.l1InfoTree.AssertExpectations(t)
	})

	t.Run("Invalid network ID", func(t *testing.T) {
		invalidNetworkID := uint32(999)
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set("network_id", fmt.Sprintf("%d", invalidNetworkID))
		queryParams.Set("deposit_count", fmt.Sprintf("%d", depositCount))

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/l1-info-tree-index?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("unsupported network id: %d", invalidNetworkID))
	})

	t.Run("Error from GetLastInfo", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.l1InfoTree.EXPECT().
			GetLastInfo().
			Return(nil, errors.New(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set("network_id", "0")
		queryParams.Set("deposit_count", fmt.Sprintf("%d", depositCount))

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/l1-info-tree-index?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), fooErrMsg)
	})

	t.Run("Error from GetRootByLER and GetLastRoot", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.l1InfoTree.EXPECT().
			GetLastInfo().
			Return(
				&l1infotreesync.L1InfoTreeLeaf{
					MainnetExitRoot: common.HexToHash("0xabc"),
					L1InfoTreeIndex: expectedIndex,
					BlockNumber:     blockNum,
				},
				nil)

		bridgeMocks.bridgeL1.EXPECT().
			GetRootByLER(mock.Anything, mock.Anything).
			Return(nil, errors.New(barErrMsg))

		// With the fallback logic, it will try GetLastRoot when GetRootByLER fails
		bridgeMocks.bridgeL1.EXPECT().
			GetLastRoot(mock.Anything).
			Return(nil, fmt.Errorf("last root error"))

		queryParams := url.Values{}
		queryParams.Set("network_id", "0")
		queryParams.Set("deposit_count", fmt.Sprintf("%d", depositCount))

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/l1-info-tree-index?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "last root error")
	})

	t.Run("Invalid network ID parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set("network_id", "invalid")
		queryParams.Set("deposit_count", "10")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/l1-info-tree-index?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
	})

	t.Run("Invalid deposit count parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set("network_id", "10")
		queryParams.Set("deposit_count", "test")

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/l1-info-tree-index?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", depositCountParam))
	})
}

func TestInjectedL1InfoLeafHandler(t *testing.T) {
	l1InfoTreeLeaf := &l1infotreesync.L1InfoTreeLeaf{
		BlockNumber:       uint64(3),
		BlockPosition:     uint64(0),
		L1InfoTreeIndex:   uint32(1),
		PreviousBlockHash: common.HexToHash("0x1"),
		Timestamp:         uint64(time.Now().Unix()),
		MainnetExitRoot:   common.HexToHash("0x2"),
		RollupExitRoot:    common.HexToHash("0x3"),
		Hash:              common.HexToHash("0x4"),
	}
	l1InfoTreeLeaf.GlobalExitRoot = crypto.Keccak256Hash(
		append(l1InfoTreeLeaf.MainnetExitRoot.Bytes(), l1InfoTreeLeaf.RollupExitRoot.Bytes()...))

	t.Run("Retrieve for L1 network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(l1InfoTreeLeaf, nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeLeaf.L1InfoTreeIndex))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, response.Code)

		var result l1infotreesync.L1InfoTreeLeaf
		err := json.Unmarshal(response.Body.Bytes(), &result)
		require.NoError(t, err)
		require.Equal(t, *l1InfoTreeLeaf, result)
	})

	t.Run("Retrieve for L2 network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.injectedGERs.EXPECT().
			GetFirstGERAfterL1InfoTreeIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(l2gersync.GlobalExitRootInfo{
				GlobalExitRoot:  l1InfoTreeLeaf.GlobalExitRoot,
				L1InfoTreeIndex: l1InfoTreeLeaf.L1InfoTreeIndex,
			}, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(l1InfoTreeLeaf, nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "10")
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeLeaf.L1InfoTreeIndex))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, response.Code)

		var result l1infotreesync.L1InfoTreeLeaf
		err := json.Unmarshal(response.Body.Bytes(), &result)
		require.NoError(t, err)
		require.Equal(t, *l1InfoTreeLeaf, result)
	})

	t.Run("Unsupported network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		unsupportedNetworkID := uint32(999)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", unsupportedNetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeLeaf.L1InfoTreeIndex))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()))

		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("unsupported network id: %d", unsupportedNetworkID))
	})

	t.Run("L1 network error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(nil, errors.New(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", mainnetNetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeLeaf.L1InfoTreeIndex))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(),
			fmt.Sprintf("failed to get L1 info tree leaf (network id=%d, leaf index=%d), error: %s",
				mainnetNetworkID, l1InfoTreeLeaf.L1InfoTreeIndex, fooErrMsg))
	})

	t.Run("L2 network - GetFirstGERAfterL1InfoTreeIndex error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.injectedGERs.EXPECT().
			GetFirstGERAfterL1InfoTreeIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(l2gersync.GlobalExitRootInfo{}, errors.New(barErrMsg))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", l2NetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeLeaf.L1InfoTreeIndex))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("failed to get injected global exit root for leaf index=%d", l1InfoTreeLeaf.L1InfoTreeIndex))
	})

	t.Run("L2 network - GetInfoByIndex error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.injectedGERs.EXPECT().
			GetFirstGERAfterL1InfoTreeIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(l2gersync.GlobalExitRootInfo{
				GlobalExitRoot:  l1InfoTreeLeaf.GlobalExitRoot,
				L1InfoTreeIndex: l1InfoTreeLeaf.L1InfoTreeIndex,
			}, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(nil, errors.New(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", l2NetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeLeaf.L1InfoTreeIndex))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(),
			fmt.Sprintf("failed to get L1 info tree leaf (leaf index=%d), error: %s", l1InfoTreeLeaf.L1InfoTreeIndex, fooErrMsg))
	})

	t.Run("Invalid network id param", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "invalid")
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeLeaf.L1InfoTreeIndex))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
	})

	t.Run("Invalid leaf index param", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "10")
		queryParams.Set(leafIndexParam, "invalid")

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("invalid %s parameter", leafIndexParam))
	})
}

func TestClaimProofHandler(t *testing.T) {
	l1InfoTreeIndex := uint32(1)
	depositCount := uint32(1)

	l1InfoTreeLeaf := &l1infotreesync.L1InfoTreeLeaf{
		MainnetExitRoot: common.HexToHash("0x1"),
		RollupExitRoot:  common.HexToHash("0x2"),
	}

	infoTreeLeafResponse := NewL1InfoTreeLeafResponse(l1InfoTreeLeaf)

	t.Run("Failed to get L1 info tree leaf", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeIndex).
			Return(nil, errors.New(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(mainnetNetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("failed to get l1 info tree leaf for index %d", l1InfoTreeIndex))
	})

	t.Run("Unsupported network id:", func(t *testing.T) {
		unsupportedNetworkID := uint32(999)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeIndex).
			Return(l1InfoTreeLeaf, nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", unsupportedNetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("failed to get claim proof, unsupported network %d", unsupportedNetworkID))
	})

	//nolint:dupl
	t.Run("Failed to get LER for L1 network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeIndex).
			Return(l1InfoTreeLeaf, nil)

		bridgeMocks.bridgeL1.EXPECT().
			GetProof(mock.Anything, depositCount, l1InfoTreeLeaf.MainnetExitRoot).
			Return(tree.Proof{}, errors.New(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", mainnetNetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(), "failed to get local exit proof")
	})

	t.Run("Failed to get RER proof for L1 network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, mock.Anything).
			Return(l1InfoTreeLeaf, nil)

		bridgeMocks.bridgeL1.EXPECT().
			GetProof(mock.Anything, mock.Anything, mock.Anything).
			Return(tree.Proof{}, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetRollupExitTreeMerkleProof(mock.Anything, mock.Anything, mock.Anything).
			Return(tree.Proof{}, errors.New(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", mainnetNetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("failed to get rollup exit proof (network id=%d, leaf index=%d, deposit count=%d), error: %s",
			mainnetNetworkID, l1InfoTreeIndex, depositCount, fooErrMsg))
	})

	//nolint:dupl
	t.Run("Failed to get LER for L2 network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeIndex).
			Return(l1InfoTreeLeaf, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetLocalExitRoot(mock.Anything, l2NetworkID, l1InfoTreeLeaf.RollupExitRoot).
			Return(common.Hash{}, errors.New(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", l2NetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(), "failed to get local exit root")
	})

	t.Run("Failed to get LER proof for L2 network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, mock.Anything).
			Return(l1InfoTreeLeaf, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetLocalExitRoot(mock.Anything, mock.Anything, mock.Anything).
			Return(common.HexToHash("0x3"), nil)

		bridgeMocks.bridgeL2.EXPECT().
			GetProof(mock.Anything, mock.Anything, mock.Anything).
			Return(tree.Proof{}, errors.New(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", l2NetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("failed to get local exit proof, error: %s", fooErrMsg))
	})

	t.Run("Retrieve claim proof for L1 network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		localExitTreeProof := tree.Proof{
			common.HexToHash("0xf"),
			common.HexToHash("0xd"),
			common.HexToHash("0xc"),
			common.HexToHash("0xb"),
		}
		rollupExitTreeProof := tree.Proof{
			common.HexToHash("0x1"),
			common.HexToHash("0x2"),
		}

		expectedClaimProof := bridgetypes.ClaimProof{
			ProofLocalExitRoot:  bridgetypes.ConvertToProofResponse(localExitTreeProof),
			ProofRollupExitRoot: bridgetypes.ConvertToProofResponse(rollupExitTreeProof),
			L1InfoTreeLeaf:      *infoTreeLeafResponse,
		}

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, mock.Anything).
			Return(l1InfoTreeLeaf, nil)

		bridgeMocks.bridgeL1.EXPECT().
			GetProof(mock.Anything, depositCount, l1InfoTreeLeaf.MainnetExitRoot).
			Return(localExitTreeProof, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetRollupExitTreeMerkleProof(mock.Anything, mock.Anything, mock.Anything).
			Return(rollupExitTreeProof, nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", mainnetNetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, response.Code)

		var result bridgetypes.ClaimProof
		err := json.Unmarshal(response.Body.Bytes(), &result)
		require.NoError(t, err)
		require.Equal(t, expectedClaimProof, result)
	})

	t.Run("Invalid network id param", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "invalid")
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
	})

	t.Run("Invalid leaf index param", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", mainnetNetworkID))
		queryParams.Set(leafIndexParam, "invalid")
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("invalid %s parameter", leafIndexParam))
	})

	t.Run("Invalid deposit count param", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", mainnetNetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, "invalid")

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("invalid %s parameter", depositCountParam))
	})

	t.Run("ClaimProof for L1 network with nil L1 syncer", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL1 = nil

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeIndex).
			Return(l1InfoTreeLeaf, nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(mainnetNetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, response.Code)

		var result gin.H
		err := json.Unmarshal(response.Body.Bytes(), &result)
		require.NoError(t, err)
		require.Equal(t, "L1 bridge syncer is not available", result["error"])
	})

	t.Run("ClaimProof for L2 network with nil L2 syncer", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL2 = nil

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeIndex).
			Return(l1InfoTreeLeaf, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetLocalExitRoot(mock.Anything, l2NetworkID, l1InfoTreeLeaf.RollupExitRoot).
			Return(common.HexToHash("0x789"), nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, response.Code)

		var result gin.H
		err := json.Unmarshal(response.Body.Bytes(), &result)
		require.NoError(t, err)
		require.Equal(t, "L2 bridge syncer is not available", result["error"])
	})
}

func TestGetLastReorgEventHandler(t *testing.T) {
	t.Run("GetLastReorgEvent for L1 network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		reorgEvent := &bridgesync.LastReorg{
			DetectedAt: 1710000000,
			FromBlock:  100,
			ToBlock:    200,
		}

		bridgeMocks.bridgeL1.EXPECT().GetLastReorgEvent(mock.Anything).Return(reorgEvent, nil)

		queryParams := url.Values{
			networkIDParam: []string{strconv.Itoa(mainnetNetworkID)},
		}

		response := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/last-reorg-event?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, response.Code)

		var result bridgesync.LastReorg
		err := json.Unmarshal(response.Body.Bytes(), &result)
		require.NoError(t, err)
		require.Equal(t, *reorgEvent, result)
	})

	t.Run("GetLastReorgEvent for L2 network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		reorgEvent := &bridgesync.LastReorg{
			DetectedAt: 1710000001,
			FromBlock:  200,
			ToBlock:    300,
		}

		bridgeMocks.bridgeL2.EXPECT().GetLastReorgEvent(mock.Anything).Return(reorgEvent, nil)

		response := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/last-reorg-event?network_id=%d", BridgeV1Prefix, l2NetworkID))
		require.Equal(t, http.StatusOK, response.Code)

		var result bridgesync.LastReorg
		err := json.Unmarshal(response.Body.Bytes(), &result)
		require.NoError(t, err)
		require.Equal(t, *reorgEvent, result)
	})

	t.Run("GetLastReorgEvent with unsupported network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		unsupportedNetworkID := uint32(999)

		response := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/last-reorg-event?network_id=%d", BridgeV1Prefix, unsupportedNetworkID))
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("failed to get last reorg event, unsupported network %d", unsupportedNetworkID))
	})

	t.Run("GetLastReorgEvent for L1 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL1.EXPECT().GetLastReorgEvent(mock.Anything).Return(nil, errors.New(fooErrMsg))

		response := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/last-reorg-event?network_id=%d", BridgeV1Prefix, mainnetNetworkID))
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("failed to get last reorg event for the L1 network, error: %s", fooErrMsg))
	})

	t.Run("GetLastReorgEvent for L2 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL2.EXPECT().GetLastReorgEvent(mock.Anything).Return(nil, errors.New(barErrMsg))

		response := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/last-reorg-event?network_id=%d", BridgeV1Prefix, l2NetworkID))
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(),
			fmt.Sprintf("failed to get last reorg event for the L2 network (ID=%d), error: %s", l2NetworkID, barErrMsg))
	})

	t.Run("Invalid network id parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "invalid")

		response := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/last-reorg-event?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(),
			fmt.Sprintf("invalid %s parameter", networkIDParam))
	})

	t.Run("GetLastReorgEvent for L1 network with nil L1 syncer", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL1 = nil

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(mainnetNetworkID))

		response := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/last-reorg-event?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, response.Code)

		var result gin.H
		err := json.Unmarshal(response.Body.Bytes(), &result)
		require.NoError(t, err)
		require.Equal(t, "L1 bridge syncer is not available", result["error"])
	})

	t.Run("GetLastReorgEvent for L2 network with nil L2 syncer", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL2 = nil

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))

		response := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/last-reorg-event?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, response.Code)

		var result gin.H
		err := json.Unmarshal(response.Body.Bytes(), &result)
		require.NoError(t, err)
		require.Equal(t, "L2 bridge syncer is not available", result["error"])
	})
}

// performRequest is a helper function to perform GET HTTP requests in tests.
func performRequest(t *testing.T, router *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	return w
}

func TestGetSyncStatusHandler(t *testing.T) {
	// Deduplicated test cases for sync status
	testCases := []struct {
		description     string
		l1ContractCount uint32
		l1BridgeCount   uint32
		l1IsSynced      bool
		l2ContractCount uint32
		l2BridgeCount   uint32
		l2IsSynced      bool
	}{
		{
			description:     "successful sync status - both synced",
			l1ContractCount: 100, l1BridgeCount: 100, l1IsSynced: true,
			l2ContractCount: 200, l2BridgeCount: 200, l2IsSynced: true,
		},
		{
			description:     "successful sync status - both out of sync",
			l1ContractCount: 100, l1BridgeCount: 90, l1IsSynced: false,
			l2ContractCount: 200, l2BridgeCount: 180, l2IsSynced: false,
		},
		{
			description:     "successful sync status - L1 synced, L2 out of sync",
			l1ContractCount: 100, l1BridgeCount: 100, l1IsSynced: true,
			l2ContractCount: 200, l2BridgeCount: 150, l2IsSynced: false,
		},
		{
			description:     "successful sync status - L1 out of sync, L2 synced",
			l1ContractCount: 100, l1BridgeCount: 80, l1IsSynced: false,
			l2ContractCount: 200, l2BridgeCount: 200, l2IsSynced: true,
		},
		{
			description:     "successful sync status - zero counts",
			l1ContractCount: 0, l1BridgeCount: 0, l1IsSynced: true,
			l2ContractCount: 0, l2BridgeCount: 0, l2IsSynced: true,
		},
		{
			description:     "successful sync status - large numbers",
			l1ContractCount: 1000000, l1BridgeCount: 1000000, l1IsSynced: true,
			l2ContractCount: 2000000, l2BridgeCount: 2000000, l2IsSynced: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			b := newBridgeWithMocks(t, l2NetworkID)
			// L1 syncer status check and data retrieval
			b.bridgeL1.EXPECT().IsActive(mock.Anything).
				Return(true).
				Once()
			b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
				Return(tc.l1ContractCount, nil).
				Once()
			b.bridgeL1.EXPECT().
				GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
				Return(nil, int(tc.l1BridgeCount), nil).
				Once()

			// L2 syncer status check and data retrieval
			b.bridgeL2.EXPECT().IsActive(mock.Anything).
				Return(true).
				Once()
			b.bridgeL2.EXPECT().GetContractDepositCount(mock.Anything).
				Return(tc.l2ContractCount, nil).
				Once()
			b.bridgeL2.EXPECT().
				GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
				Return(nil, int(tc.l2BridgeCount), nil).
				Once()

			// Add expectations for block information when not synced
			if !tc.l1IsSynced {
				b.bridgeL1.EXPECT().GetLastProcessedBlock(mock.Anything).
					Return(uint64(1234), false, nil).
					Once()
				b.bridgeL1.EXPECT().GetLatestNetworkBlock(mock.Anything).
					Return(uint64(2555), nil).
					Once()
			}
			if !tc.l2IsSynced {
				b.bridgeL2.EXPECT().GetLastProcessedBlock(mock.Anything).
					Return(uint64(1234), false, nil).
					Once()
				b.bridgeL2.EXPECT().GetLatestNetworkBlock(mock.Anything).
					Return(uint64(2555), nil).
					Once()
			}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			b.bridge.GetSyncStatusHandler(c)

			require.Equal(t, http.StatusOK, w.Code)

			var response bridgetypes.SyncStatus
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			// Check L1 info
			require.NotNil(t, response.L1Info)
			require.Equal(t, tc.l1BridgeCount, response.L1Info.SynchronizedDepositCount)
			require.Equal(t, tc.l1ContractCount, response.L1Info.ContractDepositCount)
			require.Equal(t, tc.l1IsSynced, response.L1Info.IsSynced)
			require.True(t, response.L1Info.IsActive) // L1 syncer is always active in tests
			if !tc.l1IsSynced {
				require.Equal(t, uint64(1234), response.L1Info.LastProcessedBlock)
				require.Equal(t, uint64(2555), response.L1Info.NetworkBlock)
			}

			// Check L2 info
			require.NotNil(t, response.L2Info)
			require.Equal(t, tc.l2BridgeCount, response.L2Info.SynchronizedDepositCount)
			require.Equal(t, tc.l2ContractCount, response.L2Info.ContractDepositCount)
			require.Equal(t, tc.l2IsSynced, response.L2Info.IsSynced)
			require.True(t, response.L2Info.IsActive) // L2 syncer is always active in tests
			if !tc.l2IsSynced {
				require.Equal(t, uint64(1234), response.L2Info.LastProcessedBlock)
				require.Equal(t, uint64(2555), response.L2Info.NetworkBlock)
			}
		})
	}

	// Error test cases
	errorTestCases := []struct {
		description        string
		setupMocks         func() bridgeWithMocks
		expectedStatusCode int
		expectedError      string
	}{
		{
			description: "error getting L1 contract deposit count",
			setupMocks: func() bridgeWithMocks {
				b := newBridgeWithMocks(t, l2NetworkID)
				b.bridgeL1.EXPECT().IsActive(mock.Anything).
					Return(true).
					Once()
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(0), errors.New("L1 contract error")).
					Once()
				return b
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get deposit count from L1 bridge contract: L1 contract error",
		},
		{
			description: "error getting L1 bridges from database",
			setupMocks: func() bridgeWithMocks {
				b := newBridgeWithMocks(t, l2NetworkID)
				b.bridgeL1.EXPECT().IsActive(mock.Anything).
					Return(true).
					Once()
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(100), nil).
					Once()
				b.bridgeL1.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
					Return(nil, 0, errors.New("L1 database error"))
				return b
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get bridges from L1 database: L1 database error",
		},
		{
			description: "error getting L2 contract deposit count",
			setupMocks: func() bridgeWithMocks {
				b := newBridgeWithMocks(t, l2NetworkID)
				b.bridgeL1.EXPECT().IsActive(mock.Anything).
					Return(true).
					Once()
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(100), nil).
					Once()
				b.bridgeL1.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
					Return(nil, 100, nil).
					Once()
				b.bridgeL2.EXPECT().IsActive(mock.Anything).
					Return(true).
					Once()
				b.bridgeL2.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(0), errors.New("L2 contract error")).
					Once()
				return b
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get deposit count from L2 bridge contract: L2 contract error",
		},
		{
			description: "error getting L2 bridges from database",
			setupMocks: func() bridgeWithMocks {
				b := newBridgeWithMocks(t, l2NetworkID)
				b.bridgeL1.EXPECT().IsActive(mock.Anything).
					Return(true).
					Once()
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(100), nil).
					Once()
				b.bridgeL1.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
					Return(nil, 100, nil).
					Once()
				b.bridgeL2.EXPECT().IsActive(mock.Anything).
					Return(true).
					Once()
				b.bridgeL2.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(200), nil).
					Once()
				b.bridgeL2.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
					Return(nil, 0, errors.New("L2 database error")).
					Once()
				return b
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get bridges from L2 database: L2 database error",
		},
		{
			description: "error getting L1 contract deposit count with context timeout",
			setupMocks: func() bridgeWithMocks {
				b := newBridgeWithMocks(t, l2NetworkID)
				b.bridgeL1.EXPECT().IsActive(mock.Anything).
					Return(true).
					Once()
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(0), context.DeadlineExceeded).
					Once()
				return b
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get deposit count from L1 bridge contract: context deadline exceeded",
		},
		{
			description: "error getting L2 contract deposit count with context timeout",
			setupMocks: func() bridgeWithMocks {
				b := newBridgeWithMocks(t, l2NetworkID)
				b.bridgeL1.EXPECT().IsActive(mock.Anything).
					Return(true).
					Once()
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(100), nil).
					Once()
				b.bridgeL1.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
					Return(nil, 100, nil).
					Once()
				b.bridgeL2.EXPECT().IsActive(mock.Anything).
					Return(true).
					Once()
				b.bridgeL2.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(0), context.DeadlineExceeded).
					Once()
				return b
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get deposit count from L2 bridge contract: context deadline exceeded",
		},
		{
			description: "L1 syncer inactive - only isActive field populated",
			setupMocks: func() bridgeWithMocks {
				b := newBridgeWithMocks(t, l2NetworkID)
				b.bridgeL1.EXPECT().IsActive(mock.Anything).
					Return(false).
					Once()
				b.bridgeL2.EXPECT().IsActive(mock.Anything).
					Return(true).
					Once()
				b.bridgeL2.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(200), nil).
					Once()
				b.bridgeL2.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
					Return(nil, 200, nil).
					Once()
				return b
			},
			expectedStatusCode: http.StatusOK,
			expectedError:      "",
		},
		{
			description: "L2 syncer inactive - only isActive field populated",
			setupMocks: func() bridgeWithMocks {
				b := newBridgeWithMocks(t, l2NetworkID)
				b.bridgeL1.EXPECT().IsActive(mock.Anything).
					Return(true).
					Once()
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(100), nil).
					Once()
				b.bridgeL1.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
					Return(nil, 100, nil).
					Once()
				b.bridgeL2.EXPECT().IsActive(mock.Anything).
					Return(false).
					Once()
				return b
			},
			expectedStatusCode: http.StatusOK,
			expectedError:      "",
		},
		{
			description: "Both syncers inactive - only isActive fields populated",
			setupMocks: func() bridgeWithMocks {
				b := newBridgeWithMocks(t, l2NetworkID)
				b.bridgeL1.EXPECT().IsActive(mock.Anything).
					Return(false).
					Once()
				b.bridgeL2.EXPECT().IsActive(mock.Anything).
					Return(false).
					Once()
				return b
			},
			expectedStatusCode: http.StatusOK,
			expectedError:      "",
		},
	}

	for _, tc := range errorTestCases {
		t.Run(tc.description, func(t *testing.T) {
			b := tc.setupMocks()

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			b.bridge.GetSyncStatusHandler(c)

			require.Equal(t, tc.expectedStatusCode, w.Code)

			if tc.expectedStatusCode == http.StatusOK {
				// For successful responses, check the sync status structure
				var response bridgetypes.SyncStatus
				err := json.Unmarshal(w.Body.Bytes(), &response)
				require.NoError(t, err)

				// For inactive syncer test cases, verify only isActive field is populated
				switch tc.description {
				case "L1 syncer inactive - only isActive field populated":
					require.NotNil(t, response.L1Info)
					require.False(t, response.L1Info.IsActive)
					require.Equal(t, uint32(0), response.L1Info.SynchronizedDepositCount)
					require.Equal(t, uint32(0), response.L1Info.ContractDepositCount)
					require.False(t, response.L1Info.IsSynced)
					require.Equal(t, uint64(0), response.L1Info.LastProcessedBlock)
					require.Equal(t, uint64(0), response.L1Info.NetworkBlock)

					require.NotNil(t, response.L2Info)
					require.True(t, response.L2Info.IsActive)
					require.Equal(t, uint32(200), response.L2Info.SynchronizedDepositCount)
					require.Equal(t, uint32(200), response.L2Info.ContractDepositCount)
					require.True(t, response.L2Info.IsSynced)
				case "L2 syncer inactive - only isActive field populated":
					require.NotNil(t, response.L1Info)
					require.True(t, response.L1Info.IsActive)
					require.Equal(t, uint32(100), response.L1Info.SynchronizedDepositCount)
					require.Equal(t, uint32(100), response.L1Info.ContractDepositCount)
					require.True(t, response.L1Info.IsSynced)

					require.NotNil(t, response.L2Info)
					require.False(t, response.L2Info.IsActive)
					require.Equal(t, uint32(0), response.L2Info.SynchronizedDepositCount)
					require.Equal(t, uint32(0), response.L2Info.ContractDepositCount)
					require.False(t, response.L2Info.IsSynced)
					require.Equal(t, uint64(0), response.L2Info.LastProcessedBlock)
					require.Equal(t, uint64(0), response.L2Info.NetworkBlock)
				case "Both syncers inactive - only isActive fields populated":
					require.NotNil(t, response.L1Info)
					require.False(t, response.L1Info.IsActive)
					require.Equal(t, uint32(0), response.L1Info.SynchronizedDepositCount)
					require.Equal(t, uint32(0), response.L1Info.ContractDepositCount)
					require.False(t, response.L1Info.IsSynced)
					require.Equal(t, uint64(0), response.L1Info.LastProcessedBlock)
					require.Equal(t, uint64(0), response.L1Info.NetworkBlock)

					require.NotNil(t, response.L2Info)
					require.False(t, response.L2Info.IsActive)
					require.Equal(t, uint32(0), response.L2Info.SynchronizedDepositCount)
					require.Equal(t, uint32(0), response.L2Info.ContractDepositCount)
					require.False(t, response.L2Info.IsSynced)
					require.Equal(t, uint64(0), response.L2Info.LastProcessedBlock)
					require.Equal(t, uint64(0), response.L2Info.NetworkBlock)
				}
			} else {
				// For error responses, check the error message
				var response gin.H
				err := json.Unmarshal(w.Body.Bytes(), &response)
				require.NoError(t, err)
				require.Equal(t, tc.expectedError, response["error"])
			}
		})
	}
}

func TestHealthCheckHandler(t *testing.T) {
	b := newBridgeWithMocks(t, l2NetworkID)
	w := performRequest(t, b.router, "/")
	require.Equal(t, http.StatusOK, w.Code)

	var response bridgetypes.HealthCheckResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	require.Equal(t, "ok", response.Status)
	require.NotEmpty(t, response.Time)
	require.NotEmpty(t, response.Version)
}

func TestPopulateNetworkSyncInfo(t *testing.T) {
	b := newBridgeWithMocks(t, l2NetworkID)

	testCases := []struct {
		description        string
		contractCount      uint32
		bridgeCount        uint32
		lastProcessedBlock uint64
		networkBlock       uint64
		expectedIsSynced   bool
		shouldHaveBlocks   bool
	}{
		{
			description:        "synced state - no block info needed",
			contractCount:      100,
			bridgeCount:        100,
			lastProcessedBlock: 1234,
			networkBlock:       2555,
			expectedIsSynced:   true,
			shouldHaveBlocks:   false,
		},
		{
			description:        "not synced - block info should be populated",
			contractCount:      100,
			bridgeCount:        90,
			lastProcessedBlock: 1234,
			networkBlock:       2555,
			expectedIsSynced:   false,
			shouldHaveBlocks:   true,
		},
		{
			description:        "zero counts - synced",
			contractCount:      0,
			bridgeCount:        0,
			lastProcessedBlock: 0,
			networkBlock:       0,
			expectedIsSynced:   true,
			shouldHaveBlocks:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
				Return(tc.contractCount, nil).
				Once()
			b.bridgeL1.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
				Return(nil, int(tc.bridgeCount), nil).
				Once()

			if !tc.expectedIsSynced {
				b.bridgeL1.EXPECT().GetLastProcessedBlock(mock.Anything).
					Return(tc.lastProcessedBlock, false, nil).
					Once()
				b.bridgeL1.EXPECT().GetLatestNetworkBlock(mock.Anything).
					Return(tc.networkBlock, nil).
					Once()
			}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			ctx := context.Background()
			networkInfo := &bridgetypes.NetworkSyncInfo{
				IsActive: true,
			}

			result := b.bridge.populateNetworkSyncInfo(ctx, c, b.bridgeL1, networkInfo, "L1")

			require.Equal(t, http.StatusOK, result)
			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, tc.contractCount, networkInfo.ContractDepositCount)
			require.Equal(t, tc.bridgeCount, networkInfo.SynchronizedDepositCount)
			require.Equal(t, tc.expectedIsSynced, networkInfo.IsSynced)

			if tc.shouldHaveBlocks {
				require.Equal(t, tc.lastProcessedBlock, networkInfo.LastProcessedBlock)
				require.Equal(t, tc.networkBlock, networkInfo.NetworkBlock)
			} else {
				require.Equal(t, uint64(0), networkInfo.LastProcessedBlock)
				require.Equal(t, uint64(0), networkInfo.NetworkBlock)
			}
		})
	}

	// Test error cases
	errorTestCases := []struct {
		description        string
		setupMocks         func()
		expectedStatusCode int
		expectedError      string
	}{
		{
			description: "error getting contract deposit count",
			setupMocks: func() {
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(0), errors.New("contract error")).
					Once()
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get deposit count from L1 bridge contract: contract error",
		},
		{
			description: "error getting bridges from database",
			setupMocks: func() {
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(100), nil).
					Once()
				b.bridgeL1.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
					Return(nil, 0, errors.New("database error")).
					Once()
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get bridges from L1 database: database error",
		},
	}

	for _, tc := range errorTestCases {
		t.Run(tc.description, func(t *testing.T) {
			tc.setupMocks()

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			ctx := context.Background()

			networkInfo := &bridgetypes.NetworkSyncInfo{
				IsActive: true,
			}

			result := b.bridge.populateNetworkSyncInfo(ctx, c, b.bridgeL1, networkInfo, "L1")

			require.Equal(t, tc.expectedStatusCode, result)
			require.Equal(t, tc.expectedStatusCode, w.Code)

			var response gin.H
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			require.Equal(t, tc.expectedError, response["error"])
		})
	}
}

func TestGetFirstL1InfoTreeIndexForL1Bridge_GetRootByLERFallback(t *testing.T) {
	ctx := context.Background()
	networkID := uint32(1)
	b := newBridgeWithMocks(t, networkID)

	lastInfo := &l1infotreesync.L1InfoTreeLeaf{
		BlockNumber:     1000,
		MainnetExitRoot: common.HexToHash("0xabc"),
		L1InfoTreeIndex: 1000,
	}

	firstInfo := &l1infotreesync.L1InfoTreeLeaf{
		BlockNumber:     10,
		MainnetExitRoot: common.HexToHash("0xdef"),
		L1InfoTreeIndex: 10,
	}

	depositCount := uint32(500)
	expectedIndex := uint32(500)

	t.Run("GetRootByLER fails but GetLastRoot succeeds", func(t *testing.T) {
		// Setup mocks for the fallback scenario
		b.l1InfoTree.EXPECT().GetLastInfo().
			Return(lastInfo, nil).
			Once()

		// First call to GetRootByLER fails
		b.bridgeL1.EXPECT().GetRootByLER(ctx, lastInfo.MainnetExitRoot).
			Return(nil, errors.New("LER not found")).
			Once()

		// Fallback to GetLastRoot succeeds
		fallbackRoot := &tree.Root{
			Index:    depositCount,
			BlockNum: uint64(depositCount),
		}
		b.bridgeL1.EXPECT().GetLastRoot(ctx).
			Return(fallbackRoot, nil).
			Once()

		// After GetLastRoot succeeds, GetInfoByIndex is called with the root's index
		updatedLastInfo := &l1infotreesync.L1InfoTreeLeaf{
			BlockNumber:     uint64(depositCount),
			MainnetExitRoot: common.HexToHash("0x123"),
			L1InfoTreeIndex: depositCount,
		}
		b.l1InfoTree.EXPECT().GetInfoByIndex(ctx, depositCount).
			Return(updatedLastInfo, nil).
			Once()

		// Continue with normal flow
		b.l1InfoTree.EXPECT().GetFirstInfo().
			Return(firstInfo, nil).
			Once()

		// Mock the binary search calls
		targetInfo := &l1infotreesync.L1InfoTreeLeaf{
			BlockNumber:     uint64(expectedIndex),
			MainnetExitRoot: common.HexToHash("0x123"),
			L1InfoTreeIndex: expectedIndex,
		}
		b.l1InfoTree.EXPECT().GetFirstInfoAfterBlock(mock.Anything).
			Return(targetInfo, nil).
			Once()

		// Mock successful GetRootByLER in binary search
		b.bridgeL1.EXPECT().GetRootByLER(ctx, targetInfo.MainnetExitRoot).
			Return(&tree.Root{Index: depositCount}, nil).
			Once()

		// Execute the function
		actualIndex, err := b.bridge.getFirstL1InfoTreeIndexForL1Bridge(ctx, depositCount)

		// Verify results
		require.NoError(t, err)
		require.Equal(t, expectedIndex, actualIndex)

		// Verify all mocks were called as expected
		b.l1InfoTree.AssertExpectations(t)
		b.bridgeL1.AssertExpectations(t)
	})

	t.Run("GetRootByLER fails and GetLastRoot also fails", func(t *testing.T) {
		// Setup mocks for the double failure scenario
		b.l1InfoTree.EXPECT().GetLastInfo().
			Return(lastInfo, nil).
			Once()

		// First call to GetRootByLER fails
		b.bridgeL1.EXPECT().GetRootByLER(ctx, lastInfo.MainnetExitRoot).
			Return(nil, errors.New("LER not found")).
			Once()

		// Fallback to GetLastRoot also fails
		b.bridgeL1.EXPECT().GetLastRoot(ctx).
			Return(nil, errors.New("last root not available")).
			Once()

		// Execute the function
		actualIndex, err := b.bridge.getFirstL1InfoTreeIndexForL1Bridge(ctx, depositCount)

		// Verify results - should return error from GetLastRoot
		require.Error(t, err)
		require.Equal(t, uint32(0), actualIndex)
		require.Contains(t, err.Error(), "last root not available")

		// Verify all mocks were called as expected
		b.l1InfoTree.AssertExpectations(t)
		b.bridgeL1.AssertExpectations(t)
	})

	t.Run("GetRootByLER fails, GetLastRoot succeeds but root index is too low", func(t *testing.T) {
		// Setup mocks for the fallback scenario with insufficient index
		b.l1InfoTree.EXPECT().GetLastInfo().
			Return(lastInfo, nil).
			Once()

		// First call to GetRootByLER fails
		b.bridgeL1.EXPECT().GetRootByLER(ctx, lastInfo.MainnetExitRoot).
			Return(nil, errors.New("LER not found")).
			Once()

		// Fallback to GetLastRoot succeeds but returns low index
		fallbackRoot := &tree.Root{
			Index:    depositCount - 100, // Lower than depositCount
			BlockNum: uint64(depositCount - 100),
		}
		b.bridgeL1.EXPECT().GetLastRoot(ctx).
			Return(fallbackRoot, nil).
			Once()

		// After GetLastRoot succeeds, GetInfoByIndex is called with the root's index
		updatedLastInfo := &l1infotreesync.L1InfoTreeLeaf{
			BlockNumber:     uint64(depositCount - 100),
			MainnetExitRoot: common.HexToHash("0x123"),
			L1InfoTreeIndex: depositCount - 100,
		}
		b.l1InfoTree.EXPECT().GetInfoByIndex(ctx, depositCount-100).
			Return(updatedLastInfo, nil).
			Once()

		// Execute the function
		actualIndex, err := b.bridge.getFirstL1InfoTreeIndexForL1Bridge(ctx, depositCount)

		// Verify results - should return ErrNotOnL1Info
		require.Error(t, err)
		require.Equal(t, uint32(0), actualIndex)
		require.Equal(t, ErrNotOnL1Info, err)

		// Verify all mocks were called as expected
		b.l1InfoTree.AssertExpectations(t)
		b.bridgeL1.AssertExpectations(t)
	})

	t.Run("GetRootByLER fails, GetLastRoot succeeds with higher index", func(t *testing.T) {
		// Setup mocks for the fallback scenario with higher index
		b.l1InfoTree.EXPECT().GetLastInfo().
			Return(lastInfo, nil).
			Once()

		// First call to GetRootByLER fails
		b.bridgeL1.EXPECT().GetRootByLER(ctx, lastInfo.MainnetExitRoot).
			Return(nil, errors.New("LER not found")).
			Once()

		// Fallback to GetLastRoot succeeds with higher index
		fallbackRoot := &tree.Root{
			Index:    depositCount + 50, // Higher than depositCount
			BlockNum: uint64(depositCount + 50),
		}
		b.bridgeL1.EXPECT().GetLastRoot(ctx).
			Return(fallbackRoot, nil).
			Once()

		// After GetLastRoot succeeds, GetInfoByIndex is called with the root's index
		updatedLastInfo := &l1infotreesync.L1InfoTreeLeaf{
			BlockNumber:     uint64(depositCount + 50),
			MainnetExitRoot: common.HexToHash("0x123"),
			L1InfoTreeIndex: depositCount + 50,
		}
		b.l1InfoTree.EXPECT().GetInfoByIndex(ctx, depositCount+50).
			Return(updatedLastInfo, nil).
			Once()

		// Continue with normal flow
		b.l1InfoTree.EXPECT().GetFirstInfo().
			Return(firstInfo, nil).
			Once()

		// Mock the binary search calls
		targetInfo := &l1infotreesync.L1InfoTreeLeaf{
			BlockNumber:     uint64(expectedIndex),
			MainnetExitRoot: common.HexToHash("0x123"),
			L1InfoTreeIndex: expectedIndex,
		}
		b.l1InfoTree.EXPECT().GetFirstInfoAfterBlock(mock.Anything).
			Return(targetInfo, nil).
			Once()

		// Mock successful GetRootByLER in binary search
		b.bridgeL1.EXPECT().GetRootByLER(ctx, targetInfo.MainnetExitRoot).
			Return(&tree.Root{Index: depositCount}, nil).
			Once()

		// Execute the function
		actualIndex, err := b.bridge.getFirstL1InfoTreeIndexForL1Bridge(ctx, depositCount)

		// Verify results
		require.NoError(t, err)
		require.Equal(t, expectedIndex, actualIndex)

		// Verify all mocks were called as expected
		b.l1InfoTree.AssertExpectations(t)
		b.bridgeL1.AssertExpectations(t)
	})

	t.Run("GetRootByLER fails, GetLastRoot succeeds but GetInfoByIndex fails", func(t *testing.T) {
		// Setup mocks for the fallback scenario where GetInfoByIndex fails
		b.l1InfoTree.EXPECT().GetLastInfo().
			Return(lastInfo, nil).
			Once()

		// First call to GetRootByLER fails
		b.bridgeL1.EXPECT().GetRootByLER(ctx, lastInfo.MainnetExitRoot).
			Return(nil, errors.New("LER not found")).
			Once()

		// Fallback to GetLastRoot succeeds
		fallbackRoot := &tree.Root{
			Index:    depositCount,
			BlockNum: uint64(depositCount),
		}
		b.bridgeL1.EXPECT().GetLastRoot(ctx).
			Return(fallbackRoot, nil).
			Once()

		// GetInfoByIndex fails after GetLastRoot succeeds
		b.l1InfoTree.EXPECT().GetInfoByIndex(ctx, depositCount).
			Return(nil, errors.New("failed to get info by index")).
			Once()

		// Execute the function
		actualIndex, err := b.bridge.getFirstL1InfoTreeIndexForL1Bridge(ctx, depositCount)

		// Verify results - should return error from GetInfoByIndex
		require.Error(t, err)
		require.Equal(t, uint32(0), actualIndex)
		require.Contains(t, err.Error(), "failed to get last info for L1")

		// Verify all mocks were called as expected
		b.l1InfoTree.AssertExpectations(t)
		b.bridgeL1.AssertExpectations(t)
	})
}

func TestGetClaimsByGERHandler(t *testing.T) {
	validGER := "0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757"
	gerHash := common.HexToHash(validGER)

	sampleClaim := &claimsynctypes.Claim{
		BlockNum:           1,
		GlobalIndex:        big.NewInt(1),
		OriginNetwork:      0,
		OriginAddress:      common.HexToAddress("0x1"),
		DestinationNetwork: 10,
		DestinationAddress: common.HexToAddress("0x2"),
		Amount:             common.Big0,
		GlobalExitRoot:     gerHash,
	}

	t.Run("L2 network success", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		expectedClaims := []*claimsynctypes.Claim{sampleClaim}

		bridgeMocks.claimL2.EXPECT().
			GetClaimsByGER(mock.Anything, gerHash).
			Return(expectedClaims, nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set("global_exit_root", validGER)

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims-by-ger?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimsByGERResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, 1, response.Count)
		require.Len(t, response.Claims, 1)
	})

	t.Run("L1 network success", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		expectedClaims := []*claimsynctypes.Claim{sampleClaim}

		bridgeMocks.claimL1.EXPECT().
			GetClaimsByGER(mock.Anything, gerHash).
			Return(expectedClaims, nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")
		queryParams.Set("global_exit_root", validGER)

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims-by-ger?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimsByGERResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, 1, response.Count)
	})

	t.Run("missing global_exit_root", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims-by-ger?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "global_exit_root is mandatory")
	})

	t.Run("invalid global_exit_root", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")
		queryParams.Set("global_exit_root", "not_a_hash")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims-by-ger?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "invalid global_exit_root")
	})

	t.Run("unsupported network ID", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "999")
		queryParams.Set("global_exit_root", validGER)

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims-by-ger?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("L1 bridge nil", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.claimL1 = nil

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")
		queryParams.Set("global_exit_root", validGER)

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims-by-ger?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		require.Contains(t, w.Body.String(), "L1 claim syncer is not available")
	})

	t.Run("L2 bridge nil", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.claimL2 = nil

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set("global_exit_root", validGER)

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims-by-ger?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		require.Contains(t, w.Body.String(), "L2 claim syncer is not available")
	})

	t.Run("L1 service error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.claimL1.EXPECT().
			GetClaimsByGER(mock.Anything, gerHash).
			Return(nil, errors.New("db error"))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")
		queryParams.Set("global_exit_root", validGER)

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims-by-ger?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get claims by GER")
	})

	t.Run("L2 service error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.claimL2.EXPECT().
			GetClaimsByGER(mock.Anything, gerHash).
			Return(nil, errors.New("db error"))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set("global_exit_root", validGER)

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims-by-ger?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get claims by GER")
	})

	t.Run("empty result", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.claimL2.EXPECT().
			GetClaimsByGER(mock.Anything, gerHash).
			Return([]*claimsynctypes.Claim{}, nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set("global_exit_root", validGER)

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims-by-ger?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimsByGERResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, 0, response.Count)
	})
}

func TestGetBridgeByDepositCountHandler(t *testing.T) {
	sampleBridge := &bridgesync.Bridge{
		BlockNum:           1,
		BlockPos:           0,
		DepositCount:       42,
		LeafType:           0,
		OriginNetwork:      0,
		OriginAddress:      common.HexToAddress("0xAAA"),
		DestinationNetwork: 10,
		DestinationAddress: common.HexToAddress("0xBBB"),
		Amount:             big.NewInt(1000),
		Metadata:           []byte{},
		TxnSender:          common.HexToAddress("0xCCC"),
		ToAddress:          common.HexToAddress("0xDDD"),
	}

	t.Run("L2 network success", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL2.EXPECT().
			GetBridgeByDepositCount(mock.Anything, uint32(42)).
			Return(sampleBridge, nil)
		bridgeMocks.upgradeQuerier.EXPECT().
			GetUpgradeBlock(mock.Anything, mock.Anything).
			Return(uint64(0))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set(depositCountParam, "42")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridge-by-deposit-count?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.BridgeResponse
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, uint32(42), response.DepositCount)
	})

	t.Run("L1 network success", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL1.EXPECT().
			GetBridgeByDepositCount(mock.Anything, uint32(42)).
			Return(sampleBridge, nil)
		bridgeMocks.upgradeQuerier.EXPECT().
			GetUpgradeBlock(mock.Anything, mock.Anything).
			Return(uint64(0))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")
		queryParams.Set(depositCountParam, "42")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridge-by-deposit-count?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("missing deposit_count", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridge-by-deposit-count?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid deposit_count", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")
		queryParams.Set(depositCountParam, "not_a_number")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridge-by-deposit-count?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("L1 not found", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL1.EXPECT().
			GetBridgeByDepositCount(mock.Anything, uint32(99)).
			Return(nil, db.ErrNotFound)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")
		queryParams.Set(depositCountParam, "99")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridge-by-deposit-count?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusNotFound, w.Code)
		require.Contains(t, w.Body.String(), "not found")
	})

	t.Run("L2 not found", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL2.EXPECT().
			GetBridgeByDepositCount(mock.Anything, uint32(99)).
			Return(nil, db.ErrNotFound)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set(depositCountParam, "99")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridge-by-deposit-count?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("L1 bridge nil", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL1 = nil

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")
		queryParams.Set(depositCountParam, "42")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridge-by-deposit-count?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		require.Contains(t, w.Body.String(), "L1 bridge syncer is not available")
	})

	t.Run("L2 bridge nil", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL2 = nil

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set(depositCountParam, "42")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridge-by-deposit-count?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		require.Contains(t, w.Body.String(), "L2 bridge syncer is not available")
	})

	t.Run("L1 service error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL1.EXPECT().
			GetBridgeByDepositCount(mock.Anything, uint32(42)).
			Return(nil, errors.New("db error"))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")
		queryParams.Set(depositCountParam, "42")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridge-by-deposit-count?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get bridge by deposit count")
	})

	t.Run("L2 service error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL2.EXPECT().
			GetBridgeByDepositCount(mock.Anything, uint32(42)).
			Return(nil, errors.New("db error"))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set(depositCountParam, "42")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridge-by-deposit-count?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get bridge by deposit count")
	})

	t.Run("unsupported network ID", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "999")
		queryParams.Set(depositCountParam, "42")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridge-by-deposit-count?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestGetBridgesByContentHandler(t *testing.T) {
	validOriginAddr := "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	validDestAddr := "0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	validAmount := "1000000000000000000"

	sampleBridge := &bridgesync.Bridge{
		BlockNum:           1,
		BlockPos:           0,
		DepositCount:       7,
		LeafType:           0,
		OriginNetwork:      0,
		OriginAddress:      common.HexToAddress(validOriginAddr),
		DestinationNetwork: 10,
		DestinationAddress: common.HexToAddress(validDestAddr),
		Amount:             big.NewInt(1000000000000000000),
		Metadata:           []byte{},
		TxnSender:          common.HexToAddress("0xCCC"),
		ToAddress:          common.HexToAddress("0xDDD"),
	}

	buildQuery := func(networkID int) url.Values {
		q := url.Values{}
		q.Set(networkIDParam, strconv.Itoa(networkID))
		q.Set("leaf_type", "0")
		q.Set("origin_address", validOriginAddr)
		q.Set("destination_network", strconv.Itoa(int(l2NetworkID)))
		q.Set("destination_address", validDestAddr)
		q.Set("amount", validAmount)
		return q
	}

	t.Run("L2 network success", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesByContent(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*bridgesync.Bridge{sampleBridge}, nil)
		bridgeMocks.upgradeQuerier.EXPECT().
			GetUpgradeBlock(mock.Anything, mock.Anything).
			Return(uint64(0))

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, buildQuery(int(l2NetworkID)).Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.BridgesByContentResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, 1, response.Count)
	})

	t.Run("L1 network success", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL1.EXPECT().
			GetBridgesByContent(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*bridgesync.Bridge{sampleBridge}, nil)
		bridgeMocks.upgradeQuerier.EXPECT().
			GetUpgradeBlock(mock.Anything, mock.Anything).
			Return(uint64(0))

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, buildQuery(mainnetNetworkID).Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.BridgesByContentResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, 1, response.Count)
		require.Len(t, response.Bridges, 1)
	})

	t.Run("with metadata", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesByContent(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*bridgesync.Bridge{}, nil)
		bridgeMocks.upgradeQuerier.EXPECT().
			GetUpgradeBlock(mock.Anything, mock.Anything).
			Return(uint64(0))

		q := buildQuery(int(l2NetworkID))
		q.Set("metadata", "0xdeadbeef")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, q.Encode()))
		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("invalid metadata", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		q := buildQuery(int(l2NetworkID))
		q.Set("metadata", "0xZZZZZZ")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, q.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "invalid metadata")
	})

	t.Run("missing origin_address", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		q := buildQuery(int(l2NetworkID))
		q.Del("origin_address")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, q.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "origin_address is mandatory")
	})

	t.Run("invalid origin_address", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		q := buildQuery(int(l2NetworkID))
		q.Set("origin_address", "not_an_address")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, q.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "invalid")
	})

	t.Run("missing destination_address", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		q := buildQuery(int(l2NetworkID))
		q.Del("destination_address")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, q.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "destination_address is mandatory")
	})

	t.Run("invalid destination_address", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		q := buildQuery(int(l2NetworkID))
		q.Set("destination_address", "not_an_address")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, q.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "invalid")
	})

	t.Run("missing amount", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		q := buildQuery(int(l2NetworkID))
		q.Del("amount")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, q.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "amount is mandatory")
	})

	t.Run("invalid amount", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		q := buildQuery(int(l2NetworkID))
		q.Set("amount", "not_a_number")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, q.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "invalid amount")
	})

	t.Run("leaf_type overflow", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		q := buildQuery(int(l2NetworkID))
		q.Set("leaf_type", "256")

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, q.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "leaf_type must be 0 or 1")
	})

	t.Run("L1 bridge nil", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL1 = nil

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, buildQuery(mainnetNetworkID).Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		require.Contains(t, w.Body.String(), "L1 bridge syncer is not available")
	})

	t.Run("L2 bridge nil", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL2 = nil

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, buildQuery(int(l2NetworkID)).Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		require.Contains(t, w.Body.String(), "L2 bridge syncer is not available")
	})

	t.Run("L1 service error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL1.EXPECT().
			GetBridgesByContent(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("db error"))

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, buildQuery(mainnetNetworkID).Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get bridges by content")
	})

	t.Run("L2 service error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesByContent(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("db error"))

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, buildQuery(int(l2NetworkID)).Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get bridges by content")
	})

	t.Run("unsupported network ID", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges-by-content?%s", BridgeV1Prefix, buildQuery(999).Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestGetClaimCandidatesHandler(t *testing.T) {
	buildQuery := func(destIDs []uint32, toLER, fromLER, pageNumber, pageSize string) url.Values {
		q := url.Values{}
		for _, id := range destIDs {
			q.Add(destinationNetworkIDsParam, strconv.FormatUint(uint64(id), 10))
		}
		if toLER != "" {
			q.Set(toLERParam, toLER)
		}
		if fromLER != "" {
			q.Set(fromLERParam, fromLER)
		}
		if pageNumber != "" {
			q.Set(pageNumberParam, pageNumber)
		}
		if pageSize != "" {
			q.Set(pageSizeParam, pageSize)
		}
		return q
	}

	t.Run("happy path with proof verifying via tree.VerifyProof against to_ler", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		br := &bridgesync.Bridge{
			BlockNum:           1,
			BlockPos:           0,
			LeafType:           0,
			OriginNetwork:      l2NetworkID,
			OriginAddress:      common.HexToAddress("0xAAA"),
			DestinationNetwork: 0,
			DestinationAddress: common.HexToAddress("0xBBB"),
			Amount:             big.NewInt(1000),
			Metadata:           []byte{},
			DepositCount:       7,
			TxnSender:          common.HexToAddress("0xCCC"),
			ToAddress:          common.HexToAddress("0xDDD"),
		}

		var proof tree.Proof // zero-value proof; content is irrelevant, only consistency with the root matters
		leafHash := br.Hash()
		toLER := merkletree.CalculateRoot(leafHash, proof, br.DepositCount)

		bridgeMocks.bridgeL2.EXPECT().
			GetRootByLER(mock.Anything, toLER).
			Return(&tree.Root{Index: br.DepositCount}, nil)
		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesInDepositRange(mock.Anything, DefaultPage, DefaultPageSize,
				(*uint64)(nil), uint64(br.DepositCount), []uint32{0}).
			Return([]*bridgesync.Bridge{br}, 1, nil)
		bridgeMocks.bridgeL2.EXPECT().
			GetProof(mock.Anything, br.DepositCount, toLER).
			Return(proof, nil)
		bridgeMocks.upgradeQuerier.EXPECT().
			GetUpgradeBlock(mock.Anything, mock.Anything).
			Return(uint64(0))

		queryParams := buildQuery([]uint32{0}, toLER.Hex(), "", "", "")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claim-candidates?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimCandidatesResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, 1, response.Count)
		require.Len(t, response.ClaimCandidates, 1)

		candidate := response.ClaimCandidates[0]
		require.Equal(t, bridgetypes.Hash(toLER.Hex()), candidate.LocalExitRoot)
		require.Equal(t, br.DepositCount, candidate.Bridge.DepositCount)

		// Reconstruct the proof from the response and independently verify it against to_ler.
		var respProof tree.Proof
		for i, h := range candidate.ProofLocalExitRoot {
			respProof[i] = common.HexToHash(string(h))
		}
		require.NoError(t, merkletree.VerifyProof(leafHash, respProof, br.DepositCount, toLER))
	})

	t.Run("unknown to_ler returns 404", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		unknownLER := common.HexToHash("0xdeadbeef")
		bridgeMocks.bridgeL2.EXPECT().
			GetRootByLER(mock.Anything, unknownLER).
			Return(nil, db.ErrNotFound)

		queryParams := buildQuery([]uint32{0}, unknownLER.Hex(), "", "", "")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claim-candidates?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusNotFound, w.Code)
		require.Contains(t, w.Body.String(), "not found")
	})

	t.Run("from_ler equal to to_ler returns empty list", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		sameLER := common.HexToHash("0xaaaa")
		root := &tree.Root{Index: 12}
		fromDC := uint64(root.Index)

		bridgeMocks.bridgeL2.EXPECT().
			GetRootByLER(mock.Anything, sameLER).
			Return(root, nil)
		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesInDepositRange(mock.Anything, DefaultPage, DefaultPageSize,
				&fromDC, uint64(root.Index), []uint32{0}).
			Return([]*bridgesync.Bridge{}, 0, nil)
		bridgeMocks.upgradeQuerier.EXPECT().
			GetUpgradeBlock(mock.Anything, mock.Anything).
			Return(uint64(0))

		queryParams := buildQuery([]uint32{0}, sameLER.Hex(), sameLER.Hex(), "", "")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claim-candidates?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimCandidatesResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, 0, response.Count)
		require.Empty(t, response.ClaimCandidates)
	})

	t.Run("destination_network_ids cap exceeded returns 400", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := buildQuery([]uint32{1, 2, 3, 4, 5, 6}, common.HexToHash("0x01").Hex(), "", "", "")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claim-candidates?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "too many")
	})

	t.Run("pagination parameters are forwarded", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		toLER := common.HexToHash("0x02")
		root := &tree.Root{Index: 20}
		page := uint32(2)
		pageSize := uint32(5)

		bridgeMocks.bridgeL2.EXPECT().
			GetRootByLER(mock.Anything, toLER).
			Return(root, nil)
		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesInDepositRange(mock.Anything, page, pageSize,
				(*uint64)(nil), uint64(root.Index), []uint32{1}).
			Return([]*bridgesync.Bridge{}, 0, nil)
		bridgeMocks.upgradeQuerier.EXPECT().
			GetUpgradeBlock(mock.Anything, mock.Anything).
			Return(uint64(0))

		queryParams := buildQuery([]uint32{1}, toLER.Hex(), "", "2", "5")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claim-candidates?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimCandidatesResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, 0, response.Count)
	})

	t.Run("missing destination_network_ids returns 400", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := buildQuery(nil, common.HexToHash("0x01").Hex(), "", "", "")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claim-candidates?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("%s is mandatory", destinationNetworkIDsParam))
	})

	t.Run("missing to_ler returns 400", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := buildQuery([]uint32{0}, "", "", "", "")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claim-candidates?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("%s is mandatory", toLERParam))
	})

	t.Run("invalid to_ler returns 400", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := buildQuery([]uint32{0}, "not-a-hash", "", "", "")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claim-candidates?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid from_ler returns 400", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := buildQuery([]uint32{0}, common.HexToHash("0x01").Hex(), "not-a-hash", "", "")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claim-candidates?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("unknown from_ler returns 404", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		toLER := common.HexToHash("0x01")
		unknownFromLER := common.HexToHash("0xbeef")

		bridgeMocks.bridgeL2.EXPECT().
			GetRootByLER(mock.Anything, toLER).
			Return(&tree.Root{Index: 3}, nil)
		bridgeMocks.bridgeL2.EXPECT().
			GetRootByLER(mock.Anything, unknownFromLER).
			Return(nil, db.ErrNotFound)

		queryParams := buildQuery([]uint32{0}, toLER.Hex(), unknownFromLER.Hex(), "", "")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claim-candidates?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("L2 bridge syncer not available returns 503", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.bridgeL2 = nil

		queryParams := buildQuery([]uint32{0}, common.HexToHash("0x01").Hex(), "", "", "")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claim-candidates?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		require.Contains(t, w.Body.String(), "L2 bridge syncer is not available")
	})

	t.Run("GetBridgesInDepositRange error returns 500", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		toLER := common.HexToHash("0x01")
		bridgeMocks.bridgeL2.EXPECT().
			GetRootByLER(mock.Anything, toLER).
			Return(&tree.Root{Index: 3}, nil)
		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesInDepositRange(mock.Anything, DefaultPage, DefaultPageSize,
				(*uint64)(nil), uint64(3), []uint32{0}).
			Return(nil, 0, errors.New(fooErrMsg))

		queryParams := buildQuery([]uint32{0}, toLER.Hex(), "", "", "")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claim-candidates?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GetProof error returns 500", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		br := &bridgesync.Bridge{DepositCount: 3, Amount: big.NewInt(0)}
		toLER := common.HexToHash("0x01")

		bridgeMocks.bridgeL2.EXPECT().
			GetRootByLER(mock.Anything, toLER).
			Return(&tree.Root{Index: 3}, nil)
		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesInDepositRange(mock.Anything, DefaultPage, DefaultPageSize,
				(*uint64)(nil), uint64(3), []uint32{0}).
			Return([]*bridgesync.Bridge{br}, 1, nil)
		bridgeMocks.bridgeL2.EXPECT().
			GetProof(mock.Anything, br.DepositCount, toLER).
			Return(tree.Proof{}, errors.New(barErrMsg))
		bridgeMocks.upgradeQuerier.EXPECT().
			GetUpgradeBlock(mock.Anything, mock.Anything).
			Return(uint64(0))

		queryParams := buildQuery([]uint32{0}, toLER.Hex(), "", "", "")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claim-candidates?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusInternalServerError, w.Code)
	})
}
