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
	"testing"
	"time"

	"github.com/0xPolygon/cdk-rpc/rpc"
	mocks "github.com/agglayer/aggkit/bridgeservice/mocks"
	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/claimsponsor"
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
	logger := log.WithFields("module", "test bridge service")
	b.bridge = New(
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

		expectedBridges := []*bridgesync.BridgeResponse{{
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
				Calldata:           common.Hex2Bytes("efabcd"),
				IsNativeToken:      true,
			},
			BridgeHash: common.HexToHash("0x1"),
		}}

		bridgeMocks.bridgeL1.EXPECT().
			GetBridgesPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything).
			Return(expectedBridges, len(expectedBridges), nil)

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/bridges?network_id=0&page_number=1&page_size=10", nil)
		require.Equal(t, http.StatusOK, w.Code)

		var response types.BridgesResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		require.Equal(t, expectedBridges, response.Bridges)
		require.Equal(t, len(expectedBridges), response.Count)
	})

	t.Run("GetBridges for L1 network error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL1.EXPECT().GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, fmt.Errorf("L1 network error"))

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/bridges?network_id=0&page_number=1&page_size=10", nil)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get bridges for the L1 network")
	})

	t.Run("GetBridges for L2 network error", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL2.EXPECT().GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, fmt.Errorf("L2 network error"))

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/bridges?network_id=10&page=1&page_size=10", nil)

		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get bridges for the L2 network")
	})

	t.Run("GetBridges for L2 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		expectedBridges := []*bridgesync.BridgeResponse{{
			Bridge: bridgesync.Bridge{
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
			BridgeHash: common.HexToHash("0x2"),
		}}

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesPaged(mock.Anything, page, pageSize, mock.Anything, mock.Anything).
			Return(expectedBridges, len(expectedBridges), nil)

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/bridges?network_id=10&page=1&page_size=10", nil)
		require.Equal(t, http.StatusOK, w.Code)

		var response types.BridgesResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		require.Equal(t, expectedBridges, response.Bridges)
		require.Equal(t, len(expectedBridges), response.Count)
	})

	t.Run("GetBridges with unsupported network", func(t *testing.T) {
		unsupportedNetworkID := uint32(999)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, fmt.Sprintf("/bridges?network_id=%d", unsupportedNetworkID), nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("failed to get bridges unsupported network %d", unsupportedNetworkID))
	})

	t.Run("GetBridges invalid network id", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/bridges?network_id=foo", nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
	})
}

