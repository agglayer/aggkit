package optimistichash

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

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
	require.Equal(t, expectedHash, data.hash(), "Hash should match the expected value")

}
