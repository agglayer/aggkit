package db

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	dbmocks "github.com/agglayer/aggkit/db/mocks"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_StorageExploratory(t *testing.T) {
	t.Skip()
	path := os.Getenv("DB_AGGSENDER_0_2")
	if path == "" {
		t.Fatalf("environment variable DB_AGGSENDER_0_2 is not set")
	}
	cfg := AggSenderSQLStorageConfig{
		DBPath:                   path,
		CertificatesDir:          filepath.Join(filepath.Dir(path), "certificates"),
		RetainCertificatesPolicy: *NewStorageRetainCertificatesPolicyDefault(),
	}
	storage, err := NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
	require.NoError(t, err)
	cert, err := storage.GetLastSentCertificate()
	require.NoError(t, err)
	require.NotNil(t, cert)

	cfg.DBPath = "/nonexistent"
	_, err = NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
	require.Error(t, err)
}

func Test_Storage(t *testing.T) {
	ctx := context.Background()

	path := path.Join(t.TempDir(), "aggsenderTest_Storage.sqlite")
	log.Debugf("sqlite path: %s", path)
	cfg := AggSenderSQLStorageConfig{
		DBPath:                   path,
		CertificatesDir:          filepath.Join(filepath.Dir(path), "certificates"),
		RetainCertificatesPolicy: *NewStorageRetainCertificatesPolicyDefault(),
	}

	storage, err := NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
	require.NoError(t, err)

	updateTime := uint32(time.Now().UTC().UnixMilli())
	signedCert := "signed certificate"

	t.Run("SaveLastSentCertificate", func(t *testing.T) {
		certificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           1,
				CertificateID:    common.HexToHash("0x1"),
				NewLocalExitRoot: common.HexToHash("0x2"),
				FromBlock:        1,
				ToBlock:          2,
				Status:           agglayertypes.Settled,
				CreatedAt:        updateTime,
				UpdatedAt:        updateTime,
				CertType:         types.CertificateTypeFEP,
				CertSource:       types.CertificateSourceAggLayer,
			},
			AggchainProof: &types.AggchainProof{
				LastProvenBlock: 0,
				EndBlock:        2,
				CustomChainData: []byte{0x1, 0x2},
				LocalExitRoot:   common.HexToHash("0x3"),
				AggchainParams:  common.HexToHash("0x4"),
				Context: map[string][]byte{
					"key1": {0x1, 0x2},
				},
				SP1StarkProof: &types.SP1StarkProof{
					Version: "0.1",
					Proof:   []byte{0x1, 0x2, 0x3},
					Vkey:    []byte{0x4, 0x5, 0x6},
				},
			},
			ExtraData: "extra data",
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		certificateFromDB, err := storage.GetCertificateByHeight(certificate.Header.Height)
		require.NoError(t, err)
		require.Equal(t, certificate, *certificateFromDB)
		require.Equal(t, certificate.Header.CertType, certificateFromDB.Header.CertType, "equal cert type")
		require.Equal(t, certificate.Header.CertSource, certificateFromDB.Header.CertSource, "equal cert source")

		// try to save a certificate without certificate header
		certificateWithoutHeader := types.Certificate{
			Header: nil,
		}

		err = storage.SaveLastSentCertificate(ctx, certificateWithoutHeader)
		require.ErrorContains(t, err, "error converting certificate to certificate info: missing certificate header")

		require.NoError(t, storage.clean())
	})

	t.Run("DeleteCertificate", func(t *testing.T) {
		certificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           2,
				CertificateID:    common.HexToHash("0x3"),
				NewLocalExitRoot: common.HexToHash("0x4"),
				FromBlock:        3,
				ToBlock:          4,
				Status:           agglayertypes.Settled,
				CreatedAt:        updateTime,
				UpdatedAt:        updateTime,
			},
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		require.NoError(t, storage.DeleteCertificate(nil, certificate.Header.Height, MustDelete))

		certificateFromDB, err := storage.GetCertificateByHeight(certificate.Header.Height)
		require.ErrorIs(t, err, db.ErrNotFound)
		require.Nil(t, certificateFromDB)
		require.NoError(t, storage.clean())
	})

	t.Run("GetLastSentCertificate", func(t *testing.T) {
		// try getting a certificate that doesn't exist
		certificateFromDB, err := storage.GetLastSentCertificate()
		require.NoError(t, err)
		require.Nil(t, certificateFromDB)

		// try getting a certificate header that doesn't exist
		certificateHeaderFromDB, err := storage.GetLastSentCertificateHeader()
		require.NoError(t, err)
		require.Nil(t, certificateHeaderFromDB)

		// try getting a certificate that exists
		certificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           3,
				CertificateID:    common.HexToHash("0x5"),
				NewLocalExitRoot: common.HexToHash("0x6"),
				FromBlock:        5,
				ToBlock:          6,
				Status:           agglayertypes.Pending,
				CreatedAt:        updateTime,
				UpdatedAt:        updateTime,
			},
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		certificateFromDB, err = storage.GetLastSentCertificate()
		require.NoError(t, err)
		require.NotNil(t, certificateFromDB)
		require.Equal(t, certificate, *certificateFromDB)

		// try getting a certificate header that exists
		certificateHeaderFromDB, err = storage.GetLastSentCertificateHeader()
		require.NoError(t, err)
		require.NotNil(t, certificateHeaderFromDB)
		require.Equal(t, certificate.Header, certificateHeaderFromDB)

		require.NoError(t, storage.clean())
	})

	t.Run("GetCertificateByHeight", func(t *testing.T) {
		// try getting height 0
		certificateFromDB, err := storage.GetCertificateByHeight(0)
		require.NoError(t, err)
		require.Nil(t, certificateFromDB)

		// try getting a certificate header that doesn't exist
		certificateHeaderFromDB, err := storage.GetCertificateHeaderByHeight(0)
		require.NoError(t, err)
		require.Nil(t, certificateHeaderFromDB)

		// try getting a certificate that doesn't exist
		certificateFromDB, err = storage.GetCertificateByHeight(4)
		require.ErrorIs(t, err, db.ErrNotFound)
		require.Nil(t, certificateFromDB)

		// try getting a certificate that exists
		certificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           11,
				CertificateID:    common.HexToHash("0x17"),
				NewLocalExitRoot: common.HexToHash("0x18"),
				FromBlock:        17,
				ToBlock:          18,
				Status:           agglayertypes.Pending,
				CreatedAt:        updateTime,
				UpdatedAt:        updateTime,
			},
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		certificateFromDB, err = storage.GetCertificateByHeight(certificate.Header.Height)
		require.NoError(t, err)
		require.NotNil(t, certificateFromDB)
		require.Equal(t, certificate, *certificateFromDB)

		// try getting a certificate header that exists
		certificateHeaderFromDB, err = storage.GetCertificateHeaderByHeight(certificate.Header.Height)
		require.NoError(t, err)
		require.NotNil(t, certificateHeaderFromDB)
		require.Equal(t, certificate.Header, certificateHeaderFromDB)

		require.NoError(t, storage.clean())
	})

	t.Run("GetCertificatesByStatus", func(t *testing.T) {
		prevLER := common.HexToHash("0x9")
		finalizedL1InfoRoot := common.HexToHash("0xa")
		// Insert some certificates with different statuses
		certificates := []*types.Certificate{
			{
				Header: &types.CertificateHeader{
					Height:                  7,
					CertificateID:           common.HexToHash("0x7"),
					NewLocalExitRoot:        common.HexToHash("0x8"),
					FromBlock:               7,
					ToBlock:                 8,
					Status:                  agglayertypes.Settled,
					CreatedAt:               updateTime,
					UpdatedAt:               updateTime,
					PreviousLocalExitRoot:   &prevLER,
					FinalizedL1InfoTreeRoot: &finalizedL1InfoRoot,
				},
			},
			{
				Header: &types.CertificateHeader{
					Height:                  9,
					CertificateID:           common.HexToHash("0x9"),
					NewLocalExitRoot:        common.HexToHash("0xA"),
					FromBlock:               9,
					ToBlock:                 10,
					Status:                  agglayertypes.Pending,
					CreatedAt:               updateTime,
					UpdatedAt:               updateTime,
					PreviousLocalExitRoot:   &prevLER,
					FinalizedL1InfoTreeRoot: &finalizedL1InfoRoot,
					RetryCount:              0,
					L1InfoTreeLeafCount:     10,
				},
			},
			{
				Header: &types.CertificateHeader{
					Height:                  11,
					CertificateID:           common.HexToHash("0xB"),
					NewLocalExitRoot:        common.HexToHash("0xC"),
					FromBlock:               11,
					ToBlock:                 12,
					Status:                  agglayertypes.InError,
					CreatedAt:               updateTime,
					UpdatedAt:               updateTime,
					PreviousLocalExitRoot:   &prevLER,
					FinalizedL1InfoTreeRoot: &finalizedL1InfoRoot,
					L1InfoTreeLeafCount:     15,
					RetryCount:              0,
				},
				SignedCertificate: &signedCert,
				AggchainProof: &types.AggchainProof{
					LastProvenBlock: 10,
					EndBlock:        12,
					CustomChainData: []byte{0x1, 0x2},
					LocalExitRoot:   common.HexToHash("0x3"),
					AggchainParams:  common.HexToHash("0x4"),
					Context: map[string][]byte{
						"key1": {0x1, 0x2},
					},
					SP1StarkProof: &types.SP1StarkProof{
						Version: "0.1",
						Proof:   []byte{0x1, 0x2, 0x3},
						Vkey:    []byte{0x4, 0x5, 0x6},
					},
				},
			},
		}

		for _, cert := range certificates {
			require.NoError(t, storage.SaveLastSentCertificate(ctx, *cert))
		}

		// Test fetching certificates with status Settled
		statuses := []agglayertypes.CertificateStatus{agglayertypes.Settled}
		certificatesFromDB, err := storage.GetCertificateHeadersByStatus(statuses)
		require.NoError(t, err)
		require.Len(t, certificatesFromDB, 1)
		require.ElementsMatch(t, []*types.CertificateHeader{certificates[0].Header}, certificatesFromDB)

		// Test fetching certificates with status Pending
		statuses = []agglayertypes.CertificateStatus{agglayertypes.Pending}
		certificatesFromDB, err = storage.GetCertificateHeadersByStatus(statuses)
		require.NoError(t, err)
		require.Len(t, certificatesFromDB, 1)
		require.ElementsMatch(t, []*types.CertificateHeader{certificates[1].Header}, certificatesFromDB)

		// Test fetching certificates with status InError
		statuses = []agglayertypes.CertificateStatus{agglayertypes.InError}
		certificatesFromDB, err = storage.GetCertificateHeadersByStatus(statuses)
		require.NoError(t, err)
		require.Len(t, certificatesFromDB, 1)
		require.ElementsMatch(t, []*types.CertificateHeader{certificates[2].Header}, certificatesFromDB)

		// Test fetching certificates with status InError and Pending
		statuses = []agglayertypes.CertificateStatus{agglayertypes.InError, agglayertypes.Pending}
		certificatesFromDB, err = storage.GetCertificateHeadersByStatus(statuses)
		require.NoError(t, err)
		require.Len(t, certificatesFromDB, 2)
		require.ElementsMatch(t, []*types.CertificateHeader{certificates[1].Header, certificates[2].Header}, certificatesFromDB)

		require.NoError(t, storage.clean())
	})

	t.Run("UpdateCertificateStatus", func(t *testing.T) {
		// Insert a certificate
		certificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           13,
				RetryCount:       0,
				CertificateID:    common.HexToHash("0xD"),
				NewLocalExitRoot: common.HexToHash("0xE"),
				FromBlock:        13,
				ToBlock:          14,
				Status:           agglayertypes.Pending,
				CreatedAt:        updateTime,
				UpdatedAt:        updateTime,
			},
			SignedCertificate: &signedCert,
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		// Update the status of the certificate
		certificate.Header.Status = agglayertypes.Settled
		certificate.Header.UpdatedAt = updateTime + 1
		require.NoError(t, storage.UpdateCertificateStatus(ctx, certificate.Header.CertificateID, certificate.Header.Status, certificate.Header.UpdatedAt))

		// Fetch the certificate and verify the status has been updated
		certificateFromDB, err := storage.GetCertificateByHeight(certificate.Header.Height)
		require.NoError(t, err)
		require.Equal(t, certificate.Header.Status, certificateFromDB.Header.Status, "equal status")
		require.Equal(t, certificate.Header.UpdatedAt, certificateFromDB.Header.UpdatedAt, "equal updated at")

		require.NoError(t, storage.clean())
	})
}

