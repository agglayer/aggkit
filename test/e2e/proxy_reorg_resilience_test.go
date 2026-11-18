package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

const (
	// proxy002ConfigFile is the config of aggkit-proxy-002, relative to the env's config directory.
	proxy002ConfigFile = "aggkit-proxy-noautoreg.toml"
	// proxy002BridgeURLOfAggkit001 is the bridge service URL that aggkit-proxy-002's config gives, as is,
	// to networks 0 and 1 (both served by aggkit-001).
	proxy002BridgeURLOfAggkit001 = `"http://aggkit-001:5577"`
	// proxy002TrackerSection is the header of the [Tracker] section of that config.
	proxy002TrackerSection = "\n[Tracker]\n"
	// proxy002DBPath is where the test makes aggkit-proxy-002 keep its SQLite store: inside the container
	// (its home directory), so nothing is written to the host.
	proxy002DBPath = "/home/appuser/bridgetracker.sqlite"

	// interposerHostAlias is the name the aggkit-proxy-002 container resolves to the host (extra_hosts in
	// docker-compose.yml).
	interposerHostAlias = "host.docker.internal"
	// bridgeServicePathPrefix is the prefix of the bridge service API the interposer acts on; the health
	// check ("/") always goes through.
	bridgeServicePathPrefix = "/bridge/v1/"
	// blockFinalizedField is the field the interposer removes in interposerStripFinality mode.
	blockFinalizedField = "block_finalized"

	// burstRounds is how many bridges the burst scenario sends, each followed by a reorg decision.
	burstRounds = 9
)

// interposerMode is what the bridge-service interposer does with the requests of the aggkit-proxy.
type interposerMode int32

const (
	// interposerPassThrough forwards requests and answers untouched.
	interposerPassThrough interposerMode = iota
	// interposerUnavailable answers 503 to every bridge API request, as a bridge service does while it
	// resolves a reorg.
	interposerUnavailable
	// interposerStripFinality forwards requests and removes block_finalized from the answers, as a bridge
	// service that cannot tell (or older than the field) would answer.
	interposerStripFinality
)

// bridgeServiceInterposer is a reverse proxy the test runs on the host between aggkit-proxy-002 and the
// bridge service of aggkit-001, to inject the two conditions that cannot be produced on demand from the
// real service: a 503 window and answers without block_finalized. It only alters the transport (status
// code, one JSON field); every answer that goes through is aggkit's own.
type bridgeServiceInterposer struct {
	listener net.Listener
	server   *http.Server
	mode     atomic.Int32
	// rejected counts the requests answered 503, stripped the block_finalized fields removed
	rejected atomic.Int64
	stripped atomic.Int64
}

// startBridgeServiceInterposer starts the interposer in front of the bridge service at targetURL, on a
// free port of every interface (the container reaches it through the docker host gateway). It is stopped
// when the test ends.
func startBridgeServiceInterposer(t *testing.T, targetURL string) *bridgeServiceInterposer {
	t.Helper()
	target, err := url.Parse(targetURL)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)

	i := &bridgeServiceInterposer{listener: listener}
	forward := httputil.NewSingleHostReverseProxy(target)
	forward.ModifyResponse = i.modifyResponse
	i.server = &http.Server{
		ReadHeaderTimeout: client.DefaultTimeout,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if interposerMode(i.mode.Load()) == interposerUnavailable && strings.HasPrefix(r.URL.Path, bridgeServicePathPrefix) {
				i.rejected.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":"injected by the e2e test: bridge service unavailable"}`))
				return
			}
			forward.ServeHTTP(w, r)
		}),
	}
	go func() { _ = i.server.Serve(listener) }()
	t.Cleanup(func() { _ = i.server.Close() })
	return i
}

// setMode switches what the interposer does from the next request on.
func (i *bridgeServiceInterposer) setMode(mode interposerMode) {
	i.mode.Store(int32(mode))
}

// urlFromContainer is the URL of the interposer as the aggkit-proxy-002 container reaches it.
func (i *bridgeServiceInterposer) urlFromContainer() string {
	port := i.listener.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert // a TCP listener
	return "http://" + net.JoinHostPort(interposerHostAlias, strconv.Itoa(port))
}

