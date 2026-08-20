package common

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type mockHTTPLogger struct {
	debugfCalls []string
}

func (m *mockHTTPLogger) Panicf(_ string, _ ...interface{}) {}
func (m *mockHTTPLogger) Fatalf(_ string, _ ...interface{}) {}
func (m *mockHTTPLogger) Info(_ ...interface{})             {}
func (m *mockHTTPLogger) Infof(_ string, _ ...interface{})  {}
func (m *mockHTTPLogger) Error(_ ...interface{})            {}
func (m *mockHTTPLogger) Errorf(_ string, _ ...interface{}) {}
func (m *mockHTTPLogger) Warn(_ ...interface{})             {}
func (m *mockHTTPLogger) Warnf(_ string, _ ...interface{})  {}
func (m *mockHTTPLogger) Debug(_ ...interface{})            {}
func (m *mockHTTPLogger) Debugf(format string, args ...interface{}) {
	m.debugfCalls = append(m.debugfCalls, fmt.Sprintf(format, args...))
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tcpAddr, ok := l.Addr().(*net.TCPAddr)
	require.True(t, ok)
	port := tcpAddr.Port
	require.NoError(t, l.Close())
	return port
}

func testRESTConfig(t *testing.T) RESTConfig {
	t.Helper()
	return RESTConfig{
		Host:         "127.0.0.1",
		Port:         freePort(t),
		ReadTimeout:  configtypes.Duration{Duration: 5 * time.Second},
		WriteTimeout: configtypes.Duration{Duration: 5 * time.Second},
	}
}

func TestNewHTTPServerDefaultGINMode(t *testing.T) {
	// Ensure GIN_MODE is unset so the default branch executes.
	t.Setenv("GIN_MODE", "")
	cfg := testRESTConfig(t)
	srv := NewHTTPServer(cfg, nil)
	require.NotNil(t, srv)
	require.NotNil(t, srv.Engine())
}

func TestNewHTTPServerGINModeOverride(t *testing.T) {
	t.Setenv("GIN_MODE", gin.TestMode)
	cfg := testRESTConfig(t)
	srv := NewHTTPServer(cfg, nil)
	require.NotNil(t, srv)
	require.NotNil(t, srv.Engine())
}

func TestNewHTTPServerWithLogger(t *testing.T) {
	t.Setenv("GIN_MODE", gin.TestMode)
	cfg := testRESTConfig(t)
	log := &mockHTTPLogger{}
	srv := NewHTTPServer(cfg, log)
	require.NotNil(t, srv)
	require.NotNil(t, srv.Engine())
}

func TestHTTPServerEngineNotNil(t *testing.T) {
	t.Setenv("GIN_MODE", gin.TestMode)
	cfg := testRESTConfig(t)
	srv := NewHTTPServer(cfg, nil)
	engine := srv.Engine()
	require.NotNil(t, engine)
	require.IsType(t, &gin.Engine{}, engine)
	// Verify route registration works.
	engine.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
}

func TestHTTPServerStartGracefulShutdown(t *testing.T) {
	t.Setenv("GIN_MODE", gin.TestMode)
	cfg := testRESTConfig(t)
	srv := NewHTTPServer(cfg, nil)
	srv.Engine().GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, srv.Start(ctx))

	// Once Start returns, the listener is bound and requests are served.
	addr := cfg.Address()
	resp, err := http.Get("http://" + addr + "/health")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)

	cancel()
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + addr + "/health")
		if err != nil {
			return true
		}
		resp.Body.Close()
		return false
	}, 3*time.Second, 20*time.Millisecond)
}

func TestHTTPServerStartPortConflict(t *testing.T) {
	t.Setenv("GIN_MODE", gin.TestMode)
	cfg := testRESTConfig(t)

	l, err := net.Listen("tcp", cfg.Address())
	require.NoError(t, err)
	defer l.Close()

	srv := NewHTTPServer(cfg, nil)
	err = srv.Start(context.Background())
	require.ErrorContains(t, err, "failed to listen on")
}

func TestHTTPLoggerHandlerWithQueryString(t *testing.T) {
	gin.SetMode(gin.TestMode)
	log := &mockHTTPLogger{}
	engine := gin.New()
	engine.Use(HTTPLoggerHandler(log))
	engine.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test?foo=bar", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	require.NotEmpty(t, log.debugfCalls)
	require.Contains(t, log.debugfCalls[0], "foo=bar")
}

