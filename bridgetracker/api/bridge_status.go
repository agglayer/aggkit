package api

import (
	"encoding/json"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
)

// BridgeStatus is part of the response of GET /tracker/v1/tx/{txHash} (see TrackingData),
// identifying the bridge that TrackingData.AllSteps describes
type BridgeStatus struct {
	BridgeType types.BridgeType `json:"bridge_type"`
	// BridgeTypeString is the string representation of BridgeType, auto-populated on JSON marshaling
	BridgeTypeString string               `json:"bridge_type_string"`
	BridgeLeafType   types.BridgeLeafType `json:"bridge_leaf_type"`
	// BridgeLeafTypeString is the string representation of BridgeLeafType, auto-populated on JSON marshaling
	BridgeLeafTypeString string `json:"bridge_leaf_type_string"`
	// BlockNumber is the block, on the origin network, where the BridgeEvent was emitted
	BlockNumber uint64 `json:"block_number"`
	// LogIndex is the position of the BridgeEvent log within BlockNumber
	LogIndex uint32 `json:"log_index"`
}

// MarshalJSON is the implementation of the json.Marshaler interface.
// It populates the string representation of the numeric enum fields
func (b BridgeStatus) MarshalJSON() ([]byte, error) {
	b.BridgeTypeString = b.BridgeType.String()
	b.BridgeLeafTypeString = b.BridgeLeafType.String()
	type bridgeStatusAlias BridgeStatus
	return json.Marshal(bridgeStatusAlias(b))
}

// newBridgeStatus builds the wire BridgeStatus from the bridge facts resolved by the engine;
// nil until FindBridge resolves them (see domain.TrackingBridgeTx.Info)
func newBridgeStatus(info *domain.BridgeInfo) *BridgeStatus {
	if info == nil {
		return nil
	}
	return &BridgeStatus{
		BridgeType:     info.BridgeType(),
		BridgeLeafType: info.LeafType,
		BlockNumber:    info.BlockNumber,
		LogIndex:       info.LogIndex,
	}
}
