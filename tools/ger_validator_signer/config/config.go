// Package config is the configuration of the GER validator signer.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/pelletier/go-toml/v2"
)

// Block tags the finality can be based on
const (
	FinalityLatest    = "latest"
	FinalitySafe      = "safe"
	FinalityFinalized = "finalized"
)

// Signing methods that can sign a hash. The "remote" method of go_signer can't (it signs with EIP-155), and
// "none" and "mock" are not meant for a real validator.
const (
	SignerMethodLocal = "local"
	SignerMethodGCP   = "GCP"
	SignerMethodAWS   = "AWS"
)

// Duration is a time.Duration that is read from a string ("5s")
type Duration struct{ time.Duration }

// UnmarshalText implements encoding.TextUnmarshaler
func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", string(text), err)
	}
	d.Duration = parsed
	return nil
}

// MarshalText implements encoding.TextMarshaler
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// FinalityConfig is the finality that the L1 block that inserted the GER must have to be signed:
// the block of the tag minus NumBlocks. For example, Tag latest with NumBlocks 6 is the head minus 6 blocks.
type FinalityConfig struct {
	// Tag is the block tag: latest, safe or finalized
	Tag string `toml:"Tag"`
	// NumBlocks is subtracted from the number of the block of the tag. It can't be negative
	NumBlocks uint64 `toml:"NumBlocks"`
}

// SignerConfig is the configuration of the signer (github.com/agglayer/go_signer). The key Method selects
// how to sign (local, GCP or AWS) and the other keys are the parameters of that method, for example
// { Method = "local", Path = "/app/keystore/validator.keystore", Password = "..." }
type SignerConfig map[string]any

// Method returns the signing method
func (s SignerConfig) Method() string {
	for key, value := range s {
		if strings.EqualFold(key, "Method") {
			method, _ := value.(string)
			return method
		}
	}
	return ""
}

// Params returns the parameters of the method (every key but Method), with the keys in lower case: go_signer looks
// them up that way, because in aggkit they come from viper, which lowers the case of all the keys.
func (s SignerConfig) Params() map[string]any {
	params := make(map[string]any, len(s))
	for key, value := range s {
		if !strings.EqualFold(key, "Method") {
			params[strings.ToLower(key)] = value
		}
	}
	return params
}

// RESTConfig is the configuration of the REST API
type RESTConfig struct {
	Host string `toml:"Host"`
	Port uint16 `toml:"Port"`
	// TLSCertFile and TLSKeyFile are the PEM files of the certificate and its key. Both or none
	TLSCertFile string `toml:"TLSCertFile"`
	TLSKeyFile  string `toml:"TLSKeyFile"`
	// InsecureHTTP allows to serve HTTP without TLS. Only when the TLS is terminated by something in front
	// of the validator (a reverse proxy), or for tests: the signatures travel in the clear otherwise
	InsecureHTTP bool `toml:"InsecureHTTP"`
	// RequestTimeout is the maximum duration of a request, including the queries to L1 and the signing
	RequestTimeout Duration `toml:"RequestTimeout"`
	// MaxBodyBytes limits the size of the body of a request
	MaxBodyBytes int64 `toml:"MaxBodyBytes"`
}

// CacheConfig is the configuration of the cache of the answers
type CacheConfig struct {
	// MaxEntries is the maximum number of GERs whose signature is kept (the oldest are dropped)
	MaxEntries int `toml:"MaxEntries"`
	// NegativeTTL is how long an answer that is not a signature (unknown GER, not canonical, not final)
	// is kept, so repeated requests for a GER that can't be signed don't hit the L1 RPC every time
	NegativeTTL Duration `toml:"NegativeTTL"`
}

// RateLimitConfig limits the requests of each client (IP address)
type RateLimitConfig struct {
	// RequestsPerSecond is the sustained rate per client. 0 disables the limit
	RequestsPerSecond float64 `toml:"RequestsPerSecond"`
	// Burst is how many requests a client can do at once
	Burst int `toml:"Burst"`
}

// Config is the configuration of the GER validator signer
type Config struct {
	// L1RPCURL is the JSON-RPC endpoint of L1. It can have a token in it: it is never logged
	L1RPCURL string `toml:"L1RPCURL"`
	// L1ChainID is checked against the chain id of the endpoint at startup
	L1ChainID uint64 `toml:"L1ChainID"`
	// AgglayerGERAddr is the AgglayerGER contract of L1 (globalExitRootMap is read from it)
	AgglayerGERAddr common.Address `toml:"AgglayerGERAddr"`
	// L1RequestTimeout limits each request to the L1 RPC
	L1RequestTimeout Duration `toml:"L1RequestTimeout"`
	// LogLevel is debug, info, warn or error
	LogLevel string `toml:"LogLevel"`

	Finality  FinalityConfig  `toml:"Finality"`
	Signer    SignerConfig    `toml:"Signer"`
	REST      RESTConfig      `toml:"REST"`
	Cache     CacheConfig     `toml:"Cache"`
	RateLimit RateLimitConfig `toml:"RateLimit"`
}

