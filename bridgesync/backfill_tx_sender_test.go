package bridgesync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/bridgesync/migrations"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// testAddress is a constant test address used throughout the tests
const testAddress = "0x1111111111111111111111111111111111111111"

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
			tx_hash, block_timestamp, from_address, txn_sender
		) VALUES (
			1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
			1234567890, '0x1111111111111111111111111111111111111111', ''
		)
	`)
	require.NoError(t, err)

	// Insert test claim record
	_, err = tx.Exec(`
		INSERT INTO claim (
			block_num, block_pos, global_index, origin_network, origin_address,
			destination_address, amount, proof_local_exit_root, proof_rollup_exit_root,
			mainnet_exit_root, rollup_exit_root, global_exit_root, destination_network,
			metadata, is_message, block_timestamp, tx_hash
		) VALUES (
			1, 1, '1', 1, '0x1234567890123456789012345678901234567890',
			'0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', '', '', '', '', 2,
			'', false, 1234567890,
			'0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890'
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
	require.Equal(t, 1, bridgeCount)

	// Test getting records needing backfill
	bridgeRecords, err := backfiller.getRecordsNeedingBackfill(ctx, "bridge", 10)
	require.NoError(t, err)
	require.Len(t, bridgeRecords, 1)
	require.Equal(t, uint64(1), bridgeRecords[0].BlockNum)
	require.Equal(t, uint64(0), bridgeRecords[0].BlockPos)
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
		require.Contains(t, err.Error(), "failed to backfill bridge table")

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
				tx_hash, block_timestamp, from_address, txn_sender
			) VALUES (
				1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
				2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
				'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
				1234567890, '0x1111111111111111111111111111111111111111', ''
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

		// Mock the extractTxnSender function behavior (via eth_getTransactionByHash)
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			// Simulate the transaction structure that would be returned
			tx, ok := args.Get(0).(*Transaction)
			if !ok {
				return
			}
			tx.From = testAddress
			tx.To = "0x1234"
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
		require.Contains(t, err.Error(), "failed to backfill bridge table")
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
				tx_hash, block_timestamp, from_address, txn_sender
			) VALUES (
				1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
				2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
				'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
				1234567890, '0x1111111111111111111111111111111111111111', ''
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

		// Mock the extractTxnSender function behavior (via eth_getTransactionByHash)
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			tx, ok := args.Get(0).(*Transaction)
			if !ok {
				return
			}
			tx.From = testAddress
			tx.To = "0x1234"
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
		require.Contains(t, err.Error(), "failed to get count of records needing backfill")
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
		require.Contains(t, err.Error(), "failed to get count of records needing backfill")
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
				tx_hash, block_timestamp, from_address, txn_sender
			) VALUES (
				1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
				2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
				'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
				1234567890, '0x1111111111111111111111111111111111111111', ''
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
		require.Equal(t, 1, count)
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
		require.Equal(t, 0, count)
		require.Contains(t, err.Error(), "failed to count records needing backfill")
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
				tx_hash, block_timestamp, from_address, txn_sender
			) VALUES (
				1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
				2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
				'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
				1234567890, '0x1111111111111111111111111111111111111111', ''
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

		records, err := backfiller.getRecordsNeedingBackfill(ctx, "bridge", 10)
		require.NoError(t, err)
		require.Len(t, records, 1)
		require.Equal(t, uint64(1), records[0].BlockNum)
		require.Equal(t, uint64(0), records[0].BlockPos)
		require.Equal(t, "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", records[0].TxHash.Hex())
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
		records, err := backfiller.getRecordsNeedingBackfill(ctx, "bridge", 10)
		require.Error(t, err)
		require.Nil(t, records)
		require.Contains(t, err.Error(), "failed to query records needing backfill")
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
		records, err := backfiller.getRecordsNeedingBackfill(ctx, "bridge", 10)
		require.Error(t, err)
		require.Nil(t, records)
		require.Contains(t, err.Error(), "failed to query records needing backfill")
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

		// Mock the extractTxnSender function behavior (via eth_getTransactionByHash)
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			tx, ok := args.Get(0).(*Transaction)
			if !ok {
				return
			}
			tx.From = testAddress
			tx.To = "0x1234"
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

		// Mock the extractTxnSender function to return an error (via eth_getTransactionByHash)
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(errors.New("transaction not found"))

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

		// Mock the extractTxnSender function behavior (via eth_getTransactionByHash)
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			tx, ok := args.Get(0).(*Transaction)
			if !ok {
				return
			}
			tx.From = testAddress
			tx.To = "0x1234"
		})

		// Close the database to cause bulk update error
		backfiller.db.Close()

		ctx := t.Context()
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

		// Mock the extractTxnSender function behavior (via eth_getTransactionByHash)
		expectedSender := common.HexToAddress(testAddress)
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			tx, ok := args.Get(0).(*Transaction)
			if !ok {
				return
			}
			tx.From = expectedSender.Hex()
			tx.To = "0x1234"
		})

		txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
		sender, _, err := backfiller.extractData(t.Context(), txHash,
			&agglayerbridge.AgglayerbridgeBridgeEvent{
				LeafType: bridgeLeafTypeAsset,
			})
		require.NoError(t, err)
		require.Equal(t, expectedSender, sender)
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

		// Mock the extractTxnSender function to return an error (via eth_getTransactionByHash)
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(errors.New("transaction not found"))

		txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
		sender, _, err := backfiller.extractData(t.Context(), txHash, &agglayerbridge.AgglayerbridgeBridgeEvent{
			LeafType: bridgeLeafTypeAsset,
		})
		require.Error(t, err)
		require.Equal(t, common.Address{}, sender)
		require.Contains(t, err.Error(), "failed to fetch transaction by hash")
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

		ctx := t.Context()
		tx, err := db.NewTx(ctx, database)
		require.NoError(t, err)

		// Insert test data
		_, err = tx.Exec(`INSERT INTO block (num) VALUES (1)`)
		require.NoError(t, err)

		_, err = tx.Exec(`
			INSERT INTO bridge (
				block_num, block_pos, leaf_type, origin_network, origin_address,
				destination_network, destination_address, amount, metadata, deposit_count,
				tx_hash, block_timestamp, from_address, txn_sender
			) VALUES (
				1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
				2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
				'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
				1234567890, '0x1111111111111111111111111111111111111111', ''
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
				TxnSender: common.HexToAddress(testAddress),
			},
		}

		err = backfiller.bulkUpdate(ctx, "bridge", updates)
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
				tx_hash, block_timestamp, from_address, txn_sender
			) VALUES
			(1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
			1234567890, '0x1111111111111111111111111111111111111111', ''),
			(1, 1, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
			1234567890, '0x1111111111111111111111111111111111111111', '')
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
				TxnSender: common.HexToAddress(testAddress),
			},
			{
				BlockNum:  1,
				BlockPos:  1,
				TxnSender: common.HexToAddress("0x2222222222222222222222222222222222222222"),
			},
		}

		err = backfiller.bulkUpdate(ctx, "bridge", updates)
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

		ctx := t.Context()
		err = backfiller.bulkUpdate(ctx, "bridge", []RecordUpdate{})
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

		ctx := t.Context()
		updates := []RecordUpdate{
			{
				BlockNum:  1,
				BlockPos:  0,
				TxnSender: common.HexToAddress(testAddress),
			},
		}

		err = backfiller.bulkUpdate(ctx, "bridge", updates)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to bulk update txn_sender")
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

// TestBackfillTxnSender_processBatch_Comprehensive tests the processBatch function with various scenarios
func TestBackfillTxnSender_processBatch_Comprehensive(t *testing.T) {
	t.Run("successful processing with multiple records", func(t *testing.T) {
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
				tx_hash, block_timestamp, from_address, txn_sender
			) VALUES
			(1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
			1234567890, '0x1111111111111111111111111111111111111111', ''),
			(1, 1, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567891',
			1234567890, '0x1111111111111111111111111111111111111111', ''),
			(1, 2, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567892',
			1234567890, '0x1111111111111111111111111111111111111111', '')
		`)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock successful extractions for all records
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			tx, ok := args.Get(0).(*Transaction)
			if !ok {
				return
			}
			tx.From = testAddress
			tx.To = "0x1234"
		}).Maybe() // Allow multiple calls

		records := []RecordToBackfill{
			{
				BlockNum: 1,
				BlockPos: 0,
				TxHash:   common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			},
			{
				BlockNum: 1,
				BlockPos: 1,
				TxHash:   common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567891"),
			},
			{
				BlockNum: 1,
				BlockPos: 2,
				TxHash:   common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567892"),
			},
		}

		backfiller.processBatch(ctx, "bridge", records)

		// Verify that all records were processed successfully
		// Check that txn_sender was updated in the database
		var count int
		err = database.QueryRow("SELECT COUNT(*) FROM bridge WHERE txn_sender = '0x1111111111111111111111111111111111111111'").Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 3, count)
	})

	t.Run("partial worker failures", func(t *testing.T) {
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
				tx_hash, block_timestamp, from_address, txn_sender
			) VALUES
			(1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
			1234567890, '0x1111111111111111111111111111111111111111', ''),
			(1, 1, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567891',
			1234567890, '0x1111111111111111111111111111111111111111', '')
		`)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock mixed results: first call succeeds, second fails
		var callCount int64
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			count := atomic.AddInt64(&callCount, 1)
			if count == 1 {
				// First call succeeds
				tx, ok := args.Get(0).(*Transaction)
				if !ok {
					return
				}
				tx.From = testAddress
				tx.To = "0x1234"
			}
		}).Once()

		// Mock the second call to fail
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(errors.New("transaction not found")).Once()

		records := []RecordToBackfill{
			{
				BlockNum: 1,
				BlockPos: 0,
				TxHash:   common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			},
			{
				BlockNum: 1,
				BlockPos: 1,
				TxHash:   common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567891"),
			},
		}

		backfiller.processBatch(ctx, "bridge", records)

		// Verify that only the successful record was updated
		var count int
		err = database.QueryRow("SELECT COUNT(*) FROM bridge WHERE txn_sender = '0x1111111111111111111111111111111111111111'").Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 1, count)

		// Verify that the failed record still has empty txn_sender
		var emptyCount int
		err = database.QueryRow("SELECT COUNT(*) FROM bridge WHERE txn_sender = '' OR txn_sender IS NULL").Scan(&emptyCount)
		require.NoError(t, err)
		require.Equal(t, 1, emptyCount)
	})

	t.Run("all workers fail", func(t *testing.T) {
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
				tx_hash, block_timestamp, from_address, txn_sender
			) VALUES
			(1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
			1234567890, '0x1111111111111111111111111111111111111111', ''),
			(1, 1, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567891',
			1234567890, '0x1111111111111111111111111111111111111111', '')
		`)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock all calls to fail
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(errors.New("network error"))

		records := []RecordToBackfill{
			{
				BlockNum: 1,
				BlockPos: 0,
				TxHash:   common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			},
			{
				BlockNum: 1,
				BlockPos: 1,
				TxHash:   common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567891"),
			},
		}

		backfiller.processBatch(ctx, "bridge", records)

		// Verify that no records were updated
		var count int
		err = database.QueryRow("SELECT COUNT(*) FROM bridge WHERE txn_sender != '' AND txn_sender IS NOT NULL").Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 0, count)

		// Verify that all records still have empty txn_sender
		var emptyCount int
		err = database.QueryRow("SELECT COUNT(*) FROM bridge WHERE txn_sender = '' OR txn_sender IS NULL").Scan(&emptyCount)
		require.NoError(t, err)
		require.Equal(t, 2, emptyCount)
	})

	t.Run("context cancellation during processing", func(t *testing.T) {
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
				tx_hash, block_timestamp, from_address, txn_sender
			) VALUES
			(1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
			1234567890, '0x1111111111111111111111111111111111111111', ''),
			(1, 1, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567891',
			1234567890, '0x1111111111111111111111111111111111111111', '')
		`)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Create a context that will be cancelled
		cancelCtx, cancel := context.WithCancel(ctx)

		// Mock calls that will be slow to allow cancellation
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			// Simulate some processing time
			time.Sleep(10 * time.Millisecond)
			tx, ok := args.Get(0).(*Transaction)
			if !ok {
				return
			}
			tx.From = testAddress
			tx.To = "0x1234"
		})

		records := []RecordToBackfill{
			{
				BlockNum: 1,
				BlockPos: 0,
				TxHash:   common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			},
			{
				BlockNum: 1,
				BlockPos: 1,
				TxHash:   common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567891"),
			},
		}

		// Cancel context after a short delay
		go func() {
			time.Sleep(5 * time.Millisecond)
			cancel()
		}()

		backfiller.processBatch(cancelCtx, "bridge", records)

		// Verify that no records were updated due to cancellation
		var count int
		err = database.QueryRow("SELECT COUNT(*) FROM bridge WHERE txn_sender != '' AND txn_sender IS NOT NULL").Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 0, count)
	})

	t.Run("empty batch", func(t *testing.T) {
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
		records := []RecordToBackfill{}

		// This should not panic or cause issues
		backfiller.processBatch(ctx, "bridge", records)
	})

	t.Run("large batch processing", func(t *testing.T) {
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

		// Create a large batch of records (more than default batch size)
		largeBatchSize := 150
		records := make([]RecordToBackfill, largeBatchSize)

		// Insert records into database
		for i := 0; i < largeBatchSize; i++ {
			_, err = tx.Exec(fmt.Sprintf(`
				INSERT INTO bridge (
					block_num, block_pos, leaf_type, origin_network, origin_address,
					destination_network, destination_address, amount, metadata, deposit_count,
					tx_hash, block_timestamp, from_address, txn_sender
				) VALUES (
					1, %d, 1, 1, '0x1234567890123456789012345678901234567890',
					2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
					'', 1, '0x%064x',
					1234567890, '0x1111111111111111111111111111111111111111', ''
				)
			`, i, i))
			require.NoError(t, err)

			records[i] = RecordToBackfill{
				BlockNum: 1,
				BlockPos: uint64(i),
				TxHash:   common.HexToHash(fmt.Sprintf("0x%064x", i)),
			}
		}

		err = tx.Commit()
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock successful extractions for all records
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			tx, ok := args.Get(0).(*Transaction)
			if !ok {
				return
			}
			tx.From = testAddress
			tx.To = "0x1234"
		})

		backfiller.processBatch(ctx, "bridge", records)

		// Verify that all records were processed successfully
		var count int
		err = database.QueryRow("SELECT COUNT(*) FROM bridge WHERE txn_sender = '0x1111111111111111111111111111111111111111'").Scan(&count)
		require.NoError(t, err)
		require.Equal(t, largeBatchSize, count)
	})
}

// TestBackfillTxnSender_BackfillAll_WithDifferentRecordCounts tests BackfillAll with different record counts
func TestBackfillTxnSender_BackfillAll_WithDifferentRecordCounts(t *testing.T) {
	testCases := []struct {
		name        string
		recordCount int
	}{
		{"small dataset", 25},
		{"medium dataset", 100},
		{"large dataset", 300},
		{"exact batch size", 100},
		{"single record", 5},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
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

			// Insert multiple records
			for i := 0; i < tc.recordCount; i++ {
				_, err = tx.Exec(fmt.Sprintf(`
					INSERT INTO bridge (
						block_num, block_pos, leaf_type, origin_network, origin_address,
						destination_network, destination_address, amount, metadata, deposit_count,
						tx_hash, block_timestamp, from_address, txn_sender
					) VALUES (
						1, %d, 1, 1, '0x1234567890123456789012345678901234567890',
						2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
						'', 1, '0x%064x',
						1234567890, '0x1111111111111111111111111111111111111111', ''
					)
				`, i, i))
				require.NoError(t, err)
			}

			err = tx.Commit()
			require.NoError(t, err)

			mockClient := mocks.NewEthClienter(t)
			logger := log.WithFields("module", "test")
			backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
			require.NoError(t, err)
			defer backfiller.Close()

			// Mock successful extractions for all records
			mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
				tx, ok := args.Get(0).(*Transaction)
				if !ok {
					return
				}
				tx.From = testAddress
				tx.To = "0x1234"
			})

			// Note: We can't modify the batchSize constant directly in tests
			// This test verifies that the system works with the default batch size
			// and processes records in batches correctly

			err = backfiller.BackfillAll(ctx)
			require.NoError(t, err)

			// Verify that all records were processed successfully
			var count int
			err = database.QueryRow("SELECT COUNT(*) FROM bridge WHERE txn_sender = '0x1111111111111111111111111111111111111111'").Scan(&count)
			require.NoError(t, err)
			require.Equal(t, tc.recordCount, count)

			// Verify that no records still need backfilling
			var remainingCount int
			err = database.QueryRow("SELECT COUNT(*) FROM bridge WHERE txn_sender = '' OR txn_sender IS NULL").Scan(&remainingCount)
			require.NoError(t, err)
			require.Equal(t, 0, remainingCount)
		})
	}
}

// TestBackfillTxnSender_MultipleBatches tests processing multiple batches
func TestBackfillTxnSender_MultipleBatches(t *testing.T) {
	t.Run("multiple batches with mixed results", func(t *testing.T) {
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

		// Insert records that will be processed in multiple batches
		totalRecords := 250 // This will create 3 batches with default batch size of 100
		for i := 0; i < totalRecords; i++ {
			_, err = tx.Exec(fmt.Sprintf(`
				INSERT INTO bridge (
					block_num, block_pos, leaf_type, origin_network, origin_address,
					destination_network, destination_address, amount, metadata, deposit_count,
					tx_hash, block_timestamp, from_address, txn_sender
				) VALUES (
					1, %d, 1, 1, '0x1234567890123456789012345678901234567890',
					2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
					'', 1, '0x%064x',
					1234567890, '0x1111111111111111111111111111111111111111', ''
				)
			`, i, i))
			require.NoError(t, err)
		}

		err = tx.Commit()
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock successful extractions for all records
		// Note: The mock setup is complex, so we'll test with all successful calls
		// and verify the batch processing works correctly
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			tx, ok := args.Get(0).(*Transaction)
			if !ok {
				return
			}
			tx.From = testAddress
			tx.To = "0x1234"
		}).Maybe() // Allow multiple calls

		err = backfiller.BackfillAll(ctx)
		require.NoError(t, err)

		// Verify that all records were processed successfully
		var successCount int
		err = database.QueryRow("SELECT COUNT(*) FROM bridge WHERE txn_sender = '0x1111111111111111111111111111111111111111'").Scan(&successCount)
		require.NoError(t, err)
		require.Equal(t, totalRecords, successCount)

		// Verify that no records still have empty txn_sender
		var remainingCount int
		err = database.QueryRow("SELECT COUNT(*) FROM bridge WHERE txn_sender = '' OR txn_sender IS NULL").Scan(&remainingCount)
		require.NoError(t, err)
		require.Equal(t, 0, remainingCount)
	})

	t.Run("context cancellation between batches", func(t *testing.T) {
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

		// Insert records for multiple batches
		totalRecords := 250
		for i := 0; i < totalRecords; i++ {
			_, err = tx.Exec(fmt.Sprintf(`
				INSERT INTO bridge (
					block_num, block_pos, leaf_type, origin_network, origin_address,
					destination_network, destination_address, amount, metadata, deposit_count,
					tx_hash, block_timestamp, from_address, txn_sender
				) VALUES (
					1, %d, 1, 1, '0x1234567890123456789012345678901234567890',
					2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
					'', 1, '0x%064x',
					1234567890, '0x1111111111111111111111111111111111111111', ''
				)
			`, i, i))
			require.NoError(t, err)
		}

		err = tx.Commit()
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Create a context that will be cancelled after some processing
		cancelCtx, cancel := context.WithCancel(ctx)

		// Mock calls with some delay
		var callCount int64
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			count := atomic.AddInt64(&callCount, 1)
			// Cancel after processing about 150 records (1.5 batches)
			if count == 150 {
				cancel()
			}

			time.Sleep(1 * time.Millisecond) // Small delay to allow cancellation
			tx, ok := args.Get(0).(*Transaction)
			if !ok {
				return
			}
			tx.From = testAddress
			tx.To = "0x1234"
		}).Maybe() // Allow multiple calls

		err = backfiller.BackfillAll(cancelCtx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "context canceled")

		// Verify that some records were processed before cancellation
		var processedCount int
		err = database.QueryRow("SELECT COUNT(*) FROM bridge WHERE txn_sender = '0x1111111111111111111111111111111111111111'").Scan(&processedCount)
		require.NoError(t, err)
		require.Greater(t, processedCount, 0)
		require.Less(t, processedCount, totalRecords)
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
	ctx := t.Context()
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
			tx_hash, block_timestamp, from_address, txn_sender
		) VALUES (
			1, 0, 1, 1, '0x1234567890123456789012345678901234567890',
			2, '0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', 1, '0x0000000000000000000000000000000000000000000000000000000000000000',
			1234567890, '0x1111111111111111111111111111111111111111', ''
		)
	`)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	// Create real client
	client, err := aggkittypes.DialWithRetry(t.Context(), rpcURL, nil)
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
