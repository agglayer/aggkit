package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/agglayer/aggkit/common/mocks"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	// testStallTimeout is the upstream timeout configured in the tests that expect it to expire
	testStallTimeout = 200 * time.Millisecond
	// testWait bounds every wait of these tests, so a regression fails them instead of hanging
	testWait = 5 * time.Second
)

// testClient is the (bounded) client of these tests. It never adds nor decodes Accept-Encoding
// on its own, so what the proxy relays is what the test sees
var testClient = &http.Client{
	Timeout:   testWait,
	Transport: &http.Transport{DisableCompression: true},
}

func duration(d time.Duration) types.Duration {
	return types.Duration{Duration: d}
}

// newBackend starts a bridge service stub running handler. The handler gets a channel that is
// closed on cleanup before the server is (closing a server waits for its in-flight handlers): a
// handler blocked on it can never keep the test hanging
func newBackend(
	t *testing.T,
	handler func(w http.ResponseWriter, r *http.Request, release <-chan struct{}),
) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, release)
	}))
	// cleanups run last to first: release the handlers, then close the server
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	return server
}

// stallUntilCancelled blocks until the request's context is cancelled (closing cancelled, so the
// test can tell the proxy cancelled the upstream request) or the test is over
func stallUntilCancelled(r *http.Request, release <-chan struct{}, cancelled chan<- struct{}) {
	select {
	case <-r.Context().Done():
		close(cancelled)
	case <-release:
	}
}

func requireClosed(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(testWait):
		require.FailNow(t, "timed out waiting: "+msg)
	}
}

func doRequest(t *testing.T, method, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	require.NoError(t, err)
	resp, err := testClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

// doRawRequest sends rawRequest as is over a new connection and returns the response, for the
// requests an http.Client does not build (e.g. a GET with an empty chunked body)
func doRawRequest(t *testing.T, server *httptest.Server, rawRequest string) *http.Response {
	t.Helper()
	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), testWait)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(testWait)))

	_, err = conn.Write([]byte(rawRequest))
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestUpstreamHeaderStallAnswers504(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"UpstreamResponseHeaderTimeout", Config{UpstreamResponseHeaderTimeout: duration(testStallTimeout)}},
		{"UpstreamRequestTimeout", Config{UpstreamRequestTimeout: duration(testStallTimeout)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cancelled := make(chan struct{})
			backend := newBackend(t, func(_ http.ResponseWriter, r *http.Request, release <-chan struct{}) {
				stallUntilCancelled(r, release, cancelled)
			})
			server, _ := newTestProxyWithConfig(t, tc.cfg, fakeResolver{1: backend.URL})

			start := time.Now()
			resp := doRequest(t, http.MethodGet, server.URL+"/bridge/v1/bridges?network_id=1")

			require.Equal(t, http.StatusGatewayTimeout, resp.StatusCode)
			// the body is generic: the backend's URL stays in the logs
			body := readBody(t, resp)
			require.JSONEq(t, `{"error":"bridge service timed out"}`, body)
			require.NotContains(t, body, backend.URL)
			require.Less(t, time.Since(start), testWait)
			requireClosed(t, cancelled, "the upstream request context to be cancelled")
		})
	}
}

