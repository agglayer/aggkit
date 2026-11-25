package claimer

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	"github.com/pelletier/go-toml/v2"
)

const (
	defaultAddress             = "0.0.0.0"
	defaultPort                = 7080
	defaultReadTimeoutSeconds  = 30
	defaultWriteTimeoutSeconds = 30
	// defaultRunDataDir is the default runDataDir, relative to the config file.
	defaultRunDataDir = "archive-cache"
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

	// PendingBridgesPath is the optional path to step-pending-l2-bridges.json produced by the
	// exit_certificate PENDING_CLAIMS step: the unclaimed L2→L1 bridges emitted before the certificate. When
	// set and the file exists, they are served by /bridges and /claim-params along with the
	// certificate's exits.
	PendingBridgesPath string `json:"pendingBridgesPath"`

	// RunSource is the archive of the exit_certificate run to serve (gs://<bucket>/<prefix>, uploaded by
	// its --step upload). When set, the run data (certificate, local exit tree DB, WAIT result and pending
	// bridges) is downloaded from it into RunDataDir and verified on startup, so signedCertificatePath,
	// localExitTreeDBPath, stepWaitResultPath and pendingBridgesPath must be left empty. Read access to the
	// bucket is enough.
	RunSource string `json:"runSource"`
	// RunSourceHeight, when set, makes RunSource the archive base (gs://<bucket>/<base prefix>) instead of
	// a run: the run served is <RunSource>/<NetworkID>/<RunSourceHeight, 10 digits>, so NetworkID is
	// required. Leave it unset when RunSource already names the run.
	RunSourceHeight *uint64 `json:"runSourceHeight"`
	// FollowIntervalSeconds enables follow mode when greater than 0: every that many seconds the claimer
	// looks for the run of the next certificate height in the archive and, once it is downloaded, verified
	// and its settlement indexed in the L1 Info Tree, serves it instead of the current one, without a
	// restart. It needs RunSource (a run of the archive layout, or the base with RunSourceHeight).
	FollowIntervalSeconds int `json:"followIntervalSeconds"`
	// FollowCleanupOldRuns says whether follow mode removes the local copy of a run (its directory under
	// RunDataDir) once the next one is being served. Defaults to true.
	FollowCleanupOldRuns *bool `json:"followCleanupOldRuns"`
	// RunDataDir is the base directory RunSource is downloaded into (one subdirectory per bucket/prefix).
	// Defaults to "archive-cache" next to the config file.
	RunDataDir string `json:"runDataDir"`

	// NetworkID is the source network of the exits. Defaults to the certificate's network_id when 0.
	NetworkID uint32 `json:"networkId"`

	// MessageRoutes tells API v2 how to decode the beneficiary of Message exits (see MessageRoute).
	// A Message exit of a route that is not listed is only found by its destination address.
	MessageRoutes []MessageRoute `json:"messageRoutes"`

	// L1Sync controls optional background synchronization of the L1 Info Tree from L1.
	L1Sync L1SyncConfig `json:"l1Sync"`

	// servedRun describes the archived run the run data paths point at, once FetchRunSource downloaded it.
	servedRun *runFiles
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
	if cfg.RunSource != "" && cfg.RunDataDir == "" {
		cfg.RunDataDir = defaultRunDataDir
	}
	cfg.resolvePaths(filepath.Dir(path))

	if err := cfg.checkRunSourcePaths(); err != nil {
		return nil, err
	}
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
	c.PendingBridgesPath = resolvePath(baseDir, c.PendingBridgesPath)
	c.RunDataDir = resolvePath(baseDir, c.RunDataDir)
}

// validate checks the config. With RunSource the run data paths are only filled in by FetchRunSource, so
// they are validated before it (validateRunSource) and after it (here, with the paths set).
func (c *Config) validate() error {
	if err := validateMessageRoutes(c.MessageRoutes); err != nil {
		return err
	}
	if c.FollowIntervalSeconds < 0 {
		return fmt.Errorf("followIntervalSeconds must not be negative")
	}
	if c.RunSource == "" && (c.FollowIntervalSeconds > 0 || c.RunSourceHeight != nil) {
		return fmt.Errorf("followIntervalSeconds and runSourceHeight need runSource: follow mode only follows " +
			"runs of an archive")
	}
	if c.RunSource != "" {
		if err := c.validateRunSource(); err != nil {
			return err
		}
		if c.SignedCertificatePath == "" { // not fetched yet
			return c.validateL1()
		}
	}
	if c.SignedCertificatePath == "" {
		return fmt.Errorf("signedCertificatePath is required")
	}
	if c.LocalExitTreeDBPath == "" {
		return fmt.Errorf("localExitTreeDBPath is required")
	}
	if c.StepWaitResultPath == "" {
		return fmt.Errorf("stepWaitResultPath is required")
	}
	return c.validateL1()
}

