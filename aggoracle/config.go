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
	TargetChainType          TargetChainType          `mapstructure:"TargetChainType"`
	URLRPCL1                 string                   `mapstructure:"URLRPCL1"`
	WaitPeriodNextGER        types.Duration           `mapstructure:"WaitPeriodNextGER"`
	EVMSender                chaingersender.EVMConfig `mapstructure:"EVMSender"`
	EnableAggOracleCommittee bool                     `mapstructure:"EnableAggOracleCommittee"`
}
