package claimer

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	defaultAddress             = "0.0.0.0"
	defaultPort                = 7080
	defaultReadTimeoutSeconds  = 30
	defaultWriteTimeoutSeconds = 30
)

// Config configures the exit certificate claimer backend.
type Config struct {
	// Address is the HTTP server bind host/IP (without port).
	Address string `json:"address"`
	// Port is the HTTP server bind port.
	Port int `json:"port"`
	// ReadTimeoutSeconds / WriteTimeoutSeconds bound HTTP request handling.
	ReadTimeoutSeconds  int `json:"readTimeoutSeconds"`
	WriteTimeoutSeconds int `json:"writeTimeoutSeconds"`

	// SignedCertificatePath is the path to exit-certificate-signed.json produced by exit_certificate.
	SignedCertificatePath string `json:"signedCertificatePath"`
	// LocalExitTreeDBPath is the path to step-g-l2bridgesyncerlite.sqlite produced by exit_certificate.
	LocalExitTreeDBPath string `json:"localExitTreeDBPath"`
	// L1InfoTreeDBPath is the path to the l1infotreesync SQLite database.
	L1InfoTreeDBPath string `json:"l1InfoTreeDBPath"`
	// StepWaitResultPath is the path to step-wait-result.json produced by the exit_certificate WAIT
	// step. It records the certificate's L1 settlement (the VerifyBatchesTrustedAggregator event and
	// the accompanying L1 Info Tree update), identifying the L1 info tree leaf it settled at.
	StepWaitResultPath string `json:"stepWaitResultPath"`

	// NetworkID is the source network of the exits. Defaults to the certificate's network_id when 0.
	NetworkID uint32 `json:"networkId"`

	// L1Sync controls optional background synchronization of the L1 Info Tree from L1.
	L1Sync L1SyncConfig `json:"l1Sync"`
}

// L1SyncConfig controls the optional L1 Info Tree synchronization. When Enabled is false the
// L1InfoTreeDBPath database is opened read-only.
type L1SyncConfig struct {
	Enabled            bool   `json:"enabled"`
	RPCURL             string `json:"rpcUrl"`
	GlobalExitRootAddr string `json:"globalExitRootAddr"`
	RollupManagerAddr  string `json:"rollupManagerAddr"`
	InitialBlock       uint64 `json:"initialBlock"`
	SyncBlockChunkSize uint64 `json:"syncBlockChunkSize"`
	BlockFinality      string `json:"blockFinality"`
}

// LoadConfig reads and validates the config file. JSON and TOML are both accepted; the format is
// selected by file extension (.toml → TOML, anything else → JSON). Relative file paths are
// resolved against the directory containing the config file.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %q: %w", path, err)
	}

	if strings.EqualFold(filepath.Ext(path), ".toml") {
		raw, err = tomlToJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("parsing TOML config %q: %w", path, err)
		}
	}

	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %q: %w", path, err)
	}

	cfg.applyDefaults()
	cfg.resolvePaths(filepath.Dir(path))

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ListenAddress returns the host:port the HTTP server binds to.
func (c *Config) ListenAddress() string {
	return net.JoinHostPort(c.Address, strconv.Itoa(c.Port))
}

func (c *Config) applyDefaults() {
	if c.Address == "" {
		c.Address = defaultAddress
	}
	if c.Port == 0 {
		c.Port = defaultPort
	}
	if c.ReadTimeoutSeconds == 0 {
		c.ReadTimeoutSeconds = defaultReadTimeoutSeconds
	}
	if c.WriteTimeoutSeconds == 0 {
		c.WriteTimeoutSeconds = defaultWriteTimeoutSeconds
	}
}

func (c *Config) resolvePaths(baseDir string) {
	c.SignedCertificatePath = resolvePath(baseDir, c.SignedCertificatePath)
	c.LocalExitTreeDBPath = resolvePath(baseDir, c.LocalExitTreeDBPath)
	c.L1InfoTreeDBPath = resolvePath(baseDir, c.L1InfoTreeDBPath)
	c.StepWaitResultPath = resolvePath(baseDir, c.StepWaitResultPath)
}

func (c *Config) validate() error {
	if c.SignedCertificatePath == "" {
		return fmt.Errorf("signedCertificatePath is required")
	}
	if c.LocalExitTreeDBPath == "" {
		return fmt.Errorf("localExitTreeDBPath is required")
	}
	if c.L1InfoTreeDBPath == "" {
		return fmt.Errorf("l1InfoTreeDBPath is required")
	}
	if c.StepWaitResultPath == "" {
		return fmt.Errorf("stepWaitResultPath is required")
	}
	if c.L1Sync.Enabled {
		if c.L1Sync.RPCURL == "" {
			return fmt.Errorf("l1Sync.rpcUrl is required when l1Sync.enabled is true")
		}
	}
	return nil
}

// resolvePath makes a relative path absolute against baseDir; empty and absolute paths are unchanged.
func resolvePath(baseDir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(baseDir, p)
}

// tomlToJSON normalizes a TOML document into JSON so both formats share one parsing path.
func tomlToJSON(tomlRaw []byte) ([]byte, error) {
	var m map[string]any
	if err := toml.Unmarshal(tomlRaw, &m); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}