func TestUpstreamBodyStallAbortsResponse(t *testing.T) {
	cancelled := make(chan struct{})
	backend := newBackend(t, func(w http.ResponseWriter, r *http.Request, release <-chan struct{}) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"bridges":[`))
		_ = http.NewResponseController(w).Flush()
		stallUntilCancelled(r, release, cancelled)
	})
	// UpstreamResponseHeaderTimeout does not apply once the headers are in: only the total
	// deadline bounds the rest of the response
	server, _ := newTestProxyWithConfig(t, Config{
		UpstreamResponseHeaderTimeout: duration(time.Minute),
		UpstreamRequestTimeout:        duration(testStallTimeout),
	}, fakeResolver{1: backend.URL})

	resp := doRequest(t, http.MethodGet, server.URL+"/bridge/v1/bridges?network_id=1")
	// the status is already sent when the deadline expires: it can't change anymore
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// the response is cut and the client must be able to tell: a body that just ends would look
	// complete
	start := time.Now()
	body, err := io.ReadAll(resp.Body)
	require.Error(t, err, "a truncated response must not look complete, got body %q", body)
	require.Less(t, time.Since(start), testWait)
	requireClosed(t, cancelled, "the upstream request context to be cancelled")
}

func TestClientCancelPropagatesToUpstream(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	backend := newBackend(t, func(_ http.ResponseWriter, r *http.Request, release <-chan struct{}) {
		close(started)
		stallUntilCancelled(r, release, cancelled)
	})
	// far from expiring: only the client going away can end the request
	server, _ := newTestProxyWithConfig(t, Config{
		UpstreamResponseHeaderTimeout: duration(time.Minute),
		UpstreamRequestTimeout:        duration(time.Minute),
	}, fakeResolver{1: backend.URL})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/bridge/v1/bridges?network_id=1", nil)
	require.NoError(t, err)
	clientErr := make(chan error, 1)
	go func() {
		resp, err := testClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		clientErr <- err
	}()

	requireClosed(t, started, "the request to reach the backend")
	cancel()
	requireClosed(t, cancelled, "the upstream request context to be cancelled")
	select {
	case err := <-clientErr:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(testWait):
		require.FailNow(t, "the client request did not end")
	}
}

func TestUpstreamResponseWithinLimitsIsRelayedUnchanged(t *testing.T) {
	backend := newBackend(t, func(w http.ResponseWriter, r *http.Request, _ <-chan struct{}) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bridge/v1/bridges", r.URL.Path)
		require.Equal(t, "7", r.URL.Query().Get("page_size"))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Backend", "yes")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"bridges":[]}`))
	})
	server, _ := newTestProxyWithConfig(t, Config{
		UpstreamResponseHeaderTimeout: duration(testWait),
		UpstreamRequestTimeout:        duration(testWait),
		UpstreamMaxConnsPerHost:       4,
	}, fakeResolver{1: backend.URL})

	resp := doRequest(t, http.MethodGet, server.URL+"/bridge/v1/bridges?network_id=1&page_size=7")
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.Equal(t, "yes", resp.Header.Get("X-Backend"))
	require.JSONEq(t, `{"bridges":[]}`, readBody(t, resp))
}

func TestHeadIsForwarded(t *testing.T) {
	backend := newBackend(t, func(w http.ResponseWriter, r *http.Request, _ <-chan struct{}) {
		require.Equal(t, http.MethodHead, r.Method)
		w.Header().Set("X-Backend", "yes")
	})
	server, _ := newTestProxy(t, fakeResolver{1: backend.URL})

	resp := doRequest(t, http.MethodHead, server.URL+"/bridge/v1/bridges?network_id=1")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "yes", resp.Header.Get("X-Backend"))
}

// TestStreamingResponseIsNotBuffered pins that the deadline does not turn the proxy into a
// buffering one: a chunk the backend flushes reaches the client while the response is not over
func TestStreamingResponseIsNotBuffered(t *testing.T) {
	firstChunkRead := make(chan struct{})
	backend := newBackend(t, func(w http.ResponseWriter, _ *http.Request, release <-chan struct{}) {
		_, _ = w.Write([]byte("chunk1"))
		_ = http.NewResponseController(w).Flush()
		// the second chunk is only sent once the client has the first one
		select {
		case <-firstChunkRead:
			_, _ = w.Write([]byte("chunk2"))
		case <-release:
		}
	})
	server, _ := newTestProxyWithConfig(t, Config{
		UpstreamResponseHeaderTimeout: duration(testWait),
		UpstreamRequestTimeout:        duration(testWait),
	}, fakeResolver{1: backend.URL})

	resp := doRequest(t, http.MethodGet, server.URL+"/bridge/v1/bridges?network_id=1")
	first := make([]byte, len("chunk1"))
	_, err := io.ReadFull(resp.Body, first)
	require.NoError(t, err)
	require.Equal(t, "chunk1", string(first))

	close(firstChunkRead)
	rest, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "chunk2", string(rest))
}

