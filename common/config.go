package common

import (
	"fmt"
	"strings"

	"github.com/agglayer/aggkit/config/types"
)

// RESTConfig contains the configuration settings for the REST service in the Aggkit application.
type RESTConfig struct {
	// Host specifies the hostname or IP address on which the REST service will listen.
	Host string `mapstructure:"Host"`

	// Port defines the port number on which the REST service will be accessible.
	Port int `mapstructure:"Port"`

	// ReadTimeout is the HTTP server read timeout
	// check net/http.server.ReadTimeout and net/http.server.ReadHeaderTimeout
	ReadTimeout types.Duration `mapstructure:"ReadTimeout"`

	// WriteTimeout is the HTTP server write timeout
	// check net/http.server.WriteTimeout
	WriteTimeout types.Duration `mapstructure:"WriteTimeout"`

	// MaxRequestsPerIPAndSecond defines how many requests a single IP can
	// send within a single second. 0 (the default) means unlimited: aggkit
	// does not enforce request rate limiting in-process. Apply rate limiting
	// at the fronting reverse proxy / API gateway / ingress if needed.
	MaxRequestsPerIPAndSecond float64 `mapstructure:"MaxRequestsPerIPAndSecond"`

	// CORS configures Cross-Origin Resource Sharing for this REST service.
	// Disabled by default, which keeps the current behavior (no CORS headers,
	// so cross-origin browser requests are rejected by the browser).
	CORS CORSConfig `mapstructure:"CORS"`
}

// Address constructs and returns the address as a string in the format "host:port".
func (c *RESTConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// CORSConfig configures Cross-Origin Resource Sharing (CORS) headers for a
// RESTConfig-backed HTTP server, so it can be called from browser-based
// clients hosted on a different origin.
type CORSConfig struct {
	// Enabled turns on CORS header handling. Disabled by default: a REST
	// service is only reachable cross-origin from a browser once this is set.
	Enabled bool `mapstructure:"Enabled"`

	// AllowedOrigins lists the origins allowed to make cross-origin requests.
	// "*" allows any origin. Ignored (and CORS effectively grants nothing)
	// when empty.
	AllowedOrigins []string `mapstructure:"AllowedOrigins"`

	// AllowedMethods lists the HTTP methods allowed for cross-origin requests.
	AllowedMethods []string `mapstructure:"AllowedMethods"`

	// AllowedHeaders lists the request headers allowed for cross-origin requests.
	AllowedHeaders []string `mapstructure:"AllowedHeaders"`

	// AllowCredentials allows cross-origin requests to include cookies / HTTP
	// auth. Per the CORS spec this cannot be combined with AllowedOrigins
	// containing "*": when both are set, the caller's origin is reflected
	// back instead of "*" so browsers still accept the credentialed response.
	AllowCredentials bool `mapstructure:"AllowCredentials"`

	// MaxAge is how long browsers may cache a preflight (OPTIONS) response.
	// 0 (the default) omits the Access-Control-Max-Age header, so browsers
	// fall back to their own default (5s per spec).
	MaxAge types.Duration `mapstructure:"MaxAge"`
}

// OriginAllowed reports whether origin may access an endpoint under this CORS policy, for
// callers that can't rely on the Access-Control-* response headers rs/cors sets (e.g. a
// WebSocket upgrade: browsers never CORS-preflight or gate the Upgrade request on those
// headers, so restricting origins there means rejecting the handshake outright instead).
//
// When CORS is disabled this returns true unconditionally, preserving the pre-CORS-config
// behavior of unrestricted cross-origin access. Once enabled, matching mirrors rs/cors:
// case-insensitive, "*" allows any origin, and an AllowedOrigins entry may contain a single
// "*" wildcard segment (e.g. "https://*.example.com").
func (c CORSConfig) OriginAllowed(origin string) bool {
	if !c.Enabled {
		return true
	}

	origin = strings.ToLower(origin)
	for _, allowed := range c.AllowedOrigins {
		allowed = strings.ToLower(allowed)
		if allowed == "*" {
			return true
		}
		if prefix, suffix, found := strings.Cut(allowed, "*"); found {
			if strings.HasPrefix(origin, prefix) && strings.HasSuffix(origin, suffix) {
				return true
			}
			continue
		}
		if allowed == origin {
			return true
		}
	}
	return false
}
