package multidownloader

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEVMMultidownloaderDebug(t *testing.T) {
	sut := NewEVMMultidownloaderDebug()

	sut.ForceReorg(123)
	err := sut.GetInjectedStartStepError()
	if err == nil {
		t.Fatalf("Expected error to be injected, got nil")
	}
	expectedMsg := "ForceReorg: forced reorg at block number 123"
	require.ErrorContains(t, err, expectedMsg)

	// After getting the error once, it should be cleared
	err = sut.GetInjectedStartStepError()
	require.NoError(t, err, "Expected error to be cleared after retrieval")
}
