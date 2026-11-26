package proxy

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

// These tests replay the scenarios of unbounded upstream requests and of methods and
// request bodies forwarded as is end to end: the proxy service on the engine of the
// real shared HTTP server (recovery and body limit middlewares included), a real httptest.Server
// as the bridge service, and a raw TCP client that, like a misbehaving client, controls
// byte by byte what it sends and keeps its connection open. Besides the status the client gets,
// they account for the resources at stake: the proxy handlers still running and the
// TCP connections open to the bridge service.

const (
	// tcpStall is the upstream timeout of the tests that expect it to expire
	tcpStall = 200 * time.Millisecond
	// tcpWait bounds every wait, so a regression fails the test instead of hanging it
	tcpWait = 5 * time.Second
	// tcpPoll is the polling period of the require.Eventually checks
	tcpPoll = 10 * time.Millisecond

	tcpGetNetwork1 = "GET /bridge/v1/bridges?network_id=1 HTTP/1.1\r\nHost: proxy\r\n"
	// lastChunk ends a complete chunked body: a truncated response must never carry it
	lastChunk = "\r\n0\r\n\r\n"
)

// tcpLimits is the proxy configuration with the given upstream limits
func tcpLimits(responseHeaderTimeout, requestTimeout time.Duration, maxConnsPerHost int) Config {
	return Config{
		UpstreamResponseHeaderTimeout: types.Duration{Duration: responseHeaderTimeout},
		UpstreamRequestTimeout:        types.Duration{Duration: requestTimeout},
		UpstreamMaxConnsPerHost:       maxConnsPerHost,
	}
}

func storeMax(highest *atomic.Int32, value int32) {
	for {
		current := highest.Load()
		if value <= current || highest.CompareAndSwap(current, value) {
			return
		}
	}
}

// tcpUpstream is a bridge service stub that accounts for its TCP connections and its requests
type tcpUpstream struct {
	*httptest.Server
	// release is closed on cleanup, so a stalled handler never keeps the test hanging
	release chan struct{}

	accepted, open, maxOpen atomic.Int32 // TCP connections: ever accepted, open now, open at once
	inHandler, maxInHandler atomic.Int32 // requests being handled: now, at once
	bodyBytes               atomic.Int64 // request body bytes received
	cancelled               atomic.Int32 // requests whose context was cancelled while stalled
}

func newTCPUpstream(
	t *testing.T, handler func(u *tcpUpstream, w http.ResponseWriter, r *http.Request),
) *tcpUpstream {
	t.Helper()
	u := &tcpUpstream{release: make(chan struct{})}
	u.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		storeMax(&u.maxInHandler, u.inHandler.Add(1))
		defer u.inHandler.Add(-1)
		handler(u, w, r)
	}))
	u.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			u.accepted.Add(1)
			storeMax(&u.maxOpen, u.open.Add(1))
		case http.StateClosed, http.StateHijacked:
			u.open.Add(-1)
		case http.StateActive, http.StateIdle:
		}
	}
	u.Start()
	// cleanups run last to first: release the handlers, drop the connections, close the server
	t.Cleanup(u.Close)
	t.Cleanup(u.CloseClientConnections)
	t.Cleanup(func() { close(u.release) })
	return u
}

// stall blocks until the request is cancelled (counting it) or the test is over
func (u *tcpUpstream) stall(r *http.Request) {
	select {
	case <-r.Context().Done():
		u.cancelled.Add(1)
	case <-u.release:
	}
}

// countBody reads the request body to the end, counting every byte as it arrives
func (u *tcpUpstream) countBody(r *http.Request) {
	buf := make([]byte, 1024)
	for {
		n, err := r.Body.Read(buf)
		u.bodyBytes.Add(int64(n))
		if err != nil {
			return
		}
	}
}

// tcpProxy is the proxy under test, served on a real listener, counting its running handlers
type tcpProxy struct {
	addr     string
	inFlight atomic.Int32
}

func startTCPProxy(t *testing.T, cfg Config, finder NetworkURLResolver) *tcpProxy {
	t.Helper()
	return startTCPProxyWithReadTimeout(t, cfg, finder, 0)
}

