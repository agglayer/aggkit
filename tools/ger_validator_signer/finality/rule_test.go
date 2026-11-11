package finality

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRule_FinalBlock(t *testing.T) {
	tests := []struct {
		name      string
		rule      Rule
		tagged    uint64
		wantFinal uint64
		wantOK    bool
	}{
		{name: "no blocks to subtract", rule: Rule{Tag: "finalized"}, tagged: 100, wantFinal: 100, wantOK: true},
		{name: "head minus 6", rule: Rule{Tag: "latest", NumBlocks: 6}, tagged: 100, wantFinal: 94, wantOK: true},
		{name: "exactly NumBlocks: block 0 is final", rule: Rule{Tag: "latest", NumBlocks: 6}, tagged: 6, wantFinal: 0, wantOK: true},
		{name: "chain shorter than NumBlocks: nothing is final", rule: Rule{Tag: "latest", NumBlocks: 6}, tagged: 5, wantOK: false},
		{name: "genesis only", rule: Rule{Tag: "latest"}, tagged: 0, wantFinal: 0, wantOK: true},
		{name: "genesis only and blocks to subtract", rule: Rule{Tag: "latest", NumBlocks: 1}, tagged: 0, wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			final, ok := tc.rule.FinalBlock(tc.tagged)
			require.Equal(t, tc.wantOK, ok)
			require.Equal(t, tc.wantFinal, final)
		})
	}
}

func TestRule_IsFinal(t *testing.T) {
	rule := Rule{Tag: "latest", NumBlocks: 6}

	require.True(t, rule.IsFinal(94, 100), "the final block itself is final")
	require.True(t, rule.IsFinal(0, 100))
	require.False(t, rule.IsFinal(95, 100), "one block after the final one")
	require.False(t, rule.IsFinal(100, 100))
	require.False(t, rule.IsFinal(0, 5), "no block is final in a short chain")
}
