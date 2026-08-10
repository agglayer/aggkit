package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"math/big"
	"os"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	migrate "github.com/rubenv/sql-migrate"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

func TestRunMigrationsExploratory(t *testing.T) {
	t.Skip("This test is for exploratory testing of migrations during development. " +
		"It is not meant to be run as part of automated tests.")
	dbPath := "/tmp/bridgel1sync.sqlite"
	err := RunMigrations(dbPath)
	require.NoError(t, err)
}

func TestMigration0001(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgesyncTest001.sqlite")

	err := RunMigrations(dbPath)
	require.NoError(t, err)
	db, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO block (num, hash) VALUES (1, '0xA1FA');

		INSERT INTO bridge (
			block_num,
			block_pos,
			leaf_type,
			origin_network,
			origin_address,
			destination_network,
			destination_address,
			amount,
			metadata,
			deposit_count
		) VALUES (1, 0, 0, 0, '0x0000', 0, '0x0000', 0, NULL, 0);
	`)
	require.NoError(t, err)
	err = tx.Commit()
	require.NoError(t, err)
}

func TestMigration0002(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgesyncTest0002.sqlite")

	err := RunMigrations(dbPath)
	require.NoError(t, err)
	db, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO block (num, hash) VALUES (1, '0xBEEF');;

		INSERT INTO token_mapping (
			block_num,
			block_pos,
			block_timestamp,
			tx_hash,
			origin_network,
			origin_token_address,
			wrapped_token_address,
			metadata,
			is_not_mintable,
			token_type
		) VALUES (1, 0, 1739270804, '0xabcd', 2, '0x3', '0x5', NULL, FALSE, 1);

		INSERT INTO bridge (
			block_num,
			block_pos,
			leaf_type,
			origin_network,
			origin_address,
			destination_network,
			destination_address,
			amount,
			metadata,
			deposit_count,
			block_timestamp,
			tx_hash,
			from_address
		) VALUES (1, 0, 0, 0, '0x3', 0, '0x0000', 0, NULL, 0, 1739270804, '0xabcd', '0x123');

	`)
	require.NoError(t, err)
	err = tx.Commit()
	require.NoError(t, err)

	var tokenMapping struct {
		BlockNum            uint64         `meddler:"block_num"`
		BlockPos            uint64         `meddler:"block_pos"`
		BlockTimestamp      uint64         `meddler:"block_timestamp"`
		TxHash              common.Hash    `meddler:"tx_hash,hash"`
		OriginNetwork       uint32         `meddler:"origin_network"`
		OriginTokenAddress  common.Address `meddler:"origin_token_address,address"`
		WrappedTokenAddress common.Address `meddler:"wrapped_token_address,address"`
		Metadata            []byte         `meddler:"metadata"`
		IsNotMintable       bool           `meddler:"is_not_mintable"`
		Type                uint8          `meddler:"token_type"`
	}

	err = meddler.QueryRow(db, &tokenMapping,
		`SELECT * FROM token_mapping`)
	require.NoError(t, err)
	require.NotNil(t, tokenMapping)
	require.Equal(t, uint64(1), tokenMapping.BlockNum)
	require.Equal(t, uint64(0), tokenMapping.BlockPos)
	require.Equal(t, uint64(1739270804), tokenMapping.BlockTimestamp)
	require.Equal(t, uint32(2), tokenMapping.OriginNetwork)
	require.Equal(t, common.HexToAddress("0x3"), tokenMapping.OriginTokenAddress)
	require.Equal(t, common.HexToAddress("0x5"), tokenMapping.WrappedTokenAddress)
	require.Equal(t, false, tokenMapping.IsNotMintable)
	require.Equal(t, uint8(1), tokenMapping.Type)

	var bridge struct {
		BlockNum           uint64   `meddler:"block_num"`
		BlockPos           uint64   `meddler:"block_pos"`
		LeafType           uint8    `meddler:"leaf_type"`
		OriginNetwork      uint32   `meddler:"origin_network"`
		OriginAddress      string   `meddler:"origin_address"`
		DestinationNetwork uint32   `meddler:"destination_network"`
		DestinationAddress string   `meddler:"destination_address"`
		Amount             *big.Int `meddler:"amount,bigint"`
		Metadata           []byte   `meddler:"metadata"`
		DepositCount       uint32   `meddler:"deposit_count"`
		BlockTimestamp     uint64   `meddler:"block_timestamp"`
		TxHash             string   `meddler:"tx_hash"`
		FromAddress        *string  `meddler:"from_address"`
		TxnSender          *string  `meddler:"txn_sender"`
	}

	err = meddler.QueryRow(db, &bridge,
		`SELECT * FROM bridge`)
	require.NoError(t, err)
	require.NotNil(t, bridge)
	require.Equal(t, uint64(1739270804), bridge.BlockTimestamp)
}