func Test_SaveLastSentCertificate(t *testing.T) {
	ctx := context.Background()

	path := path.Join(t.TempDir(), "aggsenderTest_SaveLastSentCertificate.sqlite")
	log.Debugf("sqlite path: %s", path)
	cfg := AggSenderSQLStorageConfig{
		DBPath:                   path,
		CertificatesDir:          filepath.Join(filepath.Dir(path), "certificates"),
		RetainCertificatesPolicy: *NewStorageRetainCertificatesPolicyDefault(),
	}

	storage, err := NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
	require.NoError(t, err)

	updateTime := uint32(time.Now().UTC().UnixMilli())

	t.Run("SaveNewCertificate", func(t *testing.T) {
		certificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           1,
				CertificateID:    common.HexToHash("0x1"),
				NewLocalExitRoot: common.HexToHash("0x2"),
				FromBlock:        1,
				ToBlock:          2,
				Status:           agglayertypes.Settled,
				CreatedAt:        updateTime,
				UpdatedAt:        updateTime,
			},
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		certificateFromDB, err := storage.GetCertificateByHeight(certificate.Header.Height)
		require.NoError(t, err)
		require.Equal(t, certificate, *certificateFromDB)
		require.NoError(t, storage.clean())
	})

	t.Run("UpdateExistingCertificate", func(t *testing.T) {
		certificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           2,
				CertificateID:    common.HexToHash("0x3"),
				NewLocalExitRoot: common.HexToHash("0x4"),
				FromBlock:        3,
				ToBlock:          4,
				Status:           agglayertypes.InError,
				CreatedAt:        updateTime,
				UpdatedAt:        updateTime,
			},
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		// Update the certificate with a new retry for the same height
		updatedCertificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           2,
				CertificateID:    common.HexToHash("0x5"),
				NewLocalExitRoot: common.HexToHash("0x6"),
				FromBlock:        3,
				ToBlock:          6,
				Status:           agglayertypes.Pending,
				RetryCount:       1,
			},
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, updatedCertificate))

		certificateFromDB, err := storage.GetCertificateByHeight(updatedCertificate.Header.Height)
		require.NoError(t, err)
		require.Equal(t, updatedCertificate, *certificateFromDB)
		require.NoError(t, storage.clean())
	})

	t.Run("SaveCertificateWithRollback", func(t *testing.T) {
		// Simulate an error during the transaction to trigger a rollback
		certificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           3,
				CertificateID:    common.HexToHash("0x7"),
				NewLocalExitRoot: common.HexToHash("0x8"),
				FromBlock:        7,
				ToBlock:          8,
				Status:           agglayertypes.Settled,
				CreatedAt:        updateTime,
				UpdatedAt:        updateTime,
			},
		}

		// Close the database to force an error
		require.NoError(t, storage.db.Close())

		err := storage.SaveLastSentCertificate(ctx, certificate)
		require.Error(t, err)

		// Reopen the database and check that the certificate was not saved
		storage.db, err = db.NewSQLiteDB(path)
		require.NoError(t, err)

		certificateFromDB, err := storage.GetCertificateByHeight(certificate.Header.Height)
		require.ErrorIs(t, err, db.ErrNotFound)
		require.Nil(t, certificateFromDB)
		require.NoError(t, storage.clean())
	})

	t.Run("SaveCertificate with raw data", func(t *testing.T) {
		agglayerCertificate := &agglayertypes.Certificate{
			NetworkID:         1,
			Height:            1,
			PrevLocalExitRoot: common.HexToHash("0x1"),
			NewLocalExitRoot:  common.HexToHash("0x2"),
			BridgeExits: []*agglayertypes.BridgeExit{
				{
					LeafType: bridgetypes.LeafTypeAsset,
					TokenInfo: &agglayertypes.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x1"),
					},
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x2"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
				},
			},
			ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{},
		}

		raw, err := json.Marshal(agglayerCertificate)
		require.NoError(t, err)

		jsonCert := string(raw)

		certificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           1,
				CertificateID:    common.HexToHash("0x9"),
				NewLocalExitRoot: common.HexToHash("0x2"),
				FromBlock:        1,
				ToBlock:          10,
				Status:           agglayertypes.Pending,
				CreatedAt:        updateTime,
				UpdatedAt:        updateTime,
			},
			SignedCertificate: &jsonCert,
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		certificateFromDB, err := storage.GetCertificateByHeight(certificate.Header.Height)
		require.NoError(t, err)
		require.Equal(t, certificate, *certificateFromDB)
		require.Equal(t, raw, []byte(*certificateFromDB.SignedCertificate))

		require.NoError(t, storage.clean())
	})
}

