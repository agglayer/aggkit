package domain

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
)

// BridgeAddressResolver is the driven port behind GET /bridge-address[/{network_id}]: it
// resolves the bridge contract address for one network, and enumerates every network currently
// known, so "every network" can be answered without a fixed config list.
// bridgeservicefinder.Finder satisfies it directly (see sources.NetworkLister, which widens the
// same shape further for the activity endpoint).
type BridgeAddressResolver interface {
	// NetworkIDs returns the networkIDs of every network currently resolved
	NetworkIDs() []uint32
	// BridgeAddress returns the bridge contract address for networkID (see
	// bridgeservicefinder.Finder.BridgeAddress for the resolution/override rules)
	BridgeAddress(ctx context.Context, networkID uint32) (common.Address, error)
}
