package agglayer

import (
	"context"
	"fmt"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/jellydator/ttlcache/v3"
)

type ConfigurationCache struct {
	// TTL that an item is valid in the cache before it is considered stale.
	TTL types.Duration
	// Capacity is the maximum number of items that can be stored in the cache.
	// If the cache exceeds this capacity, it will evict the least recently used items.
	Capacity uint64
}

// Validate checks if the configuration cache settings are valid.
func (c *ConfigurationCache) Validate() error {
	if c.TTL.Duration <= 0 {
		return fmt.Errorf("invalid TTL %s", c.TTL.String())
	}
	if c.Capacity == 0 {
		return fmt.Errorf("invalid Capacity %d. Must be >0", c.Capacity)
	}
	return nil
}

// CertificateCache provides a caching layer for certificate headers using a TTL (time-to-live) cache.
// It interacts with an AgglayerClientInterface to fetch certificate headers as needed and stores them
// in a cache keyed by common.Hash. This helps reduce redundant network calls and improves performance
// by serving frequently accessed certificate headers from the cache.
type CertificateCache struct {
	agglayerClient AgglayerClientInterface
	cache          *ttlcache.Cache[common.Hash, agglayertypes.CertificateHeader]
}

// NewCertificateCache creates and returns a new CertificateCache instance with the specified
// Agglayer client, time-to-live (TTL) duration for cache entries, and maximum cache capacity.
// The cache stores certificate headers indexed by their hash and automatically evicts entries
// based on the provided TTL and capacity constraints.
func NewCertificateCache(
	agglayerClient AgglayerClientInterface,
	ttl time.Duration,
	capacity uint64) *CertificateCache {
	c := ttlcache.New(
		ttlcache.WithTTL[common.Hash, agglayertypes.CertificateHeader](ttl),
		ttlcache.WithCapacity[common.Hash, agglayertypes.CertificateHeader](capacity),
	)
	return &CertificateCache{
		cache:          c,
		agglayerClient: agglayerClient,
	}
}

// GetCertificateHeader retrieves the certificate header associated with the given certificateID.
// It first attempts to fetch the certificate header from the local cache. If the header is not
// present in the cache, it fetches it from the agglayer client, stores it in the cache with the
// default TTL, and then returns it. Returns an error if the certificate header cannot be retrieved
// from the agglayer client.
//
// Parameters:
//   - ctx: The context for controlling cancellation and deadlines.
//   - certificateID: The unique identifier (hash) of the certificate.
//
// Returns:
//   - *agglayertypes.CertificateHeader: The retrieved certificate header.
//   - error: An error if the certificate header could not be retrieved.
func (c *CertificateCache) GetCertificateHeader(
	ctx context.Context, certificateID common.Hash) (*agglayertypes.CertificateHeader, error) {
	if c.cache.Has(certificateID) {
		tmp := c.cache.Get(certificateID).Value()
		return &tmp, nil
	}

	certificateHeader, err := c.agglayerClient.GetCertificateHeader(ctx, certificateID)
	if err != nil {
		return nil, err
	}

	// if DefaultTTL is set, the cache will use the TTL from the cache configuration
	// defined in the NewCertificateCache function
	c.cache.Set(certificateID, *certificateHeader, ttlcache.DefaultTTL)

	return certificateHeader, nil
}

// SendCertificate sends a certificate to the Agglayer client. (no cache)
func (c *CertificateCache) SendCertificate(ctx context.Context, certificate *agglayertypes.Certificate) (common.Hash, error) {
	return c.agglayerClient.SendCertificate(ctx, certificate)
}

// GetEpochConfiguration retrieves the current epoch configuration from the Agglayer client. (no cache)
func (c *CertificateCache) GetEpochConfiguration(ctx context.Context) (*agglayertypes.ClockConfiguration, error) {
	return c.agglayerClient.GetEpochConfiguration(ctx)
}

// GetLatestSettledCertificateHeader retrieves the latest settled certificate header for a given network ID. (no cache)
func (c *CertificateCache) GetLatestSettledCertificateHeader(ctx context.Context, networkID uint32) (*agglayertypes.CertificateHeader, error) {
	return c.agglayerClient.GetLatestSettledCertificateHeader(ctx, networkID)
}

// GetLatestPendingCertificateHeader retrieves the latest pending certificate header for a given network ID. (no cache)
func (c *CertificateCache) GetLatestPendingCertificateHeader(ctx context.Context, networkID uint32) (*agglayertypes.CertificateHeader, error) {
	return c.agglayerClient.GetLatestPendingCertificateHeader(ctx, networkID)
}
