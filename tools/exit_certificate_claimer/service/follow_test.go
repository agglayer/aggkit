package claimer

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive/archivetest"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

const (
	followBaseURI   = "gs://let-archive/runs"
	followNetworkID = uint32(1)
)

var followDest = common.HexToAddress("0xd1")

// followL1 is the L1 Info Tree of the follow tests: it knows the local exit root settled by each run (by
// its settlement GER) and can be told to fail for a run.
type followL1 struct {
	mu    sync.Mutex
	roots map[common.Hash]common.Hash
	fail  map[common.Hash]error
	opens int
	// onOpen, when set, runs on every open (before its result), e.g. to stop the claimer mid-prepare.
	onOpen func()
}

func (l *followL1) open(_ context.Context, ger common.Hash) (L1InfoTreeQuerier, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.opens++
	if l.onOpen != nil {
		l.onOpen()
	}
	if err := l.fail[ger]; err != nil {
		return nil, err
	}
	root, ok := l.roots[ger]
	if !ok {
		return nil, fmt.Errorf("GER %s not settled", ger.Hex())
	}
	return &fakeL1{
		leaf:      &l1infotreesync.L1InfoTreeLeaf{L1InfoTreeIndex: 9},
		localRoot: root,
	}, nil
}

func (l *followL1) setFail(ger common.Hash, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err == nil {
		delete(l.fail, ger)
		return
	}
	l.fail[ger] = err
}

// followEnv is an archive (in memory) with runs of one network, and the L1 that settled them.
type followEnv struct {
	t    *testing.T
	objs *archivetest.Objects
	base runarchive.Location
	l1   *followL1
	// gers holds the settlement GER of each archived run, by the height in its certificate.
	gers map[uint64]common.Hash
}

func newFollowEnv(t *testing.T) *followEnv {
	t.Helper()
	base, err := runarchive.ParseURI(followBaseURI)
	require.NoError(t, err)
	return &followEnv{
		t: t, objs: &archivetest.Objects{}, base: base,
		l1:   &followL1{roots: map[common.Hash]common.Hash{}, fail: map[common.Hash]error{}},
		gers: map[uint64]common.Hash{},
	}
}

// archive archives, at the prefix of height, the run of a certificate with that height: a delta chain where
// the run of height h holds h+1 exits to followDest.
func (e *followEnv) archive(height uint64) runarchive.Location {
	e.t.Helper()
	return e.archiveAt(height, height)
}

// archiveAt archives at the prefix of prefixHeight a run whose certificate says certHeight.
func (e *followEnv) archiveAt(prefixHeight, certHeight uint64) runarchive.Location {
	e.t.Helper()
	t := e.t
	dir := t.TempDir()
	token := common.HexToAddress("0xa1")
	leaves := make([]bridgesyncerlite.BridgeLeaf, certHeight+1)
	exits := make([]*agglayertypes.BridgeExit, len(leaves))
	for i := range leaves {
		leaves[i] = bridgesyncerlite.BridgeLeaf{
			BlockNum: uint64(10 + i), LeafType: 0, OriginNetwork: 0, OriginAddress: token, DestinationNetwork: 0,
			DestinationAddress: followDest, Amount: big.NewInt(int64(100 * (i + 1))), Metadata: []byte{byte(i)},
			DepositCount: uint32(i),
		}
		exits[i] = &agglayertypes.BridgeExit{
			LeafType:           bridgetypes.LeafTypeAsset,
			TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: token},
			DestinationAddress: followDest, Amount: leaves[i].Amount, Metadata: crypto.Keccak256(leaves[i].Metadata),
		}
	}
	root := buildLocalExitTreeDB(t, filepath.Join(dir, localExitTreeDBFile), leaves)
	writeJSON := func(name string, v any) {
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), raw, 0o600))
	}
	writeJSON(fullCertificateFile, &agglayertypes.Certificate{
		NetworkID: followNetworkID, Height: certHeight, NewLocalExitRoot: root, BridgeExits: exits,
	})
	update := &exitcertificate.L1InfoTreeUpdate{
		MainnetExitRoot: common.HexToHash("0x1111"), RollupExitRoot: common.BigToHash(big.NewInt(int64(1000 + certHeight))),
	}
	writeJSON(stepWaitResultFile, &exitcertificate.StepWaitResult{
		CertificateHash: certHash(certHeight), FinalStatus: agglayertypes.Settled, UpdateL1InfoTree: update,
	})
	ger := l1infotreesync.CalculateGER(update.MainnetExitRoot, update.RollupExitRoot)
	e.l1.mu.Lock()
	e.l1.roots[ger] = root
	e.l1.mu.Unlock()
	e.gers[certHeight] = ger

	loc := e.base.Run(followNetworkID, prefixHeight)
	files, err := runarchive.CollectFiles(dir, func(rel string, _ bool) bool { return strings.Contains(rel, "sqlite-") })
	require.NoError(t, err)
	_, err = runarchive.Upload(context.Background(), archivetest.NewStore(t, e.objs), loc, dir, files,
		exitcertificate.ArchiveDataVersion, map[string]string{"height": fmt.Sprint(certHeight)}, t.TempDir())
	require.NoError(t, err)
	return loc
}

