package bridgesync

import (
	"fmt"
	"strings"

	"github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// TrueFalseAutoMode represents the mode for FromAddress extraction
type TrueFalseAutoMode string

const (
	// TrueMode always extracts FromAddress using debug_traceTransaction
	TrueMode TrueFalseAutoMode = "true"
	// FalseMode never extracts FromAddress
	FalseMode TrueFalseAutoMode = "false"
	// AutoMode decides automatically based on whether BRIDGE component is active
	AutoMode TrueFalseAutoMode = "auto"
)

// UnmarshalText implements encoding.TextUnmarshaler
func (m *TrueFalseAutoMode) UnmarshalText(text []byte) error {
	str := strings.ToLower(strings.TrimSpace(string(text)))
	switch str {
	case "true":
		*m = TrueMode
	case "false":
		*m = FalseMode
	case "auto":
		*m = AutoMode
	default:
		return fmt.Errorf("invalid TrueFalseAutoMode: value %s (valid values: true, false, auto)", str)
	}
	return nil
}

// String returns the string representation
func (m TrueFalseAutoMode) String() string {
	return string(m)
}

func (m TrueFalseAutoMode) Validate(fieldName string) error {
	cpy := m
	if err := cpy.UnmarshalText([]byte(m.String())); err != nil {
		return fmt.Errorf("invalid %s configuration: %w", fieldName, err)
	}
	return nil
}

// Resolve converts the mode to a boolean, using the provided components list to resolve "auto"
func (m TrueFalseAutoMode) Resolve(autoModeResult bool) bool {
	switch m {
	case TrueMode:
		return true
	case FalseMode:
		return false
	case AutoMode:
		// Resolve to auto mode
		return autoModeResult
	default:
		// Default to false
		return false
	}
}

type Config struct {
	// DBPath path of the DB
	DBPath string `mapstructure:"DBPath"`
	// BlockFinality indicates the status of the blocks that will be queried in order to sync
	BlockFinality aggkittypes.BlockNumberFinality `jsonschema:"enum=LatestBlock, enum=SafeBlock, enum=PendingBlock, enum=FinalizedBlock, enum=EarliestBlock" mapstructure:"BlockFinality"` //nolint:lll
	// InitialBlockNum is the first block that will be queried when starting the synchronization from scratch.
	// It should be a number equal or bellow the creation of the bridge contract
	InitialBlockNum uint64 `mapstructure:"InitialBlockNum"`
	// BridgeAddr is the address of the bridge smart contract
	BridgeAddr common.Address `mapstructure:"BridgeAddr"`
	// SyncBlockChunkSize is the amount of blocks that will be queried to the client on each request
	SyncBlockChunkSize uint64 `mapstructure:"SyncBlockChunkSize"`
	// RetryAfterErrorPeriod is the time that will be waited when an unexpected error happens before retry
	RetryAfterErrorPeriod types.Duration `mapstructure:"RetryAfterErrorPeriod"`
	// MaxRetryAttemptsAfterError is the maximum number of consecutive attempts that will happen before panicing.
	// Any number smaller than zero will be considered as unlimited retries
	MaxRetryAttemptsAfterError int `mapstructure:"MaxRetryAttemptsAfterError"`
	// WaitForNewBlocksPeriod time that will be waited when the synchronizer has reached the latest block
	WaitForNewBlocksPeriod types.Duration `mapstructure:"WaitForNewBlocksPeriod"`
	// RequireStorageContentCompatibility is true it's mandatory that data stored in the database
	// is compatible with the running environment
	RequireStorageContentCompatibility bool `mapstructure:"RequireStorageContentCompatibility"`
	// DBQueryTimeout is the timeout for database operations (queries, transactions)
	// This is separate from HTTP timeouts to allow database operations more time when needed
	DBQueryTimeout types.Duration `mapstructure:"DBQueryTimeout"`
	// SyncFromInBridges controls whether to extract FromAddress for bridge Asset events.
	// Possible values:
	//   - "true": always extracts FromAddress using debug_traceTransaction (requires archive node)
	//   - "false": never extracts FromAddress (no archive node needed)
	//   - "auto": automatically decides based on whether BRIDGE component is active
	// Note: TxnSender and ToAddress are always extracted via standard eth_getTransactionByHash.
	// Default: "auto"
	SyncFromInBridges TrueFalseAutoMode `jsonschema:"enum=true, enum=false, enum=auto" 	mapstructure:"SyncFromInBridges"`
	// EmbeddedClaimSync controls whether to use embedded claim synchronization mode.
	// If brridge-service is running then we must use embedded claim sync, if not it runs in standalone
	EmbeddedClaimSync TrueFalseAutoMode `jsonschema:"enum=true, enum=false, enum=auto" 	mapstructure:"EmbeddedClaimSync"`
	// SyncFromInBridgesResolved is the resolved boolean value of SyncFromInBridges after "auto" is evaluated.
	// Not read from config file; set programmatically after resolution.
	SyncFromInBridgesResolved *bool `mapstructure:"-"`
	// EmbeddedClaimSyncResolved is the resolved boolean value of EmbeddedClaimSync after "auto" is evaluated.
	// Not read from config file; set programmatically after resolution.
	EmbeddedClaimSyncResolved *bool `mapstructure:"-"`
}

// Validate checks if the configuration is valid
func (c Config) Validate() error {
	if err := c.BlockFinality.Validate(); err != nil {
		return fmt.Errorf("invalid BlockFinality configuration: %w", err)
	}
	// Validate SyncFromInBridges
	if err := c.SyncFromInBridges.Validate("SyncFromInBridges"); err != nil {
		return err
	}
	// Validate EmbeddedClaimSync
	if err := c.EmbeddedClaimSync.Validate("EmbeddedClaimSync"); err != nil {
		return err
	}
	return nil
}

// ResolvedString returns a string representation of the resolved configuration
// to log it
func (c *Config) ResolvedString() []string {
	var result []string
	if c.SyncFromInBridgesResolved != nil {
		result = append(result, fmt.Sprintf("SyncFromInBridges:%s -> %t", c.SyncFromInBridges, *c.SyncFromInBridgesResolved))
	} else {
		result = append(result, fmt.Sprintf("SyncFromInBridges: %s -> ???", c.SyncFromInBridges))
	}
	if c.EmbeddedClaimSyncResolved != nil {
		result = append(result, fmt.Sprintf("EmbeddedClaimSync:%s -> %t", c.EmbeddedClaimSync, *c.EmbeddedClaimSyncResolved))
	} else {
		result = append(result, fmt.Sprintf("EmbeddedClaimSync: %s -> ???", c.EmbeddedClaimSync))
	}
	return result

}
