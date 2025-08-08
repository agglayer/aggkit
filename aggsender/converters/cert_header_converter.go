package converters

import (
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

// ConvertAgglayerCertHeaderToAggsender converts an agglayer CertificateHeader to an aggsender CertificateHeader
func ConvertAgglayerCertHeaderToAggsender(cert *agglayertypes.CertificateHeader) (*types.CertificateHeader, error) {
	if cert == nil {
		return nil, nil
	}

	blockRange := types.BlockRangeZero
	if cert.Metadata != aggkitcommon.ZeroHash {
		// TODO - remove this once we completely decouple metadata from the certificate
		metadataUnmarshal, err := types.NewCertificateMetadataFromHash(cert.Metadata)
		if err != nil {
			return nil, fmt.Errorf("error parsing cert metadata. Err: %w", err)
		}
		br, err := metadataUnmarshal.BlockRange()
		if err != nil {
			return nil, fmt.Errorf("cant get blockRange from certificate metadata. Err: %w", err)
		}
		blockRange = br
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
		CertSource:              types.CertificateSourceAggLayer,
	}, nil
}

// ConvertAggsenderCertHeaderToAgglayer converts an aggsender CertificateHeader to an agglayer CertificateHeader
func ConvertAggsenderCertHeaderToAgglayer(cert *types.CertificateHeader,
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
