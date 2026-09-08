package api

import (
	"encoding/json"
	"testing"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/stretchr/testify/require"
)

// TestActivityItemMarshalJSON_EmbeddedBridgeGlobalIndexIsQuotedString verifies that
// GET /activity/from/{from_address}'s payload -- which embeds the bridge service's own
// BridgeResponse in ActivityItem.Bridge unmodified -- marshals an L1-origin deposit's
// global_index (2^64, past JavaScript's Number.MAX_SAFE_INTEGER) as a quoted JSON string rather
// than a bare number. This is the path the SDK actually consumes, so the embedding must not
// regress even if BridgeResponse's own marshalling is correct in isolation.
func TestActivityItemMarshalJSON_EmbeddedBridgeGlobalIndexIsQuotedString(t *testing.T) {
	// networkID=0 (mainnet), depositCount=0 -> the same L1-origin encoding NewBridgeResponse
	// uses internally, yielding 2^64 == 18446744073709551616, matching this step's live capture.
	globalIndex := bridgesync.GenerateGlobalIndexForNetworkID(0, 0)
	require.Equal(t, "18446744073709551616", globalIndex.String(), "test fixture must equal 2^64")

	item := ActivityItem{
		Bridge: &bridgeservicetypes.BridgeResponse{
			GlobalIndex: bridgeservicetypes.BigIntString(globalIndex.String()),
		},
		BridgeNetworkID: 0,
		ClaimStatus:     "pending",
	}

	data, err := json.Marshal(item)
	require.NoError(t, err)

	require.Contains(t, string(data), `"global_index":"18446744073709551616"`,
		"embedded BridgeResponse.GlobalIndex must marshal as a quoted string")
	require.NotContains(t, string(data), `"global_index":18446744073709551616`,
		"embedded BridgeResponse.GlobalIndex must never marshal as a bare JSON number")
}
