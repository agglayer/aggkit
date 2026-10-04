package bridgedetector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	autoclaimmetrics "github.com/agglayer/aggkit/autoclaim/metrics"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/prometheus"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

// capturingLogger records formatted Warn and Debug lines; other levels are discarded.
type capturingLogger struct {
	warns  []string
	debugs []string
}

func (l *capturingLogger) Panicf(string, ...interface{}) {}
func (l *capturingLogger) Fatalf(string, ...interface{}) {}
func (l *capturingLogger) Info(...interface{})           {}
func (l *capturingLogger) Infof(string, ...interface{})  {}
func (l *capturingLogger) Error(...interface{})          {}
func (l *capturingLogger) Errorf(string, ...interface{}) {}
func (l *capturingLogger) Warn(args ...interface{})      { l.warns = append(l.warns, fmt.Sprint(args...)) }
func (l *capturingLogger) Warnf(format string, args ...interface{}) {
	l.warns = append(l.warns, fmt.Sprintf(format, args...))
}
func (l *capturingLogger) Debug(args ...interface{}) {
	l.debugs = append(l.debugs, fmt.Sprint(args...))
}
func (l *capturingLogger) Debugf(format string, args ...interface{}) {
	l.debugs = append(l.debugs, fmt.Sprintf(format, args...))
}

func TestDestinationBackoff_Curve(t *testing.T) {
	b := newDestinationBackoff(time.Second, 2*time.Minute, 0)
	now := testNow
	failure := errors.New("boom")

	require.False(t, b.shouldSkip(7, now))
	require.True(t, b.skipUntil(7).IsZero())

	expected := []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
		32 * time.Second, 64 * time.Second, 2 * time.Minute, 2 * time.Minute,
	}
	for i, want := range expected {
		window, attempt := b.recordFailure(7, failure, now)
		require.Equal(t, want, window)
		require.Equal(t, i+1, attempt)
		require.Equal(t, now.Add(want), b.skipUntil(7))
		require.True(t, b.shouldSkip(7, now))
		require.True(t, b.shouldSkip(7, now.Add(want-time.Nanosecond)))
		require.False(t, b.shouldSkip(7, now.Add(want)))
	}
	require.Equal(t, 1, b.stalledCount())

	b.recordSuccess(7)
	require.Equal(t, 0, b.stalledCount())
	window, attempt := b.recordFailure(7, failure, now)
	require.Equal(t, time.Second, window)
	require.Equal(t, 1, attempt)
}

func TestDestinationBackoff_Sanitises(t *testing.T) {
	b := newDestinationBackoff(5*time.Minute, time.Minute, 2)
	window, _ := b.recordFailure(1, errors.New("x"), testNow)
	require.Equal(t, 5*time.Minute, window)
}

func TestNewL1ToL2_PollPeriodDrivesInitialBackoff(t *testing.T) {
	failing := &fakeClaimer{
		target:   autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10},
		claimErr: errors.New("boom"),
	}
	source := &fakeBridgeSource{
		lastProcessedBlock: 10,
		found:              true,
		bridgesByRange: map[blockRange][]bridgesync.Bridge{
			{from: 0, to: 10}: {makeSyncBridge(1, autoclaimtypes.L1OriginNetwork, 10, 1, 0)},
		},
	}
	detector := newTestDetector(t, source, newMemoryCursorStore(), newFakeRegistry(failing),
		WithBlockWindow(11), WithPollPeriod(5*time.Second))

	_, err := detector.PollOnce(context.Background())
	require.Error(t, err)
	// Default construction: initial = poll period, jitter +-20%.
	window := detector.backoff.skipUntil(10).Sub(testNow)
	require.GreaterOrEqual(t, window, 4*time.Second)
	require.LessOrEqual(t, window, 6*time.Second)
	require.Equal(t, 5*time.Second, detector.backoff.initial)
}

type backoffFixture struct {
	detector *L1ToL2
	failing  *fakeClaimer
	healthyY *fakeClaimer
	healthyZ *fakeClaimer
	log      *capturingLogger
	clock    *time.Time
}

