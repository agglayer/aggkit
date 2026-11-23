// Package proxy implements the bridge-service reverse proxy component: it exposes the
// aggkit bridge service REST API on the shared HTTP server and forwards every request to
// the bridge service of the network selected by the request's network_id query parameter,
// resolved (and kept fresh) through the bridgeservicefinder.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/agglayer/aggkit/bridgeservicefinder"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/gin-gonic/gin"
)

const (
	// BridgeV1Prefix is the url prefix of the proxied bridge service API. It mirrors
	// bridgeservice.BridgeV1Prefix (asserted in tests) but is redefined here so the proxy
	// binary does not import the full bridge service (and its swagger assets) for one constant.
	BridgeV1Prefix = "/bridge/v1"

	networkIDParam = "network_id"

	decimalBase   = 10
	uint32BitSize = 32

	// allowedMethods is the value of the Allow header of a 405: the bridge service API is read-only,
	// so GET (and HEAD, its bodiless twin) are the only methods the proxy forwards
	allowedMethods = http.MethodGet + ", " + http.MethodHead

	// Used only when http.DefaultTransport is not an *http.Transport (something replaced it), so
	// there is nothing to clone: the values mirror net/http's own default transport
	fallbackTLSHandshakeTimeout = 10 * time.Second
	fallbackIdleConnTimeout     = 90 * time.Second
)

// rejectedMethods are the methods the proxy answers 405 to without contacting any backend:
// every method gin's Any accepts other than the forwarded GET and HEAD
var rejectedMethods = []string{
	http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	http.MethodOptions, http.MethodConnect, http.MethodTrace,
}

// NetworkURLResolver is the slice of the bridgeservicefinder.Finder the proxy needs: the
// per-network bridge service URL
type NetworkURLResolver interface {
	GetURL(networkID uint32) (bridgeservicefinder.NetworkURLs, error)
}

// Config holds the configuration of the bridge-service proxy. Only the mapstructure-tagged
// fields come from the configuration file ([Proxy] section); the logger is wired by the binary
// (see proxy/cmd)
type Config struct {
	Logger aggkitcommon.Logger `mapstructure:"-"`

	// UpstreamResponseHeaderTimeout is how long the proxy waits, once it has sent a request to a
	// bridge service, for the response headers. It bounds a backend that accepts the request and
	// then never answers; a request exceeding it is answered 504. 0 disables it
	UpstreamResponseHeaderTimeout types.Duration `mapstructure:"UpstreamResponseHeaderTimeout"`

	// UpstreamRequestTimeout is the total time budget of a proxied request: connecting to the
	// bridge service, its response headers and the streaming of its response body. It also bounds
	// a backend that sends the headers (or part of the body) and then stalls, which
	// UpstreamResponseHeaderTimeout cannot see. A request exceeding it is answered 504, or, when
	// the response has already started, the connection is aborted. It must stay below the REST
	// WriteTimeout (the server drops the connection at that point, before the proxy can answer),
	// and larger than the slowest legitimate bridge service response. 0 disables it
	UpstreamRequestTimeout types.Duration `mapstructure:"UpstreamRequestTimeout"`

	// UpstreamMaxConnsPerHost caps the connections (idle, dialing and in use) the proxy keeps to
	// each bridge service: further requests wait for a free one, up to UpstreamRequestTimeout.
	// 0 means no limit
	UpstreamMaxConnsPerHost int `mapstructure:"UpstreamMaxConnsPerHost"`
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.UpstreamResponseHeaderTimeout.Duration < 0 {
		return fmt.Errorf("UpstreamResponseHeaderTimeout must not be negative, got %s",
			c.UpstreamResponseHeaderTimeout.Duration)
	}
	if c.UpstreamRequestTimeout.Duration < 0 {
		return fmt.Errorf("UpstreamRequestTimeout must not be negative, got %s", c.UpstreamRequestTimeout.Duration)
	}
	if c.UpstreamMaxConnsPerHost < 0 {
		return fmt.Errorf("UpstreamMaxConnsPerHost must not be negative, got %d", c.UpstreamMaxConnsPerHost)
	}
	return nil
}

