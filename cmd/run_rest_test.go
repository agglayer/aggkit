package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	aggsenderrpc "github.com/agglayer/aggkit/aggsender/rpc"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/config"
	"github.com/agglayer/aggkit/log"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubAggsenderStorer struct{}

func (stubAggsenderStorer) GetCertificateByHeight(uint64) (*aggsendertypes.Certificate, error) {
	return nil, nil
}

func (stubAggsenderStorer) GetLastSentCertificate() (*aggsendertypes.Certificate, error) {
	return nil, nil
}

func (stubAggsenderStorer) SaveLastSentCertificate(context.Context, aggsendertypes.Certificate) error {
	return nil
}

type stubAggsender struct{}

func (stubAggsender) Info() aggsendertypes.AggsenderInfo { return aggsendertypes.AggsenderInfo{} }
func (stubAggsender) ForceTriggerCertificate()           {}

// stubRoutes registers one public and one admin route under a fixed path.
type stubRoutes struct {
	publicPath string
	adminPath  string
}

func (s stubRoutes) RegisterPublicRoutes(router gin.IRouter) {
	router.GET(s.publicPath, func(c *gin.Context) { c.Status(http.StatusOK) })
}

func (s stubRoutes) RegisterAdminRoutes(router gin.IRouter) {
	router.POST(s.adminPath, func(c *gin.Context) { c.Status(http.StatusOK) })
}

type stubBridgeHealth struct{}

func (stubBridgeHealth) RegisterHealthRoutes(router gin.IRouter) {
	router.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "bridge") })
	router.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "bridge") })
}

func newTestRoutes() (*restRoutes, *gin.Engine, *gin.Engine) {
	gin.SetMode(gin.TestMode)
	public, admin := gin.New(), gin.New()
	return newRESTRoutes(public, admin), public, admin
}

func routeSet(e *gin.Engine) map[string]struct{} {
	set := make(map[string]struct{})
	for _, r := range e.Routes() {
		set[r.Method+" "+r.Path] = struct{}{}
	}
	return set
}

func requireRoutes(t *testing.T, e *gin.Engine, want ...string) {
	t.Helper()
	set := routeSet(e)
	for _, route := range want {
		require.Contains(t, set, route)
	}
}

func TestDefaultConfigHasNoJSONRPCServer(t *testing.T) {
	require.NotContains(t, config.DefaultValues, "[RPC]")
	require.NotContains(t, config.DefaultValues, "5576")

	cfg, err := config.LoadFile(nil, "", true, false)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", cfg.AdminREST.Host)
	require.Equal(t, "0.0.0.0", cfg.PublicREST.Host)
	require.True(t, cfg.AggSender.EnableRPC)
}

func TestRESTRoutesAggsenderOnly(t *testing.T) {
	routes, public, admin := newTestRoutes()
	api := aggsenderrpc.NewAggsenderAPI(log.WithFields("module", "test"), stubAggsenderStorer{}, stubAggsender{})

	routes.addAggsender(api, true)
	routes.registerHealth(nil)

	require.True(t, routes.publicHasRoutes)
	require.True(t, routes.adminHasRoutes)
	requireRoutes(t, public,
		"GET /",
		"GET /health",
		"GET /aggsender/v1/status",
		"GET /aggsender/v1/certificate-header",
		"GET /aggsender/v1/certificate-bridge-exits",
	)
	requireRoutes(t, admin, "POST /aggsender/v1/trigger-certificate")
	require.NotContains(t, routeSet(public), "POST /aggsender/v1/trigger-certificate")
	require.NotContains(t, routeSet(admin), "GET /aggsender/v1/status")

	rec := httptest.NewRecorder()
	public.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"is_healthy": true`)
}

func TestRESTRoutesAggsenderDisabled(t *testing.T) {
	routes, public, admin := newTestRoutes()
	api := aggsenderrpc.NewAggsenderAPI(log.WithFields("module", "test"), stubAggsenderStorer{}, stubAggsender{})

	routes.addAggsender(api, false)
	routes.registerHealth(nil)

	require.False(t, routes.publicHasRoutes)
	require.False(t, routes.adminHasRoutes)
	require.Empty(t, public.Routes())
	require.Empty(t, admin.Routes())
}

func TestRESTRoutesBridge(t *testing.T) {
	routes, public, admin := newTestRoutes()
	routes.addPublicAPI(stubRoutes{publicPath: "/bridge/v1/bridges"})
	routes.registerHealth(stubBridgeHealth{})

	require.True(t, routes.publicHasRoutes)
	require.False(t, routes.adminHasRoutes)
	requireRoutes(t, public, "GET /", "GET /health", "GET /bridge/v1/bridges")
	require.Empty(t, admin.Routes())

	// A single health check is registered: the bridge one, not the generic one.
	rec := httptest.NewRecorder()
	public.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, "bridge", strings.TrimSpace(rec.Body.String()))
}

func TestRESTRoutesNoComponentNoHealth(t *testing.T) {
	routes, public, _ := newTestRoutes()
	routes.registerHealth(nil)

	require.False(t, routes.publicHasRoutes)
	require.Empty(t, public.Routes())
}

func TestRESTRoutesMultidownloaderAdminRoutesNeedDeveloperMode(t *testing.T) {
	md := stubRoutes{publicPath: "/multidownloader/v1/l1/status", adminPath: "/multidownloader/v1/l1/reorg"}

	routes, public, admin := newTestRoutes()
	routes.addMultidownloader(md, false)
	require.True(t, routes.publicHasRoutes)
	require.False(t, routes.adminHasRoutes)
	requireRoutes(t, public, "GET /multidownloader/v1/l1/status")
	require.Empty(t, admin.Routes())

	routes, _, admin = newTestRoutes()
	routes.addMultidownloader(md, true)
	require.True(t, routes.adminHasRoutes)
	requireRoutes(t, admin, "POST /multidownloader/v1/l1/reorg")
}
