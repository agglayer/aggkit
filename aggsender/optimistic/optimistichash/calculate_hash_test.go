package optimistichash

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestSignatureOptimisticData_Hash(t *testing.T) {
	aggregationProofPublicValues := &AggregationProofPublicValues{
		L1Head:           common.HexToHash("0x502cbcfe9aa2a7c4fbd1fcf81ce71be6f1a79a904b31a2b1cf27e5179f970890"),
		L2PreRoot:        common.HexToHash("0xb744b55eba3192d84812aa068e6db062cdccce9364d77515dee1ac3ac9e4a175"),
		ClaimRoot:        common.HexToHash("0x98280091281a3d554b53537892f86cbb3a38ff83528c39ac0cf52be251269a7d"),
		L2BlockNumber:    126697,
		RollupConfigHash: common.HexToHash("0xfd94d7ab6f4376bbb317864bd08cd240bff6f99dbec0755db1aa8e5ef0705a4a"),
		MultiBlockVKey:   common.HexToHash("0x35882a76205af8c12eaeea7551ff8dbc392dc2a95b0f7f31660a5468237d4434"),
		ProverAddress:    common.HexToAddress("0x4ce23a785114db45ac6351e02f0de440845351af"),
	}
	aHash, err := aggregationProofPublicValues.Hash()
	require.NoError(t, err, "Hashing should not return an error")

	signData := &OptimisticSignatureData{
		AggregationProofPublicValuesHash: aHash,
		NewLocalExitRoot:                 common.HexToHash("0x81b8a2cf7a80538dee49ae721a87655b080523d37cdad80c6a002a33e91c96cb"),
		CommitImportedBridgeExits:        common.HexToHash("0x1b2d35e62df05e64b5987fa70c318ccabb08ce181818c9c88851ac15da9d277a"),
	}
	hash := signData.Hash()
	expectedHash := common.HexToHash("0x30ab2b423a824db41a33d05756e59b1dbc46b3ef41a70750bceb3c7b7324ebc1")
	require.Equal(t, expectedHash, hash, "Hash should match the expected value")
}

func TestSignatureOptimisticData_String(t *testing.T) {
	signData := &OptimisticSignatureData{
		AggregationProofPublicValuesHash: common.HexToHash("0x502cbcfe9aa2a7c4fbd1fcf81ce71be6f1a79a904b31a2b1cf27e5179f970890"),
		NewLocalExitRoot:                 common.HexToHash("0x81b8a2cf7a80538dee49ae721a87655b080523d37cdad80c6a002a33e91c96cb"),
		CommitImportedBridgeExits:        common.HexToHash("0x1b2d35e62df05e64b5987fa70c318ccabb08ce181818c9c88851ac15da9d277a"),
	}
	expectedString := "OptimisticSignatureData{AggregationProofPublicValuesHash: 0x502cbcfe9aa2a7c4fbd1fcf81ce71be6f1a79a904b31a2b1cf27e5179f970890, NewLocalExitRoot: 0x81b8a2cf7a80538dee49ae721a87655b080523d37cdad80c6a002a33e91c96cb, CommitImportedBridgeExits: 0x1b2d35e62df05e64b5987fa70c318ccabb08ce181818c9c88851ac15da9d277a}"
	require.Equal(t, expectedString, signData.String(), "String representation should match the expected value")
}
