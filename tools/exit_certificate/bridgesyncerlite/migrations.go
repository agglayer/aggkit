package bridgesyncerlite

import (
	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	treemigrations "github.com/agglayer/aggkit/tree/migrations"
)

// bridgeTableMigration creates the single table this lite syncer persists: one row per BridgeEvent
// log, keyed by deposit_count (the contract's unique monotonic counter, which is also the exit-tree
// leaf index). Unlike the full bridgesync schema there is no block/claim/token_mapping table and no
// foreign keys — this syncer only ever stores bridge leaves and never tracks a sync checkpoint.
const bridgeTableMigration = `
-- +migrate Down
DROP TABLE IF EXISTS bridge;

-- +migrate Up
CREATE TABLE bridge (
	block_num           INTEGER NOT NULL,
	block_pos           INTEGER NOT NULL,
	leaf_type           INTEGER NOT NULL,
	origin_network      INTEGER NOT NULL,
	origin_address      VARCHAR NOT NULL,
	destination_network INTEGER NOT NULL,
	destination_address VARCHAR NOT NULL,
	amount              TEXT NOT NULL,
	metadata            BLOB,
	deposit_count       INTEGER NOT NULL PRIMARY KEY,
	tx_hash             VARCHAR NOT NULL
);
`

// getMigrations returns the bridge table migration followed by the shared tree migrations
// (creating the rht/root tables the AppendOnlyTree relies on).
func getMigrations() []dbtypes.Migration {
	migs := []dbtypes.Migration{{ID: "bridgesyncerlite0001", SQL: bridgeTableMigration}}
	return append(migs, treemigrations.Migrations...)
}

// runMigrations creates the bridge and tree tables in the sqlite file at dbPath.
func runMigrations(dbPath string) error {
	return db.RunMigrations(dbPath, getMigrations())
}
