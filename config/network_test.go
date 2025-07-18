package config

import (
	"testing"

	"github.com/agglayer/aggkit/config/types"
	"github.com/stretchr/testify/require"
)

func TestGetString(t *testing.T) {
	cfg := L2RPCClientConfig{
		RPCClientConfig: RPCClientConfig{
			URL:            "http://localhost:8123",
			MaxRetries:     3,
			InitialBackoff: types.Duration{Duration: 1000},
		},
		ExtraParams: map[string]any{
			"key":         "value",
			"another_key": 1234,
		},
		Mode: RPCModeBasic,
	}
	value, err := cfg.GetString("key")
	require.NoError(t, err)
	require.Equal(t, "value", value)
	_, err = cfg.GetString("another_key")
	require.Error(t, err)
	_, err = cfg.GetString("dont_exists_key")
	require.Error(t, err)
}
