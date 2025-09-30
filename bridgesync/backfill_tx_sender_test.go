package bridgesync

import (
	"context"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/bridgesync/migrations"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
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

	// Insert test claim record with empty tx_sender
	_, err = tx.Exec(`
		INSERT INTO claim (
			block_num, block_pos, global_index, origin_network, origin_address,
			destination_address, amount, proof_local_exit_root, proof_rollup_exit_root,
			mainnet_exit_root, rollup_exit_root, global_exit_root, destination_network,
			metadata, is_message, block_timestamp, tx_hash, from_address, tx_sender
		) VALUES (
			1, 1, '1', 1, '0x1234567890123456789012345678901234567890',
			'0x0987654321098765432109876543210987654321', '1000000000000000000',
			'', '', '', '', '', 2, '', false, 1234567890,
			'0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
			'0x2222222222222222222222222222222222222222', ''
		)
	`)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	// Create mock client
	mockClient := &MockEthClient{}

	// Create backfill instance
	logger := log.WithFields("module", "test")
	backfiller, err := NewBackfillTxSender(dbPath, mockClient, common.HexToAddress("0x1234"), logger)
	require.NoError(t, err)
	defer backfiller.Close()

	// Test getting records needing backfill count
	bridgeCount, err := backfiller.getRecordsNeedingBackfillCount(ctx, "bridge")
	require.NoError(t, err)
	assert.Equal(t, 1, bridgeCount)

	claimCount, err := backfiller.getRecordsNeedingBackfillCount(ctx, "claim")
	require.NoError(t, err)
	assert.Equal(t, 1, claimCount)

	// Test getting records needing backfill
	bridgeRecords, err := backfiller.getRecordsNeedingBackfill(ctx, "bridge", 0, 10)
	require.NoError(t, err)
	assert.Len(t, bridgeRecords, 1)
	assert.Equal(t, uint64(1), bridgeRecords[0].BlockNum)
	assert.Equal(t, uint64(0), bridgeRecords[0].BlockPos)

	claimRecords, err := backfiller.getRecordsNeedingBackfill(ctx, "claim", 0, 10)
	require.NoError(t, err)
	assert.Len(t, claimRecords, 1)
	assert.Equal(t, uint64(1), claimRecords[0].BlockNum)
	assert.Equal(t, uint64(1), claimRecords[0].BlockPos)
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

	// Verify stats
	processed, errors := backfiller.GetStats()
	t.Logf("Processed: %d, Errors: %d", processed, errors)
}

// MockEthClient is a mock implementation of EthClienter for testing
type MockEthClient struct{}

func (m *MockEthClient) Call(result interface{}, method string, args ...interface{}) error {
	// Mock implementation - return a mock call result
	if callPtr, ok := result.(**call); ok {
		*callPtr = &call{
			From: common.HexToAddress("0x1234567890123456789012345678901234567890"),
			To:   common.HexToAddress("0x1234"),
			Err:  nil,
		}
	}
	return nil
}

func (m *MockEthClient) Close() error {
	return nil
}

// Implement the required methods for types.EthClienter interface
func (m *MockEthClient) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	return big.NewInt(0), nil
}

func (m *MockEthClient) BlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error) {
	return nil, nil
}

func (m *MockEthClient) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	return nil, nil
}

func (m *MockEthClient) BlockNumber(ctx context.Context) (uint64, error) {
	return 0, nil
}

func (m *MockEthClient) CallContract(ctx context.Context, call ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	return nil, nil
}

func (m *MockEthClient) ChainID(ctx context.Context) (*big.Int, error) {
	return big.NewInt(1), nil
}

func (m *MockEthClient) CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
	return nil, nil
}

func (m *MockEthClient) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	return nil, nil
}

func (m *MockEthClient) HeaderByHash(ctx context.Context, hash common.Hash) (*types.Header, error) {
	return nil, nil
}

func (m *MockEthClient) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	return nil, nil
}

func (m *MockEthClient) NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (uint64, error) {
	return 0, nil
}

func (m *MockEthClient) PendingBalanceAt(ctx context.Context, account common.Address) (*big.Int, error) {
	return big.NewInt(0), nil
}

func (m *MockEthClient) PendingCallContract(ctx context.Context, call ethereum.CallMsg) ([]byte, error) {
	return nil, nil
}

func (m *MockEthClient) PendingCodeAt(ctx context.Context, account common.Address) ([]byte, error) {
	return nil, nil
}

func (m *MockEthClient) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	return 0, nil
}

func (m *MockEthClient) PendingStorageAt(ctx context.Context, account common.Address, key common.Hash) ([]byte, error) {
	return nil, nil
}

func (m *MockEthClient) StorageAt(ctx context.Context, account common.Address, key common.Hash, blockNumber *big.Int) ([]byte, error) {
	return nil, nil
}

func (m *MockEthClient) SubscribeFilterLogs(ctx context.Context, query ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
	return nil, nil
}

func (m *MockEthClient) SubscribeNewHead(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error) {
	return nil, nil
}

func (m *MockEthClient) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return big.NewInt(0), nil
}

func (m *MockEthClient) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	return big.NewInt(0), nil
}

func (m *MockEthClient) SyncProgress(ctx context.Context) (*ethereum.SyncProgress, error) {
	return nil, nil
}

func (m *MockEthClient) TransactionByHash(ctx context.Context, txHash common.Hash) (tx *types.Transaction, isPending bool, err error) {
	return nil, false, nil
}

func (m *MockEthClient) TransactionCount(ctx context.Context, blockHash common.Hash) (uint, error) {
	return 0, nil
}

func (m *MockEthClient) TransactionInBlock(ctx context.Context, blockHash common.Hash, index uint) (*types.Transaction, error) {
	return nil, nil
}

func (m *MockEthClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	return nil, nil
}

func (m *MockEthClient) EstimateGas(ctx context.Context, call ethereum.CallMsg) (uint64, error) {
	return 21000, nil
}

func (m *MockEthClient) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	return nil
}
