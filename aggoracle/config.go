package aggoracle

import (
	"github.com/agglayer/aggkit/aggoracle/chaingersender"
	"github.com/agglayer/aggkit/config/types"
)

type TargetChainType string

const (
	EVMChain TargetChainType = "EVM"
)

var (
	SupportedChainTypes = []TargetChainType{EVMChain}
)

type Config struct {
	TargetChainType TargetChainType `mapstructure:"TargetChainType"`
	URLRPCL1        string          `mapstructure:"URLRPCL1"`
	// BlockFinality indicates the status of the blocks that will be queried in order to sync
	BlockFinality     string                   `jsonschema:"enum=LatestBlock, enum=SafeBlock, enum=PendingBlock, enum=FinalizedBlock, enum=EarliestBlock" mapstructure:"BlockFinality"` //nolint:lll
	WaitPeriodNextGER types.Duration           `mapstructure:"WaitPeriodNextGER"`
	EVMSender         chaingersender.EVMConfig `mapstructure:"EVMSender"`
}

// ApplyL2ChainID copies L2ChainID to the L1ChainID in the Etherman config
func (c *Config) ApplyL2ChainID(l2ChainID uint64) {
	c.EVMSender.EthTxManager.Etherman.L1ChainID = l2ChainID
}