func TestGetClaimsHandler(t *testing.T) {
	t.Run("GetClaims for L1 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		expectedClaims := []*bridgesync.ClaimResponse{
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

		bridgeMocks.bridgeL1.EXPECT().
			GetClaimsPaged(mock.Anything, page, pageSize, mock.Anything).
			Return(expectedClaims, len(expectedClaims), nil)

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/claims?network_id=0&page=1&page_size=10", nil)
		require.Equal(t, http.StatusOK, w.Code)

		var response types.ClaimsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, expectedClaims, response.Claims)
		require.Equal(t, len(expectedClaims), response.Count)
	})

	t.Run("GetClaims for L2 network", func(t *testing.T) {
		page := uint32(1)
		pageSize := uint32(10)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		expectedClaims := []*bridgesync.ClaimResponse{
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
		bridgeMocks.bridgeL2.EXPECT().
			GetClaimsPaged(mock.Anything, page, pageSize, mock.Anything).
			Return(expectedClaims, len(expectedClaims), nil)

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/claims?network_id=10&page=1&page_size=10", nil)
		require.Equal(t, http.StatusOK, w.Code)

		var response types.ClaimsResult
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.Equal(t, expectedClaims, response.Claims)
		require.Equal(t, len(expectedClaims), response.Count)
	})

	t.Run("GetClaims with unsupported network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/claims?network_id=999", nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "unsupported network 999")
	})

	t.Run("GetClaims for L1 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL1.EXPECT().
			GetClaimsPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(fooErrMsg))

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/claims?network_id=0&page=1&page_size=10", nil)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get claims for the L1 network")
	})

	t.Run("GetClaims for L2 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL2.EXPECT().
			GetClaimsPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(barErrMsg))

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/claims?network_id=10&page=1&page_size=10", nil)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "failed to get claims for the L2 network")
	})

	t.Run("GetClaims for L2 network failed invalid network id", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/claims?network_id=foo&page=1&page_size=10", nil)
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

		bridgeMocks.bridgeL1.EXPECT().GetTokenMappings(mock.Anything, page, pageSize).
			Return(tokenMappings, len(tokenMappings), nil)

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/token-mappings?network_id=0&page_number=1&page_size=10", nil)
		require.Equal(t, http.StatusOK, w.Code)

		var response types.TokenMappingsResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, len(tokenMappings), response.Count)
		require.Equal(t, tokenMappings, response.TokenMappings)

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
				Type:                bridgesync.SovereignToken,
				IsNotMintable:       true,
			},
		}

		bridgeMocks.bridgeL2.EXPECT().GetTokenMappings(mock.Anything, page, pageSize).
			Return(tokenMappings, len(tokenMappings), nil)

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/token-mappings?network_id=10&page_number=1&page_size=10", nil)
		require.Equal(t, http.StatusOK, w.Code)

		var response types.TokenMappingsResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, len(tokenMappings), response.Count)
		require.Equal(t, tokenMappings, response.TokenMappings)

		bridgeMocks.bridgeL2.AssertExpectations(t)
	})

	t.Run("GetTokenMappingsHandler with unsupported network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/token-mappings?network_id=999", nil)

		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "unsupported network id 999")
	})

	t.Run("GetTokenMappingsHandler for L1 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL1.EXPECT().GetTokenMappings(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(fooErrMsg))

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/token-mappings?network_id=0", nil)

		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("failed to fetch token mappings: %s", fooErrMsg))
	})

	t.Run("GetTokenMappingsHandler for L2 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL2.EXPECT().GetTokenMappings(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New(barErrMsg))

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/token-mappings?network_id=10", nil)

		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("failed to fetch token mappings: %s", barErrMsg))
	})

	t.Run("GetTokenMappingsHandler for L2 network failed invalid network id", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/token-mappings?network_id=foo&page=1&page_size=10", nil)
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

		bridgeMocks.bridgeL1.EXPECT().
			GetLegacyTokenMigrations(mock.Anything, page, pageSize).
			Return(tokenMigrations, len(tokenMigrations), nil)

		queryParams := url.Values{}
		queryParams.Set("network_id", "0")
		queryParams.Set("page_number", "1")
		queryParams.Set("page_size", "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/legacy-token-migrations?"+queryParams.Encode(), nil)

		require.Equal(t, http.StatusOK, w.Code)

		var response types.LegacyTokenMigrationsResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, len(tokenMigrations), response.Count)
		require.Equal(t, tokenMigrations, response.TokenMigrations)

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

		bridgeMocks.bridgeL2.EXPECT().
			GetLegacyTokenMigrations(mock.Anything, page, pageSize).
			Return(tokenMigrations, len(tokenMigrations), nil)

		queryParams := url.Values{}
		queryParams.Set("network_id", fmt.Sprintf("%d", l2NetworkID))
		queryParams.Set("page_number", "1")
		queryParams.Set("page_size", "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/legacy-token-migrations?"+queryParams.Encode(), nil)
		require.Equal(t, http.StatusOK, w.Code)

		var response types.LegacyTokenMigrationsResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, len(tokenMigrations), response.Count)
		require.Equal(t, tokenMigrations, response.TokenMigrations)

		bridgeMocks.bridgeL2.AssertExpectations(t)
	})

	t.Run("GetLegacyTokenMigrations with unsupported network", func(t *testing.T) {
		unsupportedNetworkID := uint32(999)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set("network_id", fmt.Sprintf("%d", unsupportedNetworkID))

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/legacy-token-migrations?"+queryParams.Encode(), nil)

		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("unsupported network id %d", unsupportedNetworkID))
	})

	t.Run("GetLegacyTokenMigrations for L1 network failed", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridgeL1.EXPECT().
			GetLegacyTokenMigrations(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, 0, fmt.Errorf(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set("network_id", "0")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/legacy-token-migrations?"+queryParams.Encode(), nil)

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

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/legacy-token-migrations?"+queryParams.Encode(), nil)

		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), barErrMsg)
	})

	t.Run("GetLegacyTokenMigrations for L2 network failed invalid network id", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/legacy-token-migrations?network_id=foo&page=1&page_size=10", nil)
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

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/l1-info-tree-index-for-bridge?"+queryParams.Encode(), nil)
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

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/l1-info-tree-index-for-bridge?"+queryParams.Encode(), nil)
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

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/l1-info-tree-index-for-bridge?"+queryParams.Encode(), nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("unsupported network id %d", invalidNetworkID))
	})

	t.Run("Error from GetLastInfo", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		bridgeMocks.l1InfoTree.EXPECT().
			GetLastInfo().
			Return(nil, fmt.Errorf(fooErrMsg))

		queryParams := url.Values{}
		queryParams.Set("network_id", "0")
		queryParams.Set("deposit_count", fmt.Sprintf("%d", depositCount))

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/l1-info-tree-index-for-bridge?"+queryParams.Encode(), nil)
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

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/l1-info-tree-index-for-bridge?"+queryParams.Encode(), nil)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), barErrMsg)
	})

	t.Run("Invalid network ID parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set("network_id", "invalid")
		queryParams.Set("deposit_count", "10")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/l1-info-tree-index-for-bridge?"+queryParams.Encode(), nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", networkIDParam))
	})

	t.Run("Invalid deposit count parameter", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		queryParams := url.Values{}
		queryParams.Set("network_id", "10")
		queryParams.Set("deposit_count", "test")

		w := performRequest(t, bridgeMocks.bridge.router, http.MethodGet, "/l1-info-tree-index-for-bridge?"+queryParams.Encode(), nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), fmt.Sprintf("invalid %s parameter", depositCountParam))
	})
}

