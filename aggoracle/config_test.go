package aggoracle

import (
	"testing"
	"time"

	"github.com/agglayer/aggkit/aggoracle/chaingersender"
	"github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var committeeAddr = common.HexToAddress("0x1111111111111111111111111111111111111111")

func validCommitteeV2Config() CommitteeV2Config {
	return CommitteeV2Config{
		RequestTimeout:       types.NewDuration(5 * time.Second),
		CollectRetryInterval: types.NewDuration(5 * time.Second),
		MaxInjectAttempts:    3,
	}
}

func TestMode_UnmarshalText(t *testing.T) {
	tests := []struct {
		input    string
		expected Mode
		wantErr  string
	}{
		{input: "direct", expected: ModeDirect},
		{input: "committee", expected: ModeCommittee},
		{input: "auto", expected: ModeAuto},
		{input: "  AUTO  ", expected: ModeAuto},
		{input: "Committee", expected: ModeCommittee},
		{input: "", wantErr: "invalid AggOracle.Mode"},
		{input: "unknown", wantErr: "invalid AggOracle.Mode"},
		// The old AggOracleCommittee (v1) is not supported anymore, and these names were never released
		{input: "legacy", wantErr: "invalid AggOracle.Mode"},
		{input: "committee_v1", wantErr: "invalid AggOracle.Mode"},
		{input: "committee_v2", wantErr: "invalid AggOracle.Mode"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			var mode Mode
			err := mode.UnmarshalText([]byte(tc.input))
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.Empty(t, mode, "mode must not be modified on error")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.expected, mode)
			require.Equal(t, string(tc.expected), mode.String())
		})
	}
}

func TestConfig_ResolveMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     Mode
		expected Mode
		wantErr  string
	}{
		{name: "unset mode is auto", mode: "", expected: ModeAuto},
		{name: "auto", mode: ModeAuto, expected: ModeAuto},
		{name: "direct", mode: ModeDirect, expected: ModeDirect},
		{name: "committee", mode: ModeCommittee, expected: ModeCommittee},
		{name: "invalid mode", mode: "bogus", wantErr: "invalid AggOracle.Mode"},
		{name: "legacy was removed", mode: "legacy", wantErr: "invalid AggOracle.Mode"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The committee address has no effect on the mode: auto detects it on the L2
			cfg := Config{
				Mode:      tc.mode,
				EVMSender: chaingersender.EVMConfig{AggOracleCommitteeAddr: committeeAddr},
			}
			got, err := cfg.ResolveMode()
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.expected, got)
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	validator := common.HexToAddress("0x2222222222222222222222222222222222222222")

	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{name: "direct needs no committee config", cfg: Config{Mode: ModeDirect}},
		{
			name:    "committee with defaults",
			cfg:     Config{Mode: ModeCommittee, CommitteeV2: validCommitteeV2Config()},
			wantErr: "",
		},
		{
			name:    "auto validates committee config",
			cfg:     Config{Mode: ModeAuto},
			wantErr: "RequestTimeout must be greater than 0",
		},
		{
			name:    "direct does not validate committee config",
			cfg:     Config{Mode: ModeDirect},
			wantErr: "",
		},
		{name: "invalid mode", cfg: Config{Mode: "bogus"}, wantErr: "invalid AggOracle.Mode"},
		{
			name: "committee with overrides and TLS",
			cfg: func() Config {
				c := validCommitteeV2Config()
				c.ValidatorURLOverrides = []ValidatorURLOverride{{Address: validator, URL: "https://validator.example.com:8443"}}
				c.TLS = CommitteeV2TLSConfig{CACertFile: "ca.pem", ClientCertFile: "c.pem", ClientKeyFile: "k.pem"}
				return Config{Mode: ModeCommittee, CommitteeV2: c}
			}(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestCommitteeV2Config_Validate(t *testing.T) {
	validator := common.HexToAddress("0x2222222222222222222222222222222222222222")
	other := common.HexToAddress("0x3333333333333333333333333333333333333333")

	with := func(mutate func(*CommitteeV2Config)) CommitteeV2Config {
		c := validCommitteeV2Config()
		mutate(&c)
		return c
	}

	tests := []struct {
		name    string
		cfg     CommitteeV2Config
		wantErr string
	}{
		{name: "valid", cfg: validCommitteeV2Config()},
		{
			name:    "zero request timeout",
			cfg:     with(func(c *CommitteeV2Config) { c.RequestTimeout = types.Duration{} }),
			wantErr: "RequestTimeout",
		},
		{
			name:    "zero collect retry interval",
			cfg:     with(func(c *CommitteeV2Config) { c.CollectRetryInterval = types.Duration{} }),
			wantErr: "CollectRetryInterval",
		},
		{
			name:    "no inject attempts",
			cfg:     with(func(c *CommitteeV2Config) { c.MaxInjectAttempts = 0 }),
			wantErr: "MaxInjectAttempts",
		},
		{
			name:    "negative pending timeout",
			cfg:     with(func(c *CommitteeV2Config) { c.PendingGERTimeout = types.NewDuration(-time.Second) }),
			wantErr: "PendingGERTimeout",
		},
		{
			name: "zero pending timeout means no limit",
			cfg:  with(func(c *CommitteeV2Config) { c.PendingGERTimeout = types.Duration{} }),
		},
		{
			name: "override with zero address",
			cfg: with(func(c *CommitteeV2Config) {
				c.ValidatorURLOverrides = []ValidatorURLOverride{{URL: "http://v:1"}}
			}),
			wantErr: "zero address",
		},
		{
			name: "duplicated override",
			cfg: with(func(c *CommitteeV2Config) {
				c.ValidatorURLOverrides = []ValidatorURLOverride{
					{Address: validator, URL: "http://v:1"},
					{Address: validator, URL: "http://v:2"},
				}
			}),
			wantErr: "duplicated Address",
		},
		{
			name: "override without scheme",
			cfg: with(func(c *CommitteeV2Config) {
				c.ValidatorURLOverrides = []ValidatorURLOverride{{Address: validator, URL: "validator.example.com"}}
			}),
			wantErr: "absolute http or https URL",
		},
		{
			name: "override with unsupported scheme",
			cfg: with(func(c *CommitteeV2Config) {
				c.ValidatorURLOverrides = []ValidatorURLOverride{{Address: validator, URL: "ftp://validator.example.com"}}
			}),
			wantErr: "absolute http or https URL",
		},
		{
			name: "two different overrides",
			cfg: with(func(c *CommitteeV2Config) {
				c.ValidatorURLOverrides = []ValidatorURLOverride{
					{Address: validator, URL: "http://v:1"},
					{Address: other, URL: "https://v:2"},
				}
			}),
		},
		{
			name:    "client cert without key",
			cfg:     with(func(c *CommitteeV2Config) { c.TLS.ClientCertFile = "c.pem" }),
			wantErr: "must be set together",
		},
		{
			name:    "client key without cert",
			cfg:     with(func(c *CommitteeV2Config) { c.TLS.ClientKeyFile = "k.pem" }),
			wantErr: "must be set together",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
