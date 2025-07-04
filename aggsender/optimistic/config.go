package optimistic

import (
	signertypes "github.com/agglayer/go_signer/signer/types"
	ethCommon "github.com/ethereum/go-ethereum/common"
)

// Config holds the configuration for the Optimistic Mode
type Config struct {
	// SovereignRollupAddr is the L1 address of the AggchainFEP contract.
	SovereignRollupAddr ethCommon.Address `mapstructure:"SovereignRollupAddr"`
	// TrustedSequencerKey is the private key used to sign the optimistic proofs, must be trustedSequencer.
	TrustedSequencerKey signertypes.SignerConfig `mapstructure:"TrustedSequencerKey"`
	// OpNodeURL is the URL of the OpNode service used to fetch aggregation proof public value
	OpNodeURL string `mapstructure:"OpNodeURL"`
	// RequireKeyMatchTrustedSequencer Enable sanity check that the signer public key matches
	// the trusted sequencer address.
	// This is useful to ensure that the signer is the trusted sequencer, and not a random signer.
	RequireKeyMatchTrustedSequencer bool `mapstructure:"RequireKeyMatchTrustedSequencer"`
}
