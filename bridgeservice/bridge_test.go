package bridgeservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	mocks "github.com/agglayer/aggkit/bridgeservice/mocks"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/lastgersync"
	"github.com/agglayer/aggkit/log"
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
	bridge       *BridgeService
	l1InfoTree   *mocks.L1InfoTreer
	injectedGERs *mocks.LastGERer
	bridgeL1     *mocks.Bridger
	bridgeL2     *mocks.Bridger
}

func newBridgeWithMocks(t *testing.T, networkID uint32) bridgeWithMocks {
	t.Helper()
	b := bridgeWithMocks{
		l1InfoTree:   mocks.NewL1InfoTreer(t),
		injectedGERs: mocks.NewLastGERer(t),
		bridgeL1:     mocks.NewBridger(t),
		bridgeL2:     mocks.NewBridger(t),
	}
	logger := log.WithFields("module", "test bridge service")
	cfg := &Config{
		Logger:       logger,
		Address:      "localhost",
		ReadTimeout:  0,
		WriteTimeout: 0,
		NetworkID:    networkID,
	}
	b.bridge = New(cfg, b.l1InfoTree, b.injectedGERs, b.bridgeL1, b.bridgeL2)
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
				Calldata:           common.Hex2Bytes("efabcd"),
				IsNativeToken:      true,
			},
		}
		bridgesResp := aggkitcommon.MapSlice(expectedBridges, NewBridgeResponse)

		bridgeMocks.bridgeL1.EXPECT().
			GetBridgesPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything, mock.Anything).
			Return(expectedBridges, len(expectedBridges), nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(mainnetNetworkID))
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet,
			fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.BridgesResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		require.Equal(t, bridgesResp, response.Bridges)
		require.Equal(t, len(expectedBridges), response.Count)
	})

	t.Run("GetBridges for L1 network error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL1.EXPECT().GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, fmt.Errorf("L1 network error"))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(mainnetNetworkID))
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet,
			fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get bridges for the L1 network")
	})

	t.Run("GetBridges for L2 network error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL2.EXPECT().GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, fmt.Errorf("L2 network error"))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet,
			fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get bridges for the L2 network")
	})

	t.Run("GetBridges for L2 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		expectedBridges := []*bridgesync.Bridge{
			{
				BlockNum:           1,
				BlockPos:           1,
				LeafType:           1,
				OriginNetwork:      10,
				OriginAddress:      common.HexToAddress("0x2"),
				DestinationNetwork: 20,
				DestinationAddress: common.HexToAddress("0x3"),
				Amount:             common.Big0,
				DepositCount:       0,
				Metadata:           []byte("metadata"),
				Calldata:           []byte{},
				IsNativeToken:      true,
			},
		}
		bridgesResp := aggkitcommon.MapSlice(expectedBridges, NewBridgeResponse)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything, mock.Anything).
			Return(expectedBridges, len(expectedBridges), nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
		queryParams.Set(pageNumberParam, "1")
		queryParams.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet,
			fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.BridgesResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		require.Equal(t, bridgesResp, response.Bridges)
		require.Equal(t, len(expectedBridges), response.Count)
	})

	t.Run("GetBridges with unsupported network", func(t *testing.T) {
		unsupportedNetworkID := uint32(999)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{networkIDParam: []string{fmt.Sprintf("%d", unsupportedNetworkID)}}
		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("unsupported network id: %d", unsupportedNetworkID))
	})

	t.Run("GetBridges invalid network id", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{networkIDParam: []string{"foo"}}
		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
	})
}

