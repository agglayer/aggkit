package exit_certificate

import (
	"context"
	"encoding/json"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/stretchr/testify/require"
)

func TestRunStepSignRequiresMethod(t *testing.T) {
	t.Parallel()
	_, err := RunStepSign(context.Background(), &Config{}, &agglayertypes.Certificate{})
	require.ErrorContains(t, err, "signerConfig.Method is required")
}

func TestRunStepSignLocalKeystore(t *testing.T) {
	t.Parallel()
	// Generate a real go-ethereum keystore the local signer can load.
	ks := keystore.NewKeyStore(t.TempDir(), keystore.LightScryptN, keystore.LightScryptP)
	const pass = "test-password"
	acc, err := ks.NewAccount(pass)
	require.NoError(t, err)

	srv := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		require.Equal(t, "eth_chainId", method)
		return quoted("0x1"), nil
	})

	cfg := &Config{
		L2RPCURL: srv.URL,
		SignerConfig: signertypes.SignerConfig{
			Method: "local",
			Config: map[string]any{"path": acc.URL.Path, "password": pass},
		},
	}

	signed, err := RunStepSign(context.Background(), cfg, &agglayertypes.Certificate{NetworkID: 1})
	require.NoError(t, err)
	require.NotNil(t, signed.AggchainData)
	multisig, ok := signed.AggchainData.(*agglayertypes.AggchainDataMultisig)
	require.True(t, ok)
	require.Len(t, multisig.Multisig.Signatures, 1)
}
