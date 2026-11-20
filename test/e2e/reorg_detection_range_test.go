package e2e

import (
	"testing"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/stretchr/testify/require"
)

// TestReorgEventRangeProblem pins the acceptance rule of requireReorgEventWithin on the reorged blocks
// [10, 11], without any env: it is the pure reorgEventRangeProblem helper the smoke scenario asserts
// with. The legacy syncer reports the exact reorged range, so a reported range must lie within it; the
// multidownloader one only has to cover it, because its sparse rollback can widen FromBlock below the
// first reorged block and end ToBlock at the last stored block; and an inverted range (from_block above
// to_block) is rejected whatever the mode.
func TestReorgEventRangeProblem(t *testing.T) {
	// The reorged blocks every case below is about.
	const first, last = 10, 11

	tests := []struct {
		name     string
		event    bridgesync.LastReorg
		nativeMD bool
		problem  bool
	}{
		// Legacy syncer: the reported range must lie within the reorged blocks.
		{name: "legacy accepts the exact reorged range",
			event: bridgesync.LastReorg{FromBlock: 10, ToBlock: 11}},
		{name: "legacy accepts a range within the reorged blocks",
			event: bridgesync.LastReorg{FromBlock: 11, ToBlock: 11}},
		{name: "legacy rejects a range starting before the reorged blocks",
			event: bridgesync.LastReorg{FromBlock: 9, ToBlock: 11}, problem: true},
		{name: "legacy rejects a range ending after the reorged blocks",
			event: bridgesync.LastReorg{FromBlock: 10, ToBlock: 12}, problem: true},

		// Multidownloader syncer: the reported range only has to cover the reorged blocks.
		{name: "native accepts the exact reorged range",
			event: bridgesync.LastReorg{FromBlock: 10, ToBlock: 11}, nativeMD: true},
		{name: "native accepts a range covering the reorged blocks",
			event: bridgesync.LastReorg{FromBlock: 7, ToBlock: 15}, nativeMD: true},
		{name: "native rejects a range starting after the first reorged block",
			event: bridgesync.LastReorg{FromBlock: 11, ToBlock: 11}, nativeMD: true, problem: true},
		{name: "native rejects a range ending before the last reorged block",
			event: bridgesync.LastReorg{FromBlock: 10, ToBlock: 10}, nativeMD: true, problem: true},

		// Inverted range: rejected in both modes, even when the bounds happen to sit inside the reorg.
		{name: "legacy rejects an inverted range",
			event: bridgesync.LastReorg{FromBlock: 11, ToBlock: 10}, problem: true},
		{name: "native rejects an inverted range",
			event: bridgesync.LastReorg{FromBlock: 11, ToBlock: 10}, nativeMD: true, problem: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			problem := reorgEventRangeProblem(tc.event, first, last, tc.nativeMD)
			if tc.problem {
				require.NotEmpty(t, problem, "range %+v must be rejected", tc.event)
			} else {
				require.Empty(t, problem, "range %+v must be accepted", tc.event)
			}
		})
	}
}
