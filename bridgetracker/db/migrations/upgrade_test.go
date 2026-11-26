package migrations_test

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/api"
	"github.com/agglayer/aggkit/bridgetracker/db"
	"github.com/agglayer/aggkit/bridgetracker/db/migrations"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/mocks"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkitdb "github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// upgradeAddress is the address whose activity the pre-0006 database holds
var upgradeAddress = common.HexToAddress("0x1111111111111111111111111111111111111111")

// v0005Bridge is one activity_bridge row as the store wrote it before bridgetracker0006: keyed by
// global_index alone, no deposit_count column, data in the activityBridgeData JSON shape and a
// revision_hash of the v1 scheme
type v0005Bridge struct {
	network uint32
	bridge  *bridgeservicetypes.BridgeResponse
	status  types.TrackerClaimStatus
	created time.Time
}

// seedV0005 creates a database at dbPath with the migrations up to bridgetracker0005 only and
// writes rows exactly the way the store did then (same INSERT statements as
// sqliteActivityStore.registerAddress and saveBridgeRow at that version)
func seedV0005(t *testing.T, dbPath string, rows []v0005Bridge) {
	t.Helper()
	all := migrations.AllForTest()
	i := slices.IndexFunc(all, func(m dbtypes.Migration) bool { return m.ID == "bridgetracker0006" })
	require.Positive(t, i)
	require.NoError(t, aggkitdb.RunMigrations(dbPath, all[:i]))
	sqlDB, err := aggkitdb.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()

	_, err = sqlDB.Exec(`INSERT INTO activity_address
			(from_address, schema_version, updated_at, last_access, scan_state, include_tracking,
			 last_warnings, refreshed, generation)
		 VALUES (?, 1, ?, ?, '{}', 0, NULL, 1, 1)`, upgradeAddress.Hex(), time.Now().Unix(), time.Now().Unix())
	require.NoError(t, err)
	for _, row := range rows {
		data, err := json.Marshal(map[string]any{
			"Bridge": row.bridge, "Claim": nil, "Errors": nil, "Source": domain.ActivitySourceBridgeService,
			"Tracking": nil, "BridgeFinal": true,
		})
		require.NoError(t, err)
		v1Hash := make([]byte, 32)
		v1Hash[0] = 0xff
		_, err = sqlDB.Exec(`INSERT INTO activity_bridge
			(global_index, from_address, schema_version, origin_network_id, destination_network,
			 tx_hash, claim_status, tracker_claim_status, block_timestamp, created_at, updated_at, data,
			 revision_hash)
		 VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			string(row.bridge.GlobalIndex), upgradeAddress.Hex(), row.network, row.bridge.DestinationNetwork,
			string(row.bridge.TxHash), types.ClaimStatusUnclaimed.String(), row.status.String(),
			int64(row.bridge.BlockTimestamp), row.created.UnixNano(), row.created.UnixNano(), data, v1Hash)
		require.NoError(t, err)
	}
}

// TestUpgradeFrom0005KeepsEveryRow writes activity in the pre-0006 shape, then opens it with the
// current store, which runs bridgetracker0006 to 0008 and backfills the revision hashes, and reads
// it through the HTTP handler: every row is still served, in the documented order (global indexes
// above 2^64 included), with the real total, filtered and paged in SQL with gi_sort filled in,
// deposit_count backfilled and the revision of the current scheme
func TestUpgradeFrom0005KeepsEveryRow(t *testing.T) {
	const n = 45
	statuses := []types.TrackerClaimStatus{types.TrackerClaimStatusPending, types.TrackerClaimStatusReadyToClaim,
		types.TrackerClaimStatusError}
	base := time.Unix(1_700_000_000, 0)
	rows := make([]v0005Bridge, 0, n)
	for i := range n {
		network := uint32(i % 3)
		b := &bridgeservicetypes.BridgeResponse{
			BlockNum: uint64(i + 1), BlockPos: uint64(i % 2), BlockTimestamp: uint64(base.Unix()) + uint64(i%5),
			TxHash: bridgeservicetypes.Hash(fmt.Sprintf("0x%064x", i+1)), DepositCount: uint32(i),
			OriginNetwork: network, DestinationNetwork: network + 1, Amount: bridgeservicetypes.BigIntString(fmt.Sprint(i)),
			GlobalIndex: bridgeservicetypes.BigIntString(bridgesync.GenerateGlobalIndexForNetworkID(network, uint32(i)).String()),
		}
		rows = append(rows, v0005Bridge{
			network: network, bridge: b, status: statuses[i%len(statuses)],
			created: time.Unix(int64(b.BlockTimestamp), 0), // ties in groups: the global index breaks them
		})
	}
	dbPath := path.Join(t.TempDir(), "bridgetracker.sqlite")
	seedV0005(t, dbPath, rows)

	logger := log.WithFields("module", "upgrade_test")
	store, err := db.NewSQLiteActivityStore(dbPath, mocks.NewActivityBridgeScanner(t), mocks.NewActivityClaimChecker(t),
		bridgetracker.NewMemoryRegistry(10), nil, logger)
	require.NoError(t, err)
	closer, ok := store.(interface{ Close() error })
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, closer.Close()) })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	api.NewAPI(logger, "sha1", bridgetracker.NewMemoryRegistry(10), store, nil, 0, 0, time.Second,
		aggkitcommon.CORSConfig{}, nil).RegisterRoutes(router)
	get := func(query string) api.ActivityResponse {
		t.Helper()
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
			api.TrackerV1Prefix+"/activity/from/"+upgradeAddress.Hex()+"?"+query, nil))
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		var body api.ActivityResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
		return body
	}

	// the documented order, and the current-scheme revision, of every row
	want := slices.Clone(rows)
	slices.SortFunc(want, func(a, b v0005Bridge) int {
		if c := b.created.Compare(a.created); c != 0 {
			return c
		}
		return b.bridge.GlobalIndex.ToBigInt().Cmp(a.bridge.GlobalIndex.ToBigInt())
	})
	var revision domain.ActivityRevision
	for _, row := range rows {
		revision = revision.Add(domain.HashActivityEntry(&domain.ActivityEntry{
			Bridge: row.bridge, BridgeNetworkID: row.network, TrackerClaimStatus: row.status,
		}))
	}

	type key struct{ network, depositCount uint32 }
	var got []key
	for page := 1; ; page++ {
		body := get(fmt.Sprintf("page_number=%d&page_size=10", page))
		require.Equal(t, n, body.Count)
		require.Equal(t, revision.String(), body.Revision, "the revision of the current scheme")
		if len(body.Bridges) == 0 {
			break
		}
		for _, item := range body.Bridges {
			got = append(got, key{item.BridgeNetworkID, item.Bridge.DepositCount})
		}
	}
	wantKeys := make([]key, 0, n)
	for _, row := range want {
		wantKeys = append(wantKeys, key{row.network, row.bridge.DepositCount})
	}
	require.Equal(t, wantKeys, got, "every row survives the upgrade, in order")

	pending := get("filterBridges=pending&page_size=200")
	require.Equal(t, n/len(statuses), pending.Count)
	for _, item := range pending.Bridges {
		require.Equal(t, "pending", item.ClaimStatus)
	}

	sqlDB, err := aggkitdb.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	dbRows, err := sqlDB.Query("SELECT global_index, gi_sort, deposit_count, length(revision_hash) FROM activity_bridge")
	require.NoError(t, err)
	defer dbRows.Close()
	seen := 0
	for dbRows.Next() {
		var gi, giSort string
		var depositCount, hashLen int
		require.NoError(t, dbRows.Scan(&gi, &giSort, &depositCount, &hashLen))
		require.Equal(t, domain.GlobalIndexSortKey(bridgeservicetypes.BigIntString(gi)), giSort)
		giInt, ok := new(big.Int).SetString(gi, 10)
		require.True(t, ok)
		require.Equal(t, uint64(depositCount), new(big.Int).And(giInt, big.NewInt(0xffffffff)).Uint64())
		require.Equal(t, 32, hashLen, "the revision hash is backfilled")
		seen++
	}
	require.NoError(t, dbRows.Err())
	require.Equal(t, n, seen)
}
