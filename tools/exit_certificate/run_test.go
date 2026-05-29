package exit_certificate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestParseStepList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{"single step", "f", []string{"f"}, false},
		{"comma list", "h, i, sign", []string{"h", "i", "sign"}, false},
		{"closed range", "f-i", []string{"f", "g", "h", "i"}, false},
		{"open range", "f-", []string{"f", "g", "h", "i", "sign"}, false},
		{"open range from sign", "sign-", []string{"sign"}, false},
		{"single-step range", "g-g", []string{"g"}, false},
		{"explicit range including submit", "sign-submit", []string{"sign", "submit"}, false},
		{"reversed range error", "i-f", nil, true},
		{"unknown from step", "z-i", nil, true},
		{"unknown to step", "f-z", nil, true},
		// Step A and B alias and sub-step expansion.
		{"a alias expands to a1 a2", "a", []string{"a1", "a2"}, false},
		{"b alias expands to b1 b2", "b", []string{"b1", "b2"}, false},
		{"a-b expands a to a1 a2 and b to b1 b2", "a-b", []string{"a1", "a2", "b1", "b2"}, false},
		{"a2-b range expands b to b1 b2", "a2-b", []string{"a2", "b1", "b2"}, false},
		{"b-c range expands b to b1 b2", "b-c", []string{"b1", "b2", "c"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseStepList(tc.input)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
		})
	}
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
		NetworkID:   1,
		BridgeExits: bridgeExitsJSON,
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
