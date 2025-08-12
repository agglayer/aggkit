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
		require.Equal(t, "0xf60d40dabaa4d0a427d04a19b6cd58d57c28a5c58b76791e349fc1b5e0223c45", hash.String())
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
		require.Equal(t, "0x4e43189545291d5d69db51da28e7534b4fc1d501602c454111778f987a012977", hash.String())
		cert.NetworkID += 1
		hash, err = HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0xd19f41bf5692eefcbf8efbfeed974ac9adac836abd418504c48b1f25f9480bf5", hash.String())
		cert.Height += 1
		hash, err = HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0x53054a4b3a9b64077e38e29726d51307303f040d9624f8399492c714ad74f268", hash.String())
		cert.Metadata = [32]byte{6, 7, 8, 9, 10}
		hash, err = HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0xbb5243f1087a7e1fdb23954f20e03ac8b9b8aca0e01f2a7c38f16d1c23fbf4f1", hash.String())
	})
}

func TestCertificateIdHash(t *testing.T) {
	cert, unmarshalCert := getCertFromAggsenderDBForTest(t)
	id := unmarshalCert.CertificateID()
	require.Equal(t, cert.Header.CertificateID, id)
	hash, err := HashCertificateToSign(unmarshalCert)
	require.NoError(t, err)
	require.Equal(t, "0x4e43189545291d5d69db51da28e7534b4fc1d501602c454111778f987a012977", hash.String())
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