// modifyResponse removes every block_finalized field of a JSON answer in interposerStripFinality mode.
func (i *bridgeServiceInterposer) modifyResponse(resp *http.Response) error {
	if interposerMode(i.mode.Load()) != interposerStripFinality ||
		!strings.HasPrefix(resp.Request.URL.Path, bridgeServicePathPrefix) || resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber() // numbers go through untouched
	var decoded any
	if err := decoder.Decode(&decoded); err == nil {
		i.stripped.Add(int64(removeJSONField(decoded, blockFinalizedField)))
		if body, err = json.Marshal(decoded); err != nil {
			return err
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return nil
}

// removeJSONField deletes field from every object of a decoded JSON value and returns how many it removed.
func removeJSONField(value any, field string) int {
	removed := 0
	switch typed := value.(type) {
	case map[string]any:
		if _, ok := typed[field]; ok {
			delete(typed, field)
			removed++
		}
		for _, nested := range typed {
			removed += removeJSONField(nested, field)
		}
	case []any:
		for _, nested := range typed {
			removed += removeJSONField(nested, field)
		}
	}
	return removed
}

// reconfigureProxy002 rewrites the config of aggkit-proxy-002 so that it reaches the bridge service of
// aggkit-001 (networks 0 and 1) through the interposer and keeps its state in SQLite (DBPath), restarts it
// and waits for it. The original config is restored, and the proxy restarted, when the test ends.
func reconfigureProxy002(ctx context.Context, t *testing.T, interposer *bridgeServiceInterposer) {
	t.Helper()
	configPath := filepath.Join(filepath.Dir(testEnv.GetAggkitProxyConfigPath()), proxy002ConfigFile)
	original, err := os.ReadFile(configPath)
	require.NoError(t, err)
	const networksOfAggkit001 = 2
	require.Equal(t, networksOfAggkit001, strings.Count(string(original), proxy002BridgeURLOfAggkit001),
		"the config of aggkit-proxy-002 must give the bridge service of aggkit-001 to networks 0 and 1")
	require.Equal(t, 1, strings.Count(string(original), proxy002TrackerSection))
	patched := strings.ReplaceAll(string(original), proxy002BridgeURLOfAggkit001, `"`+interposer.urlFromContainer()+`"`)
	patched = strings.Replace(patched, proxy002TrackerSection,
		proxy002TrackerSection+fmt.Sprintf("DBPath = %q\n", proxy002DBPath), 1)

	t.Cleanup(func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), proxyReorgConvergeTimeout)
		defer cancel()
		if err := os.WriteFile(configPath, original, 0o600); err != nil {
			t.Errorf("restore the config of %s: %v", aggkitProxy002Service, err)
			return
		}
		// "start" first: a no-op on a running service, and it brings back one a failed scenario left stopped
		if err := testEnv.StartService(restoreCtx, aggkitProxy002Service); err != nil {
			t.Errorf("start %s: %v", aggkitProxy002Service, err)
		}
		if err := testEnv.RestartService(restoreCtx, aggkitProxy002Service); err != nil {
			t.Errorf("restart %s with its original config: %v", aggkitProxy002Service, err)
			return
		}
		if err := waitForProxyReady(restoreCtx, proxyReorgProxy2URL); err != nil {
			t.Errorf("%s did not come back with its original config: %v", aggkitProxy002Service, err)
		}
	})
	require.NoError(t, os.WriteFile(configPath, []byte(patched), 0o600))
	require.NoError(t, testEnv.RestartService(ctx, aggkitProxy002Service))
	require.NoError(t, waitForProxyReady(ctx, proxyReorgProxy2URL))
}

