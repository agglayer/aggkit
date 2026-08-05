package agglayer

import (
	"fmt"

	"github.com/agglayer/aggkit/common"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
)

// APIRateLimitConfig defines rate limiting configuration for specific API methods
type APIRateLimitConfig struct {
	// MethodName is the name of the API method (e.g., "SendCertificate", "GetEpochConfiguration")
	MethodName string `mapstructure:"MethodName"`
	// RateLimit is the rate limiting configuration for this method
	RateLimit common.RateLimitConfig `mapstructure:"RateLimit"`
}

// String returns a string representation of the APIRateLimitConfig
func (a APIRateLimitConfig) String() string {
	return fmt.Sprintf("APIRateLimitConfig{Method: %s, RateLimit: %s}", a.MethodName, a.RateLimit.String())
}

type ClientConfig struct {
	GRPC *aggkitgrpc.ClientConfig
	// Cached is the master switch for the response cache/policy wrapper (see CacheConfig):
	// false ignores every per-method policy in ConfigurationCache and calls the underlying
	// agglayer client directly for all methods, exactly as if none were configured.
	Cached             bool
	ConfigurationCache *CacheConfig
	// APIRateLimits defines rate limiting configuration for specific API methods
	// If empty, no rate limiting is applied
	APIRateLimits []APIRateLimitConfig `mapstructure:"APIRateLimits"`
}

// Validate checks if the client configuration is valid.
func (c *ClientConfig) Validate() error {
	if err := c.GRPC.Validate(); err != nil {
		return err
	}
	if c.Cached {
		return c.ConfigurationCache.Validate()
	}
	return nil
}

func (c *ClientConfig) String() string {
	rateLimitsStr := "[]"
	if len(c.APIRateLimits) > 0 {
		rateLimitsStr = fmt.Sprintf("%v", c.APIRateLimits)
	}
	return fmt.Sprintf("GRPC: %s, Cached: %t, ConfigurationCache: %s, APIRateLimits: %s",
		c.GRPC.String(),
		c.Cached,
		c.ConfigurationCache.String(),
		rateLimitsStr)
}
