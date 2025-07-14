package validator

import (
	"errors"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
)

var (
	ErrNotImplemented        = errors.New("aggsender-validator not implemented")
	ErrNilCertificate        = errors.New("aggsender-validator nil certificate")
	ErrMetadataNotCompatible = errors.New("aggsender-validator metadata not compatible with the current version")
)

// AgglayerCertificateHeaderToAggsender converts an agglayer CertificateHeader to an aggsender CertificateHeader
func AgglayerCertificateHeaderToAggsender(cert *agglayertypes.CertificateHeader) (*types.CertificateHeader, error) {
	if cert == nil {
		return nil, nil
	}
	metadataUnmarshal, err := types.NewCertificateMetadataFromHash(cert.Metadata)
	if err != nil {
		return nil, fmt.Errorf("error parsing cert metadata. Err: %w", err)
	}
	blockRange, err := metadataUnmarshal.BlockRange()
	if err != nil {
		return nil, fmt.Errorf("cant get blockRange from certificate metadata. Err: %w", err)
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

// AggsenderCertificateHeaderToAgglayer converts an aggsender CertificateHeader to an agglayer CertificateHeader
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
