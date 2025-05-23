package migrations

import (
	"context"
	"path"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

func TestRunMigrations(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "claimsponsorTest0001.sqlite")
	err := RunMigrations(dbPath)
	require.NoError(t, err)
	db, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	metadata := []byte("metadata")
	_, err = tx.Exec(
		`INSERT INTO claim (
			leaf_type,
			proof_local_exit_root,
			proof_rollup_exit_root,
			global_index,
			mainnet_exit_root,
			rollup_exit_root,
			origin_network,
			origin_token_address,
			destination_network,
			destination_address,
			amount,
			metadata,
			status,
			tx_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		1, "proof1", "proof2", "1001", "0xabc", "0xdef", 10, "0x123", 20, "0x456", "5000", metadata, "PendingClaimStatus", "tx123",
	)
	require.NoError(t, err)
	err = tx.Commit()
	require.NoError(t, err)

	var claim struct {
		LeafType            uint8  `meddler:"leaf_type"`
		ProofLocalExitRoot  string `meddler:"proof_local_exit_root"`
		ProofRollupExitRoot string `meddler:"proof_rollup_exit_root"`
		GlobalIndex         string `meddler:"global_index"`
		MainnetExitRoot     string `meddler:"mainnet_exit_root"`
		RollupExitRoot      string `meddler:"rollup_exit_root"`
		OriginNetwork       uint32 `meddler:"origin_network"`
		OriginTokenAddress  string `meddler:"origin_token_address"`
		DestinationNetwork  uint32 `meddler:"destination_network"`
		DestinationAddress  string `meddler:"destination_address"`
		Amount              string `meddler:"amount"`
		Metadata            []byte `meddler:"metadata"`
		Status              string `meddler:"status"`
		TxID                string `meddler:"tx_id"`
	}

	err = meddler.QueryRow(db, &claim, `SELECT * FROM claim WHERE global_index = 1001;`)
	require.NoError(t, err)
	require.Equal(t, uint8(1), claim.LeafType)
	require.Equal(t, "proof1", claim.ProofLocalExitRoot)
	require.Equal(t, "proof2", claim.ProofRollupExitRoot)
	require.Equal(t, "1001", claim.GlobalIndex)
	require.Equal(t, "0xabc", claim.MainnetExitRoot)
	require.Equal(t, "0xdef", claim.RollupExitRoot)
	require.Equal(t, uint32(10), claim.OriginNetwork)
	require.Equal(t, "0x123", claim.OriginTokenAddress)
	require.Equal(t, uint32(20), claim.DestinationNetwork)
	require.Equal(t, "0x456", claim.DestinationAddress)
	require.Equal(t, "5000", claim.Amount)
	require.Equal(t, metadata, claim.Metadata)
	require.Equal(t, "PendingClaimStatus", claim.Status)
	require.Equal(t, "tx123", claim.TxID)
}