func certHash(height uint64) common.Hash {
	return common.BigToHash(big.NewInt(int64(0xc0de00 + height)))
}

// editManifest rewrites the archived manifest of loc.
func (e *followEnv) editManifest(loc runarchive.Location, edit func(string) string) {
	e.t.Helper()
	name := loc.ObjectName(runarchive.ManifestFile)
	raw, ok := e.objs.Get(name)
	require.True(e.t, ok)
	edited := edit(string(raw))
	require.NotEqual(e.t, string(raw), edited, "the edit must change the manifest")
	e.setObject(name, []byte(edited))
}

func (e *followEnv) setObject(name string, data []byte) {
	// Through a store call so it takes the objects' lock.
	e.deleteObject(name)
	require.NoError(e.t, archivetest.NewStore(e.t, e.objs).Create(context.Background(), name,
		strings.NewReader(string(data)), archivetest.CRC32C(data), nil))
}

func (e *followEnv) deleteObject(name string) []byte {
	return e.objs.Delete(name)
}

// followClaimer is a claimer serving the run of a height from the archive, with its follow mode loop (not
// started: the tests drive the checks).
type followClaimer struct {
	cfg    *Config
	server *Server
	follow *follower
}

// start loads the run of height like the service does at startup and builds the server and the follower.
func (e *followEnv) start(height uint64, tweak func(*Config)) *followClaimer {
	e.t.Helper()
	t := e.t
	logger := log.WithFields("test", t.Name())
	cfg := &Config{
		RunSource: followBaseURI, RunSourceHeight: &height, NetworkID: followNetworkID, RunDataDir: t.TempDir(),
		L1InfoTreeDBPath: "l1.sqlite", FollowIntervalSeconds: 60, ReadTimeoutSeconds: 1, WriteTimeoutSeconds: 1,
	}
	if tweak != nil {
		tweak(cfg)
	}
	open := archivetest.Opener(t, e.objs)
	require.NoError(t, fetchRunSource(context.Background(), cfg, logger, open))
	initial, err := loadRun(context.Background(), cfg, cfg.servedRun, e.l1.open, logger)
	require.NoError(t, err)
	server := newServer(cfg, initial, logger)
	t.Cleanup(server.runs.close)
	follow, err := newFollower(cfg, server, e.l1.open, open, logger)
	require.NoError(t, err)
	follow.now = func() time.Time { return time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC) }
	server.follow = follow
	return &followClaimer{cfg: cfg, server: server, follow: follow}
}

// health returns /claimer/v2/health.
func (c *followClaimer) health(t *testing.T) HealthResponseV2 {
	t.Helper()
	rec := doRequest(t, c.server, apiBasePathV2+"/health")
	require.Equal(t, http.StatusOK, rec.Code)
	var h HealthResponseV2
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &h))
	return h
}

