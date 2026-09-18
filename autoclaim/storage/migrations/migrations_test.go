package migrations

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	logger "github.com/agglayer/aggkit/log"
	migrate "github.com/rubenv/sql-migrate"
	"github.com/stretchr/testify/require"
)

func TestGetAutoClaimMigrations(t *testing.T) {
	migrations := GetAutoClaimMigrations()
	require.Len(t, migrations, 3)
	require.Equal(t, "autoclaim0001", migrations[0].ID)
	require.NotEmpty(t, migrations[0].SQL)
	require.Equal(t, "autoclaim0002", migrations[1].ID)
	require.NotEmpty(t, migrations[1].SQL)
	require.Equal(t, "autoclaim0003", migrations[2].ID)
	require.NotEmpty(t, migrations[2].SQL)
}

func TestGetFullMigrations(t *testing.T) {
	full := GetFullMigrations()
	autoclaim := GetAutoClaimMigrations()
	require.NotEmpty(t, full)
	require.True(t, len(full) > len(autoclaim), "full migrations should include base + autoclaim")
	// Last entries should be autoclaim migrations
	last := full[len(full)-len(autoclaim):]
	require.Equal(t, autoclaim, last)
}

// TestAutoClaim0002MigratesExistingRows proves that an autoclaim0001-shaped database with pre-existing
// rows migrates correctly: request keys are recomputed to the source:destination:deposit_count format
// (source defaults to 0 for old rows), child transaction-attempt keys follow, source_network is 0, the
// new LER-cursor table exists, and the new UNIQUE(source_network, destination_network, deposit_count)
// constraint replaces the old origin-network based one.
func TestAutoClaim0002MigratesExistingRows(t *testing.T) {
	log := logger.GetDefaultLogger()
	dbPath := filepath.Join(t.TempDir(), "autoclaim.sqlite")
	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	// Apply base migrations + autoclaim0001 only (simulate a pre-existing autoclaim0001 database).
	require.NoError(t, db.RunMigrationsDBExtended(
		log, database,
		[]dbtypes.Migration{{ID: "autoclaim0001", SQL: autoClaim0001}},
		nil, migrate.Up, db.NoLimitMigrations,
	))

	now := time.Now().UTC()
	// Row 1 uses a non-zero origin_network in its old key to prove the re-key uses the source network
	// (0), not the token origin network.
	insertLegacyRequest(t, database, "5:1101:7", 5, 1101, 7, now)
	insertLegacyRequest(t, database, "0:1101:8", 0, 1101, 8, now)
	insertLegacyAttempt(t, database, "5:1101:7", now)

	// Apply the full migration set; only autoclaim0002 is pending.
	require.NoError(t, RunMigrations(log, database))

	// Request keys recomputed to 0:destination:deposit_count, source_network is 0, new columns default.
	type reqRow struct {
		key         string
		source      uint32
		origin      uint32
		verifyBlock uint64
		lerIsNull   bool
	}
	rows, err := database.Query(`
		SELECT request_key, source_network, origin_network, verify_block_num,
			ler IS NULL
		FROM autoclaim_request ORDER BY deposit_count`)
	require.NoError(t, err)
	defer rows.Close()

	var got []reqRow
	for rows.Next() {
		var r reqRow
		require.NoError(t, rows.Scan(&r.key, &r.source, &r.origin, &r.verifyBlock, &r.lerIsNull))
		got = append(got, r)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []reqRow{
		{key: "0:1101:7", source: 0, origin: 5, verifyBlock: 0, lerIsNull: true},
		{key: "0:1101:8", source: 0, origin: 0, verifyBlock: 0, lerIsNull: true},
	}, got)

	// The child transaction attempt is re-pointed at the recomputed request key.
	var attemptKey string
	require.NoError(t, database.QueryRow(
		"SELECT request_key FROM autoclaim_transaction_attempt").Scan(&attemptKey))
	require.Equal(t, "0:1101:7", attemptKey)

	// The LER cursor table exists.
	var lerTable string
	require.NoError(t, database.QueryRow(
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'autoclaim_ler_cursor'").Scan(&lerTable))
	require.Equal(t, "autoclaim_ler_cursor", lerTable)

	// The leaf_proof_json column was dropped: the leaf-to-LER proof is fetched fresh at claim time,
	// never stored.
	columnRows, err := database.Query("PRAGMA table_info(autoclaim_request)")
	require.NoError(t, err)
	defer columnRows.Close()
	var columns []string
	for columnRows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		require.NoError(t, columnRows.Scan(&cid, &name, &ctype, &notNull, &defaultVal, &pk))
		columns = append(columns, name)
	}
	require.NoError(t, columnRows.Err())
	require.NotContains(t, columns, "leaf_proof_json")

	// New uniqueness holds: a duplicate (source_network, destination_network, deposit_count) is rejected.
	_, err = database.Exec(insertRequestSQL(),
		"dup", 0, 999, 1101, 7, "detected", "0xdup", int64(1), int64(0), now, now, "{}")
	require.Error(t, err)

	// Old origin-network uniqueness is gone: same (origin_network, destination_network, deposit_count)
	// as row 1 (5, 1101, 7) is accepted because it has a distinct source_network.
	_, err = database.Exec(insertRequestSQL(),
		"2:1101:7", 2, 5, 1101, 7, "detected", "0xok", int64(2), int64(0), now, now, "{}")
	require.NoError(t, err)
}

// TestAutoClaim0003MigratesExistingLERCursorRows exercises the autoclaim0003 migration, which
// re-keys the durable LER discovery cursor from source_network alone to (source_network,
// destination_network).
//
// Mirrors TestAutoClaim0002MigratesExistingRows's style: apply base + 0001 + 0002, seed pre-0003 rows,
// apply 0003 Up and assert the re-key + legacy parking, then apply Down and assert the pre-0003 schema
// is restored (the Down migration collapses per-(source,destination) rows back to one row per source,
// keeping the lowest last_verify_block_num pair, and folds in any still-parked legacy rows -- see
// TestAutoClaim0003DownKeepsConservativePairPerSource below for that collapse rule exercised directly
// against several real per-pair rows).
func TestAutoClaim0003MigratesExistingLERCursorRows(t *testing.T) {
	log := logger.GetDefaultLogger()
	dbPath := filepath.Join(t.TempDir(), "autoclaim.sqlite")
	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	// Apply base + autoclaim0001 + autoclaim0002 (simulate a pre-autoclaim0003 database that has
	// already shipped the source-network-keyed autoclaim_ler_cursor table -- autoclaim0002 shipped in
	// v0.11.0-rc3..rc10, so a compat seed is mandatory, not a free rewrite).
	require.NoError(t, db.RunMigrationsDBExtended(
		log, database,
		[]dbtypes.Migration{
			{ID: "autoclaim0001", SQL: autoClaim0001},
			{ID: "autoclaim0002", SQL: autoClaim0002},
		},
		nil, migrate.Up, db.NoLimitMigrations,
	))

	now := time.Now().UTC()
	_, err = database.Exec(`
		INSERT INTO autoclaim_ler_cursor (source_network, last_ler, last_verify_block_num, updated_at)
		VALUES (?, ?, ?, ?)`,
		uint32(5), "0xaaaa", int64(100), now,
	)
	require.NoError(t, err)
	_, err = database.Exec(`
		INSERT INTO autoclaim_ler_cursor (source_network, last_ler, last_verify_block_num, updated_at)
		VALUES (?, ?, ?, ?)`,
		uint32(9), "0xbbbb", int64(200), now,
	)
	require.NoError(t, err)

	// Apply autoclaim0003 Up: the table is re-keyed to (source_network, destination_network) and the
	// pre-existing per-source rows are parked in autoclaim_ler_cursor_legacy, not discarded.
	// RunMigrations (the full migration set, mirroring TestAutoClaim0002MigratesExistingRows above) is
	// used rather than RunMigrationsDBExtended with a partial migration slice: with maxMigrations ==
	// db.NoLimitMigrations, the migration runner does not set migrate.SetIgnoreUnknown, so a partial
	// slice that omits already-applied IDs (autoclaim0001, autoclaim0002) makes the underlying
	// sql-migrate library reject the plan with "unknown migration in database".
	require.NoError(t, RunMigrations(log, database))

	// autoclaim_ler_cursor now has the composite primary key and is empty (nothing has been seeded yet
	// -- seeding is a runtime concern, driven by Storage.SeedLERCursorsFromLegacy, not this migration).
	columnRows, err := database.Query("PRAGMA table_info(autoclaim_ler_cursor)")
	require.NoError(t, err)
	var pkColumns []string
	for columnRows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		require.NoError(t, columnRows.Scan(&cid, &name, &ctype, &notNull, &defaultVal, &pk))
		if pk > 0 {
			pkColumns = append(pkColumns, name)
		}
	}
	require.NoError(t, columnRows.Err())
	columnRows.Close()
	require.ElementsMatch(t, []string{"source_network", "destination_network"}, pkColumns)

	var newTableCount int
	require.NoError(t, database.QueryRow("SELECT COUNT(*) FROM autoclaim_ler_cursor").Scan(&newTableCount))
	require.Equal(t, 0, newTableCount)

	// The pre-existing rows are preserved, not dropped, in the legacy table.
	type legacyRow struct {
		source      uint32
		ler         string
		verifyBlock uint64
	}
	rows, err := database.Query(`
		SELECT source_network, last_ler, last_verify_block_num
		FROM autoclaim_ler_cursor_legacy ORDER BY source_network`)
	require.NoError(t, err)
	var gotLegacy []legacyRow
	for rows.Next() {
		var r legacyRow
		require.NoError(t, rows.Scan(&r.source, &r.ler, &r.verifyBlock))
		gotLegacy = append(gotLegacy, r)
	}
	require.NoError(t, rows.Err())
	rows.Close()
	require.Equal(t, []legacyRow{
		{source: 5, ler: "0xaaaa", verifyBlock: 100},
		{source: 9, ler: "0xbbbb", verifyBlock: 200},
	}, gotLegacy)

	// Down restores the autoclaim0002 schema (source_network PRIMARY KEY) with the rows folded back in.
	require.NoError(t, db.RunMigrationsDBExtended(
		log, database,
		[]dbtypes.Migration{{ID: "autoclaim0003", SQL: autoClaim0003}},
		nil, migrate.Down, 1,
	))

	columnRows, err = database.Query("PRAGMA table_info(autoclaim_ler_cursor)")
	require.NoError(t, err)
	pkColumns = nil
	var columnNames []string
	for columnRows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		require.NoError(t, columnRows.Scan(&cid, &name, &ctype, &notNull, &defaultVal, &pk))
		columnNames = append(columnNames, name)
		if pk > 0 {
			pkColumns = append(pkColumns, name)
		}
	}
	require.NoError(t, columnRows.Err())
	columnRows.Close()
	require.Equal(t, []string{"source_network"}, pkColumns, "Down must restore the single-column PK")
	require.NotContains(t, columnNames, "destination_network")

	var restoredCount int
	require.NoError(t, database.QueryRow("SELECT COUNT(*) FROM autoclaim_ler_cursor").Scan(&restoredCount))
	require.Equal(t, 2, restoredCount, "both parked legacy rows must be folded back in on Down")

	// The legacy table no longer exists after Down.
	var legacyTable string
	err = database.QueryRow(
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'autoclaim_ler_cursor_legacy'",
	).Scan(&legacyTable)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

