package db

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"path"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	dbmocks "github.com/agglayer/aggkit/db/mocks"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_Storage(t *testing.T) {
	ctx := context.Background()

	path := path.Join(t.TempDir(), "aggsenderTest_Storage.sqlite")
	log.Debugf("sqlite path: %s", path)
	cfg := AggSenderSQLStorageConfig{
		DBPath:                  path,
		KeepCertificatesHistory: true,
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

		require.NoError(t, storage.DeleteCertificate(ctx, certificate.Header.CertificateID))

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
					RetryCount:              1,
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
					RetryCount:              2,
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
				RetryCount:       1234,
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
		DBPath:                  path,
		KeepCertificatesHistory: true,
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

		// Update the certificate with the same height
		updatedCertificate := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           2,
				CertificateID:    common.HexToHash("0x5"),
				NewLocalExitRoot: common.HexToHash("0x6"),
				FromBlock:        3,
				ToBlock:          6,
				Status:           agglayertypes.Pending,
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
			Metadata:          common.HexToHash("0x3"),
			BridgeExits: []*agglayertypes.BridgeExit{
				{
					LeafType: agglayertypes.LeafTypeAsset,
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
		DBPath:                  dbPath,
		KeepCertificatesHistory: true,
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
		DBPath:                  dbPath,
		KeepCertificatesHistory: true,
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
		DBPath:                  dbPath,
		KeepCertificatesHistory: true,
	}
	storage, err := NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
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
		DBPath:                  dbPath,
		KeepCertificatesHistory: true,
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

func Test_SaveNonAcceptedCertificate(t *testing.T) {
	ctx := context.Background()

	bridgeExits := []*agglayertypes.BridgeExit{
		{
			LeafType: agglayertypes.LeafTypeAsset,
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
		name          string
		mockDBFn      func()
		certificates  []*agglayertypes.Certificate
		certError     string
		expectedError string
	}{
		{
			name: "SaveNonAcceptedCertificate_Success_PP_Certificate",
			certificates: []*agglayertypes.Certificate{
				{
					Height:              1,
					PrevLocalExitRoot:   common.HexToHash("0x1"),
					NewLocalExitRoot:    common.HexToHash("0x2"),
					Metadata:            common.HexToHash("0x3"),
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
					Metadata:            common.HexToHash("0x6"),
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
					Metadata:            common.HexToHash("0x33"),
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
					Metadata:            common.HexToHash("0x333"),
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
			name:         "SaveNonAcceptedCertificate_CommitAndRollbackFails",
			certificates: []*agglayertypes.Certificate{{}},
			mockDBFn: func() {
				txnMock := dbmocks.NewTxer(t)
				newTxer = func(_ context.Context, _ dbtypes.DBer) (dbtypes.Txer, error) {
					return txnMock, nil
				}
				txnMock.EXPECT().Exec(mock.Anything, aggkitcommon.AGGSENDER, nonAcceptedCertKey, mock.Anything, mock.Anything).Return(nil, nil)
				txnMock.EXPECT().Commit().Return(errors.New("failed to commit tx"))
				txnMock.EXPECT().Rollback().Return(errors.New("failed to rollback tx"))
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

			path := path.Join(t.TempDir(), "aggsenderTest_SaveNonAcceptedCertificate.sqlite")
			log.Debugf("sqlite path: %s", path)
			cfg := AggSenderSQLStorageConfig{
				DBPath: path,
			}
			storage, err = NewAggSenderSQLStorage(log.WithFields("aggsender-db"), cfg)
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

			if tc.expectedError == "" {
				nonAcceptedCert, err := storage.GetNonAcceptedCertificate()
				require.NoError(t, err, "should retrieve one non-accepted certificate from DB even though multiple were saved")

				var certificate agglayertypes.Certificate
				if err = json.Unmarshal([]byte(nonAcceptedCert.SignedCertificate), &certificate); err != nil {
					t.Fatalf("error unmarshalling non-accepted certificate: %v", err)
				}

				require.Equal(t, tc.certificates[len(tc.certificates)-1], &certificate, "last saved certificate should match the one retrieved from DB")
				require.Equal(t, tc.certError, nonAcceptedCert.Error, "error message should match the expected error")
				require.Equal(t, createdAt, nonAcceptedCert.CreatedAt, "created at timestamp should match the expected value")
			}
		})
	}
}

func Test_GetNonAcceptedCert(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "Test_GetNonAcceptedCert.sqlite")
	cfg := AggSenderSQLStorageConfig{
		DBPath: dbPath,
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
		Metadata:            common.HexToHash("0x3"),
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
