package validator

import (
	"bytes"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
)

// DiffsCertificate compares two certificates and returns a slice of strings
// containing the differences between them. If both certificates are nil, it returns an empty slice.
func DiffsCertificate(
	expectedCertificate *agglayertypes.Certificate,
	validatingCertificate *agglayertypes.Certificate) []string {
	diffs := make([]string, 0)
	if validatingCertificate == nil && expectedCertificate == nil {
		return diffs
	}
	if validatingCertificate == nil || expectedCertificate == nil {
		diffs = append(diffs, "one of the certificates in comparison  is nil")
		return diffs
	}
	if validatingCertificate.NetworkID != expectedCertificate.NetworkID {
		diffs = append(diffs, fmt.Sprintf("network ID mismatch. Expected: %d, Certificate: %d",
			expectedCertificate.NetworkID, validatingCertificate.NetworkID))
	}
	if validatingCertificate.Height != expectedCertificate.Height {
		diffs = append(diffs, fmt.Sprintf("height mismatch. Expected: %d, Certificate: %d",
			expectedCertificate.Height, validatingCertificate.Height))
	}

	if validatingCertificate.PrevLocalExitRoot != expectedCertificate.PrevLocalExitRoot {
		diffs = append(diffs, fmt.Sprintf("prevLocalExitRoot mismatch. Expected: %s, Certificate: %s",
			expectedCertificate.PrevLocalExitRoot.Hex(), validatingCertificate.PrevLocalExitRoot.Hex()))
	}

	if validatingCertificate.NewLocalExitRoot != expectedCertificate.NewLocalExitRoot {
		diffs = append(diffs, fmt.Sprintf("NewLocalExitRoot mismatch. Expected: %s, Certificate: %s",
			expectedCertificate.NewLocalExitRoot.Hex(), validatingCertificate.NewLocalExitRoot.Hex()))
	}

	if validatingCertificate.Metadata != expectedCertificate.Metadata {
		diffs = append(diffs, fmt.Sprintf("Metadata mismatch. Expected: %s, Certificate: %s",
			expectedCertificate.Metadata.Hex(), validatingCertificate.Metadata.Hex()))
	}

	if !bytes.Equal(validatingCertificate.CustomChainData, expectedCertificate.CustomChainData) {
		diffs = append(diffs, fmt.Sprintf("CustomChainData mismatch. Expected: %x, Certificate: %x",
			expectedCertificate.CustomChainData, validatingCertificate.CustomChainData))
	}

	if validatingCertificate.L1InfoTreeLeafCount != expectedCertificate.L1InfoTreeLeafCount {
		diffs = append(diffs, fmt.Sprintf("L1InfoTreeLeafCount mismatch. Expected: %d, Certificate: %d",
			expectedCertificate.L1InfoTreeLeafCount, validatingCertificate.L1InfoTreeLeafCount))
	}

	// BridgeExits
	diffs = append(diffs, DiffsBridgeExits(expectedCertificate.BridgeExits, validatingCertificate.BridgeExits)...)

	return diffs
}

// DiffsBridgeExits compares two slices of BridgeExit and returns a slice of strings
// containing the differences between them.
func DiffsBridgeExits(
	expected []*agglayertypes.BridgeExit,
	validating []*agglayertypes.BridgeExit) []string {
	diffs := make([]string, 0)
	if len(expected) != len(validating) {
		diffs = append(diffs, fmt.Sprintf("BridgeExits length mismatch. Expected: %d, Certificate: %d",
			len(expected), len(validating)))
		return diffs
	}
	for i, expectedExit := range expected {
		bridgeValidating := validating[i]
		if bridgeValidating.Hash() != expectedExit.Hash() {
			diffs = append(diffs, fmt.Sprintf("BridgeExit %d hash mismatch. Expected: %s, Certificate: %s",
				i, expectedExit.Hash().Hex(), bridgeValidating.Hash().Hex()))
		}
	}
	return diffs
}
