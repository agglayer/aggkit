package optimistic

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestSignatureOptimisticData_Hash(t *testing.T) {
	aggregationProofPublicValues := &AggregationProofPublicValues{
		l1Head:           common.HexToHash("0x502cbcfe9aa2a7c4fbd1fcf81ce71be6f1a79a904b31a2b1cf27e5179f970890"),
		l2PreRoot:        common.HexToHash("0xb744b55eba3192d84812aa068e6db062cdccce9364d77515dee1ac3ac9e4a175"),
		claimRoot:        common.HexToHash("0x98280091281a3d554b53537892f86cbb3a38ff83528c39ac0cf52be251269a7d"),
		l2BlockNumber:    126697,
		rollupConfigHash: common.HexToHash("0xfd94d7ab6f4376bbb317864bd08cd240bff6f99dbec0755db1aa8e5ef0705a4a"),
		multiBlockVKey:   common.HexToHash("0x35882a76205af8c12eaeea7551ff8dbc392dc2a95b0f7f31660a5468237d4434"),
		proverAddress:    common.HexToAddress("0x4ce23a785114db45ac6351e02f0de440845351af"),
	}
	aHash, err := aggregationProofPublicValues.Hash()
	require.NoError(t, err, "Hashing should not return an error")

	signData := &OptimisticSignatureData{
		aggregationProofPublicValuesHash: aHash,
		newLocalExitRoot:                 common.HexToHash("0x81b8a2cf7a80538dee49ae721a87655b080523d37cdad80c6a002a33e91c96cb"),
		commitImportedBridgeExits:        common.HexToHash("0x1b2d35e62df05e64b5987fa70c318ccabb08ce181818c9c88851ac15da9d277a"),
	}
	hash := signData.Hash()
	expectedHash := common.HexToHash("0x30ab2b423a824db41a33d05756e59b1dbc46b3ef41a70750bceb3c7b7324ebc1")
	require.Equal(t, expectedHash, hash, "Hash should match the expected value")
}

func TestSignatureOptimisticData_CommitImportedBrige(t *testing.T) {
	g1 := new(big.Int)
	_, ok := g1.SetString("0x100000000000346b0", 0)
	require.True(t, ok, "BigInt should be set correctly from hex string")
	g2 := new(big.Int)
	_, ok = g2.SetString("0x4000054c8", 0)
	require.True(t, ok, "BigInt should be set correctly from hex string")

	data := &optimisticCommitImportedBrigesData{
		bridges: []optimisticCommitImportedBrigeData{
			{
				globalIndex:    g1,
				bridgeExitHash: common.HexToHash("0x637a82baa4451ff75e386d0f1df266ef1f8d6fe2e155cf2e071b5a05b2838035"),
			},
			{
				globalIndex:    g2,
				bridgeExitHash: common.HexToHash("0x855f364671260b1143962defe40ab63a0ec76b20a773f594f3c7bd0d87d6c27c"),
			},
		},
	}
	expectedHash := common.HexToHash("0x1b2d35e62df05e64b5987fa70c318ccabb08ce181818c9c88851ac15da9d277a")
	require.Equal(t, expectedHash, data.Hash(), "Hash should match the expected value")

}
