package api

import (
	"bytes"
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

	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	autoclaimstorage "github.com/agglayer/aggkit/autoclaim/storage"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	logger "github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var testNow = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

func TestNewAPIWithNilStorageWhenEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	_, err := New(Config{Enabled: true}, nil, nil)
	require.ErrorContains(t, err, "storage is nil")
}

func TestAPIWithLoggerOption(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var log aggkitcommon.Logger
	api, err := New(Config{Enabled: false}, nil, nil, WithLogger(log))
	require.NoError(t, err)
	require.Nil(t, api.log)
}

func TestAPIStartWhenDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	api, err := New(Config{Enabled: false}, nil, nil)
	require.NoError(t, err)

	err = api.Start(context.Background())
	require.NoError(t, err)
}

func TestAPIDefaultClock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	storage := newTestStorage(t)
	api, err := New(Config{Enabled: true}, storage, nil)
	require.NoError(t, err)
	require.False(t, api.now().IsZero())
}

func TestConfigFromRESTConfig(t *testing.T) {
	rest := aggkitcommon.RESTConfig{
		Host:         "0.0.0.0",
		Port:         8080,
		ReadTimeout:  cfgtypes.Duration{Duration: 15 * time.Second},
		WriteTimeout: cfgtypes.Duration{Duration: 30 * time.Second},
	}

	cfg := ConfigFromRESTConfig(true, rest)

	require.True(t, cfg.Enabled)
	require.Equal(t, "0.0.0.0:8080", cfg.Address)
	require.Equal(t, 15*time.Second, cfg.ReadTimeout)
	require.Equal(t, 30*time.Second, cfg.WriteTimeout)
}

func TestDisabledAPIDoesNotExposeRoutesOrRequireDependencies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	api, err := New(Config{Enabled: false}, nil, nil)
	require.NoError(t, err)

	response := performRequest(t, api, http.MethodGet, Prefix+"/bridges", nil)
	require.Equal(t, http.StatusNotFound, response.Code)
}

func TestListBridgesFilters(t *testing.T) {
	ctx := context.Background()
	storage := newTestStorage(t)
	api := newTestAPI(t, storage, nil)

	requests := []autoclaimtypes.AutoClaimRequest{
		makeRequest(1, 10, autoclaimtypes.RequestStatusDetected),
		makeRequest(2, 10, autoclaimtypes.RequestStatusDetected),
		makeRequest(3, 11, autoclaimtypes.RequestStatusDetected),
	}
	for _, request := range requests {
		enqueueRequest(t, ctx, storage, request)
	}

	decision := autoclaimtypes.PolicyDecision{
		PolicyName: "allow-all",
		Result:     autoclaimtypes.PolicyResultApproved,
		Reason:     "allowed",
		CreatedAt:  testNow,
		UpdatedAt:  testNow,
	}
	require.NoError(t, storage.RecordPolicyDecision(ctx, requests[1].Key, decision))

	attempt := autoclaimtypes.TransactionAttempt{
		RequestKey:       requests[1].Key,
		ClaimerID:        "claimer-10",
		AttemptNumber:    1,
		TxManagerID:      common.HexToHash("0x1001"),
		ClaimTxHash:      common.HexToHash("0x2002"),
		Status:           ethtxtypes.MonitoredTxStatusSent,
		RetryCount:       1,
		MaxRetries:       4,
		CreatedAt:        testNow,
		UpdatedAt:        testNow,
		TargetBridgeAddr: common.HexToAddress("0x5000000000000000000000000000000000000005"),
	}
	require.NoError(t, storage.RecordTransactionAttempt(ctx, requests[1].Key, attempt))

	query := url.Values{}
	query.Set("origin_network", "0")
	query.Set("destination_network", "10")
	query.Set("status", autoclaimtypes.RequestStatusDetected.String())
	query.Set("policy_status", autoclaimtypes.PolicyResultApproved.String())
	query.Set("bridge_tx_hash", requests[1].Bridge.TxHash.Hex())
	query.Set("claim_tx_hash", attempt.ClaimTxHash.Hex())
	query.Set("from_block", "101")
	query.Set("to_block", "103")

	response := performRequest(t, api, http.MethodGet, Prefix+"/bridges?"+query.Encode(), nil)
	require.Equal(t, http.StatusOK, response.Code)

	var result ListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, 1, result.Count)
	require.Len(t, result.Bridges, 1)
	require.Equal(t, string(requests[1].Key), result.Bridges[0].ID)
	require.Equal(t, autoclaimtypes.PolicyResultApproved.String(), result.Bridges[0].PolicyStatus)
	require.NotNil(t, result.Bridges[0].ClaimTxHash)
	require.Equal(t, attempt.ClaimTxHash.Hex(), *result.Bridges[0].ClaimTxHash)
}

