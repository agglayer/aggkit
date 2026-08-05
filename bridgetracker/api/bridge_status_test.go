package api

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestBridgeStatusJSONRoundTrip(t *testing.T) {
	status := BridgeStatus{
		BridgeType:     types.BridgeTypeL2ToL2.String(),
		BlockNumber:    12345,
		LogIndex:       3,
		BlockTimestamp: 1700000000,
		Event: BridgeEventData{
			LeafType:           types.BridgeLeafTypeMessage.String(),
			OriginNetwork:      1,
			OriginAddress:      common.HexToAddress("0x20"),
			DestinationNetwork: 2,
			DestinationAddress: common.HexToAddress("0x30"),
			Amount:             "100",
			DepositCount:       7,
		},
	}

	data, err := json.Marshal(status)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	require.Equal(t, "L2->L2", raw["bridge_type"])
	require.EqualValues(t, 12345, raw["block_number"])
	require.EqualValues(t, 3, raw["log_index"])
	require.EqualValues(t, 1700000000, raw["block_timestamp"])
	event, ok := raw["event"].(map[string]any)
	require.True(t, ok, "event should be a JSON object")
	require.Equal(t, "Message", event["leaf_type"])
	require.EqualValues(t, 1, event["origin_network"])
	require.Equal(t, common.HexToAddress("0x20").Hex(), event["origin_address"])
	require.EqualValues(t, 2, event["destination_network"])
	require.Equal(t, common.HexToAddress("0x30").Hex(), event["destination_address"])
	require.Equal(t, "100", event["amount"])
	require.EqualValues(t, 7, event["deposit_count"])
	_, hasStepIndex := raw["step_index"]
	require.False(t, hasStepIndex, "step_index now lives on TrackingData, not BridgeStatus")
	_, hasAllSteps := raw["all_steps"]
	require.False(t, hasAllSteps, "all_steps now lives on TrackingData, not BridgeStatus")

	var decoded BridgeStatus
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, status, decoded)
}

// TestNewBridgeStatus pins that BridgeStatus is fully derived from BridgeInfo: nil while the
// bridge is unresolved, and populated from its BridgeType()/BlockNumber/LogIndex/
// BlockTimestamp/Event once it is
func TestNewBridgeStatus(t *testing.T) {
	require.Nil(t, newBridgeStatus(nil))

	info := &domain.BridgeInfo{
		NetworkID:          1,
		LeafType:           types.BridgeLeafTypeAsset,
		DestinationNetwork: 0,
		DepositCount:       7,
		BlockNumber:        1000,
		LogIndex:           2,
		BlockTimestamp:     1700000000,
		OriginNetwork:      1,
		OriginAddress:      common.HexToAddress("0x20"),
		DestinationAddress: common.HexToAddress("0x30"),
		Amount:             big.NewInt(100),
	}

	status := newBridgeStatus(info)
	require.NotNil(t, status)
	require.Equal(t, types.BridgeTypeL2ToL1.String(), status.BridgeType)
	require.Equal(t, uint64(1000), status.BlockNumber)
	require.Equal(t, uint32(2), status.LogIndex)
	require.Equal(t, uint64(1700000000), status.BlockTimestamp)
	require.Equal(t, BridgeEventData{
		LeafType:           types.BridgeLeafTypeAsset.String(),
		OriginNetwork:      1,
		OriginAddress:      common.HexToAddress("0x20"),
		DestinationNetwork: 0,
		DestinationAddress: common.HexToAddress("0x30"),
		Amount:             "100",
		DepositCount:       7,
	}, status.Event)
}
