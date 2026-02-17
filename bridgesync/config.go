package bridgesync

import (
	"fmt"
	"strings"

	"github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// SyncFromInBridgesMode represents the mode for FromAddress extraction
type SyncFromInBridgesMode string

const (
	// SyncFromInBridgesTrue always extracts FromAddress using debug_traceTransaction
	SyncFromInBridgesTrue SyncFromInBridgesMode = "true"
	// SyncFromInBridgesFalse never extracts FromAddress
	SyncFromInBridgesFalse SyncFromInBridgesMode = "false"
	// SyncFromInBridgesAuto decides automatically based on whether BRIDGE component is active
	SyncFromInBridgesAuto SyncFromInBridgesMode = "auto"
)

// UnmarshalText implements encoding.TextUnmarshaler
func (m *SyncFromInBridgesMode) UnmarshalText(text []byte) error {
	str := strings.ToLower(strings.TrimSpace(string(text)))
	switch str {
	case "true":
		*m = SyncFromInBridgesTrue
	case "false":
		*m = SyncFromInBridgesFalse
	case "auto":
		*m = SyncFromInBridgesAuto
	default:
		return fmt.Errorf("invalid SyncFromInBridgesMode: %s (valid values: true, false, auto)", str)
	}
	return nil
}

// String returns the string representation
func (m SyncFromInBridgesMode) String() string {
	return string(m)
}

// Resolve converts the mode to a boolean, using the provided components list to resolve "auto"
func (m SyncFromInBridgesMode) Resolve(hasBridgeComponent bool) bool {
	switch m {
	case SyncFromInBridgesTrue:
		return true
	case SyncFromInBridgesFalse:
		return false
	case SyncFromInBridgesAuto:
		// If BRIDGE component is active, we need FromAddress extraction
		return hasBridgeComponent
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
	SyncFromInBridges SyncFromInBridgesMode `mapstructure:"SyncFromInBridges"`
}

// Validate checks if the configuration is valid
func (c Config) Validate() error {
	if err := c.BlockFinality.Validate(); err != nil {
		return fmt.Errorf("invalid BlockFinality configuration: %w", err)
	}
	// Validate SyncFromInBridges
	switch c.SyncFromInBridges {
	case SyncFromInBridgesTrue, SyncFromInBridgesFalse, SyncFromInBridgesAuto, "":
		// Valid values, including empty (will use default)
	default:
		return fmt.Errorf("invalid SyncFromInBridges value: %s (valid values: true, false, auto)", c.SyncFromInBridges)
	}
	return nil
}