// TestProxyReorgResilience covers the resilience scenarios of agglayer/aggkit#1972 (F1-F4).
//
// F1-F3 run against aggkit-proxy-002, reconfigured for the test: it keeps its state in SQLite (DBPath) and
// reaches the bridge service of aggkit-001 through bridgeServiceInterposer. aggkit-proxy-001 is left as it
// is and serves as the control (it talks to aggkit directly, with the in-memory store). F4 runs against
// aggkit-proxy-001.
func TestProxyReorgResilience(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	l2 := NewAnvilChain("L2", testEnv.L2.Client)
	requireProxyReorgEnv(ctx, t, l2)
	holdAggsender(ctx, t, l2)
	interposer := startBridgeServiceInterposer(t, testEnv.L2.BridgeServiceURL)
	reconfigureProxy002(ctx, t, interposer)

	// listedOnProxy2 waits until aggkit-proxy-002 lists the scenario's bridge at the block of receipt, from
	// the bridge service.
	listedOnProxy2 := func(t *testing.T, s *l2ActivityScenario, receipt *ethtypes.Receipt) *proxyActivityResponse {
		t.Helper()
		activity, err := waitForActivity(ctx, proxyReorgProxy2URL, s.sender.addr, "the bridge is listed by the second proxy",
			proxyReorgConvergeTimeout, func(a *proxyActivityResponse) bool {
				item := a.find(s.networkID, s.depositCount)
				return a.Count == 1 && itemMatchesReceipt(item, receipt) && item.Source == activitySourceBridge
			})
		require.NoError(t, err)
		requireActivityWellFormed(t, activity)
		return activity
	}

	t.Run("F1 bridge service answers 503 during the reorg", func(t *testing.T) {
		interposer.setMode(interposerPassThrough)
		s := startL2ActivityScenario(ctx, t, l2, &testEnv.L2)
		before := listedOnProxy2(t, s, s.receipt)

		// from here on the proxy gets 503 from the bridge service, while aggkit resolves the reorg
		interposer.setMode(interposerUnavailable)
		rejectedBefore := interposer.rejected.Load()
		reorgDropping(ctx, t, l2, testEnv.L2.Client, s.receipt.TxHash)
		s.requireReorgDetected(ctx, t)
		require.NoError(t, WaitForBridgeAbsent(ctx, s.bs, s.networkID, s.depositCount, proxyReorgServeTimeout))
		// the control: the proxy that reaches aggkit directly already forgot the bridge
		s.waitForActivity(ctx, t, "the first proxy forgot the bridge", func(a *proxyActivityResponse) bool { return a.Count == 0 })

		// the second proxy refreshed against the 503 (it reports the warning) and kept what it had
		during, err := waitForActivity(ctx, proxyReorgProxy2URL, s.sender.addr, "a warning for the unavailable network",
			proxyReorgConvergeTimeout, func(a *proxyActivityResponse) bool { return a.warningFor(s.networkID) != nil })
		require.NoError(t, err)
		requireActivityWellFormed(t, during)
		require.Greater(t, interposer.rejected.Load(), rejectedBefore, "the proxy must have been answered 503")
		log.Infof("F1: warning while the bridge service answers 503: %q", during.warningFor(s.networkID).Message)
		require.Equal(t, 1, during.Count, "a 503 must not be read as the bridge being gone: %s", during)
		require.True(t, itemMatchesReceipt(during.find(s.networkID, s.depositCount), s.receipt), "the entry must be kept as it was")
		require.Equal(t, before.Revision, during.Revision, "nothing changed: same revision")

		// the bridge service answers again: the proxy converges to the chain
		interposer.setMode(interposerPassThrough)
		after, err := waitForActivity(ctx, proxyReorgProxy2URL, s.sender.addr, "the bridge is gone once the bridge service answers",
			proxyReorgConvergeTimeout, func(a *proxyActivityResponse) bool {
				return a.Count == 0 && a.warningFor(s.networkID) == nil
			})
		require.NoError(t, err)
		require.Equal(t, emptyActivityRevision, after.Revision)
	})

	t.Run("F2 proxy with DBPath restarted, reorgs while it is down", func(t *testing.T) {
		interposer.setMode(interposerPassThrough)
		s := startL2ActivityScenario(ctx, t, l2, &testEnv.L2)
		listedOnProxy2(t, s, s.receipt)
		resolvedAt := func(block uint64) func(*trackerTrackingData) bool {
			return func(d *trackerTrackingData) bool { return d.BridgeStatus != nil && d.BridgeStatus.BlockNumber == block }
		}
		waitForTracking(ctx, t, proxyReorgProxy2URL, s.networkID, s.tx.Hash(), "resolved at the bridge block",
			resolvedAt(s.receipt.BlockNumber.Uint64()))

		// down, with the non-final bridge persisted
		require.NoError(t, testEnv.StopService(ctx, aggkitProxy002Service))
		persistedBridges, persistedTrackings := persistedState(ctx, t, s.sender.addr, s.tx.Hash())
		require.Equal(t, 1, persistedBridges,
			"the stopped proxy must hold the non-final bridge in its SQLite store: without it the scenario proves nothing")
		require.Equal(t, 1, persistedTrackings, "the stopped proxy must hold the tracking in its SQLite store")
		moved := reorgMoving(ctx, t, l2, testEnv.L2.Client, s.receipt.TxHash, 1)
		s.requireReorgDetected(ctx, t)
		requireBridgeServed(ctx, t, s.bs, s.networkID, s.depositCount, moved, false)

		// back: the persisted entry and tracking are corrected, not served stale nor duplicated
		require.NoError(t, testEnv.StartService(ctx, aggkitProxy002Service))
		require.NoError(t, waitForProxyReady(ctx, proxyReorgProxy2URL))
		// the address is known from the store: the first request is answered at once, not with the 503 of
		// an address registered from scratch
		_, err := fetchActivity(ctx, proxyReorgProxy2URL, s.sender.addr)
		require.NoError(t, err, "the restarted proxy must know the address from its SQLite store")
		corrected := listedOnProxy2(t, s, moved)
		control := s.waitForActivity(ctx, t, "the first proxy lists the bridge at its new block", func(a *proxyActivityResponse) bool {
			return itemMatchesReceipt(a.find(s.networkID, s.depositCount), moved)
		})
		require.Equal(t, control.Revision, corrected.Revision, "same data as the proxy that was never down, same revision")
		waitForTracking(ctx, t, proxyReorgProxy2URL, s.networkID, s.tx.Hash(), "resolved again at the new block",
			resolvedAt(moved.BlockNumber.Uint64()))

		// down again, and this time the bridge is reorged out for good
		require.NoError(t, l2.Mine(ctx, 1)) // the bridge block is head-1 again, still not final
		requireNonFinal(ctx, t, l2, moved.BlockNumber.Uint64())
		require.NoError(t, testEnv.StopService(ctx, aggkitProxy002Service))
		persistedBridges, _ = persistedState(ctx, t, s.sender.addr, s.tx.Hash())
		require.Equal(t, 1, persistedBridges)
		reorgDropping(ctx, t, l2, testEnv.L2.Client, s.receipt.TxHash)
		s.requireReorgDetected(ctx, t)
		require.NoError(t, WaitForBridgeAbsent(ctx, s.bs, s.networkID, s.depositCount, proxyReorgServeTimeout))

		require.NoError(t, testEnv.StartService(ctx, aggkitProxy002Service))
		require.NoError(t, waitForProxyReady(ctx, proxyReorgProxy2URL))
		_, err = fetchActivity(ctx, proxyReorgProxy2URL, s.sender.addr)
		require.NoError(t, err, "the restarted proxy must know the address from its SQLite store")
		gone, err := waitForActivity(ctx, proxyReorgProxy2URL, s.sender.addr, "the persisted bridge is removed on start",
			proxyReorgConvergeTimeout, func(a *proxyActivityResponse) bool { return a.Count == 0 })
		require.NoError(t, err)
		require.Equal(t, emptyActivityRevision, gone.Revision)
		// NOTE: the tracking of a reorged-out origin tx ends in a terminal error after UnresolvedTimeout
		// (open decision (c) of #1986: the assertion follows the current behaviour)
		tracking := waitForTracking(ctx, t, proxyReorgProxy2URL, s.networkID, s.tx.Hash(), "discarded: not running anymore",
			func(d *trackerTrackingData) bool { return d.TrackingStatus == trackingStatusError })
		require.Nil(t, tracking.BridgeStatus)
	})

	t.Run("F3 bridge service omits block_finalized", func(t *testing.T) {
		// every answer the second proxy gets lacks block_finalized: it has to work finality out by itself
		interposer.setMode(interposerStripFinality)
		t.Cleanup(func() { interposer.setMode(interposerPassThrough) })
		strippedBefore := interposer.stripped.Load()
		s := startL2ActivityScenario(ctx, t, l2, &testEnv.L2)
		before := listedOnProxy2(t, s, s.receipt)
		require.Greater(t, interposer.stripped.Load(), strippedBefore, "the interposer must have removed block_finalized")
		item := before.find(s.networkID, s.depositCount)
		require.Nil(t, item.Bridge.BlockFinalized, "the proxy must not have received block_finalized: %s", item)
		require.False(t, item.isFinalized(), "a bridge of a non-final block must not be final: %s", item)

		_, err := l2.MineUntilFinalized(ctx, s.receipt.BlockNumber.Uint64())
		require.NoError(t, err)
		requireBridgeServed(ctx, t, s.bs, s.networkID, s.depositCount, s.receipt, true) // aggkit itself, asked directly
		final, err := waitForActivity(ctx, proxyReorgProxy2URL, s.sender.addr, "the bridge is final by the receipt fallback",
			proxyReorgConvergeTimeout, func(a *proxyActivityResponse) bool {
				item := a.find(s.networkID, s.depositCount)
				return itemMatchesReceipt(item, s.receipt) && item.isFinalized()
			})
		require.NoError(t, err)
		require.Nil(t, final.find(s.networkID, s.depositCount).Bridge.BlockFinalized,
			"finality must have been resolved without block_finalized from the bridge service")
	})

	t.Run("F4 burst of reorgs with bridges in flight", func(t *testing.T) {
		interposer.setMode(interposerPassThrough)
		sender := newProxyReorgSender(t)
		bridger := newL2Bridger(ctx, t, &testEnv.L2, sender)
		bs := testEnv.L2.BridgeService
		networkID := testEnv.L2.NetworkID
		// the address is registered with the proxy BEFORE the burst, so that its background refresh runs
		// while the bridges come and go, and has stale entries to correct
		empty, err := waitForActivity(ctx, proxyTrackerBaseURL, sender.addr, "empty before the burst",
			proxyReorgConvergeTimeout, func(a *proxyActivityResponse) bool { return a.Count == 0 })
		require.NoError(t, err)
		require.Equal(t, emptyActivityRevision, empty.Revision)
		l2.PauseMiningForTest(ctx, t)

		// Each round sends a bridge, mines it with a block on top and, without waiting for aggkit or the
		// proxy, reorgs it out, moves it to the next block or leaves it. A dropped bridge frees its nonce
		// and its deposit count for the bridge of the next round. Amounts differ so that no two
		// transactions are the same.
		var sent []*ethtypes.Transaction
		for round := 0; round < burstRounds; round++ {
			tx := bridger.send(t, proxyReorgBridgeAmount+int64(round))
			sent = append(sent, tx)
			mineTxPlusOne(ctx, t, l2, testEnv.L2.Client, tx)
			switch round % 3 { // three outcomes, in turn
			case 0:
				reorgDropping(ctx, t, l2, testEnv.L2.Client, tx.Hash())
			case 1:
				reorgMoving(ctx, t, l2, testEnv.L2.Client, tx.Hash(), 1)
			default: // left where it was mined
			}
		}
		// The last reorg follows the deterministic recipe and must be reported by itself: first aggkit
		// catches up with the burst (it serves exactly what the chain holds, so it has no reorg left to
		// resolve), and the baseline is read again.
		require.NoError(t, WaitForBridgeSynced(ctx, bs, networkID, proxyReorgServeTimeout))
		require.NoError(t, waitForServedBridgesOf(ctx, bs, networkID, sender.addr, bridgesOnChain(ctx, t, bridger, sent)),
			"aggkit does not serve what the chain holds after the burst: a syncer problem, not a proxy one")
		baseline := reorgBaseline(ctx, t, bs, networkID)
		last := bridger.send(t, proxyReorgBridgeAmount+burstRounds)
		sent = append(sent, last)
		lastReceipt := mineTxPlusOne(ctx, t, l2, testEnv.L2.Client, last)
		requireBridgeServed(ctx, t, bs, networkID, bridger.depositCount(t, lastReceipt), lastReceipt, false)
		lastReceipt = reorgMoving(ctx, t, l2, testEnv.L2.Client, last.Hash(), 1)
		requireReorgDetected(ctx, t, bs, networkID, baseline)
		_, err = l2.MineUntilFinalized(ctx, lastReceipt.BlockNumber.Uint64())
		require.NoError(t, err)

		// what the chain holds: the bridges whose transaction has a receipt, by deposit count
		onChain := bridgesOnChain(ctx, t, bridger, sent)
		require.NotEmpty(t, onChain)
		require.Less(t, len(onChain), len(sent), "the burst must have dropped some bridges")
		log.Infof("F4: %d bridges sent, %d on chain after the burst", len(sent), len(onChain))

		// aggkit first: its bridge service must serve exactly those bridges for the sender
		require.NoError(t, WaitForBridgeSynced(ctx, bs, networkID, proxyReorgServeTimeout))
		require.NoError(t, waitForServedBridgesOf(ctx, bs, networkID, sender.addr, onChain),
			"aggkit does not serve what the chain holds after the burst: a syncer problem, not a proxy one")

		// then the proxy: the same set, all final, with no duplicate and no orphan entry
		final, err := waitForActivity(ctx, proxyTrackerBaseURL, sender.addr, "exactly the bridges on chain, all final",
			proxyReorgConvergeTimeout, func(a *proxyActivityResponse) bool {
				if a.Count != len(onChain) {
					return false
				}
				for depositCount, receipt := range onChain {
					item := a.find(networkID, depositCount)
					if !itemMatchesReceipt(item, receipt) || !item.isFinalized() || item.Source != activitySourceBridge {
						return false
					}
				}
				return true
			})
		require.NoError(t, err)
		requireActivityWellFormed(t, final)
		require.Len(t, final.Bridges, len(onChain))
	})
}

