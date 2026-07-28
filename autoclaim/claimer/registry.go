package claimer

import (
	"context"
	"fmt"
	"sort"

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

// Claimers returns all registered claimers in destination-network order.
func (r *Registry) Claimers(_ context.Context) ([]autoclaimtypes.Claimer, error) {
	if r == nil {
		return nil, nil
	}
	destinations := make([]uint32, 0, len(r.byDestination))
	for destination := range r.byDestination {
		destinations = append(destinations, destination)
	}
	sort.Slice(destinations, func(i, j int) bool {
		return destinations[i] < destinations[j]
	})

	claimers := make([]autoclaimtypes.Claimer, 0, len(destinations))
	for _, destination := range destinations {
		claimers = append(claimers, r.byDestination[destination])
	}
	return claimers, nil
}
