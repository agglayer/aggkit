package converters

import (
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// ConvertToBridgeExit converts a bridgesync.Bridge instance into an agglayertypes.BridgeExit.
// It maps relevant fields such as leaf type, token information, destination details, amount, and metadata.
// Returns a pointer to the constructed agglayertypes.BridgeExit.
func ConvertToBridgeExit(bridge bridgesync.Bridge) *agglayertypes.BridgeExit {
	metaData := convertBridgeMetadata(bridge.Metadata)

	return &agglayertypes.BridgeExit{
		LeafType: bridgetypes.LeafType(bridge.LeafType),
		TokenInfo: &agglayertypes.TokenInfo{
			OriginNetwork:      bridge.OriginNetwork,
			OriginTokenAddress: bridge.OriginAddress,
		},
		DestinationNetwork: bridge.DestinationNetwork,
		DestinationAddress: bridge.DestinationAddress,
		Amount:             bridge.Amount,
		Metadata:           metaData,
	}
}

// ConvertToBridgeExits converts a slice of bridgesync.Bridge objects
// into a slice of pointers to agglayertypes.BridgeExit.
// It iterates over each Bridge in the input slice, applies the ConvertToBridgeExit method,
// and appends the result to the output slice.
// Returns a slice of pointers to BridgeExit corresponding to the input bridges.
func ConvertToBridgeExits(bridges []bridgesync.Bridge) []*agglayertypes.BridgeExit {
	bridgeExits := make([]*agglayertypes.BridgeExit, 0, len(bridges))

	for _, bridge := range bridges {
		bridgeExits = append(bridgeExits, ConvertToBridgeExit(bridge))
	}

	return bridgeExits
}

// convertBridgeMetadata converts the bridge metadata to a hash using crypto.Keccak256.
// If the metadata is empty, it returns an empty array hash
// which is basically Keccak256(nil) = 0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470.
func convertBridgeMetadata(metadata []byte) []byte {
	return crypto.Keccak256(metadata)
}