func TestMigrations0003(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgesyncTest0003.sqlite")

	err := RunMigrations(dbPath)
	require.NoError(t, err)
	db, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	var legacyTokenMigration struct {
		BlockNum            uint64         `meddler:"block_num"`
		BlockPos            uint64         `meddler:"block_pos"`
		BlockTimestamp      uint64         `meddler:"block_timestamp"`
		TxHash              common.Hash    `meddler:"tx_hash,hash"`
		Sender              common.Address `meddler:"sender,address"`
		LegacyTokenAddress  common.Address `meddler:"legacy_token_address,address"`
		UpdatedTokenAddress common.Address `meddler:"updated_token_address,address"`
		Amount              *big.Int       `meddler:"amount,bigint"`
	}

	_, err = tx.Exec(`
		INSERT INTO block (num, hash) VALUES (1, '0xABBA');

		INSERT INTO legacy_token_migration (
			block_num,
			block_pos,
			block_timestamp,
			tx_hash,
			sender,
			legacy_token_address,
			updated_token_address,
			amount
		) VALUES (1, 10, 1739270804, '0xabcd', '0x3', '0x5', '0x7', 1000);
	`)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	err = meddler.QueryRow(db, &legacyTokenMigration,
		`SELECT * FROM legacy_token_migration`)
	require.NoError(t, err)
	require.NotNil(t, legacyTokenMigration)
	require.Equal(t, uint64(1), legacyTokenMigration.BlockNum)
	require.Equal(t, uint64(10), legacyTokenMigration.BlockPos)
	require.Equal(t, uint64(1739270804), legacyTokenMigration.BlockTimestamp)
	require.Equal(t, common.HexToAddress("0x3"), legacyTokenMigration.Sender)
	require.Equal(t, common.HexToAddress("0x5"), legacyTokenMigration.LegacyTokenAddress)
	require.Equal(t, common.HexToAddress("0x7"), legacyTokenMigration.UpdatedTokenAddress)
	require.Equal(t, big.NewInt(1000), legacyTokenMigration.Amount)
}