func (a *AggSenderSQLStorage) clean() error {
	if _, err := a.db.Exec(`DELETE FROM certificate_info;`); err != nil {
		return err
	}

	if _, err := a.db.Exec(`DELETE FROM certificate_info_history;`); err != nil {
		return err
	}

	return nil
}

func Test_StoragePreviousLER(t *testing.T) {
	ctx := context.TODO()
	dbPath := path.Join(t.TempDir(), "Test_StoragePreviousLER.sqlite")
	cfg := AggSenderSQLStorageConfig{
		DBPath:                   dbPath,
		RetainCertificatesPolicy: *NewStorageRetainCertificatesPolicyDefault(),
	}
	storage, err := NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
	require.NoError(t, err)
	require.NotNil(t, storage)

	certNoLER := types.Certificate{
		Header: &types.CertificateHeader{
			Height:           0,
			CertificateID:    common.HexToHash("0x1"),
			Status:           agglayertypes.InError,
			NewLocalExitRoot: common.HexToHash("0x2"),
		},
	}
	err = storage.SaveLastSentCertificate(ctx, certNoLER)
	require.NoError(t, err)

	readCertNoLER, err := storage.GetCertificateByHeight(0)
	require.NoError(t, err)
	require.NotNil(t, readCertNoLER)
	require.Equal(t, certNoLER, *readCertNoLER)

	certLER := types.Certificate{
		Header: &types.CertificateHeader{
			Height:                1,
			CertificateID:         common.HexToHash("0x2"),
			Status:                agglayertypes.InError,
			NewLocalExitRoot:      common.HexToHash("0x2"),
			PreviousLocalExitRoot: &common.Hash{},
		},
	}
	err = storage.SaveLastSentCertificate(ctx, certLER)
	require.NoError(t, err)

	readCertWithLER, err := storage.GetCertificateByHeight(1)
	require.NoError(t, err)
	require.NotNil(t, readCertWithLER)
	require.Equal(t, certLER, *readCertWithLER)
}

func Test_StorageFinalizedL1InfoRoot(t *testing.T) {
	ctx := context.TODO()
	dbPath := path.Join(t.TempDir(), "Test_StorageFinalizedL1InfoRoot.sqlite")
	cfg := AggSenderSQLStorageConfig{
		DBPath:                   dbPath,
		RetainCertificatesPolicy: *NewStorageRetainCertificatesPolicyDefault(),
	}
	storage, err := NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
	require.NoError(t, err)
	require.NotNil(t, storage)

	certNoL1Root := types.Certificate{
		Header: &types.CertificateHeader{
			Height:           0,
			CertificateID:    common.HexToHash("0x11"),
			Status:           agglayertypes.Settled,
			NewLocalExitRoot: common.HexToHash("0x22"),
		},
	}
	require.NoError(t, storage.SaveLastSentCertificate(ctx, certNoL1Root))

	readCertNoLER, err := storage.GetCertificateByHeight(0)
	require.NoError(t, err)
	require.NotNil(t, readCertNoLER)
	require.Equal(t, certNoL1Root, *readCertNoLER)

	certWithL1Root := types.Certificate{
		Header: &types.CertificateHeader{
			Height:                  1,
			CertificateID:           common.HexToHash("0x22"),
			Status:                  agglayertypes.Settled,
			NewLocalExitRoot:        common.HexToHash("0x23"),
			FinalizedL1InfoTreeRoot: &common.Hash{},
			L1InfoTreeLeafCount:     100,
		},
	}
	require.NoError(t, storage.SaveLastSentCertificate(ctx, certWithL1Root))

	readCertWithL1Root, err := storage.GetCertificateByHeight(1)
	require.NoError(t, err)
	require.NotNil(t, readCertWithL1Root)
	require.Equal(t, certWithL1Root, *readCertWithL1Root)
	require.Equal(t, certWithL1Root.Header.L1InfoTreeLeafCount, readCertWithL1Root.Header.L1InfoTreeLeafCount)
}

