package common

import "fmt"

const (
	// AGGORACLE name to identify the aggoracle component
	AGGORACLE = "aggoracle"
	// BRIDGE name to identify the bridge component (have RPC)
	BRIDGE = "bridge"
	// PROVER name to identify the prover component
	PROVER = "prover"
	// AGGSENDER name to identify the aggsender component
	AGGSENDER = "aggsender"
	// L1INFOTREESYNC name to identify the l1infotreesync component
	L1INFOTREESYNC = "l1infotreesync"
	// L2BRIDGESYNC name to identify the l2 bridge sync component
	L2BRIDGESYNC = "l2bridgesync"
	// L1BRIDGESYNC name to identify the l1 bridge sync component
	L1BRIDGESYNC = "l1bridgesync"
	// L2GERSYNC name to identify the l2 ger sync component
	L2GERSYNC = "l2gersync"
	// AGGCHAINPROOFGEN name to identify the aggchain-proof-gen component
	AGGCHAINPROOFGEN = "aggchain-proof-gen"
	// AGGSENDERVALIDATOR runs aggsender certificate validator
	AGGSENDERVALIDATOR = "aggsender-validator"
)

// ValidateComponents validates that all provided components are known/supported
func ValidateComponents(components []string) error {
	validComponents := map[string]bool{
		AGGORACLE:          true,
		BRIDGE:             true,
		PROVER:             true,
		AGGSENDER:          true,
		L1INFOTREESYNC:     true,
		L2BRIDGESYNC:       true,
		L1BRIDGESYNC:       true,
		L2GERSYNC:          true,
		AGGCHAINPROOFGEN:   true,
		AGGSENDERVALIDATOR: true,
	}

	for _, component := range components {
		if !validComponents[component] {
			return fmt.Errorf("unknown component: %s. Valid components are: %s, %s, %s, %s, %s, %s, %s, %s, %s",
				component,
				AGGORACLE,
				BRIDGE,
				AGGSENDER,
				AGGCHAINPROOFGEN,
				AGGSENDERVALIDATOR,
				L1INFOTREESYNC,
				L2BRIDGESYNC,
				L1BRIDGESYNC,
				L2GERSYNC)
		}
	}

	return nil
}