// persistedState returns how many bridges of from, and how many trackings of txHash, the STOPPED
// aggkit-proxy-002 holds in its SQLite store, read from a copy of the database file taken out of the
// container.
func persistedState(ctx context.Context, t *testing.T, from common.Address, txHash common.Hash) (bridges, trackings int) {
	t.Helper()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, filepath.Base(proxy002DBPath))
	require.NoError(t, testEnv.CopyFromService(ctx, aggkitProxy002Service, proxy002DBPath, dbFile))
	// the write-ahead log, if the proxy left one behind
	_ = testEnv.CopyFromService(ctx, aggkitProxy002Service, proxy002DBPath+"-wal", dbFile+"-wal")
	db, err := sql.Open("sqlite3", dbFile)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM activity_bridge WHERE LOWER(from_address) = LOWER(?)", from.Hex()).Scan(&bridges))
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tracked_bridge WHERE LOWER(tx_hash) = LOWER(?)", txHash.Hex()).Scan(&trackings))
	return bridges, trackings
}

// bridgesOnChain returns, by deposit count, the receipts of the transactions of sent that are on chain now
// (the others were reorged out).
func bridgesOnChain(
	ctx context.Context, t *testing.T, bridger *l2Bridger, sent []*ethtypes.Transaction,
) map[uint32]*ethtypes.Receipt {
	t.Helper()
	onChain := make(map[uint32]*ethtypes.Receipt)
	for _, tx := range sent {
		receipt, err := bridger.l2.Client.TransactionReceipt(ctx, tx.Hash())
		if err != nil {
			require.ErrorIs(t, err, ethereum.NotFound)
			continue // reorged out
		}
		depositCount := bridger.depositCount(t, receipt)
		require.NotContains(t, onChain, depositCount, "two transactions on chain with the same deposit count")
		onChain[depositCount] = receipt
	}
	return onChain
}

