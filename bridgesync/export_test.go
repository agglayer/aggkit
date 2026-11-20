package bridgesync

import (
	"path"
	"testing"

	"github.com/agglayer/aggkit/bridgesync/migrations"
	mocksbridgesync "github.com/agglayer/aggkit/bridgesync/mocks"
	"github.com/agglayer/aggkit/log"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

// NewSeededBridgeSyncForTest returns a BridgeSync backed by a real SQLite database (all the migrations
// applied) that holds the given bridges and token mappings, so tests of other packages (the bridge
// service HTTP handlers, for instance) can run against the real query code instead of a mock.
//
// The BridgeSync is not started: it only serves reads. It has no real reorg detector, so the finality
// of a block is never reported (the reorg detector mock fails the test if it is ever asked).
// It lives in an export_test.go file because the processor is unexported and a production constructor
// would be test-only API.
func NewSeededBridgeSyncForTest(t *testing.T, bridges []*Bridge, tokenMappings []*TokenMapping) *BridgeSync {
	t.Helper()

	dbPath := path.Join(t.TempDir(), "bridgesync-seeded.sqlite")
	require.NoError(t, migrations.RunMigrations(dbPath))
	p, err := newTestProcessor(dbPath, "bridge-syncer", log.WithFields("module", "bridge-syncer-seeded"), dbQueryTimeout)
	require.NoError(t, err)

	// every bridge and token mapping references its block, so the block rows go first
	maxBlockNum := uint64(0)
	for _, bridge := range bridges {
		maxBlockNum = max(maxBlockNum, bridge.BlockNum)
	}
	for _, tokenMapping := range tokenMappings {
		maxBlockNum = max(maxBlockNum, tokenMapping.BlockNum)
	}

	tx, err := p.db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	for blockNum := uint64(1); blockNum <= maxBlockNum; blockNum++ {
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, blockNum)
		require.NoError(t, err)
	}
	for _, bridge := range bridges {
		require.NoError(t, meddler.Insert(tx, bridgeTableName, bridge))
	}
	for _, tokenMapping := range tokenMappings {
		require.NoError(t, meddler.Insert(tx, tokenMappingTableName, tokenMapping))
	}
	require.NoError(t, tx.Commit())

	return &BridgeSync{
		processor:       p,
		reorgDetector:   mocksbridgesync.NewReorgDetector(t),
		reorgDetectorID: "bridge-syncer-seeded",
	}
}
