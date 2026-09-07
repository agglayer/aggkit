package bridgelooptester_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	"github.com/stretchr/testify/require"
)

// TestMetricsServeExposesTheToolsMetrics starts the metrics server on a free port, records a hop
// and a cycle, and scrapes /metrics back.
func TestMetricsServeExposesTheToolsMetrics(t *testing.T) {
	metrics := bridgelooptester.NewMetrics()
	require.NotNil(t, metrics)

	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop, err := metrics.Serve(ctx, addr, log.GetDefaultLogger())
	require.NoError(t, err)
	t.Cleanup(stop)

	metrics.HopStarted("loop/1/0", time.Now().Add(-time.Minute))
	metrics.HopRetried("loop", "0->1")
	metrics.HopFinished("loop/1/0", &bridgelooptester.HopResult{
		LoopName:          "loop",
		Source:            0,
		Destination:       1,
		ClaimMode:         bridgelooptester.ClaimAuto,
		Outcome:           bridgelooptester.HopOutcomeSuccess,
		Duration:          2 * time.Second,
		StalledGate:       "claim-proof",
		ClaimModeViolated: true,
	})
	metrics.CycleFinished("loop", true)
	metrics.SetLoopHealth(1, 2)

	body := scrape(t, addr)
	for _, want := range []string{
		"bridge_loop_tester_hops_total",
		"bridge_loop_tester_hop_duration_seconds",
		"bridge_loop_tester_cycles_total",
		"bridge_loop_tester_claim_mode_violations_total",
		"bridge_loop_tester_gate_stalls_total",
		"bridge_loop_tester_hop_retries_total",
		"bridge_loop_tester_oldest_inflight_hop_age_seconds",
		"bridge_loop_tester_inflight_hops",
		"bridge_loop_tester_loops_halted 1",
		"bridge_loop_tester_stranded_loops 2",
	} {
		require.Contains(t, body, want)
	}
}

// TestMetricsAreNoOpsWhenDisabled pins what a run without Global.MetricsAddr relies on: every
// recorder method is safe on a nil *Metrics, so the orchestrator records unconditionally.
func TestMetricsAreNoOpsWhenDisabled(t *testing.T) {
	t.Parallel()

	var absent *bridgelooptester.Metrics

	stop, err := absent.Serve(context.Background(), freeAddr(t), log.GetDefaultLogger())
	require.NoError(t, err)
	require.NotNil(t, stop)
	stop()

	absent.HopStarted("k", time.Now())
	absent.HopFinished("k", &bridgelooptester.HopResult{})
	absent.HopRetried("loop", "0->1")
	absent.CycleFinished("loop", true)
	absent.SetLoopHealth(1, 2)

	// An empty address is also a no-op, even on a real recorder.
	enabled := bridgelooptester.NewMetrics()
	stop, err = enabled.Serve(context.Background(), "", log.GetDefaultLogger())
	require.NoError(t, err)
	stop()
}

// TestMetricsServeReportsABindFailure confirms a bind failure reaches the caller synchronously
// instead of being swallowed in a goroutine: a run that cannot expose its metrics must say so at
// startup rather than look healthy and publish nothing.
func TestMetricsServeReportsABindFailure(t *testing.T) {
	t.Parallel()

	// Hold the address so the metrics server cannot have it.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	_, err = bridgelooptester.NewMetrics().Serve(
		context.Background(), listener.Addr().String(), log.GetDefaultLogger())
	require.Error(t, err)
	require.Contains(t, err.Error(), "serve metrics on")
}

// freeAddr returns a loopback address nothing is listening on.
func freeAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	return addr
}

// scrape fetches /metrics from addr.
func scrape(t *testing.T, addr string) string {
	t.Helper()

	url := fmt.Sprintf("http://%s/metrics", addr)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	return string(body)
}