// requireServing checks, through the HTTP API, that the claimer serves the run of height: /health says so,
// /bridges lists its height+1 exits and /claim-params builds a claim for each.
func (c *followClaimer) requireServing(t *testing.T, height uint64) {
	t.Helper()
	h := c.health(t)
	require.NotNil(t, h.Run)
	require.Equal(t, height, h.Run.Height)
	require.Equal(t, fmt.Sprintf("%s/%d/%010d", followBaseURI, followNetworkID, height), h.Run.Source)
	require.Equal(t, certHash(height).Hex(), h.Run.CertificateHash)
	require.Equal(t, 1, h.Run.DataVersion)
	require.Equal(t, height+1, h.Follow.NextHeight)
	require.Equal(t, fmt.Sprintf("%s/%d/%010d", followBaseURI, followNetworkID, height+1), h.Follow.NextRun)

	rec := doRequest(t, c.server, apiBasePathV2+"/bridges?address="+followDest.Hex())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var bridges BridgesResponseV2
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &bridges))
	require.Len(t, bridges.Bridges, int(height)+1)

	rec = doRequest(t, c.server, apiBasePathV2+"/claim-params?address="+followDest.Hex())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var claims ClaimParamsResponseV2
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &claims))
	require.Len(t, claims.Claims, int(height)+1)
}

func (c *followClaimer) runDir(e *followEnv, height uint64) string {
	return runarchive.CacheDir(c.cfg.RunDataDir, e.base.Run(followNetworkID, height))
}

func TestFollowNoNewRun(t *testing.T) { //nolint:paralleltest // its subtests are steps of one scenario
	e := newFollowEnv(t)
	e.archive(4)
	c := e.start(4, nil)

	// Before the first check.
	h := c.health(t)
	require.Equal(t, 60, h.Follow.IntervalSeconds)
	require.Empty(t, h.Follow.LastCheckTime)
	require.Empty(t, h.Follow.LastCheckResult)
	c.requireServing(t, 4)

	t.Run("nothing archived at the next height", func(t *testing.T) {
		require.Zero(t, c.follow.CheckPending(context.Background()))
		h := c.health(t)
		require.Equal(t, FollowResultNoNewRun, h.Follow.LastCheckResult)
		require.Equal(t, "2026-11-25T10:00:00Z", h.Follow.LastCheckTime)
		require.False(t, h.Follow.BlockedByUnsupportedVersion)
		require.False(t, h.Follow.Preparing)
		require.Empty(t, h.Follow.LastSwitchTime)
		c.requireServing(t, 4)
	})

	t.Run("an incomplete upload (no manifest) is ignored", func(t *testing.T) {
		next := e.archive(5)
		manifest := e.deleteObject(next.ObjectName(runarchive.ManifestFile))
		require.Zero(t, c.follow.CheckPending(context.Background()))
		require.Equal(t, FollowResultNoNewRun, c.health(t).Follow.LastCheckResult)
		c.requireServing(t, 4)
		_, err := os.Stat(c.runDir(e, 5))
		require.ErrorIs(t, err, os.ErrNotExist, "nothing is downloaded from an incomplete run")

		// Once the upload completes (the manifest is uploaded last), the run is picked up.
		e.setObject(next.ObjectName(runarchive.ManifestFile), manifest)
		require.Equal(t, 1, c.follow.CheckPending(context.Background()))
		c.requireServing(t, 5)
	})
}

// With the run of h+1 missing the claimer stays at h, even if h+2 is archived: runs are applied in order.
func TestFollowGapAtNextHeight(t *testing.T) {
	t.Parallel()
	e := newFollowEnv(t)
	e.archive(4)
	e.archive(6)
	c := e.start(4, nil)

	require.Zero(t, c.follow.CheckPending(context.Background()))
	require.Equal(t, FollowResultNoNewRun, c.health(t).Follow.LastCheckResult)
	c.requireServing(t, 4)

	// When the missing run shows up, both are applied.
	e.archive(5)
	require.Equal(t, 2, c.follow.CheckPending(context.Background()))
	c.requireServing(t, 6)
}

func TestFollowSwitchesToNextRun(t *testing.T) {
	t.Parallel()
	for name, cleanup := range map[string]bool{"removing the old run data": true, "keeping the old run data": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newFollowEnv(t)
			e.archive(4)
			c := e.start(4, func(cfg *Config) {
				if !cleanup {
					cfg.FollowCleanupOldRuns = &cleanup
				}
			})
			require.Equal(t, cleanup, c.cfg.followCleanupOldRuns())
			require.DirExists(t, c.runDir(e, 4))

			e.archive(5)
			require.Equal(t, 1, c.follow.CheckPending(context.Background()))
			c.requireServing(t, 5)
			h := c.health(t)
			require.Equal(t, "2026-11-25T10:00:00Z", h.Follow.LastSwitchTime)
			// The last check is the one that found nothing after the switch.
			require.Equal(t, FollowResultNoNewRun, h.Follow.LastCheckResult)

			require.DirExists(t, c.runDir(e, 5))
			if cleanup {
				require.NoDirExists(t, c.runDir(e, 4))
			} else {
				require.DirExists(t, c.runDir(e, 4))
			}
		})
	}
}

