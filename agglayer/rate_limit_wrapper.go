package agglayer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/agglayer/aggkit/agglayer/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	ethCommon "github.com/ethereum/go-ethereum/common"
)

// RateLimitWrapper wraps an AgglayerClientInterface and applies rate limiting based on configuration
type RateLimitWrapper struct {
	client       AgglayerClientInterface
	rateLimiters map[string]*aggkitcommon.RateLimit
	logger       aggkitcommon.Logger
	mu           sync.RWMutex
}

// NewRateLimitWrapper creates a new rate limiting wrapper around an agglayer client
func NewRateLimitWrapper(
	client AgglayerClientInterface,
	config ClientConfig,
	logger aggkitcommon.Logger,
) *RateLimitWrapper {
	wrapper := &RateLimitWrapper{
		client:       client,
		rateLimiters: make(map[string]*aggkitcommon.RateLimit),
		logger:       logger,
	}

	// Initialize rate limiters for each configured API method
	for _, apiConfig := range config.APIRateLimits {
		if apiConfig.RateLimit.Enabled() {
			wrapper.rateLimiters[apiConfig.MethodName] = aggkitcommon.NewRateLimit(apiConfig.RateLimit)
			logger.Infof("Rate limiting enabled for method '%s': %s", apiConfig.MethodName, apiConfig.RateLimit.String())
		}
	}

	return wrapper
}

// applyRateLimit applies rate limiting for the given method name
func (r *RateLimitWrapper) applyRateLimit(methodName string) {
	r.mu.RLock()
	rateLimiter, exists := r.rateLimiters[methodName]
	r.mu.RUnlock()

	if !exists {
		return
	}

	r.mu.Lock()
	rateLimitSleepTime := rateLimiter.Call(methodName, false)
	r.mu.Unlock()

	if rateLimitSleepTime != nil {
		r.logger.Warnf("rate limit reached for %s, sleeping for %s. Rate:%s",
			methodName,
			rateLimitSleepTime.String(), rateLimiter.String())
		time.Sleep(*rateLimitSleepTime)
	}
}

// SendCertificate sends a certificate to the AggLayer with rate limiting
func (r *RateLimitWrapper) SendCertificate(
	ctx context.Context,
	certificate *types.Certificate,
) (ethCommon.Hash, error) {
	r.applyRateLimit("SendCertificate")
	return r.client.SendCertificate(ctx, certificate)
}

// GetCertificateHeader gets a certificate header with rate limiting
func (r *RateLimitWrapper) GetCertificateHeader(
	ctx context.Context,
	certificateHash ethCommon.Hash,
) (*types.CertificateHeader, error) {
	r.applyRateLimit("GetCertificateHeader")
	return r.client.GetCertificateHeader(ctx, certificateHash)
}

// GetNetworkInfo gets a network info with rate limiting
func (r *RateLimitWrapper) GetNetworkInfo(
	ctx context.Context,
	networkID uint32,
) (types.NetworkInfo, error) {
	r.applyRateLimit("GetNetworkInfo")
	return r.client.GetNetworkInfo(ctx, networkID)
}

// GetEpochConfiguration gets epoch configuration with rate limiting
func (r *RateLimitWrapper) GetEpochConfiguration(ctx context.Context) (*types.ClockConfiguration, error) {
	r.applyRateLimit("GetEpochConfiguration")
	return r.client.GetEpochConfiguration(ctx)
}

// GetLatestSettledCertificateHeader gets the latest settled certificate header with rate limiting
func (r *RateLimitWrapper) GetLatestSettledCertificateHeader(
	ctx context.Context,
	networkID uint32,
) (*types.CertificateHeader, error) {
	r.applyRateLimit("GetLatestSettledCertificateHeader")
	return r.client.GetLatestSettledCertificateHeader(ctx, networkID)
}

// GetLatestPendingCertificateHeader gets the latest pending certificate header with rate limiting
func (r *RateLimitWrapper) GetLatestPendingCertificateHeader(
	ctx context.Context,
	networkID uint32,
) (*types.CertificateHeader, error) {
	r.applyRateLimit("GetLatestPendingCertificateHeader")
	return r.client.GetLatestPendingCertificateHeader(ctx, networkID)
}

// String returns a string representation of the rate limit wrapper
func (r *RateLimitWrapper) String() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	methods := make([]string, 0, len(r.rateLimiters))
	for method := range r.rateLimiters {
		methods = append(methods, method)
	}

	return fmt.Sprintf("RateLimitWrapper{methods: %v}", methods)
}
