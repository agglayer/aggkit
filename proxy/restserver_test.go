package proxy

import (
	"net"
	"net/http"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// pingRegisterer is a RoutesRegisterer registering a /ping route
type pingRegisterer struct{}

func (pingRegisterer) RegisterRoutes(router gin.IRouter) {
	router.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
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

func testRESTConfig(t *testing.T) aggkitcommon.RESTConfig {
	t.Helper()
	return aggkitcommon.RESTConfig{
		Host:         "127.0.0.1",
		Port:         freePort(t),
		ReadTimeout:  configtypes.Duration{Duration: 5 * time.Second},
		WriteTimeout: configtypes.Duration{Duration: 5 * time.Second},
	}
}

func TestRESTServerStartServesRegisteredRoutes(t *testing.T) {
	t.Setenv("GIN_MODE", gin.TestMode)
	cfg := testRESTConfig(t)
	srv := NewRESTServer(cfg, log.WithFields("module", "restserver-test"))
	srv.Register(pingRegisterer{})

	require.NoError(t, srv.Start(t.Context()))

	resp, err := http.Get("http://" + cfg.Address() + "/ping")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRESTServerStartWithoutRoutesIsNoOp(t *testing.T) {
	t.Setenv("GIN_MODE", gin.TestMode)
	cfg := testRESTConfig(t)
	srv := NewRESTServer(cfg, log.WithFields("module", "restserver-test"))

	require.NoError(t, srv.Start(t.Context()))

	// The listener must not be bound when no component registered routes
	_, err := net.DialTimeout("tcp", cfg.Address(), 100*time.Millisecond)
	require.Error(t, err)
}

func TestRESTServerStartPortConflict(t *testing.T) {
	t.Setenv("GIN_MODE", gin.TestMode)
	cfg := testRESTConfig(t)

	// Occupy the port so Start fails to bind
	l, err := net.Listen("tcp", cfg.Address())
	require.NoError(t, err)
	defer l.Close()

	srv := NewRESTServer(cfg, log.WithFields("module", "restserver-test"))
	srv.Register(pingRegisterer{})

	err = srv.Start(t.Context())
	require.ErrorContains(t, err, "failed to start REST server")
}
