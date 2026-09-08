package bridgelooptester

import (
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	cfgtypes "github.com/agglayer/aggkit/config/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// validNetwork builds a minimally-complete, valid Network for tests.
func validNetwork(id uint32, name string) Network {
	return Network{
		NetworkID:  id,
		Name:       name,
		RPCURL:     fmt.Sprintf("http://127.0.0.1:1000%d", id),
		BridgeAddr: common.BigToAddress(big.NewInt(int64(id) + 1)),
		Signer: signertypes.SignerConfig{
			Method: signertypes.MethodLocal,
			Config: map[string]any{"path": "/keystore", "password": "changeme"},
		},
	}
}

// validConfig builds a fresh, fully valid 3-network, single-loop closed-ring Config for every test
// to mutate independently (no shared state between test cases).
func validConfig() *Config {
	return &Config{
		Global: Global{
			ProxyURL:          "http://127.0.0.1:15601",
			LogLevel:          "info",
			LoopDelay:         cfgtypes.NewDuration(5 * time.Second),
			HopTimeout:        cfgtypes.NewDuration(10 * time.Minute),
			PollInterval:      cfgtypes.NewDuration(5 * time.Second),
			ManualGracePeriod: cfgtypes.NewDuration(2 * time.Minute),
			HopAttempts:       3,
		},
		Networks: []Network{
			validNetwork(0, "L1"),
			validNetwork(1, "L2A"),
			validNetwork(2, "L2B"),
		},
		Loops: []Loop{
			{
				Name:    "ring",
				Asset:   AssetETH,
				Amount:  NewWeiAmount(1_000_000),
				Enabled: true,
				Hops: []Hop{
					{Source: 0, Destination: 1, Claim: ClaimAuto},
					{Source: 1, Destination: 2, Claim: ClaimManual},
					{Source: 2, Destination: 0, Claim: ClaimAuto},
				},
			},
		},
	}
}

func TestConfig_Validate_ValidMultiLoopConfig(t *testing.T) {
	cfg := validConfig()

	// Add a second, ERC20 loop over the same ring in the opposite direction, so the valid fixture
	// also covers a multi-loop config mixing asset kinds and claim modes, as the acceptance
	// criteria call for.
	originNetwork := uint32(1)
	cfg.Loops = append(cfg.Loops, Loop{
		Name:               "erc20-ring",
		Asset:              AssetERC20,
		Amount:             NewWeiAmount(2_000_000),
		Enabled:            true,
		TokenOriginNetwork: &originNetwork,
		Hops: []Hop{
			{Source: 1, Destination: 2, Claim: ClaimManual},
			{Source: 2, Destination: 0, Claim: ClaimAuto},
			{Source: 0, Destination: 1, Claim: ClaimManual},
		},
	})

	err := cfg.Validate()
	require.NoError(t, err)
	// 3 networks participate and every one of L1->L2, L2->L1, L2->L2 is exercised by the two rings,
	// so there should be no coverage warning.
	require.Empty(t, cfg.Warnings())
}

func TestConfig_Validate_TableDriven(t *testing.T) {
	testCases := []struct {
		name      string
		mutate    func(cfg *Config)
		wantError string
	}{
		{
			name:      "hop source has no matching network",
			mutate:    func(cfg *Config) { cfg.Loops[0].Hops[0].Source = 99 },
			wantError: "Source 99 has no matching [[Networks]] entry",
		},
		{
			name:      "hop destination has no matching network",
			mutate:    func(cfg *Config) { cfg.Loops[0].Hops[0].Destination = 99 },
			wantError: "Destination 99 has no matching [[Networks]] entry",
		},
		{
			name:      "hop chain is not contiguous",
			mutate:    func(cfg *Config) { cfg.Loops[0].Hops[1].Source = 0 },
			wantError: "hop chain is not contiguous",
		},
		{
			name: "route is not closed (deliberately non-closed ring)",
			mutate: func(cfg *Config) {
				cfg.Loops[0].Hops[len(cfg.Loops[0].Hops)-1].Destination = 1
			},
			wantError: "route is not closed",
		},
		{
			name:      "hop source equals destination",
			mutate:    func(cfg *Config) { cfg.Loops[0].Hops[0].Destination = cfg.Loops[0].Hops[0].Source },
			wantError: "Source and Destination must differ",
		},
		{
			name:      "duplicate NetworkID",
			mutate:    func(cfg *Config) { cfg.Networks[1].NetworkID = cfg.Networks[0].NetworkID },
			wantError: "duplicate NetworkID",
		},
		{
			name:      "loop amount not positive",
			mutate:    func(cfg *Config) { cfg.Loops[0].Amount = NewWeiAmount(0) },
			wantError: "Amount must be greater than 0",
		},
		{
			name:      "Global.LoopDelay not positive",
			mutate:    func(cfg *Config) { cfg.Global.LoopDelay = cfgtypes.NewDuration(0) },
			wantError: "Global.LoopDelay must be greater than 0",
		},
		{
			name:      "Global.HopTimeout not positive",
			mutate:    func(cfg *Config) { cfg.Global.HopTimeout = cfgtypes.NewDuration(-time.Second) },
			wantError: "Global.HopTimeout must be greater than 0",
		},
		{
			name:      "Global.PollInterval not positive",
			mutate:    func(cfg *Config) { cfg.Global.PollInterval = cfgtypes.NewDuration(0) },
			wantError: "Global.PollInterval must be greater than 0",
		},
		{
			name:      "Global.ManualGracePeriod not positive",
			mutate:    func(cfg *Config) { cfg.Global.ManualGracePeriod = cfgtypes.NewDuration(0) },
			wantError: "Global.ManualGracePeriod must be greater than 0",
		},
		{
			name:      "Global.HopAttempts not positive",
			mutate:    func(cfg *Config) { cfg.Global.HopAttempts = 0 },
			wantError: "Global.HopAttempts must be greater than 0",
		},
		{
			name:      "invalid claim mode",
			mutate:    func(cfg *Config) { cfg.Loops[0].Hops[0].Claim = ClaimMode("sometimes") },
			wantError: `Claim must be "auto" or "manual", got "sometimes"`,
		},
		{
			name:      "ProxyURL does not parse",
			mutate:    func(cfg *Config) { cfg.Global.ProxyURL = "://bad-url" },
			wantError: "does not parse as a URL",
		},
		{
			name:      "ProxyURL missing scheme/host",
			mutate:    func(cfg *Config) { cfg.Global.ProxyURL = "justastring" },
			wantError: "must be an absolute URL with a scheme and host",
		},
		{
			name:      "ProxyURL empty",
			mutate:    func(cfg *Config) { cfg.Global.ProxyURL = "" },
			wantError: "Global.ProxyURL is required",
		},
		{
			name:      "erc20 loop missing TokenOriginNetwork",
			mutate:    func(cfg *Config) { cfg.Loops[0].Asset = AssetERC20 },
			wantError: `TokenOriginNetwork is required for Asset = "erc20"`,
		},
		{
			name: "eth loop must not set TokenOriginNetwork",
			mutate: func(cfg *Config) {
				n := uint32(1)
				cfg.Loops[0].TokenOriginNetwork = &n
			},
			wantError: `TokenOriginNetwork must not be set for Asset = "eth"`,
		},
		{
			name:      "invalid asset kind",
			mutate:    func(cfg *Config) { cfg.Loops[0].Asset = AssetKind("btc") },
			wantError: `Asset must be "eth" or "erc20", got "btc"`,
		},
		{
			name:      "too few hops",
			mutate:    func(cfg *Config) { cfg.Loops[0].Hops = cfg.Loops[0].Hops[:1] },
			wantError: "must contain at least 2 hops",
		},
		{
			name:      "network missing RPCURL",
			mutate:    func(cfg *Config) { cfg.Networks[0].RPCURL = "" },
			wantError: "RPCURL is required",
		},
		{
			name:      "network missing BridgeAddr",
			mutate:    func(cfg *Config) { cfg.Networks[0].BridgeAddr = common.Address{} },
			wantError: "BridgeAddr is required and must not be the zero address",
		},
		{
			name:      "network missing Signer.Method",
			mutate:    func(cfg *Config) { cfg.Networks[0].Signer.Method = "" },
			wantError: "Signer.Method is required",
		},
		{
			name:      "network missing Name",
			mutate:    func(cfg *Config) { cfg.Networks[0].Name = "" },
			wantError: "Name is required",
		},
		{
			name:      "loop missing Name",
			mutate:    func(cfg *Config) { cfg.Loops[0].Name = "" },
			wantError: "loops[#0]: Name is required",
		},
		{
			name:      "no loop is enabled",
			mutate:    func(cfg *Config) { cfg.Loops[0].Enabled = false },
			wantError: "no loop is enabled",
		},
		{
			name:      "no loops at all",
			mutate:    func(cfg *Config) { cfg.Loops = nil },
			wantError: "no loop is enabled",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(cfg)

			err := cfg.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantError)
		})
	}
}