func TestMigration0004(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgesyncTest0004.sqlite")

	// Create database and run migrations up to 0003 only
	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	// Define migrations up to bridgesync0003
	migrations := GetUpTo("bridgesync0003")

	// Run migrations up to bridgesync0003 (3 migrations)
	err = db.RunMigrationsDBExtended(log.GetDefaultLogger(),
		database, migrations, nil, migrate.Up, 3)
	require.NoError(t, err)

	ctx := context.Background()
	tx, err := database.BeginTx(ctx, nil)
	require.NoError(t, err)

	// Insert test data with is_native_token column (before migration 0004)
	_, err = tx.Exec(`
		INSERT INTO block (num, hash) VALUES (1, '0xCAFE');

		INSERT INTO bridge (
			block_num,
			block_pos,
			leaf_type,
			origin_network,
			origin_address,
			destination_network,
			destination_address,
			amount,
			metadata,
			deposit_count,
			block_timestamp,
			tx_hash,
			from_address,
			is_native_token
		) VALUES (1, 0, 0, 0, '0x1234', 0, '0x5678', 1000, NULL, 0, 1739270804, '0xabcd', '0x9abc', true);

		INSERT INTO bridge (
			block_num,
			block_pos,
			leaf_type,
			origin_network,
			origin_address,
			destination_network,
			destination_address,
			amount,
			metadata,
			deposit_count,
			block_timestamp,
			tx_hash,
			from_address,
			is_native_token
		) VALUES (1, 1, 0, 0, '0x2345', 0, '0x6789', 2000, NULL, 0, 1739270804, '0xbcde', '0xabcd', false);
	`)
	require.NoError(t, err)
	err = tx.Commit()
	require.NoError(t, err)

	// Verify that is_native_token column exists and data is accessible before migration
	var bridgeWithNativeToken struct {
		BlockNum           uint64   `meddler:"block_num"`
		BlockPos           uint64   `meddler:"block_pos"`
		LeafType           uint8    `meddler:"leaf_type"`
		OriginNetwork      uint32   `meddler:"origin_network"`
		OriginAddress      string   `meddler:"origin_address"`
		DestinationNetwork uint32   `meddler:"destination_network"`
		DestinationAddress string   `meddler:"destination_address"`
		Amount             *big.Int `meddler:"amount,bigint"`
		Metadata           []byte   `meddler:"metadata"`
		DepositCount       uint32   `meddler:"deposit_count"`
		BlockTimestamp     uint64   `meddler:"block_timestamp"`
		TxHash             string   `meddler:"tx_hash"`
		FromAddress        string   `meddler:"from_address"`
		IsNativeToken      bool     `meddler:"is_native_token"`
	}

	// Test that we can query the is_native_token column before migration
	err = meddler.QueryRow(database, &bridgeWithNativeToken,
		`SELECT * FROM bridge WHERE block_pos = 0`)
	require.NoError(t, err)
	require.NotNil(t, bridgeWithNativeToken)
	require.Equal(t, true, bridgeWithNativeToken.IsNativeToken)
	require.Equal(t, "0x1234", bridgeWithNativeToken.OriginAddress)

	// Test the second record with is_native_token = false
	var bridgeWithoutNativeToken struct {
		BlockNum           uint64   `meddler:"block_num"`
		BlockPos           uint64   `meddler:"block_pos"`
		LeafType           uint8    `meddler:"leaf_type"`
		OriginNetwork      uint32   `meddler:"origin_network"`
		OriginAddress      string   `meddler:"origin_address"`
		DestinationNetwork uint32   `meddler:"destination_network"`
		DestinationAddress string   `meddler:"destination_address"`
		Amount             *big.Int `meddler:"amount,bigint"`
		Metadata           []byte   `meddler:"metadata"`
		DepositCount       uint32   `meddler:"deposit_count"`
		BlockTimestamp     uint64   `meddler:"block_timestamp"`
		TxHash             string   `meddler:"tx_hash"`
		FromAddress        string   `meddler:"from_address"`
		IsNativeToken      bool     `meddler:"is_native_token"`
	}

	err = meddler.QueryRow(database, &bridgeWithoutNativeToken,
		`SELECT * FROM bridge WHERE block_pos = 1`)
	require.NoError(t, err)
	require.NotNil(t, bridgeWithoutNativeToken)
	require.Equal(t, false, bridgeWithoutNativeToken.IsNativeToken)
	require.Equal(t, "0x2345", bridgeWithoutNativeToken.OriginAddress)

	// Now test migration 0004 UP (DROP COLUMN) by manually executing the SQL
	// This simulates what the migration system would do
	_, err = database.Exec(`ALTER TABLE bridge DROP COLUMN is_native_token;`)
	require.NoError(t, err)

	// Verify that is_native_token column no longer exists
	var bridgeAfterMigration struct {
		BlockNum           uint64   `meddler:"block_num"`
		BlockPos           uint64   `meddler:"block_pos"`
		LeafType           uint8    `meddler:"leaf_type"`
		OriginNetwork      uint32   `meddler:"origin_network"`
		OriginAddress      string   `meddler:"origin_address"`
		DestinationNetwork uint32   `meddler:"destination_network"`
		DestinationAddress string   `meddler:"destination_address"`
		Amount             *big.Int `meddler:"amount,bigint"`
		Metadata           []byte   `meddler:"metadata"`
		DepositCount       uint32   `meddler:"deposit_count"`
		BlockTimestamp     uint64   `meddler:"block_timestamp"`
		TxHash             string   `meddler:"tx_hash"`
		FromAddress        string   `meddler:"from_address"`
		// Note: IsNativeToken field removed to test that column is gone
	}

	// This should succeed because we're not selecting the is_native_token column
	err = meddler.QueryRow(database, &bridgeAfterMigration,
		`SELECT block_num, block_pos, leaf_type, origin_network, origin_address,
		 destination_network, destination_address, amount, metadata, deposit_count,
		 block_timestamp, tx_hash, from_address FROM bridge WHERE block_pos = 0`)
	require.NoError(t, err)
	require.NotNil(t, bridgeAfterMigration)
	require.Equal(t, "0x1234", bridgeAfterMigration.OriginAddress)

	// Test that trying to select the is_native_token column fails
	_, err = database.Exec(`SELECT is_native_token FROM bridge LIMIT 1;`)
	require.Error(t, err) // Should fail because column doesn't exist

	// Test migration 0004 DOWN (ADD COLUMN) by manually executing the SQL
	_, err = database.Exec(`ALTER TABLE bridge ADD COLUMN is_native_token BOOLEAN;`)
	require.NoError(t, err)

	// Verify that is_native_token column exists again
	var bridgeAfterRollback struct {
		BlockNum           uint64   `meddler:"block_num"`
		BlockPos           uint64   `meddler:"block_pos"`
		LeafType           uint8    `meddler:"leaf_type"`
		OriginNetwork      uint32   `meddler:"origin_network"`
		OriginAddress      string   `meddler:"origin_address"`
		DestinationNetwork uint32   `meddler:"destination_network"`
		DestinationAddress string   `meddler:"destination_address"`
		Amount             *big.Int `meddler:"amount,bigint"`
		Metadata           []byte   `meddler:"metadata"`
		DepositCount       uint32   `meddler:"deposit_count"`
		BlockTimestamp     uint64   `meddler:"block_timestamp"`
		TxHash             string   `meddler:"tx_hash"`
		FromAddress        string   `meddler:"from_address"`
		IsNativeToken      *bool    `meddler:"is_native_token"` // Nullable since existing rows will have NULL
	}

	// This should succeed and return NULL for is_native_token since we dropped and re-added the column
	err = meddler.QueryRow(database, &bridgeAfterRollback,
		`SELECT * FROM bridge WHERE block_pos = 0`)
	require.NoError(t, err)
	require.NotNil(t, bridgeAfterRollback)
	require.Equal(t, "0x1234", bridgeAfterRollback.OriginAddress)
	require.Nil(t, bridgeAfterRollback.IsNativeToken) // Should be NULL after rollback
}

