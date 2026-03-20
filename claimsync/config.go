package claimsync

import (
	"fmt"

	"github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

type ConfigEmbedded struct {
	// DBQueryTimeout is the timeout for database operations (queries, transactions)
	// This is separate from HTTP timeouts to allow database operations more time when needed
	DBQueryTimeout types.Duration `mapstructure:"DBQueryTimeout"`
	// BridgeAddr is the address of the bridge smart contract
	BridgeAddr common.Address `mapstructure:"BridgeAddr"`
}

type ConfigStandalone struct {
	ConfigEmbedded `mapstructure:",squash"`
	// DBPath path of the DB
	DBPath string `mapstructure:"DBPath"`
	// BlockFinality indicates the status of the blocks that will be queried in order to sync
	BlockFinality aggkittypes.BlockNumberFinality `jsonschema:"enum=LatestBlock, enum=SafeBlock, enum=PendingBlock, enum=FinalizedBlock, enum=EarliestBlock" mapstructure:"BlockFinality"` //nolint:lll
	// InitialBlockNum is the first block that will be queried when starting the synchronization from scratch.
	// It should be a number equal or bellow the creation of the bridge contract
	InitialBlockNum uint64 `mapstructure:"InitialBlockNum"`
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
	// AutoStart controls whether the synchronizer should start automatically after initialization.
	// Possible values:
	//   - "true": automatically starts after initialization using InitialBlockNum
	//   - "false": does not start automatically; requires manual start
	//   - "auto": automatically decides based on which component is active
	AutoStart types.TrueFalseAutoMode `jsonschema:"enum=true, enum=false, enum=auto" mapstructure:"AutoStart"`
}

func (c ConfigEmbedded) Validate() error {
	return nil
}

func (c ConfigStandalone) Validate() error {
	if err := c.ConfigEmbedded.Validate(); err != nil {
		return err
	}
	if err := c.BlockFinality.Validate(); err != nil {
		return fmt.Errorf("invalid BlockFinality configuration: %w", err)
	}
	if err := c.AutoStart.Validate("AutoStart"); err != nil {
		return err
	}
	return nil
}
