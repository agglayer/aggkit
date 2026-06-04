package claimer

import (
	"context"
	"fmt"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
)

var _ autoclaimtypes.ClaimerRegistry = (*Registry)(nil)

// Registry resolves enabled claimers by destination network.
type Registry struct {
	byDestination map[uint32]autoclaimtypes.Claimer
}

// NewRegistry creates a destination-network claimer registry.
func NewRegistry(claimers ...autoclaimtypes.Claimer) (*Registry, error) {
	registry := &Registry{
		byDestination: make(map[uint32]autoclaimtypes.Claimer, len(claimers)),
	}
	for _, claimer := range claimers {
		if claimer == nil {
			return nil, fmt.Errorf("autoclaim registry claimer is nil")
		}
		target := claimer.Target()
		if _, ok := registry.byDestination[target.DestinationNetwork]; ok {
			return nil, fmt.Errorf("duplicate autoclaim claimer destination network: %d", target.DestinationNetwork)
		}
		registry.byDestination[target.DestinationNetwork] = claimer
	}

	return registry, nil
}

// ClaimerForDestination returns the claimer responsible for destinationNetwork.
func (r *Registry) ClaimerForDestination(
	_ context.Context,
	destinationNetwork uint32,
) (autoclaimtypes.Claimer, bool, error) {
	if r == nil {
		return nil, false, nil
	}
	claimer, ok := r.byDestination[destinationNetwork]
	return claimer, ok, nil
}
