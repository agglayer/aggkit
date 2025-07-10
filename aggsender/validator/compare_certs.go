package validator

import (
	"bytes"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
)

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
		msg1 := fmt.Sprintf("Expected: %s", expectedCertificate.Metadata.Hex())
		metadataUnmarshal, err := types.NewCertificateMetadataFromHash(expectedCertificate.Metadata)
		if err != nil {
			msg1 += fmt.Sprintf(" Error: %v", err)
		} else {
			msg1 += fmt.Sprintf(" (%s)", metadataUnmarshal.String())
		}
		msg2 := fmt.Sprintf("Certificate: %s", validatingCertificate.Metadata.Hex())
		metadataUnmarshal, err = types.NewCertificateMetadataFromHash(validatingCertificate.Metadata)
		if err != nil {
			msg2 += fmt.Sprintf(" Error: %v", err)
		} else {
			msg2 += fmt.Sprintf(" (%s)", metadataUnmarshal.String())
		}
		diffs = append(diffs, fmt.Sprintf("Metadata mismatch. %s, %s",
			msg1, msg2))
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
	// ImportedBridgeExit
	diffs = append(diffs, DiffsImportedBridgeExit(expectedCertificate.ImportedBridgeExits,
		validatingCertificate.ImportedBridgeExits)...)

	return diffs
}

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

func DiffsImportedBridgeExit(expected []*agglayertypes.ImportedBridgeExit,
	validating []*agglayertypes.ImportedBridgeExit) []string {
	diffs := make([]string, 0)
	if len(expected) != len(validating) {
		diffs = append(diffs, fmt.Sprintf("BridgeExits length mismatch. Expected: %d, Certificate: %d",
			len(expected), len(validating)))
		return diffs
	}
	for i, expectedImportedBridge := range expected {
		importedBridgeValidating := validating[i]
		if importedBridgeValidating.Hash() != expectedImportedBridge.Hash() {
			diffs = append(diffs, fmt.Sprintf("ImportedBridgeExit %d hash mismatch.\n Expected: %s,\n"+
				"Certificate: %s",
				i, expectedImportedBridge.String(), importedBridgeValidating.String()))
		}
	}
	return diffs
}