func TestGetClaimsHandler(t *testing.T) {
	t.Run("GetClaims for L1 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		expectedClaims := []*bridgesync.Claim{
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
		claimsResp := aggkitcommon.MapSlice(expectedClaims, func(claim *bridgesync.Claim) *bridgetypes.ClaimResponse {
			return NewClaimResponse(claim, false)
		})

		bridgeMocks.bridgeL1.EXPECT().
			GetClaimsPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything).
			Return(expectedClaims, len(expectedClaims), nil)

		queryParams := url.Values{
			networkIDParam:  []string{fmt.Sprintf("%d", mainnetNetworkID)},
			pageNumberParam: []string{"1"},
			pageSizeParam:   []string{"10"},
		}

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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

		expectedClaims := []*bridgesync.Claim{
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
		claimsResp := aggkitcommon.MapSlice(expectedClaims, func(claim *bridgesync.Claim) *bridgetypes.ClaimResponse {
			return NewClaimResponse(claim, false)
		})

		bridgeMocks.bridge.networkID = 10
		bridgeMocks.bridgeL2.EXPECT().
			GetClaimsPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything).
			Return(expectedClaims, len(expectedClaims), nil)

		query := url.Values{}
		query.Set(networkIDParam, "10")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()), nil)
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

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()), nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("unsupported network id: %d", unsupportedNetworkID))
	})

	t.Run("GetClaims for L1 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL1.EXPECT().
			GetClaimsPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(fooErrMsg))

		query := url.Values{}
		query.Set(networkIDParam, "0")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()), nil)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get claims for the L1 network")
	})

	t.Run("GetClaims for L2 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL2.EXPECT().
			GetClaimsPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(barErrMsg))

		query := url.Values{}
		query.Set(networkIDParam, "10")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()), nil)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get claims for the L2 network")
	})

	t.Run("GetClaims for L2 network failed invalid network id", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		query := url.Values{}
		query.Set(networkIDParam, "foo")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, query.Encode()), nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
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
				Calldata:            common.Hex2Bytes("efabcd"),
			},
		}
		tokenMappingsResp := aggkitcommon.MapSlice(tokenMappings, NewTokenMappingResponse)

		bridgeMocks.bridgeL1.EXPECT().GetTokenMappings(mock.Anything, page, pageSize).
			Return(tokenMappings, len(tokenMappings), nil)

		query := url.Values{}
		query.Set(networkIDParam, "0")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, query.Encode()), nil)
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
				Calldata:            []byte{},
				Type:                bridgetypes.SovereignToken,
				IsNotMintable:       true,
			},
		}
		tokenMappingsResp := aggkitcommon.MapSlice(tokenMappings, NewTokenMappingResponse)

		bridgeMocks.bridgeL2.EXPECT().GetTokenMappings(mock.Anything, page, pageSize).
			Return(tokenMappings, len(tokenMappings), nil)

		query := url.Values{}
		query.Set(networkIDParam, "10")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, query.Encode()), nil)
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

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, query.Encode()), nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "unsupported network id: 999")
	})

	t.Run("GetTokenMappingsHandler for L1 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL1.EXPECT().GetTokenMappings(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(fooErrMsg))

		query := url.Values{}
		query.Set(networkIDParam, "0")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, query.Encode()), nil)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("failed to fetch token mappings: %s", fooErrMsg))
	})

	t.Run("GetTokenMappingsHandler for L2 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL2.EXPECT().GetTokenMappings(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(barErrMsg))

		query := url.Values{}
		query.Set(networkIDParam, "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, query.Encode()), nil)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("failed to fetch token mappings: %s", barErrMsg))
	})

	t.Run("GetTokenMappingsHandler for L2 network failed invalid network id", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		query := url.Values{}
		query.Set(networkIDParam, "foo")
		query.Set(pageNumberParam, "1")
		query.Set(pageSizeParam, "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/token-mappings?%s", BridgeV1Prefix, query.Encode()), nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
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
				Calldata:            common.Hex2Bytes("efabcd"),
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

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/legacy-token-migrations?%s", BridgeV1Prefix, queryParams.Encode()), nil)

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

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/legacy-token-migrations?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/legacy-token-migrations?%s", BridgeV1Prefix, queryParams.Encode()), nil)

		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("unsupported network id: %d", unsupportedNetworkID))
	})

	t.Run("GetLegacyTokenMigrations for L1 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL1.EXPECT().
			GetLegacyTokenMigrations(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, fmt.Errorf(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set("network_id", "0")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/legacy-token-migrations?%s", BridgeV1Prefix, queryParams.Encode()), nil)

		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), fooErrMsg)
	})

	t.Run("GetLegacyTokenMigrations for L2 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL2.EXPECT().
			GetLegacyTokenMigrations(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, fmt.Errorf(barErrMsg))

		queryParams := url.Values{}
		queryParams.Set("network_id", fmt.Sprintf("%d", l2NetworkID))

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/legacy-token-migrations?%s", BridgeV1Prefix, queryParams.Encode()), nil)

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

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/legacy-token-migrations?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
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

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/l1-info-tree-index?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/l1-info-tree-index?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/l1-info-tree-index?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("unsupported network id: %d", invalidNetworkID))
	})

	t.Run("Error from GetLastInfo", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.l1InfoTree.EXPECT().
			GetLastInfo().
			Return(nil, fmt.Errorf(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set("network_id", "0")
		queryParams.Set("deposit_count", fmt.Sprintf("%d", depositCount))

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/l1-info-tree-index?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), fooErrMsg)
	})

	t.Run("Error from GetRootByLER", func(t *testing.T) {
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
			Return(nil, fmt.Errorf(barErrMsg))

		queryParams := url.Values{}
		queryParams.Set("network_id", "0")
		queryParams.Set("deposit_count", fmt.Sprintf("%d", depositCount))

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/l1-info-tree-index?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), barErrMsg)
	})

	t.Run("Invalid network ID parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set("network_id", "invalid")
		queryParams.Set("deposit_count", "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/l1-info-tree-index?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
	})

	t.Run("Invalid deposit count parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set("network_id", "10")
		queryParams.Set("deposit_count", "test")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/l1-info-tree-index?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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
			Return(lastgersync.GlobalExitRootInfo{
				GlobalExitRoot:  l1InfoTreeLeaf.GlobalExitRoot,
				L1InfoTreeIndex: l1InfoTreeLeaf.L1InfoTreeIndex,
			}, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(l1InfoTreeLeaf, nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "10")
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeLeaf.L1InfoTreeIndex))

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()), nil)

		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("unsupported network id: %d", unsupportedNetworkID))
	})

	t.Run("L1 network error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(nil, fmt.Errorf(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", mainnetNetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeLeaf.L1InfoTreeIndex))

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(),
			fmt.Sprintf("failed to get L1 info tree leaf (network id=%d, leaf index=%d), error: %s",
				mainnetNetworkID, l1InfoTreeLeaf.L1InfoTreeIndex, fooErrMsg))
	})

	t.Run("L2 network - GetFirstGERAfterL1InfoTreeIndex error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.injectedGERs.EXPECT().
			GetFirstGERAfterL1InfoTreeIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(lastgersync.GlobalExitRootInfo{}, fmt.Errorf(barErrMsg))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", l2NetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeLeaf.L1InfoTreeIndex))

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("failed to get injected global exit root for leaf index=%d", l1InfoTreeLeaf.L1InfoTreeIndex))
	})

	t.Run("L2 network - GetInfoByIndex error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.injectedGERs.EXPECT().
			GetFirstGERAfterL1InfoTreeIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(lastgersync.GlobalExitRootInfo{
				GlobalExitRoot:  l1InfoTreeLeaf.GlobalExitRoot,
				L1InfoTreeIndex: l1InfoTreeLeaf.L1InfoTreeIndex,
			}, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(nil, fmt.Errorf(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", l2NetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeLeaf.L1InfoTreeIndex))

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(),
			fmt.Sprintf("failed to get L1 info tree leaf (leaf index=%d), error: %s", l1InfoTreeLeaf.L1InfoTreeIndex, fooErrMsg))
	})

	t.Run("Invalid network id param", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "invalid")
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeLeaf.L1InfoTreeIndex))

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
	})

	t.Run("Invalid leaf index param", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "10")
		queryParams.Set(leafIndexParam, "invalid")

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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
			Return(nil, fmt.Errorf(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(mainnetNetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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
			Return(tree.Proof{}, fmt.Errorf(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", mainnetNetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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
			Return(common.Hash{}, fmt.Errorf(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", l2NetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
	})

	t.Run("Invalid leaf index param", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", mainnetNetworkID))
		queryParams.Set(leafIndexParam, "invalid")
		queryParams.Set(depositCountParam, fmt.Sprintf("%d", depositCount))

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("invalid %s parameter", leafIndexParam))
	})

	t.Run("Invalid deposit count param", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", mainnetNetworkID))
		queryParams.Set(leafIndexParam, fmt.Sprintf("%d", l1InfoTreeIndex))
		queryParams.Set(depositCountParam, "invalid")

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("invalid %s parameter", depositCountParam))
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

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet,
			fmt.Sprintf("%s/last-reorg-event?%s", BridgeV1Prefix, queryParams.Encode()), nil)
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

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet,
			fmt.Sprintf("%s/last-reorg-event?network_id=%d", BridgeV1Prefix, l2NetworkID), nil)
		require.Equal(t, http.StatusOK, response.Code)

		var result bridgesync.LastReorg
		err := json.Unmarshal(response.Body.Bytes(), &result)
		require.NoError(t, err)
		require.Equal(t, *reorgEvent, result)
	})

	t.Run("GetLastReorgEvent with unsupported network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		unsupportedNetworkID := uint32(999)

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet,
			fmt.Sprintf("%s/last-reorg-event?network_id=%d", BridgeV1Prefix, unsupportedNetworkID), nil)
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("failed to get last reorg event, unsupported network %d", unsupportedNetworkID))
	})

	t.Run("GetLastReorgEvent for L1 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL1.EXPECT().GetLastReorgEvent(mock.Anything).Return(nil, fmt.Errorf(fooErrMsg))

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet,
			fmt.Sprintf("%s/last-reorg-event?network_id=%d", BridgeV1Prefix, mainnetNetworkID), nil)
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(), fmt.Sprintf("failed to get last reorg event for the L1 network, error: %s", fooErrMsg))
	})

	t.Run("GetLastReorgEvent for L2 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL2.EXPECT().GetLastReorgEvent(mock.Anything).Return(nil, fmt.Errorf(barErrMsg))

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet,
			fmt.Sprintf("%s/last-reorg-event?network_id=%d", BridgeV1Prefix, l2NetworkID), nil)
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(),
			fmt.Sprintf("failed to get last reorg event for the L2 network (ID=%d), error: %s", l2NetworkID, barErrMsg))
	})

	t.Run("Invalid network id parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "invalid")

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet,
			fmt.Sprintf("%s/last-reorg-event?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(),
			fmt.Sprintf("invalid %s parameter", networkIDParam))
	})
}

