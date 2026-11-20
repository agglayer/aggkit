package migrations

import (
	"database/sql"
	"path"
	"testing"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/db"
	"github.com/stretchr/testify/require"
)

// migrationIndex is the position of id in all()
func migrationIndex(t *testing.T, id string) int {
	t.Helper()
	for i, m := range all() {
		if m.ID == id {
			return i
		}
	}
	t.Fatalf("unknown migration %s", id)
	return -1
}

// openMigratedUpTo returns a DB at a fresh path with every migration before id applied
func openMigratedUpTo(t *testing.T, id string) (string, *sql.DB) {
	t.Helper()
	dbPath := path.Join(t.TempDir(), "bridgetracker.sqlite")
	require.NoError(t, db.RunMigrations(dbPath, all()[:migrationIndex(t, id)]))
	sqlDB, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return dbPath, sqlDB
}

const (
	addrA = "0x000000000000000000000000000000000000000A"
	addrB = "0x000000000000000000000000000000000000000B"
)

func insertAddress(t *testing.T, sqlDB *sql.DB, addr string) {
	t.Helper()
	_, err := sqlDB.Exec(`INSERT INTO activity_address (from_address, schema_version, updated_at, last_access,
		scan_state) VALUES (?, 1, 0, 0, '{}')`, addr)
	require.NoError(t, err)
}

// insertOldBridgeRow inserts an activity_bridge row in the pre-0006 shape (PRIMARY KEY global_index)
func insertOldBridgeRow(t *testing.T, sqlDB *sql.DB, globalIndex, addr string, network uint32, data string,
	revisionHash []byte) {
	t.Helper()
	_, err := sqlDB.Exec(`INSERT INTO activity_bridge (global_index, from_address, schema_version,
		origin_network_id, destination_network, tx_hash, claim_status, tracker_claim_status, block_timestamp,
		created_at, updated_at, data, revision_hash) VALUES (?, ?, 1, ?, 2, '0xtx', 'unclaimed', 'pending', 10,
		20, 30, ?, ?)`, globalIndex, addr, network, []byte(data), revisionHash)
	require.NoError(t, err)
}

type migratedRow struct {
	globalIndex, fromAddress string
	network, depositCount    uint32
	createdAt                int64
	data, revisionHash       []byte
}