func TestGetLastReorgEvent(t *testing.T) {
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
		require.NotNil(t, err)
		require.Equal(t, rpc.InvalidRequestErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("this client does not support network %d", unsupportedNetworkID))
		require.Nil(t, result)
	})

	t.Run("GetLastReorgEvent for L1 network failed", func(t *testing.T) {
		bridgeMocks.setBridgeL1(mocks.NewBridger(t))
		bridgeMocks.bridgeL1.EXPECT().GetLastReorgEvent(mock.Anything).Return(nil, errors.New(fooErrMsg))

		result, err := bridgeMocks.bridge.GetLastReorgEvent(mainnetNetworkID)
		require.NotNil(t, err)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("failed to get last reorg event for the L1 network, error: %s", fooErrMsg))
		require.Nil(t, result)
	})

	t.Run("GetLastReorgEvent for L2 network failed", func(t *testing.T) {
		bridgeMocks.setBridgeL2(mocks.NewBridger(t))
		bridgeMocks.bridgeL2.EXPECT().GetLastReorgEvent(mock.Anything).Return(nil, errors.New(barErrMsg))

		result, err := bridgeMocks.bridge.GetLastReorgEvent(l2NetworkID)
		require.NotNil(t, err)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("failed to get last reorg event for the L2 network (ID=%d), error: %s", l2NetworkID, barErrMsg))
		require.Nil(t, result)
	})
}

