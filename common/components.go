package common

import (
	"fmt"
	"sort"
	"strings"
)

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
	// L2CLAIMSYNC name to identify the l2 claim sync component
	L2CLAIMSYNC = "l2claimsync"
)

// ValidateComponents validates that all provided components are known/supported.
func ValidateComponents(components []string) error {
	validComponents := map[string]struct{}{
		AGGORACLE:          {},
		BRIDGE:             {},
		PROVER:             {},
		AGGSENDER:          {},
		L1INFOTREESYNC:     {},
		L2BRIDGESYNC:       {},
		L1BRIDGESYNC:       {},
		L2GERSYNC:          {},
		AGGCHAINPROOFGEN:   {},
		AGGSENDERVALIDATOR: {},
		L2CLAIMSYNC:        {},
	}

	// build a sorted list of valid component names for error messages
	keys := make([]string, 0, len(validComponents))
	for k := range validComponents {
		keys = append(keys, k)
	}
	sort.Strings(keys) // ensures deterministic ordering
	validList := strings.Join(keys, ", ")

	for _, component := range components {
		if _, ok := validComponents[component]; !ok {
			return fmt.Errorf("unknown component: %s. Valid components are: %s",
				component, validList)
		}
	}

	return nil
}
