package optimistic

import (
	signertypes "github.com/agglayer/go_signer/signer/types"
	ethCommon "github.com/ethereum/go-ethereum/common"
)

// Config holds the configuration for the Optimistic Mode
type Config struct {
	// AggchainFEPAddr is the L1 address of the AggchainFEP contract.
	AggchainFEPAddr ethCommon.Address `mapstructure:"AggchainFEPAddr"`
	// SignPrivateKey is the private key used to sign the optimistic proofs, must be trustedSequencer.
	SignPrivateKey signertypes.SignerConfig `mapstructure:"SignPrivateKey"`
	// OpNodeURL is the URL of the OpNode service used to fetch aggregation proof public value
	OpNodeURL string `mapstructure:"OpNodeURL"`
}