func TestFollowAppliesPendingRunsInOrder(t *testing.T) {
	t.Parallel()
	e := newFollowEnv(t)
	for h := uint64(2); h <= 5; h++ {
		e.archive(h)
	}
	c := e.start(2, nil)
	opensBefore := e.l1.opens

	require.Equal(t, 3, c.follow.CheckPending(context.Background()))
	c.requireServing(t, 5)
	// Each run in between was fully prepared (its settlement looked up in the L1 Info Tree), in order.
	require.Equal(t, opensBefore+3, e.l1.opens)
	for h := uint64(2); h <= 4; h++ {
		require.NoDirExists(t, c.runDir(e, h))
	}
	require.DirExists(t, c.runDir(e, 5))
}

func TestFollowKeepsRunWhenDownloadFails(t *testing.T) { //nolint:paralleltest // its subtests are steps of one scenario
	e := newFollowEnv(t)
	e.archive(4)
	c := e.start(4, nil)

	next := e.archive(5)
	treeObject := next.ObjectName(localExitTreeDBFile + ".zst")
	good := e.deleteObject(treeObject)

	t.Run("an object of the run is missing", func(t *testing.T) {
		require.Zero(t, c.follow.CheckPending(context.Background()))
		require.Equal(t, FollowResultDownloadFailed, c.health(t).Follow.LastCheckResult)
		c.requireServing(t, 4)
		require.DirExists(t, c.runDir(e, 4), "the run served is never cleaned up")
	})

	t.Run("an object of the run does not match the manifest", func(t *testing.T) {
		corrupted := append([]byte(nil), good...)
		corrupted[len(corrupted)-1] ^= 0xff
		e.setObject(treeObject, corrupted)
		require.Zero(t, c.follow.CheckPending(context.Background()))
		require.Equal(t, FollowResultDownloadFailed, c.health(t).Follow.LastCheckResult)
		c.requireServing(t, 4)
	})

	t.Run("it is retried and applied once the run can be downloaded", func(t *testing.T) {
		e.setObject(treeObject, good)
		require.Equal(t, 1, c.follow.CheckPending(context.Background()))
		c.requireServing(t, 5)
	})
}

func TestFollowKeepsRunWhenL1SyncFails(t *testing.T) {
	t.Parallel()
	e := newFollowEnv(t)
	e.archive(4)
	c := e.start(4, nil)

	e.archive(5)
	e.l1.setFail(e.gers[5], errors.New("L1 RPC unreachable"))
	require.Zero(t, c.follow.CheckPending(context.Background()))
	h := c.health(t)
	require.Equal(t, FollowResultL1SyncFailed, h.Follow.LastCheckResult)
	require.False(t, h.Follow.Preparing)
	require.Empty(t, h.Follow.LastSwitchTime)
	// The run served is intact: its local exit tree is still open and proves claims.
	c.requireServing(t, 4)

	// Next check, L1 is back: the downloaded files are reused and the claimer switches.
	e.l1.setFail(e.gers[5], nil)
	require.Equal(t, 1, c.follow.CheckPending(context.Background()))
	c.requireServing(t, 5)
}

// A shutdown while the next run is being prepared is not reported as a result, but /health no longer says a
// run is being prepared.
func TestFollowShutdownWhilePreparing(t *testing.T) {
	t.Parallel()
	e := newFollowEnv(t)
	e.archive(4)
	c := e.start(4, nil)

	e.archive(5)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.l1.mu.Lock()
	e.l1.onOpen = cancel
	e.l1.fail[e.gers[5]] = context.Canceled
	e.l1.mu.Unlock()
	require.Zero(t, c.follow.CheckPending(ctx))
	h := c.health(t)
	require.False(t, h.Follow.Preparing)
	require.Empty(t, h.Follow.LastCheckResult)
	c.requireServing(t, 4)
}

