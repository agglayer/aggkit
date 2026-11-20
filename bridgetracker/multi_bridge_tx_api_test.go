package bridgetracker_test

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/api"
	trackerdb "github.com/agglayer/aggkit/bridgetracker/db"
	domainmocks "github.com/agglayer/aggkit/bridgetracker/domain/mocks"
	finalitymocks "github.com/agglayer/aggkit/bridgetracker/finality/mocks"
	"github.com/agglayer/aggkit/bridgetracker/mocks"
	"github.com/agglayer/aggkit/bridgetracker/sources"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkitdb "github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The tests in this file cover per-event tracking end to end, from the HTTP/WebSocket API
// down to the SQLite registry: the real REST/WS handlers, the real engine, the real SQLite
// registry and the real BridgeEventSource parsing a receipt — only the JSON-RPC client and the
// sources of the later steps are mocks. They speak to the tracker only through HTTP, so the same
// scenario can be run against a tracker without log_index support to show what it got wrong.

const (
	// multiBridgeNetwork is the L2 network the multi-bridge txs of these tests are sent to
	multiBridgeNetwork = uint32(1)
	// multiBridgeBlock is the (final) block the txs of these tests are mined in
	multiBridgeBlock = uint64(4242)
	// multiBridgeBlockTime is the timestamp of multiBridgeBlock
	multiBridgeBlockTime = uint64(1700000000)
)

var (
	// multiBridgeTxHash is a tx performing two canonical bridge calls (e.g. through a router)
	multiBridgeTxHash = common.HexToHash("0x1930000000000000000000000000000000000000000000000000000000000001")
	// multiBridgeBlockHash is the hash of multiBridgeBlock
	multiBridgeBlockHash = common.HexToHash("0xb10c000000000000000000000000000000000000000000000000000000000193")
	// multiBridgeContract is the canonical bridge contract of multiBridgeNetwork
	multiBridgeContract = common.HexToAddress("0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe")
)

// bridgeEvent describes one BridgeEvent log of a receipt
type bridgeEvent struct {
	logIndex           uint
	destinationNetwork uint32
	depositCount       uint32
	amount             int64
	emitter            common.Address
}

// The bridges of the multi-bridge tx under test: an L2->L1 one first (log 3), an L2->L2 one
// second (log 9), with an unrelated log in between (log 5, e.g. the router's own event)
var (
	firstBridge  = bridgeEvent{logIndex: 3, destinationNetwork: 0, depositCount: 7, amount: 100}
	secondBridge = bridgeEvent{logIndex: 9, destinationNetwork: 2, depositCount: 8, amount: 999}
)

// packBridgeEventLog packs ev as the BridgeEvent log the bridge contract emits (by the canonical
// bridge contract unless ev.emitter is set)
func packBridgeEventLog(t *testing.T, ev bridgeEvent) *gethtypes.Log {
	t.Helper()
	bridgeABI, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	require.NoError(t, err)
	event, ok := bridgeABI.Events["BridgeEvent"]
	require.True(t, ok)
	data, err := event.Inputs.Pack(
		uint8(types.BridgeLeafTypeAsset), multiBridgeNetwork, common.HexToAddress("0x20"),
		ev.destinationNetwork, common.HexToAddress("0x30"), big.NewInt(ev.amount), []byte{}, ev.depositCount)
	require.NoError(t, err)
	emitter := multiBridgeContract
	if ev.emitter != (common.Address{}) {
		emitter = ev.emitter
	}
	return &gethtypes.Log{
		Address:     emitter,
		Topics:      []common.Hash{event.ID},
		Data:        data,
		BlockNumber: multiBridgeBlock,
		BlockHash:   multiBridgeBlockHash,
		TxHash:      multiBridgeTxHash,
		Index:       ev.logIndex,
	}
}

