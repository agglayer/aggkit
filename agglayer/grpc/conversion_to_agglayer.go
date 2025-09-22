package grpc

// This file contains the function to convert from grpc to agglayer types.

import (
	"errors"
	"fmt"
	"math/big"

	v1nodetypes "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/types/v1"
	v1types "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrNilCertificate = errors.New("aggsender-validator nil certificate")
)

// ConvertProtoCertToAgglayer Convert a certificate from the gRPC (PROTO) format to the agglayer format
func ConvertProtoCertToAgglayer(cert *v1nodetypes.Certificate) (*agglayertypes.Certificate, error) {
	if cert == nil {
		return nil, ErrNilCertificate
	}

	if cert.PrevLocalExitRoot == nil || cert.NewLocalExitRoot == nil {
		return nil, fmt.Errorf("convertProtoCertToAgglayer. Certificate has nil fields: "+
			"PrevLocalExitRoot or NewLocalExitRoot. %w", ErrNilCertificate)
	}

	if cert.L1InfoTreeLeafCount == nil {
		return nil, fmt.Errorf("convertProtoCertToAgglayer. Certificate has nil L1InfoTreeLeafCount. %w",
			ErrNilCertificate)
	}

	aggchainData, err := grpcAggchainDataToAgglayer(cert.AggchainData)
	if err != nil {
		return nil, fmt.Errorf("convertProtoCertToAgglayer. error converting grpc aggchain data: %w", err)
	}

	bridgeExits, err := grpcBridgeExitsToAgglayer(cert.BridgeExits)
	if err != nil {
		return nil, fmt.Errorf("convertProtoCertToAgglayer. error converting grpc bridge exits: %w", err)
	}
	importedBridgeExits, err := grpcImportedBridgeExitsToAgglayer(cert.ImportedBridgeExits)
	if err != nil {
		return nil, fmt.Errorf("convertProtoCertToAgglayer. error converting grpc imported bridge exits: %w", err)
	}

	agglayerCert := &agglayertypes.Certificate{
		NetworkID:           cert.NetworkId,
		Height:              cert.Height,
		PrevLocalExitRoot:   common.BytesToHash(cert.PrevLocalExitRoot.Value),
		NewLocalExitRoot:    common.BytesToHash(cert.NewLocalExitRoot.Value),
		CustomChainData:     cert.CustomChainData,
		L1InfoTreeLeafCount: *cert.L1InfoTreeLeafCount,
		BridgeExits:         bridgeExits,
		ImportedBridgeExits: importedBridgeExits,
		AggchainData:        aggchainData,
	}

	return agglayerCert, nil
}

func grpcBridgeExitsToAgglayer(
	bridgeExits []*v1types.BridgeExit,
) ([]*agglayertypes.BridgeExit, error) {
	if len(bridgeExits) == 0 {
		return nil, nil
	}
	exits := make([]*agglayertypes.BridgeExit, len(bridgeExits))
	for i, exit := range bridgeExits {
		agglayerBridgeExit, err := grpcBridgeExitToAgglayer(exit)
		if err != nil {
			return nil, fmt.Errorf("error converting grpc bridge exit at index %d: %w", i, err)
		}
		exits[i] = agglayerBridgeExit
	}
	return exits, nil
}

func grpcBridgeExitToAgglayer(bridgeExit *v1types.BridgeExit) (*agglayertypes.BridgeExit, error) {
	if bridgeExit == nil {
		return nil, fmt.Errorf("grpcBridgeExitToAgglayer. bridge exit is nil. %w", ErrNilCertificate)
	}
	leafType, err := grpcLeafTypeToAgglayer(bridgeExit.LeafType)
	if err != nil {
		return nil, fmt.Errorf("grpcBridgeExitToAgglayer. error converting grpc leaf type to agglayer: %w", err)
	}
	if bridgeExit.TokenInfo == nil || bridgeExit.TokenInfo.OriginTokenAddress == nil {
		return nil, fmt.Errorf("grpcBridgeExitToAgglayer. bridge exit has nil TokenInfo, "+
			"OriginTokenAddress or OriginNetwork. %w", ErrNilCertificate)
	}

	if bridgeExit.DestAddress == nil {
		return nil, fmt.Errorf("grpcBridgeExitToAgglayer. bridge exit has nil DestAddress. %w", ErrNilCertificate)
	}
	amount := big.NewInt(0)
	if bridgeExit.Amount != nil {
		amount = amount.SetBytes(bridgeExit.Amount.Value)
	}

	var metadata []byte
	if bridgeExit.Metadata != nil {
		metadata = bridgeExit.Metadata.Value
	}

	agglayerBridgeExit := &agglayertypes.BridgeExit{
		LeafType: leafType,
		TokenInfo: &agglayertypes.TokenInfo{
			OriginTokenAddress: common.BytesToAddress(bridgeExit.TokenInfo.OriginTokenAddress.Value),
			OriginNetwork:      bridgeExit.TokenInfo.OriginNetwork,
		},
		DestinationNetwork: bridgeExit.DestNetwork,
		DestinationAddress: common.BytesToAddress(bridgeExit.DestAddress.Value),
		Amount:             amount,
		Metadata:           metadata,
	}

	return agglayerBridgeExit, nil
}