func TestGetMissingBridge(t *testing.T) {
	storage := newTestStorage(t)
	api := newTestAPI(t, storage, nil)

	response := performRequest(t, api, http.MethodGet, Prefix+"/bridges/0:10:404", nil)
	require.Equal(t, http.StatusNotFound, response.Code)
}

func TestApproveManualRequest(t *testing.T) {
	ctx := context.Background()
	storage := newTestStorage(t)
	registry := newFakeRegistry()
	api := newTestAPI(t, storage, registry)
	request := makeManualRequest(1, 10)
	enqueueRequest(t, ctx, storage, request)

	body := map[string]any{
		"reason":     "operator approved",
		"decider":    "operator",
		"decider_id": "alice",
		"metadata":   map[string]string{"ticket": "ABC-1"},
	}
	response := performRequest(t, api, http.MethodPost, Prefix+"/bridges/"+string(request.Key)+"/approve", body)
	require.Equal(t, http.StatusOK, response.Code)

	var result RequestResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, autoclaimtypes.RequestStatusPolicyApproved.String(), result.Status)
	require.NotNil(t, result.ManualDecision)
	require.Equal(t, autoclaimtypes.PolicyResultApproved.String(), result.ManualDecision.Result)
	require.Equal(t, "operator approved", result.ManualDecision.Reason)
	require.Equal(t, []autoclaimtypes.RequestKey{request.Key}, registry.claimer.advanced)

	stored, err := storage.GetRequest(ctx, request.Key)
	require.NoError(t, err)
	require.Equal(t, autoclaimtypes.RequestStatusPolicyApproved, stored.Status)
	require.Equal(t, autoclaimtypes.PolicyResultManual, stored.PolicyDecision.Result)
	require.Equal(t, autoclaimtypes.PolicyResultApproved, stored.ManualDecision.Result)
}

func TestRejectManualRequest(t *testing.T) {
	ctx := context.Background()
	storage := newTestStorage(t)
	registry := newFakeRegistry()
	api := newTestAPI(t, storage, registry)
	request := makeManualRequest(2, 10)
	enqueueRequest(t, ctx, storage, request)

	response := performRequest(t, api, http.MethodPost, Prefix+"/bridges/"+string(request.Key)+"/reject", nil)
	require.Equal(t, http.StatusOK, response.Code)

	var result RequestResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, autoclaimtypes.RequestStatusPolicyRejected.String(), result.Status)
	require.NotNil(t, result.ManualDecision)
	require.Equal(t, autoclaimtypes.PolicyResultRejected.String(), result.ManualDecision.Result)
	require.Equal(t, []autoclaimtypes.RequestKey{request.Key}, registry.claimer.advanced)
}

func TestInvalidManualTransition(t *testing.T) {
	ctx := context.Background()
	storage := newTestStorage(t)
	api := newTestAPI(t, storage, nil)
	request := makeRequest(3, 10, autoclaimtypes.RequestStatusDetected)
	enqueueRequest(t, ctx, storage, request)

	response := performRequest(t, api, http.MethodPost, Prefix+"/bridges/"+string(request.Key)+"/approve", nil)
	require.Equal(t, http.StatusConflict, response.Code)
}