// performRequest is a helper function to perform HTTP requests in tests.
func performRequest(t *testing.T, router *gin.Engine, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()

	var bodyReader io.Reader

	switch v := body.(type) {
	case string:
		// If the body is a raw string, use it directly.
		bodyReader = strings.NewReader(v)
	case []byte:
		// If the body is a raw byte array, use it directly.
		bodyReader = bytes.NewBuffer(v)
	default:
		if body != nil {
			jsonBytes, err := json.Marshal(body)
			require.NoError(t, err)

			// Check if the marshaled JSON is an empty object (i.e., `{}`)
			if string(jsonBytes) == "{}" {
				t.Errorf("Marshaled JSON is empty. Input: %+v", body)
			}

			bodyReader = bytes.NewBuffer(jsonBytes)
		}
	}

	req := httptest.NewRequest(method, path, bodyReader)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	return w
}

func TestGetSyncStatusHandler(t *testing.T) {
	b := newBridgeWithMocks(t, l2NetworkID)

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
			b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
				Return(tc.l1ContractCount, nil).
				Once()
			b.bridgeL1.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
				Return(nil, int(tc.l1BridgeCount), nil).
				Once()
			b.bridgeL2.EXPECT().GetContractDepositCount(mock.Anything).
				Return(tc.l2ContractCount, nil).
				Once()
			b.bridgeL2.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
				Return(nil, int(tc.l2BridgeCount), nil).
				Once()

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			b.bridge.GetSyncStatusHandler(c)

			require.Equal(t, http.StatusOK, w.Code)

			var response bridgetypes.SyncStatus
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			require.Equal(t, tc.l1BridgeCount, response.L1Info.BridgeDepositCount)
			require.Equal(t, tc.l1ContractCount, response.L1Info.ContractDepositCount)
			require.Equal(t, tc.l1IsSynced, response.L1Info.IsSynced)
			require.Equal(t, tc.l2BridgeCount, response.L2Info.BridgeDepositCount)
			require.Equal(t, tc.l2ContractCount, response.L2Info.ContractDepositCount)
			require.Equal(t, tc.l2IsSynced, response.L2Info.IsSynced)
		})
	}

	// Error test cases
	errorTestCases := []struct {
		description        string
		setupMocks         func()
		expectedStatusCode int
		expectedError      string
	}{
		{
			description: "error getting L1 contract deposit count",
			setupMocks: func() {
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(0), errors.New("L1 contract error")).
					Once()
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get deposit count from L1 bridge contract: L1 contract error",
		},
		{
			description: "error getting L1 bridges from database",
			setupMocks: func() {
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(100), nil).
					Once()
				b.bridgeL1.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
					Return(nil, 0, errors.New("L1 database error")).
					Once()
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get bridges from L1 database: L1 database error",
		},
		{
			description: "error getting L2 contract deposit count",
			setupMocks: func() {
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(100), nil).
					Once()
				b.bridgeL1.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
					Return(nil, 100, nil).
					Once()
				b.bridgeL2.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(0), errors.New("L2 contract error")).
					Once()
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get deposit count from L2 bridge contract: L2 contract error",
		},
		{
			description: "error getting L2 bridges from database",
			setupMocks: func() {
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(100), nil).
					Once()
				b.bridgeL1.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
					Return(nil, 100, nil).
					Once()
				b.bridgeL2.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(200), nil).
					Once()
				b.bridgeL2.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
					Return(nil, 0, errors.New("L2 database error")).
					Once()
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get bridges from L2 database: L2 database error",
		},
		{
			description: "error getting L1 contract deposit count with context timeout",
			setupMocks: func() {
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(0), context.DeadlineExceeded).
					Once()
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get deposit count from L1 bridge contract: context deadline exceeded",
		},
		{
			description: "error getting L2 contract deposit count with context timeout",
			setupMocks: func() {
				b.bridgeL1.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(100), nil).
					Once()
				b.bridgeL1.EXPECT().GetBridgesPaged(mock.Anything, uint32(1), uint32(1), (*uint64)(nil), []uint32(nil), "").
					Return(nil, 100, nil).
					Once()
				b.bridgeL2.EXPECT().GetContractDepositCount(mock.Anything).
					Return(uint32(0), context.DeadlineExceeded).
					Once()
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedError:      "failed to get deposit count from L2 bridge contract: context deadline exceeded",
		},
	}

	for _, tc := range errorTestCases {
		t.Run(tc.description, func(t *testing.T) {
			tc.setupMocks()

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			b.bridge.GetSyncStatusHandler(c)

			require.Equal(t, tc.expectedStatusCode, w.Code)
			var response gin.H
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			require.Equal(t, tc.expectedError, response["error"])
		})
	}
}

func TestHealthCheckHandler(t *testing.T) {
	b := newBridgeWithMocks(t, l2NetworkID)
	w := performRequest(t, b.bridge.router, http.MethodGet, "/", nil)
	require.Equal(t, http.StatusOK, w.Code)

	var response bridgetypes.HealthCheckResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	require.Equal(t, "ok", response.Status)
	require.NotEmpty(t, response.Time)
	require.NotEmpty(t, response.Version)
}
