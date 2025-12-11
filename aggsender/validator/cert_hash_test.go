package validator

import (
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/tree"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestHashCertificateToSign(t *testing.T) {
	t.Run("should hash certificate with empty imported bridge exits", func(t *testing.T) {
		cert := &agglayertypes.Certificate{
			NetworkID:           1,
			Height:              100,
			NewLocalExitRoot:    common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
			ImportedBridgeExits: nil,
		}

		hash, err := HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0x6e06392739303bf2162efe4d887b981eb36e88e90ec930e0c6b12f862e3e0ab2", hash.String())
	})

	t.Run("error hashing invalid cert ", func(t *testing.T) {
		cert := &agglayertypes.Certificate{
			NetworkID:           1,
			Height:              100,
			NewLocalExitRoot:    common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
			ImportedBridgeExits: nil,
			BridgeExits: []*agglayertypes.BridgeExit{
				{},
			},
		}
		_, err := HashCertificateToSign(cert)
		require.Error(t, err)
	})

	t.Run("check imported fields on hash", func(t *testing.T) {
		cert := getTestCert(t)
		hash, err := HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0xfeed673df1cdbe38aef628f0417c22ce4439c64f452090b5b4845ba799a9b10e", hash.String())
		cert.NetworkID += 1
		hash, err = HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0xbc2d8f9ea5e2b024d5007139c2b5d282d4e9a2e745340960dc5a1bc0b5703524", hash.String())
		cert.Height += 1
		hash, err = HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0xa504f0f8deceb412de5902c2acec8565eab9f2ae3d70e6262615fab88c317a14", hash.String())
		hash, err = HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0xa504f0f8deceb412de5902c2acec8565eab9f2ae3d70e6262615fab88c317a14", hash.String())
	})
}

func TestCertificateIdHash(t *testing.T) {
	cert := getTestCert(t)
	hash, err := HashCertificateToSign(cert)
	require.NoError(t, err)
	require.Equal(t, "0xfeed673df1cdbe38aef628f0417c22ce4439c64f452090b5b4845ba799a9b10e", hash.String())
}

// Returns aggsender and agglayer cert
func getTestCert(t *testing.T) *agglayertypes.Certificate {
	t.Helper()

	return &agglayertypes.Certificate{
		AggchainData: &agglayertypes.AggchainDataProof{
			Proof:          []byte{0x01},
			AggchainParams: common.HexToHash("0x010203"),
		},
		NetworkID:           1,
		Height:              100,
		PrevLocalExitRoot:   common.HexToHash("0x010201"),
		NewLocalExitRoot:    common.HexToHash("0x010202"),
		CustomChainData:     []byte{0x1, 0x2, 0x3},
		L1InfoTreeLeafCount: 11,
		BridgeExits: []*agglayertypes.BridgeExit{
			{
				LeafType: bridgetypes.LeafTypeAsset,
				TokenInfo: &agglayertypes.TokenInfo{
					OriginNetwork:      2,
					OriginTokenAddress: common.HexToAddress("0x010203"),
				},
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x010204"),
				Amount:             big.NewInt(100),
			},
		},
		ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{
			{
				BridgeExit: &agglayertypes.BridgeExit{
					LeafType: bridgetypes.LeafTypeAsset,
					TokenInfo: &agglayertypes.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x01111"),
					},
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x011112"),
					Amount:             big.NewInt(101),
				},
				GlobalIndex: &agglayertypes.GlobalIndex{
					MainnetFlag: true,
					RollupIndex: 0,
					LeafIndex:   1,
				},
				ClaimData: &agglayertypes.ClaimFromMainnet{
					ProofLeafMER: &agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x010203"),
						Proof: tree.EmptyProof,
					},
					ProofGERToL1Root: &agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x0102011"),
						Proof: tree.EmptyProof,
					},
					L1Leaf: &agglayertypes.L1InfoTreeLeaf{
						L1InfoTreeIndex: 1,
						RollupExitRoot:  common.HexToHash("0x0102012"),
						MainnetExitRoot: common.HexToHash("0x0102013"),
						Inner: &agglayertypes.L1InfoTreeLeafInner{
							GlobalExitRoot: common.HexToHash("0x0102014"),
							BlockHash:      common.HexToHash("0x0102015"),
							Timestamp:      1234567890,
						},
					},
				},
			},
			{
				BridgeExit: &agglayertypes.BridgeExit{
					LeafType: bridgetypes.LeafTypeMessage,
					TokenInfo: &agglayertypes.TokenInfo{
						OriginNetwork:      11,
						OriginTokenAddress: common.HexToAddress("0x011"),
					},
					DestinationNetwork: 22,
					DestinationAddress: common.HexToAddress("0x012"),
				},
				GlobalIndex: &agglayertypes.GlobalIndex{
					MainnetFlag: false,
					RollupIndex: 11,
					LeafIndex:   2,
				},
				ClaimData: &agglayertypes.ClaimFromRollup{
					ProofLeafLER: &agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x0112"),
						Proof: tree.EmptyProof,
					},
					ProofGERToL1Root: &agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x0122"),
						Proof: tree.EmptyProof,
					},
					ProofLERToRER: &agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x0123"),
						Proof: tree.EmptyProof,
					},
					L1Leaf: &agglayertypes.L1InfoTreeLeaf{
						L1InfoTreeIndex: 2,
						RollupExitRoot:  common.HexToHash("0x11"),
						MainnetExitRoot: common.HexToHash("0x12"),
						Inner: &agglayertypes.L1InfoTreeLeafInner{
							GlobalExitRoot: common.HexToHash("0x13"),
							BlockHash:      common.HexToHash("0x14"),
							Timestamp:      122222,
						},
					},
				},
			},
		},
	}
}