// Warnings returns the settings that are valid but likely not what the operator wants. The REST
// server sets the write deadline of a connection (its WriteTimeout) as soon as it has read the
// request headers, so an upstream timeout that is not below it never gets to answer 504: the client
// just sees the connection closed. That holds for UpstreamRequestTimeout and for
// UpstreamResponseHeaderTimeout alike (the latter only starts once the upstream request is written)
func (c *Config) Warnings(restWriteTimeout time.Duration) []string {
	if restWriteTimeout <= 0 {
		return nil
	}
	var warnings []string
	for _, limit := range []struct {
		key   string
		value time.Duration
	}{
		{"UpstreamResponseHeaderTimeout", c.UpstreamResponseHeaderTimeout.Duration},
		{"UpstreamRequestTimeout", c.UpstreamRequestTimeout.Duration},
	} {
		if limit.value > 0 && limit.value >= restWriteTimeout {
			warnings = append(warnings, fmt.Sprintf(
				"[Proxy] %s (%s) is not below [REST] WriteTimeout (%s): the server will close the "+
					"connection of a stalled request before the proxy can answer 504",
				limit.key, limit.value, restWriteTimeout))
		}
	}
	return warnings
}

// Service routes incoming bridge REST requests to the per-network bridge service resolved
// by the finder. Register it on the shared HTTP server with RegisterRoutes
type Service struct {
	logger aggkitcommon.Logger
	finder NetworkURLResolver

	// transport is the one http.Transport every ReverseProxy uses, so the upstream limits apply
	// the same to every network's bridge service; requestTimeout is the total deadline of a
	// proxied request (0 disables it)
	transport      *http.Transport
	requestTimeout time.Duration

	// proxies caches one ReverseProxy per resolved base URL. URLs are re-resolved on every
	// request (the finder refreshes them from on-chain events); the cache only avoids
	// rebuilding proxies for a stable URL
	mu      sync.Mutex
	proxies map[string]*httputil.ReverseProxy
}

// New returns an instance of the bridge-service proxy
func New(cfg Config, finder NetworkURLResolver) *Service {
	cfg.Logger.Info("starting bridge service proxy")

	return &Service{
		logger:         cfg.Logger,
		finder:         finder,
		transport:      newUpstreamTransport(cfg),
		requestTimeout: cfg.UpstreamRequestTimeout.Duration,
		proxies:        make(map[string]*httputil.ReverseProxy),
	}
}

// newUpstreamTransport builds the transport shared by every ReverseProxy. It is a clone of
// http.DefaultTransport (never the global itself, which other code in the process uses) with the
// upstream limits of cfg: http.DefaultTransport has no response-header timeout nor connection cap,
// so a backend that accepts a request and never answers would otherwise hold it forever
func newUpstreamTransport(cfg Config) *http.Transport {
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	} else {
		transport = &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			TLSHandshakeTimeout: fallbackTLSHandshakeTimeout,
			IdleConnTimeout:     fallbackIdleConnTimeout,
		}
	}
	transport.ResponseHeaderTimeout = cfg.UpstreamResponseHeaderTimeout.Duration
	transport.MaxConnsPerHost = cfg.UpstreamMaxConnsPerHost
	// A reverse proxy relays the client's own Accept-Encoding and the response as is: left enabled,
	// the transport would add gzip on its own and decompress whatever the backend answers, so the
	// proxy would spend CPU and memory on a response whose size the backend alone controls
	transport.DisableCompression = true
	return transport
}

