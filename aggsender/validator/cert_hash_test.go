package validator

import (
	"encoding/json"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
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

		hash, err := HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0x7f79b617b1ee18f06b6b5104d065ef71370688bbdf22d13c756e63a2b8b24f1e", hash.String())
	})

	t.Run("error hashing invalid cert ", func(t *testing.T) {
		cert := &agglayertypes.Certificate{
			NetworkID:           1,
			Height:              100,
			NewLocalExitRoot:    common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
			ImportedBridgeExits: nil,
			Metadata:            [32]byte{1, 2, 3, 4, 5},
			BridgeExits: []*agglayertypes.BridgeExit{
				{},
			},
		}
		_, err := HashCertificateToSign(cert)
		require.Error(t, err)
	})

	t.Run("check imported fields on hash", func(t *testing.T) {
		_, cert := getCertFromAggsenderDBForTest(t)
		hash, err := HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0x4916e5a0df28fcd1b53169780ca69b1e94e45423a3c40b5a150ef2b63d2d413d", hash.String())
		cert.NetworkID += 1
		hash, err = HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0xe641ff67323c6d21e960090bc3f57847a77deba552b4fef97b601e7aa19c767d", hash.String())
		cert.Height += 1
		hash, err = HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0xe9b32db20304fad88adc02ba06bf53c08ca7ba2afd5dd5b29a2dcef67ce0aac7", hash.String())
		cert.Metadata = [32]byte{6, 7, 8, 9, 10}
		hash, err = HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0x3c0656f208c2342b11da2a2fcc5e508de23e6653bb6703be34327418f0754960", hash.String())
	})
}

func TestCertificateIdHash(t *testing.T) {
	cert, unmarshalCert := getCertFromAggsenderDBForTest(t)
	id := unmarshalCert.CertificateID()
	require.Equal(t, cert.Header.CertificateID, id)
	hash, err := HashCertificateToSign(unmarshalCert)
	require.NoError(t, err)
	require.Equal(t, "0x4916e5a0df28fcd1b53169780ca69b1e94e45423a3c40b5a150ef2b63d2d413d", hash.String())
}

// Returns aggsender and agglayer cert
func getCertFromAggsenderDBForTest(t *testing.T) (*types.Certificate, *agglayertypes.Certificate) {
	t.Helper()
	dbPath := "testData/aggsender.sqlite"

	cfg := db.AggSenderSQLStorageConfig{
		DBPath:                  dbPath,
		KeepCertificatesHistory: true,
	}
	logger := log.WithFields("test", "TestCertificateHash")
	database, err := db.NewAggSenderSQLStorage(logger, cfg)
	require.NoError(t, err)
	cert, err := database.GetLastSentCertificate()
	require.NoError(t, err)
	var unmarshalCert *agglayertypes.Certificate
	err = json.Unmarshal([]byte(*cert.SignedCertificate), &unmarshalCert)
	require.NoError(t, err)
	return cert, unmarshalCert
}