func TestMigration0006(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgesyncTest0006.sqlite")

	// Create database and run migrations up to 0005 only
	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	// Define migrations up to bridgesync0005
	migrations := GetUpTo("bridgesync0005")

	// Run migrations up to 0005 (5 migrations)
	err = db.RunMigrationsDBExtended(log.GetDefaultLogger(),
		database, migrations, nil, migrate.Up, 5)
	require.NoError(t, err)

	ctx := context.Background()
	tx, err := database.BeginTx(ctx, nil)
	require.NoError(t, err)

	// Insert test data without txn_sender column (before migration 0006)
	_, err = tx.Exec(`
		INSERT INTO block (num, hash) VALUES (1, '0xDEAD');

		INSERT INTO bridge (
			block_num,
			block_pos,
			leaf_type,
			origin_network,
			origin_address,
			destination_network,
			destination_address,
			amount,
			metadata,
			deposit_count,
			block_timestamp,
			tx_hash,
			from_address
		) VALUES (1, 0, 0, 0, '0x1111', 0, '0x2222', 1000, NULL, 0, 1739270804, '0xabcd', '0x3333');

		INSERT INTO bridge (
			block_num,
			block_pos,
			leaf_type,
			origin_network,
			origin_address,
			destination_network,
			destination_address,
			amount,
			metadata,
			deposit_count,
			block_timestamp,
			tx_hash,
			from_address
		) VALUES (1, 1, 0, 0, '0x4444', 0, '0x5555', 2000, NULL, 0, 1739270804, '0xbcde', '0x6666');
	`)
	require.NoError(t, err)
	err = tx.Commit()
	require.NoError(t, err)

	// Verify that txn_sender column doesn't exist before migration
	_, err = database.Exec(`SELECT txn_sender FROM bridge LIMIT 1;`)
	require.Error(t, err) // Should fail because column doesn't exist

	// Now test migration 0006 UP (ADD COLUMN) by manually executing the SQL
	// This simulates what the migration system would do
	_, err = database.Exec(`ALTER TABLE bridge ADD COLUMN txn_sender VARCHAR DEFAULT '';`)
	require.NoError(t, err)

	// Verify that txn_sender column exists and has default value
	var bridgeWithTxnSender struct {
		BlockNum           uint64   `meddler:"block_num"`
		BlockPos           uint64   `meddler:"block_pos"`
		LeafType           uint8    `meddler:"leaf_type"`
		OriginNetwork      uint32   `meddler:"origin_network"`
		OriginAddress      string   `meddler:"origin_address"`
		DestinationNetwork uint32   `meddler:"destination_network"`
		DestinationAddress string   `meddler:"destination_address"`
		Amount             *big.Int `meddler:"amount,bigint"`
		Metadata           []byte   `meddler:"metadata"`
		DepositCount       uint32   `meddler:"deposit_count"`
		BlockTimestamp     uint64   `meddler:"block_timestamp"`
		TxHash             string   `meddler:"tx_hash"`
		FromAddress        *string  `meddler:"from_address"`
		TxnSender          *string  `meddler:"txn_sender"`
	}

	// Test that we can query the txn_sender column after migration
	err = meddler.QueryRow(database, &bridgeWithTxnSender,
		`SELECT * FROM bridge WHERE block_pos = 0`)
	require.NoError(t, err)
	require.NotNil(t, bridgeWithTxnSender)
	require.NotNil(t, bridgeWithTxnSender.TxnSender)
	require.Equal(t, "", *bridgeWithTxnSender.TxnSender) // Should have default empty string value
	require.Equal(t, "0x1111", bridgeWithTxnSender.OriginAddress)

	// Test the second record
	var bridgeWithTxnSender2 struct {
		BlockNum           uint64   `meddler:"block_num"`
		BlockPos           uint64   `meddler:"block_pos"`
		LeafType           uint8    `meddler:"leaf_type"`
		OriginNetwork      uint32   `meddler:"origin_network"`
		OriginAddress      string   `meddler:"origin_address"`
		DestinationNetwork uint32   `meddler:"destination_network"`
		DestinationAddress string   `meddler:"destination_address"`
		Amount             *big.Int `meddler:"amount,bigint"`
		Metadata           []byte   `meddler:"metadata"`
		DepositCount       uint32   `meddler:"deposit_count"`
		BlockTimestamp     uint64   `meddler:"block_timestamp"`
		TxHash             string   `meddler:"tx_hash"`
		FromAddress        *string  `meddler:"from_address"`
		TxnSender          *string  `meddler:"txn_sender"`
	}

	err = meddler.QueryRow(database, &bridgeWithTxnSender2,
		`SELECT * FROM bridge WHERE block_pos = 1`)
	require.NoError(t, err)
	require.NotNil(t, bridgeWithTxnSender2)
	require.NotNil(t, bridgeWithTxnSender2.TxnSender)
	require.Equal(t, "", *bridgeWithTxnSender2.TxnSender) // Should have default empty string value
	require.Equal(t, "0x4444", bridgeWithTxnSender2.OriginAddress)

	// Test that we can insert new records with txn_sender values
	_, err = database.Exec(`
		INSERT INTO bridge (
			block_num,
			block_pos,
			leaf_type,
			origin_network,
			origin_address,
			destination_network,
			destination_address,
			amount,
			metadata,
			deposit_count,
			block_timestamp,
			tx_hash,
			from_address,
			txn_sender
		) VALUES (1, 2, 0, 0, '0x7777', 0, '0x8888', 3000, NULL, 0, 1739270804, '0xcdef', '0x9999', '0xAAAA');
	`)
	require.NoError(t, err)

	// Verify the new record with txn_sender value
	var bridgeWithCustomTxnSender struct {
		BlockNum           uint64   `meddler:"block_num"`
		BlockPos           uint64   `meddler:"block_pos"`
		LeafType           uint8    `meddler:"leaf_type"`
		OriginNetwork      uint32   `meddler:"origin_network"`
		OriginAddress      string   `meddler:"origin_address"`
		DestinationNetwork uint32   `meddler:"destination_network"`
		DestinationAddress string   `meddler:"destination_address"`
		Amount             *big.Int `meddler:"amount,bigint"`
		Metadata           []byte   `meddler:"metadata"`
		DepositCount       uint32   `meddler:"deposit_count"`
		BlockTimestamp     uint64   `meddler:"block_timestamp"`
		TxHash             string   `meddler:"tx_hash"`
		FromAddress        *string  `meddler:"from_address"`
		TxnSender          *string  `meddler:"txn_sender"`
	}

	err = meddler.QueryRow(database, &bridgeWithCustomTxnSender,
		`SELECT * FROM bridge WHERE block_pos = 2`)
	require.NoError(t, err)
	require.NotNil(t, bridgeWithCustomTxnSender)
	require.NotNil(t, bridgeWithCustomTxnSender.TxnSender)
	require.Equal(t, "0xAAAA", *bridgeWithCustomTxnSender.TxnSender)
	require.Equal(t, "0x7777", bridgeWithCustomTxnSender.OriginAddress)

	// Test migration 0006 DOWN (DROP COLUMN) by manually executing the SQL
	_, err = database.Exec(`ALTER TABLE bridge DROP COLUMN txn_sender;`)
	require.NoError(t, err)

	// Verify that txn_sender column no longer exists
	_, err = database.Exec(`SELECT txn_sender FROM bridge LIMIT 1;`)
	require.Error(t, err) // Should fail because column doesn't exist

	// Test that we can still query other columns
	var bridgeAfterRollback struct {
		BlockNum           uint64   `meddler:"block_num"`
		BlockPos           uint64   `meddler:"block_pos"`
		LeafType           uint8    `meddler:"leaf_type"`
		OriginNetwork      uint32   `meddler:"origin_network"`
		OriginAddress      string   `meddler:"origin_address"`
		DestinationNetwork uint32   `meddler:"destination_network"`
		DestinationAddress string   `meddler:"destination_address"`
		Amount             *big.Int `meddler:"amount,bigint"`
		Metadata           []byte   `meddler:"metadata"`
		DepositCount       uint32   `meddler:"deposit_count"`
		BlockTimestamp     uint64   `meddler:"block_timestamp"`
		TxHash             string   `meddler:"tx_hash"`
		FromAddress        string   `meddler:"from_address"`
		// Note: TxnSender field removed to test that column is gone
	}

	// This should succeed because we're not selecting the txn_sender column
	err = meddler.QueryRow(database, &bridgeAfterRollback,
		`SELECT block_num, block_pos, leaf_type, origin_network, origin_address,
		 destination_network, destination_address, amount, metadata, deposit_count,
		 block_timestamp, tx_hash, from_address FROM bridge WHERE block_pos = 0`)
	require.NoError(t, err)
	require.NotNil(t, bridgeAfterRollback)
	require.Equal(t, "0x1111", bridgeAfterRollback.OriginAddress)
}

