package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const validConfig = `
L1RPCURL = "https://l1.example.com/some-token"
L1ChainID = 11155111
AgglayerGERAddr = "0x1111111111111111111111111111111111111111"

[Signer]
Method = "local"
Path = "/app/keystore/validator.keystore"
Password = "testonly"

[REST]
TLSCertFile = "/app/tls/cert.pem"
TLSKeyFile = "/app/tls/key.pem"
`

// replaceInConfig returns the valid configuration with something replaced
func replaceInConfig(t *testing.T, old, replacement string) string {
	t.Helper()
	require.Contains(t, validConfig, old)
	return strings.Replace(validConfig, old, replacement, 1)
}

func TestParse_Valid(t *testing.T) {
	cfg, err := Parse([]byte(validConfig))
	require.NoError(t, err)

	require.Equal(t, "https://l1.example.com/some-token", cfg.L1RPCURL)
	require.Equal(t, uint64(11155111), cfg.L1ChainID)
	require.Equal(t, common.HexToAddress("0x1111111111111111111111111111111111111111"), cfg.AgglayerGERAddr)
	require.Equal(t, "local", cfg.Signer.Method())
	require.Equal(t, map[string]any{"path": "/app/keystore/validator.keystore", "password": "testonly"}, cfg.Signer.Params(),
		"the keys are in lower case, as go_signer looks them up")
	require.Equal(t, "/app/tls/cert.pem", cfg.REST.TLSCertFile)
}

func TestParse_Defaults(t *testing.T) {
	cfg, err := Parse([]byte(validConfig))
	require.NoError(t, err)

	require.Equal(t, FinalityFinalized, cfg.Finality.Tag, "the safest finality by default")
	require.Zero(t, cfg.Finality.NumBlocks)
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, 5*time.Second, cfg.L1RequestTimeout.Duration)
	require.Equal(t, "0.0.0.0", cfg.REST.Host)
	require.Equal(t, uint16(8080), cfg.REST.Port)
	require.False(t, cfg.REST.InsecureHTTP, "TLS is required by default")
	require.Equal(t, 15*time.Second, cfg.REST.RequestTimeout.Duration)
	require.Equal(t, int64(4096), cfg.REST.MaxBodyBytes)
	require.Equal(t, 10000, cfg.Cache.MaxEntries)
	require.Equal(t, 5*time.Second, cfg.Cache.NegativeTTL.Duration)
	require.InDelta(t, 20.0, cfg.RateLimit.RequestsPerSecond, 0)
	require.Equal(t, 40, cfg.RateLimit.Burst)
}

func TestParse_OverridesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`
LogLevel = "debug"
L1RPCURL = "http://localhost:8545"
L1ChainID = 1337
AgglayerGERAddr = "0x1111111111111111111111111111111111111111"
[Finality]
Tag = "latest"
NumBlocks = 6
[Signer]
Method = "local"
[REST]
InsecureHTTP = true
Port = 9090
[Cache]
MaxEntries = 5
NegativeTTL = "30s"
[RateLimit]
RequestsPerSecond = 0
`))
	require.NoError(t, err)
	require.Equal(t, "debug", cfg.LogLevel)
	require.Equal(t, FinalityLatest, cfg.Finality.Tag)
	require.Equal(t, uint64(6), cfg.Finality.NumBlocks)
	require.True(t, cfg.REST.InsecureHTTP)
	require.Equal(t, uint16(9090), cfg.REST.Port)
	require.Equal(t, 5, cfg.Cache.MaxEntries)
	require.Equal(t, 30*time.Second, cfg.Cache.NegativeTTL.Duration)
	require.Zero(t, cfg.RateLimit.RequestsPerSecond, "0 disables the rate limit")
}

