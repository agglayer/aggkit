package e2e

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// proxyLimitsWait bounds every request of TestProxyUpstreamLimitsOnRealBinary, so a proxy that
// hangs fails the test instead of stalling it
const proxyLimitsWait = 30 * time.Second

// TestProxyUpstreamLimitsOnRealBinary checks, on the aggkit-proxy binary of the env and its real
// bridge services, the wire contract of the upstream limits: with the [Proxy] upstream
// limits it loads by default, a read of the bridge service API is still relayed; any method but
// GET/HEAD is answered 405 by the proxy itself, and a GET carrying a body (here chunked, with no
// Content-Length) 413, followed by the connection being closed. The stalled-upstream cases of
// #184 need a misbehaving bridge service and are covered by the proxy unit tests
// (proxy/service_limits_tcp_test.go)
func TestProxyUpstreamLimitsOnRealBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")
	if testEnv.Clients.BridgeServiceProxyURL == "" {
		t.Skip("this test exercises the aggkit-proxy, which this env does not run")
	}
	baseURL := testEnv.Clients.BridgeServiceProxyURL
	const syncStatusPath = "/bridge/v1/sync-status?network_id=1"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, waitForTrackerReady(ctx), "aggkit-proxy health check never succeeded")
	client := &http.Client{Timeout: proxyLimitsWait}

	t.Run("a GET is relayed to the bridge service", func(t *testing.T) {
		resp, err := client.Get(baseURL + syncStatusPath)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)
		require.Contains(t, string(body), `"l1_info"`, "not a sync-status answer: %s", body)
	})

	t.Run("other methods are answered 405 by the proxy", func(t *testing.T) {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			req, err := http.NewRequestWithContext(ctx, method, baseURL+syncStatusPath, strings.NewReader("{}"))
			require.NoError(t, err)
			resp, err := client.Do(req)
			require.NoError(t, err, method)
			_ = resp.Body.Close()
			require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, method)
			require.Equal(t, "GET, HEAD", resp.Header.Get("Allow"), method)
		}
	})

	t.Run("a GET with a chunked body is answered 413 and the connection closed", func(t *testing.T) {
		u, err := url.Parse(baseURL)
		require.NoError(t, err)
		conn, err := net.DialTimeout("tcp", u.Host, proxyLimitsWait)
		require.NoError(t, err)
		defer conn.Close()
		require.NoError(t, conn.SetDeadline(time.Now().Add(proxyLimitsWait)))

		_, err = conn.Write([]byte("GET " + syncStatusPath + " HTTP/1.1\r\nHost: " + u.Host + "\r\n" +
			"Transfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n"))
		require.NoError(t, err)
		reader := bufio.NewReader(conn)
		resp, err := http.ReadResponse(reader, nil)
		require.NoError(t, err)
		_, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
		require.True(t, resp.Close, "Connection: close expected")

		n, err := reader.Read(make([]byte, 1))
		require.Zero(t, n, "unexpected data after the 413")
		var netErr net.Error
		require.False(t, errors.As(err, &netErr) && netErr.Timeout(), "the proxy did not close the connection: %v", err)
		require.Error(t, err)
	})
}
