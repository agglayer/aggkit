package l1infotreesync

import (
	"github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
)

type Config struct {
	DBPath                     string         `mapstructure:"DBPath"`
	GlobalExitRootAddr         common.Address `mapstructure:"GlobalExitRootAddr"`
	RollupManagerAddr          common.Address `mapstructure:"RollupManagerAddr"`
	SyncBlockChunkSize         uint64         `mapstructure:"SyncBlockChunkSize"`
	URLRPCL1                   string         `mapstructure:"URLRPCL1"`
	WaitForNewBlocksPeriod     types.Duration `mapstructure:"WaitForNewBlocksPeriod"`
	InitialBlock               uint64         `mapstructure:"InitialBlock"`
	RetryAfterErrorPeriod      types.Duration `mapstructure:"RetryAfterErrorPeriod"`
	MaxRetryAttemptsAfterError int            `mapstructure:"MaxRetryAttemptsAfterError"`
	// RequireStorageContentCompatibility is true it's mandatory that data stored in the database
	// is compatible with the running environment
	RequireStorageContentCompatibility bool `mapstructure:"RequireStorageContentCompatibility"`
}
