package converters

import (
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/common"
)

// ConvertAgglayerCertHeaderToAggsender converts an agglayer CertificateHeader to an aggsender CertificateHeader
func ConvertAgglayerCertHeaderToAggsender(cert *agglayertypes.CertificateHeader) (*types.CertificateHeader, error) {
	if cert == nil {
		return nil, nil
	}

	return &types.CertificateHeader{
		Height:                  cert.Height,
		RetryCount:              0, // TODO: ??
		CertificateID:           cert.CertificateID,
		PreviousLocalExitRoot:   cert.PreviousLocalExitRoot,
		NewLocalExitRoot:        cert.NewLocalExitRoot,
		Status:                  cert.Status,
		FromBlock:               0, // we will deduce this from LER, ImportedBridgeExits, and contracts
		ToBlock:                 0, // we will deduce this from LER, ImportedBridgeExits, and contracts
		CreatedAt:               0, // we can not be certain about this value, so we set it to 0
		UpdatedAt:               0, // we can not be certain about this value, so we set it to 0
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

	return &agglayertypes.CertificateHeader{
		NetworkID:             networkID,
		Height:                cert.Height,
		CertificateID:         cert.CertificateID,
		PreviousLocalExitRoot: cert.PreviousLocalExitRoot,
		NewLocalExitRoot:      cert.NewLocalExitRoot,
		Status:                cert.Status,
		Metadata:              common.ZeroHash, // metadata is no longer used, and is forced to be zero hash
	}
}
