package db

import (
	"context"
	"encoding/json"
	"math/big"
	"path"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
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

	t.Run("SaveLastSentCertificate", func(t *testing.T) {
		certificate := types.CertificateInfo{
			Height:           1,
			CertificateID:    common.HexToHash("0x1"),
			NewLocalExitRoot: common.HexToHash("0x2"),
			FromBlock:        1,
			ToBlock:          2,
			Status:           agglayertypes.Settled,
			CreatedAt:        updateTime,
			UpdatedAt:        updateTime,
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		certificateFromDB, err := storage.GetCertificateByHeight(certificate.Height)
		require.NoError(t, err)

		require.Equal(t, certificate, *certificateFromDB)
		require.NoError(t, storage.clean())
	})

	t.Run("DeleteCertificate", func(t *testing.T) {
		certificate := types.CertificateInfo{
			Height:           2,
			CertificateID:    common.HexToHash("0x3"),
			NewLocalExitRoot: common.HexToHash("0x4"),
			FromBlock:        3,
			ToBlock:          4,
			Status:           agglayertypes.Settled,
			CreatedAt:        updateTime,
			UpdatedAt:        updateTime,
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		require.NoError(t, storage.DeleteCertificate(ctx, certificate.CertificateID))

		certificateFromDB, err := storage.GetCertificateByHeight(certificate.Height)
		require.ErrorIs(t, err, db.ErrNotFound)
		require.Nil(t, certificateFromDB)
		require.NoError(t, storage.clean())
	})

	t.Run("GetLastSentCertificate", func(t *testing.T) {
		// try getting a certificate that doesn't exist
		certificateFromDB, err := storage.GetLastSentCertificate()
		require.NoError(t, err)
		require.Nil(t, certificateFromDB)

		// try getting a certificate that exists
		certificate := types.CertificateInfo{
			Height:           3,
			CertificateID:    common.HexToHash("0x5"),
			NewLocalExitRoot: common.HexToHash("0x6"),
			FromBlock:        5,
			ToBlock:          6,
			Status:           agglayertypes.Pending,
			CreatedAt:        updateTime,
			UpdatedAt:        updateTime,
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		certificateFromDB, err = storage.GetLastSentCertificate()
		require.NoError(t, err)
		require.NotNil(t, certificateFromDB)
		require.Equal(t, certificate, *certificateFromDB)
		require.NoError(t, storage.clean())
	})

	t.Run("GetCertificateByHeight", func(t *testing.T) {
		// try getting height 0
		certificateFromDB, err := storage.GetCertificateByHeight(0)
		require.NoError(t, err)
		require.Nil(t, certificateFromDB)

		// try getting a certificate that doesn't exist
		certificateFromDB, err = storage.GetCertificateByHeight(4)
		require.ErrorIs(t, err, db.ErrNotFound)
		require.Nil(t, certificateFromDB)

		// try getting a certificate that exists
		certificate := types.CertificateInfo{
			Height:           11,
			CertificateID:    common.HexToHash("0x17"),
			NewLocalExitRoot: common.HexToHash("0x18"),
			FromBlock:        17,
			ToBlock:          18,
			Status:           agglayertypes.Pending,
			CreatedAt:        updateTime,
			UpdatedAt:        updateTime,
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		certificateFromDB, err = storage.GetCertificateByHeight(certificate.Height)
		require.NoError(t, err)
		require.NotNil(t, certificateFromDB)
		require.Equal(t, certificate, *certificateFromDB)
		require.NoError(t, storage.clean())
	})

	t.Run("GetCertificatesByStatus", func(t *testing.T) {
		prevLER := common.HexToHash("0x9")
		finalizedL1InfoRoot := common.HexToHash("0xa")
		// Insert some certificates with different statuses
		certificates := []*types.CertificateInfo{
			{
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
			{
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
			{
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
				AggchainProof: &types.AggchainProof{
					LastProvenBlock: 10,
					EndBlock:        12,
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
				},
			},
		}

		for _, cert := range certificates {
			require.NoError(t, storage.SaveLastSentCertificate(ctx, *cert))
		}

		// Test fetching certificates with status Settled
		statuses := []agglayertypes.CertificateStatus{agglayertypes.Settled}
		certificatesFromDB, err := storage.GetCertificatesByStatus(statuses, false)
		require.NoError(t, err)
		require.Len(t, certificatesFromDB, 1)
		require.ElementsMatch(t, []*types.CertificateInfo{certificates[0]}, certificatesFromDB)

		// Test fetching certificates with status Pending
		statuses = []agglayertypes.CertificateStatus{agglayertypes.Pending}
		certificatesFromDB, err = storage.GetCertificatesByStatus(statuses, false)
		require.NoError(t, err)
		require.Len(t, certificatesFromDB, 1)
		require.ElementsMatch(t, []*types.CertificateInfo{certificates[1]}, certificatesFromDB)

		// Test fetching certificates with status InError
		statuses = []agglayertypes.CertificateStatus{agglayertypes.InError}
		certificatesFromDB, err = storage.GetCertificatesByStatus(statuses, false)
		require.NoError(t, err)
		require.Len(t, certificatesFromDB, 1)
		require.ElementsMatch(t, []*types.CertificateInfo{certificates[2]}, certificatesFromDB)

		// Test fetching certificates with status InError and Pending
		statuses = []agglayertypes.CertificateStatus{agglayertypes.InError, agglayertypes.Pending}
		certificatesFromDB, err = storage.GetCertificatesByStatus(statuses, false)
		require.NoError(t, err)
		require.Len(t, certificatesFromDB, 2)
		require.ElementsMatch(t, []*types.CertificateInfo{certificates[1], certificates[2]}, certificatesFromDB)

		// Test fetching certificates with status InError, but with light query
		statuses = []agglayertypes.CertificateStatus{agglayertypes.InError}
		certificatesFromDB, err = storage.GetCertificatesByStatus(statuses, true)
		require.NoError(t, err)
		require.Len(t, certificatesFromDB, 1)
		require.Equal(t, certificates[2].Height, certificatesFromDB[0].Height)
		require.Equal(t, certificates[2].RetryCount, certificatesFromDB[0].RetryCount)
		require.Equal(t, certificates[2].CertificateID, certificatesFromDB[0].CertificateID)
		require.Equal(t, certificates[2].Status, certificatesFromDB[0].Status)
		require.Equal(t, certificates[2].CreatedAt, certificatesFromDB[0].CreatedAt)
		require.Equal(t, certificates[2].UpdatedAt, certificatesFromDB[0].UpdatedAt)
		require.Equal(t, certificates[2].FromBlock, certificatesFromDB[0].FromBlock)
		require.Equal(t, certificates[2].ToBlock, certificatesFromDB[0].ToBlock)
		require.Nil(t, certificatesFromDB[0].AggchainProof)

		require.NoError(t, storage.clean())
	})

	t.Run("UpdateCertificateStatus", func(t *testing.T) {
		// Insert a certificate
		certificate := types.CertificateInfo{
			Height:           13,
			RetryCount:       1234,
			CertificateID:    common.HexToHash("0xD"),
			NewLocalExitRoot: common.HexToHash("0xE"),
			FromBlock:        13,
			ToBlock:          14,
			Status:           agglayertypes.Pending,
			CreatedAt:        updateTime,
			UpdatedAt:        updateTime,
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		// Update the status of the certificate
		certificate.Status = agglayertypes.Settled
		certificate.UpdatedAt = updateTime + 1
		require.NoError(t, storage.UpdateCertificateStatus(ctx, certificate.CertificateID, certificate.Status, certificate.UpdatedAt))

		// Fetch the certificate and verify the status has been updated
		certificateFromDB, err := storage.GetCertificateByHeight(certificate.Height)
		require.NoError(t, err)
		require.Equal(t, certificate.Status, certificateFromDB.Status, "equal status")
		require.Equal(t, certificate.UpdatedAt, certificateFromDB.UpdatedAt, "equal updated at")

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
		certificate := types.CertificateInfo{
			Height:           1,
			CertificateID:    common.HexToHash("0x1"),
			NewLocalExitRoot: common.HexToHash("0x2"),
			FromBlock:        1,
			ToBlock:          2,
			Status:           agglayertypes.Settled,
			CreatedAt:        updateTime,
			UpdatedAt:        updateTime,
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		certificateFromDB, err := storage.GetCertificateByHeight(certificate.Height)
		require.NoError(t, err)
		require.Equal(t, certificate, *certificateFromDB)
		require.NoError(t, storage.clean())
	})

	t.Run("UpdateExistingCertificate", func(t *testing.T) {
		certificate := types.CertificateInfo{
			Height:           2,
			CertificateID:    common.HexToHash("0x3"),
			NewLocalExitRoot: common.HexToHash("0x4"),
			FromBlock:        3,
			ToBlock:          4,
			Status:           agglayertypes.InError,
			CreatedAt:        updateTime,
			UpdatedAt:        updateTime,
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		// Update the certificate with the same height
		updatedCertificate := types.CertificateInfo{
			Height:           2,
			CertificateID:    common.HexToHash("0x5"),
			NewLocalExitRoot: common.HexToHash("0x6"),
			FromBlock:        3,
			ToBlock:          6,
			Status:           agglayertypes.Pending,
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, updatedCertificate))

		certificateFromDB, err := storage.GetCertificateByHeight(updatedCertificate.Height)
		require.NoError(t, err)
		require.Equal(t, updatedCertificate, *certificateFromDB)
		require.NoError(t, storage.clean())
	})

	t.Run("SaveCertificateWithRollback", func(t *testing.T) {
		// Simulate an error during the transaction to trigger a rollback
		certificate := types.CertificateInfo{
			Height:           3,
			CertificateID:    common.HexToHash("0x7"),
			NewLocalExitRoot: common.HexToHash("0x8"),
			FromBlock:        7,
			ToBlock:          8,
			Status:           agglayertypes.Settled,
			CreatedAt:        updateTime,
			UpdatedAt:        updateTime,
		}

		// Close the database to force an error
		require.NoError(t, storage.db.Close())

		err := storage.SaveLastSentCertificate(ctx, certificate)
		require.Error(t, err)

		// Reopen the database and check that the certificate was not saved
		storage.db, err = db.NewSQLiteDB(path)
		require.NoError(t, err)

		certificateFromDB, err := storage.GetCertificateByHeight(certificate.Height)
		require.ErrorIs(t, err, db.ErrNotFound)
		require.Nil(t, certificateFromDB)
		require.NoError(t, storage.clean())
	})

	t.Run("SaveCertificate with raw data", func(t *testing.T) {
		certfiicate := &agglayertypes.Certificate{
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

		raw, err := json.Marshal(certfiicate)
		require.NoError(t, err)

		certificate := types.CertificateInfo{
			Height:            1,
			CertificateID:     common.HexToHash("0x9"),
			NewLocalExitRoot:  common.HexToHash("0x2"),
			FromBlock:         1,
			ToBlock:           10,
			Status:            agglayertypes.Pending,
			CreatedAt:         updateTime,
			UpdatedAt:         updateTime,
			SignedCertificate: string(raw),
		}
		require.NoError(t, storage.SaveLastSentCertificate(ctx, certificate))

		certificateFromDB, err := storage.GetCertificateByHeight(certificate.Height)
		require.NoError(t, err)
		require.Equal(t, certificate, *certificateFromDB)
		require.Equal(t, raw, []byte(certificateFromDB.SignedCertificate))

		require.NoError(t, storage.clean())
	})
}

func (a *AggSenderSQLStorage) clean() error {
	if _, err := a.db.Exec(`DELETE FROM certificate_info;`); err != nil {
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

	certNoLER := types.CertificateInfo{
		Height:           0,
		CertificateID:    common.HexToHash("0x1"),
		Status:           agglayertypes.InError,
		NewLocalExitRoot: common.HexToHash("0x2"),
	}
	err = storage.SaveLastSentCertificate(ctx, certNoLER)
	require.NoError(t, err)

	readCertNoLER, err := storage.GetCertificateByHeight(0)
	require.NoError(t, err)
	require.NotNil(t, readCertNoLER)
	require.Equal(t, certNoLER, *readCertNoLER)

	certLER := types.CertificateInfo{
		Height:                1,
		CertificateID:         common.HexToHash("0x2"),
		Status:                agglayertypes.InError,
		NewLocalExitRoot:      common.HexToHash("0x2"),
		PreviousLocalExitRoot: &common.Hash{},
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

	certNoL1Root := types.CertificateInfo{
		Height:           0,
		CertificateID:    common.HexToHash("0x11"),
		Status:           agglayertypes.Settled,
		NewLocalExitRoot: common.HexToHash("0x22"),
	}
	require.NoError(t, storage.SaveLastSentCertificate(ctx, certNoL1Root))

	readCertNoLER, err := storage.GetCertificateByHeight(0)
	require.NoError(t, err)
	require.NotNil(t, readCertNoLER)
	require.Equal(t, certNoL1Root, *readCertNoLER)

	certWithL1Root := types.CertificateInfo{
		Height:                  1,
		CertificateID:           common.HexToHash("0x22"),
		Status:                  agglayertypes.Settled,
		NewLocalExitRoot:        common.HexToHash("0x23"),
		FinalizedL1InfoTreeRoot: &common.Hash{},
		L1InfoTreeLeafCount:     100,
	}
	require.NoError(t, storage.SaveLastSentCertificate(ctx, certWithL1Root))

	readCertWithL1Root, err := storage.GetCertificateByHeight(1)
	require.NoError(t, err)
	require.NotNil(t, readCertWithL1Root)
	require.Equal(t, certWithL1Root, *readCertWithL1Root)
	require.Equal(t, certWithL1Root.L1InfoTreeLeafCount, readCertWithL1Root.L1InfoTreeLeafCount)
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
	certNoAggchainProof := types.CertificateInfo{
		Height:           0,
		CertificateID:    common.HexToHash("0x111"),
		Status:           agglayertypes.Pending,
		NewLocalExitRoot: common.HexToHash("0x222"),
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

	certWithAggchainProof := types.CertificateInfo{
		Height:           1,
		CertificateID:    common.HexToHash("0x222"),
		Status:           agglayertypes.Settled,
		NewLocalExitRoot: common.HexToHash("0x223"),
		AggchainProof:    aggchainProof,
	}
	require.NoError(t, storage.SaveLastSentCertificate(ctx, certWithAggchainProof))

	readCertWithAggchainProof, err := storage.GetCertificateByHeight(1)
	require.NoError(t, err)
	require.NotNil(t, readCertWithAggchainProof)
	require.Equal(t, certWithAggchainProof, *readCertWithAggchainProof)
}
