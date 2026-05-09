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

type bridgeExitsOverrideEnvelope struct {
	NetworkID    uint32          `json:"network_id"`
	Description  string          `json:"description"`
	Heights      json.RawMessage `json:"heights"`
	Certificates json.RawMessage `json:"certificates"`
}

type agglayerCertificatesFileJSON struct {
	NetworkID    uint32                     `json:"network_id"`
	Description  string                     `json:"description"`
	Certificates map[string]json.RawMessage `json:"certificates"`
}

// LoadBridgeExitsOverride reads and validates a JSON fallback file containing
// either raw AggLayer certificates or pre-extracted bridge exits keyed by
// certificate height.
//
// Preferred Aggkit override file format (heights are string-keyed; amount is a decimal string):
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
// AggLayer admin export format is also accepted. Each certificate value may be
// either a raw Certificate object, the raw admin_getCertificate JSON-RPC response,
// or the admin_getCertificate result pair [Certificate, CertificateHeader|null]:
//
//	{
//	  "network_id": 1,
//	  "description": "optional description",
//	  "certificates": {
//	    "42": {
//	      "jsonrpc": "2.0",
//	      "id": 1,
//	      "result": [{ "network_id": 1, "height": 42, "bridge_exits": [] }, null]
//	    }
//	  }
//	}
//
// Returns an error when:
//   - the file cannot be read
//   - the JSON is malformed
//   - network_id is zero
//   - neither heights nor certificates is present
//   - any height key is not a non-negative integer
//   - an AggLayer certificate entry has a mismatched network_id or height
func LoadBridgeExitsOverride(filePath string) (*BridgeExitsOverride, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read override file %s: %w", filePath, err)
	}

	var envelope bridgeExitsOverrideEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("parse override file %s: %w", filePath, err)
	}

	if len(envelope.Heights) > 0 {
		return loadAggkitBridgeExitsOverride(filePath, data)
	}
	if len(envelope.Certificates) > 0 {
		return loadAgglayerCertificatesOverride(filePath, data)
	}
	return nil, fmt.Errorf("override file %s: heights map is missing and certificates map is missing", filePath)
}

func loadAggkitBridgeExitsOverride(filePath string, data []byte) (*BridgeExitsOverride, error) {
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

func loadAgglayerCertificatesOverride(filePath string, data []byte) (*BridgeExitsOverride, error) {
	var raw agglayerCertificatesFileJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse agglayer certificates file %s: %w", filePath, err)
	}
	if raw.NetworkID == 0 {
		return nil, fmt.Errorf("agglayer certificates file %s: network_id must be non-zero", filePath)
	}
	if raw.Certificates == nil {
		return nil, fmt.Errorf("agglayer certificates file %s: certificates map is missing", filePath)
	}

	parsed := make(map[uint64][]*agglayertypes.BridgeExit, len(raw.Certificates))
	for key, certRaw := range raw.Certificates {
		height, parseErr := strconv.ParseUint(key, 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("agglayer certificates file %s: non-numeric height key %q: %w", filePath, key, parseErr)
		}
		cert, err := extractAgglayerCertificate(certRaw)
		if err != nil {
			return nil, fmt.Errorf("agglayer certificates file %s: height %d: %w", filePath, height, err)
		}
		if cert.NetworkID != raw.NetworkID {
			return nil, fmt.Errorf("agglayer certificates file %s: height %d certificate network_id %d does not match file network_id %d",
				filePath, height, cert.NetworkID, raw.NetworkID)
		}
		if cert.Height != height {
			return nil, fmt.Errorf("agglayer certificates file %s: height key %d does not match certificate height %d",
				filePath, height, cert.Height)
		}
		exits := cert.BridgeExits
		if exits == nil {
			exits = []*agglayertypes.BridgeExit{}
		}
		parsed[height] = exits
	}

	description := raw.Description
	if description == "" {
		description = "generated from agglayer admin_getCertificate responses"
	}
	return &BridgeExitsOverride{
		NetworkID:   raw.NetworkID,
		Description: description,
		parsed:      parsed,
	}, nil
}

func extractAgglayerCertificate(data json.RawMessage) (*agglayertypes.Certificate, error) {
	var rpcResponse struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &rpcResponse); err == nil {
		if len(rpcResponse.Error) > 0 && string(rpcResponse.Error) != "null" {
			return nil, fmt.Errorf("admin_getCertificate response contains error: %s", string(rpcResponse.Error))
		}
		if len(rpcResponse.Result) > 0 {
			return extractAgglayerCertificate(rpcResponse.Result)
		}
	}

	var pair [2]json.RawMessage
	if err := json.Unmarshal(data, &pair); err == nil && len(pair[0]) > 0 && string(pair[0]) != "null" {
		return extractAgglayerCertificate(pair[0])
	}

	var wrapped struct {
		Certificate json.RawMessage `json:"certificate"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && len(wrapped.Certificate) > 0 {
		return extractAgglayerCertificate(wrapped.Certificate)
	}

	var cert agglayertypes.Certificate
	if err := json.Unmarshal(data, &cert); err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	if cert.NetworkID == 0 {
		return nil, fmt.Errorf("certificate network_id must be non-zero")
	}
	return &cert, nil
}
