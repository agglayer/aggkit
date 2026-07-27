package types

import (
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
)

// TrackingStatus is the full lifecycle of a supervised bridge, from registration to
// settlement: Registered while BridgeStatus is still nil, then Running/Error/Finished from
// the same signal TrackingData.StepIndex points at once it resolves
type TrackingStatus int

const (
	// TrackingStatusRegistered the bridge has been added to the supervised list but the
	// tracker has no information about it yet (BridgeStatus is nil)
	TrackingStatusRegistered TrackingStatus = iota
	// TrackingStatusRunning the bridge is resolved and alive: still being polled/updated
	TrackingStatusRunning
	// TrackingStatusError the bridge is resolved and stopped: one of its steps reached an error
	TrackingStatusError
	// TrackingStatusFinished the bridge is resolved and reached its terminal step (Claimed)
	TrackingStatusFinished
)

var trackingStatusNames = map[TrackingStatus]string{
	TrackingStatusRegistered: "registered",
	TrackingStatusRunning:    "running",
	TrackingStatusError:      "error",
	TrackingStatusFinished:   "finished",
}

// String representation of the enum
func (t TrackingStatus) String() string {
	if name, ok := trackingStatusNames[t]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", int(t))
}

// TrackingData is the body of every GET /tracker/v1/network/{network_id}/tx/{tx_hash}
// response (always 200 OK) and of every WebSocket "status" message: TrackingStatus carries
// the bridge's full lifecycle, and BridgeStatus carries the detail behind it once resolved
type TrackingData struct {
	TrackingStatus TrackingStatus `json:"tracking_status"`
	// TrackingStatusString is the string representation of TrackingStatus,
	// auto-populated on JSON marshaling
	TrackingStatusString string `json:"tracking_status_string"`
	// NetworkID is the network of the request (0 -> Mainnet)
	NetworkID uint32 `json:"network_id"`
	// TxHash is the transaction hash of the request
	TxHash common.Hash `json:"tx_hash"`
	// BridgeStatus is nil until the tracker resolves the bridge; from then on it carries
	// the full BridgeStatus. Marshaled explicitly as null while unresolved (no omitempty)
	// so clients can poll on its presence without an extra field to check
	BridgeStatus *BridgeStatus `json:"bridge_status"`
	// StepIndex is the index into AllSteps of the step that explains TrackingStatus: the
	// step currently in progress when Running, the step in error when Error, or the last
	// step (Claimed) when Finished. nil while BridgeStatus/AllSteps are nil
	StepIndex *int `json:"step_index"`
	// AllSteps holds all expected steps of the bridge's route; GER/LER, certificate and
	// claim data are reported per step in each entry's Result. nil while BridgeStatus is nil
	AllSteps []BridgeStepPath `json:"all_steps"`
	// Error is set when the tracker gave up trying to even resolve the bridge (e.g. the tx
	// does not exist on the network or is not a bridge transaction). TrackingStatus is Error
	// and BridgeStatus/StepIndex/AllSteps stay nil forever in that case. Unrelated to
	// per-step errors, which are carried in AllSteps[i].Error instead
	Error *ErrorStep `json:"error"`
}

// MarshalJSON is the implementation of the json.Marshaler interface.
// It populates the string representation of the numeric enum fields
func (t TrackingData) MarshalJSON() ([]byte, error) {
	t.TrackingStatusString = t.TrackingStatus.String()
	type trackingDataAlias TrackingData
	return json.Marshal(trackingDataAlias(t))
}
