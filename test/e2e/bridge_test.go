package e2e

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJustBridge(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	if _, err := exec.LookPath("cast"); err != nil {
		t.Skip("cast not found in PATH, skipping TestJustBridge")
	}
	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")
}
