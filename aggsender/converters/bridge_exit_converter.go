package converters

import (
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/crypto"
)

// BridgeExitConverter is a struct that provides methods for converting or processing
// data related to bridge exit operations within the application. It serves as a
// placeholder for implementing conversion logic specific to bridge exit events.
type BridgeExitConverter struct{}

// NewBridgeExitConverter initializes and returns a new instance of BridgeExitConverter.
func NewBridgeExitConverter() *BridgeExitConverter {
	return &BridgeExitConverter{}
}

// ConvertToBridgeExit converts a bridgesync.Bridge instance into an agglayertypes.BridgeExit.
// It maps relevant fields such as leaf type, token information, destination details, amount, and metadata.
// Returns a pointer to the constructed agglayertypes.BridgeExit.
func (c *BridgeExitConverter) ConvertToBridgeExit(bridge bridgesync.Bridge) *agglayertypes.BridgeExit {
	metaData := convertBridgeMetadata(bridge.Metadata)

	return &agglayertypes.BridgeExit{
		LeafType: agglayertypes.LeafType(bridge.LeafType),
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
func (c *BridgeExitConverter) ConvertToBridgeExits(bridges []bridgesync.Bridge) []*agglayertypes.BridgeExit {
	bridgeExits := make([]*agglayertypes.BridgeExit, 0, len(bridges))

	for _, bridge := range bridges {
		bridgeExits = append(bridgeExits, c.ConvertToBridgeExit(bridge))
	}

	return bridgeExits
}

// convertBridgeMetadata converts the bridge metadata to a hash using crypto.Keccak256.
// If the metadata is empty, it returns nil (the zero value for a slice in Go).
// Note: The "previous flag" is no longer returned by this function.
func convertBridgeMetadata(metadata []byte) []byte {
	var metaData []byte

	if len(metadata) > 0 {
		metaData = crypto.Keccak256(metadata)
	}

	return metaData
}
