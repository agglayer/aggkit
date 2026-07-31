package api

import (
	"encoding/json"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
)

// TrackingData is the body of every GET /tracker/v1/network/{network_id}/tx/{tx_hash}
// response (always 200 OK) and of every WebSocket "status" message: TrackingStatus carries
// the bridge's full lifecycle, and BridgeStatus carries the detail behind it once resolved
type TrackingData struct {
	TrackingStatus types.TrackingStatus `json:"tracking_status"`
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
	AllSteps []types.BridgeStepPath `json:"all_steps"`
	// Error mirrors whatever currently explains the bridge not progressing, if anything: a
	// terminal give-up to even resolve it (e.g. the tx does not exist on the network or is
	// not a bridge transaction — TrackingStatus is Error and BridgeStatus/StepIndex/AllSteps
	// stay nil forever), a transient FindBridge failure still being retried (TrackingStatus
	// is unaffected), or the same error already nested in AllSteps[StepIndex].Error once the
	// bridge is otherwise resolved. nil while nothing has failed
	Error *types.ErrorStep `json:"error"`
}

// MarshalJSON is the implementation of the json.Marshaler interface.
// It populates the string representation of the numeric enum fields
func (t TrackingData) MarshalJSON() ([]byte, error) {
	t.TrackingStatusString = t.TrackingStatus.String()
	type trackingDataAlias TrackingData
	return json.Marshal(trackingDataAlias(t))
}

// trackingDataFrom builds the wire TrackingData from a supervised-registry snapshot
func trackingDataFrom(t *domain.TrackingData) TrackingData {
	id := t.ID()
	return TrackingData{
		TrackingStatus: t.TrackingStatus(),
		NetworkID:      id.NetworkID,
		TxHash:         id.TxHash,
		BridgeStatus:   newBridgeStatus(t.Info()),
		StepIndex:      t.StepIndex(),
		AllSteps:       t.AllSteps(),
		Error:          t.Error(),
	}
}
