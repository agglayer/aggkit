package bridgetracker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/api"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

const wsTestTimeout = 5 * time.Second

// wsEnvelope mirrors types.WSMessage with a raw payload so each test can decode Data into
// the type announced by Type
type wsEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// newTestTrackerWithCORS is like newTestTracker but with a custom CORS policy, for tests that
// exercise the WebSocket endpoint's origin check.
func newTestTrackerWithCORS(t *testing.T, cors aggkitcommon.CORSConfig) (*BridgeTracker, *gin.Engine) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	tracker := New(&Config{
		Logger:     log.WithFields("module", "bridgetracker_test"),
		ConfigSHA1: testConfigSHA1,
		CORS:       cors,
	})
	router := gin.New()
	tracker.API().RegisterRoutes(router)
	return tracker, router
}

// dialWS spins up the tracker on a test server and opens a WebSocket connection to the
// given tx path
func dialWS(t *testing.T, networkID, txHash string) (*BridgeTracker, *websocket.Conn) {
	t.Helper()

	tracker, router := newTestTracker(t)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") +
		api.TrackerV1Prefix + "/network/" + networkID + "/tx/" + txHash + "/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	t.Cleanup(func() { conn.Close() })

	return tracker, conn
}

func readEnvelope(t *testing.T, conn *websocket.Conn) wsEnvelope {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(wsTestTimeout)))
	var msg wsEnvelope
	require.NoError(t, conn.ReadJSON(&msg))
	return msg
}

// expectClose reads until the peer closes the connection and returns the close code
func expectClose(t *testing.T, conn *websocket.Conn) int {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(wsTestTimeout)))
	_, _, err := conn.ReadMessage()
	var closeErr *websocket.CloseError
	require.ErrorAs(t, err, &closeErr, "expected a close frame")
	return closeErr.Code
}

// TestWSTrackingThenStatusUpdates pins the full happy path: connect on an unknown bridge
// (bridge_status: null), receive a status push with bridge_status populated on publish, and
// a final status + normal closure when the bridge reaches Claimed
func TestWSTrackingThenStatusUpdates(t *testing.T) {
	tracker, conn := dialWS(t, "1", testTxHash)

	// initial message: registered, no info yet (bridge_status: null)
	msg := readEnvelope(t, conn)
	require.Equal(t, types.WSTypeStatus, msg.Type)

	var tracking struct {
		TrackingStatus string          `json:"tracking_status"`
		NetworkID      uint32          `json:"network_id"`
		TxHash         string          `json:"tx_hash"`
		BridgeStatus   json.RawMessage `json:"bridge_status"`
		StepIndex      int             `json:"step_index"`
		AllSteps       json.RawMessage `json:"all_steps"`
	}
	require.NoError(t, json.Unmarshal(msg.Data, &tracking))
	require.Equal(t, "registered", tracking.TrackingStatus)
	require.Equal(t, uint32(1), tracking.NetworkID)
	require.Equal(t, testTxHash, tracking.TxHash)
	require.Equal(t, "null", string(tracking.BridgeStatus))

	// engine publishes an in-progress status -> tracking_status/bridge_status populated
	tracker.Publish(TrackingID{NetworkID: 1, TxHash: common.HexToHash(testTxHash)}, testBridgeInfo(), testAllSteps(false))
	msg = readEnvelope(t, conn)
	require.Equal(t, types.WSTypeStatus, msg.Type)
	require.NoError(t, json.Unmarshal(msg.Data, &tracking))
	require.Equal(t, "running", tracking.TrackingStatus)

	var allSteps []struct {
		StepName string `json:"step_name"`
	}
	require.NoError(t, json.Unmarshal(tracking.AllSteps, &allSteps))
	require.Equal(t, "PendingInclusion", allSteps[tracking.StepIndex].StepName)

	// terminal state -> final status followed by a normal closure (1000)
	tracker.Publish(TrackingID{NetworkID: 1, TxHash: common.HexToHash(testTxHash)}, testBridgeInfo(), testAllSteps(true))
	msg = readEnvelope(t, conn)
	require.Equal(t, types.WSTypeStatus, msg.Type)
	require.NoError(t, json.Unmarshal(msg.Data, &tracking))
	require.Equal(t, "finished", tracking.TrackingStatus)
	require.NoError(t, json.Unmarshal(tracking.AllSteps, &allSteps))
	require.Equal(t, "Claimed", allSteps[tracking.StepIndex].StepName)

	require.Equal(t, websocket.CloseNormalClosure, expectClose(t, conn))
}