func TestListPagination(t *testing.T) {
	ctx := context.Background()
	storage := newTestStorage(t)
	api := newTestAPI(t, storage, nil)
	for i := uint32(1); i <= 3; i++ {
		enqueueRequest(t, ctx, storage, makeRequest(i, 10, autoclaimtypes.RequestStatusDetected))
	}

	response := performRequest(t, api, http.MethodGet, Prefix+"/bridges?page_size=2", nil)
	require.Equal(t, http.StatusOK, response.Code)

	var first ListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &first))
	require.Equal(t, 3, first.Count)
	require.Equal(t, uint32(0), first.PageNumber)
	require.Equal(t, uint32(2), first.PageSize)
	require.Len(t, first.Bridges, 2)
	require.Equal(t, "0:10:3", first.Bridges[0].ID)
	require.Equal(t, "0:10:2", first.Bridges[1].ID)

	response = performRequest(t, api, http.MethodGet, Prefix+"/bridges?page_size=2&page_number=1", nil)
	require.Equal(t, http.StatusOK, response.Code)

	var second ListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &second))
	require.Equal(t, 3, second.Count)
	require.Equal(t, uint32(1), second.PageNumber)
	require.Len(t, second.Bridges, 1)
	require.Equal(t, "0:10:1", second.Bridges[0].ID)
}

func TestListRejectsOversizedPageSize(t *testing.T) {
	api := newTestAPI(t, newTestStorage(t), nil)
	path := fmt.Sprintf("%s/bridges?page_size=%d", Prefix, autoclaimtypes.MaxRequestPageSize+1)

	response := performRequest(t, api, http.MethodGet, path, nil)

	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "page_size")
}

func TestResponseJSONFields(t *testing.T) {
	ctx := context.Background()
	storage := newTestStorage(t)
	api := newTestAPI(t, storage, nil)
	request := makeManualRequest(4, 10)
	request.LastError = "last problem"
	enqueueRequest(t, ctx, storage, request)

	response := performRequest(t, api, http.MethodGet, Prefix+"/bridges/"+string(request.Key), nil)
	require.Equal(t, http.StatusOK, response.Code)

	var result RequestResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, string(request.Key), result.ID)
	require.Equal(t, autoclaimtypes.RequestStatusManualApprovalRequired.String(), result.Status)
	require.Equal(t, uint32(0), result.OriginNetwork)
	require.Equal(t, uint32(10), result.DestinationNetwork)
	require.Equal(t, uint32(4), result.DepositCount)
	require.NotEmpty(t, result.GlobalIndex)
	require.Equal(t, request.Bridge.TxHash.Hex(), result.BridgeTxHash)
	require.Equal(t, request.Bridge.OriginAddress.Hex(), result.OriginAddress)
	require.Equal(t, request.Bridge.DestinationAddress.Hex(), result.DestinationAddress)
	require.Equal(t, request.Bridge.ToAddress.Hex(), result.ToAddress)
	require.Equal(t, request.Bridge.TxnSender.Hex(), result.TxnSender)
	require.Equal(t, request.Bridge.Amount.String(), result.Amount)
	require.Equal(t, "0x04", result.Metadata)
	require.Equal(t, request.Bridge.BlockNum, result.BlockNum)
	require.Equal(t, request.RetryCount, result.RetryCount)
	require.Equal(t, request.MaxRetries, result.MaxRetries)
	require.NotNil(t, result.PolicyDecision)
	require.Equal(t, autoclaimtypes.PolicyResultManual.String(), result.PolicyDecision.Result)
	require.Equal(t, "last problem", result.LastError)
}

