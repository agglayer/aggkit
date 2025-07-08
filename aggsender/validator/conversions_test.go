package validator

import (
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
)

func TestAgglayerCertificateHeaderToAggsender(t *testing.T) {
	t.Run("NilCertificate", func(t *testing.T) {
		result, err := AgglayerCertificateHeaderToAggsender(nil)
		assert.Nil(t, result)
		assert.NoError(t, err)
	})

	t.Run("MetadataNotCompatible", func(t *testing.T) {
		cert := &agglayertypes.CertificateHeader{
			Metadata: common.HexToHash("0xff"),
		}
		result, err := AgglayerCertificateHeaderToAggsender(cert)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrMetadataNotCompatible)
	})
}

func TestAggsenderCertificateHeaderToAgglayer(t *testing.T) {
	t.Run("NilCertificate", func(t *testing.T) {
		result := AggsenderCertificateHeaderToAgglayer(nil, 1)
		assert.Nil(t, result)
	})

	t.Run("ValidConversion", func(t *testing.T) {
		prevLER := common.HexToHash("0x4444")
		cert := &types.CertificateHeader{
			Height:                10,
			CertificateID:         common.HexToHash("0x123"),
			PreviousLocalExitRoot: &prevLER,
			NewLocalExitRoot:      common.HexToHash("0x5555"),
			Status:                1,
			FromBlock:             100,
			ToBlock:               200,
			CreatedAt:             1234567890,
			CertType:              types.CertificateType(1),
		}
		result := AggsenderCertificateHeaderToAgglayer(cert, 42)
		assert.NotNil(t, result)
		assert.Equal(t, uint32(42), result.NetworkID)
		assert.Equal(t, cert.Height, result.Height)
		assert.Equal(t, cert.CertificateID, result.CertificateID)
		assert.Equal(t, cert.PreviousLocalExitRoot, result.PreviousLocalExitRoot)
		assert.Equal(t, cert.NewLocalExitRoot, result.NewLocalExitRoot)
		assert.Equal(t, cert.Status, result.Status)
	})
}