func TestConfig_Validate_AggregatesMultipleProblems(t *testing.T) {
	cfg := validConfig()
	cfg.Global.ProxyURL = ""
	cfg.Loops[0].Amount = NewWeiAmount(0)
	cfg.Networks[0].RPCURL = ""

	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "Global.ProxyURL is required")
	require.Contains(t, err.Error(), "Amount must be greater than 0")
	require.Contains(t, err.Error(), "RPCURL is required")
}

func TestConfig_Validate_DirectionCoverageWarning(t *testing.T) {
	// 3 networks configured, but the only loop is a 2-hop L1<->L2 back-and-forth: L2->L2 is never
	// exercised. This must be a warning, not a validation error.
	cfg := validConfig()
	cfg.Loops[0].Hops = []Hop{
		{Source: 0, Destination: 1, Claim: ClaimAuto},
		{Source: 1, Destination: 0, Claim: ClaimManual},
	}

	err := cfg.Validate()
	require.NoError(t, err)
	require.NotEmpty(t, cfg.Warnings())
	require.Contains(t, cfg.Warnings()[0], "L2->L2")
}

func TestConfig_Validate_NoDirectionWarningWithTwoNetworks(t *testing.T) {
	// Only 2 networks participate, so the "must cover all three directions" rule does not apply
	// (a 2-network config can never exercise L2->L2).
	cfg := validConfig()
	cfg.Networks = cfg.Networks[:2]
	cfg.Loops[0].Hops = []Hop{
		{Source: 0, Destination: 1, Claim: ClaimAuto},
		{Source: 1, Destination: 0, Claim: ClaimManual},
	}

	err := cfg.Validate()
	require.NoError(t, err)
	require.Empty(t, cfg.Warnings())
}

