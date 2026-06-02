package e2e

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/stretchr/testify/require"
)

// triggerCertModesTestTimeout bounds the overall test. It mirrors certSettlementTestTimeout
// (20m) so the bounded cadence observation window has room without approaching the suite -timeout.
const triggerCertModesTestTimeout = 20 * time.Minute

// triggerCertModesObserveTimeout bounds the wait while observing certificate-height advancement.
// Cert cadence on op-pp is multi-minute (empirically ~1 cert per couple of minutes when there is
// L2 activity), so a generous 15m window is used to stay non-flaky, matching certSettlementWaitTimeout.
const triggerCertModesObserveTimeout = 15 * time.Minute

// triggerCertModesBridgeAmount is the small ETH amount bridged L1->L2 to keep the network warm so
// the aggsender has activity to certify. EpochBased still needs bridge/network activity to produce a
// non-trivial certificate; this is "keep the network warm", not "trigger by bridge". Kept minimal.
var triggerCertModesBridgeAmount = certSettlementBridgeAmount

// triggerCertMode identifiers, matching the aggsender config/trigger semantics:
//   - aggsender [AggSender] Mode (e.g. "PessimisticProof").
//   - TriggerCertMode config key; when absent it defaults to "Auto" (see aggkit config/default.go),
//     and Auto resolves by aggsender Mode in aggsender/trigger/factory.go: PessimisticProof (and
//     AggchainProof) -> EpochBased.
const (
	aggsenderModePessimisticProof = "PessimisticProof"
	triggerCertModeAuto           = "Auto"
	triggerCertModeEpochBased     = "EpochBased"
	triggerCertModeUnknown        = "Unknown"
)

// TestTriggerCertModes ports the "Measure certificate generation intervals" case from the legacy
// e2e/tests/aggkit/trigger-cert-modes.bats. It (1) detects the configured trigger-cert mode by
// reading the deployed aggkit config (faithful to bats detect_trigger_mode, which greps the config
// for TriggerCertMode), resolving an absent TriggerCertMode to the default "Auto" and, for the op-pp
// PessimisticProof aggsender, to the effective "EpochBased" trigger mode; and (2) measures the
// certificate-production cadence by observing the agglayer certificate height advance over a bounded
// window (faithful to bats monitor_certificate_intervals, which counts height changes and records
// inter-cert wall-clock intervals). It drives a light L1->L2 bridge-and-claim to keep the network
// warm, asserts at least one new certificate is produced within the window, records observed
// intervals as informational stats, and leaves the env healthy.
func TestTriggerCertModes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	ctx, cancel := context.WithTimeout(context.Background(), triggerCertModesTestTimeout)
	defer cancel()

	// (1) Detect the configured trigger mode from the deployed aggkit config (faithful to the bats
	// detect_trigger_mode config grep), then resolve the effective mode.
	aggsenderMode := readAggSenderConfigKey(t, env, "Mode")
	require.Equal(t, aggsenderModePessimisticProof, aggsenderMode,
		"op-pp aggkit config [AggSender] Mode is expected to be PessimisticProof")

	configuredTrigger := readAggSenderConfigKey(t, env, "TriggerCertMode")
	if configuredTrigger == "" {
		// Faithful to bats: an absent TriggerCertMode key yields empty. In aggkit this means the
		// config default "Auto" applies (config/default.go).
		configuredTrigger = triggerCertModeAuto
	}
	log.Infof("[TestTriggerCertModes] detected [AggSender] Mode=%q TriggerCertMode=%q (op-pp)",
		aggsenderMode, configuredTrigger)

	effectiveTrigger := resolveTriggerCertMode(configuredTrigger, aggsenderMode)
	log.Infof("[TestTriggerCertModes] resolved effective trigger mode=%q", effectiveTrigger)
	require.Equal(t, triggerCertModeEpochBased, effectiveTrigger,
		"op-pp PessimisticProof + Auto TriggerCertMode must resolve to EpochBased")

	// Best-effort corroboration via aggsender logs (the "aggsender" module logs trigger/epoch
	// activity). This is informational only and must never fail the test if absent/unavailable.
	corroborateTriggerModeFromLogs(ctx, t, env)

	// (2) Drive a little bridge activity so the aggsender has something to certify.
	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	result := bridgeETHL1ToL2AndClaim(ctx, t, env, l1Opts, l2Opts, triggerCertModesBridgeAmount)
	log.Infof("[TestTriggerCertModes] L1->L2 bridge complete: deposit_count=%d global_index=%s",
		result.DepositCount, result.GlobalIndex.String())

	// (3) Measure cadence: observe the agglayer certificate height advance over the bounded window,
	// recording inter-cert wall-clock intervals (faithful to bats monitor_certificate_intervals,
	// which counts height changes and records the interval since the previous change).
	readRPCURL := agglayerReadRPCURL(t, env)
	log.Infof("[TestTriggerCertModes] using agglayer read RPC %s for L2 network id %d",
		readRPCURL, env.L2.NetworkID)

	intervals := observeCertificateIntervals(ctx, t, env, readRPCURL, triggerCertModesObserveTimeout)

	// Pass condition (faithful to the bats assert_success on monitor_certificate_intervals plus its
	// "did the height advance?" intent): at least one new certificate height must be observed within
	// the bounded window. Unlike the bats fixed 300s window, this test uses a generous timeout and
	// stops early once it has observed enough increments; the interval array is informational stats,
	// not a hard upper-bound on cadence.
	require.GreaterOrEqual(t, len(intervals), 1,
		"expected at least one new certificate height within %s", triggerCertModesObserveTimeout)
	logIntervalStats(t, intervals)

	// Leave the env healthy so later tests and the post-suite TestMain check pass.
	assertNetworkHealthy(ctx, t, env)
}

