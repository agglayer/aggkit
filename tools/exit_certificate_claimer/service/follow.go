package claimer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
)

// servedRun is a loaded exit_certificate run: the claimer that serves it and what /health says about it.
type servedRun struct {
	claimer *Claimer
	// info describes the run in /health; nil for a server built straight from a Claimer (NewServer).
	info *RunInfo
	// files are the run's local files; nil for a server built straight from a Claimer.
	files *runFiles
	// release frees the resources of the run (its local exit tree DB); may be nil.
	release func()

	// mu is read-locked by every request using the run, and write-locked to retire it: the resources are
	// only released once no request uses them.
	mu      sync.RWMutex
	retired bool
}

// runSlot holds the run being served and lets it be replaced while requests are in flight: a request keeps
// the run it started with until it ends, new requests get the new one.
type runSlot struct {
	cur atomic.Pointer[servedRun]
}

func newRunSlot(run *servedRun) *runSlot {
	s := &runSlot{}
	s.cur.Store(run)
	return s
}

// acquire returns the run being served and the function that releases it, to call when the request ends.
func (s *runSlot) acquire() (*servedRun, func()) {
	for {
		run := s.cur.Load()
		run.mu.RLock()
		if !run.retired {
			return run, run.mu.RUnlock
		}
		run.mu.RUnlock() // replaced between Load and RLock: take the new one
	}
}

// current returns the run being served without holding it (for its immutable description only).
func (s *runSlot) current() *servedRun {
	return s.cur.Load()
}

// swap makes next the run being served and retires the previous one, waiting for the requests still using
// it. It returns the previous run.
func (s *runSlot) swap(next *servedRun) *servedRun {
	old := s.cur.Swap(next)
	old.retire()
	return old
}

// close retires the run being served (on shutdown, once the HTTP server stopped).
func (s *runSlot) close() {
	s.cur.Load().retire()
}

// retire releases the resources of the run once no request uses it.
func (r *servedRun) retire() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.retired {
		return
	}
	r.retired = true
	if r.release != nil {
		r.release()
	}
}

// followPrepareTimeout bounds one attempt to prepare the next run (download, load and L1 sync). The work
// already done is kept (downloaded files, synced L1 blocks, and the L1 sync itself keeps running, see
// l1InfoTree), so the next attempt resumes from there.
const followPrepareTimeout = time.Hour

// follower is the follow mode loop: it looks for the run of the next certificate height in the archive
// and, when it is there, prepares it and makes the server serve it. The run served is only ever replaced
// by a fully prepared one, so a failure at any step leaves the claimer serving what it had.
type follower struct {
	logger     *log.Logger
	cfg        *Config
	server     *Server
	openL1     l1Opener
	open       archiveOpener
	interval   time.Duration
	prepareTTL time.Duration
	now        func() time.Time

	base      runarchive.Location
	networkID uint32

	// store is the archive bucket, opened on the first check and kept until the loop ends.
	store      runarchive.ObjectStore
	closeStore func() error

	mu     sync.Mutex
	status FollowStatus
	// height is the height of the run being served.
	height uint64
}

// newFollower builds the follow mode loop of a server that already serves the run cfg points at.
func newFollower(
	cfg *Config, server *Server, openL1 l1Opener, open archiveOpener, logger *log.Logger,
) (*follower, error) {
	ref, err := cfg.runRef()
	if err != nil {
		return nil, err
	}
	if !ref.InLayout || cfg.FollowIntervalSeconds <= 0 {
		return nil, errors.New("follow mode needs followIntervalSeconds and a runSource of the archive layout")
	}
	f := &follower{
		logger: logger, cfg: cfg, server: server, openL1: openL1, open: open,
		interval:   time.Duration(cfg.FollowIntervalSeconds) * time.Second,
		prepareTTL: followPrepareTimeout,
		now:        time.Now,
		base:       ref.Base, networkID: ref.NetworkID,
		status: FollowStatus{IntervalSeconds: cfg.FollowIntervalSeconds},
	}
	f.setServedHeight(ref.Height)
	return f, nil
}

// Status returns a copy of the state of follow mode.
func (f *follower) Status() *FollowStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	status := f.status
	return &status
}

// setServedHeight records the height of the run being served and, from it, the next run to look for.
func (f *follower) setServedHeight(height uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.height = height
	f.status.NextHeight = height + 1
	f.status.NextRun = f.base.Run(f.networkID, height+1).String()
}

