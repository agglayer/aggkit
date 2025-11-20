package validator

import (
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	aggkittypes "github.com/agglayer/aggkit/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	ethCommon "github.com/ethereum/go-ethereum/common"
)

var errInvalidSovereignRollupAddr = fmt.Errorf("SovereignRollupAddr must be set for AggchainProof mode")

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
	PPConfig PPConfig `mapstructure:"PPConfig"`
	// FEPConfig specific configuration for FEP mode
	FEPConfig FEPConfig `mapstructure:"FEPConfig"`
	// AgglayerClient is the Agglayer gRPC client configuration
	AgglayerClient agglayer.ClientConfig `mapstructure:"AgglayerClient"`
	// Mode is the mode of the AggSender Validator (regular pessimistic proof mode or the aggchain proof mode)
	Mode aggsendertypes.AggsenderMode `jsonschema:"enum=PessimisticProof, enum=AggchainProof, enum=Auto" mapstructure:"Mode"` //nolint:lll
	// RequireCommitteeMembershipCheck indicates whether to check if the validator is part of the committee
	RequireCommitteeMembershipCheck bool `mapstructure:"RequireCommitteeMembershipCheck"`
	// AgglayerBridgeL2Addr is the address of the bridge L2 sovereign contract on L2 sovereign chain
	AgglayerBridgeL2Addr ethCommon.Address `mapstructure:"AgglayerBridgeL2Addr"`
	// GERValidateConfig contains the configuration for the validation of GlobalExitRoots
	GERValidateConfig GERValidateConfig `mapstructure:"GERValidateConfig"`
}

// PPConfig contains specific configuration for Pessimistic Proof mode
type PPConfig struct {
	// RequireOneBridgeInPPCertificate is a flag to force the validator to have at least one bridge exit
	// for the Pessimistic Proof certificates
	RequireOneBridgeInPPCertificate bool `mapstructure:"RequireOneBridgeInPPCertificate"`
}

// FEPConfig contains specific configuration for FEP (AggchainProof) mode
type FEPConfig struct {
	// SovereignRollupAddr is the address of the sovereign rollup contract on L1
	SovereignRollupAddr ethCommon.Address `mapstructure:"SovereignRollupAddr"`
	// RequireNoBlockGap is true if the AggSender should not accept a gap between
	// lastBlock from lastCertificate and first block of FEP
	RequireNoBlockGap bool `mapstructure:"RequireNoBlockGap"`
	// OpNodeURL is the URL of the OP Node to query for op related data
	OpNodeURL string `mapstructure:"OpNodeURL"`
}

// LerQuerierConfig contains the configuration for the LER querier
type LerQuerierConfig struct {
	// RollupManagerAddr is the address of the RollupManager contract on L1
	RollupManagerAddr ethCommon.Address `mapstructure:"RollupManagerAddr"`
	// RollupCreationBlockL1 is the block number when the rollup was created on L1
	RollupCreationBlockL1 uint64 `mapstructure:"RollupCreationBlockL1"`
}

// GERValidateConfig contains the configuration for validating GlobalExitRoots
type GERValidateConfig struct {
	// GlobalExitRootL1Addr is the address of the GlobalExitRootManager contract on l1 chain
	GlobalExitRootL1Addr ethCommon.Address `mapstructure:"GlobalExitRootL1"`
	// BlockFinality indicates the finality of the blocks that will be used to validate the GERs
	// Possible values: PendingBlock, LatestBlock, SafeBlock, FinalizedBlock, EarliestBlock
	// (with an optional offset, e.g. SafeBlock/-5)
	// See also: aggkittypes.BlockNumberFinality
	BlockFinality aggkittypes.BlockNumberFinality `jsonschema:"enum=LatestBlock, enum=SafeBlock, enum=PendingBlock, enum=FinalizedBlock, enum=EarliestBlock" mapstructure:"BlockFinality"` //nolint:lll
}

// Validate checks if the GERValidateConfig is valid
func (g GERValidateConfig) Validate() error {
	if err := g.BlockFinality.Validate(); err != nil {
		return fmt.Errorf("invalid BlockFinality configuration: %w", err)
	}

	if g.GlobalExitRootL1Addr == aggkitcommon.ZeroAddress {
		return fmt.Errorf("GlobalExitRootL1Addr must be set")
	}

	return nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	err := c.Mode.Validate()
	if err != nil {
		return fmt.Errorf("invalid mode %s, must be one of PessimisticProof, AggchainProof, Auto: %w", c.Mode, err)
	}

	if c.Mode == aggsendertypes.AggchainProofMode {
		if c.FEPConfig.SovereignRollupAddr == aggkitcommon.ZeroAddress {
			return errInvalidSovereignRollupAddr
		}
	}

	if err := c.AgglayerClient.Validate(); err != nil {
		return fmt.Errorf("invalid agglayer client config: %w", err)
	}

	if err := c.GERValidateConfig.Validate(); err != nil {
		return fmt.Errorf("invalid GER validation config: %w", err)
	}

	return nil
}