func TestInjectedInfoAfterIndex(t *testing.T) {
	bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

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
		append(l1InfoTreeLeaf.MainnetExitRoot.Bytes(),
			l1InfoTreeLeaf.RollupExitRoot.Bytes()...))

	t.Run("InjectedInfoAfterIndex for L1 network", func(t *testing.T) {
		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(l1InfoTreeLeaf, nil)

		result, err := bridgeMocks.bridge.InjectedInfoAfterIndex(mainnetNetworkID, l1InfoTreeLeaf.L1InfoTreeIndex)
		require.NoError(t, err)
		require.Equal(t, l1InfoTreeLeaf, result)
	})

	t.Run("InjectedInfoAfterIndex for L2 network", func(t *testing.T) {
		bridgeMocks.injectedGERs.EXPECT().
			GetFirstGERAfterL1InfoTreeIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(
				lastgersync.GlobalExitRootInfo{
					GlobalExitRoot:  l1InfoTreeLeaf.GlobalExitRoot,
					L1InfoTreeIndex: l1InfoTreeLeaf.L1InfoTreeIndex,
				}, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(l1InfoTreeLeaf, nil)

		result, err := bridgeMocks.bridge.InjectedInfoAfterIndex(l2NetworkID, l1InfoTreeLeaf.L1InfoTreeIndex)
		require.NoError(t, err)
		require.Equal(t, l1InfoTreeLeaf, result)
	})

	t.Run("InjectedInfoAfterIndex for unsupported network", func(t *testing.T) {
		unsupportedNetworkID := uint32(100)

		result, err := bridgeMocks.bridge.InjectedInfoAfterIndex(unsupportedNetworkID, l1InfoTreeLeaf.L1InfoTreeIndex)
		require.NotNil(t, err)
		require.Equal(t, rpc.InvalidRequestErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, fmt.Sprintf("this client does not support network %d", unsupportedNetworkID))
		require.Nil(t, result)
	})

	t.Run("InjectedInfoAfterIndex for L1 network failed", func(t *testing.T) {
		bridgeMocks = newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, mock.Anything).
			Return(nil, errors.New(fooErrMsg))

		result, err := bridgeMocks.bridge.InjectedInfoAfterIndex(mainnetNetworkID, l1InfoTreeLeaf.L1InfoTreeIndex)
		require.NotNil(t, err)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err,
			fmt.Sprintf("failed to get L1 info tree leaf for index %d, error: %s", l1InfoTreeLeaf.L1InfoTreeIndex, fooErrMsg))
		require.Nil(t, result)
	})

	t.Run("InjectedInfoAfterIndex for L2 network failed (GetFirstGERAfterL1InfoTreeIndex failure)", func(t *testing.T) {
		bridgeMocks = newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.injectedGERs.EXPECT().
			GetFirstGERAfterL1InfoTreeIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(lastgersync.GlobalExitRootInfo{}, errors.New(barErrMsg))

		result, err := bridgeMocks.bridge.InjectedInfoAfterIndex(l2NetworkID, l1InfoTreeLeaf.L1InfoTreeIndex)
		require.NotNil(t, err)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err,
			fmt.Sprintf("failed to get injected global exit root for L1 info tree index %d, error: %s", l1InfoTreeLeaf.L1InfoTreeIndex, barErrMsg))
		require.Nil(t, result)
	})

	t.Run("InjectedInfoAfterIndex for L2 network failed (GetInfoByIndex failure)", func(t *testing.T) {
		bridgeMocks = newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, mock.Anything).
			Return(nil, errors.New(fooErrMsg))

		bridgeMocks.injectedGERs.EXPECT().
			GetFirstGERAfterL1InfoTreeIndex(mock.Anything, l1InfoTreeLeaf.L1InfoTreeIndex).
			Return(lastgersync.GlobalExitRootInfo{
				GlobalExitRoot:  l1InfoTreeLeaf.GlobalExitRoot,
				L1InfoTreeIndex: l1InfoTreeLeaf.L1InfoTreeIndex,
			}, nil)

		result, err := bridgeMocks.bridge.InjectedInfoAfterIndex(l2NetworkID, l1InfoTreeLeaf.L1InfoTreeIndex)
		require.NotNil(t, err)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err,
			fmt.Sprintf("failed to get L1 info tree leaf for index %d na L2 network (ID=%d), error: %s",
				l1InfoTreeLeaf.L1InfoTreeIndex, l2NetworkID, fooErrMsg))
		require.Nil(t, result)
	})
}

