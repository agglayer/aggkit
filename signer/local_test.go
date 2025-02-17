package signer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// To keep compatibility with previous version, a empty config file
// was a no privateKey (nil), so the idea is keep the same behavior
func TestNewKeyStoreFileConfigEmpty(t *testing.T) {
	cfg, err := NewKeyStoreFileConfig(SignerConfig{})
	require.NoError(t, err)
	require.Equal(t, "", cfg.Path)
	require.Equal(t, "", cfg.Password)
}