func TestWeiAmount_UnmarshalText(t *testing.T) {
	testCases := []struct {
		name        string
		input       string
		wantValue   int64
		wantErr     bool
		errContains string
	}{
		{name: "positive decimal", input: "1000000000000000000", wantValue: 1_000_000_000_000_000_000},
		{name: "zero", input: "0", wantValue: 0},
		{name: "empty string decodes to zero", input: "", wantValue: 0},
		{name: "negative rejected", input: "-1", wantErr: true, errContains: "must not be negative"},
		{name: "non-numeric rejected", input: "abc", wantErr: true, errContains: "not a base-10 integer"},
		{name: "hex-looking string rejected as decimal", input: "0x10", wantErr: true, errContains: "not a base-10 integer"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var w WeiAmount
			err := w.UnmarshalText([]byte(tc.input))
			if tc.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errContains)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantValue, w.Int64())
		})
	}
}

func TestWeiAmount_IsPositiveAndBigInt(t *testing.T) {
	require.False(t, NewWeiAmount(0).IsPositive())
	require.True(t, NewWeiAmount(1).IsPositive())

	w := NewWeiAmount(42)
	got := w.BigInt()
	require.Equal(t, big.NewInt(42), got)

	// BigInt returns a copy: mutating it must not affect the WeiAmount.
	got.SetInt64(0)
	require.Equal(t, int64(42), w.Int64())
}

func TestLoadConfig_RequiresAtLeastOnePath(t *testing.T) {
	_, err := LoadConfig()
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least one config file path is required")
}

