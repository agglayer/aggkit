package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	apitypes "github.com/agglayer/aggkit/autoclaim/apitypes"
	autoclaimstorage "github.com/agglayer/aggkit/autoclaim/storage"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
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

func TestAPIDefaultClock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	storage := newTestStorage(t)
	api, err := New(Config{Enabled: true}, storage, nil)
	require.NoError(t, err)
	require.False(t, api.now().IsZero())
}

func TestConfigFromRESTConfig(t *testing.T) {
	rest := aggkitcommon.RESTConfig{}

	cfg := ConfigFromRESTConfig(true, rest)

	require.True(t, cfg.Enabled)
}

func TestDisabledAPIDoesNotExposeRoutesOrRequireDependencies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	api, err := New(Config{Enabled: false}, nil, nil)
	require.NoError(t, err)

	response := performRequest(t, api, http.MethodPost, Prefix+"/bridges/0:10:1/approve", nil)
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

	var result apitypes.RequestResponse
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

	var result apitypes.RequestResponse
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

func TestManualDecisionRejectsOversizedFields(t *testing.T) {
	ctx := context.Background()
	storage := newTestStorage(t)
	api := newTestAPI(t, storage, nil)
	request := makeManualRequest(7, 10)
	enqueueRequest(t, ctx, storage, request)

	body := map[string]any{
		"decider": string(make([]byte, 300)),
	}
	response := performRequest(t, api, http.MethodPost, Prefix+"/bridges/"+string(request.Key)+"/approve", body)
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "decider exceeds maximum length")
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
	require.Contains(t, doc.Paths, "/bridges/{id}/approve")
	require.Contains(t, doc.Paths, "/bridges/{id}/reject")
	require.NotContains(t, doc.Paths, "/bridges")
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
		Key:         autoclaimtypes.DeriveRequestKey(bridge.SourceNetwork, bridge.DestinationNetwork, depositCount),
		Status:      status,
		Bridge:      bridge,
		GlobalIndex: new(big.Int).Set(bridge.GlobalIndex),
		RetryCount:  1,
		MaxRetries:  4,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func makeManualRequest(depositCount, destinationNetwork uint32) autoclaimtypes.AutoClaimRequest {
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

// fakeRegistryWithError always returns an error from ClaimerForDestination.
type fakeRegistryWithError struct{ err error }

func (r *fakeRegistryWithError) ClaimerForDestination(
	_ context.Context, _ uint32,
) (autoclaimtypes.Claimer, bool, error) {
	return nil, false, r.err
}

func (r *fakeRegistryWithError) Claimers(context.Context) ([]autoclaimtypes.Claimer, error) {
	return nil, r.err
}

// errClaimer returns an error from Advance.
type errClaimer struct {
	target autoclaimtypes.ClaimerTarget
	err    error
}

func (c *errClaimer) Target() autoclaimtypes.ClaimerTarget { return c.target }
func (c *errClaimer) IsClaimed(context.Context, autoclaimtypes.BridgeExit) (bool, error) {
	return false, nil
}
func (c *errClaimer) Enqueue(context.Context, autoclaimtypes.BridgeExit) error { return nil }
func (c *errClaimer) Advance(_ context.Context, _ autoclaimtypes.RequestKey) error {
	return c.err
}

// fakeRegistryWithAdvanceError wraps an errClaimer that returns an error from Advance.
type fakeRegistryWithAdvanceError struct{ claimer *errClaimer }

func (r *fakeRegistryWithAdvanceError) ClaimerForDestination(
	_ context.Context, destinationNetwork uint32,
) (autoclaimtypes.Claimer, bool, error) {
	if r.claimer.target.DestinationNetwork == destinationNetwork {
		return r.claimer, true, nil
	}
	return nil, false, nil
}

func (r *fakeRegistryWithAdvanceError) Claimers(context.Context) ([]autoclaimtypes.Claimer, error) {
	return []autoclaimtypes.Claimer{r.claimer}, nil
}

func performRawRequest(t *testing.T, api *API, method, path string, rawBody []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Router().ServeHTTP(response, req)
	return response
}

func TestManualDecisionRequestNotFound(t *testing.T) {
	storage := newTestStorage(t)
	api := newTestAPI(t, storage, nil)

	response := performRequest(t, api, http.MethodPost, Prefix+"/bridges/0:99:999/approve", nil)
	require.Equal(t, http.StatusNotFound, response.Code)
}

func TestManualDecisionInvalidJSONBody(t *testing.T) {
	ctx := context.Background()
	storage := newTestStorage(t)
	api := newTestAPI(t, storage, nil)
	request := makeManualRequest(20, 10)
	enqueueRequest(t, ctx, storage, request)

	response := performRawRequest(t, api, http.MethodPost,
		Prefix+"/bridges/"+string(request.Key)+"/approve", []byte("not-json"))
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "decode manual decision request")
}

func TestManualDecisionDeciderIDOversize(t *testing.T) {
	ctx := context.Background()
	storage := newTestStorage(t)
	api := newTestAPI(t, storage, nil)
	request := makeManualRequest(21, 10)
	enqueueRequest(t, ctx, storage, request)

	body := map[string]any{
		"decider_id": string(make([]byte, maxDeciderIDLength+1)),
	}
	response := performRequest(t, api, http.MethodPost, Prefix+"/bridges/"+string(request.Key)+"/approve", body)
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "decider_id exceeds maximum length")
}

func TestManualDecisionReasonOversize(t *testing.T) {
	ctx := context.Background()
	storage := newTestStorage(t)
	api := newTestAPI(t, storage, nil)
	request := makeManualRequest(22, 10)
	enqueueRequest(t, ctx, storage, request)

	body := map[string]any{
		"reason": string(make([]byte, maxReasonLength+1)),
	}
	response := performRequest(t, api, http.MethodPost, Prefix+"/bridges/"+string(request.Key)+"/approve", body)
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "reason exceeds maximum length")
}

func TestManualDecisionNilRegistryApproves(t *testing.T) {
	ctx := context.Background()
	storage := newTestStorage(t)
	api := newTestAPI(t, storage, nil)
	request := makeManualRequest(23, 99)
	enqueueRequest(t, ctx, storage, request)

	response := performRequest(t, api, http.MethodPost, Prefix+"/bridges/"+string(request.Key)+"/approve", nil)
	require.Equal(t, http.StatusOK, response.Code)
}

func TestManualDecisionNotifyClaimerLookupError(t *testing.T) {
	ctx := context.Background()
	storage := newTestStorage(t)
	registry := &fakeRegistryWithError{err: errors.New("registry rpc failed")}
	api := newTestAPI(t, storage, registry)
	request := makeManualRequest(24, 10)
	enqueueRequest(t, ctx, storage, request)

	response := performRequest(t, api, http.MethodPost, Prefix+"/bridges/"+string(request.Key)+"/approve", nil)
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Contains(t, response.Body.String(), "registry rpc failed")
}

func TestManualDecisionNotifyClaimerAdvanceError(t *testing.T) {
	ctx := context.Background()
	storage := newTestStorage(t)
	registry := &fakeRegistryWithAdvanceError{
		claimer: &errClaimer{
			target: autoclaimtypes.ClaimerTarget{DestinationNetwork: 10},
			err:    errors.New("advance failed"),
		},
	}
	api := newTestAPI(t, storage, registry)
	request := makeManualRequest(25, 10)
	enqueueRequest(t, ctx, storage, request)

	response := performRequest(t, api, http.MethodPost, Prefix+"/bridges/"+string(request.Key)+"/approve", nil)
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Contains(t, response.Body.String(), "advance failed")
}