// This test check that bridge.to_address have the default ” and also
// that the previous data is not lost after migration 0013
func TestMigration0013(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgesyncTest0013.sqlite")

	// Create database and run migrations up to 0012 only
	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	// Define migrations up to bridgesync0012
	migrations := GetUpTo("bridgesync0012")

	// Run migrations up to 0012 (12 migrations)
	err = db.RunMigrationsDBExtended(log.GetDefaultLogger(),
		database, migrations, nil, migrate.Up, 12)
	require.NoError(t, err)

	ctx := context.Background()
	tx, err := database.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.Exec(`
		INSERT INTO block (num, hash) VALUES (1, '0xDEAD');

		INSERT INTO bridge (
			block_num,
			block_pos,
			leaf_type,
			origin_network,
			origin_address,
			destination_network,
			destination_address,
			amount,
			metadata,
			deposit_count,
			block_timestamp,
			tx_hash,
			from_address,
			to_address
		) VALUES (1, 0, 0, 0, '0x1111', 0, '0x2222', 1000, NULL, 0, 1739270804, '0xabcd', '0x3333', '0x42');

		INSERT INTO bridge (
			block_num,
			block_pos,
			leaf_type,
			origin_network,
			origin_address,
			destination_network,
			destination_address,
			amount,
			metadata,
			deposit_count,
			block_timestamp,
			tx_hash,
			from_address,
			to_address
		) VALUES (1, 2, 0, 0, '0x1111', 0, '0x2222', 1000, NULL, 0, 1739270804, '0xabcd', '0x3333', NULL);
	`)
	require.NoError(t, err)

	// Confirm to_address is actually NULL before migration
	var nullCheck *string
	require.NoError(t, tx.QueryRow(`SELECT to_address FROM bridge WHERE block_num=1 AND block_pos=2`).Scan(&nullCheck))
	require.Nil(t, nullCheck, "to_address should be NULL before migration 0013")

	err = tx.Commit()
	require.NoError(t, err)
	migrations = GetUpTo("bridgesync0013")
	// Run migrations up to 0013 (13 migrations)
	err = db.RunMigrationsDBExtended(log.GetDefaultLogger(),
		database, migrations, nil, migrate.Up, 13)
	require.NoError(t, err)
	tx, err = database.BeginTx(ctx, nil)
	require.NoError(t, err)
	// Insert bridge with no to_address so must use the default ''
	_, err = tx.Exec(`
		INSERT INTO bridge (
			block_num,
			block_pos,
			leaf_type,
			origin_network,
			origin_address,
			destination_network,
			destination_address,
			amount,
			metadata,
			deposit_count,
			block_timestamp,
			tx_hash,
			from_address
		) VALUES (1, 1, 0, 0, '0x1111', 0, '0x2222', 1000, NULL, 0, 1739270804, '0xabcd', '0x4444');
	`)
	require.NoError(t, err)
	// First insert preserves to_address = '0x42'
	row := tx.QueryRow(`SELECT to_address FROM bridge WHERE block_num=1 AND block_pos=0`)
	var toAddress string
	err = row.Scan(&toAddress)
	require.NoError(t, err)
	require.Equal(t, "0x42", toAddress)
	// Second insert had no to_address → DEFAULT '' applied
	row = tx.QueryRow(`SELECT to_address FROM bridge WHERE block_num=1 AND block_pos=1`)
	var toAddress2 string
	err = row.Scan(&toAddress2)
	require.NoError(t, err)
	require.Equal(t, "", toAddress2)
	// Third insert had NULL to_address before migration → converted to '' by migration
	row = tx.QueryRow(`SELECT to_address FROM bridge WHERE block_num=1 AND block_pos=2`)
	var toAddress3 string
	err = row.Scan(&toAddress3)
	require.NoError(t, err)
	require.Equal(t, "", toAddress3, "NULL to_address must be converted to '' by migration 0013")
	err = tx.Commit()
	require.NoError(t, err)
}
func TestMigration0015(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgesyncTest0015.sqlite")

	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	// Run migrations up to 0014 — claim, set_claim and unset_claim still exist.
	err = db.RunMigrationsDBExtended(log.GetDefaultLogger(),
		database, GetUpTo("bridgesync0014"), nil, migrate.Up, db.NoLimitMigrations)
	require.NoError(t, err)

	tableExists := func(name string) bool {
		var count int
		err := database.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count)
		require.NoError(t, err)
		return count > 0
	}

	require.True(t, tableExists("claim"), "claim table should exist before migration 0015")
	require.True(t, tableExists("set_claim"), "set_claim table should exist before migration 0015")
	require.True(t, tableExists("unset_claim"), "unset_claim table should exist before migration 0015")

	// Apply migration 0015.
	err = db.RunMigrationsDBExtended(log.GetDefaultLogger(),
		database, GetUpTo("bridgesync0015"), nil, migrate.Up, db.NoLimitMigrations)
	require.NoError(t, err)

	require.False(t, tableExists("claim"), "claim table should be dropped by migration 0015")
	require.False(t, tableExists("set_claim"), "set_claim table should be dropped by migration 0015")
	require.False(t, tableExists("unset_claim"), "unset_claim table should be dropped by migration 0015")
}

