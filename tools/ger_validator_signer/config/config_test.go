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

[PublicREST]
TLSCertFile = "/app/tls/cert.pem"
TLSKeyFile = "/app/tls/key.pem"
`

// replaceInConfig returns the valid configuration with something replaced
func replaceInConfig(t *testing.T, old, replacement string) string {
	t.Helper()
	require.Contains(t, validConfig, old)
	return strings.Replace(validConfig, old, replacement, 1)
}

// inPublicREST returns the valid configuration with lines added to the [PublicREST] section
func inPublicREST(t *testing.T, lines string) string {
	t.Helper()
	return replaceInConfig(t, "[PublicREST]", "[PublicREST]\n"+lines)
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
	require.Equal(t, "/app/tls/cert.pem", cfg.PublicREST.TLSCertFile)
}

func TestParse_Defaults(t *testing.T) {
	cfg, err := Parse([]byte(validConfig))
	require.NoError(t, err)

	require.Equal(t, BlockFinality{Tag: FinalityFinalized}, cfg.BlockFinality, "the safest finality by default")
	require.Equal(t, LogConfig{Environment: LogEnvironmentDevelopment, Level: "info", Outputs: []string{"stderr"}}, cfg.Log,
		"the same as aggkit")
	require.Equal(t, 5*time.Second, cfg.L1RequestTimeout.Duration)
	require.Equal(t, PublicRESTConfig{
		Host:                      "0.0.0.0",
		Port:                      8080,
		ReadTimeout:               Duration{15 * time.Second},
		WriteTimeout:              Duration{20 * time.Second},
		MaxRequestsPerIPAndSecond: 20,
		MaxRequestBodyBytes:       4096,
		MaxRequestsBurstPerIP:     40,
		RequestTimeout:            Duration{15 * time.Second},
		TLSCertFile:               "/app/tls/cert.pem",
		TLSKeyFile:                "/app/tls/key.pem",
		InsecureHTTP:              false,
	}, cfg.PublicREST, "TLS is required by default")
	require.Equal(t, CacheConfig{MaxEntries: 10000, NegativeTTL: Duration{5 * time.Second}}, cfg.Cache)
}

func TestDefault_IsDefaultValues(t *testing.T) {
	cfg := Default()
	require.Equal(t, BlockFinality{Tag: FinalityFinalized}, cfg.BlockFinality)
	require.Empty(t, cfg.L1RPCURL, "the mandatory values have no default")
	require.Zero(t, cfg.L1ChainID)
	require.Nil(t, cfg.Signer)
	require.ErrorContains(t, cfg.Validate(), "L1RPCURL is required", "the default configuration is not enough")
}

func TestParse_OverridesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`
L1RPCURL = "http://localhost:8545"
L1ChainID = 1337
AgglayerGERAddr = "0x1111111111111111111111111111111111111111"
BlockFinality = "LatestBlock/-6"
[Log]
Environment = "production"
Level = "debug"
Outputs = ["stdout", "/var/log/validator.log"]
[Signer]
Method = "local"
[PublicREST]
InsecureHTTP = true
Port = 9090
ReadTimeout = "1s"
WriteTimeout = "3s"
RequestTimeout = "2s"
MaxRequestsPerIPAndSecond = 0
MaxRequestBodyBytes = 0
[Cache]
MaxEntries = 5
NegativeTTL = "30s"
`))
	require.NoError(t, err)
	require.Equal(t, BlockFinality{Tag: FinalityLatest, NumBlocks: 6}, cfg.BlockFinality)
	require.Equal(t, LogConfig{
		Environment: LogEnvironmentProduction, Level: "debug", Outputs: []string{"stdout", "/var/log/validator.log"},
	}, cfg.Log)
	require.True(t, cfg.PublicREST.InsecureHTTP)
	require.Equal(t, uint16(9090), cfg.PublicREST.Port)
	require.Equal(t, time.Second, cfg.PublicREST.ReadTimeout.Duration)
	require.Equal(t, 3*time.Second, cfg.PublicREST.WriteTimeout.Duration)
	require.Equal(t, 2*time.Second, cfg.PublicREST.RequestTimeout.Duration)
	require.Zero(t, cfg.PublicREST.MaxRequestsPerIPAndSecond, "0 is unlimited, as in aggkit")
	require.Equal(t, DefaultMaxRequestBodyBytes, cfg.PublicREST.RequestBodyLimit(), "0 is the default, as in aggkit")
	require.Equal(t, "0.0.0.0", cfg.PublicREST.Host, "the keys that are not in the file keep their default")
	require.Equal(t, 5, cfg.Cache.MaxEntries)
	require.Equal(t, 30*time.Second, cfg.Cache.NegativeTTL.Duration)
}

func TestNewBlockFinality(t *testing.T) {
	valid := map[string]BlockFinality{
		"FinalizedBlock":    {Tag: FinalityFinalized},
		"SafeBlock":         {Tag: FinalitySafe},
		"LatestBlock":       {Tag: FinalityLatest},
		"LatestBlock/-12":   {Tag: FinalityLatest, NumBlocks: 12},
		"FinalizedBlock/-2": {Tag: FinalityFinalized, NumBlocks: 2},
		"FinalizedBlock/0":  {Tag: FinalityFinalized},
		"finalizedblock":    {Tag: FinalityFinalized},
		"LatestBlock/-0x10": {Tag: FinalityLatest, NumBlocks: 16},
		"SAFEBLOCK/-1":      {Tag: FinalitySafe, NumBlocks: 1},
	}
	for text, want := range valid {
		t.Run(text, func(t *testing.T) {
			got, err := NewBlockFinality(text)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}

	invalid := map[string]string{
		"":                 "invalid block finality",
		"finalized":        "invalid block finality",
		"PendingBlock":     "invalid block finality",
		"123":              "invalid block finality",
		"LatestBlock/+5":   "the offset can't be positive",
		"FinalizedBlock/1": "the offset can't be positive",
		"LatestBlock/abc":  "invalid offset",
		"LatestBlock/-1/2": "invalid offset",
	}
	for text, wantErr := range invalid {
		t.Run("invalid "+text, func(t *testing.T) {
			_, err := NewBlockFinality(text)
			require.ErrorContains(t, err, wantErr)
		})
	}
}

func TestBlockFinality_String(t *testing.T) {
	for _, text := range []string{"FinalizedBlock", "SafeBlock/-3", "LatestBlock/-12"} {
		finality, err := NewBlockFinality(text)
		require.NoError(t, err)
		require.Equal(t, text, finality.String())
		marshalled, err := finality.MarshalText()
		require.NoError(t, err)
		require.Equal(t, text, string(marshalled))
	}
}

func TestParse_InvalidConfigurations(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr string
	}{
		{"unknown key", replaceInConfig(t, `L1ChainID = 11155111`, "L1ChainID = 11155111\nL1ChainIDD = 1"), "L1ChainIDD"},
		{"unknown key in a table", inPublicREST(t, "Portt = 1"), "Portt"},
		{"not toml", "this is not toml", "invalid configuration"},
		{"missing L1RPCURL", replaceInConfig(t, `L1RPCURL = "https://l1.example.com/some-token"`, ""), "L1RPCURL is required"},
		{"L1RPCURL without scheme", replaceInConfig(t, "https://l1.example.com/some-token", "l1.example.com"), "L1RPCURL must be an absolute"},
		{"L1RPCURL with another scheme", replaceInConfig(t, "https://l1.example.com/some-token", "ws://l1.example.com"), "L1RPCURL must be an absolute"},
		{"missing chain id", replaceInConfig(t, "L1ChainID = 11155111", ""), "L1ChainID is required"},
		{"missing AgglayerGER", replaceInConfig(t, `AgglayerGERAddr = "0x1111111111111111111111111111111111111111"`, ""), "AgglayerGERAddr is required"},
		{"invalid duration", replaceInConfig(t, `L1ChainID`, "L1RequestTimeout = \"soon\"\nL1ChainID"), "invalid duration"},
		{"zero L1 timeout", replaceInConfig(t, `L1ChainID`, "L1RequestTimeout = \"0s\"\nL1ChainID"), "L1RequestTimeout must be greater than 0"},
		{"invalid block finality", replaceInConfig(t, `L1ChainID`, "BlockFinality = \"PendingBlock\"\nL1ChainID"), "invalid block finality"},
		{"positive offset", replaceInConfig(t, `L1ChainID`, "BlockFinality = \"SafeBlock/+2\"\nL1ChainID"), "can't be positive"},
		{"invalid log environment", validConfig + "\n[Log]\nEnvironment = \"staging\"", "invalid Log.Environment"},
		{"invalid log level", validConfig + "\n[Log]\nLevel = \"trace\"", "invalid Log.Level"},
		{"no log outputs", validConfig + "\n[Log]\nOutputs = []", "Log.Outputs needs at least one output"},
		{"empty log output", validConfig + "\n[Log]\nOutputs = [\"\"]", "Log.Outputs has an empty output"},
		{"missing signer method", replaceInConfig(t, `Method = "local"`, ""), "Signer.Method is required"},
		{"signer method remote", replaceInConfig(t, `Method = "local"`, `Method = "remote"`), "can't sign a hash"},
		{"signer method none", replaceInConfig(t, `Method = "local"`, `Method = "none"`), `invalid Signer.Method "none"`},
		{"signer method mock", replaceInConfig(t, `Method = "local"`, `Method = "mock"`), `invalid Signer.Method "mock"`},
		{"TLS cert without key", replaceInConfig(t, `TLSKeyFile = "/app/tls/key.pem"`, ""), "must be set together"},
		{"TLS key without cert", replaceInConfig(t, `TLSCertFile = "/app/tls/cert.pem"`, ""), "must be set together"},
		{"no TLS and no explicit opt-in", replaceInConfig(t, "TLSCertFile = \"/app/tls/cert.pem\"\nTLSKeyFile = \"/app/tls/key.pem\"", ""), "TLS is required"},
		{"port zero", inPublicREST(t, "Port = 0"), "PublicREST.Port"},
		{"zero read timeout", inPublicREST(t, `ReadTimeout = "0s"`), "PublicREST.ReadTimeout"},
		{"zero request timeout", inPublicREST(t, `RequestTimeout = "0s"`), "PublicREST.RequestTimeout must be greater than 0"},
		{"write timeout not greater than the request one", inPublicREST(t, "WriteTimeout = \"15s\""), "PublicREST.WriteTimeout (15s) must be greater"},
		{"negative body limit", inPublicREST(t, "MaxRequestBodyBytes = -1"), "PublicREST.MaxRequestBodyBytes"},
		{"negative rate", inPublicREST(t, "MaxRequestsPerIPAndSecond = -1"), "PublicREST.MaxRequestsPerIPAndSecond"},
		{"rate without burst", inPublicREST(t, "MaxRequestsBurstPerIP = 0"), "PublicREST.MaxRequestsBurstPerIP"},
		{"no cache", validConfig + "\n[Cache]\nMaxEntries = 0", "Cache.MaxEntries"},
		{"negative negative TTL", validConfig + "\n[Cache]\nNegativeTTL = \"-1s\"", "Cache.NegativeTTL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.toml))
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// A configuration of the previous format says what each old key is now
func TestParse_RenamedKeys(t *testing.T) {
	_, err := Parse([]byte(`
