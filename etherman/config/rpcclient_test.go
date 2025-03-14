package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetString(t *testing.T) {
	cfg := RPCClientConfig{
		ExtraParams: map[string]interface{}{
			"key":         "value",
			"another_key": 1234,
		},
		URL:  "http://localhost:8123",
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