func TestFollowRefusesInconsistentRun(t *testing.T) {
	t.Parallel()
	e := newFollowEnv(t)
	e.archive(4)
	c := e.start(4, nil)

	// The run archived at the prefix of height 5 holds the certificate of height 7.
	e.archiveAt(5, 7)
	require.Zero(t, c.follow.CheckPending(context.Background()))
	require.Equal(t, FollowResultLoadFailed, c.health(t).Follow.LastCheckResult)
	c.requireServing(t, 4)
}

func TestFollowUnsupportedVersion(t *testing.T) {
	t.Parallel()
	edits := map[string]func(string) string{
		"data version": func(m string) string {
			return strings.Replace(m, `"dataVersion": 1`, `"dataVersion": 2`, 1)
		},
		"manifest version": func(m string) string { return strings.Replace(m, `"version": 1`, `"version": 2`, 1) },
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newFollowEnv(t)
			e.archive(4)
			c := e.start(4, nil)
			next := e.archive(5)
			original, _ := e.objs.Get(next.ObjectName(runarchive.ManifestFile))
			e.editManifest(next, edit)
			e.archive(6) // a supported run after it does not unblock: runs are applied in order

			// It does not switch, keeps serving, and says so on every check.
			for range 3 {
				require.Zero(t, c.follow.CheckPending(context.Background()))
				h := c.health(t)
				require.Equal(t, "ok", h.Status)
				require.Equal(t, FollowResultUnsupportedVersion, h.Follow.LastCheckResult)
				require.True(t, h.Follow.BlockedByUnsupportedVersion)
				c.requireServing(t, 4)
			}
			_, err := os.Stat(c.runDir(e, 5))
			require.ErrorIs(t, err, os.ErrNotExist, "nothing of an unsupported run is downloaded")

			// v1 /health reports it too.
			rec := doRequest(t, c.server, apiBasePathV1+"/health")
			var v1 HealthResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v1))
			require.True(t, v1.Follow.BlockedByUnsupportedVersion)
			require.Equal(t, uint64(4), v1.Run.Height)

			// A claimer that supports the run (here: the manifest back to a supported one) moves on.
			e.setObject(next.ObjectName(runarchive.ManifestFile), original)
			require.Equal(t, 2, c.follow.CheckPending(context.Background()))
			h := c.health(t)
			require.False(t, h.Follow.BlockedByUnsupportedVersion)
			c.requireServing(t, 6)
		})
	}
}

// A manifest written before dataVersion existed is data version 1: served at startup and followed.
func TestFollowLegacyManifest(t *testing.T) {
	t.Parallel()
	legacy := func(m string) string { return strings.Replace(m, `  "dataVersion": 1,`+"\n", "", 1) }
	e := newFollowEnv(t)
	e.editManifest(e.archive(4), legacy)
	c := e.start(4, nil)
	c.requireServing(t, 4)

	e.editManifest(e.archive(5), legacy)
	require.Equal(t, 1, c.follow.CheckPending(context.Background()))
	c.requireServing(t, 5)
}

func TestFollowLookupFails(t *testing.T) {
	t.Parallel()
	e := newFollowEnv(t)
	e.archive(4)
	c := e.start(4, nil)
	c.follow.open = func(context.Context, string) (runarchive.ObjectStore, func() error, error) {
		return nil, nil, errors.New("no credentials")
	}
	require.Zero(t, c.follow.CheckPending(context.Background()))
	require.Equal(t, FollowResultLookupFailed, c.health(t).Follow.LastCheckResult)
	c.requireServing(t, 4)
}

// The loop checks on its own, applies what it finds and stops with its context.
func TestFollowRunLoop(t *testing.T) {
	t.Parallel()
	e := newFollowEnv(t)
	e.archive(4)
	c := e.start(4, nil)
	c.follow.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.follow.Run(ctx)
	}()
	e.archive(5)
	require.Eventually(t, func() bool { return c.health(t).Run.Height == 5 }, 10*time.Second, 10*time.Millisecond)
	c.requireServing(t, 5)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the follow loop did not stop")
	}
}

