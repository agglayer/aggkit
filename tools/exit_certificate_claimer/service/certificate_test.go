package claimer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const sampleSignedCertificate = `{
  "network_id": 1,
  "new_local_exit_root": "0x8a644096ff45bf6efc0057b1f42dc52cdbf7bd098f9154f9f72c5fd270a8c519",
  "bridge_exits": [
    {
      "leaf_type": "Transfer",
      "token_info": {
        "origin_network": 0,
        "origin_token_address": "0x0000000000000000000000000000000000000000"
      },
      "dest_network": 0,
      "dest_address": "0x0b68058e5b2592b1f472adfe106305295a332a7c",
      "amount": "20000005400000000",
      "metadata": "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"
    },
    {
      "leaf_type": "Transfer",
      "token_info": {
        "origin_network": 0,
        "origin_token_address": "0x62bf798edae1b7fde524276864757cc424a5c3dd"
      },
      "dest_network": 0,
      "dest_address": "0x85da99c8a7c2c95964c8efd687e95e632fc533d6",
      "amount": "100000000000000000",
      "metadata": "0c9cd205d5953a2e073bcc4e1dbb0996d17f6e5d820c69b2d16ae1142d2b004f"
    }
  ],
  "l1_info_tree_leaf_count": 10
}`

func writeSampleCert(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "exit-certificate-signed.json")
	require.NoError(t, os.WriteFile(path, []byte(sampleSignedCertificate), 0o600))
	return path
}

func TestLoadCertificate(t *testing.T) {
	t.Parallel()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)

	require.Equal(t, uint32(1), cert.NetworkID)
	require.Equal(t,
		common.HexToHash("0x8a644096ff45bf6efc0057b1f42dc52cdbf7bd098f9154f9f72c5fd270a8c519"),
		cert.NewLocalExitRoot)
	require.Len(t, cert.Leaves, 2)

	first := cert.Leaves[0]
	require.Equal(t, leafTypeAsset, first.LeafType)
	require.Equal(t, uint32(0), first.OriginNetwork)
	require.Equal(t, common.Address{}, first.OriginTokenAddress)
	require.Equal(t, uint32(0), first.DestinationNetwork)
	require.Equal(t,
		common.HexToAddress("0x0b68058e5b2592b1f472adfe106305295a332a7c"),
		first.DestinationAddress)
	require.Equal(t, "20000005400000000", first.Amount.String())
	require.Len(t, first.MetadataHash, 32)

	// Leaf hashes must be deterministic and distinct.
	require.NotEqual(t, first.Hash(), cert.Leaves[1].Hash())
}

func TestLoadCertificateErrors(t *testing.T) {
	t.Parallel()

	_, err := LoadCertificate(filepath.Join(t.TempDir(), "missing.json"))
	require.Error(t, err)

	badPath := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(badPath, []byte(`{"bridge_exits":[{"leaf_type":"Nope"}]}`), 0o600))
	_, err = LoadCertificate(badPath)
	require.ErrorContains(t, err, "unknown leaf_type")
}

func TestParseMetadata(t *testing.T) {
	t.Parallel()

	empty, err := parseMetadata("")
	require.NoError(t, err)
	require.Empty(t, empty)
	require.NotNil(t, empty)

	withPrefix, err := parseMetadata("0xabcd")
	require.NoError(t, err)
	require.Equal(t, []byte{0xab, 0xcd}, withPrefix)

	withoutPrefix, err := parseMetadata("abcd")
	require.NoError(t, err)
	require.Equal(t, []byte{0xab, 0xcd}, withoutPrefix)

	_, err = parseMetadata("0xzz")
	require.Error(t, err)
}