// grpcLeafTypeToAgglayer converts a leaf type to a proto leaf type
func grpcLeafTypeToAgglayer(leafType v1types.LeafType) (agglayertypes.LeafType, error) {
	switch leafType {
	case v1types.LeafType_LEAF_TYPE_TRANSFER:
		return agglayertypes.LeafTypeAsset, nil
	case v1types.LeafType_LEAF_TYPE_MESSAGE:
		return agglayertypes.LeafTypeMessage, nil
	default:
		return agglayertypes.LeafTypeAsset, fmt.Errorf("unknown leaf type: %s", leafType)
	}
}

func grpcImportedBridgeExitsToAgglayer(
	importedBridgeExits []*v1types.ImportedBridgeExit,
) ([]*agglayertypes.ImportedBridgeExit, error) {
	if len(importedBridgeExits) == 0 {
		return nil, nil
	}
	exits := make([]*agglayertypes.ImportedBridgeExit, len(importedBridgeExits))
	for i, exit := range importedBridgeExits {
		agglayerImportedBridgeExit, err := grpcImportedBridgeExitToAgglayer(exit)
		if err != nil {
			return nil, fmt.Errorf("grpcImportedBridgeExitsToAgglayer. error converting grpc imported bridge exit"+
				" at index %d: %w", i, err)
		}
		exits[i] = agglayerImportedBridgeExit
	}
	return exits, nil
}
func grpcImportedBridgeExitToAgglayer(
	importedBridgeExit *v1types.ImportedBridgeExit,
) (*agglayertypes.ImportedBridgeExit, error) {
	if importedBridgeExit == nil {
		return nil, fmt.Errorf("imported bridge exit is nil. %w", ErrNilCertificate)
	}

	bridgeExit, err := grpcBridgeExitToAgglayer(importedBridgeExit.BridgeExit)
	if err != nil {
		return nil, fmt.Errorf("grpcImportedBridgeExitToAgglayer. error converting grpc bridge exit to agglayer: %w", err)
	}

	if importedBridgeExit.GlobalIndex == nil {
		return nil, fmt.Errorf("grpcImportedBridgeExitToAgglayer. imported bridge exit has nil GlobalIndex or Hash. %w",
			ErrNilCertificate)
	}
	globalIndexBigInt := new(big.Int).SetBytes(importedBridgeExit.GlobalIndex.Value)
	mainnetFlag, rollupIndex, leafIndex, err := bridgesync.DecodeGlobalIndex(globalIndexBigInt)
	if err != nil {
		return nil, fmt.Errorf("grpcImportedBridgeExitToAgglayer. error decoding global index: %w", err)
	}
	claimData, err := grpcClaimDataToAgglayer(importedBridgeExit.Claim)
	if err != nil {
		return nil, fmt.Errorf("grpcImportedBridgeExitToAgglayer. error converting grpc claim data to agglayer: %w", err)
	}

	agglayerImportedBridgeExit := &agglayertypes.ImportedBridgeExit{
		BridgeExit: bridgeExit,
		GlobalIndex: &agglayertypes.GlobalIndex{
			MainnetFlag: mainnetFlag,
			RollupIndex: rollupIndex,
			LeafIndex:   leafIndex,
		},
		ClaimData: claimData,
	}
	return agglayerImportedBridgeExit, nil
}