// startTCPProxyWithReadTimeout is startTCPProxy on a server with the given ReadTimeout, as the
// [REST] ReadTimeout of the binary sets it
func startTCPProxyWithReadTimeout(
	t *testing.T, cfg Config, finder NetworkURLResolver, readTimeout time.Duration,
) *tcpProxy {
	t.Helper()
	cfg.Logger = log.WithFields("module", "proxy-tcp-test")
	engine := aggkitcommon.NewHTTPServer(aggkitcommon.RESTConfig{}, nil).Engine()
	New(cfg, finder).RegisterRoutes(engine)

	p := &tcpProxy{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.inFlight.Add(1)
		defer p.inFlight.Add(-1)
		engine.ServeHTTP(w, r)
	}))
	server.Config.ReadTimeout = readTimeout
	server.Start()
	t.Cleanup(server.Close)
	p.addr = server.Listener.Addr().String()
	return p
}

// tcpClient is a raw HTTP/1.1 client connection that records every byte it receives
type tcpClient struct {
	conn     net.Conn
	received bytes.Buffer
	reader   *bufio.Reader
}

// dial opens a connection to the proxy; it is only closed on cleanup, so the proxy can never rely
// on the client going away
func (p *tcpProxy) dial(t *testing.T) *tcpClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", p.addr, tcpWait)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(tcpWait)))
	c := &tcpClient{conn: conn}
	c.reader = bufio.NewReader(io.TeeReader(conn, &c.received))
	return c
}

func (c *tcpClient) send(t *testing.T, raw string) {
	t.Helper()
	_, err := c.conn.Write([]byte(raw))
	require.NoError(t, err)
}

// readResponse reads the response head; the body is left to the caller
func (c *tcpClient) readResponse(t *testing.T) *http.Response {
	t.Helper()
	resp, err := http.ReadResponse(c.reader, nil)
	require.NoError(t, err, "no response from the proxy")
	return resp
}

// requireClosedByServer asserts the proxy closes the connection: the next read ends without data
// (EOF, or a reset) well before the client's deadline
func (c *tcpClient) requireClosedByServer(t *testing.T) {
	t.Helper()
	n, err := c.reader.Read(make([]byte, 1))
	require.Zero(t, n, "unexpected data after the response")
	var netErr net.Error
	require.False(t, errors.As(err, &netErr) && netErr.Timeout(),
		"the proxy did not close the connection: %v", err)
	require.Error(t, err)
}

func (p *tcpProxy) requireHandlersDone(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { return p.inFlight.Load() == 0 }, tcpWait, tcpPoll,
		"a proxy handler is still running")
}

func (u *tcpUpstream) requireNoOpenConnection(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { return u.open.Load() == 0 }, tcpWait, tcpPoll,
		"a connection to the bridge service is still open")
}

// TestProxyLimitsHeaderStallWhileClientKeepsConnectionOpen: a bridge service that accepts the request
// and never answers, and a client that never goes away. The client gets a 504 within the limit and
// neither the proxy handler nor the upstream connection outlives it, while the client stays
// connected
func TestProxyLimitsHeaderStallWhileClientKeepsConnectionOpen(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"UpstreamResponseHeaderTimeout", tcpLimits(tcpStall, 0, 0)},
		{"UpstreamRequestTimeout", tcpLimits(0, tcpStall, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newTCPUpstream(t, func(u *tcpUpstream, _ http.ResponseWriter, r *http.Request) {
				u.stall(r)
			})
			proxy := startTCPProxy(t, tc.cfg, fakeResolver{1: upstream.URL})
			client := proxy.dial(t)

			start := time.Now()
			client.send(t, tcpGetNetwork1+"\r\n")
			resp := client.readResponse(t)
			elapsed := time.Since(start)

			require.Equal(t, http.StatusGatewayTimeout, resp.StatusCode)
			require.GreaterOrEqual(t, elapsed, tcpStall, "answered before the timeout expired")
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"error":"bridge service timed out"}`, string(body))

			// the client connection is still open: the proxy released everything on its own
			proxy.requireHandlersDone(t)
			upstream.requireNoOpenConnection(t)
			require.EqualValues(t, 1, upstream.cancelled.Load(), "the upstream request was not cancelled")
			require.Zero(t, upstream.inHandler.Load())
		})
	}
}

// TestProxyLimitsBodyStallAbortsTheClientConnection: the bridge service sends the headers and part of
// the body, then stalls. Only the total deadline can end it; the status is already out, so the
// proxy must cut the connection, and the client must not get a response that looks complete
func TestProxyLimitsBodyStallAbortsTheClientConnection(t *testing.T) {
	upstream := newTCPUpstream(t, func(u *tcpUpstream, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"bridges":[`))
		_ = http.NewResponseController(w).Flush()
		u.stall(r)
	})
	proxy := startTCPProxy(t, tcpLimits(time.Minute, tcpStall, 0), fakeResolver{1: upstream.URL})
	client := proxy.dial(t)

	client.send(t, tcpGetNetwork1+"\r\n")
	resp := client.readResponse(t)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, []string{"chunked"}, resp.TransferEncoding)

	body, err := io.ReadAll(resp.Body)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF, "a truncated response must not look complete, got %q", body)
	require.Equal(t, `{"bridges":[`, string(body))
	client.requireClosedByServer(t)
	require.NotContains(t, client.received.String(), lastChunk, "the response carries the last chunk")

	proxy.requireHandlersDone(t)
	upstream.requireNoOpenConnection(t)
	require.EqualValues(t, 1, upstream.cancelled.Load(), "the upstream request was not cancelled")
}