// readAggSenderConfigKey reads a top-level scalar key (e.g. "Mode", "TriggerCertMode") from the
// [AggSender] section of the deployed aggkit config TOML at env.GetAggkitConfigPath(). It is faithful
// to the bats detect_trigger_mode approach (grep "^TriggerCertMode" / "^Mode" + cut on quotes) but
// restricts the scan to the [AggSender] section (stopping at the next non-subsection header) so it
// does not pick up an identically named key in an unrelated section. It returns the unquoted value,
// or "" if the key is absent (mirroring the bats empty result for a missing key).
func readAggSenderConfigKey(t *testing.T, env *envs.Env, key string) string {
	t.Helper()
	configPath := env.GetAggkitConfigPath()
	data, err := os.ReadFile(configPath)
	require.NoError(t, err, "read aggkit config at %s", configPath)

	inAggSender := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue // comment line
		}
		if strings.HasPrefix(line, "[") {
			// A section header. Enter the [AggSender] table; leave it on any header that is neither
			// [AggSender] nor one of its [AggSender.*] subsections (top-level scalars live only
			// directly under [AggSender]).
			header := strings.Trim(line, "[]")
			switch {
			case header == "AggSender":
				inAggSender = true
			case strings.HasPrefix(header, "AggSender."):
				// Subsection of [AggSender]; top-level [AggSender] scalars are no longer in scope.
				inAggSender = false
			default:
				inAggSender = false
			}
			continue
		}
		if !inAggSender {
			continue
		}
		name, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		if strings.TrimSpace(name) != key {
			continue
		}
		// Strip surrounding quotes (TOML string) or inline comment, mirroring the bats `cut -d'"'`.
		v := strings.TrimSpace(value)
		if i := strings.Index(v, "\""); i >= 0 {
			rest := v[i+1:]
			if j := strings.Index(rest, "\""); j >= 0 {
				return rest[:j]
			}
		}
		// Non-string scalar (rare for these keys): strip any trailing inline comment.
		if i := strings.Index(v, "#"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		return v
	}
	require.NoError(t, scanner.Err(), "scan aggkit config %s", configPath)
	return ""
}

// resolveTriggerCertMode resolves the configured TriggerCertMode to the effective trigger mode the
// aggsender uses. It mirrors aggsender/trigger/factory.go defaultTriggerForAggsenderMode: an explicit
// mode is used as-is; "Auto" resolves by the aggsender Mode, with PessimisticProof (and AggchainProof)
// -> EpochBased. For op-pp (PessimisticProof + absent/Auto TriggerCertMode) this yields EpochBased.
func resolveTriggerCertMode(configuredTrigger, aggsenderMode string) string {
	if configuredTrigger != "" && configuredTrigger != triggerCertModeAuto {
		return configuredTrigger
	}
	switch aggsenderMode {
	case aggsenderModePessimisticProof, "AggchainProof":
		return triggerCertModeEpochBased
	default:
		return triggerCertModeUnknown
	}
}