// Default values
const (
	defaultL1RequestTimeout   = 5 * time.Second
	defaultRESTPort           = 8080
	defaultRESTRequestTimeout = 15 * time.Second
	defaultMaxBodyBytes       = 4096
	defaultCacheMaxEntries    = 10000
	defaultNegativeTTL        = 5 * time.Second
	defaultRequestsPerSecond  = 20
	defaultBurst              = 40
)

// Default returns the configuration with the default values. The mandatory fields (L1RPCURL, L1ChainID,
// AgglayerGERAddr and Signer) have no default.
func Default() Config {
	return Config{
		L1RequestTimeout: Duration{defaultL1RequestTimeout},
		LogLevel:         "info",
		Finality:         FinalityConfig{Tag: FinalityFinalized},
		REST: RESTConfig{
			Host:           "0.0.0.0",
			Port:           defaultRESTPort,
			RequestTimeout: Duration{defaultRESTRequestTimeout},
			MaxBodyBytes:   defaultMaxBodyBytes,
		},
		Cache:     CacheConfig{MaxEntries: defaultCacheMaxEntries, NegativeTTL: Duration{defaultNegativeTTL}},
		RateLimit: RateLimitConfig{RequestsPerSecond: defaultRequestsPerSecond, Burst: defaultBurst},
	}
}

// Load reads and validates the configuration of a TOML file. An unknown key is an error: a typo in a
// security parameter must not be silently ignored.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("failed to read the configuration file: %w", err)
	}
	return Parse(data)
}

// Parse reads and validates the configuration from the content of a TOML file
func Parse(data []byte) (Config, error) {
	cfg := Default()
	decoder := toml.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		var strictErr *toml.StrictMissingError
		if errors.As(err, &strictErr) {
			return Config{}, fmt.Errorf("invalid configuration: %s", strictErr.String())
		}
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks the configuration
func (c Config) Validate() error {
	if err := validateURL("L1RPCURL", c.L1RPCURL); err != nil {
		return err
	}
	if c.L1ChainID == 0 {
		return errors.New("L1ChainID is required")
	}
	if c.AgglayerGERAddr == (common.Address{}) {
		return errors.New("AgglayerGERAddr is required")
	}
	if c.L1RequestTimeout.Duration <= 0 {
		return errors.New("L1RequestTimeout must be greater than 0")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid LogLevel %q (debug, info, warn or error)", c.LogLevel)
	}

	switch c.Finality.Tag {
	case FinalityLatest, FinalitySafe, FinalityFinalized:
	default:
		return fmt.Errorf("invalid Finality.Tag %q (%s, %s or %s)",
			c.Finality.Tag, FinalityLatest, FinalitySafe, FinalityFinalized)
	}

	if err := c.validateSigner(); err != nil {
		return err
	}
	if err := c.REST.validate(); err != nil {
		return err
	}
	if c.Cache.MaxEntries < 1 {
		return errors.New("Cache.MaxEntries must be at least 1")
	}
	if c.Cache.NegativeTTL.Duration < 0 {
		return errors.New("Cache.NegativeTTL must not be negative")
	}
	if c.RateLimit.RequestsPerSecond < 0 {
		return errors.New("RateLimit.RequestsPerSecond must not be negative")
	}
	if c.RateLimit.RequestsPerSecond > 0 && c.RateLimit.Burst < 1 {
		return errors.New("RateLimit.Burst must be at least 1 when the rate limit is enabled")
	}
	return nil
}

func (c Config) validateSigner() error {
	switch method := c.Signer.Method(); method {
	case SignerMethodLocal, SignerMethodGCP, SignerMethodAWS:
		return nil
	case "":
		return errors.New("Signer.Method is required (local, GCP or AWS)")
	case "remote":
		return errors.New("the signer method \"remote\" can't be used: it can't sign a hash (it signs with EIP-155). " +
			"Use local, GCP or AWS")
	default:
		return fmt.Errorf("invalid Signer.Method %q (local, GCP or AWS)", method)
	}
}

func (r RESTConfig) validate() error {
	if r.Port == 0 {
		return errors.New("REST.Port must be between 1 and 65535")
	}
	if (r.TLSCertFile == "") != (r.TLSKeyFile == "") {
		return errors.New("REST.TLSCertFile and REST.TLSKeyFile must be set together")
	}
	if r.TLSCertFile == "" && !r.InsecureHTTP {
		return errors.New("TLS is required: set REST.TLSCertFile and REST.TLSKeyFile, " +
			"or REST.InsecureHTTP = true if the TLS is terminated in front of the validator (or for tests)")
	}
	if r.RequestTimeout.Duration <= 0 {
		return errors.New("REST.RequestTimeout must be greater than 0")
	}
	if r.MaxBodyBytes < 1 {
		return errors.New("REST.MaxBodyBytes must be at least 1")
	}
	return nil
}

func validateURL(name, rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("%s is required", name)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("%s must be an absolute http or https URL", name)
	}
	return nil
}