func TestMigration0016_IndexesExist(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgesyncTest0016.sqlite")
	err := RunMigrations(dbPath)
	require.NoError(t, err)

	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	var name string
	err = database.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, "idx_bridge_from_address_upper",
	).Scan(&name)
	require.NoError(t, err, "index idx_bridge_from_address_upper should exist")
	require.Equal(t, "idx_bridge_from_address_upper", name)

	// Without ANALYZE stats, an index on destination_network hijacks the query planner away
	// from idx_bridge_deposit_count_desc and idx_bridge_from_address_upper, so it must not exist.
	err = database.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, "idx_bridge_destination_network",
	).Scan(&name)
	require.ErrorIs(t, err, sql.ErrNoRows, "index idx_bridge_destination_network must not exist")
}

func TestMigrationsDown(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgesyncTestDown.sqlite")
	err := RunMigrations(dbPath)
	require.NoError(t, err)
	err = RunMigrationsDown(dbPath, 1)
	require.Error(t, err)
}

// This check that migrations over existing databases produce the same schema as
// creating a new database.
// So it create a empty DB and apply all migrations.
// Copy databases "testdata/*.sqlite" and apply all migrations to them
// Compare schema of all databases that must be the same
func TestMigrationFromPreviousVersion(t *testing.T) {
	// Create a fresh empty DB and apply all migrations — this is the reference.
	freshDBPath := path.Join(t.TempDir(), "fresh.sqlite")
	err := RunMigrations(freshDBPath)
	require.NoError(t, err)

	freshDB, err := db.NewSQLiteDB(freshDBPath)
	require.NoError(t, err)
	defer freshDB.Close()

	referenceHash := schemaHash(t, freshDB)

	// Build the expected set of migration IDs from the full migration list.
	expectedIDs := make(map[string]struct{})
	for _, m := range GetFullMigrations() {
		expectedIDs[m.ID] = struct{}{}
	}

	// Verify the fresh DB itself has all expected migrations applied.
	appliedIDs, err := db.GetMigrationsIDsApplied(freshDB)
	require.NoError(t, err)
	for _, id := range appliedIDs {
		delete(expectedIDs, id)
	}
	require.Empty(t, expectedIDs, "fresh DB is missing migrations: %v", expectedIDs)

	// For each testdata/*.sqlite, copy it, apply all remaining migrations, and
	// verify that the resulting schema hash matches the reference and that all
	// expected migrations are recorded in gorp_migrations.
	testdataEntries, err := os.ReadDir("testdata")
	require.NoError(t, err)

	for _, entry := range testdataEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sqlite") {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			dstPath := path.Join(t.TempDir(), entry.Name())
			require.NoError(t, copyFile(path.Join("testdata", entry.Name()), dstPath))

			err := RunMigrations(dstPath)
			require.NoError(t, err)

			migratedDB, err := db.NewSQLiteDB(dstPath)
			require.NoError(t, err)
			defer migratedDB.Close()

			require.Equal(t, referenceHash, schemaHash(t, migratedDB),
				"schema mismatch for %s after applying all migrations", entry.Name())

			// Verify all expected migrations are recorded in gorp_migrations.
			applied, err := db.GetMigrationsIDsApplied(migratedDB)
			require.NoError(t, err)
			appliedSet := make(map[string]struct{}, len(applied))
			for _, id := range applied {
				appliedSet[id] = struct{}{}
			}
			for _, m := range GetFullMigrations() {
				require.Contains(t, appliedSet, m.ID,
					"migration %q missing from gorp_migrations after migrating %s", m.ID, entry.Name())
			}
		})
	}
}

