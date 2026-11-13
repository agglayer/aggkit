package gervalidator

import (
	"crypto/ecdsa"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// goldenDir has the golden fixtures that define the wire contract of the REST API of the validator
// (ger_validator_signer). The validator module reads the same files.
var goldenDir = filepath.Join("..", "..", "tools", "ger_validator_signer", "api", "testdata")

var testGER = common.HexToHash("0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	return key
}

// signGER signs the GER as a validator does, returning v as 0 or 1 (as crypto.Sign does)
func signGER(t *testing.T, key *ecdsa.PrivateKey, ger common.Hash) []byte {
	t.Helper()
	signature, err := crypto.Sign(GERDigest(ger).Bytes(), key)
	require.NoError(t, err)
	return signature
}

// withV27 returns the signature with v as 27 or 28, as the contract expects it
func withV27(signature []byte) []byte {
	out := append([]byte(nil), signature...)
	out[vIndex] += 27
	return out
}

func TestGERDigest(t *testing.T) {
	// The AggOracleCommittee verifies keccak256(abi.encodePacked(ger)): the hash of the 32 bytes of the GER
	require.Equal(t, crypto.Keccak256Hash(testGER.Bytes()), GERDigest(testGER))
	require.NotEqual(t, testGER, GERDigest(testGER))
	// Not the EIP-191 digest of the GER
	require.NotEqual(t, accounts191Hash(testGER), GERDigest(testGER))
}

func accounts191Hash(ger common.Hash) common.Hash {
	prefixed := append([]byte("\x19Ethereum Signed Message:\n32"), ger.Bytes()...)
	return crypto.Keccak256Hash(prefixed)
}

func TestRecoverSigner_Valid(t *testing.T) {
	key := newKey(t)
	expectedSigner := crypto.PubkeyToAddress(key.PublicKey)
	signature := signGER(t, key, testGER)

	t.Run("v as 0 or 1 is normalized to 27 or 28", func(t *testing.T) {
		normalized, signer, err := RecoverSigner(testGER, signature)
		require.NoError(t, err)
		require.Equal(t, expectedSigner, signer)
		require.Equal(t, withV27(signature), normalized)
		require.Contains(t, []byte{27, 28}, normalized[vIndex])
		// The input is not modified
		require.Less(t, signature[vIndex], byte(2))
	})

	t.Run("v as 27 or 28 is kept", func(t *testing.T) {
		normalized, signer, err := RecoverSigner(testGER, withV27(signature))
		require.NoError(t, err)
		require.Equal(t, expectedSigner, signer)
		require.Equal(t, withV27(signature), normalized)
	})
}

func TestRecoverSigner_Invalid(t *testing.T) {
	key := newKey(t)
	signature := signGER(t, key, testGER)
	n := crypto.S256().Params().N

	maleable := func() []byte {
		// (r, n-s, v^1) is another valid signature of the same message, with s in the upper half
		s := new(big.Int).SetBytes(signature[rLength:vIndex])
		highS := new(big.Int).Sub(n, s)
		out := append([]byte(nil), signature...)
		copy(out[rLength:vIndex], make([]byte, sLength))
		highS.FillBytes(out[rLength:vIndex])
		out[vIndex] ^= 1
		return out
	}()

	withByte := func(index int, value byte) []byte {
		out := append([]byte(nil), signature...)
		out[index] = value
		return out
	}

	tests := []struct {
		name      string
		signature []byte
		wantErr   string
	}{
		{name: "empty", signature: nil, wantErr: "signature has 0 bytes"},
		{name: "too short", signature: signature[:64], wantErr: "signature has 64 bytes"},
		{name: "too long", signature: append(append([]byte(nil), signature...), 0), wantErr: "signature has 66 bytes"},
		{name: "v is 2", signature: withByte(vIndex, 2), wantErr: "invalid v value 2"},
		{name: "v is 26", signature: withByte(vIndex, 26), wantErr: "invalid v value 26"},
		{name: "v is 29", signature: withByte(vIndex, 29), wantErr: "invalid v value 29"},
		{name: "v is 255", signature: withByte(vIndex, 255), wantErr: "invalid v value 255"},
		{name: "s in the upper half (malleable)", signature: maleable, wantErr: "lower half of the curve order"},
		{name: "r is zero", signature: func() []byte {
			out := append([]byte(nil), signature...)
			copy(out[:rLength], make([]byte, rLength))
			return out
		}(), wantErr: "out of range"},
		{name: "s is zero", signature: func() []byte {
			out := append([]byte(nil), signature...)
			copy(out[rLength:vIndex], make([]byte, sLength))
			return out
		}(), wantErr: "out of range"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			normalized, signer, err := RecoverSigner(testGER, tc.signature)
			require.ErrorIs(t, err, ErrInvalidResponse)
			require.ErrorContains(t, err, tc.wantErr)
			require.Nil(t, normalized)
			require.Equal(t, common.Address{}, signer)
		})
	}
}

