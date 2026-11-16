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

func TestRule_BlocksToFinal(t *testing.T) {
	tests := []struct {
		name   string
		rule   Rule
		block  uint64
		tagged uint64
		want   uint64
	}{
		{name: "already final", rule: Rule{Tag: TagFinalized}, block: 100, tagged: 100, want: 0},
		{name: "finalized behind", rule: Rule{Tag: TagFinalized}, block: 4305, tagged: 4290, want: 15},
		{name: "head minus 12", rule: Rule{Tag: TagLatest, NumBlocks: 12}, block: 4305, tagged: 4310, want: 7},
		{name: "chain shorter than NumBlocks", rule: Rule{Tag: TagLatest, NumBlocks: 12}, block: 3, tagged: 5, want: 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.rule.BlocksToFinal(tc.block, tc.tagged))
			require.Equal(t, tc.want == 0, tc.rule.IsFinal(tc.block, tc.tagged))
		})
	}
}

func TestRule_String(t *testing.T) {
	require.Equal(t, "FinalizedBlock", Rule{Tag: TagFinalized}.String())
	require.Equal(t, "SafeBlock", Rule{Tag: TagSafe}.String())
	require.Equal(t, "LatestBlock/-12", Rule{Tag: TagLatest, NumBlocks: 12}.String())
}
