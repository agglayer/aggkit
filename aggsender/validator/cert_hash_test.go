package validator

import (
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
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
			Metadata:            [32]byte{1, 2, 3, 4, 5},
		}

		hash := HashCertificateToSign(cert)
		require.NotEqual(t, common.Hash{}, hash)
	})

	t.Run("should hash certificate with imported bridge exits", func(t *testing.T) {
		cert := &agglayertypes.Certificate{
			NetworkID:        1,
			Height:           100,
			NewLocalExitRoot: common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
			ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{
				&agglayertypes.ImportedBridgeExit{
					GlobalIndex: &agglayertypes.GlobalIndex{
						MainnetFlag: true,
						RollupIndex: 1,
						LeafIndex:   10,
					},
				},
				&agglayertypes.ImportedBridgeExit{
					GlobalIndex: &agglayertypes.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 2,
						LeafIndex:   20,
					},
				},
			},
			Metadata: [32]byte{1, 2, 3, 4, 5},
		}

		hash := HashCertificateToSign(cert)
		require.NotEqual(t, common.Hash{}, hash)
		require.Equal(t, "0x43d8f05fa6b02f43f1114ec1b73973361fa5930d15c6cc58e7614e59417ca7d0", hash.String())
	})

	t.Run("check imported fields on hash", func(t *testing.T) {
		cert := &agglayertypes.Certificate{
			NetworkID:        1,
			Height:           100,
			NewLocalExitRoot: common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
			ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{
				&agglayertypes.ImportedBridgeExit{
					GlobalIndex: &agglayertypes.GlobalIndex{
						MainnetFlag: true,
						RollupIndex: 1,
						LeafIndex:   10,
					},
				},
				&agglayertypes.ImportedBridgeExit{
					GlobalIndex: &agglayertypes.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 2,
						LeafIndex:   20,
					},
				},
			},
			Metadata: [32]byte{1, 2, 3, 4, 5},
		}

		require.Equal(t, "0x43d8f05fa6b02f43f1114ec1b73973361fa5930d15c6cc58e7614e59417ca7d0", HashCertificateToSign(cert).String())
		cert.NetworkID += 1
		require.Equal(t, "0x7b3a820e84307ecbaacde0af267ad50c9028aea6c43a63b6cbfe1d52474a49ad", HashCertificateToSign(cert).String())
		cert.Height += 1
		require.Equal(t, "0xfd290b1e07867ae1f3d63619ec8a534967b52497a034b3a56de8d92b2175add6", HashCertificateToSign(cert).String())
		cert.Metadata = [32]byte{6, 7, 8, 9, 10}
		require.Equal(t, "0x912dd941aadbad91de9079c8738180f0539578f8bc7c4cc18e0353d1c885ed96", HashCertificateToSign(cert).String())
	})

}