func newBackoffFixture(t *testing.T, failure error) *backoffFixture {
	t.Helper()
	bridges := []bridgesync.Bridge{
		makeSyncBridge(1, autoclaimtypes.L1OriginNetwork, 10, 2, 0),
		makeSyncBridge(2, autoclaimtypes.L1OriginNetwork, 11, 3, 0),
		makeSyncBridge(3, autoclaimtypes.L1OriginNetwork, 12, 4, 0),
	}
	source := &fakeBridgeSource{
		lastProcessedBlock: 10,
		found:              true,
		bridgesByRange:     map[blockRange][]bridgesync.Bridge{{from: 1, to: 10}: bridges},
	}
	f := &backoffFixture{
		failing:  &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}, claimErr: failure},
		healthyY: &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer11ID, DestinationNetwork: 11}},
		healthyZ: &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-12", DestinationNetwork: 12}},
		log:      &capturingLogger{},
	}
	clock := testNow
	f.clock = &clock
	store := newMemoryCursorStore()
	f.detector = newTestDetector(t, source, store, newFakeRegistry(f.failing, f.healthyY, f.healthyZ),
		WithStartBlock(0), WithBlockWindow(10), WithOverlapBlocks(0), WithLogger(f.log),
		WithNow(func() time.Time { return *f.clock }))
	f.detector.backoff = newDestinationBackoff(time.Second, 2*time.Minute, 0)
	seeded := autoclaimtypes.BridgeCursor{FromBlock: 0, ToBlock: 0, BlockNum: 0}
	for _, dest := range []uint32{10, 11, 12} {
		store.cursors[f.detector.cursorNameForDestination(dest)] = seeded
	}
	return f
}

func TestL1ToL2_BackoffSkipsFailingDestination(t *testing.T) {
	ctx := context.Background()
	f := newBackoffFixture(t, errors.New("boom"))

	result, err := f.detector.PollOnce(ctx)
	require.Error(t, err)
	require.Equal(t, []uint32{10}, result.FailedDestinations)
	require.Empty(t, result.BackedOffDestinations)
	require.Len(t, f.failing.claimChecks, 1)
	require.Len(t, f.log.warns, 1)

	// Same clock: destination 10 is skipped, the others are unaffected.
	result, err = f.detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, []uint32{10}, result.BackedOffDestinations)
	require.Empty(t, result.FailedDestinations)
	require.Len(t, f.failing.claimChecks, 1, "no IsClaimed call while backed off")
	require.Len(t, f.healthyY.claimChecks, 1, "healthy destination is not re-evaluated once its cursor advanced")
	require.Len(t, f.log.warns, 1)
	require.Len(t, f.log.debugs, 1)
	require.Contains(t, f.log.debugs[0], "skipping destination 10, backed off until")

	// Just before the window ends it is still skipped; at the end it is retried.
	*f.clock = testNow.Add(time.Second - time.Nanosecond)
	result, err = f.detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, []uint32{10}, result.BackedOffDestinations)

	*f.clock = testNow.Add(time.Second)
	result, err = f.detector.PollOnce(ctx)
	require.Error(t, err)
	require.Empty(t, result.BackedOffDestinations)
	require.Equal(t, []uint32{10}, result.FailedDestinations)
	require.Len(t, f.failing.claimChecks, 2)
	require.Len(t, f.log.warns, 2)
}

func TestL1ToL2_BackoffWindowsGrowAndWarnOncePerWindow(t *testing.T) {
	ctx := context.Background()
	f := newBackoffFixture(t, errors.New("boom"))

	windows := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	for i, window := range windows {
		_, err := f.detector.PollOnce(ctx)
		require.Error(t, err)
		require.Len(t, f.log.warns, i+1)
		require.Contains(t, f.log.warns[i], "paused for "+window.String())
		require.Contains(t, f.log.warns[i], fmt.Sprintf("after error (attempt %d)", i+1))
		require.Equal(t, f.clock.Add(window), f.detector.backoff.skipUntil(10))

		// Several polls inside the window add no Warn.
		for range 3 {
			result, err := f.detector.PollOnce(ctx)
			require.NoError(t, err)
			require.Equal(t, []uint32{10}, result.BackedOffDestinations)
		}
		require.Len(t, f.log.warns, i+1)
		*f.clock = f.clock.Add(window)
	}
}

