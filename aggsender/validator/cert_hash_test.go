package validator

import (
	"encoding/json"
	"fmt"
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

	t.Run("check imported fields on hash", func(t *testing.T) {
		_, cert := getCertFromAggsenderDBForTest(t)
		hash, err := HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0xe927a8204f8f4750e4cc2c3938e43b88eaccc7ddb5f21b54ede4a69843e87b3d", hash.String())
		cert.NetworkID += 1
		hash, err = HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0x84ecc0220119803af79a5be33d9efcaea76862da70ef044151d5ae1d7b12fd4c", hash.String())
		cert.Height += 1
		hash, err = HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0x356561c0a4fe69ef6a9eb6047c99aed2d76c5ab8639b5d43e5d40ea0e4914de8", hash.String())
		cert.Metadata = [32]byte{6, 7, 8, 9, 10}
		hash, err = HashCertificateToSign(cert)
		require.NoError(t, err)
		require.Equal(t, "0x52bf89461f0aba19c0120e716f7f562ec508d0d77135f2869abe856e793b646c", hash.String())
	})
}

func TestCertificateIdHash(t *testing.T) {
	cert, unmarshalCert := getCertFromAggsenderDBForTest(t)
	id := unmarshalCert.CertificateID()
	require.Equal(t, cert.Header.CertificateID, id)
	hash, err := HashCertificateToSign(unmarshalCert)
	require.NoError(t, err)
	require.Equal(t, "0xe927a8204f8f4750e4cc2c3938e43b88eaccc7ddb5f21b54ede4a69843e87b3d", hash.String())
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
	fmt.Println(*cert.SignedCertificate)
	err = json.Unmarshal([]byte(*cert.SignedCertificate), &unmarshalCert)
	require.NoError(t, err)
	return cert, unmarshalCert
}
