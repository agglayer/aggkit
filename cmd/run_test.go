package main

import (
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/stretchr/testify/require"
)

func TestShouldAutoStartL2ClaimSync(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		components []string
		want       bool
	}{
		{
			name:       "bridge only auto-starts (no aggsender bootstrap)",
			components: []string{aggkitcommon.BRIDGE},
			want:       true,
		},
		{
			name:       "l2bridgesync only auto-starts",
			components: []string{aggkitcommon.L2BRIDGESYNC},
			want:       true,
		},
		{
			name:       "aggsender + bridge does NOT auto-start (aggsender bootstraps; avoid block-0 race)",
			components: []string{aggkitcommon.AGGSENDER, aggkitcommon.AGGORACLE, aggkitcommon.BRIDGE},
			want:       false,
		},
		{
			name:       "aggsender-validator + bridge does NOT auto-start",
			components: []string{aggkitcommon.AGGSENDERVALIDATOR, aggkitcommon.BRIDGE},
			want:       false,
		},
		{
			name:       "aggchain-proof-gen + bridge does NOT auto-start",
			components: []string{aggkitcommon.AGGCHAINPROOFGEN, aggkitcommon.BRIDGE},
			want:       false,
		},
		{
			name:       "aggsender without bridge does not auto-start",
			components: []string{aggkitcommon.AGGSENDER},
			want:       false,
		},
		{
			name:       "no relevant components does not auto-start",
			components: []string{aggkitcommon.L2CLAIMSYNC},
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, shouldAutoStartL2ClaimSync(tt.components))
		})
	}
}