func TestHTTPServerStartCORSDisabledByDefault(t *testing.T) {
	t.Setenv("GIN_MODE", gin.TestMode)
	cfg := testRESTConfig(t)
	srv := NewHTTPServer(cfg, nil)
	srv.Engine().GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, srv.Start(ctx))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+cfg.Address()+"/health", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://example.com")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
}

func TestHTTPServerStartCORSEnabled(t *testing.T) {
	t.Setenv("GIN_MODE", gin.TestMode)
	cfg := testRESTConfig(t)
	cfg.CORS = CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://example.com"},
		AllowedMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowedHeaders: []string{"Content-Type"},
		MaxAge:         configtypes.Duration{Duration: 12 * time.Hour},
	}
	srv := NewHTTPServer(cfg, nil)
	srv.Engine().GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, srv.Start(ctx))

	// Simple (non-preflight) request from an allowed origin gets the origin echoed back.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+cfg.Address()+"/health", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://example.com")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "https://example.com", resp.Header.Get("Access-Control-Allow-Origin"))

	// A request from a non-allowed origin does not get CORS headers, so the
	// browser blocks the response from being read cross-origin.
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+cfg.Address()+"/health", nil)
	require.NoError(t, err)
	req2.Header.Set("Origin", "https://not-allowed.com")

	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()

	require.Empty(t, resp2.Header.Get("Access-Control-Allow-Origin"))

	// Preflight request advertises the configured methods/headers/max-age.
	preflight, err := http.NewRequestWithContext(ctx, http.MethodOptions, "http://"+cfg.Address()+"/health", nil)
	require.NoError(t, err)
	preflight.Header.Set("Origin", "https://example.com")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	// Per the Fetch spec, browsers send this lowercased; mimic that here since
	// rs/cors compares it case-sensitively against the allow-list.
	preflight.Header.Set("Access-Control-Request-Headers", "content-type")

	preflightResp, err := http.DefaultClient.Do(preflight)
	require.NoError(t, err)
	defer preflightResp.Body.Close()

	require.Equal(t, "https://example.com", preflightResp.Header.Get("Access-Control-Allow-Origin"))
	require.Contains(t, preflightResp.Header.Get("Access-Control-Allow-Methods"), http.MethodGet)
	require.Equal(t, "content-type", preflightResp.Header.Get("Access-Control-Allow-Headers"))
	require.Equal(t, fmt.Sprintf("%d", int((12*time.Hour).Seconds())), preflightResp.Header.Get("Access-Control-Max-Age"))
}

func TestHTTPServerStartCORSAllowCredentialsReflectsOriginInsteadOfWildcard(t *testing.T) {
	t.Setenv("GIN_MODE", gin.TestMode)
	cfg := testRESTConfig(t)
	cfg.CORS = CORSConfig{
		Enabled:          true,
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{http.MethodGet},
		AllowCredentials: true,
	}
	srv := NewHTTPServer(cfg, nil)
	srv.Engine().GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, srv.Start(ctx))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+cfg.Address()+"/health", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://example.com")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Per the CORS spec, credentials cannot be combined with a wildcard origin,
	// so the caller's origin must be reflected back instead of "*".
	require.Equal(t, "https://example.com", resp.Header.Get("Access-Control-Allow-Origin"))
	require.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))
}

func TestCorsHandlerMapsConfig(t *testing.T) {
	cfg := CORSConfig{
		AllowedOrigins:   []string{"https://example.com"},
		AllowedMethods:   []string{http.MethodGet},
		AllowedHeaders:   []string{"Content-Type"},
		AllowCredentials: true,
		MaxAge:           configtypes.Duration{Duration: 30 * time.Minute},
	}

	handler := corsHandler(cfg)
	require.NotNil(t, handler)

	gin.SetMode(gin.TestMode)
	base := gin.New()
	base.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	preflight := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	preflight.Header.Set("Origin", "https://example.com")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	w := httptest.NewRecorder()
	handler.Handler(base).ServeHTTP(w, preflight)

	require.Equal(t, "https://example.com", w.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
	require.Equal(t, fmt.Sprintf("%d", int((30*time.Minute).Seconds())), w.Header().Get("Access-Control-Max-Age"))
}

func TestHTTPLoggerHandlerWithoutQueryString(t *testing.T) {
	gin.SetMode(gin.TestMode)
	log := &mockHTTPLogger{}
	engine := gin.New()
	engine.Use(HTTPLoggerHandler(log))
	engine.GET("/plain", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/plain", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	require.NotEmpty(t, log.debugfCalls)
	require.Contains(t, log.debugfCalls[0], "/plain")
	require.NotContains(t, log.debugfCalls[0], "?")
}
