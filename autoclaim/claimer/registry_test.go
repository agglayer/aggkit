package claimer

import (
	"context"
	"testing"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/stretchr/testify/require"
)

func TestNewRegistryNilClaimer(t *testing.T) {
	_, err := NewRegistry(nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "claimer is nil")
}

func TestNewRegistryDuplicateNetwork(t *testing.T) {
	c1 := stubClaimer(1)
	c2 := stubClaimer(1)
	_, err := NewRegistry(c1, c2)
	require.Error(t, err)
	require.ErrorContains(t, err, "duplicate autoclaim claimer destination network")
}

func TestClaimerForDestinationNilReceiver(t *testing.T) {
	var r *Registry
	c, ok, err := r.ClaimerForDestination(context.Background(), 1)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, c)
}

func TestClaimersNilReceiver(t *testing.T) {
	var r *Registry
	cs, err := r.Claimers(context.Background())
	require.NoError(t, err)
	require.Nil(t, cs)
}

func TestRegistryClaimers(t *testing.T) {
	c3 := stubClaimer(3)
	c1 := stubClaimer(1)
	c2 := stubClaimer(2)
	r, err := NewRegistry(c3, c1, c2)
	require.NoError(t, err)

	// ClaimerForDestination
	got, ok, err := r.ClaimerForDestination(context.Background(), 2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, c2, got)

	_, ok, err = r.ClaimerForDestination(context.Background(), 99)
	require.NoError(t, err)
	require.False(t, ok)

	// Claimers returns in destination-network order
	all, err := r.Claimers(context.Background())
	require.NoError(t, err)
	require.Equal(t, []autoclaimtypes.Claimer{c1, c2, c3}, all)
}

// stubClaimerImpl is a minimal Claimer that returns a target with the given destination network.
type stubClaimerImpl struct{ target autoclaimtypes.ClaimerTarget }

func (s *stubClaimerImpl) Target() autoclaimtypes.ClaimerTarget { return s.target }
func (s *stubClaimerImpl) IsClaimed(_ context.Context, _ autoclaimtypes.BridgeExit) (bool, error) {
	return false, nil
}
func (s *stubClaimerImpl) Enqueue(_ context.Context, _ autoclaimtypes.BridgeExit) error { return nil }
func (s *stubClaimerImpl) Advance(_ context.Context, _ autoclaimtypes.RequestKey) error { return nil }

func stubClaimer(destinationNetwork uint32) autoclaimtypes.Claimer {
	return &stubClaimerImpl{target: autoclaimtypes.ClaimerTarget{DestinationNetwork: destinationNetwork}}
}
