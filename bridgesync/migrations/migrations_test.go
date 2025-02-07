package migrations

import (
	"context"
	"path"
	"testing"

	"github.com/agglayer/aggkit/db"
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
			origin_network, 
			origin_token_address, 
			wrapped_token_address,
			metadata
		) VALUES (1, 0, 2, '0x0003', '0x0005', NULL);
	`)
	require.NoError(t, err)
	err = tx.Commit()
	require.NoError(t, err)

	var tokenMapping struct {
		BlockNum            int     `meddler:"block_num"`
		BlockPos            int     `meddler:"block_pos"`
		OriginNetwork       int     `meddler:"origin_network"`
		OriginTokenAddress  string  `meddler:"origin_token_address"`
		WrappedTokenAddress string  `meddler:"wrapped_token_address"`
		Metadata            *string `meddler:"metadata"`
	}

	err = meddler.QueryRow(db, &tokenMapping,
		`SELECT 
			block_num, block_pos, 
			origin_network, origin_token_address, 
			wrapped_token_address, metadata
		FROM token_mapping`)
	require.NoError(t, err)

	require.NotNil(t, tokenMapping)
	require.Equal(t, 1, tokenMapping.BlockNum)
	require.Equal(t, 0, tokenMapping.BlockPos)
	require.Equal(t, 2, tokenMapping.OriginNetwork)
	require.Equal(t, "0x0003", tokenMapping.OriginTokenAddress)
	require.Equal(t, "0x0005", tokenMapping.WrappedTokenAddress)
}
