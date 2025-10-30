package l1infotreesync

import (
	"github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

type Config struct {
	// DBPath is the path of the database where the L1 Info Tree data will be stored
	DBPath string `mapstructure:"DBPath"`
	// GlobalExitRootAddr is the address of the GlobalExitRoot manager contract on L1
	GlobalExitRootAddr common.Address `mapstructure:"GlobalExitRootAddr"`
	// RollupManagerAddr is the address of the RollupManager/AgglayerManager contract
	RollupManagerAddr common.Address `mapstructure:"RollupManagerAddr"`
	// Possible values: LatestBlock, SafeBlock, PendingBlock, FinalizedBlock, EarliestBlock
	BlockFinality aggkittypes.BlockNumberFinality `jsonschema:"enum=LatestBlock,enum=SafeBlock,enum=PendingBlock,enum=FinalizedBlock,enum=EarliestBlock" mapstructure:"BlockFinality"` //nolint:lll
	// SyncBlockChunkSize is the amount of blocks that will be queried to the client on each request
	SyncBlockChunkSize uint64 `mapstructure:"SyncBlockChunkSize"`
	URLRPCL1           string `mapstructure:"URLRPCL1"`
	// WaitForNewBlocksPeriod time that will be waited when the synchronizer has queries for new blocks
	WaitForNewBlocksPeriod types.Duration `mapstructure:"WaitForNewBlocksPeriod"`
	// InitialBlock is the first block that will be queried when starting the synchronization from scratch
	InitialBlock uint64 `mapstructure:"InitialBlock"`
	// RetryAfterErrorPeriod is the time that will be waited when an unexpected error happens before retry
	RetryAfterErrorPeriod types.Duration `mapstructure:"RetryAfterErrorPeriod"`
	// MaxRetryAttemptsAfterError is the maximum number of consecutive attempts that will happen before panicing
	MaxRetryAttemptsAfterError int `mapstructure:"MaxRetryAttemptsAfterError"`
	// RequireStorageContentCompatibility is true it's mandatory that data stored in the database
	// is compatible with the running environment
	RequireStorageContentCompatibility bool `mapstructure:"RequireStorageContentCompatibility"`
}