func Test_StorageAggchainProof(t *testing.T) {
	t.Parallel()

	ctx := context.TODO()
	dbPath := path.Join(t.TempDir(), "Test_StorageAggchainProof.sqlite")
	cfg := AggSenderSQLStorageConfig{
		DBPath:                   dbPath,
		RetainCertificatesPolicy: *NewStorageRetainCertificatesPolicyDefault(),
	}
	storage, err := NewAggSenderSQLStorage(log.WithFields("module", "aggsender-db"), cfg)
	require.NoError(t, err)
	require.NotNil(t, storage)

	// no aggchain proof in cert
	certNoAggchainProof := types.Certificate{
		Header: &types.CertificateHeader{
			Height:           0,
			CertificateID:    common.HexToHash("0x111"),
			Status:           agglayertypes.Pending,
			NewLocalExitRoot: common.HexToHash("0x222"),
		},
	}
	require.NoError(t, storage.SaveLastSentCertificate(ctx, certNoAggchainProof))

	readCertNoAggchainProof, err := storage.GetCertificateByHeight(0)
	require.NoError(t, err)
	require.NotNil(t, readCertNoAggchainProof)
	require.Equal(t, certNoAggchainProof, *readCertNoAggchainProof)

	// aggchain proof in cert
	aggchainProof := &types.AggchainProof{
		LastProvenBlock: 10,
		EndBlock:        20,
		CustomChainData: []byte{0x1, 0x2, 0x3},
		LocalExitRoot:   common.HexToHash("0x123"),
		AggchainParams:  common.HexToHash("0x456"),
		Context: map[string][]byte{
			"key1": {0x1, 0x2},
		},
		SP1StarkProof: &types.SP1StarkProof{
			Version: "0.1",
			Proof:   []byte{0x1, 0x2, 0x3},
			Vkey:    []byte{0x4, 0x5, 0x6},
		},
	}

	certWithAggchainProof := types.Certificate{
		Header: &types.CertificateHeader{
			Height:           1,
			CertificateID:    common.HexToHash("0x222"),
			Status:           agglayertypes.Settled,
			NewLocalExitRoot: common.HexToHash("0x223"),
		},
		AggchainProof: aggchainProof,
	}
	require.NoError(t, storage.SaveLastSentCertificate(ctx, certWithAggchainProof))

	readCertWithAggchainProof, err := storage.GetCertificateByHeight(1)
	require.NoError(t, err)
	require.NotNil(t, readCertWithAggchainProof)
	require.Equal(t, certWithAggchainProof, *readCertWithAggchainProof)
}

func Test_GetLastSentCertificateHeaderWithProofIfInError(t *testing.T) {
	ctx := context.TODO()
	dbPath := path.Join(t.TempDir(), "Test_GetLastSentCertificateHeaderWithProofIfInError.sqlite")
	cfg := AggSenderSQLStorageConfig{
		DBPath:                   dbPath,
		RetainCertificatesPolicy: *NewStorageRetainCertificatesPolicyDefault(),
	}
	storage, err := NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
	require.NoError(t, err)
	require.NotNil(t, storage)

	t.Run("NoCertificates", func(t *testing.T) {
		header, proof, err := storage.GetLastSentCertificateHeaderWithProofIfInError(ctx)
		require.NoError(t, err)
		require.Nil(t, header)
		require.Nil(t, proof)
	})

	t.Run("CertificateNotInError", func(t *testing.T) {
		certificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           1,
				CertificateID:    common.HexToHash("0x1"),
				NewLocalExitRoot: common.HexToHash("0x2"),
				FromBlock:        1,
				ToBlock:          2,
				Status:           agglayertypes.Settled,
				CreatedAt:        uint32(time.Now().UTC().UnixMilli()),
				UpdatedAt:        uint32(time.Now().UTC().UnixMilli()),
				CertType:         types.CertificateTypeFEP,
				CertSource:       types.CertificateSourceLocal,
			},
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		header, proof, err := storage.GetLastSentCertificateHeaderWithProofIfInError(ctx)
		require.NoError(t, err)
		require.NotNil(t, header)
		require.Nil(t, proof)
		require.Equal(t, certificate.Header, header)
	})

	t.Run("CertificateInErrorWithProof", func(t *testing.T) {
		aggchainProof := &types.AggchainProof{
			LastProvenBlock: 10,
			EndBlock:        20,
			CustomChainData: []byte{0x1, 0x2, 0x3},
			LocalExitRoot:   common.HexToHash("0x123"),
			AggchainParams:  common.HexToHash("0x456"),
			Context: map[string][]byte{
				"key1": {0x1, 0x2},
			},
			SP1StarkProof: &types.SP1StarkProof{
				Version: "0.1",
				Proof:   []byte{0x1, 0x2, 0x3},
				Vkey:    []byte{0x4, 0x5, 0x6},
			},
		}

		certificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           2,
				CertificateID:    common.HexToHash("0x2"),
				NewLocalExitRoot: common.HexToHash("0x3"),
				FromBlock:        3,
				ToBlock:          4,
				Status:           agglayertypes.InError,
				CreatedAt:        uint32(time.Now().UTC().UnixMilli()),
				UpdatedAt:        uint32(time.Now().UTC().UnixMilli()),
				CertType:         types.CertificateTypeFEP,
				CertSource:       types.CertificateSourceLocal,
			},
			AggchainProof: aggchainProof,
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		header, proof, err := storage.GetLastSentCertificateHeaderWithProofIfInError(ctx)
		require.NoError(t, err)
		require.NotNil(t, header)
		require.NotNil(t, proof)
		require.Equal(t, certificate.Header, header)
		require.Equal(t, certificate.AggchainProof, proof)
	})

	t.Run("CertificateInErrorWithoutProof", func(t *testing.T) {
		certificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           3,
				CertificateID:    common.HexToHash("0x3"),
				NewLocalExitRoot: common.HexToHash("0x4"),
				FromBlock:        5,
				ToBlock:          6,
				Status:           agglayertypes.InError,
				CreatedAt:        uint32(time.Now().UTC().UnixMilli()),
				UpdatedAt:        uint32(time.Now().UTC().UnixMilli()),
				CertType:         types.CertificateTypeFEP,
				CertSource:       types.CertificateSourceLocal,
			},
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		header, proof, err := storage.GetLastSentCertificateHeaderWithProofIfInError(ctx)
		require.NoError(t, err)
		require.NotNil(t, header)
		require.Nil(t, proof)
		require.Equal(t, certificate.Header, header)
	})
}

