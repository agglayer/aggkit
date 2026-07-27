// Package domain holds the pure business rules of the bridge tracker: decisions that
// depend only on bridge facts, with no I/O and no dependency on the ports or adapters.
package domain

import (
	"github.com/agglayer/aggkit/bridgetracker/types"
)

// BridgeTypeFor derives the direction of a bridge from its origin and destination
// networks (0 -> Mainnet)
func BridgeTypeFor(originNetwork, destinationNetwork uint32) types.BridgeType {
	switch {
	case originNetwork == 0:
		return types.BridgeTypeL1ToL2
	case destinationNetwork == 0:
		return types.BridgeTypeL2ToL1
	default:
		return types.BridgeTypeL2ToL2
	}
}
