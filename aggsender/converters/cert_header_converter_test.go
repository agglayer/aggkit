package converters

import (
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestAgglayerCertificateHeaderToAggsender(t *testing.T) {
	t.Run("NilCertificate", func(t *testing.T) {
		result, err := ConvertAgglayerCertHeaderToAggsender(nil)
		require.Nil(t, result)
		require.NoError(t, err)
	})

	t.Run("ok", func(t *testing.T) {
		cert := &agglayertypes.CertificateHeader{}
		result, err := ConvertAgglayerCertHeaderToAggsender(cert)
		require.NotNil(t, result)
		require.NoError(t, err)
	})
}

func TestAggsenderCertificateHeaderToAgglayer(t *testing.T) {
	t.Run("NilCertificate", func(t *testing.T) {
		result := ConvertAggsenderCertHeaderToAgglayer(nil, 1)
		require.Nil(t, result)
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
		result := ConvertAggsenderCertHeaderToAgglayer(cert, 42)
		require.NotNil(t, result)
		require.Equal(t, uint32(42), result.NetworkID)
		require.Equal(t, cert.Height, result.Height)
		require.Equal(t, cert.CertificateID, result.CertificateID)
		require.Equal(t, cert.PreviousLocalExitRoot, result.PreviousLocalExitRoot)
		require.Equal(t, cert.NewLocalExitRoot, result.NewLocalExitRoot)
		require.Equal(t, cert.Status, result.Status)
		require.Equal(t, aggkitcommon.ZeroHash, result.Metadata) // metadata is forced to be zero hash
	})
}