// unrelatedLog is a non-BridgeEvent log at index (e.g. an ERC20 Transfer of the router)
func unrelatedLog(index uint) *gethtypes.Log {
	return &gethtypes.Log{
		Address:     common.HexToAddress("0x7070"),
		Topics:      []common.Hash{common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")},
		BlockNumber: multiBridgeBlock,
		BlockHash:   multiBridgeBlockHash,
		TxHash:      multiBridgeTxHash,
		Index:       index,
	}
}

// multiBridgeReceipt is the receipt of the multi-bridge tx: two canonical BridgeEvents (logs 3 and 9)
// and an unrelated log (5)
func multiBridgeReceipt(t *testing.T) []*gethtypes.Log {
	t.Helper()
	return []*gethtypes.Log{packBridgeEventLog(t, firstBridge), unrelatedLog(5), packBridgeEventLog(t, secondBridge)}
}

// trackerUnderTest is the tracker's HTTP API served over a real engine and SQLite registry
type trackerUnderTest struct {
	server   *httptest.Server
	engine   *bridgetracker.Engine
	registry bridgetracker.SupervisedRegistry
	dbPath   string
}

// newTrackerUnderTest serves the tracker API over a SQLite registry at dbPath and an engine
// whose BridgeEventSource reads, through a mocked JSON-RPC client, a final receipt of
// multiBridgeTxHash carrying logs (nil logs: the client must not be called at all). Every bridge
// stays at its first step (WaitingLERUpdate): the origin LER is never updated
func newTrackerUnderTest(t *testing.T, dbPath string, logs []*gethtypes.Log) *trackerUnderTest {
	t.Helper()
	logger := log.WithFields("module", "multi_bridge_tx_api_test")

	client := aggkittypesmocks.NewBaseEthereumClienter(t)
	if logs != nil {
		client.EXPECT().TransactionReceipt(mock.Anything, multiBridgeTxHash).Return(&gethtypes.Receipt{
			Status:      gethtypes.ReceiptStatusSuccessful,
			TxHash:      multiBridgeTxHash,
			BlockHash:   multiBridgeBlockHash,
			BlockNumber: new(big.Int).SetUint64(multiBridgeBlock),
			Logs:        logs,
		}, nil).Maybe()
		client.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
			Return(&aggkittypes.BlockHeader{Number: multiBridgeBlock}, nil).Maybe()
		client.EXPECT().HeaderByHash(mock.Anything, multiBridgeBlockHash).
			Return(&gethtypes.Header{Time: multiBridgeBlockTime}, nil).Maybe()
	}
	bridges, err := sources.NewBridgeEventSource(
		sources.StaticClients{multiBridgeNetwork: client},
		aggkittypes.FinalizedBlock, aggkittypes.FinalizedBlock,
		map[uint32]common.Address{multiBridgeNetwork: multiBridgeContract},
		mocks.NewBridgeAddressResolver(t), // never consulted: the static override covers the network
	)
	require.NoError(t, err)

	registry, err := trackerdb.NewSQLiteRegistry(dbPath, 0, logger, nil)
	require.NoError(t, err)
	t.Cleanup(func() { closeRegistry(t, registry) })

	lers := mocks.NewLERSource(t)
	lers.EXPECT().OriginLER(mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	origin := domainmocks.NewOriginBlockSource(t)
	origin.EXPECT().CanonicalBlockHash(mock.Anything, multiBridgeNetwork, multiBridgeBlock).
		Return(multiBridgeBlockHash, nil).Maybe()
	finality := finalitymocks.NewResolver(t)
	finality.EXPECT().IsFinal(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(false, nil).Maybe()

	engine, err := bridgetracker.NewEngine(bridgetracker.EngineConfig{}, logger, registry, bridgetracker.EngineSources{
		Bridges:           bridges,
		Certificates:      mocks.NewCertificateSource(t),
		GERs:              mocks.NewGERSource(t),
		LERs:              lers,
		ClaimChecker:      mocks.NewClaimChecker(t),
		Claims:            mocks.NewClaimSource(t),
		Settlement:        mocks.NewSettlementSource(t),
		SettlementHistory: domainmocks.NewSettlementHistorySource(t),
		OriginBlocks:      origin,
		Finality:          finality,
		InjectedGERs:      domainmocks.NewInjectedGERChecker(t),
	})
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	api.NewAPI(logger, "", registry, nil, nil, 0, 0, 0, aggkitcommon.CORSConfig{}, nil).RegisterRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return &trackerUnderTest{server: server, engine: engine, registry: registry, dbPath: dbPath}
}

// closeRegistry closes registry's SQLite connection (idempotent)
func closeRegistry(t *testing.T, registry bridgetracker.SupervisedRegistry) {
	t.Helper()
	if closer, ok := registry.(io.Closer); ok {
		_ = closer.Close()
	}
}

// wireTracking is the part of the tracker's TrackingData (api.TrackingData) these tests read
type wireTracking struct {
	TrackingStatus string  `json:"tracking_status"`
	ClaimStatus    string  `json:"claim_status"`
	LogIndex       *uint32 `json:"log_index"`
	BridgeStatus   *struct {
		LogIndex uint32 `json:"log_index"`
		Event    struct {
			DestinationNetwork uint32 `json:"destination_network"`
			DepositCount       uint32 `json:"deposit_count"`
			Amount             string `json:"amount"`
		} `json:"event"`
	} `json:"bridge_status"`
	AllSteps []struct {
		StepName string `json:"step_name"`
	} `json:"all_steps"`
	Error *struct {
		ErrorTypeString string   `json:"error_type_string"`
		Description     []string `json:"description"`
	} `json:"error"`
}

// txQuery renders the query string selecting logIndex (nil: a tx-only request)
func txQuery(logIndex *uint) string {
	if logIndex == nil {
		return ""
	}
	return fmt.Sprintf("?log_index=%d", *logIndex)
}

// logIdx is a helper turning a log index into the *uint txQuery takes
func logIdx(index uint) *uint { return &index }

// get requests the tracker's REST tx-status endpoint for multiBridgeTxHash
func (tr *trackerUnderTest) get(t *testing.T, logIndex *uint) wireTracking {
	t.Helper()
	url := fmt.Sprintf("%s%s/network/%d/tx/%s%s",
		tr.server.URL, api.TrackerV1Prefix, multiBridgeNetwork, multiBridgeTxHash.Hex(), txQuery(logIndex))
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	var tracking wireTracking
	require.NoError(t, json.Unmarshal(body, &tracking), string(body))
	return tracking
}

// subscribe opens the tracker's WebSocket tx-status endpoint for multiBridgeTxHash and returns
// its first "status" message
func (tr *trackerUnderTest) subscribe(t *testing.T, logIndex *uint) (*websocket.Conn, wireTracking) {
	t.Helper()
	url := fmt.Sprintf("ws%s%s/network/%d/tx/%s/ws%s", strings.TrimPrefix(tr.server.URL, "http"),
		api.TrackerV1Prefix, multiBridgeNetwork, multiBridgeTxHash.Hex(), txQuery(logIndex))
	conn, resp, err := websocket.DefaultDialer.DialContext(t.Context(), url, nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	t.Cleanup(func() { conn.Close() })
	var msg struct {
		Type string       `json:"type"`
		Data wireTracking `json:"data"`
	}
	require.NoError(t, conn.ReadJSON(&msg))
	require.Equal(t, types.WSTypeStatus, msg.Type)
	return conn, msg.Data
}

// trackedRows counts the tracked_bridge rows the SQLite registry holds for multiBridgeTxHash
func (tr *trackerUnderTest) trackedRows(t *testing.T) int {
	t.Helper()
	db, err := aggkitdb.NewSQLiteDB(tr.dbPath)
	require.NoError(t, err)
	defer db.Close()
	var rows int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM tracked_bridge WHERE tx_hash = ?",
		multiBridgeTxHash.Hex()).Scan(&rows))
	return rows
}

// requireTracks checks that tracking reports exactly the facts and route of ev: its own
// BridgeEvent (log index, deposit count, destination, amount) and the path its destination
// implies, stuck at the first step
func requireTracks(t *testing.T, ev bridgeEvent, tracking wireTracking) {
	t.Helper()
	require.NotNil(t, tracking.BridgeStatus, "bridge of log %d not resolved: %+v", ev.logIndex, tracking.Error)
	require.Equal(t, ev.depositCount, tracking.BridgeStatus.Event.DepositCount,
		"log %d reports the deposit count of another bridge of the tx", ev.logIndex)
	require.Equal(t, ev.destinationNetwork, tracking.BridgeStatus.Event.DestinationNetwork,
		"log %d reports the destination of another bridge of the tx", ev.logIndex)
	require.Equal(t, fmt.Sprint(ev.amount), tracking.BridgeStatus.Event.Amount)
	require.Equal(t, uint32(ev.logIndex), tracking.BridgeStatus.LogIndex)

	wantSteps := 8 // L2 -> L2: the GER must also be injected in the destination L2
	if ev.destinationNetwork == 0 {
		wantSteps = 7 // L2 -> L1
	}
	require.Len(t, tracking.AllSteps, wantSteps, "log %d reports the route of another bridge of the tx", ev.logIndex)
	require.Equal(t, types.TrackingStatusRunning.String(), tracking.TrackingStatus)
	require.Equal(t, types.StepWaitingLERUpdate.String(), tracking.AllSteps[0].StepName)
	require.Nil(t, tracking.Error)
}

// requirePermanentError checks that tracking is a give-up on resolving the bridge whose
// description is about wantErr and lists the tx's canonical BridgeEvents (logs 3 and 9)
func requirePermanentError(t *testing.T, wantErr string, tracking wireTracking) {
	t.Helper()
	require.Nil(t, tracking.BridgeStatus, "resolved to one of the tx's bridges instead of failing")
	require.NotNil(t, tracking.Error)
	require.Equal(t, types.TrackingStatusError.String(), tracking.TrackingStatus)
	require.Equal(t, types.TrackerClaimStatusError.String(), tracking.ClaimStatus)
	require.Equal(t, types.StepErrorPermanent.String(), tracking.Error.ErrorTypeString)
	require.Len(t, tracking.Error.Description, 1)
	require.Contains(t, tracking.Error.Description[0], wantErr)
	require.Contains(t, tracking.Error.Description[0], "log_index [3 9]")
}

// TestMultiBridgeTxEachBridgeTrackedByLogIndex is the multi-bridge scenario: one tx emits two
// canonical BridgeEvents (plus an unrelated log). Tracking each one by its log_index, over REST
// and over the WebSocket, gives each its own facts, route and progress — the second one is not
// served the first one's — each in its own SQLite row, across a restart too
func TestMultiBridgeTxEachBridgeTrackedByLogIndex(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tracker.sqlite")
	tracker := newTrackerUnderTest(t, dbPath, multiBridgeReceipt(t))

	// first contact registers both bridges, unresolved yet
	for _, ev := range []bridgeEvent{firstBridge, secondBridge} {
		tracking := tracker.get(t, logIdx(ev.logIndex))
		require.Equal(t, types.TrackingStatusRegistered.String(), tracking.TrackingStatus)
		require.Nil(t, tracking.BridgeStatus)
	}
	bridgetracker.TickOnce(t.Context(), tracker.engine)

	first, second := tracker.get(t, logIdx(firstBridge.logIndex)), tracker.get(t, logIdx(secondBridge.logIndex))
	requireTracks(t, firstBridge, first)
	requireTracks(t, secondBridge, second)
	require.Equal(t, 2, tracker.trackedRows(t), "one SQLite row per bridge of the tx")
	// each response names the bridge it is about
	require.NotNil(t, first.LogIndex)
	require.Equal(t, uint32(firstBridge.logIndex), *first.LogIndex)
	require.NotNil(t, second.LogIndex)
	require.Equal(t, uint32(secondBridge.logIndex), *second.LogIndex)

	// the WebSocket serves the same per-bridge snapshot
	_, wsSecond := tracker.subscribe(t, logIdx(secondBridge.logIndex))
	requireTracks(t, secondBridge, wsSecond)
	_, wsFirst := tracker.subscribe(t, logIdx(firstBridge.logIndex))
	requireTracks(t, firstBridge, wsFirst)

	// a restart: each bridge is served from its own row, nothing is resolved again (the
	// JSON-RPC client of the new instance has no expectations: any call fails the test)
	closeRegistry(t, tracker.registry)
	restarted := newTrackerUnderTest(t, dbPath, nil)
	requireTracks(t, secondBridge, restarted.get(t, logIdx(secondBridge.logIndex)))
	requireTracks(t, firstBridge, restarted.get(t, logIdx(firstBridge.logIndex)))
	require.Equal(t, 2, restarted.trackedRows(t))
}

// TestMultiBridgeTxTxOnlyRequestIsAmbiguous: a request with no log_index on a tx that created
// several bridges fails permanently, naming the candidate log indexes, instead of reporting the
// first bridge for all of them. Over the WebSocket the final status is followed by a normal
// closure. The tx's bridges can still be tracked one by one afterwards
func TestMultiBridgeTxTxOnlyRequestIsAmbiguous(t *testing.T) {
	tracker := newTrackerUnderTest(t, filepath.Join(t.TempDir(), "tracker.sqlite"), multiBridgeReceipt(t))

	tracker.get(t, nil)
	bridgetracker.TickOnce(t.Context(), tracker.engine)
	txOnly := tracker.get(t, nil)
	requirePermanentError(t, "select one by log_index", txOnly)
	require.Nil(t, txOnly.LogIndex, "a tx-only response has no log_index")

	conn, wsTxOnly := tracker.subscribe(t, nil)
	requirePermanentError(t, "select one by log_index", wsTxOnly)
	_, _, err := conn.ReadMessage()
	require.True(t, websocket.IsCloseError(err, websocket.CloseNormalClosure), "got %v", err)

	tracker.get(t, logIdx(secondBridge.logIndex))
	bridgetracker.TickOnce(t.Context(), tracker.engine)
	requireTracks(t, secondBridge, tracker.get(t, logIdx(secondBridge.logIndex)))
	requirePermanentError(t, "select one by log_index", tracker.get(t, nil))
}

// TestMultiBridgeTxUnknownLogIndex: a log_index holding no canonical BridgeEvent — no log at
// all, an unrelated log, or a BridgeEvent-shaped log of another contract — fails permanently,
// naming the log indexes that do hold one, instead of reporting another bridge of the tx
func TestMultiBridgeTxUnknownLogIndex(t *testing.T) {
	impostor := bridgeEvent{
		logIndex: 11, destinationNetwork: 0, depositCount: 7, amount: 1, emitter: common.HexToAddress("0xbad"),
	}
	for _, index := range []uint{4, 5, 11} {
		t.Run(fmt.Sprintf("log_index=%d", index), func(t *testing.T) {
			logs := append(multiBridgeReceipt(t), packBridgeEventLog(t, impostor))
			tracker := newTrackerUnderTest(t, filepath.Join(t.TempDir(), "tracker.sqlite"), logs)

			tracker.get(t, logIdx(index))
			bridgetracker.TickOnce(t.Context(), tracker.engine)
			requirePermanentError(t, "no BridgeEvent at the selected log_index", tracker.get(t, logIdx(index)))
		})
	}
}

// TestSingleBridgeTxUnchanged pins backwards compatibility: a tx that created a single bridge
// is tracked as before with no log_index — response without log_index — and selecting its log
// tracks the same bridge
func TestSingleBridgeTxUnchanged(t *testing.T) {
	tracker := newTrackerUnderTest(t, filepath.Join(t.TempDir(), "tracker.sqlite"),
		[]*gethtypes.Log{unrelatedLog(1), packBridgeEventLog(t, firstBridge)})

	tracker.get(t, nil)
	tracker.get(t, logIdx(firstBridge.logIndex))
	bridgetracker.TickOnce(t.Context(), tracker.engine)

	txOnly := tracker.get(t, nil)
	requireTracks(t, firstBridge, txOnly)
	require.Nil(t, txOnly.LogIndex, "a tx-only response keeps its pre-existing shape")
	requireTracks(t, firstBridge, tracker.get(t, logIdx(firstBridge.logIndex)))

	_, wsTxOnly := tracker.subscribe(t, nil)
	requireTracks(t, firstBridge, wsTxOnly)
}
