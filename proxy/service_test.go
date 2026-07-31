package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agglayer/aggkit/bridgeservice"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/log"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakeResolver is a fixed networkID -> bridge URL NetworkURLResolver
type fakeResolver map[uint32]string

func (f fakeResolver) GetURL(networkID uint32) (bridgeservicefinder.NetworkURLs, error) {
	u, ok := f[networkID]
	if !ok {
		return bridgeservicefinder.NetworkURLs{},
			fmt.Errorf("%w: %d", bridgeservicefinder.ErrURLNotFound, networkID)
	}
	return bridgeservicefinder.NetworkURLs{BridgeURL: u}, nil
}

// newTestProxy starts an HTTP server running the proxy service and returns it together with
// the service. A real server (instead of a bare ResponseRecorder) is required because
// httputil.ReverseProxy needs a ResponseWriter with the full http.Server surface
func newTestProxy(t *testing.T, finder NetworkURLResolver) (*httptest.Server, *Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	svc := New(Config{Logger: log.WithFields("module", "proxy-test")}, finder)
	svc.RegisterRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, svc
}

func doGet(t *testing.T, server *httptest.Server, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(server.URL + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

func TestBridgeV1PrefixMatchesBridgeService(t *testing.T) {
	require.Equal(t, bridgeservice.BridgeV1Prefix, BridgeV1Prefix)
}

func TestForwardHandlerForwardsToResolvedBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bridge/v1/bridges", r.URL.Path)
		require.Equal(t, "3", r.URL.Query().Get("network_id"))
		require.Equal(t, "10", r.URL.Query().Get("page_size"))
		require.NotEmpty(t, r.Header.Get("X-Forwarded-For"))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"bridges":[]}`))
	}))
	defer backend.Close()

	server, _ := newTestProxy(t, fakeResolver{3: backend.URL})

	status, body := doGet(t, server, "/bridge/v1/bridges?network_id=3&page_size=10")
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"bridges":[]}`, body)
}

func TestForwardHandlerPreservesBackendStatusAndBody(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"claim not found"}`))
	}))
	defer backend.Close()

	server, _ := newTestProxy(t, fakeResolver{1: backend.URL})

	status, body := doGet(t, server, "/bridge/v1/claims?network_id=1")
	require.Equal(t, http.StatusNotFound, status)
	require.JSONEq(t, `{"error":"claim not found"}`, body)
}

func TestForwardHandlerForwardsNonGETMethodsAndBody(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, `{"key":"value"}`, string(body))
		w.WriteHeader(http.StatusCreated)
	}))
	defer backend.Close()

	server, _ := newTestProxy(t, fakeResolver{2: backend.URL})

	resp, err := http.Post(server.URL+"/bridge/v1/future-endpoint?network_id=2",
		"application/json", strings.NewReader(`{"key":"value"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestForwardHandlerMissingNetworkID(t *testing.T) {
	server, _ := newTestProxy(t, fakeResolver{})

	status, body := doGet(t, server, "/bridge/v1/bridges")
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body, "missing mandatory query parameter")
}

func TestForwardHandlerInvalidNetworkID(t *testing.T) {
	server, _ := newTestProxy(t, fakeResolver{})

	for _, invalid := range []string{"abc", "-1", "4294967296"} {
		status, body := doGet(t, server, "/bridge/v1/bridges?network_id="+invalid)
		require.Equal(t, http.StatusBadRequest, status, "network_id=%s", invalid)
		require.Contains(t, body, "invalid network_id parameter", "network_id=%s", invalid)
	}
}

func TestForwardHandlerUnknownNetwork(t *testing.T) {
	server, _ := newTestProxy(t, fakeResolver{})

	status, body := doGet(t, server, "/bridge/v1/bridges?network_id=7")
	require.Equal(t, http.StatusNotFound, status)
	require.Contains(t, body, "bridge service url not found")
}

func TestForwardHandlerBackendUnreachable(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	backend.Close() // resolved URL points to a closed listener

	server, _ := newTestProxy(t, fakeResolver{4: backend.URL})

	status, body := doGet(t, server, "/bridge/v1/bridges?network_id=4")
	require.Equal(t, http.StatusBadGateway, status)
	require.JSONEq(t, `{"error":"bridge service unreachable"}`, body)
}

func TestForwardHandlerCachesProxyPerURL(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	server, svc := newTestProxy(t, fakeResolver{5: backend.URL})

	for range 2 {
		status, _ := doGet(t, server, "/bridge/v1/bridges?network_id=5")
		require.Equal(t, http.StatusOK, status)
	}
	require.Len(t, svc.proxies, 1)
}