func grpcClaimDataToAgglayer(claim interface{}) (agglayertypes.Claim, error) {
	switch v := claim.(type) {
	case *v1types.ImportedBridgeExit_Mainnet:
		proofs, err := grpcMerkleProofsToAgglayer(
			v.Mainnet.ProofLeafMer,
			v.Mainnet.ProofGerL1Root,
		)
		if err != nil {
			return nil, fmt.Errorf("grpcClaimDataToAgglayer. error converting Mmainnet proofs: %w", err)
		}
		l1feaf, err := grpcL1LeafToAgglayer(v.Mainnet.L1Leaf)
		if err != nil {
			return nil, fmt.Errorf("grpcClaimDataToAgglayer. error converting Mainnet L1 leaf: %w", err)
		}
		return &agglayertypes.ClaimFromMainnet{
			ProofLeafMER:     proofs[0],
			ProofGERToL1Root: proofs[1],
			L1Leaf:           l1feaf,
		}, nil
	case *v1types.ImportedBridgeExit_Rollup:
		proofs, err := grpcMerkleProofsToAgglayer(
			v.Rollup.ProofLeafLer,
			v.Rollup.ProofLerRer,
			v.Rollup.ProofGerL1Root,
		)
		if err != nil {
			return nil, fmt.Errorf("grpcClaimDataToAgglayer. error converting Roolup proofs: %w", err)
		}
		l1feaf, err := grpcL1LeafToAgglayer(v.Rollup.L1Leaf)
		if err != nil {
			return nil, fmt.Errorf("grpcClaimDataToAgglayer. error converting Roolup L1 leaf: %w", err)
		}
		return &agglayertypes.ClaimFromRollup{
			ProofLeafLER:     proofs[0],
			ProofLERToRER:    proofs[1],
			ProofGERToL1Root: proofs[2],
			L1Leaf:           l1feaf,
		}, nil
	default:
		return nil, fmt.Errorf("unknown claim type: %T", v)
	}
}

func grpcMerkleProofsToAgglayer(proofs ...*v1types.MerkleProof) ([]*agglayertypes.MerkleProof, error) {
	result := make([]*agglayertypes.MerkleProof, len(proofs))
	for i, proof := range proofs {
		converted, err := grpcMerkleProofToAgglayer(proof)
		if err != nil {
			return nil, fmt.Errorf("error converting merkle proof at index %d: %w", i, err)
		}
		result[i] = converted
	}
	return result, nil
}

func grpcMerkleProofToAgglayer(proof *v1types.MerkleProof) (*agglayertypes.MerkleProof, error) {
	if proof == nil {
		return nil, fmt.Errorf("grpcMerkleProofToAgglayer. proof is nil. %w", ErrNilCertificate)
	}
	if proof.Root == nil || proof.Siblings == nil {
		return nil, fmt.Errorf("grpcMerkleProofToAgglayer. proof has nil Root or Siblings. %w", ErrNilCertificate)
	}
	if len(proof.Siblings) != int(treetypes.DefaultHeight) {
		return nil, fmt.Errorf("grpcMerkleProofToAgglayer. proof has invalid number of siblings: %d. Expected: %d",
			len(proof.Siblings), treetypes.DefaultHeight)
	}
	siblings := [treetypes.DefaultHeight]common.Hash{}
	for i, sibling := range proof.Siblings {
		siblings[i] = common.BytesToHash(sibling.Value)
	}

	return &agglayertypes.MerkleProof{
		Root:  common.BytesToHash(proof.Root.Value),
		Proof: siblings,
	}, nil
}

func grpcL1LeafToAgglayer(l1Leaf *v1types.L1InfoTreeLeafWithContext) (*agglayertypes.L1InfoTreeLeaf, error) {
	if l1Leaf == nil {
		return nil, fmt.Errorf("grpcL1LeafToAgglayer. l1 leaf is nil. %w", ErrNilCertificate)
	}
	if l1Leaf.Rer == nil || l1Leaf.Mer == nil || l1Leaf.Inner == nil {
		return nil, fmt.Errorf("grpcL1LeafToAgglayer. l1 leaf has nil Rer or Mer or Inner. %w", ErrNilCertificate)
	}

	if l1Leaf.Inner.GlobalExitRoot == nil || l1Leaf.Inner.BlockHash == nil {
		return nil, fmt.Errorf("grpcL1LeafToAgglayer. l1 leaf inner has nil GlobalExitRoot or BlockHash. %w",
			ErrNilCertificate)
	}
	inner := &agglayertypes.L1InfoTreeLeafInner{
		GlobalExitRoot: common.BytesToHash(l1Leaf.Inner.GlobalExitRoot.Value),
		BlockHash:      common.BytesToHash(l1Leaf.Inner.BlockHash.Value),
		Timestamp:      l1Leaf.Inner.Timestamp,
	}

	return &agglayertypes.L1InfoTreeLeaf{
		L1InfoTreeIndex: l1Leaf.L1InfoTreeIndex,
		RollupExitRoot:  common.BytesToHash(l1Leaf.Rer.Value),
		MainnetExitRoot: common.BytesToHash(l1Leaf.Mer.Value),
		Inner:           inner,
	}, nil
}

