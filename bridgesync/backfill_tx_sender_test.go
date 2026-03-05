package bridgesync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/bridgesync/migrations"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// testAddress is a constant test address used throughout the tests
const testAddress = "0x1111111111111111111111111111111111111111"

// newTestBridge creates a Bridge with default test values using the given block position and tx hash.
// Both TxnSender and FromAddress are set to testAddress (non-empty hex strings via AddressMeddler).
// Use a SQL UPDATE to set txn_sender = ” (empty string) or txn_sender = NULL after inserting
// if the record needs to trigger backfill.
func newTestBridge(blockNum, blockPos uint64, txHash string) *Bridge {
	return &Bridge{
		BlockNum:           blockNum,
		BlockPos:           blockPos,
		LeafType:           1,
		OriginNetwork:      1,
		OriginAddress:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
		DestinationNetwork: 2,
		DestinationAddress: common.HexToAddress("0x0987654321098765432109876543210987654321"),
		Amount:             big.NewInt(1e18),
		Metadata:           []byte{},
		DepositCount:       1,
		TxHash:             common.HexToHash(txHash),
		BlockTimestamp:     1234567890,
		FromAddress:        func() *common.Address { a := common.HexToAddress(testAddress); return &a }(),
		TxnSender:          common.HexToAddress(testAddress),
	}
}

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

	require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 0,
		"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")))

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

	_, err = database.Exec("UPDATE bridge SET txn_sender = '' WHERE block_num = 1 AND block_pos = 0")
	require.NoError(t, err)

	// Create mock client
	mockClient := mocks.NewEthClienter(t)

	// Create backfill instance
	logger := log.WithFields("module", "test")
	backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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

		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, bridgeAddr, true, logger)
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

		backfiller, err := NewBackfillTxnSender(invalidPath, mockClient, bridgeAddr, true, logger)
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

		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")))

		err = tx.Commit()
		require.NoError(t, err)

		_, err = database.Exec("UPDATE bridge SET txn_sender = '' WHERE block_num = 1 AND block_pos = 0")
		require.NoError(t, err)

		// Create mock client
		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock the extractTxnSender function behavior (via eth_getTransactionByHash)
		// leaf_type=1 = bridgeLeafTypeMessage, so RPCTransactionByHash is used
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			// Simulate the transaction structure that would be returned
			tx, ok := args.Get(0).(*Transaction)
			if !ok {
				return
			}
			tx.FromRaw = testAddress
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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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

		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")))

		err = tx.Commit()
		require.NoError(t, err)

		_, err = database.Exec("UPDATE bridge SET txn_sender = '' WHERE block_num = 1 AND block_pos = 0")
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock the extractTxnSender function behavior (via eth_getTransactionByHash)
		// leaf_type=1 = bridgeLeafTypeMessage, so RPCTransactionByHash is used
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			tx, ok := args.Get(0).(*Transaction)
			if !ok {
				return
			}
			tx.FromRaw = testAddress
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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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

		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")))

		err = tx.Commit()
		require.NoError(t, err)

		_, err = database.Exec("UPDATE bridge SET txn_sender = '' WHERE block_num = 1 AND block_pos = 0")
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()

		count, err := backfiller.getRecordsNeedingBackfillCount(ctx, "bridge")
		require.NoError(t, err)
		require.Equal(t, 1, count)
	})

	t.Run("excludes backward_let and forward_let sources", func(t *testing.T) {
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
		_, err = tx.Exec(`INSERT INTO block (num) VALUES (1), (2), (3), (4)`)
		require.NoError(t, err)

		// Insert bridge with empty txn_sender and NULL source (should be counted)
		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")))

		// Insert bridge with empty txn_sender and backward_let source (should NOT be counted)
		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(2, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567891")))

		// Insert bridge with empty txn_sender and forward_let source (should NOT be counted)
		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(3, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567892")))

		// Insert bridge with empty txn_sender and no source (should be counted)
		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(4, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567893")))

		err = tx.Commit()
		require.NoError(t, err)

		_, err = database.Exec("UPDATE bridge SET txn_sender = '' WHERE block_pos = 0")
		require.NoError(t, err)
		_, err = database.Exec("UPDATE bridge SET from_address = '', source = 'backward_let' WHERE block_num = 2 AND block_pos = 0")
		require.NoError(t, err)
		_, err = database.Exec("UPDATE bridge SET from_address = '', source = 'forward_let' WHERE block_num = 3 AND block_pos = 0")
		require.NoError(t, err)
		_, err = database.Exec("UPDATE bridge SET from_address = '' WHERE block_num = 4 AND block_pos = 0")
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Should only count the 2 records without backward_let or forward_let source
		count, err := backfiller.getRecordsNeedingBackfillCount(ctx, "bridge")
		require.NoError(t, err)
		require.Equal(t, 2, count)

		// Verify getRecordsNeedingBackfill also excludes these sources
		records, err := backfiller.getRecordsNeedingBackfill(ctx, "bridge", 10)
		require.NoError(t, err)
		require.Len(t, records, 2)

		// Verify the correct records were returned (block_num 1 and 4)
		blockNums := []uint64{records[0].BlockNum, records[1].BlockNum}
		require.Contains(t, blockNums, uint64(1))
		require.Contains(t, blockNums, uint64(4))
	})

	t.Run("database error", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		// Run migrations
		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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

		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")))

		err = tx.Commit()
		require.NoError(t, err)

		_, err = database.Exec("UPDATE bridge SET txn_sender = '' WHERE block_num = 1 AND block_pos = 0")
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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

func mockClientCallGetTransactionByHash(t *testing.T,
	mockClient *mocks.EthClienter,
	expectedTxHash common.Hash, fromAddress string, toAddress string) {
	t.Helper()
	mockClient.EXPECT().Call(mock.Anything, GetTransactionByHashEndpoint, mock.Anything).Run(func(result any, method string, args ...any) {
		arg, ok := result.(*Transaction)
		require.True(t, ok)
		arg.FromRaw = fromAddress
		arg.To = toAddress
		arg.Hash = expectedTxHash.Hex()
		arg.Input = common.Bytes2Hex(BridgeAssetMethodID)
	}).Return(nil).Maybe()
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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()
		// txReceipt To is not bridgeAddr, so must call debugTrace
		mockClientCallGetTransactionByHash(t, mockClient,
			common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			testAddress, "0x0000000000000000000000000000000000000000000")
		mockClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
			Run(func(result any, method string, args ...any) {
				arg, ok := result.(*Call)
				require.True(t, ok)
				arg.Input = BridgeAssetMethodID
			}).Return(nil).Maybe()

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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()
		// txReceipt To is not bridgeAddr, so must call debugTrace
		mockClientCallGetTransactionByHash(t, mockClient,
			common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			testAddress, "0x0000000000000000000000000000000000000000000")
		mockClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).Return(errors.New("error")).Maybe()
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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()
		// txReceipt To is not bridgeAddr, so must call debugTrace
		mockClientCallGetTransactionByHash(t, mockClient,
			common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			testAddress, "0x0000000000000000000000000000000000000000000")
		mockClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
			Run(func(result any, method string, args ...any) {
				arg, ok := result.(*Call)
				require.True(t, ok)
				arg.Input = BridgeAssetMethodID
			}).Return(nil).Maybe()
		mockClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
			Run(func(result any, method string, args ...any) {
				arg, ok := result.(*Call)
				require.True(t, ok)
				arg.Input = BridgeAssetMethodID
			}).Return(nil).Maybe()

		// Close the database to cause bulk update error
		backfiller.db.Close()

		ctx := t.Context()
		addr := "0x1111111111111111111111111111111111111111"
		records := []RecordToBackfill{
			{
				BlockNum:    1,
				BlockPos:    0,
				TxHash:      common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
				FromAddress: &addr,
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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()

		expectedSender := common.HexToAddress(testAddress)
		// txReceipt To is not bridgeAddr, so must call debugTrace
		mockClientCallGetTransactionByHash(t, mockClient,
			common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			testAddress, "0x0000000000000000000000000000000000000000000")
		mockClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
			Run(func(result any, method string, args ...any) {
				arg, ok := result.(*Call)
				require.True(t, ok)
				arg.Input = BridgeAssetMethodID
				arg.From = expectedSender
				arg.To = common.HexToAddress("0x1234")
			}).Return(nil).Maybe()

		txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
		sender, _, _, err := backfiller.extractData(t.Context(), txHash,
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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()
		// txReceipt To is not bridgeAddr, so must call debugTrace
		mockClientCallGetTransactionByHash(t, mockClient,
			common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			testAddress, "0x0000000000000000000000000000000000000000000")
		mockClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).Return(errors.New("error")).Maybe()

		txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
		sender, _, _, err := backfiller.extractData(t.Context(), txHash, &agglayerbridge.AgglayerbridgeBridgeEvent{
			LeafType: bridgeLeafTypeAsset,
		})
		require.Error(t, err)
		require.Equal(t, common.Address{}, sender)
		require.Contains(t, err.Error(), "failed")
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

		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")))

		err = tx.Commit()
		require.NoError(t, err)

		_, err = database.Exec("UPDATE bridge SET txn_sender = '' WHERE block_num = 1 AND block_pos = 0")
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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

		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")))
		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 1,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567891")))

		err = tx.Commit()
		require.NoError(t, err)

		_, err = database.Exec("UPDATE bridge SET txn_sender = '' WHERE block_num = 1")
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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

	t.Run("syncFromInBridges=false: updates txn_sender and to_address but not from_address", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")

		err := migrations.RunMigrations(dbPath)
		require.NoError(t, err)

		database, err := db.NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer database.Close()

		ctx := t.Context()
		tx, err := db.NewTx(ctx, database)
		require.NoError(t, err)

		_, err = tx.Exec(`INSERT INTO block (num) VALUES (1)`)
		require.NoError(t, err)

		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")))
		require.NoError(t, tx.Commit())

		// Clear txn_sender, from_address and to_address to simulate unbackfilled record
		_, err = database.Exec("UPDATE bridge SET txn_sender = '', from_address = '', to_address = '' WHERE block_num = 1 AND block_pos = 0")
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), false, logger)
		require.NoError(t, err)
		defer backfiller.Close()

		toAddr := common.HexToAddress("0x3333333333333333333333333333333333333333")
		updates := []RecordUpdate{
			{
				BlockNum:  1,
				BlockPos:  0,
				TxnSender: common.HexToAddress(testAddress),
				FromAddr:  nil, // syncFromInBridges=false: no from_address available
				ToAddr:    toAddr,
			},
		}

		err = backfiller.bulkUpdate(ctx, "bridge", updates)
		require.NoError(t, err)

		var txnSender, fromAddress, toAddress string
		row := database.QueryRowContext(ctx, "SELECT txn_sender, from_address, to_address FROM bridge WHERE block_num = 1 AND block_pos = 0")
		require.NoError(t, row.Scan(&txnSender, &fromAddress, &toAddress))

		require.Equal(t, testAddress, txnSender)
		require.Empty(t, fromAddress, "from_address must remain empty (not updated)")
		require.Equal(t, toAddr.Hex(), toAddress)
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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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

		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")))
		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 1,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567891")))
		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 2,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567892")))

		err = tx.Commit()
		require.NoError(t, err)

		_, err = database.Exec("UPDATE bridge SET txn_sender = '' WHERE block_num = 1")
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()
		// txReceipt To is not bridgeAddr, so must call debugTrace
		mockClientCallGetTransactionByHash(t, mockClient,
			common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			testAddress, "0x0000000000000000000000000000000000000000000")
		mockClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
			Run(func(result any, method string, args ...any) {
				arg, ok := result.(*Call)
				require.True(t, ok)
				arg.Input = BridgeAssetMethodID
				arg.From = common.HexToAddress(testAddress)
				arg.To = common.HexToAddress("0x1234")
			}).Return(nil).Maybe()

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

		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")))
		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 1,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567891")))

		err = tx.Commit()
		require.NoError(t, err)

		_, err = database.Exec("UPDATE bridge SET txn_sender = '' WHERE block_num = 1")
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()
		// txReceipt To is not bridgeAddr, so must call debugTrace
		mockClientCallGetTransactionByHash(t, mockClient,
			common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			testAddress, "0x0000000000000000000000000000000000000000000")
		// Mock mixed results: first call succeeds, second fails

		mockClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
			Run(func(result any, method string, args ...any) {
				arg, ok := result.(*Call)
				require.True(t, ok)
				arg.Input = BridgeAssetMethodID
				arg.From = common.HexToAddress(testAddress)
				arg.To = common.HexToAddress("0x1234")
			}).Return(nil).Once()

		// Mock the second call to fail
		mockClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).Return(errors.New("error")).Once()

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

		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")))
		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 1,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567891")))

		err = tx.Commit()
		require.NoError(t, err)

		_, err = database.Exec("UPDATE bridge SET txn_sender = '' WHERE block_num = 1")
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()
		// txReceipt To is not bridgeAddr, so must call debugTrace
		mockClientCallGetTransactionByHash(t, mockClient,
			common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			testAddress, "0x0000000000000000000000000000000000000000000")
		// Mock all calls to fail
		mockClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).Return(errors.New("error")).Maybe()
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(errors.New("network error")).Maybe()

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

		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 0,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")))
		require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 1,
			"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567891")))

		err = tx.Commit()
		require.NoError(t, err)

		_, err = database.Exec("UPDATE bridge SET txn_sender = '' WHERE block_num = 1")
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Create a context that will be cancelled
		cancelCtx, cancel := context.WithCancel(ctx)
		// txReceipt To is not bridgeAddr, so must call debugTrace
		mockClientCallGetTransactionByHash(t, mockClient,
			common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			testAddress, "0x0000000000000000000000000000000000000000000")
		mockClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
			Run(func(result any, method string, args ...any) {
				time.Sleep(10 * time.Millisecond)
				arg, ok := result.(*Call)
				require.True(t, ok)
				arg.Input = BridgeAssetMethodID
			}).Return(nil).Maybe()

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
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
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
			require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, uint64(i),
				fmt.Sprintf("0x%064x", i))))

			records[i] = RecordToBackfill{
				BlockNum: 1,
				BlockPos: uint64(i),
				TxHash:   common.HexToHash(fmt.Sprintf("0x%064x", i)),
			}
		}

		err = tx.Commit()
		require.NoError(t, err)

		_, err = database.Exec("UPDATE bridge SET txn_sender = ''")
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()
		// txReceipt To is not bridgeAddr, so must call debugTrace
		mockClientCallGetTransactionByHash(t, mockClient,
			common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
			testAddress, "0x0000000000000000000000000000000000000000000")
		mockClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
			Run(func(result any, method string, args ...any) {
				arg, ok := result.(*Call)
				require.True(t, ok)
				arg.Input = BridgeAssetMethodID
				arg.From = common.HexToAddress(testAddress)
				arg.To = common.HexToAddress("0x1234")
			}).Return(nil).Maybe()

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
				require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, uint64(i),
					fmt.Sprintf("0x%064x", i))))
			}

			err = tx.Commit()
			require.NoError(t, err)

			_, err = database.Exec("UPDATE bridge SET txn_sender = ''")
			require.NoError(t, err)

			mockClient := mocks.NewEthClienter(t)
			logger := log.WithFields("module", "test")
			backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
			require.NoError(t, err)
			defer backfiller.Close()

			// Mock successful extractions for all records
			// leaf_type=1 = bridgeLeafTypeMessage, so RPCTransactionByHash is used
			mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
				tx, ok := args.Get(0).(*Transaction)
				if !ok {
					return
				}
				tx.FromRaw = testAddress
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
			require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, uint64(i),
				fmt.Sprintf("0x%064x", i))))
		}

		err = tx.Commit()
		require.NoError(t, err)

		_, err = database.Exec("UPDATE bridge SET txn_sender = ''")
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Mock successful extractions for all records
		// leaf_type=1 = bridgeLeafTypeMessage, so RPCTransactionByHash is used
		mockClient.On("Call", mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			tx, ok := args.Get(0).(*Transaction)
			if !ok {
				return
			}
			tx.FromRaw = testAddress
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
			require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, uint64(i),
				fmt.Sprintf("0x%064x", i))))
		}

		err = tx.Commit()
		require.NoError(t, err)

		_, err = database.Exec("UPDATE bridge SET txn_sender = ''")
		require.NoError(t, err)

		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		defer backfiller.Close()

		// Create a context that will be cancelled after some processing
		cancelCtx, cancel := context.WithCancel(ctx)

		// Mock calls with some delay
		// leaf_type=1 = bridgeLeafTypeMessage, so RPCTransactionByHash is used
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
			tx.FromRaw = testAddress
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