// RegisterRoutes registers the proxied bridge service API on router. Only GET and HEAD are
// forwarded: the bridge service API is read-only (see bridgeservice.RegisterRoutes), so any other
// method is answered 405 here, without contacting a backend. A CORS preflight (OPTIONS) never gets
// here: the CORS handler wrapping the engine (see common.HTTPServer) answers it
func (s *Service) RegisterRoutes(router gin.IRouter) {
	path := BridgeV1Prefix + "/*any"
	router.GET(path, s.ForwardHandler)
	router.HEAD(path, s.ForwardHandler)
	router.Match(rejectedMethods, path, methodNotAllowedHandler)
}

// methodNotAllowedHandler answers 405 with the methods the proxy does forward
func methodNotAllowedHandler(c *gin.Context) {
	c.Header("Allow", allowedMethods)
	c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed: only " + allowedMethods})
}

// requireNoBody answers 413 and returns false when the request carries a body. The bridge service
// API takes none, so none is forwarded: it is rejected before anything else (no network lookup, no
// upstream request) and no byte of it is relayed. A request of unknown length (chunked) is probed
// by reading one byte, so that one with an empty body still goes through
func requireNoBody(c *gin.Context) bool {
	req := c.Request
	if req.Body == nil || req.Body == http.NoBody {
		return true
	}
	if req.ContentLength == 0 {
		return true
	}

	if req.ContentLength < 0 {
		var probe [1]byte
		_, err := io.ReadFull(req.Body, probe[:])
		switch {
		case errors.Is(err, io.EOF):
			// an empty chunked body: nothing to reject, and nothing to forward
			req.Body = http.NoBody
			req.ContentLength = 0
			req.TransferEncoding = nil
			return true
		case err != nil:
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return false
		}
	}

	// Close the connection after the answer, so the server does not wait for another request on it.
	// net/http still reads (and discards) up to 256 KiB of a chunked body, or of a declared remainder
	// of up to that size, before closing it: that is bounded by the REST ReadTimeout, and none of it
	// is forwarded
	c.Header("Connection", "close")
	c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge,
		gin.H{"error": "request body not allowed: the bridge service API takes no request body"})
	return false
}

// ForwardHandler forwards the request to the bridge service of the network selected by the
// network_id query parameter, preserving method, path and query. It answers 413 to a request with
// a body, 400 on a missing/invalid network_id, 404 when no bridge service is known for the
// network, 502 when the backend is unreachable and 504 when it does not answer in time (see
// Config.UpstreamResponseHeaderTimeout and Config.UpstreamRequestTimeout)
func (s *Service) ForwardHandler(c *gin.Context) {
	if !requireNoBody(c) {
		return
	}

	rawNetworkID := c.Query(networkIDParam)
	if rawNetworkID == "" {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": "missing mandatory query parameter: " + networkIDParam})
		return
	}
	networkID, err := strconv.ParseUint(rawNetworkID, decimalBase, uint32BitSize)
	if err != nil {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": "invalid " + networkIDParam + " parameter: " + rawNetworkID})
		return
	}

	urls, err := s.finder.GetURL(uint32(networkID))
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, bridgeservicefinder.ErrURLNotFound) {
			status = http.StatusNotFound
		}
		s.logger.Warnf("no bridge service resolved for network %d: %v", networkID, err)
		c.JSON(status, gin.H{"error": aggkitcommon.RedactError(err)})
		return
	}

	proxy, err := s.proxyFor(urls.BridgeURL)
	if err != nil {
		s.logger.Errorf("invalid bridge service URL %q for network %d: %v", urls.BridgeURL, networkID, err)
		c.JSON(http.StatusBadGateway,
			gin.H{"error": "invalid bridge service URL resolved for network " + rawNetworkID})
		return
	}

	req := c.Request
	if s.requestTimeout > 0 {
		// The deadline is derived from the client's own context, so a client that goes away still
		// cancels the upstream request at once
		ctx, cancel := context.WithTimeout(req.Context(), s.requestTimeout)
		defer cancel()
		req = req.WithContext(ctx)
	}
	s.serveProxied(proxy, urls.BridgeURL, c.Writer, req)
}

