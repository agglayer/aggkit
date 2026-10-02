package l1infotreesync_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// serveGET is a minimal local stand-in for bridgeservice's own (unexported) performRequest
// helper: this package cannot reach it, and a real bridgeservice.BridgeService is otherwise
// wired up exactly as production code does, through RegisterRoutes and httptest.
func serveGET(router *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// TestBridgeServiceReportsL1InfoTreeHalt drives a real (non-mocked) l1infotreesync processor
// through halt and unhalt and asserts that both /bridge/v1/sync-status and /health report the
// transition consistently, wired up end to end through a real bridgeservice.BridgeService and
// httptest. This exercises the processor.halted -> IsActive -> both-endpoints chain against an
// actual processor (real halt()/unhalt() semantics), which the mock-based bridgeservice test
// suite never does since it always substitutes a mock. The race between IsActive and the
// following data call (the syncer halting in between the two) is instead covered
// deterministically by that mock suite's halted_between_calls cases, which can force that
// ordering exactly; a real processor's halt timing can't be forced tightly enough to do the same
// here.
func TestBridgeServiceReportsL1InfoTreeHalt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	s, err := l1infotreesync.NewReadOnly(ctx, path.Join(t.TempDir(), "l1info.sqlite"))
	require.NoError(t, err)

	cfg := &bridgeservice.Config{
		Logger:      log.WithFields("module", "test bridge service"),
		ReadTimeout: time.Second,
		// Tiny but positive: zero would fall back to the (multi-second) package default, which
		// would mask the halt/unhalt transitions below behind the health cache.
		HealthCheckCacheTTL: cfgtypes.Duration{Duration: time.Nanosecond},
	}
	// Every other syncer is an untyped nil, so bridgeservice.New receives genuine nil interfaces
	// (not a typed nil wrapped in a non-nil interface) and treats them as unconfigured.
	bs := bridgeservice.New(cfg, nil, s, nil, nil, nil, nil, nil)
	router := gin.New()
	bs.RegisterRoutes(router)

	// outlastCache sleeps past HealthCheckCacheTTL so the next /health call recomputes instead of
	// returning a stale cached result from before a halt/unhalt transition.
	outlastCache := func() { time.Sleep(2 * time.Millisecond) }

	requireHealthy := func() {
		w := serveGET(router, "/bridge/v1/sync-status")
		require.Equal(t, http.StatusOK, w.Code)

		var status bridgetypes.SyncStatus
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
		require.NotNil(t, status.L1InfoTreeInfo)
		require.True(t, status.L1InfoTreeInfo.IsActive)
		require.False(t, status.L1InfoTreeInfo.IsHalted)
		require.Empty(t, status.L1InfoTreeInfo.Error)
		require.Zero(t, status.L1InfoTreeInfo.LastProcessedBlock)
		// No bridge syncer is wired on this instance.
		require.False(t, status.L1Info.IsActive)
		require.False(t, status.L1Info.IsHalted)
		require.False(t, status.L2Info.IsActive)
		require.False(t, status.L2Info.IsHalted)

		outlastCache()
		wh := serveGET(router, "/health")
		require.Equal(t, http.StatusOK, wh.Code)

		var health bridgetypes.HealthCheckResponse
		require.NoError(t, json.Unmarshal(wh.Body.Bytes(), &health))
		require.Equal(t, bridgetypes.HealthSyncStatusDone, health.SyncStatus)
		require.NotNil(t, health.Details.L1InfoTree)
		require.True(t, health.Details.L1InfoTree.IsActive)
		require.False(t, health.Details.L1InfoTree.IsHalted)
		require.Empty(t, health.Details.L1InfoTree.Error)
		require.Nil(t, health.Details.L1, "no bridge syncer configured on this instance")
		require.Nil(t, health.Details.L2, "no bridge syncer configured on this instance")
	}

	requireHalted := func() {
		w := serveGET(router, "/bridge/v1/sync-status")
		require.Equal(t, http.StatusOK, w.Code)
		// The halt reason must never leak onto the public API.
		require.NotContains(t, w.Body.String(), "test halt reason")

		var status bridgetypes.SyncStatus
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
		require.NotNil(t, status.L1InfoTreeInfo)
		require.False(t, status.L1InfoTreeInfo.IsActive)
		require.True(t, status.L1InfoTreeInfo.IsHalted)
		require.Empty(t, status.L1InfoTreeInfo.Error)

		outlastCache()
		wh := serveGET(router, "/health")
		require.Equal(t, http.StatusOK, wh.Code)
		require.NotContains(t, wh.Body.String(), "test halt reason")

		var health bridgetypes.HealthCheckResponse
		require.NoError(t, json.Unmarshal(wh.Body.Bytes(), &health))
		require.Equal(t, bridgetypes.HealthSyncStatusError, health.SyncStatus)
		require.NotNil(t, health.Details.L1InfoTree)
		require.False(t, health.Details.L1InfoTree.IsActive)
		require.True(t, health.Details.L1InfoTree.IsHalted)
		require.Empty(t, health.Details.L1InfoTree.Error)
	}

	requireHealthy()

	s.HaltForTest("test halt reason")
	requireHalted()

	s.UnhaltForTest()
	requireHealthy()
}
