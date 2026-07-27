package domain

import (
	"testing"

	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/stretchr/testify/require"
)

func TestBridgeTypeFor(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name               string
		originNetwork      uint32
		destinationNetwork uint32
		expected           types.BridgeType
	}{
		{
			name:               "L1 to L2",
			originNetwork:      0,
			destinationNetwork: 1,
			expected:           types.BridgeTypeL1ToL2,
		},
		{
			name:               "L2 to L1",
			originNetwork:      1,
			destinationNetwork: 0,
			expected:           types.BridgeTypeL2ToL1,
		},
		{
			name:               "L2 to L2",
			originNetwork:      1,
			destinationNetwork: 2,
			expected:           types.BridgeTypeL2ToL2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.expected, BridgeTypeFor(tc.originNetwork, tc.destinationNetwork))
		})
	}
}