func TestBackfillTxnSender_getRecordsNeedingBackfill_Cases(t *testing.T) {
	filledAddr := common.HexToAddress("0xAAAABBBBCCCCDDDDEEEEFFFFAAAABBBBCCCCDDDD")

	// setup creates a fresh migrated DB and a BackfillTxnSender.
	setup := func(t *testing.T) (*BackfillTxnSender, *sql.DB, context.Context) {
		t.Helper()
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "test.db")
		require.NoError(t, migrations.RunMigrations(dbPath))
		database, err := db.NewSQLiteDB(dbPath)
		require.NoError(t, err)
		t.Cleanup(func() { database.Close() })
		mockClient := mocks.NewEthClienter(t)
		logger := log.WithFields("module", "test")
		backfiller, err := NewBackfillTxnSender(dbPath, mockClient, common.HexToAddress("0x1234"), true, logger)
		require.NoError(t, err)
		t.Cleanup(func() { backfiller.Close() })
		return backfiller, database, context.Background()
	}

	insertBlock := func(t *testing.T, sqlDB *sql.DB, num uint64) {
		t.Helper()
		_, err := sqlDB.Exec("INSERT INTO block (num) VALUES (?)", num)
		require.NoError(t, err)
	}

	// newBridge creates a Bridge with both txn_sender and from_address set to filledAddr,
	// so it does NOT need backfill by default. Tests can UPDATE fields afterward.
	newBridge := func(blockNum, blockPos uint64) *Bridge {
		return &Bridge{
			BlockNum:           blockNum,
			BlockPos:           blockPos,
			TxHash:             common.HexToHash(fmt.Sprintf("0x%064x", blockNum*1000+blockPos)),
			BlockTimestamp:     1234567890,
			LeafType:           1,
			OriginNetwork:      1,
			OriginAddress:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
			DestinationNetwork: 2,
			DestinationAddress: common.HexToAddress("0x0987654321098765432109876543210987654321"),
			Amount:             big.NewInt(1e18),
			DepositCount:       uint32(blockNum*10 + blockPos),
			FromAddress:        &filledAddr,
			TxnSender:          filledAddr,
		}
	}

	// insertBridge uses meddler.Insert to match the format used by the processor in production.
	insertBridge := func(t *testing.T, sqlDB *sql.DB, bridge *Bridge) {
		t.Helper()
		dbtx, err := sqlDB.Begin()
		require.NoError(t, err)
		require.NoError(t, meddler.Insert(dbtx, bridgeTableName, bridge))
		require.NoError(t, dbtx.Commit())
	}

	t.Run("empty database returns no records", func(t *testing.T) {
		backfiller, _, ctx := setup(t)
		records, err := backfiller.getRecordsNeedingBackfill(ctx, bridgeTableName, 10)
		require.NoError(t, err)
		require.Empty(t, records)
	})

	t.Run("null txn_sender triggers retrieval", func(t *testing.T) {
		backfiller, sqlDB, ctx := setup(t)
		insertBlock(t, sqlDB, 1)
		insertBridge(t, sqlDB, newBridge(1, 0))
		_, err := sqlDB.Exec("UPDATE bridge SET txn_sender = NULL WHERE block_num = 1 AND block_pos = 0")
		require.NoError(t, err)

		records, err := backfiller.getRecordsNeedingBackfill(ctx, bridgeTableName, 10)
		require.NoError(t, err)
		require.Len(t, records, 1)
		require.Equal(t, uint64(1), records[0].BlockNum)
		require.Nil(t, records[0].TxnSender)
	})

	t.Run("empty txn_sender triggers retrieval", func(t *testing.T) {
		backfiller, sqlDB, ctx := setup(t)
		insertBlock(t, sqlDB, 1)
		insertBridge(t, sqlDB, newBridge(1, 0))
		_, err := sqlDB.Exec("UPDATE bridge SET txn_sender = '' WHERE block_num = 1 AND block_pos = 0")
		require.NoError(t, err)

		records, err := backfiller.getRecordsNeedingBackfill(ctx, bridgeTableName, 10)
		require.NoError(t, err)
		require.Len(t, records, 1)
	})

	t.Run("null from_address triggers retrieval", func(t *testing.T) {
		backfiller, sqlDB, ctx := setup(t)
		insertBlock(t, sqlDB, 1)
		insertBridge(t, sqlDB, newBridge(1, 0))
		_, err := sqlDB.Exec("UPDATE bridge SET from_address = NULL WHERE block_num = 1 AND block_pos = 0")
		require.NoError(t, err)

		records, err := backfiller.getRecordsNeedingBackfill(ctx, bridgeTableName, 10)
		require.NoError(t, err)
		require.Len(t, records, 1)
		require.Nil(t, records[0].FromAddress)
	})

	t.Run("empty from_address triggers retrieval", func(t *testing.T) {
		backfiller, sqlDB, ctx := setup(t)
		insertBlock(t, sqlDB, 1)
		insertBridge(t, sqlDB, newBridge(1, 0))
		_, err := sqlDB.Exec("UPDATE bridge SET from_address = '' WHERE block_num = 1 AND block_pos = 0")
		require.NoError(t, err)

		records, err := backfiller.getRecordsNeedingBackfill(ctx, bridgeTableName, 10)
		require.NoError(t, err)
		require.Len(t, records, 1)
	})

	t.Run("record with both fields populated is excluded", func(t *testing.T) {
		// meddler.Insert stores non-zero address-codec fields as non-empty hex strings,
		// so a freshly inserted bridge with filledAddr for both fields is excluded.
		backfiller, sqlDB, ctx := setup(t)
		insertBlock(t, sqlDB, 1)
		insertBridge(t, sqlDB, newBridge(1, 0))

		records, err := backfiller.getRecordsNeedingBackfill(ctx, bridgeTableName, 10)
		require.NoError(t, err)
		require.Empty(t, records)
	})

	t.Run("limit is respected", func(t *testing.T) {
		backfiller, sqlDB, ctx := setup(t)
		for i := uint64(1); i <= 3; i++ {
			insertBlock(t, sqlDB, i)
			insertBridge(t, sqlDB, newBridge(i, 0))
		}
		_, err := sqlDB.Exec("UPDATE bridge SET txn_sender = NULL")
		require.NoError(t, err)

		records, err := backfiller.getRecordsNeedingBackfill(ctx, bridgeTableName, 2)
		require.NoError(t, err)
		require.Len(t, records, 2)
	})

	t.Run("context cancelled returns error", func(t *testing.T) {
		backfiller, sqlDB, _ := setup(t)
		insertBlock(t, sqlDB, 1)
		insertBridge(t, sqlDB, newBridge(1, 0))
		_, err := sqlDB.Exec("UPDATE bridge SET txn_sender = NULL WHERE block_num = 1 AND block_pos = 0")
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = backfiller.getRecordsNeedingBackfill(ctx, bridgeTableName, 10)
		require.Error(t, err)
	})

	t.Run("mixed records - only those needing backfill returned", func(t *testing.T) {
		backfiller, sqlDB, ctx := setup(t)
		for i := uint64(1); i <= 3; i++ {
			insertBlock(t, sqlDB, i)
			insertBridge(t, sqlDB, newBridge(i, 0))
		}
		// Block 1: needs backfill (empty txn_sender)
		_, err := sqlDB.Exec("UPDATE bridge SET txn_sender = '' WHERE block_num = 1")
		require.NoError(t, err)
		// Block 2: needs backfill (NULL from_address)
		_, err = sqlDB.Exec("UPDATE bridge SET from_address = NULL WHERE block_num = 2")
		require.NoError(t, err)
		// Block 3: both fields populated → excluded

		records, err := backfiller.getRecordsNeedingBackfill(ctx, bridgeTableName, 10)
		require.NoError(t, err)
		require.Len(t, records, 2)

		blockNums := []uint64{records[0].BlockNum, records[1].BlockNum}
		require.ElementsMatch(t, []uint64{1, 2}, blockNums)
	})

	t.Run("database error returns error", func(t *testing.T) {
		backfiller, _, _ := setup(t)
		backfiller.db.Close()

		ctx := context.Background()
		_, err := backfiller.getRecordsNeedingBackfill(ctx, bridgeTableName, 10)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to query records needing backfill")
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

	require.NoError(t, meddler.Insert(tx, bridgeTableName, newTestBridge(1, 0,
		"0x0000000000000000000000000000000000000000000000000000000000000000")))

	err = tx.Commit()
	require.NoError(t, err)

	_, err = database.Exec("UPDATE bridge SET txn_sender = '' WHERE block_num = 1 AND block_pos = 0")
	require.NoError(t, err)

	logger := log.WithFields("module", "test")
	// Create real client
	client, err := etherman.DialWithRetry(t.Context(), logger, &ethermanconfig.RPCClientConfig{
		URL: rpcURL,
	})
	require.NoError(t, err)

	// Create backfill instance

	backfiller, err := NewBackfillTxnSender(dbPath, client, common.HexToAddress("0x1234"), true, logger)
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