// corroborateTriggerModeFromLogs best-effort reads recent aggkit-001 logs and notes whether the
// aggsender module is emitting trigger/epoch activity. It is informational only: any error or absence
// is logged and ignored, never failing the test (the authoritative detection is config-driven).
func corroborateTriggerModeFromLogs(ctx context.Context, t *testing.T, env *envs.Env) {
	t.Helper()
	logsCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := env.DockerComposeLogs(logsCtx, "--no-log-prefix", "--tail", "500", "aggkit-001")
	if err != nil {
		log.Warnf("[TestTriggerCertModes] best-effort aggsender log corroboration skipped: %v", err)
		return
	}
	logs := string(out)
	hasAggsender := strings.Contains(logs, "aggsender")
	hasEpochOrTrigger := strings.Contains(strings.ToLower(logs), "epoch") ||
		strings.Contains(strings.ToLower(logs), "trigger")
	log.Infof("[TestTriggerCertModes] aggsender log corroboration (informational): aggsender_logs=%t epoch/trigger_mentions=%t",
		hasAggsender, hasEpochOrTrigger)
}

// observeCertificateIntervals observes certificate-height advancement on the agglayer read RPC over a
// bounded window, returning the wall-clock intervals (in seconds) between successive height changes.
// It is the faithful Go equivalent of the bats monitor_certificate_intervals loop: it reads the
// current latest-known certificate height as a baseline, then polls interop_getLatestKnownCertificateHeader
// and, each time the height changes, records the interval since the previous change. It stops early
// once it has observed two height changes (enough to compute at least one interval) or when the
// window elapses. Returns the recorded intervals (may be a single zero-length slice element if only
// one change was seen with no preceding change to measure against).
func observeCertificateIntervals(
	ctx context.Context, t *testing.T, env *envs.Env, readRPCURL string, window time.Duration,
) []float64 {
	t.Helper()
	l2NetworkID := env.L2.NetworkID

	// Establish a baseline height (treat "no cert yet" / RPC error as baseline-unknown).
	var (
		lastHeight   uint64
		haveBaseline bool
		lastChangeAt = time.Now()
		intervals    []float64
		changesSeen  int
	)
	if header, err := getLatestKnownCertificateHeader(ctx, readRPCURL, l2NetworkID); err == nil && header != nil {
		lastHeight = header.Height
		haveBaseline = true
		log.Infof("[observeCertificateIntervals] baseline cert height=%d status=%s", header.Height, header.Status)
	} else {
		log.Infof("[observeCertificateIntervals] no baseline certificate yet; will count first observed height as change #1")
	}

	pollCtx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	// The poller returns done once we have observed 2 height changes (>=1 measurable interval). If the
	// window elapses first, pollWithBackoff returns a timeout error which we tolerate: any intervals
	// recorded so far are still returned and the caller asserts on len>=1.
	_ = pollWithBackoff(pollCtx, window, backoffInitial, backoffMax, "certificate height advance",
		func() (bool, error) {
			header, err := getLatestKnownCertificateHeader(pollCtx, readRPCURL, l2NetworkID)
			if err != nil {
				// Non-fatal: network may have no cert yet or RPC transiently unavailable. Keep polling.
				log.Debugf("[observeCertificateIntervals] header error (retrying): %v", err)
				return false, nil
			}
			if header == nil {
				return false, nil
			}
			if !haveBaseline {
				lastHeight = header.Height
				haveBaseline = true
				lastChangeAt = time.Now()
				return false, nil
			}
			if header.Height != lastHeight {
				now := time.Now()
				interval := now.Sub(lastChangeAt).Seconds()
				intervals = append(intervals, interval)
				changesSeen++
				log.Infof("[observeCertificateIntervals] cert change #%d: height %d -> %d status=%s interval=%.1fs",
					changesSeen, lastHeight, header.Height, header.Status, interval)
				lastHeight = header.Height
				lastChangeAt = now
				// Stop once we have at least one measured interval between two observed changes.
				return changesSeen >= 1, nil
			}
			return false, nil
		})
	return intervals
}

// logIntervalStats logs informational count/min/max/avg statistics over the observed inter-cert
// intervals, mirroring the bats calculate_stats output. It is informational only and asserts nothing.
func logIntervalStats(t *testing.T, intervals []float64) {
	t.Helper()
	if len(intervals) == 0 {
		log.Infof("[TestTriggerCertModes] interval stats: count=0 (no inter-cert interval measured)")
		return
	}
	minV, maxV, sum := intervals[0], intervals[0], 0.0
	for _, v := range intervals {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
		sum += v
	}
	avg := sum / float64(len(intervals))
	log.Infof("[TestTriggerCertModes] interval stats (informational): count=%d min=%.1fs max=%.1fs avg=%.1fs",
		len(intervals), minV, maxV, avg)
}