func Test_SaveNonAcceptedCertificate_Nil(t *testing.T) {
	path := path.Join(t.TempDir(), "aggsenderTest_SaveNonAcceptedCertificate.sqlite")
	log.Debugf("sqlite path: %s", path)
	cfg := AggSenderSQLStorageConfig{
		DBPath:          path,
		CertificatesDir: filepath.Join(filepath.Dir(path), "certificates"),
	}
	storage, err := NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
	require.NoError(t, err)
	err = storage.SaveNonAcceptedCertificate(context.Background(), nil)
	require.ErrorContains(t, err, "param nonAcceptedCert is nil")
}

func Test_SaveNonAcceptedCertificate(t *testing.T) {
	ctx := context.Background()

	bridgeExits := []*agglayertypes.BridgeExit{
		{
			LeafType: bridgetypes.LeafTypeAsset,
			TokenInfo: &agglayertypes.TokenInfo{
				OriginNetwork:      1,
				OriginTokenAddress: common.HexToAddress("0x1"),
			},
			DestinationNetwork: 2,
			DestinationAddress: common.HexToAddress("0x2"),
			Amount:             big.NewInt(100),
			Metadata:           []byte("metadata"),
		},
	}

	importedBridgeExits := []*agglayertypes.ImportedBridgeExit{
		{
			BridgeExit: bridgeExits[0],
			ClaimData:  &agglayertypes.ClaimFromRollup{},
		},
	}

	createdAt := uint32(time.Now().UTC().UnixMilli())

	testCases := []struct {
		name                string
		mockDBFn            func()
		certificates        []*agglayertypes.Certificate
		OverrideFileContent bool
		certError           string
		expectedError       string
	}{
		{
			name: "SaveNonAcceptedCertificate_Success_PP_Certificate",
			certificates: []*agglayertypes.Certificate{
				{
					Height:              1,
					PrevLocalExitRoot:   common.HexToHash("0x1"),
					NewLocalExitRoot:    common.HexToHash("0x2"),
					NetworkID:           2,
					BridgeExits:         bridgeExits,
					ImportedBridgeExits: importedBridgeExits,
					L1InfoTreeLeafCount: 19,
					AggchainData: &agglayertypes.AggchainDataSignature{
						Signature: common.Hex2Bytes("0x1234567890abcdef"),
					},
				},
			},
			certError: "some error happened on agglayer",
		},
		{
			name: "SaveNonAcceptedCertificate_Success_FEP_Certificate",
			certificates: []*agglayertypes.Certificate{
				{
					Height:              2,
					PrevLocalExitRoot:   common.HexToHash("0x4"),
					NewLocalExitRoot:    common.HexToHash("0x5"),
					NetworkID:           3,
					BridgeExits:         bridgeExits,
					ImportedBridgeExits: importedBridgeExits,
					L1InfoTreeLeafCount: 20,
					AggchainData: &agglayertypes.AggchainDataProof{
						Proof:          common.Hex2Bytes("abcdef1234567890"),
						Version:        "0.1",
						Vkey:           common.Hex2Bytes("bcdef1234567890abcdef1234567890"),
						AggchainParams: common.HexToHash("0x7"),
						Context: map[string][]byte{
							"key1": {0x1, 0x2},
							"key2": {0x3, 0x4},
						},
						Signature: common.Hex2Bytes("1234567890abcdef1234567890abcdef"),
					},
				},
			},
			certError: "another error occurred",
		},
		{
			name: "SaveNonAcceptedCertificate_Multiple_Certificates",
			certificates: []*agglayertypes.Certificate{
				{
					Height:              11,
					PrevLocalExitRoot:   common.HexToHash("0x11"),
					NewLocalExitRoot:    common.HexToHash("0x22"),
					NetworkID:           2,
					BridgeExits:         bridgeExits,
					ImportedBridgeExits: importedBridgeExits,
					L1InfoTreeLeafCount: 12,
					AggchainData: &agglayertypes.AggchainDataSignature{
						Signature: common.Hex2Bytes("0x1234567890abcdef"),
					},
				},
				{
					Height:              12,
					PrevLocalExitRoot:   common.HexToHash("0x111"),
					NewLocalExitRoot:    common.HexToHash("0x222"),
					NetworkID:           2,
					BridgeExits:         bridgeExits,
					ImportedBridgeExits: importedBridgeExits,
					L1InfoTreeLeafCount: 15,
					AggchainData: &agglayertypes.AggchainDataSignature{
						Signature: common.Hex2Bytes("0x1234567890abcdef"),
					},
				},
			},
			certError: "yet another error occurred",
		},
		{
			name: "SaveNonAcceptedCertificate_Mismatch_file_on_disk",
			certificates: []*agglayertypes.Certificate{
				{
					Height:              11,
					PrevLocalExitRoot:   common.HexToHash("0x11"),
					NewLocalExitRoot:    common.HexToHash("0x22"),
					NetworkID:           2,
					BridgeExits:         bridgeExits,
					ImportedBridgeExits: importedBridgeExits,
					L1InfoTreeLeafCount: 12,
					AggchainData: &agglayertypes.AggchainDataSignature{
						Signature: common.Hex2Bytes("0x1234567890abcdef"),
					},
				},
			},
			certError:           "yet another error occurred",
			OverrideFileContent: true,
		},
		{
			name:         "SaveNonAcceptedCertificate_CommitAndRollbackFails",
			certificates: []*agglayertypes.Certificate{{}},
			mockDBFn: func() {
				txnMock := dbmocks.NewTxer(t)
				newTxer = func(_ context.Context, _ dbtypes.DBer) (dbtypes.Txer, error) {
					return txnMock, nil
				}
				txnMock.EXPECT().Exec(mock.Anything, aggkitcommon.AGGSENDER, nonAcceptedCertKey, mock.Anything, mock.Anything).Return(nil, nil).Once()
				txnMock.EXPECT().Commit().Return(errors.New("failed to commit tx")).Once()
				txnMock.EXPECT().Rollback().Return(errors.New("failed to rollback tx")).Once()
			},
			expectedError: "failed to commit tx",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				storage *AggSenderSQLStorage
				err     error
			)

			path := path.Join(t.TempDir(), "aggsenderTest_SaveNonAcceptedCertificate"+tc.name+".sqlite")
			log.Debugf("sqlite path: %s", path)
			cfg := AggSenderSQLStorageConfig{
				DBPath:          path,
				CertificatesDir: filepath.Join(filepath.Dir(path), "certificates"),
			}
			storage, err = NewAggSenderSQLStorage(log.WithFields("module", "aggsender-db"), cfg)
			require.NoError(t, err)

			if tc.mockDBFn != nil {
				tc.mockDBFn()
			}

			for _, cert := range tc.certificates {
				nonAcceptedCert, err := NewNonAcceptedCertificate(cert, createdAt, tc.certError)
				require.NoError(t, err, "should create non-accepted certificate without error")
				err = storage.SaveNonAcceptedCertificate(ctx, nonAcceptedCert)
				if tc.expectedError != "" {
					require.ErrorContains(t, err, tc.expectedError)
				} else {
					require.NoError(t, err, "should save non-accepted certificate without error")
				}
			}
			if tc.OverrideFileContent {
				// Override the content of the last saved certificate file to simulate file read error
				certFilePath := filepath.Join(cfg.CertificatesDir, nonAcceptedCertFilename)
				err = os.WriteFile(certFilePath, []byte("invalid json"), 0o644)
				require.NoError(t, err, "should override certificate file content without error")
			}

			if tc.expectedError == "" {
				nonAcceptedCert, err := storage.GetNonAcceptedCertificate()
				if tc.OverrideFileContent {
					require.ErrorContains(t, err, "certificate hash mismatch")
				} else {
					require.NoError(t, err, "should retrieve one non-accepted certificate from DB even though multiple were saved")

					var certificate agglayertypes.Certificate
					if err = json.Unmarshal([]byte(nonAcceptedCert.SignedCertificate), &certificate); err != nil {
						t.Fatalf("error unmarshalling non-accepted certificate: %v", err)
					}

					require.Equal(t, tc.certificates[len(tc.certificates)-1], &certificate, "last saved certificate should match the one retrieved from DB")
					require.Equal(t, tc.certError, nonAcceptedCert.Error, "error message should match the expected error")
					require.Equal(t, createdAt, nonAcceptedCert.CreatedAt, "created at timestamp should match the expected value")
				}
			}
		})
	}
}

