package migrations

import (
	"context"
	"math/big"
	"path"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	migrate "github.com/rubenv/sql-migrate"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

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

		INSERT INTO claim (
			block_num,
			block_pos,
    		global_index,
			origin_network,
			origin_address,
			destination_address,
			amount,
			proof_local_exit_root,
			proof_rollup_exit_root,
			mainnet_exit_root,
			rollup_exit_root,
			global_exit_root,
			destination_network,
			metadata,
			is_message
		) VALUES (1, 0, 0, 0, '0x0000', '0x0000', 0, '0x000,0x000', '0x000,0x000', '0x000', '0x000', '0x0', 0, NULL, FALSE);
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

		INSERT INTO claim (
			block_num,
			block_pos,
    		global_index,
			origin_network,
			origin_address,
			destination_address,
			amount,
			destination_network,
			metadata,
			is_message,
			block_timestamp,
			tx_hash
		) VALUES (1, 0, 0, 0, '0x3', '0x0000', 0, 0, NULL, FALSE, 1739270804, '0xabcd');
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
		FromAddress        string   `meddler:"from_address"`
		TxnSender          string   `meddler:"txn_sender"`
	}

	err = meddler.QueryRow(db, &bridge,
		`SELECT * FROM bridge`)
	require.NoError(t, err)
	require.NotNil(t, bridge)
	require.Equal(t, uint64(1739270804), bridge.BlockTimestamp)

	var claim struct {
		BlockNum           uint64   `meddler:"block_num"`
		BlockPos           uint64   `meddler:"block_pos"`
		GlobalIndex        *big.Int `meddler:"global_index,bigint"`
		OriginNetwork      uint32   `meddler:"origin_network"`
		OriginAddress      string   `meddler:"origin_address"`
		DestinationAddress string   `meddler:"destination_address"`
		Amount             *big.Int `meddler:"amount,bigint"`
		DestinationNetwork uint32   `meddler:"destination_network"`
		Metadata           []byte   `meddler:"metadata"`
		IsMessage          bool     `meddler:"is_message"`
		BlockTimestamp     uint64   `meddler:"block_timestamp"`
		TxHash             string   `meddler:"tx_hash"`
	}

	err = meddler.QueryRow(db, &claim,
		`SELECT * FROM claim`)
	require.NoError(t, err)
	require.NotNil(t, claim)
	require.Equal(t, uint64(1739270804), claim.BlockTimestamp)
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
	err = db.RunMigrationsDBExtended(log.GetDefaultLogger(), database, migrations, migrate.Up, 3)
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
	err = db.RunMigrationsDBExtended(log.GetDefaultLogger(), database, migrations, migrate.Up, 5)
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
		FromAddress        string   `meddler:"from_address"`
		TxnSender          string   `meddler:"txn_sender"`
	}

	// Test that we can query the txn_sender column after migration
	err = meddler.QueryRow(database, &bridgeWithTxnSender,
		`SELECT * FROM bridge WHERE block_pos = 0`)
	require.NoError(t, err)
	require.NotNil(t, bridgeWithTxnSender)
	require.Equal(t, "", bridgeWithTxnSender.TxnSender) // Should have default empty string value
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
		FromAddress        string   `meddler:"from_address"`
		TxnSender          string   `meddler:"txn_sender"`
	}

	err = meddler.QueryRow(database, &bridgeWithTxnSender2,
		`SELECT * FROM bridge WHERE block_pos = 1`)
	require.NoError(t, err)
	require.NotNil(t, bridgeWithTxnSender2)
	require.Equal(t, "", bridgeWithTxnSender2.TxnSender) // Should have default empty string value
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
		FromAddress        string   `meddler:"from_address"`
		TxnSender          string   `meddler:"txn_sender"`
	}

	err = meddler.QueryRow(database, &bridgeWithCustomTxnSender,
		`SELECT * FROM bridge WHERE block_pos = 2`)
	require.NoError(t, err)
	require.NotNil(t, bridgeWithCustomTxnSender)
	require.Equal(t, "0xAAAA", bridgeWithCustomTxnSender.TxnSender)
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
