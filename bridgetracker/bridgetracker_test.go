package bridgetracker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/api"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	testTxHash     = "0x1234567890123456789012345678901234567890123456789012345678901234"
	testConfigSHA1 = "2ef7bde608ce5404e97d5f042f95f89f1c232871"
)

func newTestTracker(t *testing.T) (*BridgeTracker, *gin.Engine) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	tracker := New(&Config{
		Logger:     log.WithFields("module", "bridgetracker_test"),
		ConfigSHA1: testConfigSHA1,
	})
	router := gin.New()
	tracker.API().RegisterRoutes(router)
	return tracker, router
}

func performRequest(t *testing.T, router *gin.Engine, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	req, err := http.NewRequest(method, path, nil)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

// testBridgeInfo returns a BridgeInfo snapshot for tests (BridgeType derives to L2ToL1 since
// DestinationNetwork is the zero value, Mainnet). The matching TrackingStatus (Running or
// Finished), step index (always 0: the single entry testAllSteps returns) and steps
// (testAllSteps) are the caller's responsibility to pass alongside it to Publish
func testBridgeInfo() *BridgeInfo {
	return &BridgeInfo{
		NetworkID: 1,
		LeafType:  types.BridgeLeafTypeAsset,
	}
}

// testAllSteps returns the expected-path snapshot matching testBridgeInfo, claimed or in progress
func testAllSteps(claimed bool) []BridgeStepPath {
	step := types.StepPendingInclusion
	stepStatus := types.StepStatusInProgress
	if claimed {
		step = types.StepClaimed
		stepStatus = types.StepStatusDone
	}
	return []BridgeStepPath{{Step: step, Status: stepStatus}}
}

// testAllStepsWithError returns an expected-path snapshot with its in-progress step failed,
// for tests exercising a step-level error (as opposed to a terminal resolution failure)
func testAllStepsWithError() []BridgeStepPath {
	return []BridgeStepPath{{
		Step: types.StepPendingInclusion, Status: types.StepStatusError, Error: testErrorStep(),
	}}
}

// testErrorStep returns the ErrorStep snapshot for tests exercising a bridge the tracker
// gave up resolving (e.g. tx not found / not a bridge transaction)
func testErrorStep() *types.ErrorStep {
	return &types.ErrorStep{
		ErrorType:   types.StepErrorExhausted,
		RetryCount:  3,
		Description: []string{"bridge tx not found"},
	}
}

func TestGetTxStatusHandlerInvalidTxHash(t *testing.T) {
	_, router := newTestTracker(t)

	invalidHashes := []string{
		"foo",
		"0x1234",                        // too short
		"0x" + strings.Repeat("z", 64),  // right length, not hex
		"00" + strings.Repeat("12", 32), // 66 chars but no 0x prefix
	}
	for _, hash := range invalidHashes {
		resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/network/1/tx/"+hash)
		require.Equal(t, http.StatusBadRequest, resp.Code, "hash: %q", hash)

		var errData types.ErrorData
		require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &errData))
		require.Equal(t, http.StatusBadRequest, errData.Code)
		require.Contains(t, errData.Message, "tx_hash")
	}
}

func TestGetTxStatusHandlerInvalidNetworkID(t *testing.T) {
	_, router := newTestTracker(t)

	txHash := testTxHash
	testCases := []struct {
		name      string
		networkID string
	}{
		{name: "non-numeric network_id", networkID: "foo"},
		{name: "negative network_id", networkID: "-1"},
		{name: "network_id overflows uint32", networkID: "4294967296"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := performRequest(t, router, http.MethodGet,
				api.TrackerV1Prefix+"/network/"+tc.networkID+"/tx/"+txHash)
			require.Equal(t, http.StatusBadRequest, resp.Code)

			var errData types.ErrorData
			require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &errData))
			require.Equal(t, http.StatusBadRequest, errData.Code)
			require.Contains(t, errData.Message, "network_id")
		})
	}
}

func TestGetTxStatusHandlerMissingNetworkID(t *testing.T) {
	_, router := newTestTracker(t)

	// without the network segment the route does not exist
	txHash := testTxHash
	resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/tx/"+txHash)
	require.Equal(t, http.StatusNotFound, resp.Code)
}