// A signature of another GER is valid, but recovers another address: the collector rejects it
// because the recovered address is not the validator's one
func TestRecoverSigner_OtherGER(t *testing.T) {
	// Fixed key and GERs, so the recovered address is always the same
	key, err := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000002")
	require.NoError(t, err)
	signature := signGER(t, key, testGER)

	_, signer, err := RecoverSigner(testGER, signature)
	require.NoError(t, err)
	require.Equal(t, crypto.PubkeyToAddress(key.PublicKey), signer)

	_, signer, err = RecoverSigner(common.HexToHash("0x02"), signature)
	require.NoError(t, err)
	require.NotEqual(t, crypto.PubkeyToAddress(key.PublicKey), signer,
		"the signature of another GER must not recover the validator address")
}

// The signature of the golden fixture is the one a validator with the private key 1 produces
func TestRecoverSigner_GoldenFixture(t *testing.T) {
	var resp SignResponse
	readGolden(t, "sign_response.json", &resp)

	normalized, signer, err := RecoverSigner(resp.GER, resp.Signature)
	require.NoError(t, err)
	require.Equal(t, resp.Signer, signer)
	require.Equal(t, common.HexToAddress("0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf"), signer)
	require.Equal(t, []byte(resp.Signature), normalized, "the fixture already has v as 27 or 28")

	// Deterministic signing (RFC 6979): the same key and GER give the same bytes
	key, err := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	require.NoError(t, err)
	require.Equal(t, hexutil.Bytes(withV27(signGER(t, key, resp.GER))), resp.Signature)
}

func TestPackSignatures(t *testing.T) {
	mk := func(addr string, fill byte) signedGER {
		signature := make([]byte, SignatureLength)
		for i := range signature {
			signature[i] = fill
		}
		return signedGER{signer: common.HexToAddress(addr), signature: signature}
	}
	low := mk("0x0000000000000000000000000000000000000001", 0x01)
	mid := mk("0x0000000000000000000000000000000000000100", 0x02)
	high := mk("0xffffffffffffffffffffffffffffffffffffffff", 0x03)

	t.Run("sorted in strictly ascending order of address", func(t *testing.T) {
		input := []signedGER{high, low, mid}
		packed, signers, err := packSignatures(input)
		require.NoError(t, err)
		require.Equal(t, []common.Address{low.signer, mid.signer, high.signer}, signers)
		require.Len(t, packed, 3*SignatureLength)
		require.Equal(t, low.signature, packed[:SignatureLength])
		require.Equal(t, mid.signature, packed[SignatureLength:2*SignatureLength])
		require.Equal(t, high.signature, packed[2*SignatureLength:])
		// The input order is not modified
		require.Equal(t, high.signer, input[0].signer)
	})

	t.Run("a repeated signer is an error", func(t *testing.T) {
		_, _, err := packSignatures([]signedGER{low, mid, low})
		require.ErrorContains(t, err, "is repeated")
	})

	t.Run("no signatures", func(t *testing.T) {
		packed, signers, err := packSignatures(nil)
		require.NoError(t, err)
		require.Empty(t, packed)
		require.Empty(t, signers)
	})
}

func readGolden(t *testing.T, name string, out any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(goldenDir, name))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, out))
}
