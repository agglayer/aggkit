package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/stretchr/testify/require"
)

// fetchBridgeServiceHealth performs a raw GET against path (either "/" or "/health") on the
// Anvil env's aggkit-001 bridge-service REST API and decodes the body as a
// types.HealthCheckResponse. It returns the HTTP status code alongside the decoded body so
// callers can assert both independently, matching HealthCheckHandler's always-200 contract
// (see bridgeservice.BridgeService.HealthCheckHandler's doc comment).
func fetchBridgeServiceHealth(ctx context.Context, path string) (int, *types.HealthCheckResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bridgeServiceBaseURL+path, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	var health types.HealthCheckResponse
	if err := json.Unmarshal(body, &health); err != nil {
		return resp.StatusCode, nil, fmt.Errorf("decode health response %q: %w", strings.TrimSpace(string(body)), err)
	}
	return resp.StatusCode, &health, nil
}

// requireValidSyncStatus asserts status is one of the three documented HealthSyncStatus values
// (issue #1689); a value outside that set would mean HealthCheckHandler regressed to leaking an
// internal/unexpected string through the JSON contract.
func requireValidSyncStatus(t *testing.T, status types.HealthSyncStatus) {
	t.Helper()
	require.Contains(t,
		[]types.HealthSyncStatus{types.HealthSyncStatusDone, types.HealthSyncStatusPending, types.HealthSyncStatusError},
		status,
	)
}

// TestBridgeServiceHealthSyncStatus exercises issue #1689: the bridge-service health check
// served identically at both "/" and "/health", always answering HTTP 200 regardless of
// sync_status (see HealthCheckHandler's doc comment for why), with a sync_status/details payload
// derived from the same computeSyncStatus feed as GET /bridge/v1/sync-status.
//
// Only a "done" observation is asserted for the settled-env case: anvil-2chains mines L1/L2
// blocks instantly and this test runs late in the suite (after the other tests have already
// driven bridge traffic through aggkit-001), so by the time it runs every configured syncer is
// caught up. Provoking a "pending" reading cheaply would need a syncer-lag knob (e.g. an
// artificial RPC delay or a paused block producer) that anvil-2chains does not expose today;
// adding one is a new env knob, which is out of scope here -- so this test
// only asserts "done" is reachable, and that whatever SyncStatus is observed along the way (the
// two polls below, and the cached-vs-fresh reads) is always one of the three documented values.
func TestBridgeServiceHealthSyncStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Poll "/" until the env is settled (sync_status == "done"), validating every observed
	// value along the way. Interval spacing eventually exceeds HealthCheckCacheTTL (2s, see
	// bridgeservice.DefaultHealthCheckCacheTTL) via pollWithBackoff's growth, so this is not
	// just reading one cached value repeatedly.
	var lastHealth *types.HealthCheckResponse
	err := pollWithBackoff(ctx, 90*time.Second, backoffInitial, backoffMax, "bridge-service sync_status done",
		func() (bool, error) {
			statusCode, health, ferr := fetchBridgeServiceHealth(ctx, "/")
			if ferr != nil {
				return false, nil //nolint:nilerr // transient, keep polling
			}
			require.Equal(t, http.StatusOK, statusCode, "GET / must always answer 200")
			requireValidSyncStatus(t, health.SyncStatus)
			lastHealth = health
			return health.SyncStatus == types.HealthSyncStatusDone, nil
		})
	require.NoError(t, err, "bridge-service sync_status never reached \"done\" on GET /")

	require.Equal(t, "ok", lastHealth.Status)
	require.NotEmpty(t, lastHealth.Version)
	require.False(t, lastHealth.Time.IsZero())
	require.True(t,
		lastHealth.Details.L1 != nil || lastHealth.Details.L2 != nil || lastHealth.Details.L2GER != nil,
		"a settled instance must report at least one configured sync component",
	)
	if lastHealth.Details.L1 != nil {
		require.True(t, lastHealth.Details.L1.IsActive)
		require.Empty(t, lastHealth.Details.L1.Error)
	}
	if lastHealth.Details.L2 != nil {
		require.True(t, lastHealth.Details.L2.IsActive)
		require.Empty(t, lastHealth.Details.L2.Error)
	}
	if lastHealth.Details.L2GER != nil {
		require.True(t, lastHealth.Details.L2GER.IsActive)
		require.Empty(t, lastHealth.Details.L2GER.Error)
	}

	// GET /health is the explicit alias added by #1689: it must serve the identical handler,
	// always 200, and (once settled, as established above) the same "done" sync_status.
	statusCode, health, err := fetchBridgeServiceHealth(ctx, "/health")
	require.NoError(t, err, "GET /health failed")
	require.Equal(t, http.StatusOK, statusCode, "GET /health must always answer 200")
	requireValidSyncStatus(t, health.SyncStatus)
	require.Equal(t, "ok", health.Status)
	require.Equal(t, types.HealthSyncStatusDone, health.SyncStatus, "env is already settled per the GET / poll above")
}
