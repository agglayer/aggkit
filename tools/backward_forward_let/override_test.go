package backward_forward_let

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// writeOverrideFile writes content to a temp file and returns the path.
func writeOverrideFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "override.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestLoadBridgeExitsOverride_HappyPath verifies that a valid override file is
// loaded correctly, heights are mapped to uint64, and GetExits returns the right data.
func TestLoadBridgeExitsOverride_HappyPath(t *testing.T) {
	t.Parallel()

	originAddr := "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	destAddr := "0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"

	// Use a raw JSON fixture to explicitly verify the on-disk field names
	// ("dest_network", "dest_address") and amount as a decimal string.
	path := writeOverrideFile(t, `{
		"network_id": 1,
		"description": "test fixture",
		"heights": {
			"3": [
				{
					"leaf_type": 0,
					"token_info": {
						"origin_network": 1,
						"origin_token_address": "`+originAddr+`"
					},
					"dest_network": 2,
					"dest_address": "`+destAddr+`",
					"amount": "100",
					"metadata": null
				}
			],
			"7": []
		}
	}`)

	result, err := LoadBridgeExitsOverride(path)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, uint32(1), result.NetworkID)
	require.Equal(t, "test fixture", result.Description)

	// Height 3: one bridge exit with all fields populated.
	exits3, ok := result.GetExits(3)
	require.True(t, ok)
	require.Len(t, exits3, 1)

	be := exits3[0]
	require.NotNil(t, be.TokenInfo)
	require.Equal(t, uint32(1), be.TokenInfo.OriginNetwork)
	require.Equal(t, common.HexToAddress(originAddr), be.TokenInfo.OriginTokenAddress)
	require.Equal(t, uint32(2), be.DestinationNetwork)
	require.Equal(t, common.HexToAddress(destAddr), be.DestinationAddress)
	require.Equal(t, big.NewInt(100), be.Amount)
	require.Nil(t, be.Metadata)

	// Height 7: empty list present.
	exits7, ok := result.GetExits(7)
	require.True(t, ok, "height 7 must be present")
	require.Empty(t, exits7)

	// Unknown height must return false.
	_, ok = result.GetExits(99)
	require.False(t, ok, "absent height must return false")
}

// TestLoadBridgeExitsOverride_HeightZero verifies that the height key "0" maps to uint64(0).
func TestLoadBridgeExitsOverride_HeightZero(t *testing.T) {
	t.Parallel()

	path := writeOverrideFile(t, `{
		"network_id": 2,
		"heights": {
			"0": []
		}
	}`)

	result, err := LoadBridgeExitsOverride(path)
	require.NoError(t, err)

	exits, ok := result.GetExits(0)
	require.True(t, ok)
	require.Empty(t, exits)

	_, ok = result.GetExits(1)
	require.False(t, ok)
}

// TestLoadBridgeExitsOverride_EmptyExitsList verifies that an empty exits list at a
// height returns ([], true) from GetExits (present but empty, not absent).
func TestLoadBridgeExitsOverride_EmptyExitsList(t *testing.T) {
	t.Parallel()

	path := writeOverrideFile(t, `{
		"network_id": 1,
		"heights": {
			"5": []
		}
	}`)

	result, err := LoadBridgeExitsOverride(path)
	require.NoError(t, err)

	exits, ok := result.GetExits(5)
	require.True(t, ok, "height 5 must be present")
	require.Empty(t, exits, "empty list must be returned as empty, not absent")

	_, ok = result.GetExits(999)
	require.False(t, ok, "absent height must return false")
}

// TestLoadBridgeExitsOverride_RoundTrip verifies that marshaling a BridgeExit slice via
// the internal wire type and loading it back produces identical values.
func TestLoadBridgeExitsOverride_RoundTrip(t *testing.T) {
	t.Parallel()

	tokenAddr := common.HexToAddress("0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC")
	destAddr := common.HexToAddress("0xDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD")

	original := []*agglayertypes.BridgeExit{
		{
			DestinationNetwork: 3,
			DestinationAddress: destAddr,
			TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: tokenAddr},
			Amount:             big.NewInt(999),
			Metadata:           nil,
		},
	}

	raw := overrideFileJSON{
		NetworkID:   5,
		Description: "round-trip test",
		Heights:     map[string][]*agglayertypes.BridgeExit{"2": original},
	}
	data, err := json.Marshal(raw)
	require.NoError(t, err)

	tmpPath := filepath.Join(t.TempDir(), "override.json")
	require.NoError(t, os.WriteFile(tmpPath, data, 0o600))

	result, err := LoadBridgeExitsOverride(tmpPath)
	require.NoError(t, err)
	require.Equal(t, uint32(5), result.NetworkID)
	require.Equal(t, "round-trip test", result.Description)

	exits, ok := result.GetExits(2)
	require.True(t, ok)
	require.Len(t, exits, 1)
	require.Equal(t, uint32(3), exits[0].DestinationNetwork)
	require.Equal(t, destAddr, exits[0].DestinationAddress)
	require.Equal(t, big.NewInt(999), exits[0].Amount)
	require.Nil(t, exits[0].Metadata)
}

// TestLoadBridgeExitsOverride_NonNumericKey verifies that a non-numeric height key
// causes an error.
func TestLoadBridgeExitsOverride_NonNumericKey(t *testing.T) {
	t.Parallel()

	path := writeOverrideFile(t, `{
		"network_id": 1,
		"heights": {
			"abc": []
		}
	}`)

	_, err := LoadBridgeExitsOverride(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "non-numeric height key")
	require.Contains(t, err.Error(), `"abc"`)
}

// TestLoadBridgeExitsOverride_MissingNetworkID verifies that a zero (or absent)
// network_id is rejected.
func TestLoadBridgeExitsOverride_MissingNetworkID(t *testing.T) {
	t.Parallel()

	path := writeOverrideFile(t, `{
		"heights": {
			"0": []
		}
	}`)

	_, err := LoadBridgeExitsOverride(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "network_id must be non-zero")
}

// TestLoadBridgeExitsOverride_MalformedJSON verifies that malformed JSON is rejected.
func TestLoadBridgeExitsOverride_MalformedJSON(t *testing.T) {
	t.Parallel()

	path := writeOverrideFile(t, `{not valid json}`)

	_, err := LoadBridgeExitsOverride(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse override file")
}

// TestLoadBridgeExitsOverride_MissingHeightsMap verifies that an absent "heights" key
// (nil map after unmarshal) is rejected.
func TestLoadBridgeExitsOverride_MissingHeightsMap(t *testing.T) {
	t.Parallel()

	path := writeOverrideFile(t, `{
		"network_id": 1,
		"description": "no heights key"
	}`)

	_, err := LoadBridgeExitsOverride(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "heights map is missing")
}
