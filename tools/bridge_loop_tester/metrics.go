package bridgelooptester

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkitprometheus "github.com/agglayer/aggkit/prometheus"
	promclient "github.com/prometheus/client_golang/prometheus"
)

// Metric names exposed on Global.MetricsAddr. They all carry the tool's own prefix so a scrape
// shared with an aggkit process cannot collide.
const (
	metricHopsTotal            = "bridge_loop_tester_hops_total"
	metricHopDurationSeconds   = "bridge_loop_tester_hop_duration_seconds"
	metricCyclesTotal          = "bridge_loop_tester_cycles_total"
	metricInflightHopAge       = "bridge_loop_tester_oldest_inflight_hop_age_seconds"
	metricInflightHops         = "bridge_loop_tester_inflight_hops"
	metricLoopsHalted          = "bridge_loop_tester_loops_halted"
	metricStrandedLoops        = "bridge_loop_tester_stranded_loops"
	metricClaimModeViolations  = "bridge_loop_tester_claim_mode_violations_total"
	metricGateStallsTotal      = "bridge_loop_tester_gate_stalls_total"
	metricHopRetriesTotal      = "bridge_loop_tester_hop_retries_total"
	metricInflightGaugeRefresh = 5 * time.Second
)

// metricsReadHeaderTimeout bounds how long the metrics server waits for a client's request
// headers, so an idle or malicious connection cannot pin a goroutine indefinitely.
const metricsReadHeaderTimeout = 10 * time.Second

// metricsShutdownTimeout bounds the graceful shutdown of the metrics server.
const metricsShutdownTimeout = 5 * time.Second

// Metrics publishes the run's progress to Prometheus. It is built by NewMetrics when
// Global.MetricsAddr is set, and every method is a no-op on a nil *Metrics - so the orchestrator
// records metrics unconditionally and a run without a metrics address simply costs nothing.
//
// Metrics are registered through aggkit/prometheus, i.e. into the process-wide default registry,
// and re-registration of an already-registered name is a no-op there. That makes repeated
// in-process Run calls safe, at the cost of counters being cumulative across them - which is the
// right behaviour for a scrape target and the reason the Report, not the metrics, is what a test
// asserts on.
type Metrics struct {
	hops          *promclient.CounterVec
	hopDuration   *promclient.HistogramVec
	cycles        *promclient.CounterVec
	violations    *promclient.CounterVec
	gateStalls    *promclient.CounterVec
	retries       *promclient.CounterVec
	inflightAge   promclient.Gauge
	inflightHops  promclient.Gauge
	loopsHalted   promclient.Gauge
	strandedLoops promclient.Gauge

	// mu guards inflight, the set of hops currently executing and when each started, from which
	// the in-flight age gauge is derived.
	mu       sync.Mutex
	inflight map[string]time.Time
}

// NewMetrics registers the tool's metrics and returns a recorder for them. It performs no I/O and
// starts no server: call (*Metrics).Serve for that.
func NewMetrics() *Metrics {
	aggkitprometheus.Init()

	// Seconds-scale buckets sized for bridge hops: a fast same-rollup hop is seconds, an L2->L1 hop
	// waiting on settlement is minutes, and the tail matters most (a hop that took 20 minutes is
	// the interesting one).
	hopDurationBuckets := []float64{1, 5, 15, 30, 60, 120, 300, 600, 900, 1800, 3600}

	aggkitprometheus.RegisterCounterVecs(
		aggkitprometheus.CounterVecOpts{
			CounterOpts: promclient.CounterOpts{
				Name: metricHopsTotal,
				Help: "Hop attempts, by loop, route, claim mode and outcome.",
			},
			Labels: []string{"loop", "route", "claim_mode", "outcome"},
		},
		aggkitprometheus.CounterVecOpts{
			CounterOpts: promclient.CounterOpts{
				Name: metricCyclesTotal,
				Help: "Ring passes, by loop and outcome (completed or failed).",
			},
			Labels: []string{"loop", "outcome"},
		},
		aggkitprometheus.CounterVecOpts{
			CounterOpts: promclient.CounterOpts{
				Name: metricClaimModeViolations,
				Help: "Hops whose claim-mode expectation was violated, by loop, route and expected mode.",
			},
			Labels: []string{"loop", "route", "claim_mode"},
		},
		aggkitprometheus.CounterVecOpts{
			CounterOpts: promclient.CounterOpts{
				Name: metricGateStallsTotal,
				Help: "Hops that stalled on a readiness gate, by loop, route and gate name.",
			},
			Labels: []string{"loop", "route", "gate"},
		},
		aggkitprometheus.CounterVecOpts{
			CounterOpts: promclient.CounterOpts{
				Name: metricHopRetriesTotal,
				Help: "Hop retries after a transient failure, by loop and route.",
			},
			Labels: []string{"loop", "route"},
		},
	)

	aggkitprometheus.RegisterHistogramVecs(aggkitprometheus.HistogramVecOpts{
		HistogramOpts: promclient.HistogramOpts{
			Name:    metricHopDurationSeconds,
			Help:    "End-to-end duration of a hop attempt, by loop, route and claim mode.",
			Buckets: hopDurationBuckets,
		},
		Labels: []string{"loop", "route", "claim_mode"},
	})

	aggkitprometheus.RegisterGauges(
		promclient.GaugeOpts{
			Name: metricInflightHopAge,
			Help: "Age in seconds of the longest-running hop currently in flight (0 when idle).",
		},
		promclient.GaugeOpts{
			Name: metricInflightHops,
			Help: "Number of hops currently in flight across all loops.",
		},
		promclient.GaugeOpts{
			Name: metricLoopsHalted,
			Help: "Number of loops permanently halted by a non-retryable failure.",
		},
		promclient.GaugeOpts{
			Name: metricStrandedLoops,
			Help: "Number of loops whose value is not resting on their origin network.",
		},
	)

	m := &Metrics{inflight: map[string]time.Time{}}
	m.hops, _ = aggkitprometheus.CounterVec(metricHopsTotal)
	m.cycles, _ = aggkitprometheus.CounterVec(metricCyclesTotal)
	m.violations, _ = aggkitprometheus.CounterVec(metricClaimModeViolations)
	m.gateStalls, _ = aggkitprometheus.CounterVec(metricGateStallsTotal)
	m.retries, _ = aggkitprometheus.CounterVec(metricHopRetriesTotal)
	m.hopDuration, _ = aggkitprometheus.HistogramVec(metricHopDurationSeconds)
	m.inflightAge, _ = aggkitprometheus.Gauge(metricInflightHopAge)
	m.inflightHops, _ = aggkitprometheus.Gauge(metricInflightHops)
	m.loopsHalted, _ = aggkitprometheus.Gauge(metricLoopsHalted)
	m.strandedLoops, _ = aggkitprometheus.Gauge(metricStrandedLoops)

	return m
}

