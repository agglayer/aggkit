package envs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadEnv_InvalidEnvName(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env, err := LoadEnv(ctx, ENVName("non-existent-env"))
	require.Error(t, err, "LoadEnv should return an error for non-existent environment")
	require.Nil(t, env, "Env should be nil when error occurs")
}

func TestFindEnvsDir(t *testing.T) {
	// Test that FindEnvsDir works from current directory
	envsDir, err := FindEnvsDir()
	require.NoError(t, err, "FindEnvsDir should not return an error")
	require.NotEmpty(t, envsDir, "envs directory path should not be empty")

	// Verify the directory exists
	info, err := os.Stat(envsDir)
	require.NoError(t, err, "envs directory should exist")
	require.True(t, info.IsDir(), "envs path should be a directory")

	// Verify op-pp subdirectory exists
	opPPDir := filepath.Join(envsDir, string(EnvOpPP))
	info, err = os.Stat(opPPDir)
	require.NoError(t, err, "op-pp directory should exist")
	require.True(t, info.IsDir(), "op-pp path should be a directory")

	// Verify summary.json exists
	summaryPath := filepath.Join(opPPDir, "summary.json")
	_, err = os.Stat(summaryPath)
	require.NoError(t, err, "summary.json should exist")
}
