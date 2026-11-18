package l1infotreesync

// HaltForTest halts s's processor for reason, letting tests outside this package drive a real
// (non-mocked) syncer into the halted state without reaching into its private processor field.
func (s *L1InfoTreeSync) HaltForTest(reason string) {
	s.processor.halt(reason)
}

// UnhaltForTest reverses HaltForTest.
func (s *L1InfoTreeSync) UnhaltForTest() {
	s.processor.unhalt()
}

// OnHaltForTest registers fn to be called (without the processor lock held) every time the
// processor enters the halted state. It lets a test observe a halt deterministically: the halt is
// transient (the sync driver reorgs and unhalts within the same millisecond), so polling a public
// getter can miss it. Register it before starting the syncer.
func (s *L1InfoTreeSync) OnHaltForTest(fn func(reason string)) {
	s.processor.mu.Lock()
	defer s.processor.mu.Unlock()
	s.processor.haltedObserverForTest = fn
}