// waitForServedBridgesOf waits until the bridge service serves, for the sender from, exactly the bridges of
// want (deposit count -> receipt of the bridge transaction).
func waitForServedBridgesOf(
	ctx context.Context, bs *client.Client, networkID uint32, from common.Address, want map[uint32]*ethtypes.Receipt,
) error {
	state := "not polled yet"
	fromAddress := from.Hex()
	pageSize := listServedBridgesPageSize
	return waitUntil(ctx, proxyReorgServeTimeout, fmt.Sprintf("aggkit to serve the %d bridges of %s", len(want), fromAddress),
		func() (bool, error) {
			result, err := bs.GetBridges(ctx, client.GetBridgesParams{NetworkID: networkID, FromAddress: &fromAddress, PageSize: &pageSize})
			if err != nil {
				state = err.Error()
				return false, nil
			}
			state = fmt.Sprintf("%d bridges served", len(result.Bridges))
			if result.Count != len(want) || len(result.Bridges) != len(want) {
				return false, nil
			}
			for _, bridge := range result.Bridges {
				receipt, ok := want[bridge.DepositCount]
				if !ok || common.HexToHash(string(bridge.TxHash)) != receipt.TxHash || bridge.BlockNum != receipt.BlockNumber.Uint64() {
					state = fmt.Sprintf("bridge deposit_count=%d tx=%s block=%d is not on chain like that",
						bridge.DepositCount, bridge.TxHash, bridge.BlockNum)
					return false, nil
				}
			}
			return true, nil
		}, func() string { return state })
}