func TestParse_InvalidConfigurations(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr string
	}{
		{"unknown key", replaceInConfig(t, `L1ChainID = 11155111`, "L1ChainID = 11155111\nL1ChainIDD = 1"), "L1ChainIDD"},
		{"unknown key in a table", replaceInConfig(t, `Method = "local"`, "Method = \"local\"\n[Finality]\nTagg = \"latest\""), "Tagg"},
		{"not toml", "this is not toml", "invalid configuration"},
		{"missing L1RPCURL", replaceInConfig(t, `L1RPCURL = "https://l1.example.com/some-token"`, ""), "L1RPCURL is required"},
		{"L1RPCURL without scheme", replaceInConfig(t, "https://l1.example.com/some-token", "l1.example.com"), "L1RPCURL must be an absolute"},
		{"L1RPCURL with another scheme", replaceInConfig(t, "https://l1.example.com/some-token", "ws://l1.example.com"), "L1RPCURL must be an absolute"},
		{"missing chain id", replaceInConfig(t, "L1ChainID = 11155111", ""), "L1ChainID is required"},
		{"missing AgglayerGER", replaceInConfig(t, `AgglayerGERAddr = "0x1111111111111111111111111111111111111111"`, ""), "AgglayerGERAddr is required"},
		{"invalid log level", replaceInConfig(t, `L1ChainID`, "LogLevel = \"trace\"\nL1ChainID"), "invalid LogLevel"},
		{"invalid duration", replaceInConfig(t, `L1ChainID`, "L1RequestTimeout = \"soon\"\nL1ChainID"), "invalid duration"},
		{"zero L1 timeout", replaceInConfig(t, `L1ChainID`, "L1RequestTimeout = \"0s\"\nL1ChainID"), "L1RequestTimeout must be greater than 0"},
		{"missing signer method", replaceInConfig(t, `Method = "local"`, ""), "Signer.Method is required"},
		{"signer method remote", replaceInConfig(t, `Method = "local"`, `Method = "remote"`), "can't sign a hash"},
		{"signer method none", replaceInConfig(t, `Method = "local"`, `Method = "none"`), `invalid Signer.Method "none"`},
		{"signer method mock", replaceInConfig(t, `Method = "local"`, `Method = "mock"`), `invalid Signer.Method "mock"`},
		{"TLS cert without key", replaceInConfig(t, `TLSKeyFile = "/app/tls/key.pem"`, ""), "must be set together"},
		{"TLS key without cert", replaceInConfig(t, `TLSCertFile = "/app/tls/cert.pem"`, ""), "must be set together"},
		{"no TLS and no explicit opt-in", replaceInConfig(t, "TLSCertFile = \"/app/tls/cert.pem\"\nTLSKeyFile = \"/app/tls/key.pem\"", ""), "TLS is required"},
		{"port zero", replaceInConfig(t, `[REST]`, "[REST]\nPort = 0"), "REST.Port"},
		{"zero request timeout", replaceInConfig(t, `[REST]`, "[REST]\nRequestTimeout = \"0s\""), "REST.RequestTimeout"},
		{"zero body limit", replaceInConfig(t, `[REST]`, "[REST]\nMaxBodyBytes = 0"), "REST.MaxBodyBytes"},
		{"invalid finality tag", validConfig + "\n[Finality]\nTag = \"pending\"", "invalid Finality.Tag"},
		{"negative blocks", validConfig + "\n[Finality]\nNumBlocks = -1", "invalid configuration"},
		{"no cache", validConfig + "\n[Cache]\nMaxEntries = 0", "Cache.MaxEntries"},
		{"negative negative TTL", validConfig + "\n[Cache]\nNegativeTTL = \"-1s\"", "Cache.NegativeTTL"},
		{"negative rate", validConfig + "\n[RateLimit]\nRequestsPerSecond = -1", "RateLimit.RequestsPerSecond"},
		{"rate without burst", validConfig + "\n[RateLimit]\nBurst = 0", "RateLimit.Burst"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.toml))
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// The example configuration is documentation: it must be valid, and use only keys that exist
func TestExampleConfigIsValid(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "config.toml.example"))
	require.NoError(t, err)

	require.Equal(t, uint64(11155111), cfg.L1ChainID)
	require.Equal(t, FinalityFinalized, cfg.Finality.Tag)
	require.Equal(t, SignerMethodLocal, cfg.Signer.Method())
	require.False(t, cfg.REST.InsecureHTTP)
	require.NotEmpty(t, cfg.REST.TLSCertFile)

	// The values of the example are the default ones, except the mandatory ones and the signer
	defaults := Default()
	require.Equal(t, defaults.L1RequestTimeout, cfg.L1RequestTimeout)
	require.Equal(t, defaults.REST.Port, cfg.REST.Port)
	require.Equal(t, defaults.REST.RequestTimeout, cfg.REST.RequestTimeout)
	require.Equal(t, defaults.REST.MaxBodyBytes, cfg.REST.MaxBodyBytes)
	require.Equal(t, defaults.Cache, cfg.Cache)
	require.Equal(t, defaults.RateLimit, cfg.RateLimit)
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(validConfig), 0o600))
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, uint64(11155111), cfg.L1ChainID)

	_, err = Load(filepath.Join(t.TempDir(), "missing.toml"))
	require.ErrorContains(t, err, "failed to read the configuration file")
}

func TestSignerConfig(t *testing.T) {
	signer := SignerConfig{"method": "AWS", "KeyName": "k", "Region": "eu-west-1"}
	require.Equal(t, "AWS", signer.Method(), "the key Method is case insensitive")
	require.Equal(t, map[string]any{"keyname": "k", "region": "eu-west-1"}, signer.Params())

	require.Empty(t, SignerConfig{}.Method())
	require.Empty(t, SignerConfig(nil).Method())
	require.Empty(t, SignerConfig{"Method": 3}.Method(), "a Method that is not a string is no method")
}

func TestMaskURL(t *testing.T) {
	tests := map[string]string{
		"https://l1.example.com":                       "https://l1.example.com",
		"https://l1.example.com/":                      "https://l1.example.com",
		"https://l1.example.com/v1/secret-token":       "https://l1.example.com/***",
		"https://l1.example.com:8545/secret":           "https://l1.example.com:8545/***",
		"https://user:pass@l1.example.com":             "https://l1.example.com/***",
		"https://l1.example.com/?apikey=secret":        "https://l1.example.com/***",
		"http://localhost:8545":                        "http://localhost:8545",
		"not a url":                                    "***",
		"":                                             "***",
		"https://l1.example.com/path/with/many/tokens": "https://l1.example.com/***",
	}
	for input, expected := range tests {
		masked := MaskURL(input)
		require.Equal(t, expected, masked, input)
		require.NotContains(t, masked, "secret")
		require.NotContains(t, masked, "pass")
	}
}

func TestDuration(t *testing.T) {
	var d Duration
	require.NoError(t, d.UnmarshalText([]byte("1m30s")))
	require.Equal(t, 90*time.Second, d.Duration)

	text, err := d.MarshalText()
	require.NoError(t, err)
	require.Equal(t, "1m30s", string(text))

	require.ErrorContains(t, d.UnmarshalText([]byte("abc")), `invalid duration "abc"`)
	require.Equal(t, 90*time.Second, d.Duration, "not modified on error")
}
