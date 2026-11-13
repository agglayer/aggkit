// Package config is the configuration of the GER validator signer.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/pelletier/go-toml/v2"
)

// Block tags of the JSON-RPC API the finality can be based on
const (
	FinalityLatest    = "latest"
	FinalitySafe      = "safe"
	FinalityFinalized = "finalized"
)

// Names of the blocks in BlockFinality, the same as in the configuration of aggkit
const (
	LatestBlockName    = "LatestBlock"
	SafeBlockName      = "SafeBlock"
	FinalizedBlockName = "FinalizedBlock"

	blockNameAndOffsetSeparator = "/"
)

// Log environments, the same as in aggkit: production logs JSON, development logs text
const (
	LogEnvironmentProduction  = "production"
	LogEnvironmentDevelopment = "development"
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

// BlockFinality is the finality that the L1 block that inserted the GER must have to be signed, written as in the
// configuration of aggkit: <block>[/<offset>], where block is LatestBlock, SafeBlock or FinalizedBlock and the
// offset, if any, is negative. For example "FinalizedBlock", or "LatestBlock/-12" (the head minus 12 blocks).
// A positive offset is not accepted: it would sign blocks that are less final than the ones of the block.
type BlockFinality struct {
	// Tag is the block tag of the JSON-RPC API: latest, safe or finalized
	Tag string
	// NumBlocks is subtracted from the number of the block of the tag (the offset, without the sign)
	NumBlocks uint64
}

// NewBlockFinality parses a block finality ("FinalizedBlock", "LatestBlock/-12"). The name is case insensitive
func NewBlockFinality(text string) (BlockFinality, error) {
	name, offsetText, hasOffset := strings.Cut(text, blockNameAndOffsetSeparator)
	var finality BlockFinality
	switch strings.ToLower(name) {
	case strings.ToLower(LatestBlockName):
		finality.Tag = FinalityLatest
	case strings.ToLower(SafeBlockName):
		finality.Tag = FinalitySafe
	case strings.ToLower(FinalizedBlockName):
		finality.Tag = FinalityFinalized
	default:
		return BlockFinality{}, fmt.Errorf("invalid block finality %q: the block must be %s, %s or %s, "+
			"optionally with a negative offset (e.g. %s/-12)", text, LatestBlockName, SafeBlockName, FinalizedBlockName,
			LatestBlockName)
	}
	if !hasOffset {
		return finality, nil
	}
	offset, err := strconv.ParseInt(offsetText, 0, 64)
	if err != nil {
		return BlockFinality{}, fmt.Errorf("invalid offset of the block finality %q: %w", text, err)
	}
	if offset > 0 {
		return BlockFinality{}, fmt.Errorf("invalid block finality %q: the offset can't be positive, "+
			"it would sign blocks that are not final yet", text)
	}
	finality.NumBlocks = uint64(-offset)
	return finality, nil
}

// String returns the block finality as it is written in the configuration
func (f BlockFinality) String() string {
	var name string
	switch f.Tag {
	case FinalityLatest:
		name = LatestBlockName
	case FinalitySafe:
		name = SafeBlockName
	case FinalityFinalized:
		name = FinalizedBlockName
	default:
		name = f.Tag
	}
	if f.NumBlocks == 0 {
		return name
	}
	return fmt.Sprintf("%s%s-%d", name, blockNameAndOffsetSeparator, f.NumBlocks)
}

// UnmarshalText implements encoding.TextUnmarshaler
func (f *BlockFinality) UnmarshalText(text []byte) error {
	parsed, err := NewBlockFinality(string(text))
	if err != nil {
		return err
	}
	*f = parsed
	return nil
}

// MarshalText implements encoding.TextMarshaler
func (f BlockFinality) MarshalText() ([]byte, error) {
	return []byte(f.String()), nil
}

// LogConfig is the configuration of the logs, the same as the [Log] section of aggkit
type LogConfig struct {
	// Environment is the format of the logs: production (JSON) or development (text)
	Environment string `toml:"Environment"`
	// Level is debug, info, warn or error
	Level string `toml:"Level"`
	// Outputs are where the logs go: stdout, stderr or the path of a file (the logs are appended to it)
	Outputs []string `toml:"Outputs"`
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

// PublicRESTConfig is the configuration of the REST API. The first keys are the ones of the [PublicREST] section of
// aggkit, with the same meaning; the rest are of the validator
type PublicRESTConfig struct {
	Host string `toml:"Host"`
	Port uint16 `toml:"Port"`
	// ReadTimeout is the maximum duration to read a request (net/http.Server.ReadTimeout)
	ReadTimeout Duration `toml:"ReadTimeout"`
	// WriteTimeout is the maximum duration to answer a request, from the end of the reading of its headers
	// (net/http.Server.WriteTimeout). It must be greater than RequestTimeout
	WriteTimeout Duration `toml:"WriteTimeout"`
	// MaxRequestsPerIPAndSecond is the sustained rate of requests per client (IP address). 0 means unlimited
	MaxRequestsPerIPAndSecond float64 `toml:"MaxRequestsPerIPAndSecond"`
	// MaxRequestBodyBytes limits the size of the body of a request. 0 means the default (4096)
	MaxRequestBodyBytes int64 `toml:"MaxRequestBodyBytes"`

	// MaxRequestsBurstPerIP is how many requests a client can do at once, when MaxRequestsPerIPAndSecond is set
	MaxRequestsBurstPerIP int `toml:"MaxRequestsBurstPerIP"`
	// RequestTimeout is the maximum duration of the processing of a request: the queries to L1 and the signing
	RequestTimeout Duration `toml:"RequestTimeout"`
	// TLSCertFile and TLSKeyFile are the PEM files of the certificate and its key, to serve HTTPS. Both or none
	TLSCertFile string `toml:"TLSCertFile"`
	TLSKeyFile  string `toml:"TLSKeyFile"`
	// InsecureHTTP allows to serve HTTP without TLS. Only when the TLS is terminated by something in front
	// of the validator (a reverse proxy), or for tests: the signatures travel in the clear otherwise
	InsecureHTTP bool `toml:"InsecureHTTP"`
}

// RequestBodyLimit returns the limit of the size of the body of a request: MaxRequestBodyBytes when set, the
// default otherwise
func (r PublicRESTConfig) RequestBodyLimit() int64 {
	if r.MaxRequestBodyBytes <= 0 {
		return DefaultMaxRequestBodyBytes
	}
	return r.MaxRequestBodyBytes
}

// CacheConfig is the configuration of the cache of the answers
type CacheConfig struct {
	// MaxEntries is the maximum number of GERs whose signature is kept (the oldest are dropped)
	MaxEntries int `toml:"MaxEntries"`
	// NegativeTTL is how long an answer that is not a signature (unknown GER, not canonical, not final)
	// is kept, so repeated requests for a GER that can't be signed don't hit the L1 RPC every time
	NegativeTTL Duration `toml:"NegativeTTL"`
}

// Config is the configuration of the GER validator signer. The sections and keys that also exist in aggkit ([Log],
// [PublicREST], BlockFinality) are written as in aggkit. The default values are in DefaultValues
type Config struct {
	// L1RPCURL is the JSON-RPC endpoint of L1. It can have a token in it: it is never logged
	L1RPCURL string `toml:"L1RPCURL"`
	// L1ChainID is checked against the chain id of the endpoint at startup
	L1ChainID uint64 `toml:"L1ChainID"`
	// AgglayerGERAddr is the AgglayerGER contract of L1 (globalExitRootMap is read from it)
	AgglayerGERAddr common.Address `toml:"AgglayerGERAddr"`
	// L1RequestTimeout limits each request to the L1 RPC
	L1RequestTimeout Duration `toml:"L1RequestTimeout"`
	// BlockFinality is the finality the L1 block that inserted a GER needs to sign it ("LatestBlock/-12")
	BlockFinality BlockFinality `toml:"BlockFinality"`

	Log        LogConfig        `toml:"Log"`
	Signer     SignerConfig     `toml:"Signer"`
	PublicREST PublicRESTConfig `toml:"PublicREST"`
	Cache      CacheConfig      `toml:"Cache"`
}

// DefaultMaxRequestBodyBytes is the limit of the size of the body of a request when MaxRequestBodyBytes is not set
const DefaultMaxRequestBodyBytes int64 = 4096

// renamedKeys explains the keys of the previous format of the configuration, that are now as in aggkit
var renamedKeys = map[string]string{
	"LogLevel": "LogLevel is now Level in the [Log] section, as in aggkit",
	"Finality": "the [Finality] section is now BlockFinality, as in aggkit: e.g. BlockFinality = \"LatestBlock/-12\"",
	"REST":     "the [REST] section is now [PublicREST], as in aggkit (MaxBodyBytes is MaxRequestBodyBytes)",
	"RateLimit": "the [RateLimit] section is now PublicREST.MaxRequestsPerIPAndSecond (RequestsPerSecond) and " +
		"PublicREST.MaxRequestsBurstPerIP (Burst)",
}

// Default returns the configuration with the default values (DefaultValues). The mandatory fields (L1RPCURL,
// L1ChainID, AgglayerGERAddr and Signer) have no default.
func Default() Config {
	var cfg Config
	if err := toml.Unmarshal([]byte(DefaultValues), &cfg); err != nil {
		panic(fmt.Sprintf("invalid DefaultValues: %v", err))
	}
	return cfg
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
			return Config{}, fmt.Errorf("invalid configuration: %s%s", strictErr.String(), renamedKeyHints(strictErr))
		}
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// renamedKeyHints explains the unknown keys that are of the previous format of the configuration
func renamedKeyHints(strictErr *toml.StrictMissingError) string {
	var hints []string
	seen := make(map[string]bool)
	for _, decodeErr := range strictErr.Errors {
		key := decodeErr.Key()
		if len(key) == 0 || seen[key[0]] {
			continue
		}
		if hint, ok := renamedKeys[key[0]]; ok {
			seen[key[0]] = true
			hints = append(hints, hint)
		}
	}
	if len(hints) == 0 {
		return ""
	}
	return "\n" + strings.Join(hints, "\n")
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
	if err := c.Log.validate(); err != nil {
		return err
	}
	switch c.BlockFinality.Tag {
	case FinalityLatest, FinalitySafe, FinalityFinalized:
	default:
		return errors.New("BlockFinality is required (e.g. FinalizedBlock or LatestBlock/-12)")
	}

	if err := c.validateSigner(); err != nil {
		return err
	}
	if err := c.PublicREST.validate(); err != nil {
		return err
	}
	if c.Cache.MaxEntries < 1 {
		return errors.New("Cache.MaxEntries must be at least 1")
	}
	if c.Cache.NegativeTTL.Duration < 0 {
		return errors.New("Cache.NegativeTTL must not be negative")
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

func (l LogConfig) validate() error {
	switch l.Environment {
	case LogEnvironmentProduction, LogEnvironmentDevelopment:
	default:
		return fmt.Errorf("invalid Log.Environment %q (%s or %s)",
			l.Environment, LogEnvironmentProduction, LogEnvironmentDevelopment)
	}
	switch l.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid Log.Level %q (debug, info, warn or error)", l.Level)
	}
	if len(l.Outputs) == 0 {
		return errors.New("Log.Outputs needs at least one output (stdout, stderr or the path of a file)")
	}
	for _, output := range l.Outputs {
		if output == "" {
			return errors.New("Log.Outputs has an empty output")
		}
	}
	return nil
}

func (r PublicRESTConfig) validate() error {
	if r.Port == 0 {
		return errors.New("PublicREST.Port must be between 1 and 65535")
	}
	if r.ReadTimeout.Duration <= 0 {
		return errors.New("PublicREST.ReadTimeout must be greater than 0")
	}
	if r.RequestTimeout.Duration <= 0 {
		return errors.New("PublicREST.RequestTimeout must be greater than 0")
	}
	if r.WriteTimeout.Duration <= r.RequestTimeout.Duration {
		return fmt.Errorf("PublicREST.WriteTimeout (%s) must be greater than PublicREST.RequestTimeout (%s): "+
			"the answer is written after the request is processed", r.WriteTimeout.Duration, r.RequestTimeout.Duration)
	}
	if r.MaxRequestBodyBytes < 0 {
		return errors.New("PublicREST.MaxRequestBodyBytes must not be negative (0 is the default)")
	}
	if r.MaxRequestsPerIPAndSecond < 0 {
		return errors.New("PublicREST.MaxRequestsPerIPAndSecond must not be negative (0 is unlimited)")
	}
	if r.MaxRequestsPerIPAndSecond > 0 && r.MaxRequestsBurstPerIP < 1 {
		return errors.New("PublicREST.MaxRequestsBurstPerIP must be at least 1 when MaxRequestsPerIPAndSecond is set")
	}
	if (r.TLSCertFile == "") != (r.TLSKeyFile == "") {
		return errors.New("PublicREST.TLSCertFile and PublicREST.TLSKeyFile must be set together")
	}
	if r.TLSCertFile == "" && !r.InsecureHTTP {
		return errors.New("TLS is required: set PublicREST.TLSCertFile and PublicREST.TLSKeyFile to serve HTTPS, " +
			"or PublicREST.InsecureHTTP = true if the TLS is terminated in front of the validator (or for tests)")
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