// Requests keep being answered while runs are swapped under them, each by a run that is still open.
func TestFollowSwapUnderLoad(t *testing.T) {
	t.Parallel()
	e := newFollowEnv(t)
	for h := uint64(1); h <= 4; h++ {
		e.archive(h)
	}
	c := e.start(1, nil)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	failures := make(chan string, 64)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rec := doRequest(t, c.server, apiBasePathV2+"/claim-params?address="+followDest.Hex())
				if rec.Code != http.StatusOK {
					select {
					case failures <- rec.Body.String():
					default:
					}
				}
			}
		}()
	}
	require.Equal(t, 3, c.follow.CheckPending(context.Background()))
	close(stop)
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Errorf("request failed during a swap: %s", f)
	}
	c.requireServing(t, 4)
}

func TestRunSlotRetiresOnlyWhenUnused(t *testing.T) {
	t.Parallel()
	released := make(chan struct{})
	old := &servedRun{release: func() { close(released) }}
	slot := newRunSlot(old)

	run, done := slot.acquire()
	require.Same(t, old, run)
	next := &servedRun{}
	swapped := make(chan struct{})
	go func() {
		defer close(swapped)
		require.Same(t, old, slot.swap(next))
	}()
	// The swap is visible to new requests at once, but the old run is not released while one uses it.
	require.Eventually(t, func() bool { return slot.current() == next }, 5*time.Second, time.Millisecond)
	select {
	case <-released:
		t.Fatal("released while a request still uses the run")
	case <-time.After(50 * time.Millisecond):
	}
	done()
	<-swapped
	<-released

	got, doneNext := slot.acquire()
	require.Same(t, next, got)
	doneNext()
	old.retire() // idempotent
	slot.close()
	require.True(t, next.retired)
}