func TestAllProxiesShareTheConfiguredTransport(t *testing.T) {
	newStalledBackend := func() *httptest.Server {
		return newBackend(t, func(_ http.ResponseWriter, r *http.Request, release <-chan struct{}) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		})
	}
	backend1, backend2 := newStalledBackend(), newStalledBackend()
	server, svc := newTestProxyWithConfig(t, Config{
		UpstreamResponseHeaderTimeout: duration(testStallTimeout),
		UpstreamMaxConnsPerHost:       7,
	}, fakeResolver{1: backend1.URL, 2: backend2.URL})

	for _, networkID := range []string{"1", "2"} {
		resp := doRequest(t, http.MethodGet, server.URL+"/bridge/v1/bridges?network_id="+networkID)
		require.Equal(t, http.StatusGatewayTimeout, resp.StatusCode, "network %s", networkID)
	}

	require.Len(t, svc.proxies, 2)
	for url, proxy := range svc.proxies {
		require.Same(t, svc.transport, proxy.Transport, "proxy of %s", url)
	}
	require.Equal(t, testStallTimeout, svc.transport.ResponseHeaderTimeout)
	require.Equal(t, 7, svc.transport.MaxConnsPerHost)
	require.Equal(t, 7, svc.transport.MaxIdleConnsPerHost, "the whole cap can be reused")
	require.True(t, svc.transport.DisableCompression)

	// the shared default transport is cloned, never changed
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok)
	require.NotSame(t, defaultTransport, svc.transport)
	require.Zero(t, defaultTransport.ResponseHeaderTimeout)
	require.Zero(t, defaultTransport.MaxConnsPerHost)
	require.Zero(t, defaultTransport.MaxIdleConnsPerHost)
	require.False(t, defaultTransport.DisableCompression)

	// no cap: the idle pool per host keeps net/http's default
	unlimited := newUpstreamTransport(Config{})
	require.Zero(t, unlimited.MaxConnsPerHost)
	require.Zero(t, unlimited.MaxIdleConnsPerHost)
}

func TestNewUpstreamTransportWhenDefaultTransportWasReplaced(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("not used")
	})

	transport := newUpstreamTransport(Config{
		UpstreamResponseHeaderTimeout: duration(time.Second),
		UpstreamMaxConnsPerHost:       3,
	})
	require.Equal(t, time.Second, transport.ResponseHeaderTimeout)
	require.Equal(t, 3, transport.MaxConnsPerHost)
	require.True(t, transport.DisableCompression)
	require.NotZero(t, transport.TLSHandshakeTimeout)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestMaxConnsPerHostCapsStalledRequests pins that a flood of requests stalled by one backend is
