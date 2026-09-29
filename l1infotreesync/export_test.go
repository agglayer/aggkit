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
