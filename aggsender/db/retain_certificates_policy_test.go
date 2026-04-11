package db

import (
	"fmt"
	"path"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

func TestRetainPolicy_NCerts(t *testing.T) {
	testCases := []struct {
		name                           string
		cfgString                      string
		RetainCertificatesCount        uint32
		keepCertificatesHistory        bool
		howManyCertsToInsert           int
		howmanyRetriesPerCert          int
		expectedCentificateInfo        []CertificateKey
		expectedCentificateInfoHistory []CertificateKey
	}{
		{
			name:                           "retain 1 certs with no history",
			cfgString:                      "retain last 1 certificates, keep history: false",
			RetainCertificatesCount:        1,
			keepCertificatesHistory:        false,
			howManyCertsToInsert:           5,
			howmanyRetriesPerCert:          2,
			expectedCentificateInfo:        []CertificateKey{{Height: 3, RetryCount: 1}, {Height: 4, RetryCount: 1}},
			expectedCentificateInfoHistory: []CertificateKey{},
		},
		{
			name:                           "retain 1 certs with history",
			cfgString:                      "retain last 1 certificates, keep history: true",
			RetainCertificatesCount:        1,
			keepCertificatesHistory:        true,
			howManyCertsToInsert:           5,
			howmanyRetriesPerCert:          2,
			expectedCentificateInfo:        []CertificateKey{{Height: 3, RetryCount: 1}, {Height: 4, RetryCount: 1}},
			expectedCentificateInfoHistory: []CertificateKey{{Height: 3, RetryCount: 0}, {Height: 4, RetryCount: 0}},
		},
		{
			name:                           "retain all certs, with history",
			cfgString:                      "retain all certificates, keep history: true",
			RetainCertificatesCount:        KeepAllCertificates,
			keepCertificatesHistory:        true,
			howManyCertsToInsert:           3,
			howmanyRetriesPerCert:          2,
			expectedCentificateInfo:        []CertificateKey{{Height: 0, RetryCount: 1}, {Height: 1, RetryCount: 1}, {Height: 2, RetryCount: 1}},
			expectedCentificateInfoHistory: []CertificateKey{{Height: 0, RetryCount: 0}, {Height: 1, RetryCount: 0}, {Height: 2, RetryCount: 0}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			storage := newTestStorage(t, "rcpt_1", &StorageRetainCertificatesPolicy{
				RetainCertificatesCount: tc.RetainCertificatesCount,
				KeepCertificatesHistory: tc.keepCertificatesHistory,
			})
			if tc.cfgString != "" {
				require.Equal(t, tc.cfgString, storage.cfg.RetainCertificatesPolicy.String())
			}
			for i := 0; i < tc.howManyCertsToInsert; i++ {
				for r := 0; r < tc.howmanyRetriesPerCert; r++ {
					signedData := fmt.Sprintf("signed data for height %d retry %d", i, r)
					cert := types.Certificate{
						Header: &types.CertificateHeader{
							Height:     uint64(i),
							RetryCount: r,
						},
						SignedCertificate: &signedData,
					}
					err := storage.SaveLastSentCertificate(t.Context(), cert)
					require.NoError(t, err, "inserting cert height %d retry %d", i, r)
				}
			}
			// Check the expected number of certs in the main table
			certs, err := storage.getCerts(nil, tableCertificate, "", "", nil)
			require.NoError(t, err)
			certKeys := certsToCertificateKey(certs)
			require.Equal(t, tc.expectedCentificateInfo, certKeys)
			// Check the expected number of certs in the history table
			certs, err = storage.getCerts(nil, tableCertificateHistory, "", "", nil)
			require.NoError(t, err)
			certKeys = certsToCertificateKey(certs)
			require.Equal(t, tc.expectedCentificateInfoHistory, certKeys)
		})
	}
}

func TestRetainPolicy_Validate(t *testing.T) {
	require.NoError(t, NewStorageRetainCertificatesPolicyDefault().Validate())
	require.NoError(t, NewStorageRetainCertificatesPolicy(2, false).Validate())
	var rp *StorageRetainCertificatesPolicy
	require.Error(t, rp.Validate())
}

func TestRetainPolicy_String(t *testing.T) {
	var rp *StorageRetainCertificatesPolicy
	require.Equal(t, "nil", rp.String())
	require.Equal(t, "retain all certificates, keep history: true", NewStorageRetainCertificatesPolicyDefault().String())
	require.Equal(t, "retain last 2 certificates, keep history: false", NewStorageRetainCertificatesPolicy(2, false).String())
}

func certsToCertificateKey(certs []*types.CertificateHeader) []CertificateKey {
	certKeys := make([]CertificateKey, 0, len(certs))
	for _, c := range certs {
		certKeys = append(certKeys, CertificateKey{Height: c.Height, RetryCount: c.RetryCount})
	}
	return certKeys
}

func newTestStorage(t *testing.T, dbName string,
	retainCfg *StorageRetainCertificatesPolicy) *AggSenderSQLStorage {
	t.Helper()
	dbPath := path.Join(t.TempDir(), dbName)
	cfg := AggSenderSQLStorageConfig{
		DBPath:                   dbPath,
		CertificatesDir:          filepath.Join(filepath.Dir(dbPath), "certificates"),
		RetainCertificatesPolicy: *retainCfg,
	}
	storage, err := NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
	require.NoError(t, err)
	require.NotNil(t, storage)
	return storage
}
