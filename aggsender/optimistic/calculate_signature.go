package optimistic

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/big"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type AggregationProofPublicValues struct {
	l1Head           common.Hash
	l2PreRoot        common.Hash
	claimRoot        common.Hash
	l2BlockNumber    uint64
	rollupConfigHash common.Hash
	multiBlockVKey   common.Hash
	proverAddress    common.Address
}

func (s *AggregationProofPublicValues) Hash() (common.Hash, error) {
	// Crear tipos ABI uno por uno
	tBytes32, err := abi.NewType("bytes32", "", nil)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to create bytes32 type: %w", err)
	}

	tUint64, err := abi.NewType("uint64", "", nil)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to create uint64 type: %w", err)
	}

	tAddress, err := abi.NewType("address", "", nil)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to create address type: %w", err)
	}

	// Definir la secuencia de argumentos ABI
	args := abi.Arguments{
		{Type: tBytes32},
		{Type: tBytes32},
		{Type: tBytes32},
		{Type: tUint64},
		{Type: tBytes32},
		{Type: tBytes32},
		{Type: tAddress},
	}
	packed, err := args.Pack(
		s.l1Head,
		s.l2PreRoot,
		s.claimRoot,
		s.l2BlockNumber,
		s.rollupConfigHash,
		s.multiBlockVKey,
		s.proverAddress,
	)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to pack arguments: %w", err)
	}
	shaDigest := sha256.Sum256(packed)
	return common.BytesToHash(shaDigest[:]), nil
}

type OptimisticSignatureData struct {
	aggregationProofPublicValuesHash common.Hash
	newLocalExitRoot                 common.Hash
	commitImportedBridgeExits        common.Hash
}

/*
	func NewOptimisticSignatureDataFromAgglayerCert(aggregationProofPublicValues AggregationProofPublicValues, cert *agglayertypes.Certificate) *OptimisticSignatureData {
		return &OptimisticSignatureData{
			aggregationProofPublicValues: aggregationProofPublicValues,
			newLocalExitRoot:             cert.NewLocalExitRoot,
			commitImportedBridgeExits:    CalculateCommitImportedBridgeExitsHashFromImportedBridges(cert.ImportedBridgeExits),
		}
	}

	func NewOptimisticSignatureDataFromCertBuildParams(aggregationProofPublicValues AggregationProofPublicValues, certBuildParams *types.CertificateBuildParams) *OptimisticSignatureData {
		// TODO: Fill newLocalExitRoot with the correct value!!!!!!
		return &OptimisticSignatureData{
			aggregationProofPublicValues: aggregationProofPublicValues,
			newLocalExitRoot:             common.Hash{},
			commitImportedBridgeExits:    CalculateCommitImportedBrdigeExitsHashFromClaims(certBuildParams.Claims),
		}
	}
*/

func (o *OptimisticSignatureData) Hash() common.Hash {
	return crypto.Keccak256Hash(
		o.aggregationProofPublicValuesHash.Bytes(),
		o.newLocalExitRoot.Bytes(),
		o.commitImportedBridgeExits.Bytes(),
	)
}

func (o *OptimisticSignatureData) Sign(ctx context.Context, signer signertypes.HashSigner) (common.Hash, error) {
	hash := o.Hash()
	signData, err := signer.SignHash(ctx, hash)
	if err != nil {
		return common.Hash{}, fmt.Errorf("OptimisticSignatureData.Sign: error signing hash: %w", err)
	}
	return common.BytesToHash(signData), nil
}

// CalculateCommitImportedBridgeExitsHashFromImportedBridges calculate from a agglayer certificate
func CalculateCommitImportedBridgeExitsHashFromImportedBridges(
	importedBridges []*agglayertypes.ImportedBridgeExit) common.Hash {
	var combined []byte
	for _, claim := range importedBridges {
		globalIndex := bridgesync.GenerateGlobalIndex(
			claim.GlobalIndex.MainnetFlag,
			claim.GlobalIndex.RollupIndex,
			claim.GlobalIndex.LeafIndex,
		).Uint64()
		combined = append(combined, aggkitcommon.Uint64ToBigEndianBytes(globalIndex)...)
		claimHash := CalculateBridgeExitHash(claim.BridgeExit).Bytes()
		combined = append(combined, claimHash[:]...)

	}
	return crypto.Keccak256Hash(combined)
}

func CalculateBridgeExitHash(bridgeExit *agglayertypes.BridgeExit) common.Hash {
	amount := bridgeExit.Amount
	if amount == nil {
		amount = big.NewInt(0)
	}

	metaDataHash := bridgeExit.Metadata
	if len(metaDataHash) == 0 {
		metaDataHash = crypto.Keccak256(nil)
	}

	return crypto.Keccak256Hash(
		[]byte{bridgeExit.LeafType.Uint8()},
		aggkitcommon.Uint32ToBytes(bridgeExit.TokenInfo.OriginNetwork),
		bridgeExit.TokenInfo.OriginTokenAddress.Bytes(),
		aggkitcommon.Uint32ToBytes(bridgeExit.DestinationNetwork),
		bridgeExit.DestinationAddress.Bytes(),
		common.BigToHash(amount).Bytes(),
		metaDataHash,
	)
}

// CalculateCommitImportedBrdigeExitsHashFromClaims.This calculate hash from certBuildParams
func CalculateCommitImportedBrdigeExitsHashFromClaims(claims []bridgesync.Claim) common.Hash {
	var combined []byte
	for _, claim := range claims {
		globalIndex := claim.GlobalIndex.Uint64()
		combined = append(combined, aggkitcommon.Uint64ToBigEndianBytes(globalIndex)...)
		claimHash := CalculateClaimHash(&claim).Bytes()
		combined = append(combined, claimHash[:]...)

	}
	return crypto.Keccak256Hash(combined)
}

// This calculate hash from certBuildParamss
func CalculateClaimHash(claim *bridgesync.Claim) common.Hash {
	amount := claim.Amount
	if amount == nil {
		amount = big.NewInt(0)
	}

	metaDataHash := claim.Metadata
	if len(metaDataHash) == 0 {
		metaDataHash = crypto.Keccak256(nil)
	}

	leafType := uint8(agglayertypes.LeafTypeAsset)
	if claim.IsMessage {
		leafType = uint8(agglayertypes.LeafTypeMessage)
	}

	return crypto.Keccak256Hash(
		[]byte{leafType},
		aggkitcommon.Uint32ToBytes(claim.OriginNetwork),
		claim.OriginAddress.Bytes(),
		aggkitcommon.Uint32ToBytes(claim.DestinationNetwork),
		claim.DestinationAddress.Bytes(),
		common.BigToHash(amount).Bytes(),
		metaDataHash,
	)
}