// Serve starts an HTTP server exposing the metrics on addr until ctx is cancelled, then shuts it
// down gracefully. It returns as soon as the listener is bound (so a bind failure is reported to
// the caller synchronously rather than swallowed in a goroutine) and the returned stop function
// blocks until the server has shut down.
func (m *Metrics) Serve(
	ctx context.Context, addr string, logger aggkitcommon.Logger,
) (stop func(), err error) {
	if m == nil || addr == "" {
		return func() {}, nil
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("serve metrics on %s: %w", addr, err)
	}

	mux := http.NewServeMux()
	mux.Handle(aggkitprometheus.Endpoint, aggkitprometheus.Handler())
	server := &http.Server{Handler: mux, ReadHeaderTimeout: metricsReadHeaderTimeout}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Errorf("bridge_loop_tester: metrics server on %s stopped: %v", addr, serveErr)
		}
	}()

	// Refresh the in-flight age gauge on a timer: it is a function of wall-clock time, so it would
	// otherwise be stale for exactly the hop that matters - the one that is stuck.
	wg.Add(1)
	refreshDone := make(chan struct{})
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(metricInflightGaugeRefresh)
		defer ticker.Stop()
		for {
			select {
			case <-refreshDone:
				return
			case <-ticker.C:
				m.refreshInflightAge()
			}
		}
	}()

	logger.Infof("bridge_loop_tester: serving metrics on http://%s%s", listener.Addr(), aggkitprometheus.Endpoint)

	return func() {
		close(refreshDone)
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), metricsShutdownTimeout)
		defer cancel()
		if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
			logger.Warnf("bridge_loop_tester: metrics server on %s did not shut down cleanly: %v",
				addr, shutdownErr)
		}
		wg.Wait()
	}, nil
}

// HopStarted notes that a hop began, so the in-flight gauges reflect it.
func (m *Metrics) HopStarted(key string, startedAt time.Time) {
	if m == nil {
		return
	}

	m.mu.Lock()
	m.inflight[key] = startedAt
	m.mu.Unlock()
	m.refreshInflightAge()
}

// HopFinished records a completed hop attempt: its outcome, its duration, and, when it stalled,
// which readiness gate it stalled on.
func (m *Metrics) HopFinished(key string, result *HopResult) {
	if m == nil || result == nil {
		return
	}

	m.mu.Lock()
	delete(m.inflight, key)
	m.mu.Unlock()
	m.refreshInflightAge()

	route := result.Route()
	claimMode := string(result.ClaimMode)
	m.hops.WithLabelValues(result.LoopName, route, claimMode, string(result.Outcome)).Inc()
	m.hopDuration.WithLabelValues(result.LoopName, route, claimMode).Observe(result.Duration.Seconds())
	if result.ClaimModeViolated {
		m.violations.WithLabelValues(result.LoopName, route, claimMode).Inc()
	}
	if result.StalledGate != "" {
		m.gateStalls.WithLabelValues(result.LoopName, route, result.StalledGate).Inc()
	}
}

// HopRetried records that a hop is being retried after a transient failure.
func (m *Metrics) HopRetried(loopName, route string) {
	if m == nil {
		return
	}
	m.retries.WithLabelValues(loopName, route).Inc()
}

// CycleFinished records a ring pass and whether it closed the ring.
func (m *Metrics) CycleFinished(loopName string, closed bool) {
	if m == nil {
		return
	}

	outcome := "failed"
	if closed {
		outcome = "completed"
	}
	m.cycles.WithLabelValues(loopName, outcome).Inc()
}

// SetLoopHealth publishes how many loops are halted and how many hold stranded value.
func (m *Metrics) SetLoopHealth(halted, stranded int) {
	if m == nil {
		return
	}
	m.loopsHalted.Set(float64(halted))
	m.strandedLoops.Set(float64(stranded))
}

// refreshInflightAge recomputes the in-flight gauges from the current set of running hops.
func (m *Metrics) refreshInflightAge() {
	if m == nil {
		return
	}

	m.mu.Lock()
	count := len(m.inflight)
	oldest := time.Duration(0)
	now := time.Now()
	for _, startedAt := range m.inflight {
		if age := now.Sub(startedAt); age > oldest {
			oldest = age
		}
	}
	m.mu.Unlock()

	m.inflightHops.Set(float64(count))
	m.inflightAge.Set(oldest.Seconds())
}
