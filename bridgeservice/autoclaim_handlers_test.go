package bridgeservice

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	apitypes "github.com/agglayer/aggkit/autoclaim/apitypes"
	autoclaimstorage "github.com/agglayer/aggkit/autoclaim/storage"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	logger "github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var autoClaimTestNow = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

func newAutoClaimTestService(t *testing.T, storage AutoClaimQuerier) *BridgeService {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	b := &BridgeService{
		readTimeout:      30 * time.Second,
		autoClaimQuerier: storage,
		router:           router,
	}
	group := router.Group(BridgeV1Prefix)
	group.GET("/autoclaim/bridges", b.GetAutoClaimBridgesHandler)
	group.GET("/autoclaim/bridges/:id", b.GetAutoClaimBridgeHandler)
	return b
}

func newAutoClaimTestStorage(t *testing.T) *autoclaimstorage.Storage {
	t.Helper()
	storage, err := autoclaimstorage.NewStandalone(
		logger.GetDefaultLogger(),
		filepath.Join(t.TempDir(), "autoclaim.sqlite"),
		30*time.Second,
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, storage.Close()) })
	return storage
}

func makeAutoClaimRequest(depositCount, destinationNetwork uint32) autoclaimtypes.AutoClaimRequest {
	now := autoClaimTestNow.Add(time.Duration(depositCount) * time.Second)
	bridge := autoclaimtypes.BridgeExit{
		BlockNum:           100 + uint64(depositCount),
		BlockPos:           uint64(depositCount),
		TxHash:             common.BigToHash(big.NewInt(int64(depositCount))),
		BlockTimestamp:     1000 + uint64(depositCount),
		LeafType:           bridgesynctypes.LeafTypeAsset,
		OriginNetwork:      autoclaimtypes.L1OriginNetwork,
		OriginAddress:      common.HexToAddress("0x1000000000000000000000000000000000000001"),
		DestinationNetwork: destinationNetwork,
		DestinationAddress: common.HexToAddress("0x2000000000000000000000000000000000000002"),
		Amount:             big.NewInt(1000 + int64(depositCount)),
		Metadata:           []byte{byte(depositCount)},
		DepositCount:       depositCount,
		TxnSender:          common.HexToAddress("0x3000000000000000000000000000000000000003"),
		ToAddress:          common.HexToAddress("0x4000000000000000000000000000000000000004"),
		GlobalIndex:        autoclaimtypes.DeriveGlobalIndex(autoclaimtypes.L1OriginNetwork, depositCount),
	}
	return autoclaimtypes.AutoClaimRequest{
		Key:         autoclaimtypes.DeriveRequestKey(bridge.OriginNetwork, bridge.DestinationNetwork, depositCount),
		Status:      autoclaimtypes.RequestStatusDetected,
		Bridge:      bridge,
		GlobalIndex: new(big.Int).Set(bridge.GlobalIndex),
		RetryCount:  1,
		MaxRetries:  4,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func enqueueAutoClaimRequest(t *testing.T, storage *autoclaimstorage.Storage, request autoclaimtypes.AutoClaimRequest) {
	t.Helper()
	_, inserted, err := storage.EnqueueRequest(context.Background(), request)
	require.NoError(t, err)
	require.True(t, inserted)
}

func performAutoClaimRequest(t *testing.T, b *BridgeService, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	b.router.ServeHTTP(response, request)
	return response
}

func TestGetAutoClaimBridgesPaginationAndFilter(t *testing.T) {
	storage := newAutoClaimTestStorage(t)
	b := newAutoClaimTestService(t, storage)
	for i := uint32(1); i <= 3; i++ {
		enqueueAutoClaimRequest(t, storage, makeAutoClaimRequest(i, 10))
	}
	enqueueAutoClaimRequest(t, storage, makeAutoClaimRequest(1, 11))

	response := performAutoClaimRequest(t, b, BridgeV1Prefix+"/autoclaim/bridges?page_size=2")
	require.Equal(t, http.StatusOK, response.Code)
	var first apitypes.ListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &first))
	require.Equal(t, 4, first.Count)
	require.Equal(t, uint32(2), first.PageSize)
	require.Len(t, first.Bridges, 2)

	query := url.Values{}
	query.Set("destination_network", "11")
	response = performAutoClaimRequest(t, b, BridgeV1Prefix+"/autoclaim/bridges?"+query.Encode())
	require.Equal(t, http.StatusOK, response.Code)
	var filtered apitypes.ListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &filtered))
	require.Equal(t, 1, filtered.Count)
	require.Len(t, filtered.Bridges, 1)
	require.Equal(t, uint32(11), filtered.Bridges[0].DestinationNetwork)
}

func TestGetAutoClaimBridgesRejectsOversizedPageSize(t *testing.T) {
	b := newAutoClaimTestService(t, newAutoClaimTestStorage(t))
	path := fmt.Sprintf("%s/autoclaim/bridges?page_size=%d", BridgeV1Prefix, autoclaimtypes.MaxRequestPageSize+1)
	response := performAutoClaimRequest(t, b, path)
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "page_size")
}

func TestGetAutoClaimBridgeByID(t *testing.T) {
	storage := newAutoClaimTestStorage(t)
	b := newAutoClaimTestService(t, storage)
	request := makeAutoClaimRequest(4, 10)
	enqueueAutoClaimRequest(t, storage, request)

	response := performAutoClaimRequest(t, b, BridgeV1Prefix+"/autoclaim/bridges/"+string(request.Key))
	require.Equal(t, http.StatusOK, response.Code)
	var result apitypes.RequestResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, string(request.Key), result.ID)
	require.Equal(t, uint32(10), result.DestinationNetwork)
	require.Equal(t, uint32(4), result.DepositCount)
	require.NotEmpty(t, result.GlobalIndex)
}

func TestGetAutoClaimBridgeNotFound(t *testing.T) {
	b := newAutoClaimTestService(t, newAutoClaimTestStorage(t))
	response := performAutoClaimRequest(t, b, BridgeV1Prefix+"/autoclaim/bridges/0:10:404")
	require.Equal(t, http.StatusNotFound, response.Code)
}

func TestAutoClaimRoutesNotRegisteredWithoutQuerier(t *testing.T) {
	cfg := &Config{
		Logger:    logger.GetDefaultLogger(),
		Address:   "127.0.0.1:0",
		NetworkID: 1,
	}
	b := New(cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	response := performAutoClaimRequest(t, b, BridgeV1Prefix+"/autoclaim/bridges")
	require.Equal(t, http.StatusNotFound, response.Code)
}