func TestL1ToL2_BackoffSuccessResets(t *testing.T) {
	ctx := context.Background()
	f := newBackoffFixture(t, errors.New("boom"))

	_, err := f.detector.PollOnce(ctx)
	require.Error(t, err)
	*f.clock = f.clock.Add(time.Second)
	_, err = f.detector.PollOnce(ctx)
	require.Error(t, err)
	require.Equal(t, 1, f.detector.backoff.stalledCount())

	f.failing.claimErr = nil
	*f.clock = f.clock.Add(2 * time.Second)
	result, err := f.detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Empty(t, result.FailedDestinations)
	require.Equal(t, 0, f.detector.backoff.stalledCount())
	require.Len(t, f.log.warns, 2)

	// A new failure starts again from the initial window.
	f.failing.claimErr = errors.New("boom again")
	f.detector.backoff.recordFailure(11, nil, *f.clock) // unrelated entry must not matter
	f.detector.backoff.recordSuccess(11)
	window, attempt := f.detector.backoff.recordFailure(10, f.failing.claimErr, *f.clock)
	require.Equal(t, time.Second, window)
	require.Equal(t, 1, attempt)
}

func TestL1ToL2_BackoffRateLimitedUsesHTTP429Wording(t *testing.T) {
	ctx := context.Background()
	f := newBackoffFixture(t, rpc.HTTPError{StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests"})

	for i, window := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		_, err := f.detector.PollOnce(ctx)
		require.Error(t, err)
		require.Len(t, f.log.warns, i+1)
		require.Contains(t, f.log.warns[i], "paused for "+window.String())
		require.Contains(t, f.log.warns[i], fmt.Sprintf("after rate limiting (HTTP 429, attempt %d)", i+1))
		*f.clock = f.clock.Add(window)
	}
}

func TestL1ToL2_BackoffOtherHTTPErrorUsesGenericWording(t *testing.T) {
	ctx := context.Background()
	f := newBackoffFixture(t, rpc.HTTPError{StatusCode: http.StatusInternalServerError, Status: "500"})

	_, err := f.detector.PollOnce(ctx)
	require.Error(t, err)
	require.Len(t, f.log.warns, 1)
	require.True(t, strings.Contains(f.log.warns[0], "paused for 1s after error (attempt 1)"))
}

func TestL1ToL2_BackoffNotUpdatedWhenContextCancelled(t *testing.T) {
	f := newBackoffFixture(t, errors.New("boom"))
	ctx, cancel := context.WithCancel(context.Background())
	f.failing.claimErr = errors.New("boom")
	cancel()

	result, err := f.detector.PollOnce(ctx)
	require.Error(t, err)
	require.Equal(t, []uint32{10}, result.FailedDestinations)
	require.Equal(t, 0, f.detector.backoff.stalledCount())
	require.Empty(t, f.log.warns)
}

func TestL1ToL2_BackoffMetrics(t *testing.T) {
	prometheus.Init()
	autoclaimmetrics.Register()
	ctx := context.Background()
	f := newBackoffFixture(t, errors.New("boom"))

	gauge, ok := prometheus.Gauge("l1_to_l2_stalled_destinations") // the wrapper keys series by bare name
	require.True(t, ok)
	vec, ok := prometheus.CounterVec("detector_destination_errors_total")
	require.True(t, ok)
	errCounter := vec.WithLabelValues(string(autoclaimmetrics.DetectorL1ToL2), "10")
	before := testutil.ToFloat64(errCounter)

	_, err := f.detector.PollOnce(ctx)
	require.Error(t, err)
	require.Equal(t, 1.0, testutil.ToFloat64(gauge))
	require.Equal(t, before+1, testutil.ToFloat64(errCounter))

	// Polls inside the window neither add errors nor change the gauge.
	_, err = f.detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1.0, testutil.ToFloat64(gauge))
	require.Equal(t, before+1, testutil.ToFloat64(errCounter))

	// Recovery brings the gauge back to 0.
	f.failing.claimErr = nil
	*f.clock = f.clock.Add(time.Second)
	_, err = f.detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 0.0, testutil.ToFloat64(gauge))
	require.Equal(t, before+1, testutil.ToFloat64(errCounter))
}
