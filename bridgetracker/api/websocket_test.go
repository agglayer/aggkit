package api

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// wsTestReadTimeout bounds how long TestTxStatusWSHandlerSubscribeErrorRedacted waits for the
// server's messages/close frame
const wsTestReadTimeout = 5 * time.Second

func TestWSTerminalReason(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		resolved       bool
		txError        *types.ErrorStep
		steps          []domain.BridgeStepPath
		expectedReason string
		expectedDone   bool
	}{
		{name: "registered"},
		{
			name:    "transient tx error",
			txError: &types.ErrorStep{ErrorType: types.StepErrorTransient},
		},
		{
			name:           "permanent tx error",
			txError:        &types.ErrorStep{ErrorType: types.StepErrorPermanent},
			expectedReason: "tracker gave up resolving the bridge",
			expectedDone:   true,
		},
		{
			name:           "exhausted tx error",
			txError:        &types.ErrorStep{ErrorType: types.StepErrorExhausted},
			expectedReason: "tracker gave up resolving the bridge",
			expectedDone:   true,
		},
		{
			name:     "step in progress",
			resolved: true,
			steps: []domain.BridgeStepPath{{
				Step: types.StepWaitingClaim, Status: types.StepStatusInProgress,
			}},
		},
		{
			name:     "transient step error",
			resolved: true,
			steps: []domain.BridgeStepPath{{
				Step: types.StepWaitL1SettledGER, Status: types.StepStatusError,
				Error: &types.ErrorStep{ErrorType: types.StepErrorTransient},
			}},
		},
		{
			name:     "permanent step error",
			resolved: true,
			steps: []domain.BridgeStepPath{{
				Step: types.StepWaitL1SettledGER, Status: types.StepStatusError,
				Error: &types.ErrorStep{ErrorType: types.StepErrorPermanent},
			}},
			expectedReason: "tracker gave up resolving the bridge",
			expectedDone:   true,
		},
		{
			name:     "exhausted step error",
			resolved: true,
			steps: []domain.BridgeStepPath{{
				Step: types.StepWaitL1SettledGER, Status: types.StepStatusError,
				Error: &types.ErrorStep{ErrorType: types.StepErrorExhausted},
			}},
			expectedReason: "tracker gave up resolving the bridge",
			expectedDone:   true,
		},
		{
			name:     "step error without details",
			resolved: true,
			steps: []domain.BridgeStepPath{{
				Step: types.StepWaitL1SettledGER, Status: types.StepStatusError,
			}},
			expectedReason: "tracker gave up resolving the bridge",
			expectedDone:   true,
		},
		{
			name:     "claimed",
			resolved: true,
			steps: []domain.BridgeStepPath{{
				Step: types.StepClaimed, Status: types.StepStatusDone,
			}},
			expectedReason: "bridge claimed",
			expectedDone:   true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			bridgeTx := domain.TrackingBridgeTx{Error: testCase.txError}
			if testCase.resolved {
				bridgeTx.Info = &domain.BridgeInfo{NetworkID: 1}
			}
			tracking := domain.NewTrackingData(domain.TrackingID{}, bridgeTx, testCase.steps)

			reason, done := wsTerminalReason(tracking)
			require.Equal(t, testCase.expectedDone, done)
			require.Equal(t, testCase.expectedReason, reason)
		})
	}
}

// TestTxStatusWSHandlerSubscribeErrorRedacted pins that a backend URL embedded in a
// StatusNotifier.Subscribe error never reaches the client over the WebSocket endpoint: neither
// in the "error" message payload (types.ErrorData.Message, redacted at the construction site in
// TxStatusWSHandler) nor in the close-frame reason wsClose sends right after (which reuses that
// same already-redacted string, see websocket.go's wsSendError)
func TestTxStatusWSHandlerSubscribeErrorRedacted(t *testing.T) {
	t.Parallel()

	rawErr := `claim status: fetching claims of global index 123 on network 2: do request: ` +
		`Get "http://10.0.0.5:5577/bridge/v1/claims?network_id=2&global_index=123": ` +
		`dial tcp 10.0.0.5:5577: connect: connection refused`

	handler := newWSHandler(
		log.NewLoggerNil(),
		&fakeSupervisedRegistry{subscribeErr: errors.New(rawErr)},
		aggkitcommon.CORSConfig{},
	)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/ws/:"+networkIDParam+"/:"+txHashParam, handler.TxStatusWSHandler)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/1/" + testGetTxStatusHash
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	t.Cleanup(func() { conn.Close() })
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(wsTestReadTimeout)))

	var msg struct {
		Type string          `json:"type"`
		Data types.ErrorData `json:"data"`
	}
	require.NoError(t, conn.ReadJSON(&msg))
	require.Equal(t, string(types.WSTypeError), msg.Type)
	require.NotContains(t, msg.Data.Message, "://")
	require.NotContains(t, msg.Data.Message, "10.0.0.5")

	_, _, err = conn.ReadMessage()
	var closeErr *websocket.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.NotContains(t, closeErr.Text, "://")
	require.NotContains(t, closeErr.Text, "10.0.0.5")
}