// TestGetTxStatusHandlerRegisters pins the supervised-list semantics: every call answers
// 200 OK with a TrackingData body; its bridge_status field is null until the tracking
// engine publishes a status, and carries the full BridgeStatus from then on
func TestGetTxStatusHandlerRegisters(t *testing.T) {
	tracker, router := newTestTracker(t)

	path := api.TrackerV1Prefix + "/network/1/tx/" + testTxHash

	// first call: registered, no info yet -> 200 + TrackingData with bridge_status: null
	resp := performRequest(t, router, http.MethodGet, path)
	require.Equal(t, http.StatusOK, resp.Code)

	var tracking struct {
		TrackingStatus string          `json:"tracking_status"`
		NetworkID      uint32          `json:"network_id"`
		TxHash         string          `json:"tx_hash"`
		BridgeStatus   json.RawMessage `json:"bridge_status"`
		StepIndex      int             `json:"step_index"`
		AllSteps       json.RawMessage `json:"all_steps"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &tracking))
	require.Equal(t, "registered", tracking.TrackingStatus)
	require.Equal(t, uint32(1), tracking.NetworkID)
	require.Equal(t, testTxHash, tracking.TxHash)
	require.Equal(t, "null", string(tracking.BridgeStatus))
	require.Equal(t, "null", string(tracking.AllSteps))

	// still no info: polling keeps answering bridge_status: null
	resp = performRequest(t, router, http.MethodGet, path)
	require.Equal(t, http.StatusOK, resp.Code)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &tracking))
	require.Equal(t, "null", string(tracking.BridgeStatus))

	// the tracking engine publishes a status -> tracking_status/bridge_status are populated
	tracker.Publish(TrackingID{NetworkID: 1, TxHash: common.HexToHash(testTxHash)}, testBridgeInfo(), testAllSteps(false))
	resp = performRequest(t, router, http.MethodGet, path)
	require.Equal(t, http.StatusOK, resp.Code)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &tracking))
	require.Equal(t, "running", tracking.TrackingStatus)

	var status struct {
		BridgeType string `json:"bridge_type"`
		Event      struct {
			LeafType string `json:"leaf_type"`
		} `json:"event"`
	}
	require.NoError(t, json.Unmarshal(tracking.BridgeStatus, &status))
	require.Equal(t, "L2->L1", status.BridgeType)
	require.Equal(t, "Asset", status.Event.LeafType)

	var allSteps []struct {
		StepName string `json:"step_name"`
	}
	require.NoError(t, json.Unmarshal(tracking.AllSteps, &allSteps))
	require.Equal(t, "PendingInclusion", allSteps[tracking.StepIndex].StepName)
}

// TestGetTxStatusHandlerResolvesWithinRegisterResolveTimeout pins the end-to-end wiring of
// Config.RegisterResolveTimeout: with a live tracking engine sharing the same registry, the
// very first response for a freshly registered tx already carries the resolved status instead
// of the bare Registered state a caller would otherwise have to poll for
func TestGetTxStatusHandlerResolvesWithinRegisterResolveTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tracker := New(&Config{
		Logger:                 log.WithFields("module", "bridgetracker_test"),
		ConfigSHA1:             testConfigSHA1,
		RegisterResolveTimeout: DefaultRegisterResolveTimeout,
	})
	router := gin.New()
	tracker.API().RegisterRoutes(router)

	f := &fakeSources{bridge: l2ToL2Bridge()}
	engine, err := NewEngine(
		EngineConfig{PollInterval: time.Hour}, // far in the future: only the trigger path can resolve in time
		log.WithFields("module", "bridgetracker_test"), tracker.supervised, f.engineSources())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	engine.Start(ctx)

	resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/network/1/tx/"+testTxHash)
	require.Equal(t, http.StatusOK, resp.Code)

	var tracking struct {
		TrackingStatus string          `json:"tracking_status"`
		BridgeStatus   json.RawMessage `json:"bridge_status"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &tracking))
	require.Equal(t, "running", tracking.TrackingStatus)
	require.NotEqual(t, "null", string(tracking.BridgeStatus))
}

