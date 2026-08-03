package api

import (
	"encoding/json"
	"testing"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/stretchr/testify/require"
)

func TestBridgeStatusJSONRoundTrip(t *testing.T) {
	status := BridgeStatus{
		BridgeType:     types.BridgeTypeL2ToL2,
		BridgeLeafType: types.BridgeLeafTypeMessage,
		BlockNumber:    12345,
		LogIndex:       3,
	}

	data, err := json.Marshal(status)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	require.EqualValues(t, 2, raw["bridge_type"])
	require.Equal(t, "L2->L2", raw["bridge_type_string"])
	require.EqualValues(t, 1, raw["bridge_leaf_type"])
	require.Equal(t, "Message", raw["bridge_leaf_type_string"])
	require.EqualValues(t, 12345, raw["block_number"])
	require.EqualValues(t, 3, raw["log_index"])
	_, hasStepIndex := raw["step_index"]
	require.False(t, hasStepIndex, "step_index now lives on TrackingData, not BridgeStatus")
	_, hasAllSteps := raw["all_steps"]
	require.False(t, hasAllSteps, "all_steps now lives on TrackingData, not BridgeStatus")

	var decoded BridgeStatus
	require.NoError(t, json.Unmarshal(data, &decoded))

	expected := status
	expected.BridgeTypeString = status.BridgeType.String()
	expected.BridgeLeafTypeString = status.BridgeLeafType.String()
	require.Equal(t, expected, decoded)
}

// TestNewBridgeStatus pins that BridgeStatus is fully derived from BridgeInfo: nil while the
// bridge is unresolved, and populated from its BridgeType()/LeafType/BlockNumber/LogIndex once it is
func TestNewBridgeStatus(t *testing.T) {
	require.Nil(t, newBridgeStatus(nil))

	info := &domain.BridgeInfo{
		NetworkID:          1,
		LeafType:           types.BridgeLeafTypeAsset,
		DestinationNetwork: 0,
		BlockNumber:        1000,
		LogIndex:           2,
	}

	status := newBridgeStatus(info)
	require.NotNil(t, status)
	require.Equal(t, types.BridgeTypeL2ToL1, status.BridgeType)
	require.Equal(t, types.BridgeLeafTypeAsset, status.BridgeLeafType)
	require.Equal(t, uint64(1000), status.BlockNumber)
	require.Equal(t, uint32(2), status.LogIndex)
}
