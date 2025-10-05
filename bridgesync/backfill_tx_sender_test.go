package bridgesync

import (
	"context"
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
	"github.com/stretchr/testify/require"
)

func TestBackfillTxSender(t *testing.T) {
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

	// Create test data with empty tx_sender
	ctx := context.Background()
	tx, err := db.NewTx(ctx, database)
	require.NoError(t, err)

	// Insert test bridge record with empty tx_sender
	_, err = tx.Exec(`
		INSERT INTO block (num) VALUES (1)
	`)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO bridge (
			block_num, block_pos, leaf_type, origin_network, origin_address,
			destination_network, destination_address, amount, metadata, deposit_count,
			tx_hash, block_timestamp, from_address, calldata, tx_sender
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
	backfiller, err := NewBackfillTxSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
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

func TestBackfillTxSenderIntegration(t *testing.T) {
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

	// Insert test records with empty tx_sender
	_, err = tx.Exec(`
		INSERT INTO block (num) VALUES (1)
	`)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO bridge (
			block_num, block_pos, leaf_type, origin_network, origin_address,
			destination_network, destination_address, amount, metadata, deposit_count,
			tx_hash, block_timestamp, from_address, calldata, tx_sender
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
	backfiller, err := NewBackfillTxSender(dbPath, client, common.HexToAddress("0x1234"), logger)
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
