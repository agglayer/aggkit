package backward_forward_let

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
)

// BridgeExitsOverride holds pre-extracted certificate bridge exits keyed by height.
// Load via LoadBridgeExitsOverride. Use GetExits to retrieve exits for a specific height.
//
// NOTE: the JSON field names follow the Go agglayertypes.BridgeExit json tags
// (e.g., "dest_network", "dest_address"). The agglayer Rust serde may use different
// names (e.g., "destination_network"); if so, build the file by marshaling the
// Certificate.BridgeExits value obtained via json.Unmarshal from the admin API response,
// not from the raw Rust JSON text.
type BridgeExitsOverride struct {
	NetworkID   uint32
	Description string
	parsed      map[uint64][]*agglayertypes.BridgeExit
}

// GetExits returns the bridge exits for the given certificate height.
// The second return value is false when the height has no entry in the override.
func (o *BridgeExitsOverride) GetExits(height uint64) ([]*agglayertypes.BridgeExit, bool) {
	exits, ok := o.parsed[height]
	return exits, ok
}

// overrideFileJSON is the JSON wire format used when reading a BridgeExitsOverride file.
// Heights are string-keyed because JSON does not support integer object keys.
type overrideFileJSON struct {
	NetworkID   uint32                                 `json:"network_id"`
	Description string                                 `json:"description"`
	Heights     map[string][]*agglayertypes.BridgeExit `json:"heights"`
}

// LoadBridgeExitsOverride reads and validates a JSON override file containing
// pre-extracted certificate bridge exits keyed by certificate height.
//
// Expected file format (heights are string-keyed; amount is a decimal string):
//
//	{
//	  "network_id": 1,
//	  "description": "optional description",
//	  "heights": {
//	    "0": [
//	      {
//	        "leaf_type": 0,
//	        "token_info": { "origin_network": 0, "origin_token_address": "0x..." },
//	        "dest_network": 0,
//	        "dest_address": "0x...",
//	        "amount": "0",
//	        "metadata": null
//	      }
//	    ],
//	    "1": []
//	  }
//	}
//
// Returns an error when:
//   - the file cannot be read
//   - the JSON is malformed
//   - network_id is zero
//   - the heights map is absent
//   - any height key is not a non-negative integer
func LoadBridgeExitsOverride(filePath string) (*BridgeExitsOverride, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read override file %s: %w", filePath, err)
	}

	var raw overrideFileJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse override file %s: %w", filePath, err)
	}

	if raw.NetworkID == 0 {
		return nil, fmt.Errorf("override file %s: network_id must be non-zero", filePath)
	}

	if raw.Heights == nil {
		return nil, fmt.Errorf("override file %s: heights map is missing", filePath)
	}

	parsed := make(map[uint64][]*agglayertypes.BridgeExit, len(raw.Heights))
	for key, exits := range raw.Heights {
		h, parseErr := strconv.ParseUint(key, 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("override file %s: non-numeric height key %q: %w", filePath, key, parseErr)
		}
		parsed[h] = exits
	}

	return &BridgeExitsOverride{
		NetworkID:   raw.NetworkID,
		Description: raw.Description,
		parsed:      parsed,
	}, nil
}
