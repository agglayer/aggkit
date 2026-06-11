package gertracker

// NewTestGERTracker creates a GERTracker with injected fakes, for use in unit tests only.
func NewTestGERTracker(l2GERManager L2GERManagerContract, l1InfoTreeSync L1InfoTreeSyncer) GERTracker {
	return &gerTracker{
		l2GERManager:   l2GERManager,
		l1InfoTreeSync: l1InfoTreeSync,
	}
}
