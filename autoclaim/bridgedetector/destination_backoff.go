package bridgedetector

import (
	"errors"
	"net/http"
	"time"

	"github.com/agglayer/aggkit/autoclaim/metrics"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/rpc"
)

const (
	destinationBackoffFactor = 2.0
	destinationBackoffMax    = 2 * time.Minute
	destinationBackoffJitter = 0.2
)

// destinationBackoff tracks, per destination network, a capped exponential back-off so that a failing
// destination is retried after a growing pause instead of on every poll. The first window equals the
// initial delay (the detector poll period) and doubles up to the max; a success resets it.
//
// The window is purely computed from the number of consecutive failures: a server-provided Retry-After
// hint is NOT honoured, because go-ethereum's rpc.HTTPError does not expose the response headers.
//
// It is not goroutine-safe: each detector owns one instance and only touches it from its poll goroutine.
// The type is shared by the L1-to-L2 and L2-to-Lx detectors; the instances are not.
type destinationBackoff struct {
	initial, max time.Duration
	jitter       float64
	randFloat    func() float64 // nil = default
	entries      map[uint32]*destinationBackoffEntry
}

type destinationBackoffEntry struct {
	backoff *aggkitcommon.ExponentialBackoff
	until   time.Time
	lastErr error
}

// newDestinationBackoff creates a tracker whose first window is initial, grows by destinationBackoffFactor
// and is capped at maxWindow, with the given symmetric relative jitter. Out-of-range arguments are sanitised
// (initial <= 0 becomes one second, maxWindow < initial becomes initial, jitter outside [0, 1) becomes 0).
func newDestinationBackoff(initial, maxWindow time.Duration, jitter float64) *destinationBackoff {
	if initial <= 0 {
		initial = time.Second
	}
	if maxWindow < initial {
		maxWindow = initial
	}
	if jitter < 0 || jitter >= 1 {
		jitter = 0
	}
	return &destinationBackoff{
		initial: initial,
		max:     maxWindow,
		jitter:  jitter,
		entries: make(map[uint32]*destinationBackoffEntry),
	}
}

// shouldSkip reports whether dest is inside its back-off window at now.
func (b *destinationBackoff) shouldSkip(dest uint32, now time.Time) bool {
	entry, ok := b.entries[dest]
	return ok && now.Before(entry.until)
}

// skipUntil returns the end of dest's current window (zero if none), for the Debug log.
func (b *destinationBackoff) skipUntil(dest uint32) time.Time {
	if entry, ok := b.entries[dest]; ok {
		return entry.until
	}
	return time.Time{}
}

// recordFailure opens the next window: window = entry.backoff.Next(); until = now + window.
// It returns the window and the number of consecutive failures (the attempt) recorded for dest.
func (b *destinationBackoff) recordFailure(
	dest uint32, err error, now time.Time,
) (window time.Duration, attempt int) {
	entry, ok := b.entries[dest]
	if !ok {
		var opts []aggkitcommon.ExponentialBackoffOption
		if b.randFloat != nil {
			opts = append(opts, aggkitcommon.WithBackoffRandFloat(b.randFloat))
		}
		// The configuration is sanitised by newDestinationBackoff, so it is always valid.
		backoff, _ := aggkitcommon.NewExponentialBackoff(aggkitcommon.ExponentialBackoffConfig{
			Initial: b.initial,
			Max:     b.max,
			Factor:  destinationBackoffFactor,
			Jitter:  b.jitter,
		}, opts...)
		entry = &destinationBackoffEntry{backoff: backoff}
		b.entries[dest] = entry
	}
	window = entry.backoff.Next()
	entry.until = now.Add(window)
	entry.lastErr = err
	return window, entry.backoff.Attempts()
}

// recordSuccess deletes dest's entry (full reset).
func (b *destinationBackoff) recordSuccess(dest uint32) {
	delete(b.entries, dest)
}

// stalledCount returns the number of destinations with an entry (failing and not yet recovered).
func (b *destinationBackoff) stalledCount() int {
	return len(b.entries)
}

// recordFailureAndWarn records a destination failure, counts it in the destination error metric and logs
// the one Warn per back-off window (an HTTP 429 gets its own wording). detectorName is the detector name
// used in the log text ("l1-to-l2" or "l2-to-lx").
func (b *destinationBackoff) recordFailureAndWarn(
	detector metrics.Detector,
	detectorName string,
	dest uint32,
	err error,
	now time.Time,
	warnf func(format string, args ...interface{}),
) {
	window, attempt := b.recordFailure(dest, err, now)
	metrics.IncDestinationError(detector, dest)
	var httpErr rpc.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusTooManyRequests {
		warnf("autoclaim %s bridge detector: detection for destination %d paused for %s "+
			"after rate limiting (HTTP 429, attempt %d): %v", detectorName, dest, window, attempt, err)
		return
	}
	warnf("autoclaim %s bridge detector: detection for destination %d paused for %s "+
		"after error (attempt %d): %v", detectorName, dest, window, attempt, err)
}