// TestAutoClaim0003DownKeepsConservativePairPerSource exercises the Down migration's
// most-conservative-pair subquery directly, unlike TestAutoClaim0003MigratesExistingLERCursorRows
// above, which never seeds autoclaim_ler_cursor with more than one destination per source before
// running Down (there, every restored row comes from the legacy fold-back, not from collapsing real
// per-pair rows). Here, several per-(source, destination) rows are inserted for the same source with
// different last_verify_block_num values -- the shape a real deployment has after
// SeedLERCursorsFromLegacy/normal operation seeds and advances several destinations -- and Down must
// keep exactly the pair with the lowest last_verify_block_num (ties broken by the lowest
// destination_network), per the migration's doc comment: rolling back then re-fetches from an older
// LER, which is idempotent rather than skipping candidates.
func TestAutoClaim0003DownKeepsConservativePairPerSource(t *testing.T) {
	log := logger.GetDefaultLogger()
	dbPath := filepath.Join(t.TempDir(), "autoclaim.sqlite")
	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	require.NoError(t, RunMigrations(log, database))

	now := time.Now().UTC()
	type seedPair struct {
		source, destination uint32
		ler                 string
		verify              int64
	}
	seedPairs := []seedPair{
		// Source 5: three destinations: 101 is the conservative pick (lowest verify block, 200).
		{source: 5, destination: 101, ler: "0xbbbb", verify: 200},
		{source: 5, destination: 100, ler: "0xaaaa", verify: 300},
		{source: 5, destination: 102, ler: "0xcccc", verify: 250},
		// Source 9: a tie on verify block (50): the lowest destination_network (200) must win.
		{source: 9, destination: 201, ler: "0xffff", verify: 50},
		{source: 9, destination: 200, ler: "0xdddd", verify: 50},
	}
	for _, p := range seedPairs {
		_, err := database.Exec(`
			INSERT INTO autoclaim_ler_cursor (
				source_network, destination_network, last_ler, last_verify_block_num, updated_at
			) VALUES (?, ?, ?, ?, ?)`,
			p.source, p.destination, p.ler, p.verify, now,
		)
		require.NoError(t, err)
	}

	require.NoError(t, db.RunMigrationsDBExtended(
		log, database,
		[]dbtypes.Migration{{ID: "autoclaim0003", SQL: autoClaim0003}},
		nil, migrate.Down, 1,
	))

	type restoredRow struct {
		source uint32
		ler    string
		verify int64
	}
	rows, err := database.Query(`
		SELECT source_network, last_ler, last_verify_block_num FROM autoclaim_ler_cursor ORDER BY source_network`)
	require.NoError(t, err)
	var got []restoredRow
	for rows.Next() {
		var r restoredRow
		require.NoError(t, rows.Scan(&r.source, &r.ler, &r.verify))
		got = append(got, r)
	}
	require.NoError(t, rows.Err())
	rows.Close()

	require.Equal(t, []restoredRow{
		{source: 5, ler: "0xbbbb", verify: 200},
		{source: 9, ler: "0xdddd", verify: 50},
	}, got, "Down must keep exactly the lowest-last_verify_block_num pair per source "+
		"(ties broken by the lowest destination_network)")
}

func insertRequestSQL() string {
	return `INSERT INTO autoclaim_request (
		request_key, source_network, origin_network, destination_network, deposit_count, status,
		bridge_tx_hash, block_num, block_pos, created_at, updated_at, bridge_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

func insertLegacyRequest(
	t *testing.T,
	database *sql.DB,
	key string,
	originNetwork, destinationNetwork, depositCount uint32,
	now time.Time,
) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO autoclaim_request (
			request_key, origin_network, destination_network, deposit_count, status,
			bridge_tx_hash, block_num, block_pos, created_at, updated_at, bridge_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		key, originNetwork, destinationNetwork, depositCount, "detected",
		"0xbridge", int64(depositCount), int64(0), now, now, "{}",
	)
	require.NoError(t, err)
}

func insertLegacyAttempt(
	t *testing.T,
	database *sql.DB,
	requestKey string,
	now time.Time,
) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO autoclaim_transaction_attempt (
			request_key, attempt_number, claimer_id, tx_manager_id, claim_tx_hash, status,
			created_at, updated_at, target_bridge_addr, attempt_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		requestKey, int64(1), "claimer", "0xtxm", "0xclaim", "sent",
		now, now, "0xbridgeaddr", "{}",
	)
	require.NoError(t, err)
}
