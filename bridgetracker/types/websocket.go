package types

// WebSocket message types carried in the WSMessage envelope
const (
	// WSTypeStatus is a TrackingData snapshot (initial or on every change); its BridgeStatus
	// field is nil until the tracker resolves the bridge
	WSTypeStatus = "status"
	// WSTypeError is an ErrorData sent only for invalid request parameters (bad network_id/
	// tx_hash); the server closes the connection after sending it. Once a bridge is
	// registered, every outcome — including giving up trying to resolve it — is a
	// WSTypeStatus message instead (see TrackingData.Error)
	WSTypeError = "error"
)

// WSMessage is the envelope of every message the WebSocket endpoint sends
type WSMessage struct {
	// Type is one of WSTypeStatus or WSTypeError
	Type string `json:"type"`
	// Data is a TrackingData for "status" and an ErrorData for "error"
	Data any `json:"data"`
}
