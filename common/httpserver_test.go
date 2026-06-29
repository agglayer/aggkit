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

func (m *mockHTTPLogger) Panicf(_ string, _ ...interface{})           {}
func (m *mockHTTPLogger) Fatalf(_ string, _ ...interface{})           {}
func (m *mockHTTPLogger) Info(_ ...interface{})                       {}
func (m *mockHTTPLogger) Infof(_ string, _ ...interface{})            {}
func (m *mockHTTPLogger) Error(_ ...interface{})                      {}
func (m *mockHTTPLogger) Errorf(_ string, _ ...interface{})           {}
func (m *mockHTTPLogger) Warn(_ ...interface{})                       {}
func (m *mockHTTPLogger) Warnf(_ string, _ ...interface{})            {}
func (m *mockHTTPLogger) Debug(_ ...interface{})                      {}
func (m *mockHTTPLogger) Debugf(format string, args ...interface{}) {
	m.debugfCalls = append(m.debugfCalls, fmt.Sprintf(format, args...))
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
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
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	addr := cfg.Address()
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + addr + "/health")
		if err == nil {
			resp.Body.Close()
			return true
		}
		return false
	}, 3*time.Second, 20*time.Millisecond)

	cancel()
	require.NoError(t, <-errCh)
}

func TestHTTPServerStartPortConflict(t *testing.T) {
	t.Setenv("GIN_MODE", gin.TestMode)
	cfg := testRESTConfig(t)

	l, err := net.Listen("tcp", cfg.Address())
	require.NoError(t, err)
	defer l.Close()

	srv := NewHTTPServer(cfg, nil)
	err = srv.Start(context.Background())
	require.ErrorContains(t, err, "httpserver ListenAndServe")
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