// capped: the backend never sees more than UpstreamMaxConnsPerHost at once, and the requests
// waiting for a connection still end at the total deadline
func TestMaxConnsPerHostCapsStalledRequests(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	backend := newBackend(t, func(_ http.ResponseWriter, r *http.Request, release <-chan struct{}) {
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			highest := maxInFlight.Load()
			if current <= highest || maxInFlight.CompareAndSwap(highest, current) {
				break
			}
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	server, _ := newTestProxyWithConfig(t, Config{
		UpstreamRequestTimeout:  duration(testStallTimeout),
		UpstreamMaxConnsPerHost: 1,
	}, fakeResolver{1: backend.URL})

	const requests = 4
	statuses := make(chan int, requests)
	for range requests {
		go func() {
			resp, err := testClient.Get(server.URL + "/bridge/v1/bridges?network_id=1")
			if err != nil {
				statuses <- 0
				return
			}
			_ = resp.Body.Close()
			statuses <- resp.StatusCode
		}()
	}
	for range requests {
		select {
		case status := <-statuses:
			require.Equal(t, http.StatusGatewayTimeout, status)
		case <-time.After(testWait):
			require.FailNow(t, "a request did not end")
		}
	}
	require.EqualValues(t, 1, maxInFlight.Load())
}

// TestProxyDoesNotAddAcceptEncoding pins that the proxy neither asks the backend for gzip on its
// own (it would have to decompress, on a size only the backend controls) nor drops the client's
func TestProxyDoesNotAddAcceptEncoding(t *testing.T) {
	seen := make(chan string, 2)
	backend := newBackend(t, func(w http.ResponseWriter, r *http.Request, _ <-chan struct{}) {
		seen <- r.Header.Get("Accept-Encoding")
		w.WriteHeader(http.StatusOK)
	})
	server, _ := newTestProxy(t, fakeResolver{1: backend.URL})

	for _, acceptEncoding := range []string{"", "gzip"} {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/bridge/v1/bridges?network_id=1", nil)
		require.NoError(t, err)
		if acceptEncoding != "" {
			req.Header.Set("Accept-Encoding", acceptEncoding)
		}
		resp, err := testClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, acceptEncoding, <-seen)
	}
}

// hitCounter is a backend that counts the requests it gets and the body bytes in them
type hitCounter struct {
	server *httptest.Server
	hits   atomic.Int32
	bytes  atomic.Int64
}

func newHitCounter(t *testing.T) *hitCounter {
	t.Helper()
	h := &hitCounter{}
	h.server = newBackend(t, func(w http.ResponseWriter, r *http.Request, _ <-chan struct{}) {
		h.hits.Add(1)
		n, _ := io.Copy(io.Discard, r.Body)
		h.bytes.Add(n)
		w.WriteHeader(http.StatusOK)
	})
	return h
}

func TestOnlyGetAndHeadAreForwarded(t *testing.T) {
	backend := newHitCounter(t)
	server, svc := newTestProxy(t, fakeResolver{1: backend.server.URL})

	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
		http.MethodOptions, http.MethodTrace, http.MethodConnect,
	} {
		resp := doRequest(t, method, server.URL+"/bridge/v1/bridges?network_id=1")
		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, method)
		require.Equal(t, "GET, HEAD", resp.Header.Get("Allow"), method)
		if method != http.MethodHead {
			require.Contains(t, readBody(t, resp), "method not allowed", method)
		}
	}

	// a method no route knows is never forwarded either
	resp := doRequest(t, "FOO", server.URL+"/bridge/v1/bridges?network_id=1")
	require.GreaterOrEqual(t, resp.StatusCode, http.StatusBadRequest)

	require.Zero(t, backend.hits.Load(), "a rejected method must not reach the backend")
	require.Empty(t, svc.proxies, "a rejected method must not even resolve the backend")
}

func TestRequestWithBodyIsRejected(t *testing.T) {
	tests := []struct {
		name string
		body func(t *testing.T) io.Reader
	}{
		{"one byte", func(*testing.T) io.Reader { return strings.NewReader("x") }},
		{"bigger than the shared server limit", func(*testing.T) io.Reader {
			return bytes.NewReader(make([]byte, 2<<20))
		}},
		{"unbounded stream of unknown length", func(t *testing.T) io.Reader {
			t.Helper()
			pr, pw := io.Pipe()
			t.Cleanup(func() { _ = pr.Close() })
			go func() {
				defer pw.Close()
				chunk := bytes.Repeat([]byte("a"), 4096)
				for {
					if _, err := pw.Write(chunk); err != nil {
						return
					}
				}
			}()
			return pr
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend := newHitCounter(t)
			server, _ := newTestProxy(t, fakeResolver{1: backend.server.URL})

			req, err := http.NewRequest(http.MethodGet, server.URL+"/bridge/v1/bridges?network_id=1", tc.body(t))
			require.NoError(t, err)
			resp, err := testClient.Do(req)
			require.NoError(t, err)
			t.Cleanup(func() { _ = resp.Body.Close() })

			require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
			require.Zero(t, backend.hits.Load(), "the backend must not be contacted")
			require.Zero(t, backend.bytes.Load(), "no byte of the body must reach the backend")
		})
	}
}

