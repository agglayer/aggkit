package multidownloader

import "testing"

func TestEVMMultidownloaderDebug(t *testing.T) {
	sut := NewEVMMultidownloaderDebug()

	sut.ForceRorg(123)
	err := sut.GetInjectedStartStepError()
	if err == nil {
		t.Fatalf("Expected error to be injected, got nil")
	}
	expectedMsg := "ForceRorg: forced reorg at block number 123"
	if err.Error() != expectedMsg {
		t.Fatalf("Expected error message '%s', got '%s'", expectedMsg, err.Error())
	}

	// After getting the error once, it should be cleared
	err = sut.GetInjectedStartStepError()
	if err != nil {
		t.Fatalf("Expected error to be cleared after retrieval, got '%s'", err.Error())
	}
}
