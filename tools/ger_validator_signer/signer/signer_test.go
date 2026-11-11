package signer

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/config"
	"github.com/agglayer/go_signer/signer/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	testGER      = common.HexToHash("0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	errSignerKMS = errors.New("kms unavailable")
)

// goldenDir has the golden fixtures that define the wire contract with the AggOracle
var goldenDir = filepath.Join("..", "api", "testdata")

func testKey(t *testing.T, n byte) *ecdsa.PrivateKey {
	t.Helper()
	keyBytes := make([]byte, 32)
	keyBytes[31] = n
	key, err := crypto.ToECDSA(keyBytes)
	require.NoError(t, err)
	return key
}

// newSigner creates a Signer whose hash signer is the generated mock of go_signer
func newSigner(t *testing.T, key *ecdsa.PrivateKey) (*Signer, *mocks.HashSigner) {
	t.Helper()
	hashSigner := mocks.NewHashSigner(t)
	return NewFromHashSigner(hashSigner, crypto.PubkeyToAddress(key.PublicKey)), hashSigner
}

// signature returns what crypto.Sign returns: v is 0 or 1
func signature(t *testing.T, key *ecdsa.PrivateKey, ger common.Hash) []byte {
	t.Helper()
	sig, err := crypto.Sign(GERDigest(ger).Bytes(), key)
	require.NoError(t, err)
	return sig
}

func withV(sig []byte, v byte) []byte {
	out := append([]byte(nil), sig...)
	out[vIndex] = v
	return out
}

// highS returns the other valid signature of the same message: (r, n-s, v^1)
func highS(sig []byte) []byte {
	curveOrder := crypto.S256().Params().N
	s := new(big.Int).SetBytes(sig[32:64])
	out := append([]byte(nil), sig...)
	new(big.Int).Sub(curveOrder, s).FillBytes(out[32:64])
	out[vIndex] ^= 1
	return out
}

func TestGERDigest(t *testing.T) {
	// The contract verifies keccak256(abi.encodePacked(ger)): the hash of the 32 bytes, with no prefix
	require.Equal(t, crypto.Keccak256Hash(testGER.Bytes()), GERDigest(testGER))
	require.Equal(t, "0x52b3f53ff196a28e7d2d01283ef9427070bda64128fb5630b97b6ab17a8ff0a8", GERDigest(testGER).Hex())
}

func TestSigner_SignGER(t *testing.T) {
	key := testKey(t, 1)
	canonical := signature(t, key, testGER) // v 0 or 1
	expected := withV(canonical, canonical[vIndex]+27)

	tests := []struct {
		name     string
		returned []byte
	}{
		{name: "v as 0 or 1 (go-ethereum, local keystore)", returned: canonical},
		{name: "v as 27 or 28", returned: expected},
		{name: "s in the upper half, as a KMS can return it", returned: highS(canonical)},
		{name: "s in the upper half and v as 27 or 28", returned: withV(highS(canonical), highS(canonical)[vIndex]+27)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			signer, hashSigner := newSigner(t, key)
			hashSigner.EXPECT().SignHash(mock.Anything, GERDigest(testGER)).Return(tc.returned, nil).Once()

			got, err := signer.SignGER(context.Background(), testGER)
			require.NoError(t, err)
			require.Equal(t, expected, got, "always the canonical form: v 27/28 and a low s")
			require.Len(t, got, SignatureLength)
			require.Contains(t, []byte{27, 28}, got[vIndex])
		})
	}
}

// The signature of the golden fixture is the one that the validator produces with the private key 1
func TestSigner_GoldenFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(goldenDir, "sign_response.json"))
	require.NoError(t, err)
	var fixture struct {
		GER       common.Hash    `json:"ger"`
		Signature hexutil.Bytes  `json:"signature"`
		Signer    common.Address `json:"signer"`
	}
	require.NoError(t, json.Unmarshal(data, &fixture))

	key := testKey(t, 1)
	signer, hashSigner := newSigner(t, key)
	hashSigner.EXPECT().SignHash(mock.Anything, GERDigest(fixture.GER)).Return(signature(t, key, fixture.GER), nil).Once()

	got, err := signer.SignGER(context.Background(), fixture.GER)
	require.NoError(t, err)
	require.Equal(t, []byte(fixture.Signature), got)
	require.Equal(t, fixture.Signer, signer.Address())
}

func TestSigner_Address(t *testing.T) {
	key := testKey(t, 7)
	signer, _ := newSigner(t, key)
	require.Equal(t, crypto.PubkeyToAddress(key.PublicKey), signer.Address())
}

func TestSigner_SignGER_Failures(t *testing.T) {
	key := testKey(t, 1)
	canonical := signature(t, key, testGER)

	tests := []struct {
		name     string
		returned []byte
		err      error
		wantErr  string
	}{
		{name: "the signer fails", err: errSignerKMS, wantErr: "failed to sign the GER"},
		{name: "empty signature", returned: nil, wantErr: "signature of 0 bytes"},
		{name: "short signature", returned: canonical[:64], wantErr: "signature of 64 bytes"},
		{name: "long signature", returned: append(append([]byte(nil), canonical...), 0), wantErr: "signature of 66 bytes"},
		{name: "invalid v", returned: withV(canonical, 2), wantErr: "invalid v value: 2"},
		{name: "invalid v 29", returned: withV(canonical, 29), wantErr: "invalid v value: 29"},
		{name: "the signature is from another key", returned: signature(t, testKey(t, 2), testGER), wantErr: "not from the address of the signer"},
		{name: "the signature is of another message", returned: signature(t, key, common.HexToHash("0x99")), wantErr: "not from the address of the signer"},
		{name: "all zeros", returned: make([]byte, SignatureLength), wantErr: "not valid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			signer, hashSigner := newSigner(t, key)
			hashSigner.EXPECT().SignHash(mock.Anything, mock.Anything).Return(tc.returned, tc.err).Once()

			got, err := signer.SignGER(context.Background(), testGER)
			require.ErrorContains(t, err, tc.wantErr)
			require.Nil(t, got, "no signature must be returned when something is wrong")
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			}
		})
	}
}

func TestSigner_New_LocalKeystore(t *testing.T) {
	key := testKey(t, 5)
	keystorePath := writeKeystore(t, "testonly", key)

	signer, err := New(context.Background(), config.SignerConfig{
		"Method": "local", "Path": keystorePath, "Password": "testonly",
	}, 1337, testLogger())
	require.NoError(t, err)
	require.Equal(t, crypto.PubkeyToAddress(key.PublicKey), signer.Address())

	got, err := signer.SignGER(context.Background(), testGER)
	require.NoError(t, err)
	require.Equal(t, signer.Address(), recoverSigner(t, testGER, got))
}

func TestSigner_New_Failures(t *testing.T) {
	key := testKey(t, 5)
	keystorePath := writeKeystore(t, "testonly", key)

	t.Run("wrong password", func(t *testing.T) {
		_, err := New(context.Background(), config.SignerConfig{
			"Method": "local", "Path": keystorePath, "Password": "wrong",
		}, 1337, testLogger())
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to")
	})

	t.Run("missing keystore", func(t *testing.T) {
		_, err := New(context.Background(), config.SignerConfig{
			"Method": "local", "Path": filepath.Join(t.TempDir(), "missing"), "Password": "x",
		}, 1337, testLogger())
		require.Error(t, err)
	})

	t.Run("unknown method", func(t *testing.T) {
		_, err := New(context.Background(), config.SignerConfig{"Method": "carrier-pigeon"}, 1337, testLogger())
		require.ErrorContains(t, err, "failed to create the signer")
	})
}
