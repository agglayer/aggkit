package backward_forward_let

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMakeFakeBridgeExits(t *testing.T) {
	t.Parallel()

	exits := makeFakeBridgeExits(2, 7, "test-nonce", big.NewInt(42))

	require.Len(t, exits, 2)
	require.Equal(t, big.NewInt(42), exits[0].Amount)
	require.NotEqual(t, exits[0].DestinationAddress, exits[1].DestinationAddress)
	require.Equal(t, exits[0].DestinationNetwork, exits[1].DestinationNetwork)
}

func TestMakeFakeBridgeExits_DeterministicWithNonce(t *testing.T) {
	t.Parallel()

	a := makeFakeBridgeExits(1, 1, "same", big.NewInt(0))
	b := makeFakeBridgeExits(1, 1, "same", big.NewInt(0))

	require.Equal(t, a[0].DestinationAddress, b[0].DestinationAddress)
}