// serveProxied relays req to the backend through proxy. When the response fails after its headers
// were sent (the backend stalls past UpstreamRequestTimeout or drops the connection),
// httputil.ReverseProxy can only abort: it panics with http.ErrAbortHandler, which net/http turns
// into a closed connection so the client can tell its body is truncated. The recovery middleware
// of the shared server swallows that panic, though, and the response would end with a valid chunked
// terminator: a truncated body that looks complete. So the connection is closed here instead.
func (s *Service) serveProxied(
	proxy *httputil.ReverseProxy, bridgeURL string, w http.ResponseWriter, req *http.Request,
) {
	defer func() {
		rec := recover()
		if rec == nil {
			return
		}
		if err, ok := rec.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
			panic(rec)
		}
		if errors.Is(req.Context().Err(), context.DeadlineExceeded) {
			// same level as a timeout before the headers (see the ErrorHandler of proxyFor)
			s.logger.Errorf("response of %s %s from %s aborted: UpstreamRequestTimeout expired after its headers were sent",
				req.Method, req.URL.Path, bridgeURL)
		} else {
			s.logger.Debugf("response of %s %s from %s aborted after its headers were sent",
				req.Method, req.URL.Path, bridgeURL)
		}
		if err := closeConnection(w); err != nil {
			// Not a connection we can hijack: let the abort propagate as ReverseProxy meant. It does not
			// happen on the shared server, which serves HTTP/1.1 only (plain TCP, no TLS nor h2c), so its
			// connections can always be hijacked. Were it ever to serve HTTP/2, the recovery middleware
			// would swallow this panic as well, and the stream would need resetting some other way: log it
			// loudly, so that change cannot go unnoticed
			s.logger.Errorf("response of %s %s from %s aborted, but its connection could not be closed: %v",
				req.Method, req.URL.Path, bridgeURL, err)
			panic(rec)
		}
	}()
	proxy.ServeHTTP(w, req)
}

// closeConnection closes the client connection of w abruptly, after sending what was written to it
func closeConnection(w http.ResponseWriter) error {
	// What the handler wrote may still sit in a buffer: gin delays the status line until the first
	// body write, and ReverseProxy only flushes a response of known length when the copy is over.
	// A hijack discards that buffer, so the client would get no response at all (and gin would then
	// write the status to the hijacked connection). Flushed, it gets the status and the partial body
	// before the connection is cut. An error just means the client is gone already
	_ = http.NewResponseController(w).Flush()

	// gin's writer refuses to hijack once body bytes are written, which is the case that matters
	// here: get to the net/http one under it
	for {
		wrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		w = wrapper.Unwrap()
	}
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return err
	}
	return conn.Close()
}

// upstreamErrorResponse maps a failure of an upstream request to the status and the JSON body
// answered to the client. The body is generic on purpose: the error carries the backend's URL and
// network details, which only go to the logs
func upstreamErrorResponse(err error) (int, string) {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return http.StatusGatewayTimeout, `{"error":"bridge service timed out"}`
	}
	return http.StatusBadGateway, `{"error":"bridge service unreachable"}`
}

// proxyFor returns the (cached) ReverseProxy that forwards to the given bridge service base URL
func (s *Service) proxyFor(bridgeURL string) (*httputil.ReverseProxy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.proxies[bridgeURL]; ok {
		return p, nil
	}

	target, err := url.Parse(bridgeURL)
	if err != nil {
		return nil, err
	}

	p := &httputil.ReverseProxy{
		Transport: s.transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			status, body := upstreamErrorResponse(err)
			if errors.Is(err, context.Canceled) && errors.Is(r.Context().Err(), context.Canceled) {
				// the client went away: not a bridge service failure, and nobody reads this answer
				s.logger.Debugf("forwarding %s %s to %s cancelled: the client went away", r.Method, r.URL.Path, bridgeURL)
			} else {
				s.logger.Errorf("forwarding %s %s to %s failed (%d): %v", r.Method, r.URL.Path, bridgeURL, status, err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		},
	}
	s.proxies[bridgeURL] = p
	return p, nil
}