func TestRejectedRequestBodyAnswerClosesConnection(t *testing.T) {
	backend := newHitCounter(t)
	server, _ := newTestProxy(t, fakeResolver{1: backend.server.URL})

	req, err := http.NewRequest(http.MethodGet, server.URL+"/bridge/v1/bridges?network_id=1", strings.NewReader("x"))
	require.NoError(t, err)
	resp, err := testClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	// the server must not read what the client keeps sending: it closes the connection
	require.True(t, resp.Close, "Connection: close expected")
	require.Contains(t, readBody(t, resp), "request body not allowed")
}

func TestChunkedRequestBody(t *testing.T) {
	t.Run("with data is rejected before reaching the backend", func(t *testing.T) {
		backend := newHitCounter(t)
		server, _ := newTestProxy(t, fakeResolver{1: backend.server.URL})

		resp := doRawRequest(t, server, "GET /bridge/v1/bridges?network_id=1 HTTP/1.1\r\n"+
			"Host: proxy\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n")

		require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
		require.Zero(t, backend.hits.Load())
		require.Zero(t, backend.bytes.Load())
	})

	t.Run("empty is forwarded without a body", func(t *testing.T) {
		var forwarded atomic.Bool
		backend := newBackend(t, func(w http.ResponseWriter, r *http.Request, _ <-chan struct{}) {
			forwarded.Store(true)
			require.Zero(t, r.ContentLength)
			require.Empty(t, r.TransferEncoding)
			w.WriteHeader(http.StatusOK)
		})
		server, _ := newTestProxy(t, fakeResolver{1: backend.URL})

		resp := doRawRequest(t, server, "GET /bridge/v1/bridges?network_id=1 HTTP/1.1\r\n"+
			"Host: proxy\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n")

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.True(t, forwarded.Load())
	})
}

func TestRequireNoBodyUnreadableBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/bridge/v1/bridges", iotest.ErrReader(errors.New("boom")))
	c.Request.ContentLength = -1

	require.False(t, requireNoBody(c))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestConfigValidate(t *testing.T) {
	require.NoError(t, (&Config{}).Validate())
	require.NoError(t, (&Config{
		UpstreamResponseHeaderTimeout: duration(30 * time.Second),
		UpstreamRequestTimeout:        duration(2 * time.Minute),
		UpstreamMaxConnsPerHost:       256,
	}).Validate())

	require.ErrorContains(t,
		(&Config{UpstreamResponseHeaderTimeout: duration(-time.Second)}).Validate(),
		"UpstreamResponseHeaderTimeout")
	require.ErrorContains(t,
		(&Config{UpstreamRequestTimeout: duration(-time.Second)}).Validate(),
		"UpstreamRequestTimeout")
	require.ErrorContains(t,
		(&Config{UpstreamMaxConnsPerHost: -1}).Validate(),
		"UpstreamMaxConnsPerHost")
}

