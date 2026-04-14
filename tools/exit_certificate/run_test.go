package exit_certificate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestParseBlockNumber_Decimal(t *testing.T) {
	t.Parallel()
	require.Equal(t, uint64(12345), parseBlockNumber("12345"))
}

func TestParseBlockNumber_Hex(t *testing.T) {
	t.Parallel()
	require.Equal(t, uint64(255), parseBlockNumber("0xff"))
}

func TestSaveAndLoadJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testData := []string{"hello", "world"}

	saveJSON(dir, "test.json", testData)

	var loaded []string
	err := loadJSON(dir, "test.json", &loaded)
	require.NoError(t, err)
	require.Equal(t, testData, loaded)
}

func TestLoadJSON_FileNotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var target []string
	err := loadJSON(dir, "nonexistent.json", &target)
	require.Error(t, err)
}

func TestLoadJSON_InvalidJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("{bad}"), 0o600))

	var target map[string]string
	err := loadJSON(dir, "bad.json", &target)
	require.Error(t, err)
}

func TestCertificateJSON_ToAgglayerCertificate(t *testing.T) {
	t.Parallel()

	bridgeExitsJSON, _ := json.Marshal([]map[string]any{
		{
			"leaf_type": "Transfer",
			"token_info": map[string]any{
				"origin_network":       0,
				"origin_token_address": "0x0000000000000000000000000000000000000000",
			},
			"dest_network": 0,
			"dest_address": "0x1111111111111111111111111111111111111111",
			"amount":       "1000",
		},
	})

	certJSON := &certificateJSON{
		NetworkID:       1,
		BridgeExits:     bridgeExitsJSON,
	}

	cert := certJSON.toAgglayerCertificate()
	require.Equal(t, uint32(1), cert.NetworkID)
	require.Len(t, cert.BridgeExits, 1)
}

func TestCertificateJSON_EmptyBridgeExits(t *testing.T) {
	t.Parallel()

	certJSON := &certificateJSON{NetworkID: 1}
	cert := certJSON.toAgglayerCertificate()
	require.Equal(t, uint32(1), cert.NetworkID)
	require.Empty(t, cert.BridgeExits)
}

func TestSaveJSON_ComplexData(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	data := map[string]any{
		"address": common.HexToAddress("0x1234").Hex(),
		"balance": "1000000",
	}

	saveJSON(dir, "complex.json", data)

	content, err := os.ReadFile(filepath.Join(dir, "complex.json"))
	require.NoError(t, err)

	var loaded map[string]any
	require.NoError(t, json.Unmarshal(content, &loaded))
	require.Equal(t, "1000000", loaded["balance"])
}