func TestSwaggerRoutes(t *testing.T) {
	storage := newTestStorage(t)
	api := newTestAPI(t, storage, nil)

	response := performRequest(t, api, http.MethodGet, Prefix+"/swagger", nil)
	require.Equal(t, http.StatusFound, response.Code)
	require.Equal(t, Prefix+"/swagger/index.html", response.Header().Get("Location"))

	response = performRequest(t, api, http.MethodGet, Prefix+"/swagger/doc.json", nil)
	require.Equal(t, http.StatusOK, response.Code)

	var doc struct {
		BasePath string                    `json:"basePath"`
		Paths    map[string]map[string]any `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &doc))
	require.Equal(t, Prefix, doc.BasePath)
	require.Contains(t, doc.Paths, "/bridges")
	require.Contains(t, doc.Paths, "/bridges/{id}")
	require.Contains(t, doc.Paths, "/bridges/{id}/approve")
	require.Contains(t, doc.Paths, "/bridges/{id}/reject")
}

func TestClaimingPathIsIndependentWhenAPIDisabled(t *testing.T) {
	api, err := New(Config{Enabled: false}, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, api.Router())

	claimer := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-10", DestinationNetwork: 10}}
	require.NoError(t, claimer.Advance(context.Background(), autoclaimtypes.RequestKey("0:10:1")))
	require.Equal(t, []autoclaimtypes.RequestKey{"0:10:1"}, claimer.advanced)
}

func newTestStorage(t *testing.T) *autoclaimstorage.Storage {
	t.Helper()

	storage, err := autoclaimstorage.NewStandalone(
		logger.GetDefaultLogger(),
		filepath.Join(t.TempDir(), "autoclaim.sqlite"),
		30*time.Second,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, storage.Close())
	})
	return storage
}

func newTestAPI(
	t *testing.T,
	storage *autoclaimstorage.Storage,
	registry autoclaimtypes.ClaimerRegistry,
) *API {
	t.Helper()

	gin.SetMode(gin.TestMode)
	api, err := New(
		Config{Enabled: true},
		storage,
		registry,
		WithNow(func() time.Time { return testNow }),
	)
	require.NoError(t, err)
	return api
}

func makeRequest(
	depositCount uint32,
	destinationNetwork uint32,
	status autoclaimtypes.RequestStatus,
) autoclaimtypes.AutoClaimRequest {
	now := testNow.Add(time.Duration(depositCount) * time.Second)
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
		Status:      status,
		Bridge:      bridge,
		GlobalIndex: new(big.Int).Set(bridge.GlobalIndex),
		RetryCount:  1,
		MaxRetries:  4,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func makeManualRequest(depositCount uint32, destinationNetwork uint32) autoclaimtypes.AutoClaimRequest {
	request := makeRequest(depositCount, destinationNetwork, autoclaimtypes.RequestStatusManualApprovalRequired)
	request.PolicyDecision = &autoclaimtypes.PolicyDecision{
		PolicyName: "api-approve",
		Result:     autoclaimtypes.PolicyResultManual,
		Reason:     "needs approval",
		CreatedAt:  testNow,
		UpdatedAt:  testNow,
	}
	return request
}

func enqueueRequest(
	t *testing.T,
	ctx context.Context,
	storage *autoclaimstorage.Storage,
	request autoclaimtypes.AutoClaimRequest,
) {
	t.Helper()

	_, inserted, err := storage.EnqueueRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, inserted)
}

func performRequest(t *testing.T, api *API, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var requestBody *bytes.Reader
	if body == nil {
		requestBody = bytes.NewReader(nil)
	} else {
		jsonBody, err := json.Marshal(body)
		require.NoError(t, err)
		requestBody = bytes.NewReader(jsonBody)
	}

	request := httptest.NewRequest(method, path, requestBody)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Router().ServeHTTP(response, request)
	return response
}

type fakeRegistry struct {
	claimer *fakeClaimer
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{
		claimer: &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-10", DestinationNetwork: 10}},
	}
}

func (r *fakeRegistry) ClaimerForDestination(
	_ context.Context,
	destinationNetwork uint32,
) (autoclaimtypes.Claimer, bool, error) {
	if r.claimer.target.DestinationNetwork != destinationNetwork {
		return nil, false, nil
	}
	return r.claimer, true, nil
}

func (r *fakeRegistry) Claimers(context.Context) ([]autoclaimtypes.Claimer, error) {
	return []autoclaimtypes.Claimer{r.claimer}, nil
}

type fakeClaimer struct {
	target   autoclaimtypes.ClaimerTarget
	advanced []autoclaimtypes.RequestKey
}

func (c *fakeClaimer) Target() autoclaimtypes.ClaimerTarget {
	return c.target
}

func (c *fakeClaimer) IsClaimed(context.Context, autoclaimtypes.BridgeExit) (bool, error) {
	return false, nil
}

func (c *fakeClaimer) Enqueue(context.Context, autoclaimtypes.BridgeExit) error {
	return fmt.Errorf("not implemented")
}

func (c *fakeClaimer) Advance(_ context.Context, key autoclaimtypes.RequestKey) error {
	c.advanced = append(c.advanced, key)
	return nil
}