func Test_GetNonAcceptedCert(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "Test_GetNonAcceptedCert.sqlite")
	cfg := AggSenderSQLStorageConfig{
		DBPath:          dbPath,
		CertificatesDir: filepath.Join(filepath.Dir(dbPath), "certificates"),
	}

	newTxer = db.NewTx

	storage, err := NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
	require.NoError(t, err)

	// Test with no non-accepted certificate
	nonAcceptedCert, err := storage.GetNonAcceptedCertificate()
	require.NoError(t, err)
	require.Nil(t, nonAcceptedCert, "should return nil when no non-accepted certificate exists")

	// Test with a non-accepted certificate
	certificate := &agglayertypes.Certificate{
		Height:              1,
		PrevLocalExitRoot:   common.HexToHash("0x1"),
		NewLocalExitRoot:    common.HexToHash("0x2"),
		NetworkID:           2,
		BridgeExits:         []*agglayertypes.BridgeExit{},
		ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{},
		L1InfoTreeLeafCount: 19,
	}
	nonAcceptedCert, err = NewNonAcceptedCertificate(certificate, uint32(time.Now().UTC().UnixMilli()), "test error")
	require.NoError(t, err)

	require.NoError(t, storage.SaveNonAcceptedCertificate(context.Background(), nonAcceptedCert))
	nonAcceptedCert, err = storage.GetNonAcceptedCertificate()
	require.NoError(t, err)
	require.NotNil(t, nonAcceptedCert, "should return a non-nil non-accepted certificate")

	var certificateFromDB agglayertypes.Certificate
	if err = json.Unmarshal([]byte(nonAcceptedCert.SignedCertificate), &certificateFromDB); err != nil {
		t.Fatalf("error unmarshalling non-accepted certificate: %v", err)
	}
	require.Equal(t, *certificate, certificateFromDB, "retrieved certificate should match the saved certificate")
}

func TestSaveOrUpdateCertificate(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "Test_GetNonAcceptedCert.sqlite")
	cfg := AggSenderSQLStorageConfig{
		DBPath:          dbPath,
		CertificatesDir: filepath.Join(filepath.Dir(dbPath), "certificates"),
	}

	newTxer = db.NewTx

	storage, err := NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
	require.NoError(t, err)

	signedCert := "signed-cert"

	// Save new certificate
	certificate := &types.Certificate{
		Header: &types.CertificateHeader{
			Height:           1,
			CertificateID:    common.HexToHash("0x1"),
			NewLocalExitRoot: common.HexToHash("0x2"),
			FromBlock:        1,
			ToBlock:          2,
			Status:           agglayertypes.Pending,
			CreatedAt:        uint32(time.Now().UTC().UnixMilli()),
		},
		SignedCertificate: &signedCert,
		ExtraData:         "extra data",
	}

	require.NoError(t, storage.SaveOrUpdateCertificate(t.Context(), *certificate))
	certificateFromDB, err := storage.GetCertificateByHeight(certificate.Header.Height)
	require.NoError(t, err)
	require.Equal(t, certificate, certificateFromDB)

	// Update existing certificate
	certificate.Header.Status = agglayertypes.Settled
	certificate.Header.UpdatedAt = uint32(time.Now().UTC().UnixMilli())
	require.NoError(t, storage.SaveOrUpdateCertificate(t.Context(), *certificate))

	certificateFromDB, err = storage.GetCertificateByHeight(certificate.Header.Height)
	require.NoError(t, err)
	require.Equal(t, certificate, certificateFromDB, "equal status")
}

