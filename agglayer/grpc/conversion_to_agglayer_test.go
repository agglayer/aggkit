package grpc

import (
	"math/big"
	"testing"

	v1types "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/tree"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var exampleTestAgglayerCert = &agglayertypes.Certificate{
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
			LeafType: agglayertypes.LeafTypeAsset,
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
				LeafType: agglayertypes.LeafTypeAsset,
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
				LeafType: agglayertypes.LeafTypeMessage,
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

func TestConvertProtoCertToAgglayer(t *testing.T) {
	t.Run("nil certificate", func(t *testing.T) {
		result, err := ConvertProtoCertToAgglayer(nil)
		require.Nil(t, result)
		require.ErrorIs(t, err, ErrNilCertificate)
	})

	t.Run("nil PrevLocalExitRoot", func(t *testing.T) {
		protoCert, err := ConvertCertToProtoCertificate(exampleTestAgglayerCert)
		require.NoError(t, err)
		protoCert.PrevLocalExitRoot = nil
		result, err := ConvertProtoCertToAgglayer(protoCert)
		require.Nil(t, result)
		require.ErrorIs(t, err, ErrNilCertificate)
		require.ErrorContains(t, err, "Certificate has nil fields")
	})

	t.Run("nil NewLocalExitRoot", func(t *testing.T) {
		protoCert, err := ConvertCertToProtoCertificate(exampleTestAgglayerCert)
		require.NoError(t, err)
		protoCert.NewLocalExitRoot = nil
		result, err := ConvertProtoCertToAgglayer(protoCert)
		require.Nil(t, result)
		require.ErrorIs(t, err, ErrNilCertificate)
		require.ErrorContains(t, err, "Certificate has nil fields")
	})

	t.Run("nil L1InfoTreeLeafCount", func(t *testing.T) {
		protoCert, err := ConvertCertToProtoCertificate(exampleTestAgglayerCert)
		require.NoError(t, err)
		protoCert.AggchainData = nil
		protoCert.L1InfoTreeLeafCount = nil
		result, err := ConvertProtoCertToAgglayer(protoCert)
		require.Nil(t, result)
		require.ErrorIs(t, err, ErrNilCertificate)
		require.ErrorContains(t, err, "has nil L1InfoTreeLeafCount")
	})

	t.Run("undefined AggchainData", func(t *testing.T) {
		protoCert, err := ConvertCertToProtoCertificate(exampleTestAgglayerCert)
		require.NoError(t, err)
		protoCert.AggchainData = nil
		result, err := ConvertProtoCertToAgglayer(protoCert)
		require.NoError(t, err)
		require.Nil(t, result.AggchainData)
	})

	t.Run("successful conversion", func(t *testing.T) {
		protoCert, err := ConvertCertToProtoCertificate(exampleTestAgglayerCert)
		require.NoError(t, err)
		result, err := ConvertProtoCertToAgglayer(protoCert)
		require.NotNil(t, result)
		require.Equal(t, exampleTestAgglayerCert.CertificateID(), result.CertificateID())
		require.NoError(t, err)
	})

	t.Run("successful conversion - aggchain data signature", func(t *testing.T) {
		protoCert, err := ConvertCertToProtoCertificate(exampleTestAgglayerCert)
		require.NoError(t, err)
		protoCert.AggchainData = &v1types.AggchainData{
			Data: &v1types.AggchainData_Signature{
				Signature: &v1types.FixedBytes65{
					Value: common.HexToHash("0x0102030405060708090a0b0c0d0e0f1011121314151617181920212223242526").Bytes(),
				},
			},
		}
		result, err := ConvertProtoCertToAgglayer(protoCert)
		require.NotNil(t, result)
		require.Equal(t, exampleTestAgglayerCert.CertificateID(), result.CertificateID())
		require.NoError(t, err)
	})

	t.Run("error in bridge exits conversion", func(t *testing.T) {
		protoCert, err := ConvertCertToProtoCertificate(exampleTestAgglayerCert)
		require.NoError(t, err)
		protoCert.BridgeExits[0].TokenInfo = nil // This should cause an error
		result, err := ConvertProtoCertToAgglayer(protoCert)
		require.Error(t, err)
		require.Nil(t, result)
		require.ErrorContains(t, err, "error converting grpc bridge exits")
	})

	t.Run("error in imported bridge exits conversion", func(t *testing.T) {
		protoCert, err := ConvertCertToProtoCertificate(exampleTestAgglayerCert)
		require.NoError(t, err)
		protoCert.ImportedBridgeExits = []*v1types.ImportedBridgeExit{nil}
		result, err := ConvertProtoCertToAgglayer(protoCert)
		require.Error(t, err)
		require.Nil(t, result)
		require.ErrorContains(t, err, "error converting grpc imported bridge exits")
	})

	t.Run("error in aggchain data proof - not sp1 stark", func(t *testing.T) {
		protoCert, err := ConvertCertToProtoCertificate(exampleTestAgglayerCert)
		require.NoError(t, err)
		protoCert.AggchainData = &v1types.AggchainData{
			Data: &v1types.AggchainData_Generic{
				Generic: &v1types.AggchainProof{},
			},
		}
		result, err := ConvertProtoCertToAgglayer(protoCert)
		require.Error(t, err)
		require.Nil(t, result)
		require.ErrorContains(t, err, "expected Sp1Stark proof, got")
	})

	t.Run("error in aggchain data proof - nil proof", func(t *testing.T) {
		protoCert, err := ConvertCertToProtoCertificate(exampleTestAgglayerCert)
		require.NoError(t, err)
		protoCert.AggchainData = &v1types.AggchainData{
			Data: &v1types.AggchainData_Generic{
				Generic: &v1types.AggchainProof{
					Proof: &v1types.AggchainProof_Sp1Stark{},
				},
			},
		}
		result, err := ConvertProtoCertToAgglayer(protoCert)
		require.Error(t, err)
		require.Nil(t, result)
		require.ErrorContains(t, err, "aggchain data has nil Sp1Stark proof")
	})

	t.Run("error in aggchain data proof - nil aggchain params", func(t *testing.T) {
		protoCert, err := ConvertCertToProtoCertificate(exampleTestAgglayerCert)
		require.NoError(t, err)
		protoCert.AggchainData = &v1types.AggchainData{
			Data: &v1types.AggchainData_Generic{
				Generic: &v1types.AggchainProof{
					Proof: &v1types.AggchainProof_Sp1Stark{
						Sp1Stark: &v1types.SP1StarkProof{
							Proof:   []byte{0x01, 0x02, 0x03},
							Version: "1.0",
							Vkey:    []byte{0x01, 0x02, 0x03},
						},
					},
				},
			},
		}
		result, err := ConvertProtoCertToAgglayer(protoCert)
		require.Error(t, err)
		require.Nil(t, result)
		require.ErrorContains(t, err, "aggchain data has nil AggchainParams")
	})

	t.Run("error in aggchain data proof - nil aggchain proof signature", func(t *testing.T) {
		protoCert, err := ConvertCertToProtoCertificate(exampleTestAgglayerCert)
		require.NoError(t, err)
		protoCert.AggchainData = &v1types.AggchainData{
			Data: &v1types.AggchainData_Generic{
				Generic: &v1types.AggchainProof{
					Proof: &v1types.AggchainProof_Sp1Stark{
						Sp1Stark: &v1types.SP1StarkProof{
							Proof:   []byte{0x01, 0x02, 0x03},
							Version: "1.0",
							Vkey:    []byte{0x01, 0x02, 0x03},
						},
					},
					AggchainParams: &v1types.FixedBytes32{
						Value: common.HexToHash("0x0102030405060708090a0b0c0d0e0f1011121314151617181920212223242526").Bytes(),
					},
					Signature: nil, // This should cause an error
				},
			},
		}
		result, err := ConvertProtoCertToAgglayer(protoCert)
		require.Error(t, err)
		require.Nil(t, result)
		require.ErrorContains(t, err, "aggchain data has nil Signature")
	})

	t.Run("aggchain data multisig", func(t *testing.T) {
		// multisig nil
		protoCert, err := ConvertCertToProtoCertificate(exampleTestAgglayerCert)
		require.NoError(t, err)
		protoCert.AggchainData = &v1types.AggchainData{
			Data: &v1types.AggchainData_Multisig{},
		}

		result, err := ConvertProtoCertToAgglayer(protoCert)
		require.Nil(t, result)
		require.ErrorContains(t, err, "multisig is nil")

		// multisig not ecdsa
		protoCert.AggchainData = &v1types.AggchainData{
			Data: &v1types.AggchainData_Multisig{
				Multisig: &v1types.Multisig{},
			},
		}

		result, err = ConvertProtoCertToAgglayer(protoCert)
		require.Nil(t, result)
		require.ErrorContains(t, err, "expected Ecdsa multisig")

		// mutlisig ecdsa nil
		protoCert.AggchainData = &v1types.AggchainData{
			Data: &v1types.AggchainData_Multisig{
				Multisig: &v1types.Multisig{
					Data: &v1types.Multisig_Ecdsa{},
				},
			},
		}

		result, err = ConvertProtoCertToAgglayer(protoCert)
		require.Nil(t, result)
		require.ErrorContains(t, err, "multisig Ecdsa is nil")

		// ok conversion
		protoCert.AggchainData = &v1types.AggchainData{
			Data: &v1types.AggchainData_Multisig{
				Multisig: &v1types.Multisig{
					Data: &v1types.Multisig_Ecdsa{
						Ecdsa: &v1types.ECDSAMultisig{
							Signatures: []*v1types.ECDSAMultisig_ECDSAMultisigEntry{
								{Index: 0, Signature: &v1types.FixedBytes65{Value: make([]byte, aggkitcommon.SignatureSize)}},
								{Index: 1, Signature: &v1types.FixedBytes65{Value: make([]byte, aggkitcommon.SignatureSize)}},
							},
						},
					},
				},
			},
		}

		result, err = ConvertProtoCertToAgglayer(protoCert)
		require.NoError(t, err)
		require.NotNil(t, result.AggchainData)

		multisig, ok := result.AggchainData.(*agglayertypes.AggchainDataMultisig)
		require.True(t, ok)
		require.NotNil(t, multisig)
		require.Len(t, multisig.Multisig.Signatures, 2)
	})

	t.Run("aggchain data multisig and aggchain proof", func(t *testing.T) {
		// multisig and aggchain proof nil
		protoCert, err := ConvertCertToProtoCertificate(exampleTestAgglayerCert)
		require.NoError(t, err)

		protoCert.AggchainData = &v1types.AggchainData{
			Data: &v1types.AggchainData_MultisigAndAggchainProof{},
		}

		result, err := ConvertProtoCertToAgglayer(protoCert)
		require.Nil(t, result)
		require.ErrorContains(t, err, "aggchain data has nil MultisigAndAggchainProof")

		// ok conversion
		protoCert.AggchainData = &v1types.AggchainData{
			Data: &v1types.AggchainData_MultisigAndAggchainProof{
				MultisigAndAggchainProof: &v1types.AggchainProofWithMultisig{
					Multisig: &v1types.Multisig{
						Data: &v1types.Multisig_Ecdsa{
							Ecdsa: &v1types.ECDSAMultisig{
								Signatures: []*v1types.ECDSAMultisig_ECDSAMultisigEntry{
									{Index: 0, Signature: &v1types.FixedBytes65{Value: make([]byte, aggkitcommon.SignatureSize)}},
								},
							},
						},
					},
					AggchainProof: &v1types.AggchainProof{
						Proof: &v1types.AggchainProof_Sp1Stark{
							Sp1Stark: &v1types.SP1StarkProof{
								Proof:   []byte{0x01, 0x02, 0x03},
								Version: "1.0",
								Vkey:    []byte{0x01, 0x02, 0x03},
							},
						},
						AggchainParams: &v1types.FixedBytes32{
							Value: common.HexToHash("0x0102030405060708090a0b0c0d0e0f1011121314151617181920212223242526").Bytes(),
						},
						Signature: &v1types.FixedBytes65{Value: make([]byte, aggkitcommon.SignatureSize)},
					},
				},
			},
		}

		result, err = ConvertProtoCertToAgglayer(protoCert)
		require.NoError(t, err)
		require.NotNil(t, result)
	})
}

func TestGrpcBridgeExitToAgglayer(t *testing.T) {
	t.Run("nil bridge exit", func(t *testing.T) {
		result, err := grpcBridgeExitToAgglayer(nil)
		require.Error(t, err)
		require.Nil(t, result)
		require.ErrorIs(t, err, ErrNilCertificate)
	})

	t.Run("nil TokenInfo", func(t *testing.T) {
		bridgeExit := &v1types.BridgeExit{
			LeafType:    v1types.LeafType_LEAF_TYPE_TRANSFER,
			TokenInfo:   nil,
			DestNetwork: 2,
			DestAddress: &v1types.FixedBytes20{Value: common.HexToAddress("0x456").Bytes()},
		}

		result, err := grpcBridgeExitToAgglayer(bridgeExit)
		require.Error(t, err)
		require.Nil(t, result)
		require.ErrorIs(t, err, ErrNilCertificate)
	})

	t.Run("nil DestAddress", func(t *testing.T) {
		bridgeExit := &v1types.BridgeExit{
			LeafType: v1types.LeafType_LEAF_TYPE_TRANSFER,
			TokenInfo: &v1types.TokenInfo{
				OriginTokenAddress: &v1types.FixedBytes20{Value: common.HexToAddress("0x123").Bytes()},
				OriginNetwork:      1,
			},
			DestNetwork: 2,
			DestAddress: nil,
		}

		result, err := grpcBridgeExitToAgglayer(bridgeExit)
		require.Error(t, err)
		require.Nil(t, result)
		require.ErrorIs(t, err, ErrNilCertificate)
	})

	t.Run("successful conversion with metadata", func(t *testing.T) {
		bridgeExit := &v1types.BridgeExit{
			LeafType: v1types.LeafType_LEAF_TYPE_MESSAGE,
			TokenInfo: &v1types.TokenInfo{
				OriginTokenAddress: &v1types.FixedBytes20{Value: common.HexToAddress("0x123").Bytes()},
				OriginNetwork:      1,
			},
			DestNetwork: 2,
			DestAddress: &v1types.FixedBytes20{Value: common.HexToAddress("0x456").Bytes()},
			Amount:      &v1types.FixedBytes32{Value: big.NewInt(1000).Bytes()},
			Metadata:    &v1types.FixedBytes32{Value: common.HexToHash("0xbeef").Bytes()},
		}

		result, err := grpcBridgeExitToAgglayer(bridgeExit)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, agglayertypes.LeafTypeMessage, result.LeafType)
		require.Equal(t, common.HexToAddress("0x123"), result.TokenInfo.OriginTokenAddress)
		require.Equal(t, uint32(1), result.TokenInfo.OriginNetwork)
		require.Equal(t, uint32(2), result.DestinationNetwork)
		require.Equal(t, common.HexToAddress("0x456"), result.DestinationAddress)
		require.Equal(t, big.NewInt(1000), result.Amount)
		require.Equal(t, common.HexToHash("0xbeef").Bytes(), result.Metadata)
	})
}

func TestGrpcLeafTypeToAgglayer(t *testing.T) {
	t.Run("transfer leaf type", func(t *testing.T) {
		result, err := grpcLeafTypeToAgglayer(v1types.LeafType_LEAF_TYPE_TRANSFER)
		require.NoError(t, err)
		require.Equal(t, agglayertypes.LeafTypeAsset, result)
	})

	t.Run("message leaf type", func(t *testing.T) {
		result, err := grpcLeafTypeToAgglayer(v1types.LeafType_LEAF_TYPE_MESSAGE)
		require.NoError(t, err)
		require.Equal(t, agglayertypes.LeafTypeMessage, result)
	})

	t.Run("unknown leaf type", func(t *testing.T) {
		result, err := grpcLeafTypeToAgglayer(v1types.LeafType(999))
		require.Error(t, err)
		require.Equal(t, agglayertypes.LeafTypeAsset, result)
		require.Contains(t, err.Error(), "unknown leaf type")
	})
}

func TestGrpcL1LeafToAgglayer(t *testing.T) {
	t.Run("nil L1InfoTreeLeafWithContext", func(t *testing.T) {
		result, err := grpcL1LeafToAgglayer(&v1types.L1InfoTreeLeafWithContext{})
		require.Nil(t, result)
		require.ErrorIs(t, err, ErrNilCertificate)
	})
	t.Run("ok L1InfoTreeLeafWithContext", func(t *testing.T) {
		result, err := grpcL1LeafToAgglayer(&v1types.L1InfoTreeLeafWithContext{
			Rer: &v1types.FixedBytes32{Value: common.HexToHash("0x123").Bytes()},
			Mer: &v1types.FixedBytes32{Value: common.HexToHash("0x123").Bytes()},
			Inner: &v1types.L1InfoTreeLeaf{
				GlobalExitRoot: &v1types.FixedBytes32{Value: common.HexToHash("0x123").Bytes()},
				BlockHash:      &v1types.FixedBytes32{Value: common.HexToHash("0x123").Bytes()},
				Timestamp:      1234567890,
			},
			L1InfoTreeIndex: 1,
		})
		require.NotNil(t, result)
		require.NoError(t, err)
	})

	t.Run("nil L1InfoTreeLeafWithContext.Inner", func(t *testing.T) {
		result, err := grpcL1LeafToAgglayer(&v1types.L1InfoTreeLeafWithContext{
			Rer:             &v1types.FixedBytes32{Value: common.HexToHash("0x123").Bytes()},
			Mer:             &v1types.FixedBytes32{Value: common.HexToHash("0x123").Bytes()},
			Inner:           nil,
			L1InfoTreeIndex: 1,
		})
		require.Nil(t, result)
		require.ErrorIs(t, err, ErrNilCertificate)
	})
}