func grpcAggchainDataToAgglayer(
	aggchainData *v1types.AggchainData,
) (agglayertypes.AggchainData, error) {
	if aggchainData == nil || aggchainData.Data == nil {
		return nil, nil
	}

	switch ad := aggchainData.Data.(type) {
	case *v1types.AggchainData_Signature:
		if ad.Signature == nil {
			return nil, fmt.Errorf("grpcAggchainDataToAgglayer. aggchain data has nil Signature. %w", ErrNilCertificate)
		}
		return &agglayertypes.AggchainDataSignature{
			Signature: ad.Signature.Value,
		}, nil
	case *v1types.AggchainData_Generic:
		return grpcAggchainProofToAgglayer(ad.Generic)
	case *v1types.AggchainData_Multisig:
		multisig, err := grpcMultisigToAgglayer(ad.Multisig)
		if err != nil {
			return nil, fmt.Errorf("grpcAggchainDataToAgglayer. failed to convert multisig: %w", err)
		}
		return &agglayertypes.AggchainDataMultisig{
			Multisig: multisig,
		}, nil
	case *v1types.AggchainData_MultisigAndAggchainProof:
		if ad.MultisigAndAggchainProof == nil {
			return nil, fmt.Errorf("grpcAggchainDataToAgglayer. aggchain data has nil MultisigAndAggchainProof. %w",
				ErrNilCertificate)
		}

		aggchainProof, err := grpcAggchainProofToAgglayer(ad.MultisigAndAggchainProof.AggchainProof)
		if err != nil {
			return nil, fmt.Errorf("grpcAggchainDataToAgglayer. failed to convert aggchain proof: %w", err)
		}

		multisig, err := grpcMultisigToAgglayer(ad.MultisigAndAggchainProof.Multisig)
		if err != nil {
			return nil, fmt.Errorf("grpcAggchainDataToAgglayer. failed to convert multisig: %w", err)
		}

		return &agglayertypes.AggchainDataMultisigWithProof{
			AggchainProof: aggchainProof,
			Multisig:      multisig,
		}, nil
	default:
		return nil, fmt.Errorf("grpcAggchainDataToAgglayer. unknown aggchain data type: %T", aggchainData)
	}
}

func grpcMultisigToAgglayer(multisig *v1types.Multisig) (*agglayertypes.Multisig, error) {
	if multisig == nil {
		return nil, fmt.Errorf("grpcMultisigToAgglayer. multisig is nil. %w", ErrNilCertificate)
	}

	multisigEcdsa, ok := multisig.Data.(*v1types.Multisig_Ecdsa)
	if !ok {
		return nil, fmt.Errorf("grpcMultisigToAgglayer. expected Ecdsa multisig, got: %T", multisig.Data)
	}

	if multisigEcdsa.Ecdsa == nil {
		return nil, fmt.Errorf("grpcMultisigToAgglayer. multisig Ecdsa is nil. %w", ErrNilCertificate)
	}

	multisigAgglayer := &agglayertypes.Multisig{
		Signatures: make([]agglayertypes.ECDSAMultisigEntry, len(multisigEcdsa.Ecdsa.Signatures)),
	}

	for i, sig := range multisigEcdsa.Ecdsa.Signatures {
		multisigAgglayer.Signatures[i] = agglayertypes.ECDSAMultisigEntry{
			Signature: sig.Signature.Value,
			Index:     sig.Index,
		}
	}

	return multisigAgglayer, nil
}

func grpcAggchainProofToAgglayer(aggchainProof *v1types.AggchainProof) (*agglayertypes.AggchainDataProof, error) {
	if aggchainProof == nil {
		return nil, fmt.Errorf("grpcAggchainProofToAgglayer. aggchain proof is nil. %w", ErrNilCertificate)
	}

	sp1Proof, ok := aggchainProof.Proof.(*v1types.AggchainProof_Sp1Stark)
	if !ok {
		return nil, fmt.Errorf("grpcAggchainDataToAgglayer. expected Sp1Stark proof, got: %T", aggchainProof.Proof)
	}

	if sp1Proof.Sp1Stark == nil {
		return nil, fmt.Errorf("grpcAggchainDataToAgglayer. aggchain data has nil Sp1Stark proof. %w", ErrNilCertificate)
	}

	if aggchainProof.AggchainParams == nil {
		return nil, fmt.Errorf("grpcAggchainDataToAgglayer. aggchain data has nil AggchainParams. %w", ErrNilCertificate)
	}

	if aggchainProof.Signature == nil {
		return nil, fmt.Errorf("grpcAggchainDataToAgglayer. aggchain data has nil Signature. %w", ErrNilCertificate)
	}

	return &agglayertypes.AggchainDataProof{
		Proof:          sp1Proof.Sp1Stark.Proof,
		Version:        sp1Proof.Sp1Stark.Version,
		Vkey:           sp1Proof.Sp1Stark.Vkey,
		AggchainParams: common.BytesToHash(aggchainProof.AggchainParams.Value),
		Context:        aggchainProof.Context,
		Signature:      aggchainProof.Signature.Value,
	}, nil
}