func TestConfigWarnings(t *testing.T) {
	cfg := Config{UpstreamRequestTimeout: duration(2 * time.Minute)}

	require.Empty(t, cfg.Warnings(5*time.Minute))
	require.Empty(t, cfg.Warnings(0), "no REST write timeout: nothing to outlive")
	require.Empty(t, (&Config{}).Warnings(time.Minute), "no total deadline: nothing to compare")

	require.Len(t, cfg.Warnings(2*time.Minute), 1, "equal to the write timeout is not below it")
	warnings := cfg.Warnings(time.Minute)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "UpstreamRequestTimeout")
	require.Contains(t, warnings[0], "WriteTimeout")

	// the response-header timeout has the same dead zone: the write deadline is already set when it
	// starts counting
	headerCfg := Config{UpstreamResponseHeaderTimeout: duration(30 * time.Second)}
	require.Empty(t, headerCfg.Warnings(time.Minute))
	warnings = headerCfg.Warnings(30 * time.Second)
	require.Len(t, warnings, 1, "equal to the write timeout is not below it")
	require.Contains(t, warnings[0], "UpstreamResponseHeaderTimeout")

	both := Config{
		UpstreamResponseHeaderTimeout: duration(2 * time.Minute),
		UpstreamRequestTimeout:        duration(3 * time.Minute),
	}
	require.Len(t, both.Warnings(time.Minute), 2)
}

// TestServeProxiedOnlySwallowsAbortsItCanCloseTheConnectionOf pins what serveProxied does not
// recover: a panic other than http.ErrAbortHandler, and an abort on a writer it cannot hijack (it
// is left to net/http, which closes the connection itself)
func TestServeProxiedOnlySwallowsAbortsItCanCloseTheConnectionOf(t *testing.T) {
	svc := New(Config{Logger: log.WithFields("module", "proxy-test")}, fakeResolver{})
	panicking := func(value any) *httputil.ReverseProxy {
		return &httputil.ReverseProxy{
			Rewrite: func(*httputil.ProxyRequest) {},
			Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("backend down")
			}),
			ErrorHandler: func(http.ResponseWriter, *http.Request, error) { panic(value) },
		}
	}
	errOther := errors.New("not an abort")
	for _, value := range []any{"not an error", errOther, http.ErrAbortHandler} {
		req := httptest.NewRequest(http.MethodGet, "/bridge/v1/bridges", nil)
		require.PanicsWithValue(t, value, func() {
			// a ResponseRecorder cannot be hijacked
			svc.serveProxied(panicking(value), "http://backend", httptest.NewRecorder(), req)
		})
	}
}

// TestUpstreamErrorLogLevel pins that a client going away is logged at debug level (it is no
// bridge service failure), while a real upstream failure, a cancellation the client did not cause
// included, stays an error
func TestUpstreamErrorLogLevel(t *testing.T) {
	newProxy := func(t *testing.T) (*httputil.ReverseProxy, *mocks.Logger) {
		t.Helper()
		logger := mocks.NewLogger(t)
		logger.EXPECT().Info(mock.Anything).Once()
		svc := New(Config{Logger: logger}, fakeResolver{})
		p, err := svc.proxyFor("http://backend")
		require.NoError(t, err)
		return p, logger
	}
	request := func(ctx context.Context) *http.Request {
		return httptest.NewRequest(http.MethodGet, "/bridge/v1/bridges", nil).WithContext(ctx)
	}

	t.Run("the client went away", func(t *testing.T) {
		p, logger := newProxy(t)
		// format, method, path and backend URL
		logger.EXPECT().Debugf(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Once()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		p.ErrorHandler(httptest.NewRecorder(), request(ctx), context.Canceled)
	})

	t.Run("the upstream request timed out", func(t *testing.T) {
		p, logger := newProxy(t)
		// format, method, path, backend URL, status and error
		logger.EXPECT().Errorf(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything,
			mock.Anything).Once()
		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		<-ctx.Done()
		recorder := httptest.NewRecorder()
		p.ErrorHandler(recorder, request(ctx), context.DeadlineExceeded)
		require.Equal(t, http.StatusGatewayTimeout, recorder.Code)
	})

	t.Run("a cancellation the client did not cause", func(t *testing.T) {
		p, logger := newProxy(t)
		logger.EXPECT().Errorf(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything,
			mock.Anything).Once()
		recorder := httptest.NewRecorder()
		p.ErrorHandler(recorder, request(context.Background()), context.Canceled)
		require.Equal(t, http.StatusBadGateway, recorder.Code)
	})
}
