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

// TestActivityItemMarshalJSON_ErrorsAreRedacted is the S17 C1 regression test: ActivityItem.Errors
// (surfaced by GET /activity/from/{from_address} as "errors") must never leak a backend URL or
// host:port to an API client, whether or not the construction site (ActivityCache.refresh)
// already redacted it - MarshalJSON is the defensive last line.
func TestActivityItemMarshalJSON_ErrorsAreRedacted(t *testing.T) {
	item := ActivityItem{
		BridgeNetworkID: 2,
		ClaimStatus:     "error",
		Errors: map[string]string{
			// Simulates a construction site that forgot to redact (defensive coverage).
			"claim": `resolving JSON-RPC client for network 2: dialing JSON-RPC client of network 2 at ` +
				`http://l2-anvil-002.internal:8545: Post "http://l2-anvil-002.internal:8545": ` +
				`dial tcp 10.0.0.5:8545: connect: connection refused`,
		},
	}

	data, err := json.Marshal(item)
	require.NoError(t, err)

	require.NotContains(t, string(data), "://")
	require.NotContains(t, string(data), "l2-anvil-002.internal")
	require.NotContains(t, string(data), "10.0.0.5")

	var decoded ActivityItem
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "resolving JSON-RPC client for network 2: "+
		"dialing JSON-RPC client of network 2 at <redacted-url>: Post <redacted-url>: "+
		"dial tcp <redacted-host>: connect: connection refused", decoded.Errors["claim"])
}

// TestActivityWarningItemMarshalJSON_MessageIsRedacted is the S17 C1 regression test:
// ActivityWarningItem.Message (surfaced by GET /activity/from/{from_address} as
// "warnings[].message") must never leak a backend URL or host:port to an API client, whether or
// not the construction site (sources.ActivitySource.warnf) already redacted it - MarshalJSON is
// the defensive last line.
func TestActivityWarningItemMarshalJSON_MessageIsRedacted(t *testing.T) {
	item := ActivityWarningItem{
		NetworkID: 2,
		Message: `fetching bridges from 0xabc on network 2: do request: ` +
			`Get "http://aggkit-002.internal:5577/bridge/v1/bridges?from_address=0xabc": ` +
			`dial tcp 10.0.0.6:5577: connection refused`,
	}

	data, err := json.Marshal(item)
	require.NoError(t, err)

	require.NotContains(t, string(data), "://")
	require.NotContains(t, string(data), "aggkit-002.internal")
	require.NotContains(t, string(data), "10.0.0.6")

	var decoded ActivityWarningItem
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "fetching bridges from 0xabc on network 2: "+
		"do request: Get <redacted-url>: dial tcp <redacted-host>: connection refused", decoded.Message)
}

// TestActivityResponseMarshalJSON_NoRemainingURLs is an end-to-end S17 C1 regression test over
// the full wire shape GET /activity/from/{from_address} returns: neither a bridge's "errors" nor
// a top-level "warnings[].message" may contain a raw backend URL or host:port once marshalled,
// even when the construction sites (ActivityCache.refresh / ActivitySource.warnf) are bypassed by
// constructing the response types directly.
func TestActivityResponseMarshalJSON_NoRemainingURLs(t *testing.T) {
	resp := ActivityResponse{
		Bridges: []ActivityItem{{
			BridgeNetworkID: 2,
			ClaimStatus:     "error",
			Errors: map[string]string{
				"readiness": `fetching l1 info tree index for network 2 deposit 7: do request: ` +
					`Get "http://aggkit-002.internal:5577/bridge/v1/l1-info-tree-index?network_id=2": ` +
					`dial tcp i/o timeout`,
			},
		}},
		Warnings: []ActivityWarningItem{{
			NetworkID: 2,
			Message: `fetching bridges from 0xabc on network 2: do request: ` +
				`Get "http://aggkit-002.internal:5577/bridge/v1/bridges?from_address=0xabc": ` +
				`dial tcp connection refused`,
		}},
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	require.NotContains(t, string(data), "://")
	require.NotContains(t, string(data), "aggkit-002.internal")
}