func Test_GetLastSettledCertificate(t *testing.T) {
	ctx := context.Background()
	dbPath := path.Join(t.TempDir(), "Test_GetLastSettledCertificate.sqlite")
	cfg := AggSenderSQLStorageConfig{
		DBPath:                   dbPath,
		RetainCertificatesPolicy: *NewStorageRetainCertificatesPolicyDefault(),
	}
	storage, err := NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
	require.NoError(t, err)
	require.NotNil(t, storage)

	updateTime := uint32(time.Now().UTC().UnixMilli())

	t.Run("NoSettledCertificates", func(t *testing.T) {
		header, err := storage.GetLastSettledCertificate()
		require.ErrorIs(t, err, db.ErrNotFound)
		require.Nil(t, header)
	})

	t.Run("SingleSettledCertificate", func(t *testing.T) {
		cert := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           1,
				CertificateID:    common.HexToHash("0x1"),
				NewLocalExitRoot: common.HexToHash("0x2"),
				FromBlock:        1,
				ToBlock:          2,
				Status:           agglayertypes.Settled,
				CreatedAt:        updateTime,
				UpdatedAt:        updateTime,
			},
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, cert))

		header, err := storage.GetLastSettledCertificate()
		require.NoError(t, err)
		require.NotNil(t, header)
		require.Equal(t, cert.Header, header)

		require.NoError(t, storage.clean())
	})

	t.Run("MultipleCertificatesWithDifferentStatuses", func(t *testing.T) {
		certs := []*types.Certificate{
			{
				Header: &types.CertificateHeader{
					Height:           2,
					CertificateID:    common.HexToHash("0x2"),
					NewLocalExitRoot: common.HexToHash("0x3"),
					FromBlock:        2,
					ToBlock:          3,
					Status:           agglayertypes.Pending,
					CreatedAt:        updateTime,
					UpdatedAt:        updateTime,
				},
			},
			{
				Header: &types.CertificateHeader{
					Height:           3,
					CertificateID:    common.HexToHash("0x3"),
					NewLocalExitRoot: common.HexToHash("0x4"),
					FromBlock:        3,
					ToBlock:          4,
					Status:           agglayertypes.Settled,
					CreatedAt:        updateTime,
					UpdatedAt:        updateTime,
				},
			},
			{
				Header: &types.CertificateHeader{
					Height:           4,
					CertificateID:    common.HexToHash("0x4"),
					NewLocalExitRoot: common.HexToHash("0x5"),
					FromBlock:        4,
					ToBlock:          5,
					Status:           agglayertypes.InError,
					CreatedAt:        updateTime,
					UpdatedAt:        updateTime,
				},
			},
			{
				Header: &types.CertificateHeader{
					Height:           5,
					CertificateID:    common.HexToHash("0x5"),
					NewLocalExitRoot: common.HexToHash("0x6"),
					FromBlock:        5,
					ToBlock:          6,
					Status:           agglayertypes.Settled,
					CreatedAt:        updateTime,
					UpdatedAt:        updateTime,
				},
			},
		}

		for _, cert := range certs {
			require.NoError(t, storage.SaveLastSentCertificate(ctx, *cert))
		}

		header, err := storage.GetLastSettledCertificate()
		require.NoError(t, err)
		require.NotNil(t, header)
		// Should return the one with the highest height and Settled status
		require.Equal(t, certs[len(certs)-1].Header, header)

		require.NoError(t, storage.clean())
	})

	t.Run("ErrorFromDB", func(t *testing.T) {
		// Close DB to force an error
		require.NoError(t, storage.db.Close())
		header, err := storage.GetLastSettledCertificate()
		require.Error(t, err)
		require.Nil(t, header)
	})
}

