package api

import (
	"net/http"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	// wsWriteWait is the deadline applied to every write to the peer
	wsWriteWait = 10 * time.Second
	// wsPongWait is how long the connection survives without a pong from the peer
	wsPongWait = 60 * time.Second
	// wsPingPeriod is the period between ping frames; it must be shorter than wsPongWait
	wsPingPeriod = (wsPongWait * 9) / 10 //nolint:mnd // conventional 90% of the pong wait
)

// wsUpgrader upgrades tracker HTTP requests to WebSocket connections. The tracker is a
// public read-only API served to arbitrary web clients, so cross-origin upgrades are allowed
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

// wsHandler serves the WebSocket bridge-status subscription endpoint. Built once at API
// construction time with the supervised registry and logger it needs.
type wsHandler struct {
	logger     aggkitcommon.Logger
	supervised domain.SupervisedRegistry
}

// TxStatusWSHandler upgrades the request to a WebSocket connection subscribed to the bridge
// originated by the given transaction hash. Connecting registers the bridge in the supervised
// list, same as the REST endpoint. The server then pushes:
//
//   - an initial "status" message (TrackingData) — its BridgeStatus field is nil if the
//     tracker has no information yet, or populated if it does; no further action from the
//     client is needed, the field fills in on a later message;
//   - a "status" message with the full TrackingData on every change;
//   - the final "status" message when the bridge reaches a terminal state — Claimed, or the
//     tracker giving up trying to resolve it at all (TrackingData.Error set) — followed by a
//     normal closure (code 1000);
//   - an "error" message (ErrorData) followed by the closure of the connection only if the
//     request itself is invalid (bad network_id/tx_hash path parameters).
//
// @Summary Subscribe to bridge status updates by transaction hash
// @Description Upgrades the request to a WebSocket connection that receives the bridge
// @Description status as it changes, instead of polling the REST endpoint. Connecting adds
// @Description the bridge to the list of supervised bridges
// @Tags bridge-tracker
// @Param network_id path uint32 true "Network where the bridge transaction was sent (0 -> Mainnet)"
// @Param tx_hash path string true "Hash of the transaction that created the bridge (bridgeAsset or bridgeMessage)"
// @Success 101 {string} string "Switching Protocols"
// @Router /network/{network_id}/tx/{tx_hash}/ws [get]
func (w *wsHandler) TxStatusWSHandler(c *gin.Context) {
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// Upgrade already replied to the client with an HTTP error
		w.logger.Debugf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	req, err := parseBridgeRequest(c)
	if err != nil {
		w.wsSendError(conn, &types.ErrorData{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}

	id := domain.TrackingID{NetworkID: req.NetworkID, TxHash: req.TxHash}

	// Subscribe before reading the snapshot so no update published in between is missed;
	// the latest-value channel semantics collapse any duplicate with the initial message
	updates, unsubscribe := w.supervised.Subscribe(id)
	defer unsubscribe()
	tracking, err := w.supervised.Get(id, true)
	if err != nil {
		w.wsSendError(conn, &types.ErrorData{Code: http.StatusInternalServerError, Message: err.Error()})
		return
	}

	if !w.wsSendTracking(conn, tracking) {
		return
	}
	if reason, done := wsTerminalReason(tracking); done {
		w.wsClose(conn, websocket.CloseNormalClosure, reason)
		return
	}

	// Reader loop: the client is not expected to send data frames, but reading is required
	// to process pong (keepalive) and close frames. Its termination signals a dead peer
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		conn.SetReadLimit(512) //nolint:mnd // server-push endpoint: clients only send control frames
		if err := conn.SetReadDeadline(time.Now().Add(wsPongWait)); err != nil {
			return
		}
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(wsPongWait))
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	pingTicker := time.NewTicker(wsPingPeriod)
	defer pingTicker.Stop()

	for {
		select {
		case update := <-updates:
			if !w.wsSendTracking(conn, update) {
				return
			}
			if reason, done := wsTerminalReason(update); done {
				// Terminal state: final status already sent, close normally
				w.wsClose(conn, websocket.CloseNormalClosure, reason)
				return
			}
		case <-pingTicker.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
				return
			}
		case <-readerDone:
			return
		case <-c.Request.Context().Done():
			return
		}
	}
}

// wsSendTracking pushes a "status" message carrying the wire TrackingData built from
// tracking; BridgeStatus/StepIndex/AllSteps are nil until the tracker resolves the bridge, and
// Error is set only if it gave up trying to resolve the bridge at all. Returns false if the
// write failed
func (w *wsHandler) wsSendTracking(conn *websocket.Conn, tracking *domain.TrackingData) bool {
	return w.wsSend(conn, types.WSMessage{Type: types.WSTypeStatus, Data: trackingDataFrom(tracking)})
}

// wsTerminalReason reports whether the given snapshot is a terminal state the connection
// should close normally after, and the reason to close with: the bridge reached Claimed, or
// the tracker gave up trying to resolve it at all (domain.TrackingData.Failed — a step-level
// error on an otherwise-resolved bridge is not terminal, the engine keeps polling it)
func wsTerminalReason(tracking *domain.TrackingData) (reason string, done bool) {
	switch {
	case tracking.TrackingStatus() == types.TrackingStatusFinished:
		return "bridge claimed", true
	case tracking.Failed():
		return "tracker gave up resolving the bridge", true
	default:
		return "", false
	}
}

// wsSendError pushes an "error" message and closes the connection. Only used for invalid
// request parameters (400): once a bridge is registered, every outcome — including giving up
// on it — flows through wsSendTracking as a "status" message instead
func (w *wsHandler) wsSendError(conn *websocket.Conn, errData *types.ErrorData) {
	if w.wsSend(conn, types.WSMessage{Type: types.WSTypeError, Data: errData}) {
		w.wsClose(conn, websocket.ClosePolicyViolation, errData.Message)
	}
}

// wsSend writes a JSON text frame; returns false if the write failed
func (w *wsHandler) wsSend(conn *websocket.Conn, msg types.WSMessage) bool {
	if err := conn.SetWriteDeadline(time.Now().Add(wsWriteWait)); err != nil {
		return false
	}
	if err := conn.WriteJSON(msg); err != nil {
		w.logger.Debugf("websocket write failed: %v", err)
		return false
	}
	return true
}

// wsClose sends a close control frame with the given code and reason
func (w *wsHandler) wsClose(conn *websocket.Conn, code int, reason string) {
	deadline := time.Now().Add(wsWriteWait)
	if err := conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason), deadline); err != nil {
		w.logger.Debugf("websocket close failed: %v", err)
	}
}