func TestGetSponsoredClaimStatus(t *testing.T) {
	t.Run("Client does not support sponsored claims", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.sponsor = nil

		result, err := bridgeMocks.bridge.GetSponsoredClaimStatus(common.Big1)
		require.Nil(t, result)
		require.NotNil(t, err)
		require.Equal(t, rpc.InvalidRequestErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, "this client does not support claim sponsoring")
	})

	t.Run("Failed to get claim status", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.sponsor.EXPECT().GetClaim(mock.Anything).
			Return(nil, errors.New(fooErrMsg))

		result, err := bridgeMocks.bridge.GetSponsoredClaimStatus(common.Big1)
		require.Nil(t, result)
		require.NotNil(t, err)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err,
			fmt.Sprintf("failed to get claim status for global index %d, error: %s", common.Big1, fooErrMsg))
	})

	t.Run("Claim status retrieval successful", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.sponsor.EXPECT().GetClaim(mock.Anything).
			Return(&claimsponsor.Claim{
				GlobalIndex: common.Big2,
				Status:      claimsponsor.PendingClaimStatus,
			}, nil)

		result, err := bridgeMocks.bridge.GetSponsoredClaimStatus(common.Big1)
		require.Nil(t, err)
		require.Equal(t, result, claimsponsor.PendingClaimStatus)
	})
}

func TestSponsorClaim(t *testing.T) {
	t.Run("Client does not support sponsored claims", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.sponsor = nil

		result, err := bridgeMocks.bridge.SponsorClaim(claimsponsor.Claim{})
		require.Nil(t, result)
		require.NotNil(t, err)
		require.Equal(t, rpc.InvalidRequestErrorCode, err.ErrorCode())
		require.ErrorContains(t, err, "this client does not support claim sponsoring")
	})

	t.Run("Unsupported network id", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)

		result, err := bridgeMocks.bridge.SponsorClaim(claimsponsor.Claim{DestinationNetwork: 999})
		require.Nil(t, result)
		require.NotNil(t, err)
		require.Equal(t, rpc.InvalidRequestErrorCode, err.ErrorCode())
		require.ErrorContains(t, err,
			fmt.Sprintf("this client only sponsors claims for destination network %d", l2NetworkID))
	})

	t.Run("Failed to add claim to the queue", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.sponsor.EXPECT().
			AddClaimToQueue(mock.Anything).
			Return(errors.New(fooErrMsg))

		result, err := bridgeMocks.bridge.SponsorClaim(claimsponsor.Claim{DestinationNetwork: l2NetworkID})
		require.Nil(t, result)
		require.NotNil(t, err)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err,
			fmt.Sprintf("error adding claim to the queue %s", fooErrMsg))
	})

	t.Run("Claim is added to the queue", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.sponsor.EXPECT().
			AddClaimToQueue(mock.Anything).
			Return(nil)

		result, err := bridgeMocks.bridge.SponsorClaim(claimsponsor.Claim{DestinationNetwork: l2NetworkID})
		require.Nil(t, result)
		require.Nil(t, err)
	})
}

