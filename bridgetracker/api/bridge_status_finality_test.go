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

// TestBridgeStatusMarshalJSON_BlockFinalizedIsAlwaysSerialized verifies bridge_status.block_finalized
// is on the wire whatever its value (no omitempty): false until the tracker follow-up populates
// it, and true once it does
func TestBridgeStatusMarshalJSON_BlockFinalizedIsAlwaysSerialized(t *testing.T) {
	for _, finalized := range []bool{false, true} {
		data, err := json.Marshal(BridgeStatus{BlockFinalized: finalized})
		require.NoError(t, err)

		var raw map[string]any
		require.NoError(t, json.Unmarshal(data, &raw))
		value, present := raw["block_finalized"]
		require.True(t, present, "block_finalized must always be serialized")
		require.Equal(t, finalized, value)

		var decoded BridgeStatus
		require.NoError(t, json.Unmarshal(data, &decoded))
		require.Equal(t, finalized, decoded.BlockFinalized)
	}
}

// TestNewBridgeStatus_CarriesBlockFinalized verifies BridgeStatus.BlockFinalized is taken from
// BridgeInfo.BlockFinalized as it is
func TestNewBridgeStatus_CarriesBlockFinalized(t *testing.T) {
	info := &domain.BridgeInfo{
		NetworkID:          1,
		LeafType:           types.BridgeLeafTypeAsset,
		DestinationNetwork: 0,
		BlockNumber:        1000,
		OriginAddress:      common.HexToAddress("0x20"),
		DestinationAddress: common.HexToAddress("0x30"),
		Amount:             big.NewInt(100),
	}
	require.False(t, newBridgeStatus(info).BlockFinalized, "a bridge that does not set BlockFinalized is not final")

	info.BlockFinalized = true
	require.True(t, newBridgeStatus(info).BlockFinalized)
}

// TestTrackingDataJSONRoundTrip_ClaimedFinalized verifies TrackingData's claim_status accepts the
// new "claimed_finalized" value on the wire and that bridge_status.block_finalized rides along
func TestTrackingDataJSONRoundTrip_ClaimedFinalized(t *testing.T) {
	data := TrackingData{
		TrackingStatus: types.TrackingStatusFinished.String(),
		ClaimStatus:    types.TrackerClaimStatusClaimedFinalized.String(),
		NetworkID:      1,
		BridgeStatus:   &BridgeStatus{BridgeType: types.BridgeTypeL2ToL1.String(), BlockFinalized: true},
	}

	encoded, err := json.Marshal(data)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"claim_status":"claimed_finalized"`)
	require.Contains(t, string(encoded), `"block_finalized":true`)

	var decoded TrackingData
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, data, decoded)
}
