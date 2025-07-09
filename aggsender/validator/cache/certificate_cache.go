package cache

import (
	"context"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/jellydator/ttlcache/v3"
)

// CertificateCache provides a caching layer for certificate headers using a TTL (time-to-live) cache.
// It interacts with an AgglayerClientInterface to fetch certificate headers as needed and stores them
// in a cache keyed by common.Hash. This helps reduce redundant network calls and improves performance
// by serving frequently accessed certificate headers from the cache.
type CertificateCache struct {
	agglayerClient agglayer.AgglayerClientInterface

	cache *ttlcache.Cache[common.Hash, *agglayertypes.CertificateHeader]
}

// NewCertificateCache creates and returns a new CertificateCache instance with the specified
// Agglayer client, time-to-live (TTL) duration for cache entries, and maximum cache capacity.
// The cache stores certificate headers indexed by their hash and automatically evicts entries
// based on the provided TTL and capacity constraints.
func NewCertificateCache(
	agglayerClient agglayer.AgglayerClientInterface,
	ttl time.Duration,
	capacity uint64) *CertificateCache {
	c := ttlcache.New(
		ttlcache.WithTTL[common.Hash, *agglayertypes.CertificateHeader](ttl),
		ttlcache.WithCapacity[common.Hash, *agglayertypes.CertificateHeader](capacity),
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
		return c.cache.Get(certificateID).Value(), nil
	}

	certificateHeader, err := c.agglayerClient.GetCertificateHeader(ctx, certificateID)
	if err != nil {
		return nil, err
	}

	// if DefaultTTL is set, the cache will use the TTL from the cache configuration
	// defined in the NewCertificateCache function
	c.cache.Set(certificateID, certificateHeader, ttlcache.DefaultTTL)

	return certificateHeader, nil
}