func TestLoadConfig_ExampleTOML(t *testing.T) {
	cfg, err := LoadConfig("config-examples/example.toml")
	require.NoError(t, err)

	require.Equal(t, "http://127.0.0.1:15601", cfg.Global.ProxyURL)
	require.Len(t, cfg.Networks, 3)
	require.Len(t, cfg.Loops, 2)

	require.NoError(t, cfg.Validate())
	require.Empty(t, cfg.Warnings())
}

func TestLoadConfig_AppliesGlobalDefaults(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/minimal.toml"
	minimal := `
[Global]
ProxyURL = "http://127.0.0.1:15601"

[[Networks]]
NetworkID = 0
Name = "L1"
RPCURL = "http://127.0.0.1:13545"
BridgeAddr = "0x0000000000000000000000000000000000000001"

[Networks.Signer]
Method = "local"
Path = "/keystore"
Password = "changeme"

[[Networks]]
NetworkID = 1
Name = "L2A"
RPCURL = "http://127.0.0.1:14545"
BridgeAddr = "0x0000000000000000000000000000000000000002"

[Networks.Signer]
Method = "local"
Path = "/keystore"
Password = "changeme"

[[Loops]]
Name = "ring"
Asset = "eth"
Amount = "1000"
Enabled = true

[[Loops.Hops]]
Source = 0
Destination = 1
Claim = "auto"

[[Loops.Hops]]
Source = 1
Destination = 0
Claim = "manual"
`
	require.NoError(t, os.WriteFile(path, []byte(minimal), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	require.Equal(t, defaultLogLevel, cfg.Global.LogLevel)
	require.Equal(t, defaultLoopDelay, cfg.Global.LoopDelay.Duration)
	require.Equal(t, defaultHopTimeout, cfg.Global.HopTimeout.Duration)
	require.Equal(t, defaultPollInterval, cfg.Global.PollInterval.Duration)
	require.Equal(t, defaultManualGracePeriod, cfg.Global.ManualGracePeriod.Duration)
	require.EqualValues(t, defaultHopAttempts, cfg.Global.HopAttempts)

	require.NoError(t, cfg.Validate())
}

// TestLoadConfig_HopAttemptsOverride pins that Global.HopAttempts is settable from TOML (not just
// defaulted), and that Validate rejects a configured value of 0.
func TestLoadConfig_HopAttemptsOverride(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/hopattempts.toml"
	content := `
[Global]
ProxyURL = "http://127.0.0.1:15601"
HopAttempts = 7

[[Networks]]
NetworkID = 0
Name = "L1"
RPCURL = "http://127.0.0.1:13545"
BridgeAddr = "0x0000000000000000000000000000000000000001"

[Networks.Signer]
Method = "local"
Path = "/keystore"
Password = "changeme"

[[Networks]]
NetworkID = 1
Name = "L2A"
RPCURL = "http://127.0.0.1:14545"
BridgeAddr = "0x0000000000000000000000000000000000000002"

[Networks.Signer]
Method = "local"
Path = "/keystore"
Password = "changeme"

[[Loops]]
Name = "ring"
Asset = "eth"
Amount = "1000"
Enabled = true

[[Loops.Hops]]
Source = 0
Destination = 1
Claim = "auto"

[[Loops.Hops]]
Source = 1
Destination = 0
Claim = "manual"
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.EqualValues(t, 7, cfg.Global.HopAttempts)
	require.NoError(t, cfg.Validate())
}

// TestConfig_EnabledLoops pins that Enabled has no implicit true-default: only loops that set it
// explicitly are driven, which is exactly why Validate refuses a config where none of them do.
func TestConfig_EnabledLoops(t *testing.T) {
	cfg := validConfig()
	cfg.Loops = append(cfg.Loops, Loop{
		Name:    "off",
		Asset:   AssetETH,
		Amount:  NewWeiAmount(1),
		Enabled: false,
		Hops: []Hop{
			{Source: 0, Destination: 1, Claim: ClaimAuto},
			{Source: 1, Destination: 0, Claim: ClaimAuto},
		},
	})

	enabled := cfg.EnabledLoops()
	require.Len(t, enabled, 1)
	require.Equal(t, "ring", enabled[0].Name)

	byID := cfg.NetworksByID()
	require.Len(t, byID, 3)
	require.Equal(t, "L2B", byID[2].Name)
}
