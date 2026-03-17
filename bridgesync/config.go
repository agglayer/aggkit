package bridgesync

import (
	"fmt"

	"github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// TrueFalseAutoMode is an alias for config/types.TrueFalseAutoMode.
type TrueFalseAutoMode = types.TrueFalseAutoMode

// Re-export the TrueFalseAutoMode values from config/types.
var (
	// TrueMode always extracts FromAddress using debug_traceTransaction
	TrueMode = types.TrueMode
	// FalseMode never extracts FromAddress
	FalseMode = types.FalseMode
	// AutoMode decides automatically based on whether BRIDGE component is active
	AutoMode = types.AutoMode
)

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
	// SyncFromInBridges.Resolved is set programmatically after resolution; not read from config.
	SyncFromInBridges TrueFalseAutoMode `jsonschema:"enum=true, enum=false, enum=auto" mapstructure:"SyncFromInBridges"` //nolint:lll
}

// Validate checks if the configuration is valid
func (c Config) Validate() error {
	if err := c.BlockFinality.Validate(); err != nil {
		return fmt.Errorf("invalid BlockFinality configuration: %w", err)
	}
	// Validate SyncFromInBridges (empty is allowed — means not configured)
	if c.SyncFromInBridges.Mode != "" {
		var m TrueFalseAutoMode
		if err := m.UnmarshalText([]byte(c.SyncFromInBridges.Mode)); err != nil {
			return fmt.Errorf("invalid SyncFromInBridges value: %w", err)
		}
	}
	return nil
}

// ResolvedString returns a string representation of the resolved configuration
// to log it
func (c *Config) ResolvedString() []string {
	var result []string
	if c.SyncFromInBridges.Resolved != nil {
		result = append(result, fmt.Sprintf("SyncFromInBridges:%s -> %t", c.SyncFromInBridges, *c.SyncFromInBridges.Resolved))
	} else {
		result = append(result, fmt.Sprintf("SyncFromInBridges: %s -> ???", c.SyncFromInBridges))
	}
	return result
}