func migratedRows(t *testing.T, sqlDB *sql.DB) []migratedRow {
	t.Helper()
	rows, err := sqlDB.Query(`SELECT global_index, from_address, origin_network_id, deposit_count, created_at,
		data, revision_hash FROM activity_bridge ORDER BY from_address, origin_network_id, deposit_count`)
	require.NoError(t, err)
	defer rows.Close()
	var out []migratedRow
	for rows.Next() {
		var r migratedRow
		require.NoError(t, rows.Scan(&r.globalIndex, &r.fromAddress, &r.network, &r.depositCount, &r.createdAt,
			&r.data, &r.revisionHash))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

func indexNames(t *testing.T, sqlDB *sql.DB) []string {
	t.Helper()
	rows, err := sqlDB.Query(
		"SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'activity_bridge' AND sql IS NOT NULL")
	require.NoError(t, err)
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		names = append(names, name)
	}
	require.NoError(t, rows.Err())
	return names
}

func requireNoForeignKeyViolations(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	rows, err := sqlDB.Query("PRAGMA foreign_key_check")
	require.NoError(t, err)
	defer rows.Close()
	require.False(t, rows.Next(), "foreign_key_check reports violations")
	require.NoError(t, rows.Err())
}

// TestMigration0006RekeysActivityBridgePerOwner seeds activity_bridge rows in the old
// PRIMARY KEY (global_index) shape, upgrades, and checks every keyable row survives with its
// columns (revision_hash included) and a backfilled deposit_count, unkeyable rows are dropped, the
// new key admits what the old one could not, and the foreign key,
// cascade and indexes still hold
func TestMigration0006RekeysActivityBridgePerOwner(t *testing.T) {
	dbPath, sqlDB := openMigratedUpTo(t, "bridgetracker0006")
	insertAddress(t, sqlDB, addrA)
	insertAddress(t, sqlDB, addrB)
	hash := make([]byte, 32)
	hash[31] = 7
	insertOldBridgeRow(t, sqlDB, "7", addrA, 0, `{"Bridge":{"deposit_count":7,"global_index":"7"}}`, hash)
	insertOldBridgeRow(t, sqlDB, "4294967298", addrB, 2, `{"Bridge":{"deposit_count":2}}`, nil)
	insertOldBridgeRow(t, sqlDB, "8", addrA, 0, `not json`, nil)                  // cannot be keyed: dropped
	insertOldBridgeRow(t, sqlDB, "9", addrA, 0, `{"Bridge":{"amount":"1"}}`, nil) // no deposit_count: dropped
	require.NoError(t, sqlDB.Close())

	require.NoError(t, db.RunMigrations(dbPath, all()[:migrationIndex(t, "bridgetracker0006")+1]))
	sqlDB, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	rows := migratedRows(t, sqlDB)
	require.Len(t, rows, 2)
	require.Equal(t, migratedRow{
		globalIndex: "7", fromAddress: addrA, network: 0, depositCount: 7, createdAt: 20,
		data: []byte(`{"Bridge":{"deposit_count":7,"global_index":"7"}}`), revisionHash: hash,
	}, rows[0])
	require.Equal(t, uint32(2), rows[1].depositCount)
	require.Nil(t, rows[1].revisionHash, "a NULL revision_hash stays NULL (recomputed from data on read)")
	requireNoForeignKeyViolations(t, sqlDB)
	require.ElementsMatch(t,
		[]string{"idx_activity_bridge_from_address_status", "idx_activity_bridge_from_address_timestamp"},
		indexNames(t, sqlDB))

	insert := func(globalIndex, addr string, network, depositCount uint32) error {
		_, err := sqlDB.Exec(`INSERT INTO activity_bridge (global_index, from_address, schema_version,
			origin_network_id, deposit_count, destination_network, tx_hash, claim_status, tracker_claim_status,
			block_timestamp, created_at, updated_at, data) VALUES (?, ?, 1, ?, ?, 1, '0x', 'unclaimed', 'pending',
			0, 0, 0, '{}')`, globalIndex, addr, network, depositCount)
		return err
	}
	require.NoError(t, insert("7", addrB, 0, 7), "another address can hold the same bridge")
	require.NoError(t, insert("7", addrA, 1, 7), "the legacy collision: same global index, another network")
	require.Error(t, insert("7", addrA, 0, 7), "one row per owner and bridge")
	require.Error(t, insert("1", "0xunknown", 0, 1), "the foreign key to activity_address still holds")

	_, err = sqlDB.Exec("DELETE FROM activity_address WHERE from_address = ?", addrA)
	require.NoError(t, err)
	for _, row := range migratedRows(t, sqlDB) {
		require.Equal(t, addrB, row.fromAddress, "deleting A cascades to A's rows only")
	}
}

// TestMigration0006Down goes back to PRIMARY KEY (global_index): rows whose global index is unique
// are kept, rows the old key cannot hold (a global index shared by two rows) are dropped
func TestMigration0006Down(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgetracker.sqlite")
	require.NoError(t, RunMigrations(dbPath))
	sqlDB, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	insertAddress(t, sqlDB, addrA)
	insertAddress(t, sqlDB, addrB)
	for _, row := range []struct {
		globalIndex, addr     string
		network, depositCount uint32
	}{{"7", addrA, 0, 7}, {"7", addrB, 0, 7}, {"7", addrA, 1, 7}, {"5", addrA, 0, 5}} {
		_, err := sqlDB.Exec(`INSERT INTO activity_bridge (global_index, from_address, schema_version,
			origin_network_id, deposit_count, destination_network, tx_hash, claim_status, tracker_claim_status,
			block_timestamp, created_at, updated_at, data) VALUES (?, ?, 1, ?, ?, 1, '0x', 'unclaimed', 'pending',
			0, 0, 0, '{}')`, row.globalIndex, row.addr, row.network, row.depositCount)
		require.NoError(t, err)
	}

	require.NoError(t, db.RunMigrationsDown(dbPath, all(), nil, len(all())-migrationIndex(t, "bridgetracker0006")))

	var globalIndexes []string
	rows, err := sqlDB.Query("SELECT global_index FROM activity_bridge")
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var gi string
		require.NoError(t, rows.Scan(&gi))
		globalIndexes = append(globalIndexes, gi)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"5"}, globalIndexes)
	_, err = sqlDB.Exec("SELECT deposit_count FROM activity_bridge")
	require.Error(t, err, "the column is gone")
}

// TestMigration0007BackfillsGlobalIndexSortKey verifies existing rows get the same gi_sort the
// store writes (domain.GlobalIndexSortKey) and the page index is created
func TestMigration0007BackfillsGlobalIndexSortKey(t *testing.T) {
	dbPath, sqlDB := openMigratedUpTo(t, "bridgetracker0007")
	insertAddress(t, sqlDB, addrA)
	globalIndexes := []string{"7", "007", "0", "18446744073709551616", "36893488147419103231", "not a number"}
	for i, gi := range globalIndexes {
		_, err := sqlDB.Exec(`INSERT INTO activity_bridge (global_index, from_address, schema_version,
			origin_network_id, deposit_count, destination_network, tx_hash, claim_status, tracker_claim_status,
			block_timestamp, created_at, updated_at, data) VALUES (?, ?, 1, 0, ?, 1, '0x', 'unclaimed', 'pending',
			0, 0, 0, '{}')`, gi, addrA, i)
		require.NoError(t, err)
	}
	require.NoError(t, sqlDB.Close())

	require.NoError(t, RunMigrations(dbPath))
	sqlDB, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	for _, gi := range globalIndexes {
		var giSort string
		require.NoError(t, sqlDB.QueryRow("SELECT gi_sort FROM activity_bridge WHERE global_index = ?", gi).
			Scan(&giSort))
		require.Len(t, giSort, domain.GlobalIndexSortWidth)
		require.Equal(t, domain.GlobalIndexSortKey(bridgeservicetypes.BigIntString(gi)), giSort, gi)
	}
	require.Contains(t, indexNames(t, sqlDB), "idx_activity_bridge_page")
	requireNoForeignKeyViolations(t, sqlDB)
}
