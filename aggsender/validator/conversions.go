package validator

import (
	"errors"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
)

var (
	ErrNotImplemented        = errors.New("aggsender-verifier not implemented")
	ErrNilCertificate        = errors.New("aggsender-verfivier nil certificate")
	ErrMetadataNotCompatible = errors.New("aggsender-verifier metadata not compatible with the current version")
)

func AgglayerCertificateHeaderToAggsender(cert *agglayertypes.CertificateHeader) (*types.CertificateHeader, error) {
	if cert == nil {
		return nil, nil
	}
	metadataUnmarshal, err := types.NewCertificateMetadataFromHash(cert.Metadata)
	if err != nil {
		return nil, ErrMetadataNotCompatible
	}
	blockRange, err := metadataUnmarshal.BlockRange()
	if err != nil {
		return nil, fmt.Errorf("cant get blockRange from certificate. Err: %w", err)
	}

	return &types.CertificateHeader{
		Height:                  cert.Height,
		RetryCount:              0, // TODO: ??
		CertificateID:           cert.CertificateID,
		PreviousLocalExitRoot:   cert.PreviousLocalExitRoot,
		NewLocalExitRoot:        cert.NewLocalExitRoot,
		Status:                  cert.Status,
		FromBlock:               blockRange.FromBlock,
		ToBlock:                 blockRange.ToBlock,
		CreatedAt:               0,
		UpdatedAt:               0,
		FinalizedL1InfoTreeRoot: nil,
		CertType:                metadataUnmarshal.CertificateType(),
		CertSource:              types.CertificateSourceAggLayer,
	}, nil
}

func AggsenderCertificateHeaderToAgglayer(cert *types.CertificateHeader,
	networkID uint32) *agglayertypes.CertificateHeader {
	if cert == nil {
		return nil
	}
	metadata := types.NewCertificateMetadata(
		cert.FromBlock,
		uint32(cert.ToBlock-cert.FromBlock),
		cert.CreatedAt,
		cert.CertType.ToInt(),
	)
	return &agglayertypes.CertificateHeader{
		NetworkID:             networkID,
		Height:                cert.Height,
		CertificateID:         cert.CertificateID,
		PreviousLocalExitRoot: cert.PreviousLocalExitRoot,
		NewLocalExitRoot:      cert.NewLocalExitRoot,
		Status:                cert.Status,
		Metadata:              metadata.ToHash(),
	}
}

/*
func AgglayerCertificateToCertificateBuildParams(cert *agglayertypes.Certificate)
(*types.CertificateBuildParams, error) {
	metadataUnmarshal, err := types.NewCertificateMetadataFromHash(cert.Metadata)
	if err != nil {
		return nil, ErrMetadataNotCompatible
	}
	blockRange, err := metadataUnmarshal.BlockRange()
	if err != nil {
		return nil, fmt.Errorf("cant get blockRange from certificate. Err: %w", err)
	}

	bridges := ConvertBridgeExits(cert.BridgeExits)
	claims, err := ConvertImportedBridgeExits(cert.ImportedBridgeExits)
	if err != nil {
		return nil, fmt.Errorf("error converting imported bridge exits: %w", err)
	}

	certParams := &types.CertificateBuildParams{
		FromBlock:           blockRange.FromBlock,
		ToBlock:             blockRange.ToBlock,
		RetryCount:          0, // TODO: ??
		LastSentCertificate: nil,
		CertificateType:     metadataUnmarshal.CertificateType(),
		//L1InfoTreeRootFromWhichToProve:,
		L1InfoTreeLeafCount: cert.L1InfoTreeLeafCount,
		Bridges:             bridges,
		Claims:              claims,
	}
	return certParams, nil
}

func ConvertBridgeExits(bridgeExits []*agglayertypes.BridgeExit) []bridgesync.Bridge {
	bridges := make([]bridgesync.Bridge, 0, len(bridgeExits))
	for _, bridgeExit := range bridgeExits {
		if bridgeExit != nil {
			bridge := ConvertBridgeExit(*bridgeExit)
			bridges = append(bridges, bridge)
		}
	}
	return bridges
}

func ConvertBridgeExit(bridgeExit agglayertypes.BridgeExit) bridgesync.Bridge {
	// TODO: can't reverse megadata
	metadata := bridgeExit.Metadata
	bridge := bridgesync.Bridge{
		LeafType:           uint8(bridgeExit.LeafType),
		DestinationNetwork: bridgeExit.DestinationNetwork,
		DestinationAddress: bridgeExit.DestinationAddress,
		Amount:             bridgeExit.Amount,
		Metadata:           metadata,
		// TODO: ?? what about rest of fields,  BlockNum, BlockPos, .....
	}
	if bridgeExit.TokenInfo != nil {
		bridge.OriginNetwork = bridgeExit.TokenInfo.OriginNetwork
		bridge.OriginAddress = bridgeExit.TokenInfo.OriginTokenAddress
	}
	return bridge
}

func ConvertImportedBridgeExits(importedBridgeExits []*agglayertypes.ImportedBridgeExit) ([]bridgesync.Claim, error) {
	claims := make([]bridgesync.Claim, 0, len(importedBridgeExits))

	for i, ibe := range importedBridgeExits {
		if ibe == nil || ibe.BridgeExit == nil {
			return nil, fmt.Errorf("invalid imported bridge exit at index %d: nil value", i)
		}
		bridge := ConvertBridgeExit(*ibe.BridgeExit)

		// TODO: Metadata can't be reversed
		claim := bridgesync.Claim{
			OriginNetwork:      bridge.OriginNetwork,
			OriginAddress:      bridge.OriginAddress,
			DestinationNetwork: bridge.DestinationNetwork,
			DestinationAddress: bridge.DestinationAddress,
			Amount:             bridge.Amount,
			Metadata:           bridge.Metadata,
			GlobalIndex: bridgesync.GenerateGlobalIndex(
				ibe.GlobalIndex.MainnetFlag, ibe.GlobalIndex.RollupIndex, ibe.GlobalIndex.LeafIndex),
			IsMessage: ibe.BridgeExit.LeafType == agglayertypes.LeafTypeMessage,
		}

		// Populate additional fields based on ClaimData
		switch data := ibe.ClaimData.(type) {
		case *agglayertypes.ClaimFromMainnnet:
			claim.GlobalExitRoot = data.L1Leaf.Inner.GlobalExitRoot
			claim.RollupExitRoot = data.L1Leaf.RollupExitRoot
			claim.MainnetExitRoot = data.L1Leaf.MainnetExitRoot
			claim.ProofLocalExitRoot = data.ProofLeafMER.Proof
		case *agglayertypes.ClaimFromRollup:
			claim.GlobalExitRoot = data.L1Leaf.Inner.GlobalExitRoot
			claim.RollupExitRoot = data.L1Leaf.RollupExitRoot
			claim.MainnetExitRoot = data.L1Leaf.MainnetExitRoot
			claim.ProofLocalExitRoot = data.ProofLeafLER.Proof
			claim.ProofRollupExitRoot = data.ProofLERToRER.Proof
		default:
			return nil, fmt.Errorf("unsupported ClaimData type at index %d", i)
		}

		claims = append(claims, claim)
	}

	return claims, nil
}
*/