// TestGetTxStatusHandlerTerminalError pins the terminal-resolution-failure semantics: when
// the tracking engine gives up trying to resolve the bridge (e.g. the tx does not exist or
// is not a bridge tx), polling keeps answering 200 OK with tracking_status: error and the
// reason in TrackingData.Error — no 404, no separate error shape
func TestGetTxStatusHandlerTerminalError(t *testing.T) {
	tracker, router := newTestTracker(t)

	path := api.TrackerV1Prefix + "/network/1/tx/" + testTxHash
	resp := performRequest(t, router, http.MethodGet, path)
	require.Equal(t, http.StatusOK, resp.Code)

	tracker.PublishError(TrackingID{NetworkID: 1, TxHash: common.HexToHash(testTxHash)}, testErrorStep())

	resp = performRequest(t, router, http.MethodGet, path)
	require.Equal(t, http.StatusOK, resp.Code)

	var tracking struct {
		TrackingStatus string          `json:"tracking_status"`
		BridgeStatus   json.RawMessage `json:"bridge_status"`
		Error          struct {
			ErrorTypeString string   `json:"error_type_string"`
			RetryCount      int      `json:"retry_count"`
			Description     []string `json:"description"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &tracking))
	require.Equal(t, "error", tracking.TrackingStatus)
	require.Equal(t, "null", string(tracking.BridgeStatus), "the bridge was never resolved")
	require.Equal(t, "exhausted", tracking.Error.ErrorTypeString)
	require.Equal(t, 3, tracking.Error.RetryCount)
	require.Equal(t, []string{"bridge tx not found"}, tracking.Error.Description)
}

// TestGetTxStatusHandlerUnprefixedTxHash pins the tx-hash validation behaviour inherited from
// common.IsHexHash: the 0x prefix is optional, so a bare 64-char hex hash is accepted and is
// registered in the supervised list (200) instead of failing validation
func TestGetTxStatusHandlerUnprefixedTxHash(t *testing.T) {
	_, router := newTestTracker(t)

	resp := performRequest(t, router, http.MethodGet,
		api.TrackerV1Prefix+"/network/1/tx/"+strings.TrimPrefix(testTxHash, "0x"))
	require.Equal(t, http.StatusOK, resp.Code)
}

func TestHealthHandler(t *testing.T) {
	_, router := newTestTracker(t)

	resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/health")
	require.Equal(t, http.StatusOK, resp.Code)

	var health types.HealthResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &health))
	require.Equal(t, types.HealthStatusOK, health.Status)
	require.Equal(t, testConfigSHA1, health.ConfigSHA1)
	_, err := uuid.Parse(health.InstanceID)
	require.NoError(t, err, "instance_id must be a valid UUID")
	require.Equal(t, aggkit.Version, health.Version.Version)
	require.NotEmpty(t, health.Version.GoVersion)
	require.NotEmpty(t, health.Version.OS)
	require.NotEmpty(t, health.Version.Arch)
}

// TestHealthHandlerInstanceIDPerInstance verifies the instance id changes across instances
// and stays stable within one instance
func TestHealthHandlerInstanceIDPerInstance(t *testing.T) {
	_, router1 := newTestTracker(t)
	_, router2 := newTestTracker(t)

	getInstanceID := func(router *gin.Engine) string {
		resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/health")
		require.Equal(t, http.StatusOK, resp.Code)
		var health types.HealthResponse
		require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &health))
		return health.InstanceID
	}

	id1 := getInstanceID(router1)
	require.Equal(t, id1, getInstanceID(router1), "same instance must keep its id")
	require.NotEqual(t, id1, getInstanceID(router2), "different instances must have different ids")
}

// TestHealthHandlerNoSideEffects verifies the health endpoint does not register anything in
// the supervised list
func TestHealthHandlerNoSideEffects(t *testing.T) {
	tracker, router := newTestTracker(t)

	resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/health")
	require.Equal(t, http.StatusOK, resp.Code)

	reg, ok := tracker.supervised.(*memoryRegistry)
	require.True(t, ok, "default registry must be the in-memory adapter")
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	require.Empty(t, reg.bridges)
}

// TestActivityHandlerNotRegisteredWithoutSources verifies the activity endpoint is absent
// (404) when Config.ActivityScanner/ActivityClaims are left nil, exactly like every tracker
// built before this endpoint existed
func TestActivityHandlerNotRegisteredWithoutSources(t *testing.T) {
	_, router := newTestTracker(t)

	resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/activity/from/"+testFromAddress.Hex())
	require.Equal(t, http.StatusNotFound, resp.Code)
}

// TestActivityHandlerInvalidAddress verifies an invalid from_address is rejected with 400
func TestActivityHandlerInvalidAddress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tracker := New(&Config{
		Logger:          log.WithFields("module", "bridgetracker_test"),
		ConfigSHA1:      testConfigSHA1,
		ActivityScanner: &fakeActivityScanner{},
		ActivityClaims:  &fakeActivityClaims{},
	})
	router := gin.New()
	tracker.API().RegisterRoutes(router)

	resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/activity/from/not-an-address")
	require.Equal(t, http.StatusBadRequest, resp.Code)
}

// TestActivityHandlerHappyPath verifies the activity endpoint reports the bridges found by the
// wired sources, in the ActivityResponse wire shape
func TestActivityHandlerHappyPath(t *testing.T) {
	bridge := testBridge(1)
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}

	gin.SetMode(gin.TestMode)
	tracker := New(&Config{
		Logger:     log.WithFields("module", "bridgetracker_test"),
		ConfigSHA1: testConfigSHA1,
		ActivityScanner: &fakeActivityScanner{
			bridges: []*bridgeservicetypes.BridgeResponse{bridge},
		},
		ActivityClaims: &fakeActivityClaims{
			isClaimed: []bool{true},
			claimInfo: []*bridgeservicetypes.ClaimResponse{claim},
		},
	})
	router := gin.New()
	tracker.API().RegisterRoutes(router)

	resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/activity/from/"+testFromAddress.Hex())
	require.Equal(t, http.StatusOK, resp.Code)

	var body api.ActivityResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	require.Equal(t, testFromAddress, body.FromAddress)
	require.Len(t, body.Bridges, 1)
	require.Equal(t, "true", body.Bridges[0].Claimed)
	require.Equal(t, bridge.OriginNetwork, body.Bridges[0].BridgeNetworkID)
	require.Equal(t, claim.TxHash, body.Bridges[0].Claim.TxHash)
	require.Equal(t, bridge.DestinationNetwork, body.Bridges[0].ClaimNetworkID)
}
