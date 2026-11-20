// Package proxy implements the bridge-service reverse proxy component: it exposes the
// aggkit bridge service REST API on the shared HTTP server and forwards every request to
// the bridge service of the network selected by the request's network_id query parameter,
// resolved (and kept fresh) through the bridgeservicefinder.
package proxy

import (
	"context"
	"errors"
	"fmt"
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

	// Used only when http.DefaultTransport is not an *http.Transport (something replaced it), so
	// there is nothing to clone: the values mirror net/http's own default transport
	fallbackTLSHandshakeTimeout = 10 * time.Second
	fallbackIdleConnTimeout     = 90 * time.Second
)

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
// server drops a connection at its WriteTimeout, so an UpstreamRequestTimeout that is not below it
// never gets to answer 504: the client just sees the connection closed
func (c *Config) Warnings(restWriteTimeout time.Duration) []string {
	var warnings []string
	if c.UpstreamRequestTimeout.Duration > 0 && restWriteTimeout > 0 &&
		c.UpstreamRequestTimeout.Duration >= restWriteTimeout {
		warnings = append(warnings, fmt.Sprintf(
			"[Proxy] UpstreamRequestTimeout (%s) is not below [REST] WriteTimeout (%s): the server will "+
				"close the connection of a stalled request before the proxy can answer 504",
			c.UpstreamRequestTimeout.Duration, restWriteTimeout))
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

// RegisterRoutes registers the proxied bridge service API on router
func (s *Service) RegisterRoutes(router gin.IRouter) {
	router.Any(BridgeV1Prefix+"/*any", s.ForwardHandler)
}

// ForwardHandler forwards the request to the bridge service of the network selected by the
// network_id query parameter, preserving method, path, query and body. It answers 400 on a
// missing/invalid network_id, 404 when no bridge service is known for the network, 502 when the
// backend is unreachable and 504 when it does not answer in time (see
// Config.UpstreamResponseHeaderTimeout and Config.UpstreamRequestTimeout)
func (s *Service) ForwardHandler(c *gin.Context) {
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
			s.logger.Warnf("response of %s %s from %s aborted: UpstreamRequestTimeout expired after its headers were sent",
				req.Method, req.URL.Path, bridgeURL)
		} else {
			s.logger.Debugf("response of %s %s from %s aborted after its headers were sent",
				req.Method, req.URL.Path, bridgeURL)
		}
		if err := closeConnection(w); err != nil {
			// not an HTTP/1 connection we can close: let the abort propagate as ReverseProxy meant
			panic(rec)
		}
	}()
	proxy.ServeHTTP(w, req)
}

// closeConnection closes the client connection of w abruptly
func closeConnection(w http.ResponseWriter) error {
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
			s.logger.Errorf("forwarding %s %s to %s failed (%d): %v", r.Method, r.URL.Path, bridgeURL, status, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		},
	}
	s.proxies[bridgeURL] = p
	return p, nil
}
