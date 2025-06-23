package reorgdetector

import (
	"time"

	"github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
)

const (
	defaultCheckReorgsInterval = 2 * time.Second
)

// Config is the configuration for the reorg detector
type Config struct {
	// DBPath is the path to the database
	DBPath string `mapstructure:"DBPath"`

	// CheckReorgsInterval is the interval to check for reorgs in tracked blocks
	CheckReorgsInterval types.Duration `mapstructure:"CheckReorgsInterval"`
	// FinalizedBlockType indicates the status of the blocks that will be queried in order to sync
	// if finalizedBlock == "LatestBlock" then it's disabled and we assume the network has no chances of reorgs
	FinalizedBlock aggkittypes.BlockNumberFinality `jsonschema:"enum=LatestBlock, enum=SafeBlock, enum=PendingBlock, enum=FinalizedBlock, enum=EarliestBlock" mapstructure:"FinalizedBlock"` //nolint:lll
}

// GetCheckReorgsInterval returns the interval to check for reorgs in tracked blocks
func (c *Config) GetCheckReorgsInterval() time.Duration {
	if c.CheckReorgsInterval.Duration == 0 {
		return defaultCheckReorgsInterval
	}

	return c.CheckReorgsInterval.Duration
}
