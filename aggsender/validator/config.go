package validator

import (
	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/config/types"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	signertypes "github.com/agglayer/go_signer/signer/types"
	ethCommon "github.com/ethereum/go-ethereum/common"
)

// Config defines the configuration for the validator validator service.
type Config struct {
	// EnableRPC is a flag to enable the RPC for validator
	EnableRPC bool `mapstructure:"EnableRPC"`
	// Signer is the key which is used to sign valid certificates
	Signer signertypes.SignerConfig `mapstructure:"Signer"`
	// ServerConfig contains the configuration for the gRPC server.
	ServerConfig aggkitgrpc.ServerConfig `mapstructure:"ServerConfig"`
	// MaxCertSize is the maximum size of the certificate (the emitted certificate cannot be bigger that this size)
	// 0 is infinite
	MaxCertSize uint `mapstructure:"MaxCertSize"`
	// MaxL2BlockNumber is the last L2 block number that is going to be included in a certificate
	// 0 means disabled
	MaxL2BlockNumber uint64 `mapstructure:"MaxL2BlockNumber"`
	// DelayBetweenRetries is the delay between retries:
	//  is used on store Certificate and also in initial check
	DelayBetweenRetries types.Duration `mapstructure:"DelayBetweenRetries"`
	// LerQuerier contains the configuration for the LER querier
	// which is used to query the LER data from the RollupManager contract
	LerQuerier LerQuerierConfig `mapstructure:"LerQuerierConfig"`
	// PPConfig specific configuration for Pessimistic mode
	PPConfig `mapstructure:"PPConfig"`
	// AgglayerClient is the Agglayer gRPC client configuration
	AgglayerClient agglayer.ClientConfig `mapstructure:"AgglayerClient"`
	// BridgeQuerier contains the configuration for the bridge querier
	BridgeQuerier BridgeQuerierConfig `mapstructure:"BridgeQuerier"`
}

type PPConfig struct {
	// RequireOneBridgeInPPCertificate is a flag to force the validator to have at least one bridge exit
	// for the Pessimistic Proof certificates
	RequireOneBridgeInPPCertificate bool `mapstructure:"RequireOneBridgeInPPCertificate"`
}

type LerQuerierConfig struct {
	// RollupManagerAddr is the address of the RollupManager contract on L1
	RollupManagerAddr ethCommon.Address `mapstructure:"RollupManagerAddr"`
	// RollupCreationBlockL1 is the block number when the rollup was created on L1
	RollupCreationBlockL1 uint64 `mapstructure:"RollupCreationBlockL1"`
}

type BridgeQuerierConfig struct {
	// BridgeL2SovereignAddr is the address of the bridge L2 sovereign contract on L2 sovereign chain
	BridgeL2SovereignAddr ethCommon.Address `mapstructure:"BridgeL2SovereignAddr"`
}