// schemaHash returns a SHA-256 hash of the normalised schema of every user
// table in the database. Columns are sorted by name before hashing so that
// different column-creation orders (which can happen when a column was added
// outside the migration system) do not cause a false mismatch. Indexes are
// likewise sorted by name.
// gorp_migrations is excluded because its contents legitimately differ across
// databases with different migration histories.
func schemaHash(t *testing.T, database *sql.DB) string {
	t.Helper()

	tableRows, err := database.Query(`
		SELECT name FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'
		  AND name != 'gorp_migrations'
		ORDER BY name
	`)
	require.NoError(t, err)
	defer tableRows.Close()

	var tables []string
	for tableRows.Next() {
		var name string
		require.NoError(t, tableRows.Scan(&name))
		tables = append(tables, name)
	}
	require.NoError(t, tableRows.Err())

	var sb strings.Builder
	for _, tbl := range tables {
		fmt.Fprintf(&sb, "TABLE:%s\n", tbl)

		// --- columns (sorted by name, order-insensitive) ---
		colRows, err := database.Query("PRAGMA table_info(" + tbl + ")")
		require.NoError(t, err)

		type colDef struct {
			name    string
			colType string
			notNull int
			dflt    sql.NullString
			pk      int
		}
		var cols []colDef
		for colRows.Next() {
			var cid int
			var c colDef
			require.NoError(t, colRows.Scan(&cid, &c.name, &c.colType, &c.notNull, &c.dflt, &c.pk))
			cols = append(cols, c)
		}
		require.NoError(t, colRows.Err())
		colRows.Close()

		sort.Slice(cols, func(i, j int) bool { return cols[i].name < cols[j].name })
		for _, c := range cols {
			fmt.Fprintf(&sb, "  col:%s type:%s notnull:%d dflt:%v pk:%d\n",
				c.name, c.colType, c.notNull, c.dflt, c.pk)
		}

		// --- indexes (sorted by name) ---
		idxRows, err := database.Query("PRAGMA index_list(" + tbl + ")")
		require.NoError(t, err)

		var idxNames []string
		for idxRows.Next() {
			var seq, unique, partial int
			var name, origin string
			require.NoError(t, idxRows.Scan(&seq, &name, &unique, &origin, &partial))
			if origin != "pk" {
				idxNames = append(idxNames, name)
			}
		}
		require.NoError(t, idxRows.Err())
		idxRows.Close()

		sort.Strings(idxNames)
		for _, name := range idxNames {
			fmt.Fprintf(&sb, "  idx:%s\n", name)
		}
	}

	h := sha256.Sum256([]byte(sb.String()))
	return fmt.Sprintf("%x", h)
}