func Test_RuntimeData_IsCompatible(t *testing.T) {
	tests := []struct {
		name        string
		runtime     RuntimeData
		storage     RuntimeData
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Compatible - same network ID",
			runtime:     RuntimeData{NetworkID: 1},
			storage:     RuntimeData{NetworkID: 1},
			expectError: false,
		},
		{
			name:        "Compatible - same network ID zero",
			runtime:     RuntimeData{NetworkID: 0},
			storage:     RuntimeData{NetworkID: 0},
			expectError: false,
		},
		{
			name:        "Incompatible - different network IDs",
			runtime:     RuntimeData{NetworkID: 1},
			storage:     RuntimeData{NetworkID: 2},
			expectError: true,
			errorMsg:    "network ID mismatch: 1 != 2",
		},
		{
			name:        "Incompatible - runtime zero, storage non-zero",
			runtime:     RuntimeData{NetworkID: 0},
			storage:     RuntimeData{NetworkID: 1},
			expectError: true,
			errorMsg:    "network ID mismatch: 0 != 1",
		},
		{
			name:        "Incompatible - runtime non-zero, storage zero",
			runtime:     RuntimeData{NetworkID: 5},
			storage:     RuntimeData{NetworkID: 0},
			expectError: true,
			errorMsg:    "network ID mismatch: 5 != 0",
		},
		{
			name:        "Incompatible - large network IDs",
			runtime:     RuntimeData{NetworkID: 4294967295}, // max uint32
			storage:     RuntimeData{NetworkID: 4294967294},
			expectError: true,
			errorMsg:    "network ID mismatch: 4294967295 != 4294967294",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.runtime.IsCompatible(tt.storage)

			if tt.expectError {
				require.Error(t, err)
				require.Equal(t, tt.errorMsg, err.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func Test_deleteCertificate(t *testing.T) {
	ctx := context.Background()
	logger := log.WithFields("test")

	// Helper function to setup database and create certificate
	setupCertificate := func(t *testing.T, testName string, signedCert *string) (*AggSenderSQLStorage, common.Hash, *certificateInfo) {
		t.Helper()
		dbPath := path.Join(t.TempDir(), testName+".sqlite")
		cfg := AggSenderSQLStorageConfig{
			DBPath:          dbPath,
			CertificatesDir: filepath.Join(filepath.Dir(dbPath), "certificates"),
		}
		storage, err := NewAggSenderSQLStorage(logger, cfg)
		require.NoError(t, err)

		testCertID := common.HexToHash("0x1234")
		certificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           1,
				CertificateID:    testCertID,
				NewLocalExitRoot: common.HexToHash("0x2"),
				FromBlock:        1,
				ToBlock:          2,
				Status:           agglayertypes.Pending,
				CreatedAt:        uint32(time.Now().Unix()),
				UpdatedAt:        uint32(time.Now().Unix()),
			},
			SignedCertificate: signedCert,
		}

		if signedCert != nil {
			require.NoError(t, storage.SaveOrUpdateCertificate(ctx, certificate))
		} else {
			require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))
		}

		// Get certificate info from database if signed certificate exists
		var certInfo *certificateInfo
		if signedCert != nil {
			var info certificateInfo
			err = meddler.QueryRow(storage.db, &info,
				"SELECT * FROM certificate_info WHERE certificate_id = $1", testCertID.String())
			require.NoError(t, err)
			certInfo = &info
		}

		return storage, testCertID, certInfo
	}

	// Helper function to test certificate deletion with file
	testCertificateDeleteWithFile := func(t *testing.T, testName string, certData *string, shouldFileBeDeleted bool) {
		t.Helper()
		storage, _, certInfo := setupCertificate(t, testName, certData)
		require.NotNil(t, certInfo.SignedCertificate)

		// Verify the generated file exists
		generatedFilePath := *certInfo.SignedCertificateFilename()
		_, err := os.Stat(generatedFilePath)
		require.NoError(t, err)

		// Create transaction and test deleteCertificate
		tx, err := db.NewTx(ctx, storage.db)
		require.NoError(t, err)
		shouldRollback := true
		defer func() {
			if shouldRollback {
				if rollbackErr := tx.Rollback(); rollbackErr != nil {
					t.Logf("Failed to rollback transaction: %v", rollbackErr)
				}
			}
		}()

		err = storage.DeleteCertificate(tx, certInfo.Height, MustDelete)
		require.NoError(t, err)

		require.NoError(t, tx.Commit())
		shouldRollback = false

		// Verify certificate is deleted from database
		_, err = storage.GetCertificateByHeight(1)
		require.ErrorIs(t, err, db.ErrNotFound)

		// Verify the generated file is deleted
		_, err = os.Stat(generatedFilePath)
		if shouldFileBeDeleted {
			require.True(t, os.IsNotExist(err))
		} else {
			require.NoError(t, err)
		}
	}

	t.Run("successful deletion without file", func(t *testing.T) {
		storage, _, _ := setupCertificate(t, "test_delete_no_file", nil)

		// Create transaction and test deleteCertificate
		tx, err := db.NewTx(ctx, storage.db)
		require.NoError(t, err)
		shouldRollback := true
		defer func() {
			if shouldRollback {
				if rollbackErr := tx.Rollback(); rollbackErr != nil {
					t.Logf("Failed to rollback transaction: %v", rollbackErr)
				}
			}
		}()
		height := uint64(1)
		err = storage.DeleteCertificate(tx, height, MustDelete)
		require.NoError(t, err)

		require.NoError(t, tx.Commit())
		shouldRollback = false

		// Verify certificate is deleted
		_, err = storage.GetCertificateByHeight(height)
		require.ErrorIs(t, err, db.ErrNotFound)
	})

	t.Run("successful deletion with JSON file", func(t *testing.T) {
		signedCertData := `{"test": "signed certificate data"}`
		testCertificateDeleteWithFile(t, "test_delete_with_file", &signedCertData, true)
	})

	t.Run("deletion with file path containing non-path data", func(t *testing.T) {
		rawCertData := "raw certificate data, not a file path"
		testCertificateDeleteWithFile(t, "test_delete_non_json", &rawCertData, true)
	})

	t.Run("non-existent certificate", func(t *testing.T) {
		storage, _, _ := setupCertificate(t, "test_delete_nonexistent", nil)

		// Try to delete a certificate that doesn't exist
		testNonExistingHeight := uint64(9999)
		// Create transaction and test deleteCertificate
		tx, err := db.NewTx(ctx, storage.db)
		require.NoError(t, err)
		shouldRollback := true
		defer func() {
			if shouldRollback {
				if rollbackErr := tx.Rollback(); rollbackErr != nil {
					t.Logf("Failed to rollback transaction: %v", rollbackErr)
				}
			}
		}()

		err = storage.DeleteCertificate(tx, testNonExistingHeight, MustDelete)
		require.ErrorIs(t, err, ErrNoCertDeleted)
	})

	t.Run("file deletion error should not fail the function", func(t *testing.T) {
		signedCertData := `{"test": "certificate data"}`
		storage, _, certInfo := setupCertificate(t, "test_delete_file_error", &signedCertData)
		require.NotNil(t, certInfo.SignedCertificate)

		// Delete the file manually to simulate a file deletion error scenario
		generatedFilePath := certInfo.SignedCertificateFilename()
		require.NotNil(t, generatedFilePath)
		err := os.Remove(*generatedFilePath)
		require.NoError(t, err)

		// Create transaction and test deleteCertificate
		tx, err := db.NewTx(ctx, storage.db)
		require.NoError(t, err)
		shouldRollback := true
		defer func() {
			if shouldRollback {
				if rollbackErr := tx.Rollback(); rollbackErr != nil {
					t.Logf("Failed to rollback transaction: %v", rollbackErr)
				}
			}
		}()

		// This should succeed despite the file being already deleted
		err = storage.DeleteCertificate(tx, certInfo.Height, MustDelete)
		require.NoError(t, err)

		require.NoError(t, tx.Commit())
		shouldRollback = false

		// Verify certificate is deleted from database
		_, err = storage.GetCertificateByHeight(1)
		require.ErrorIs(t, err, db.ErrNotFound)
	})
}

func TestGetCertificateBridgeExits(t *testing.T) {
	ctx := context.Background()
	dbPath := path.Join(t.TempDir(), "aggsenderTest_BridgeExits.sqlite")
	cfg := AggSenderSQLStorageConfig{
		DBPath:                   dbPath,
		CertificatesDir:          filepath.Join(filepath.Dir(dbPath), "certificates"),
		RetainCertificatesPolicy: *NewStorageRetainCertificatesPolicyDefault(),
	}
	storage, err := NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
	require.NoError(t, err)

	bridgeExits := []*agglayertypes.BridgeExit{
		{
			LeafType:           0,
			DestinationNetwork: 1,
			DestinationAddress: common.HexToAddress("0xdeadbeef"),
			Amount:             big.NewInt(1000),
		},
	}
	agglayerCert := agglayertypes.Certificate{
		NetworkID:    1,
		Height:       100,
		BridgeExits:  bridgeExits,
	}
	signedCertJSON, err := json.Marshal(agglayerCert)
	require.NoError(t, err)
	signedCertStr := string(signedCertJSON)

	t.Run("found certificate with bridge exits", func(t *testing.T) {
		cert := types.Certificate{
			Header: &types.CertificateHeader{
				Height:        100,
				CertificateID: common.HexToHash("0xabc"),
				Status:        agglayertypes.Settled,
				CreatedAt:     1000,
				UpdatedAt:     1000,
			},
			SignedCertificate: &signedCertStr,
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, cert))

		exits, err := storage.GetCertificateBridgeExits(100)
		require.NoError(t, err)
		require.Len(t, exits, 1)
		require.Equal(t, bridgeExits[0].DestinationNetwork, exits[0].DestinationNetwork)
		require.Equal(t, bridgeExits[0].DestinationAddress, exits[0].DestinationAddress)
		require.NoError(t, storage.clean())
	})

	t.Run("certificate not found returns nil", func(t *testing.T) {
		exits, err := storage.GetCertificateBridgeExits(999)
		require.ErrorIs(t, err, db.ErrNotFound)
		require.Nil(t, exits)
	})

	t.Run("certificate with nil signed certificate returns nil", func(t *testing.T) {
		cert := types.Certificate{
			Header: &types.CertificateHeader{
				Height:        101,
				CertificateID: common.HexToHash("0xdef"),
				Status:        agglayertypes.Pending,
				CreatedAt:     1000,
				UpdatedAt:     1000,
			},
			SignedCertificate: nil,
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, cert))

		exits, err := storage.GetCertificateBridgeExits(101)
		require.NoError(t, err)
		require.Nil(t, exits)
		require.NoError(t, storage.clean())
	})
}