// TestUpstreamDyingMidResponseIsNotRelayedAsComplete: the bridge service drops the connection in
// the middle of the body (no timeout involved). The client must see a cut connection, never a
// response that ends cleanly, and the proxy must keep serving other requests afterwards
func TestUpstreamDyingMidResponseIsNotRelayedAsComplete(t *testing.T) {
	tests := []struct {
		name          string
		contentLength string
	}{
		{"chunked", ""},
		{"with Content-Length", "1000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dying := newTCPUpstream(t, func(_ *tcpUpstream, w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tc.contentLength != "" {
					w.Header().Set("Content-Length", tc.contentLength)
				}
				_, _ = w.Write([]byte(`{"bridges":[`))
				conn, _, err := http.NewResponseController(w).Hijack()
				if err == nil {
					_ = conn.Close()
				}
			})
			healthy := newTCPUpstream(t, func(_ *tcpUpstream, w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"bridges":[]}`))
			})
			proxy := startTCPProxy(t, tcpLimits(time.Minute, time.Minute, 0),
				fakeResolver{1: dying.URL, 2: healthy.URL})
			client := proxy.dial(t)

			client.send(t, tcpGetNetwork1+"\r\n")
			resp := client.readResponse(t)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			body, err := io.ReadAll(resp.Body)
			require.ErrorIs(t, err, io.ErrUnexpectedEOF, "a truncated response must not look complete, got %q", body)
			client.requireClosedByServer(t)
			require.NotContains(t, client.received.String(), lastChunk, "the response carries the last chunk")
			proxy.requireHandlersDone(t)

			// the abort only took that one connection down
			other := proxy.dial(t)
			other.send(t, "GET /bridge/v1/bridges?network_id=2 HTTP/1.1\r\nHost: proxy\r\n\r\n")
			resp = other.readResponse(t)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			body, err = io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"bridges":[]}`, string(body))
		})
	}
}

// TestProxyLimitsMaxConnsPerHostCapsUpstreamConnections: many clients at once, each on its own
// connection. The bridge service never sees more TCP connections, nor requests, at once than
// UpstreamMaxConnsPerHost, and the requests over the cap wait and are still served
func TestProxyLimitsMaxConnsPerHostCapsUpstreamConnections(t *testing.T) {
	const (
		maxConns = 2
		clients  = 8
		hold     = 100 * time.Millisecond
	)
	upstream := newTCPUpstream(t, func(u *tcpUpstream, w http.ResponseWriter, _ *http.Request) {
		select {
		case <-time.After(hold):
		case <-u.release:
		}
		_, _ = w.Write([]byte("ok"))
	})
	proxy := startTCPProxy(t, tcpLimits(tcpWait, tcpWait, maxConns), fakeResolver{1: upstream.URL})

	conns := make([]*tcpClient, clients)
	for i := range conns {
		conns[i] = proxy.dial(t)
	}
	type result struct {
		status int
		body   string
		err    error
	}
	results := make(chan result, clients)
	var wg sync.WaitGroup
	for _, c := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.conn.Write([]byte(tcpGetNetwork1 + "\r\n")); err != nil {
				results <- result{err: err}
				return
			}
			resp, err := http.ReadResponse(c.reader, nil)
			if err != nil {
				results <- result{err: err}
				return
			}
			body, err := io.ReadAll(resp.Body)
			results <- result{status: resp.StatusCode, body: string(body), err: err}
		}()
	}
	wg.Wait()
	close(results)
	for r := range results {
		require.NoError(t, r.err)
		require.Equal(t, http.StatusOK, r.status)
		require.Equal(t, "ok", r.body)
	}

	require.LessOrEqual(t, upstream.maxOpen.Load(), int32(maxConns),
		"TCP connections open at once to the bridge service")
	require.LessOrEqual(t, upstream.maxInHandler.Load(), int32(maxConns),
		"requests at once on the bridge service")
	require.LessOrEqual(t, upstream.accepted.Load(), int32(maxConns),
		"the connections under the cap are reused, not churned")
}

