package optimistichash

import (
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
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

func TestNewCommitImportedBrigesData(t *testing.T) {
	claims := []bridgesync.Claim{
		{
			GlobalIndex:        big.NewInt(12345),
			IsMessage:          false,
			OriginNetwork:      1,
			OriginAddress:      common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
			DestinationNetwork: 2,
			DestinationAddress: common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12"),
			Amount:             big.NewInt(1000),
			Metadata:           []byte("metadata1"),
		},
		{
			GlobalIndex:        big.NewInt(67890),
			IsMessage:          true,
			OriginNetwork:      3,
			OriginAddress:      common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdef"),
			DestinationNetwork: 4,
			DestinationAddress: common.HexToAddress("0x1234561234561234561234561234561234561234"),
			Amount:             big.NewInt(2000),
			Metadata:           []byte("metadata2"),
		},
	}

	data := newCommitImportedBrigesData(claims)

	require.NotNil(t, data, "Data should not be nil")
	require.Len(t, data.bridges, len(claims), "Number of bridges should match the number of claims")

	for i, claim := range claims {
		require.Equal(t, claim.GlobalIndex, data.bridges[i].globalIndex, "GlobalIndex should match")
		require.NotNil(t, data.bridges[i].bridgeExitHash, "BridgeExitHash should not be nil")
	}
}
func TestSetBridgeExitHash(t *testing.T) {
	claim := &bridgesync.Claim{
		GlobalIndex:        big.NewInt(12345),
		IsMessage:          false,
		OriginNetwork:      1,
		OriginAddress:      common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		DestinationNetwork: 2,
		DestinationAddress: common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12"),
		Amount:             big.NewInt(1000),
		Metadata:           []byte("metadata1"),
	}

	data := optimisticCommitImportedBrigeData{}
	data.setBridgeExitHash(claim)

	require.NotNil(t, data.bridgeExitHash, "BridgeExitHash should not be nil")

	leafType := agglayertypes.LeafTypeAsset
	if claim.IsMessage {
		leafType = agglayertypes.LeafTypeMessage
	}

	expectedBridgeExit := agglayertypes.BridgeExit{
		LeafType: leafType,
		TokenInfo: &agglayertypes.TokenInfo{
			OriginNetwork:      claim.OriginNetwork,
			OriginTokenAddress: claim.OriginAddress,
		},
		DestinationNetwork: claim.DestinationNetwork,
		DestinationAddress: claim.DestinationAddress,
		Amount:             claim.Amount,
		Metadata:           claim.Metadata,
	}

	expectedHash := expectedBridgeExit.Hash()
	require.Equal(t, expectedHash, data.bridgeExitHash, "BridgeExitHash should match the expected hash")
}