// Run checks for the next run every interval until ctx is done.
func (f *follower) Run(ctx context.Context) {
	f.logger.Infof("follow mode: looking for the next run every %s (next: %s)", f.interval, f.Status().NextRun)
	defer f.closeArchive()
	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()
	for {
		f.CheckPending(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// CheckPending applies every run already archived after the one served, in height order, and returns how
// many it switched through. It stops at the first height that is not archived or cannot be applied.
func (f *follower) CheckPending(ctx context.Context) int {
	switched := 0
	for ctx.Err() == nil && f.checkNext(ctx) {
		switched++
	}
	return switched
}

// checkNext looks for the run of the next height and switches to it when it can. It reports whether it
// switched.
func (f *follower) checkNext(ctx context.Context) bool {
	f.mu.Lock()
	height := f.height + 1
	f.mu.Unlock()
	loc := f.base.Run(f.networkID, height)

	result, err := f.tryRun(ctx, loc, height)
	if ctx.Err() != nil {
		return false // shutting down: not a result worth reporting
	}
	switch result {
	case FollowResultSwitched:
	case FollowResultNoNewRun:
		f.logger.Debugf("follow mode: no run archived at %s yet", loc)
	case FollowResultUnsupportedVersion:
		f.logger.Warnf("follow mode: NOT switching to the run archived at %s, this claimer does not support it; "+
			"it keeps serving the run of height %d. Upgrade the claimer: %v", loc, height-1, err)
	default:
		f.logger.Warnf("follow mode: could not switch to the run archived at %s (%s), still serving the run of "+
			"height %d; retrying in %s: %v", loc, result, height-1, f.interval, err)
	}

	f.mu.Lock()
	f.status.LastCheckTime = f.now().UTC().Format(time.RFC3339)
	f.status.LastCheckResult = result
	f.status.BlockedByUnsupportedVersion = result == FollowResultUnsupportedVersion
	f.status.Preparing = false
	if result == FollowResultSwitched {
		f.status.LastSwitchTime = f.status.LastCheckTime
	}
	f.mu.Unlock()
	return result == FollowResultSwitched
}

// tryRun does one check of the run at loc (the next height): it reads its manifest with a single request
// and, when the run is there and supported, downloads it, loads it and switches to it. It returns one of
// the FollowResult* values, with the error behind any other than "switched" and "no new run".
func (f *follower) tryRun(ctx context.Context, loc runarchive.Location, height uint64) (string, error) {
	store, err := f.archive(ctx)
	if err != nil {
		return FollowResultLookupFailed, err
	}
	manifest, err := runarchive.ReadManifest(ctx, store, loc)
	switch {
	case errors.Is(err, runarchive.ErrIncompleteRun):
		// Not archived yet, or an upload that did not complete (its manifest is uploaded last).
		return FollowResultNoNewRun, nil
	case errors.Is(err, runarchive.ErrUnsupportedVersion):
		return FollowResultUnsupportedVersion, err
	case err != nil:
		return FollowResultLookupFailed, err
	}
	if err := runarchive.CheckDataVersion(manifest, SupportedDataVersions); err != nil {
		return FollowResultUnsupportedVersion, err
	}

	f.logger.Infof("follow mode: found the run of height %d at %s, preparing it", height, loc)
	f.mu.Lock()
	f.status.Preparing = true
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, f.prepareTTL)
	defer cancel()

	files, err := fetchRun(ctx, store, loc, f.cfg.RunDataDir, f.logger)
	if err != nil {
		if errors.Is(err, runarchive.ErrUnsupportedVersion) {
			return FollowResultUnsupportedVersion, err
		}
		return FollowResultDownloadFailed, err
	}
	next, err := loadRun(ctx, f.cfg, files, f.openL1, f.logger)
	if err != nil {
		if errors.Is(err, errL1Sync) {
			return FollowResultL1SyncFailed, err
		}
		return FollowResultLoadFailed, err
	}
	if err := f.checkNextRun(next, height); err != nil {
		next.retire()
		return FollowResultLoadFailed, err
	}

	old := f.server.runs.swap(next)
	f.setServedHeight(height)
	f.logger.Infof("follow mode: now serving the run of height %d (%s, certificate %s)",
		height, loc, next.info.CertificateHash)
	f.cleanup(old, next)
	return FollowResultSwitched, nil
}

// checkNextRun refuses a run that is not the one expected at its prefix: its certificate must be of the
// network served, at the next height.
func (f *follower) checkNextRun(next *servedRun, height uint64) error {
	cert := next.claimer.cert
	if cert.NetworkID != f.networkID {
		return fmt.Errorf("the certificate of the run is for network %d, not %d", cert.NetworkID, f.networkID)
	}
	if served := f.server.runs.current().claimer.NetworkID(); next.claimer.NetworkID() != served {
		return fmt.Errorf("the run is for network %d, the claimer serves network %d", next.claimer.NetworkID(), served)
	}
	if cert.Height != height {
		return fmt.Errorf("the certificate of the run has height %d, its prefix says %d", cert.Height, height)
	}
	return nil
}

// cleanup removes the local copy of the run that was replaced, when configured to and when it is a
// directory of the archive cache other than the one now served.
func (f *follower) cleanup(old, next *servedRun) {
	if !f.cfg.followCleanupOldRuns() || old.files == nil || old.files.Source == nil {
		return
	}
	dir := runarchive.CacheDir(f.cfg.RunDataDir, *old.files.Source)
	if dir != old.files.Dir || dir == next.files.Dir {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		f.logger.Warnf("follow mode: remove the local copy of the previous run %s: %v", dir, err)
		return
	}
	f.logger.Infof("follow mode: removed the local copy of the previous run (%s)", dir)
}

// archive returns the archive bucket, opening it on first use.
func (f *follower) archive(ctx context.Context) (runarchive.ObjectStore, error) {
	if f.store != nil {
		return f.store, nil
	}
	store, closeStore, err := f.open(ctx, f.base.Bucket)
	if err != nil {
		return nil, fmt.Errorf("open the archive: %w", err)
	}
	f.store, f.closeStore = store, closeStore
	return store, nil
}

func (f *follower) closeArchive() {
	if f.closeStore == nil {
		return
	}
	if err := f.closeStore(); err != nil {
		f.logger.Warnf("close archive store: %v", err)
	}
	f.store, f.closeStore = nil, nil
}