// TestProxyLimitsMethodsOtherThanGetHeadNeverReachUpstream: a method the bridge service API does not
// use, with a body, is answered 405 by the proxy itself: not even a TCP connection is opened to
// the bridge service
func TestProxyLimitsMethodsOtherThanGetHeadNeverReachUpstream(t *testing.T) {
	upstream := newTCPUpstream(t, func(u *tcpUpstream, w http.ResponseWriter, r *http.Request) {
		u.countBody(r)
		w.WriteHeader(http.StatusCreated)
	})
	proxy := startTCPProxy(t, tcpLimits(tcpWait, tcpWait, 0), fakeResolver{1: upstream.URL})

	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
		http.MethodOptions, http.MethodTrace,
	} {
		client := proxy.dial(t)
		client.send(t, method+" /bridge/v1/bridges?network_id=1 HTTP/1.1\r\nHost: proxy\r\n"+
			"Content-Type: application/json\r\nContent-Length: 2\r\n\r\n{}")
		resp := client.readResponse(t)
		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, method)
		require.Equal(t, "GET, HEAD", resp.Header.Get("Allow"), method)
		_ = resp.Body.Close()
	}

	proxy.requireHandlersDone(t)
	require.Zero(t, upstream.accepted.Load(), "a connection was opened to the bridge service")
	require.Zero(t, upstream.bodyBytes.Load(), "body bytes reached the bridge service")
}

// TestProxyLimitsRequestBodyNeverReachesUpstream: a GET carrying a body, declared or chunked, and
// finished or not (the client just keeps the connection open), is answered 413 at once, without
// waiting for the rest of the body and without a single byte, or even a TCP connection, reaching
// the bridge service. The connection is then closed: at once, or, when net/http drains what is
// left of an unfinished body (up to 256 KiB, chunked or declared), at the latest at ReadTimeout
func TestProxyLimitsRequestBodyNeverReachesUpstream(t *testing.T) {
	const readTimeout = time.Second

	tests := []struct {
		name string
		raw  string
	}{
		{"chunked without Content-Length, never finished",
			"Transfer-Encoding: chunked\r\n\r\n4\r\nabcd\r\n"},
		{"chunked without Content-Length, finished",
			"Transfer-Encoding: chunked\r\n\r\n4\r\nabcd\r\n0\r\n\r\n"},
		{"Content-Length under the drain limit, never finished",
			"Content-Length: 100000\r\n\r\n" + strings.Repeat("a", 1000)},
		{"Content-Length over the drain limit, never finished",
			"Content-Length: 600000\r\n\r\n" + strings.Repeat("a", 1000)},
		{"Content-Length, finished",
			"Content-Length: 5\r\n\r\nhello"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newTCPUpstream(t, func(u *tcpUpstream, w http.ResponseWriter, r *http.Request) {
				u.countBody(r)
				w.WriteHeader(http.StatusOK)
			})
			proxy := startTCPProxyWithReadTimeout(t, tcpLimits(tcpWait, tcpWait, 0),
				fakeResolver{1: upstream.URL}, readTimeout)
			client := proxy.dial(t)

			start := time.Now()
			client.send(t, tcpGetNetwork1+tc.raw)
			resp := client.readResponse(t)
			require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
			require.Less(t, time.Since(start), readTimeout, "the answer waited for the body")
			require.True(t, resp.Close, "Connection: close expected")
			_, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			client.requireClosedByServer(t)

			proxy.requireHandlersDone(t)
			require.Zero(t, upstream.accepted.Load(), "a connection was opened to the bridge service")
			require.Zero(t, upstream.bodyBytes.Load(), "body bytes reached the bridge service")
		})
	}
}
