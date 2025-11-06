package aggoracle

import (
	"github.com/agglayer/aggkit/aggoracle/chaingersender"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/grpc"
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
	WaitPeriodNextGER        types.Duration           `mapstructure:"WaitPeriodNextGER"`
	EVMSender                chaingersender.EVMConfig `mapstructure:"EVMSender"`
	EnableAggOracleCommittee bool                     `mapstructure:"EnableAggOracleCommittee"`
	EnableValidatorSigned    bool                     `mapstructure:"EnableValidatorSigned"`
	ValidatorClient          grpc.ClientConfig        `mapstructure:"ValidatorClient"`
}