func TestAddSourceField(t *testing.T) {
	t.Run("skips when bridgesync0014 not applied", func(t *testing.T) {
		dbPath := path.Join(t.TempDir(), "add_source_no0014.sqlite")
		database, err := db.NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer database.Close()

		migs := GetUpTo("bridgesync0013")
		err = db.RunMigrationsDBExtended(log.GetDefaultLogger(), database, migs, nil, migrate.Up, db.NoLimitMigrations)
		require.NoError(t, err)

		err = addSourceField(database)
		require.NoError(t, err)

		// source column must NOT have been added
		_, err = database.Exec("SELECT source FROM bridge LIMIT 1")
		require.Error(t, err)
	})

	t.Run("adds source column when bridgesync0014 is applied", func(t *testing.T) {
		dbPath := path.Join(t.TempDir(), "add_source_0014.sqlite")
		database, err := db.NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer database.Close()

		migs := GetUpTo("bridgesync0014")
		err = db.RunMigrationsDBExtended(log.GetDefaultLogger(), database, migs, nil, migrate.Up, db.NoLimitMigrations)
		require.NoError(t, err)

		// Confirm source does not exist before calling addSourceField
		_, err = database.Exec("SELECT source FROM bridge LIMIT 1")
		require.Error(t, err)

		err = addSourceField(database)
		require.NoError(t, err)

		// source column must now exist
		_, err = database.Exec("SELECT source FROM bridge LIMIT 1")
		require.NoError(t, err)
	})

	t.Run("is idempotent when source column already exists", func(t *testing.T) {
		dbPath := path.Join(t.TempDir(), "add_source_idempotent.sqlite")
		database, err := db.NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer database.Close()

		migs := GetUpTo("bridgesync0014")
		err = db.RunMigrationsDBExtended(log.GetDefaultLogger(), database, migs, nil, migrate.Up, db.NoLimitMigrations)
		require.NoError(t, err)

		// First call adds the column
		err = addSourceField(database)
		require.NoError(t, err)

		// Second call must not fail
		err = addSourceField(database)
		require.NoError(t, err)

		// Column still exists
		_, err = database.Exec("SELECT source FROM bridge LIMIT 1")
		require.NoError(t, err)
	})
}

// copyFile copies the file at src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
