package aggoracle

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/agglayer/aggkit/aggoracle/chaingersender"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
)

type TargetChainType string

const (
	EVMChain TargetChainType = "EVM"
)

var (
	SupportedChainTypes = []TargetChainType{EVMChain}
)

// Mode selects how the AggOracle injects Global Exit Roots (GER) on the L2.
type Mode string

const (
	// ModeDirect injects the GER with a single transaction sent by the GER updater (an EOA)
	// directly to the L2 GER manager (AgglayerGERL2).
	ModeDirect Mode = "direct"
	// ModeCommittee collects signatures from the GER validators and injects the GER
	// with a single transaction on the AggOracleCommittee (v2) contract.
	ModeCommittee Mode = "committee"
	// ModeAuto inspects the contract configured as GER updater on the L2 and picks one of the
	// other modes accordingly.
	ModeAuto Mode = "auto"
)

// SupportedModes lists all the valid values for Config.Mode.
var SupportedModes = []Mode{ModeDirect, ModeCommittee, ModeAuto}

// UnmarshalText implements encoding.TextUnmarshaler. It is case-insensitive and trims spaces.
func (m *Mode) UnmarshalText(text []byte) error {
	parsed := Mode(strings.ToLower(strings.TrimSpace(string(text))))
	if err := parsed.Validate(); err != nil {
		return err
	}
	*m = parsed
	return nil
}

// String returns the mode as a string.
func (m Mode) String() string {
	return string(m)
}

// Validate checks that the mode is one of SupportedModes.
func (m Mode) Validate() error {
	for _, supported := range SupportedModes {
		if m == supported {
			return nil
		}
	}
	return fmt.Errorf("invalid AggOracle.Mode %q (supported values: %v)", string(m), SupportedModes)
}

// ValidatorURLOverride sets the URL of a GER validator, replacing the one registered on-chain (if any).
type ValidatorURLOverride struct {
	// Address is the validator address, as registered on the AggOracleCommittee contract
	Address common.Address `mapstructure:"Address"`
	// URL is the base URL of the GER validator REST API
	URL string `mapstructure:"URL"`
}

// CommitteeV2TLSConfig sets the TLS options used to reach the GER validators.
type CommitteeV2TLSConfig struct {
	// CACertFile is an optional PEM file with the CA(s) used to verify the validators certificate,
	// instead of the system trust store
	CACertFile string `mapstructure:"CACertFile"`
	// ClientCertFile and ClientKeyFile are optional PEM files used to authenticate this
	// AggOracle against the validators (mTLS). Both or none must be set
	ClientCertFile string `mapstructure:"ClientCertFile"`
	ClientKeyFile  string `mapstructure:"ClientKeyFile"`
}

// CommitteeV2Config is the configuration specific to ModeCommittee.
type CommitteeV2Config struct {
	// RequestTimeout is the timeout of each request to a GER validator
	RequestTimeout types.Duration `mapstructure:"RequestTimeout"`
	// CollectRetryInterval is the time to wait before asking the validators again
	// when the threshold of signatures was not reached
	CollectRetryInterval types.Duration `mapstructure:"CollectRetryInterval"`
	// MaxInjectAttempts is how many times signatures are collected again when the
	// committee changes (validators or threshold) between collecting and mining the transaction
	MaxInjectAttempts int `mapstructure:"MaxInjectAttempts"`
	// PendingGERTimeout is how long a GER is kept pending while the validators don't consider it final.
	// 0 means keep it pending until it is injected or replaced by a newer one
	PendingGERTimeout types.Duration `mapstructure:"PendingGERTimeout"`
	// ValidatorURLOverrides sets the URL of a validator, for the validators that have no URL registered
	// on-chain or whose registered URL must be replaced. It takes precedence over the on-chain URL
	ValidatorURLOverrides []ValidatorURLOverride `mapstructure:"ValidatorURLOverrides"`
	// TLS options to reach the validators
	TLS CommitteeV2TLSConfig `mapstructure:"TLS"`
}

type Config struct {
	TargetChainType   TargetChainType          `mapstructure:"TargetChainType"`
	WaitPeriodNextGER types.Duration           `mapstructure:"WaitPeriodNextGER"`
	EVMSender         chaingersender.EVMConfig `mapstructure:"EVMSender"`
	// Mode selects how GERs are injected: direct, committee or auto. The old AggOracleCommittee (v1) is not
	// supported anymore: see the ErrLegacyCommitteeNotSupported error of the detection
	Mode Mode `mapstructure:"Mode"`
	// CommitteeV2 is the configuration used by ModeCommittee
	CommitteeV2 CommitteeV2Config `mapstructure:"CommitteeV2"`
}

// ResolveMode returns the mode requested by the configuration, which is ModeAuto if it is not set. The result can
// be ModeAuto: it is resolved at startup, by inspecting the contract on the L2.
func (c Config) ResolveMode() (Mode, error) {
	mode := c.Mode
	if mode == "" {
		mode = ModeAuto
	}
	if err := mode.Validate(); err != nil {
		return "", err
	}
	return mode, nil
}

// Validate checks the AggOracle configuration.
func (c Config) Validate() error {
	mode, err := c.ResolveMode()
	if err != nil {
		return err
	}

	if mode == ModeCommittee || mode == ModeAuto {
		if err := c.CommitteeV2.Validate(); err != nil {
			return fmt.Errorf("invalid AggOracle.CommitteeV2 configuration: %w", err)
		}
	}

	return nil
}

// Validate checks the CommitteeV2 configuration.
func (c CommitteeV2Config) Validate() error {
	if c.RequestTimeout.Duration <= 0 {
		return errors.New("RequestTimeout must be greater than 0")
	}
	if c.CollectRetryInterval.Duration <= 0 {
		return errors.New("CollectRetryInterval must be greater than 0")
	}
	if c.MaxInjectAttempts < 1 {
		return errors.New("MaxInjectAttempts must be at least 1")
	}
	if c.PendingGERTimeout.Duration < 0 {
		return errors.New("PendingGERTimeout must not be negative")
	}

	seen := make(map[common.Address]struct{}, len(c.ValidatorURLOverrides))
	for i, override := range c.ValidatorURLOverrides {
		if override.Address == aggkitcommon.ZeroAddress {
			return fmt.Errorf("ValidatorURLOverrides[%d]: Address must not be the zero address", i)
		}
		if _, dup := seen[override.Address]; dup {
			return fmt.Errorf("ValidatorURLOverrides[%d]: duplicated Address %s", i, override.Address)
		}
		seen[override.Address] = struct{}{}
		if err := validateValidatorURL(override.URL); err != nil {
			return fmt.Errorf("ValidatorURLOverrides[%d] (%s): %w", i, override.Address, err)
		}
	}

	if (c.TLS.ClientCertFile == "") != (c.TLS.ClientKeyFile == "") {
		return errors.New("TLS.ClientCertFile and TLS.ClientKeyFile must be set together")
	}

	return nil
}

// validateValidatorURL checks that the URL is an absolute http(s) URL.
func validateValidatorURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("invalid URL %q: it must be an absolute http or https URL", rawURL)
	}
	return nil
}