// checkRunSourcePaths rejects run data paths set together with runSource: they come from the archive.
func (c *Config) checkRunSourcePaths() error {
	if c.RunSource == "" {
		return nil
	}
	if c.SignedCertificatePath != "" || c.LocalExitTreeDBPath != "" || c.StepWaitResultPath != "" ||
		c.PendingBridgesPath != "" {
		return fmt.Errorf("runSource is set: leave signedCertificatePath, localExitTreeDBPath, stepWaitResultPath " +
			"and pendingBridgesPath empty (they point at the files downloaded from runSource)")
	}
	return nil
}

// validateRunSource checks runSource, runSourceHeight, the follow mode options and runDataDir.
func (c *Config) validateRunSource() error {
	ref, err := c.runRef()
	if err != nil {
		return err
	}
	if c.FollowIntervalSeconds > 0 && !ref.InLayout {
		return fmt.Errorf("followIntervalSeconds needs a runSource of the archive layout: a run "+
			"gs://<bucket>/<base>/<networkID>/<height, 10 digits>, or the base with runSourceHeight (the next run "+
			"is looked up by height); %s is neither", c.RunSource)
	}
	if c.RunDataDir == "" {
		return fmt.Errorf("runDataDir is required with runSource")
	}
	return nil
}

// runRef is the archived run a config serves first.
type runRef struct {
	// Location is the run's location.
	Location runarchive.Location
	// InLayout is true when the run is at <Base>/<NetworkID>/<Height>, the layout exit_certificate archives
	// runs in. Only then the run of the next height can be located (follow mode). Base, NetworkID and
	// Height are only meaningful when it is true.
	InLayout  bool
	Base      runarchive.Location
	NetworkID uint32
	Height    uint64
}

// runRef resolves runSource (and runSourceHeight) into the run to serve. runSource is a run when its prefix
// ends with /<networkID>/<height, 10 digits> (runarchive.Location.RunParts); with runSourceHeight it is the
// archive base and the run is <base>/<networkId>/<runSourceHeight>. Any other prefix is taken as a run
// outside the layout (it can be served, not followed).
func (c *Config) runRef() (*runRef, error) {
	loc, err := runarchive.ParseURI(c.RunSource)
	if err != nil {
		return nil, fmt.Errorf("invalid runSource: %w", err)
	}
	base, networkID, height, isRun := loc.RunParts()
	switch {
	case isRun && c.RunSourceHeight != nil:
		return nil, fmt.Errorf("runSource %s already names a run (network %d, height %d): remove "+
			"runSourceHeight, or set runSource to the archive base", c.RunSource, networkID, height)
	case isRun:
		return &runRef{Location: loc, InLayout: true, Base: base, NetworkID: networkID, Height: height}, nil
	case c.RunSourceHeight != nil:
		if c.NetworkID == 0 {
			return nil, fmt.Errorf("runSourceHeight makes runSource the archive base: networkId is required to " +
				"locate the run (<runSource>/<networkId>/<height>)")
		}
		return &runRef{
			Location: loc.Run(c.NetworkID, *c.RunSourceHeight), InLayout: true,
			Base: loc, NetworkID: c.NetworkID, Height: *c.RunSourceHeight,
		}, nil
	default:
		return &runRef{Location: loc}, nil
	}
}

// followCleanupOldRuns returns the effective followCleanupOldRuns (default true).
func (c *Config) followCleanupOldRuns() bool {
	return c.FollowCleanupOldRuns == nil || *c.FollowCleanupOldRuns
}

// validateL1 checks the L1 Info Tree settings.
func (c *Config) validateL1() error {
	if c.L1InfoTreeDBPath == "" {
		return fmt.Errorf("l1InfoTreeDBPath is required")
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