func TestClaimProof(t *testing.T) {
	l1InfoTreeIndex := uint32(1)
	depositCount := uint32(1)
	l1InfoTreeLeaf := &l1infotreesync.L1InfoTreeLeaf{
		MainnetExitRoot: common.HexToHash("0x1"),
		RollupExitRoot:  common.HexToHash("0x2"),
	}

	t.Run("Failed to get L1 info tree leaf", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, mock.Anything).
			Return(nil, errors.New(fooErrMsg))

		result, err := bridgeMocks.bridge.ClaimProof(mainnetNetworkID, depositCount, l1InfoTreeIndex)
		require.Nil(t, result)
		require.NotNil(t, err)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err,
			fmt.Sprintf("failed to get l1 info tree leaf for index %d: %s", l1InfoTreeIndex, fooErrMsg))
	})

	t.Run("Unsupported network id", func(t *testing.T) {
		unsupportedNetworkID := uint32(999)

		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, mock.Anything).
			Return(&l1infotreesync.L1InfoTreeLeaf{}, nil)

		result, err := bridgeMocks.bridge.ClaimProof(unsupportedNetworkID, depositCount, l1InfoTreeIndex)
		require.Nil(t, result)
		require.NotNil(t, err)
		require.Equal(t, rpc.InvalidRequestErrorCode, err.ErrorCode())
		require.ErrorContains(t, err,
			fmt.Sprintf("this client does not support network %d", unsupportedNetworkID))
	})

	t.Run("Failed to get local exit proof for L1 network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, mock.Anything).
			Return(l1InfoTreeLeaf, nil)

		bridgeMocks.bridgeL1.EXPECT().
			GetProof(mock.Anything, mock.Anything, mock.Anything).
			Return(tree.Proof{}, errors.New(fooErrMsg))

		result, err := bridgeMocks.bridge.ClaimProof(mainnetNetworkID, depositCount, l1InfoTreeIndex)
		require.Nil(t, result)
		require.NotNil(t, err)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err,
			fmt.Sprintf("failed to get local exit proof, error: %s", fooErrMsg))
	})

	t.Run("Failed to get local exit root for L2 network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, mock.Anything).
			Return(l1InfoTreeLeaf, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetLocalExitRoot(mock.Anything, mock.Anything, mock.Anything).
			Return(common.Hash{}, errors.New(fooErrMsg))

		result, err := bridgeMocks.bridge.ClaimProof(l2NetworkID, depositCount, l1InfoTreeIndex)
		require.Nil(t, result)
		require.NotNil(t, err)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err,
			fmt.Sprintf("failed to get local exit root from rollup exit tree, error: %s", fooErrMsg))
	})

	t.Run("Failed to get local exit proof for L2 network", func(t *testing.T) {
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

		result, err := bridgeMocks.bridge.ClaimProof(l2NetworkID, depositCount, l1InfoTreeIndex)
		require.Nil(t, result)
		require.NotNil(t, err)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err,
			fmt.Sprintf("failed to get local exit proof, error: %s", fooErrMsg))
	})

	t.Run("Failed to get rollup exit proof for L1 network", func(t *testing.T) {
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

		result, err := bridgeMocks.bridge.ClaimProof(mainnetNetworkID, depositCount, l1InfoTreeIndex)
		require.Nil(t, result)
		require.NotNil(t, err)
		require.Equal(t, rpc.DefaultErrorCode, err.ErrorCode())
		require.ErrorContains(t, err,
			fmt.Sprintf("failed to get rollup exit proof, error: %s", fooErrMsg))
	})

	t.Run("Retrieve claim proof for L1 network", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, mock.Anything).
			Return(l1InfoTreeLeaf, nil)

		localExitTreeProof := tree.Proof{
			common.HexToHash("0xf"),
			common.HexToHash("0xd"),
			common.HexToHash("0xc"),
			common.HexToHash("0xb"),
		}

		rollupExitTreeProof := tree.Proof{
			common.HexToHash("0x1"),
			common.HexToHash("0x2"),
			common.HexToHash("0x3"),
			common.HexToHash("0x4"),
		}

		expectedClaimProof := types.ClaimProof{
			ProofLocalExitRoot:  localExitTreeProof,
			ProofRollupExitRoot: rollupExitTreeProof,
			L1InfoTreeLeaf:      *l1InfoTreeLeaf,
		}

		bridgeMocks.bridgeL1.EXPECT().
			GetProof(mock.Anything, mock.Anything, mock.Anything).
			Return(localExitTreeProof, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetRollupExitTreeMerkleProof(mock.Anything, mock.Anything, mock.Anything).
			Return(rollupExitTreeProof, nil)

		result, err := bridgeMocks.bridge.ClaimProof(mainnetNetworkID, depositCount, l1InfoTreeIndex)
		require.Nil(t, err)
		require.NotNil(t, result)
		require.Equal(t, expectedClaimProof, result)
	})
}

// performRequest is a helper function to perform HTTP requests in tests.
func performRequest(t *testing.T, router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reqBody io.Reader
	if body != nil {
		jsonBytes, err := json.Marshal(body)
		require.NoError(t, err)
		reqBody = bytes.NewBuffer(jsonBytes)
	}

	req := httptest.NewRequest(method, path, reqBody)
	// req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	return w
}
