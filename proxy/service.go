// Package proxy implements the bridge-service reverse proxy component: it exposes the
// aggkit bridge service REST API on the shared HTTP server and forwards every request to
// the bridge service of the network selected by the request's network_id query parameter,
// resolved (and kept fresh) through the bridgeservicefinder.
package proxy

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"

	"github.com/agglayer/aggkit/bridgeservicefinder"
	aggkitcommon "github.com/agglayer/aggkit/common"
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
)

// NetworkURLResolver is the slice of the bridgeservicefinder.Finder the proxy needs: the
// per-network bridge service URL
type NetworkURLResolver interface {
	GetURL(networkID uint32) (bridgeservicefinder.NetworkURLs, error)
}

// Config holds the configuration of the bridge-service proxy
type Config struct {
	Logger aggkitcommon.Logger
}

// Service routes incoming bridge REST requests to the per-network bridge service resolved
// by the finder. Register it on the shared HTTP server with RegisterRoutes
type Service struct {
	logger aggkitcommon.Logger
	finder NetworkURLResolver

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
		logger:  cfg.Logger,
		finder:  finder,
		proxies: make(map[string]*httputil.ReverseProxy),
	}
}

// RegisterRoutes registers the proxied bridge service API on router
func (s *Service) RegisterRoutes(router gin.IRouter) {
	router.Any(BridgeV1Prefix+"/*any", s.ForwardHandler)
}

// ForwardHandler forwards the request to the bridge service of the network selected by the
// network_id query parameter, preserving method, path, query and body. It answers 400 on a
// missing/invalid network_id, 404 when no bridge service is known for the network and 502
// when the backend is unreachable
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
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	proxy, err := s.proxyFor(urls.BridgeURL)
	if err != nil {
		s.logger.Errorf("invalid bridge service URL %q for network %d: %v", urls.BridgeURL, networkID, err)
		c.JSON(http.StatusBadGateway,
			gin.H{"error": "invalid bridge service URL resolved for network " + rawNetworkID})
		return
	}

	proxy.ServeHTTP(c.Writer, c.Request)
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
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.logger.Errorf("forwarding %s %s to %s failed: %v", r.Method, r.URL.Path, bridgeURL, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"bridge service unreachable"}`))
		},
	}
	s.proxies[bridgeURL] = p
	return p, nil
}
