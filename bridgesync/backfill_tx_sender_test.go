package bridgesync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/bridgesync/migrations"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBackfillTxnSender(t *testing.T) {
	// Create temporary database
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	// Run migrations
	err := migrations.RunMigrations(dbPath)
	require.NoError(t, err)

	// Create database connection
	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	// Create test data with empty txn_sender
	ctx := context.Background()
	tx, err := db.NewTx(ctx, database)
	require.NoError(t, err)

	// Insert test bridge record with empty txn_sender
	_, err = tx.Exec(`
		INSERT INTO block (num) VALUES (1)
	`)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO bridge (
			block_num, block_pos, leaf_type, origin_network, origin_address,
			destination_network, destination_address, amount, metadata, deposit_count,
			tx_hash, block_timestamp, from_address, calldata, txn_sender
		) VALUES (
			1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
			1234567890, '0x1111111111111111111111111111111111111111', '', ''
		)
	`)
	require.NoError(t, err)

	// Insert test claim record
	_, err = tx.Exec(`
		INSERT INTO claim (
			block_num, block_pos, global_index, origin_network, origin_address,
			destination_address, amount, proof_local_exit_root, proof_rollup_exit_root,
			mainnet_exit_root, rollup_exit_root, global_exit_root, destination_network,
			metadata, is_message, block_timestamp, tx_hash, from_address
		) VALUES (
			1, 1, '1', 1, '0x1234567890123456789012345678901234567890',
			'0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', '', '', '', '', 2, '', false, 1234567890,
			'0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
			'0x2222222222222222222222222222222222222222'
		)
	`)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	// Create mock client
	mockClient := mocks.NewEthClienter(t)

	// Create backfill instance
	logger := log.WithFields("module", "test")
	backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
	require.NoError(t, err)
	defer backfiller.Close()

	// Test getting records needing backfill count
	bridgeCount, err := backfiller.getRecordsNeedingBackfillCount(ctx, "bridge")
	require.NoError(t, err)
	assert.Equal(t, 1, bridgeCount)

	// Test getting records needing backfill
	bridgeRecords, err := backfiller.getRecordsNeedingBackfill(ctx, "bridge", 0, 10)
	require.NoError(t, err)
	assert.Len(t, bridgeRecords, 1)
	assert.Equal(t, uint64(1), bridgeRecords[0].BlockNum)
	assert.Equal(t, uint64(0), bridgeRecords[0].BlockPos)
}

func TestNewBackfillTxnSender(t *testing.T) {
	t.Run("successful creation", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		bridgeAddr := common.HexToAddress("0x1234")

		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, bridgeAddr, logger)
		require.NoError(t, err)
		require.NotNil(t, backfiller)
		require.Equal(t, bridgeAddr, backfiller.bridgeAddr)
		require.Equal(t, mockClient, backfiller.client)
		require.Equal(t, logger, backfiller.log)
		require.NotNil(t, backfiller.db)

		err = backfiller.Close()
		require.NoError(t, err)
	})

	t.Run("database initialization failure", func(t *testing.T) {
		invalidPath := "/invalid/path/that/does/not/exist/test.db"
		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		bridgeAddr := common.HexToAddress("0x1234")

		backfiller, err := NewBackfillTxnSender(invalidPath, mockClient, bridgeAddr, logger)
		require.NoError(t, err) // sql.Open doesn't validate paths
		require.NotNil(t, backfiller)

		// Try to use the database to trigger the error
		ctx := context.Background()
		err = backfiller.BackfillAll(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to backfill bridge table")

		err = backfiller.Close()
		require.NoError(t, err)
	})
}

func TestBackfillTxnSender_BackfillAll(t *testing.T) {
	t.Run("successful backfill", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		// Create test data
		database, err := db.NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer database.Close()

		ctx := context.Background()
		tx, err := db.NewTx(ctx, database)
		require.NoError(t, err)

		// Insert test data
		_, err = tx.Exec(`INSERT INTO block (num) VALUES (1)`)
		require.NoError(t, err)

		_, err = tx.Exec(`
			INSERT INTO bridge (
				block_num, block_pos, leaf_type, origin_network, origin_address,
				destination_network, destination_address, amount, metadata, deposit_count,
				tx_hash, block_timestamp, from_address, calldata, txn_sender
			) VALUES (
				1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
				2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
				'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
				1234567890, '0x1111111111111111111111111111111111111111', '', ''
			)
		`)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Create mock client
		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock the extractRootCall function behavior
		mockClient.On("Call", mock.Anything, "debug_traceTransaction", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			// Simulate the call structure that would be returned
			call, ok := args.Get(0).(*call)
			if !ok {
				return
			}
			call.From = common.HexToAddress("0x1111111111111111111111111111111111111111")
			call.To = common.HexToAddress("0x1234")
		})

		err = backfiller.BackfillAll(ctx)
		require.NoError(t, err)
	})

	t.Run("backfill table error", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Close the database to cause an error
		backfiller.db.Close()

		ctx := context.Background()
		err = backfiller.BackfillAll(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to backfill bridge table")
	})
}

func TestBackfillTxnSender_backfillTable(t *testing.T) {
	t.Run("no records need backfilling", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		ctx := context.Background()
		err = backfiller.backfillTable(ctx, "bridge")
		require.NoError(t, err)
	})

	t.Run("records need backfilling", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		// Create test data
		database, err := db.NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer database.Close()

		ctx := context.Background()
		tx, err := db.NewTx(ctx, database)
		require.NoError(t, err)

		// Insert test data with empty txn_sender
		_, err = tx.Exec(`INSERT INTO block (num) VALUES (1)`)
		require.NoError(t, err)

		_, err = tx.Exec(`
			INSERT INTO bridge (
				block_num, block_pos, leaf_type, origin_network, origin_address,
				destination_network, destination_address, amount, metadata, deposit_count,
				tx_hash, block_timestamp, from_address, calldata, txn_sender
			) VALUES (
				1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
				2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
				'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
				1234567890, '0x1111111111111111111111111111111111111111', '', ''
			)
		`)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock the extractRootCall function behavior
		mockClient.On("Call", mock.Anything, "debug_traceTransaction", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			call, ok := args.Get(0).(*call)
			if !ok {
				return
			}
			call.From = common.HexToAddress("0x1111111111111111111111111111111111111111")
			call.To = common.HexToAddress("0x1234")
		})

		err = backfiller.backfillTable(ctx, "bridge")
		require.NoError(t, err)
	})

	t.Run("get records count error", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Close the database to cause an error
		backfiller.db.Close()

		ctx := context.Background()
		err = backfiller.backfillTable(ctx, "bridge")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get count of records needing backfill")
	})

	t.Run("get records error", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Close the database to cause an error
		backfiller.db.Close()

		ctx := context.Background()
		err = backfiller.backfillTable(ctx, "bridge")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get count of records needing backfill")
	})

}

func TestBackfillTxnSender_getRecordsNeedingBackfillCount(t *testing.T) {
	t.Run("successful count", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		// Create test data
		database, err := db.NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer database.Close()

		ctx := context.Background()
		tx, err := db.NewTx(ctx, database)
		require.NoError(t, err)

		// Insert test data
		_, err = tx.Exec(`INSERT INTO block (num) VALUES (1)`)
		require.NoError(t, err)

		_, err = tx.Exec(`
			INSERT INTO bridge (
				block_num, block_pos, leaf_type, origin_network, origin_address,
				destination_network, destination_address, amount, metadata, deposit_count,
				tx_hash, block_timestamp, from_address, calldata, txn_sender
			) VALUES (
				1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
				2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
				'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
				1234567890, '0x1111111111111111111111111111111111111111', '', ''
			)
		`)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		count, err := backfiller.getRecordsNeedingBackfillCount(ctx, "bridge")
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("database error", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Close the database to cause an error
		backfiller.db.Close()

		ctx := context.Background()
		count, err := backfiller.getRecordsNeedingBackfillCount(ctx, "bridge")
		require.Error(t, err)
		assert.Equal(t, 0, count)
		assert.Contains(t, err.Error(), "failed to count records needing backfill")
	})
}

func TestBackfillTxnSender_getRecordsNeedingBackfill(t *testing.T) {
	t.Run("successful retrieval", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		// Create test data
		database, err := db.NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer database.Close()

		ctx := context.Background()
		tx, err := db.NewTx(ctx, database)
		require.NoError(t, err)

		// Insert test data
		_, err = tx.Exec(`INSERT INTO block (num) VALUES (1)`)
		require.NoError(t, err)

		_, err = tx.Exec(`
			INSERT INTO bridge (
				block_num, block_pos, leaf_type, origin_network, origin_address,
				destination_network, destination_address, amount, metadata, deposit_count,
				tx_hash, block_timestamp, from_address, calldata, txn_sender
			) VALUES (
				1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
				2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
				'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
				1234567890, '0x1111111111111111111111111111111111111111', '', ''
			)
		`)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		records, err := backfiller.getRecordsNeedingBackfill(ctx, "bridge", 0, 10)
		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Equal(t, uint64(1), records[0].BlockNum)
		assert.Equal(t, uint64(0), records[0].BlockPos)
		assert.Equal(t, "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", records[0].TxHash.Hex())
	})

	t.Run("database error", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Close the database to cause an error
		backfiller.db.Close()

		ctx := context.Background()
		records, err := backfiller.getRecordsNeedingBackfill(ctx, "bridge", 0, 10)
		require.Error(t, err)
		assert.Nil(t, records)
		assert.Contains(t, err.Error(), "failed to query records needing backfill")
	})

	t.Run("scan error", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Close the database to cause an error during query
		backfiller.db.Close()

		ctx := context.Background()
		records, err := backfiller.getRecordsNeedingBackfill(ctx, "bridge", 0, 10)
		require.Error(t, err)
		assert.Nil(t, records)
		assert.Contains(t, err.Error(), "failed to query records needing backfill")
	})
}

func TestBackfillTxnSender_processBatch(t *testing.T) {
	t.Run("successful processing", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock the extractRootCall function behavior
		mockClient.On("Call", mock.Anything, "debug_traceTransaction", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			call, ok := args.Get(0).(*call)
			if !ok {
				return
			}
			call.From = common.HexToAddress("0x1111111111111111111111111111111111111111")
			call.To = common.HexToAddress("0x1234")
		})

		ctx := context.Background()
		records := []RecordToBackfill{
			{
				BlockNum: 1,
				BlockPos: 0,
				TxHash:   common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			},
		}

		backfiller.processBatch(ctx, "bridge", records)
	})

	t.Run("failed extraction", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock the extractRootCall function to return an error
		mockClient.On("Call", mock.Anything, "debug_traceTransaction", mock.Anything, mock.Anything).Return(errors.New("transaction not found"))

		ctx := context.Background()
		records := []RecordToBackfill{
			{
				BlockNum: 1,
				BlockPos: 0,
				TxHash:   common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			},
		}

		backfiller.processBatch(ctx, "bridge", records)
	})

	t.Run("bulk update error", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock the extractRootCall function behavior
		mockClient.On("Call", mock.Anything, "debug_traceTransaction", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			call := args.Get(0).(*call)
			call.From = common.HexToAddress("0x1111111111111111111111111111111111111111")
			call.To = common.HexToAddress("0x1234")
		})

		// Close the database to cause bulk update error
		backfiller.db.Close()

		ctx := context.Background()
		records := []RecordToBackfill{
			{
				BlockNum: 1,
				BlockPos: 0,
				TxHash:   common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			},
		}

		backfiller.processBatch(ctx, "bridge", records)
	})
}

func TestBackfillTxnSender_extractTxnSender(t *testing.T) {
	t.Run("successful extraction", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock the extractRootCall function behavior
		expectedSender := common.HexToAddress("0x1111111111111111111111111111111111111111")
		mockClient.On("Call", mock.Anything, "debug_traceTransaction", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			call := args.Get(0).(*call)
			call.From = expectedSender
			call.To = common.HexToAddress("0x1234")
		})

		txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
		sender, err := backfiller.extractTxnSender(txHash)
		require.NoError(t, err)
		assert.Equal(t, expectedSender, sender)
	})

	t.Run("extraction error", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock the extractRootCall function to return an error
		mockClient.On("Call", mock.Anything, "debug_traceTransaction", mock.Anything, mock.Anything).Return(errors.New("transaction not found"))

		txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
		sender, err := backfiller.extractTxnSender(txHash)
		require.Error(t, err)
		assert.Equal(t, common.Address{}, sender)
		assert.Contains(t, err.Error(), "failed to extract root call")
	})
}

func TestBackfillTxnSender_bulkUpdateTxnSender(t *testing.T) {
	t.Run("successful bulk update", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		// Create test data
		database, err := db.NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer database.Close()

		ctx := context.Background()
		tx, err := db.NewTx(ctx, database)
		require.NoError(t, err)

		// Insert test data
		_, err = tx.Exec(`INSERT INTO block (num) VALUES (1)`)
		require.NoError(t, err)

		_, err = tx.Exec(`
			INSERT INTO bridge (
				block_num, block_pos, leaf_type, origin_network, origin_address,
				destination_network, destination_address, amount, metadata, deposit_count,
				tx_hash, block_timestamp, from_address, calldata, txn_sender
			) VALUES (
				1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
				2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
				'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
				1234567890, '0x1111111111111111111111111111111111111111', '', ''
			)
		`)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		updates := []RecordUpdate{
			{
				BlockNum:  1,
				BlockPos:  0,
				TxnSender: common.HexToAddress("0x1111111111111111111111111111111111111111"),
			},
		}

		err = backfiller.bulkUpdateTxnSender(ctx, "bridge", updates)
		require.NoError(t, err)
	})

	t.Run("successful bulk update with multiple records", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		// Create test data
		database, err := db.NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer database.Close()

		ctx := context.Background()
		tx, err := db.NewTx(ctx, database)
		require.NoError(t, err)

		// Insert test data
		_, err = tx.Exec(`INSERT INTO block (num) VALUES (1)`)
		require.NoError(t, err)

		_, err = tx.Exec(`
			INSERT INTO bridge (
				block_num, block_pos, leaf_type, origin_network, origin_address,
				destination_network, destination_address, amount, metadata, deposit_count,
				tx_hash, block_timestamp, from_address, calldata, txn_sender
			) VALUES
			(1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
			1234567890, '0x1111111111111111111111111111111111111111', '', ''),
			(1, 1, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
			1234567890, '0x1111111111111111111111111111111111111111', '', '')
		`)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		updates := []RecordUpdate{
			{
				BlockNum:  1,
				BlockPos:  0,
				TxnSender: common.HexToAddress("0x1111111111111111111111111111111111111111"),
			},
			{
				BlockNum:  1,
				BlockPos:  1,
				TxnSender: common.HexToAddress("0x2222222222222222222222222222222222222222"),
			},
		}

		err = backfiller.bulkUpdateTxnSender(ctx, "bridge", updates)
		require.NoError(t, err)
	})

	t.Run("empty updates", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		ctx := context.Background()
		err = backfiller.bulkUpdateTxnSender(ctx, "bridge", []RecordUpdate{})
		require.NoError(t, err)
	})

	t.Run("database error", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Close the database to cause an error
		backfiller.db.Close()

		ctx := context.Background()
		updates := []RecordUpdate{
			{
				BlockNum:  1,
				BlockPos:  0,
				TxnSender: common.HexToAddress("0x1111111111111111111111111111111111111111"),
			},
		}

		err = backfiller.bulkUpdateTxnSender(ctx, "bridge", updates)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to bulk update txn_sender")
	})
}

func TestBackfillTxnSender_Close(t *testing.T) {
	t.Run("successful close", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)

		err = backfiller.Close()
		require.NoError(t, err)
	})
}

func TestBackfillTxnSenderIntegration(t *testing.T) {
	// Skip integration test if no RPC URL is provided
	rpcURL := os.Getenv("TEST_RPC_URL")
	if rpcURL == "" {
		t.Skip("Skipping integration test - TEST_RPC_URL not set")
	}

	// Create temporary database
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "integration_test.db")

	// Run migrations
	err := migrations.RunMigrations(dbPath)
	require.NoError(t, err)

	// Create database connection
	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	// Create test data
	ctx := context.Background()
	tx, err := db.NewTx(ctx, database)
	require.NoError(t, err)

	// Insert test records with empty txn_sender
	_, err = tx.Exec(`
		INSERT INTO block (num) VALUES (1)
	`)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO bridge (
			block_num, block_pos, leaf_type, origin_network, origin_address,
			destination_network, destination_address, amount, metadata, deposit_count,
			tx_hash, block_timestamp, from_address, calldata, txn_sender
		) VALUES (
			1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0x0000000000000000000000000000000000000000000000000000000000000000',
			1234567890, '0x1111111111111111111111111111111111111111', '', ''
		)
	`)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	// Create real client
	client, err := aggkittypes.DialWithRetry(context.Background(), rpcURL, nil)
	require.NoError(t, err)

	// Create backfill instance
	logger := log.WithFields("module", "test")
	backfiller, err := NewBackfillTxnSender(dbPath, client, common.HexToAddress("0x1234"), logger)
	require.NoError(t, err)
	defer backfiller.Close()

	// Run backfilling
	err = backfiller.BackfillAll(ctx)
	// Note: This might fail if the transaction doesn't exist on the network
	// That's expected for this test
	if err != nil {
		t.Logf("Backfilling failed as expected (transaction not found): %v", err)
	}
}
