package api

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

var publicTestNow = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

func newPublicTestStorage(t *testing.T) *autoclaimstorage.Storage {
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

func newPublicTestRouter(t *testing.T, storage Querier) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := NewPublicAPI(storage, 30*time.Second)
	api.RegisterRoutes(router)
	return router
}

func makePublicTestRequest(depositCount, destinationNetwork uint32) autoclaimtypes.AutoClaimRequest {
	now := publicTestNow.Add(time.Duration(depositCount) * time.Second)
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

func enqueuePublicTestRequest(t *testing.T, storage *autoclaimstorage.Storage, request autoclaimtypes.AutoClaimRequest) {
	t.Helper()
	_, inserted, err := storage.EnqueueRequest(context.Background(), request)
	require.NoError(t, err)
	require.True(t, inserted)
}

func doPublicRequest(t *testing.T, router *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

const autoclaimpublicV1 = "/autoclaim/v1"

func TestPublicAPIListBridgesPaginationAndFilter(t *testing.T) {
	storage := newPublicTestStorage(t)
	router := newPublicTestRouter(t, storage)
	for i := uint32(1); i <= 3; i++ {
		enqueuePublicTestRequest(t, storage, makePublicTestRequest(i, 10))
	}
	enqueuePublicTestRequest(t, storage, makePublicTestRequest(1, 11))

	resp := doPublicRequest(t, router, autoclaimpublicV1+"/bridges?page_size=2")
	require.Equal(t, http.StatusOK, resp.Code)
	var first apitypes.ListResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &first))
	require.Equal(t, 4, first.Count)
	require.Equal(t, uint32(2), first.PageSize)
	require.Len(t, first.Bridges, 2)

	query := url.Values{}
	query.Set("destination_network", "11")
	resp = doPublicRequest(t, router, autoclaimpublicV1+"/bridges?"+query.Encode())
	require.Equal(t, http.StatusOK, resp.Code)
	var filtered apitypes.ListResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &filtered))
	require.Equal(t, 1, filtered.Count)
	require.Len(t, filtered.Bridges, 1)
	require.Equal(t, uint32(11), filtered.Bridges[0].DestinationNetwork)
}

func TestPublicAPIListBridgesRejectsOversizedPageSize(t *testing.T) {
	router := newPublicTestRouter(t, newPublicTestStorage(t))
	path := fmt.Sprintf("%s/bridges?page_size=%d", autoclaimpublicV1, autoclaimtypes.MaxRequestPageSize+1)
	resp := doPublicRequest(t, router, path)
	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.Contains(t, resp.Body.String(), "page_size")
}

func TestPublicAPIGetBridgeByID(t *testing.T) {
	storage := newPublicTestStorage(t)
	router := newPublicTestRouter(t, storage)
	request := makePublicTestRequest(4, 10)
	enqueuePublicTestRequest(t, storage, request)

	resp := doPublicRequest(t, router, autoclaimpublicV1+"/bridges/"+string(request.Key))
	require.Equal(t, http.StatusOK, resp.Code)
	var result apitypes.RequestResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &result))
	require.Equal(t, string(request.Key), result.ID)
	require.Equal(t, uint32(10), result.DestinationNetwork)
	require.Equal(t, uint32(4), result.DepositCount)
	require.NotEmpty(t, result.GlobalIndex)
}

func TestPublicAPIGetBridgeNotFound(t *testing.T) {
	router := newPublicTestRouter(t, newPublicTestStorage(t))
	resp := doPublicRequest(t, router, autoclaimpublicV1+"/bridges/0:10:404")
	require.Equal(t, http.StatusNotFound, resp.Code)
}