func TestStartupUnsupportedRunFailsFast(t *testing.T) {
	t.Parallel()
	logger := log.WithFields("test", t.Name())
	for name, edit := range map[string]func(string) string{
		"data version": func(m string) string {
			return strings.Replace(m, `"dataVersion": 1`, `"dataVersion": 2`, 1)
		},
		"manifest version": func(m string) string { return strings.Replace(m, `"version": 1`, `"version": 2`, 1) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newFollowEnv(t)
			e.archive(3) // an older, supported run is not a fallback
			loc := e.archive(4)
			e.editManifest(loc, edit)
			for _, cfg := range []*Config{
				{RunSource: loc.String(), RunDataDir: t.TempDir(), L1InfoTreeDBPath: "l1.sqlite"},
				{
					RunSource: followBaseURI, RunSourceHeight: ptr(uint64(4)), NetworkID: followNetworkID,
					RunDataDir: t.TempDir(), L1InfoTreeDBPath: "l1.sqlite", FollowIntervalSeconds: 30,
				},
			} {
				err := fetchRunSource(context.Background(), cfg, logger, archivetest.Opener(t, e.objs))
				require.ErrorIs(t, err, runarchive.ErrUnsupportedVersion)
				require.ErrorContains(t, err, "newer exit_certificate than this claimer supports")
				require.Empty(t, cfg.SignedCertificatePath)
				_, statErr := os.Stat(runarchive.CacheDir(cfg.RunDataDir, loc))
				require.ErrorIs(t, statErr, os.ErrNotExist)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestRunRef(t *testing.T) {
	t.Parallel()
	const run = followBaseURI + "/1/0000000005"
	base, err := runarchive.ParseURI(followBaseURI)
	require.NoError(t, err)

	ref, err := (&Config{RunSource: run}).runRef()
	require.NoError(t, err)
	require.Equal(t, &runRef{Location: base.Run(1, 5), InLayout: true, Base: base, NetworkID: 1, Height: 5}, ref)

	// The base with a starting height; height 0 is a height like any other.
	ref, err = (&Config{RunSource: followBaseURI, RunSourceHeight: ptr(uint64(0)), NetworkID: 7}).runRef()
	require.NoError(t, err)
	require.Equal(t, &runRef{Location: base.Run(7, 0), InLayout: true, Base: base, NetworkID: 7, Height: 0}, ref)
	require.Equal(t, followBaseURI+"/7/0000000000", ref.Location.String())

	// Any other prefix is a run outside the layout: served as is.
	ref, err = (&Config{RunSource: "gs://bucket/some/run"}).runRef()
	require.NoError(t, err)
	require.False(t, ref.InLayout)
	require.Equal(t, "gs://bucket/some/run", ref.Location.String())

	_, err = (&Config{RunSource: run, RunSourceHeight: ptr(uint64(5))}).runRef()
	require.ErrorContains(t, err, "already names a run")
	_, err = (&Config{RunSource: followBaseURI, RunSourceHeight: ptr(uint64(5))}).runRef()
	require.ErrorContains(t, err, "networkId is required")
	_, err = (&Config{RunSource: "gs://bucket"}).runRef()
	require.ErrorContains(t, err, "invalid runSource")
}

func TestLoadConfigFollow(t *testing.T) {
	t.Parallel()
	load := func(body string) (*Config, error) {
		return LoadConfig(writeConfig(t, "config.toml", body+"\nl1InfoTreeDBPath = \"L1InfoTreeSync.sqlite\"\n"))
	}

	cfg, err := load(`
runSource = "` + followBaseURI + `"
runSourceHeight = 0
networkId = 1
followIntervalSeconds = 60
followCleanupOldRuns = false
`)
	require.NoError(t, err)
	require.NotNil(t, cfg.RunSourceHeight)
	require.Zero(t, *cfg.RunSourceHeight)
	require.Equal(t, 60, cfg.FollowIntervalSeconds)
	require.False(t, cfg.followCleanupOldRuns())

	cfg, err = load(`
runSource = "` + testRunSource + `"
followIntervalSeconds = 60
`)
	require.NoError(t, err)
	require.Nil(t, cfg.RunSourceHeight)
	require.True(t, cfg.followCleanupOldRuns(), "old runs are cleaned up by default")

	_, err = load(`
runSource = "gs://bucket/some/run"
followIntervalSeconds = 60
`)
	require.ErrorContains(t, err, "followIntervalSeconds needs a runSource of the archive layout")

	_, err = load(`
signedCertificatePath = "c.json"
localExitTreeDBPath = "t.sqlite"
stepWaitResultPath = "w.json"
followIntervalSeconds = 60
`)
	require.ErrorContains(t, err, "need runSource")

	_, err = load(`
runSource = "` + testRunSource + `"
followIntervalSeconds = -1
`)
	require.ErrorContains(t, err, "must not be negative")

	_, err = load(`
runSource = "` + followBaseURI + `"
runSourceHeight = 4
`)
	require.ErrorContains(t, err, "networkId is required")
}

func TestNewFollowerNeedsLayout(t *testing.T) {
	t.Parallel()
	_, err := newFollower(&Config{RunSource: "gs://bucket/some/run", FollowIntervalSeconds: 5}, nil, nil, nil, nil)
	require.ErrorContains(t, err, "archive layout")
	_, err = newFollower(&Config{RunSource: testRunSource}, nil, nil, nil, nil)
	require.ErrorContains(t, err, "followIntervalSeconds")
}

// A claimer serving local files reports its run in /health without a source, and no follow state.
func TestHealthRunInfoLocalFiles(t *testing.T) {
	t.Parallel()
	e := newFollowEnv(t)
	loc := e.archive(4)
	logger := log.WithFields("test", t.Name())
	cfg := &Config{RunSource: loc.String(), RunDataDir: t.TempDir(), L1InfoTreeDBPath: "l1.sqlite"}
	require.NoError(t, fetchRunSource(context.Background(), cfg, logger, archivetest.Opener(t, e.objs)))
	local := &runFiles{
		Certificate: cfg.SignedCertificatePath, LocalExitTreeDB: cfg.LocalExitTreeDBPath,
		StepWaitResult: cfg.StepWaitResultPath,
	}
	run, err := loadRun(context.Background(), cfg, local, e.l1.open, logger)
	require.NoError(t, err)
	srv := newServer(cfg, run, logger)
	t.Cleanup(srv.runs.close)

	for _, path := range []string{apiBasePathV1, apiBasePathV2} {
		rec := doRequest(t, srv, path+"/health")
		var body map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.JSONEq(t, `{"height":4,"certificate_hash":"`+certHash(4).Hex()+`"}`, string(body["run"]))
		require.NotContains(t, body, "follow")
	}
}

// The follow fields of /health, as a client sees them.
func TestHealthFollowJSON(t *testing.T) {
	t.Parallel()
	e := newFollowEnv(t)
	e.archive(4)
	c := e.start(4, nil)
	e.archive(5)
	require.Equal(t, 1, c.follow.CheckPending(context.Background()))

	rec := doRequest(t, c.server, apiBasePathV2+"/health")
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.JSONEq(t, `{
		"source": "gs://let-archive/runs/1/0000000005",
		"height": 5,
		"certificate_hash": "`+certHash(5).Hex()+`",
		"data_version": 1
	}`, string(body["run"]))
	require.JSONEq(t, `{
		"interval_seconds": 60,
		"next_run": "gs://let-archive/runs/1/0000000006",
		"next_height": 6,
		"last_check_time": "2026-11-25T10:00:00Z",
		"last_check_result": "no_new_run",
		"blocked_by_unsupported_version": false,
		"preparing": false,
		"last_switch_time": "2026-11-25T10:00:00Z"
	}`, string(body["follow"]))
	// The fields that were there before are untouched.
	require.JSONEq(t, "2", string(body["api_version"]))
	require.JSONEq(t, `"ok"`, string(body["status"]))
	require.JSONEq(t, "1", string(body["network_id"]))
	require.JSONEq(t, "[2]", string(body["api_versions"]))
}

func TestApplyRunSourceFlags(t *testing.T) {
	t.Parallel()
	ec := &exitcertificate.Config{Options: exitcertificate.Options{OutputDir: "/runs/output"}}
	newContext := func(args ...string) *cli.Context {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.String("config", "", "")
		fs.String("run-source", "", "")
		fs.Uint64("run-source-height", 0, "")
		fs.Int("follow-interval-seconds", 0, "")
		fs.Bool("follow-keep-old-runs", false, "")
		require.NoError(t, fs.Parse(args))
		return cli.NewContext(nil, fs, nil)
	}
	derived := func() *Config {
		return &Config{NetworkID: 1, L1InfoTreeDBPath: "/runs/output/L1InfoTreeSync.sqlite"}
	}

	// The archive base, a starting height (0 included) and follow mode.
	cfg := derived()
	require.NoError(t, applyRunSourceFlags(newContext("--run-source", followBaseURI, "--run-source-height", "0",
		"--follow-interval-seconds", "30", "--follow-keep-old-runs"), cfg, ec))
	ref, err := cfg.runRef()
	require.NoError(t, err)
	require.Equal(t, followBaseURI+"/1/0000000000", ref.Location.String())
	require.Equal(t, 30, cfg.FollowIntervalSeconds)
	require.False(t, cfg.followCleanupOldRuns())
	require.Equal(t, "/runs/archive-cache", cfg.RunDataDir)

	// An explicit run, followed.
	cfg = derived()
	require.NoError(t, applyRunSourceFlags(newContext("--run-source", testRunSource, "--follow-interval-seconds", "30"),
		cfg, ec))
	require.Nil(t, cfg.RunSourceHeight)
	require.True(t, cfg.followCleanupOldRuns())

	// No --run-source: nothing changes, and its companions are refused.
	cfg = derived()
	require.NoError(t, applyRunSourceFlags(newContext(), cfg, ec))
	require.Empty(t, cfg.RunSource)
	for _, args := range [][]string{
		{"--run-source-height", "4"}, {"--follow-interval-seconds", "30"}, {"--follow-keep-old-runs"},
	} {
		require.ErrorContains(t, applyRunSourceFlags(newContext(args...), derived(), ec), "needs --run-source")
	}

	// The base without a height is not a run of the layout: it cannot be followed.
	err = applyRunSourceFlags(newContext("--run-source", followBaseURI, "--follow-interval-seconds", "30"), derived(), ec)
	require.ErrorContains(t, err, "--run-source")
	require.ErrorContains(t, err, "archive layout")

	// With --config these flags are refused: the file has the options.
	_, err = selectConfig(context.Background(), newContext("--config", "x.toml", "--follow-interval-seconds", "30"), nil)
	require.ErrorContains(t, err, "needs --exit-certificate-config")
	// ...but only when given: their defaults do not get in the way of --config.
	_, err = selectConfig(context.Background(), newContext("--config", writeNativeClaimerConfig(t)), nil)
	require.NoError(t, err)
}