// TestWSInitialStatusWhenKnown pins that a bridge already known to the tracker gets its
// current status pushed immediately on connect
func TestWSInitialStatusWhenKnown(t *testing.T) {
	tracker, router := newTestTracker(t)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	// register + publish before connecting
	resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/network/1/tx/"+testTxHash)
	require.Equal(t, http.StatusOK, resp.Code)
	tracker.Publish(TrackingID{NetworkID: 1, TxHash: common.HexToHash(testTxHash)}, testBridgeInfo(), testAllSteps(false))

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") +
		api.TrackerV1Prefix + "/network/1/tx/" + testTxHash + "/ws"
	conn, dialResp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if dialResp != nil && dialResp.Body != nil {
		dialResp.Body.Close()
	}
	t.Cleanup(func() { conn.Close() })

	msg := readEnvelope(t, conn)
	require.Equal(t, types.WSTypeStatus, msg.Type)
}

// TestWSTerminalError pins that giving up trying to resolve a bridge is pushed as a normal
// "status" message (tracking_status: error, TrackingData.Error set) followed by a normal
// closure — not a distinct "error" message, which is reserved for invalid request parameters
func TestWSTerminalError(t *testing.T) {
	tracker, conn := dialWS(t, "1", testTxHash)

	msg := readEnvelope(t, conn)
	require.Equal(t, types.WSTypeStatus, msg.Type)

	tracker.PublishError(TrackingID{NetworkID: 1, TxHash: common.HexToHash(testTxHash)}, testErrorStep())

	msg = readEnvelope(t, conn)
	require.Equal(t, types.WSTypeStatus, msg.Type)

	var tracking struct {
		TrackingStatus string          `json:"tracking_status"`
		BridgeStatus   json.RawMessage `json:"bridge_status"`
		Error          struct {
			ErrorTypeString string `json:"error_type_string"`
			RetryCount      int    `json:"retry_count"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(msg.Data, &tracking))
	require.Equal(t, "error", tracking.TrackingStatus)
	require.Equal(t, "null", string(tracking.BridgeStatus))
	require.Equal(t, "exhausted", tracking.Error.ErrorTypeString)
	require.Equal(t, 3, tracking.Error.RetryCount)

	require.Equal(t, websocket.CloseNormalClosure, expectClose(t, conn))
}

// TestWSInvalidParams pins that invalid path params produce an error message over the
// upgraded connection followed by its closure
func TestWSInvalidParams(t *testing.T) {
	_, conn := dialWS(t, "1", "0x1234")

	msg := readEnvelope(t, conn)
	require.Equal(t, types.WSTypeError, msg.Type)

	var errData types.ErrorData
	require.NoError(t, json.Unmarshal(msg.Data, &errData))
	require.Equal(t, http.StatusBadRequest, errData.Code)
	require.Contains(t, errData.Message, "tx_hash")

	require.Equal(t, websocket.ClosePolicyViolation, expectClose(t, conn))
}

// TestWSCORSOriginCheck pins that the WebSocket handshake enforces the tracker's CORS policy
// instead of Access-Control-* response headers (browsers never CORS-preflight or gate the
// Upgrade request on those): disabled CORS (the default) accepts any origin, matching the
// tracker's pre-CORS-config behavior; enabled with a restricted AllowedOrigins rejects the
// handshake outright (403) for an origin not on the list, and accepts one that is.
func TestWSCORSOriginCheck(t *testing.T) {
	tests := []struct {
		name       string
		cors       aggkitcommon.CORSConfig
		origin     string
		wantForbid bool
	}{
		{
			name:   "CORS disabled allows any origin",
			cors:   aggkitcommon.CORSConfig{Enabled: false},
			origin: "https://not-in-the-list.com",
		},
		{
			name:   "CORS enabled allows a listed origin",
			cors:   aggkitcommon.CORSConfig{Enabled: true, AllowedOrigins: []string{"https://allowed.com"}},
			origin: "https://allowed.com",
		},
		{
			name:       "CORS enabled rejects an origin not on the list",
			cors:       aggkitcommon.CORSConfig{Enabled: true, AllowedOrigins: []string{"https://allowed.com"}},
			origin:     "https://not-allowed.com",
			wantForbid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, router := newTestTrackerWithCORS(t, tt.cors)
			server := httptest.NewServer(router)
			t.Cleanup(server.Close)

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") +
				api.TrackerV1Prefix + "/network/1/tx/" + testTxHash + "/ws"
			header := http.Header{"Origin": []string{tt.origin}}
			conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
			if resp != nil && resp.Body != nil {
				defer resp.Body.Close()
			}

			if tt.wantForbid {
				require.Error(t, err)
				require.NotNil(t, resp)
				require.Equal(t, http.StatusForbidden, resp.StatusCode)
				return
			}

			require.NoError(t, err)
			t.Cleanup(func() { conn.Close() })
			msg := readEnvelope(t, conn)
			require.Equal(t, types.WSTypeStatus, msg.Type)
		})
	}
}
