package signer

import (
	"crypto/ecdsa"
	"io"
	"log/slog"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// writeKeystore imports the key in a new keystore, as the local signer of go_signer reads it, and returns the
// path of the keystore file
func writeKeystore(t *testing.T, password string, key *ecdsa.PrivateKey) string {
	t.Helper()
	// light scrypt parameters: it is a test keystore
	store := keystore.NewKeyStore(t.TempDir(), keystore.LightScryptN, keystore.LightScryptP)
	account, err := store.ImportECDSA(key, password)
	require.NoError(t, err)
	return account.URL.Path
}

// recoverSigner returns the address that signed the GER. The signature has v as 27 or 28
func recoverSigner(t *testing.T, ger common.Hash, signature []byte) common.Address {
	t.Helper()
	ethSignature := append([]byte(nil), signature...)
	ethSignature[vIndex] -= recoveryIDOffset
	pubKey, err := crypto.Ecrecover(GERDigest(ger).Bytes(), ethSignature)
	require.NoError(t, err)
	return common.BytesToAddress(crypto.Keccak256(pubKey[1:])[12:])
}
