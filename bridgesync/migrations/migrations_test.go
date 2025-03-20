package migrations

import (
	"context"
	"math/big"
	"path"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/ethereum/go-ethereum/common"
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
		INSERT INTO block (num) VALUES (1);

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
		INSERT INTO block (num) VALUES (1);

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
			type
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
			tx_hash,
			from_address
		) VALUES (1, 0, 0, 0, '0x3', '0x0000', 0, 0, NULL, FALSE, 1739270804, '0xabcd', '0x123');
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
		Type                uint8          `meddler:"type"`
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
		FromAddress        string   `meddler:"from_address"`
	}

	err = meddler.QueryRow(db, &claim,
		`SELECT * FROM claim`)
	require.NoError(t, err)
	require.NotNil(t, claim)
	require.Equal(t, uint64(1739270804), claim.BlockTimestamp)
	require.Equal(t, "0x123", claim.FromAddress)
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
		INSERT INTO block (num) VALUES (1);

		INSERT INTO legacy_token_migration (
			block_num,
			block_pos,
			block_timestamp,
			tx_hash,
			sender,
			legacy_token_address,
			updated_token_address,
			amount,
			calldata
		) VALUES (1, 10, 1739270804, '0xabcd', '0x3', '0x5', '0x7', 1000, NULL);
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