L1RPCURL = "https://l1.example.com"
L1ChainID = 1
AgglayerGERAddr = "0x1111111111111111111111111111111111111111"
LogLevel = "debug"
[Finality]
Tag = "latest"
NumBlocks = 6
[Signer]
Method = "local"
[REST]
InsecureHTTP = true
MaxBodyBytes = 10
[RateLimit]
RequestsPerSecond = 1
`))
	require.ErrorContains(t, err, "LogLevel is now Level in the [Log] section, as in aggkit")
	require.ErrorContains(t, err, `BlockFinality = "LatestBlock/-12"`)
	require.ErrorContains(t, err, "the [REST] section is now [PublicREST]")
	require.ErrorContains(t, err, "PublicREST.MaxRequestsPerIPAndSecond")
	require.Equal(t, 1, strings.Count(err.Error(), "[PublicREST], as in aggkit"), "one hint per old key")

	_, err = Parse([]byte(replaceInConfig(t, `L1ChainID = 11155111`, "L1ChainID = 11155111\nL1ChainIDD = 1")))
	require.NotContains(t, err.Error(), "as in aggkit", "no hint for a key that is just unknown")
}

// The example configuration is documentation: it must be valid, and use only keys that exist
func TestExampleConfigIsValid(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "config.toml.example"))
	require.NoError(t, err)

	require.Equal(t, uint64(11155111), cfg.L1ChainID)
	require.Equal(t, BlockFinality{Tag: FinalityFinalized}, cfg.BlockFinality)
	require.Equal(t, SignerMethodLocal, cfg.Signer.Method())
	require.False(t, cfg.PublicREST.InsecureHTTP)
	require.NotEmpty(t, cfg.PublicREST.TLSCertFile)

	// The values of the example are the default ones, except the mandatory ones, the signer and the TLS files
	defaults := Default()
	require.Equal(t, defaults.L1RequestTimeout, cfg.L1RequestTimeout)
	require.Equal(t, defaults.Log, cfg.Log)
	rest := cfg.PublicREST
	rest.TLSCertFile, rest.TLSKeyFile = "", ""
	require.Equal(t, defaults.PublicREST, rest)
	require.Equal(t, defaults.Cache, cfg.Cache)
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
