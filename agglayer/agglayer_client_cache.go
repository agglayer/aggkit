package agglayer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/jellydator/ttlcache/v3"
)

// MethodPolicy is the access policy applied to one AgglayerClientInterface method by
// AgglayerClientCache: whether calls to it are served from cache, passed straight through to the
// underlying client, or refused outright
type MethodPolicy string

const (
	// MethodPolicyCached routes the call through the response cache, keyed by its arguments (see
	// AgglayerClientCache); a cache miss falls through to the underlying client and stores the
	// result. Rejected by CacheConfig.Validate for SendCertificate: caching a mutating call would
	// silently skip submitting a certificate on a hit
	MethodPolicyCached MethodPolicy = "cached"
	// MethodPolicyPassthrough calls the underlying agglayer client directly, uncached. This is
	// the effective policy for any method left unset (the zero value) in configuration
	MethodPolicyPassthrough MethodPolicy = "passthrough"
	// MethodPolicyForbidden refuses the call immediately with ErrMethodForbidden, without ever
	// reaching the underlying agglayer client -- e.g. a read-only client that must never be able
	// to SendCertificate
	MethodPolicyForbidden MethodPolicy = "forbidden"
)

// ErrMethodForbidden is the error a forbidden method call fails with (see MethodPolicyForbidden)
var ErrMethodForbidden = errors.New("method forbidden by client policy")

// effective returns the policy to apply: p itself, or MethodPolicyPassthrough if p is the zero
// value -- a method left unset in configuration
func (p MethodPolicy) effective() MethodPolicy {
	if p == "" {
		return MethodPolicyPassthrough
	}
	return p
}

// Validate reports whether p is a recognized policy; the zero value is valid (see effective)
func (p MethodPolicy) Validate() error {
	switch p {
	case "", MethodPolicyCached, MethodPolicyPassthrough, MethodPolicyForbidden:
		return nil
	default:
		return fmt.Errorf("invalid method policy %q: must be one of %q, %q, %q",
			p, MethodPolicyCached, MethodPolicyPassthrough, MethodPolicyForbidden)
	}
}

// CacheConfig configures AgglayerClientCache: the shared TTL/capacity its per-method caches are
// built with, and the policy applied to each AgglayerClientInterface method. A method left unset
// (the zero value) behaves as MethodPolicyPassthrough
type CacheConfig struct {
	// TTL that an item is valid in the cache before it is considered stale.
	TTL types.Duration
	// Capacity is the maximum number of items that can be stored in each per-method cache.
	// If a cache exceeds this capacity, it evicts the least recently used items.
	Capacity uint64

	// SendCertificate is the policy for AgglayerClientInterface.SendCertificate. MethodPolicyCached
	// is rejected by Validate (see its doc)
	SendCertificate MethodPolicy `mapstructure:"SendCertificate"`
	// GetCertificateHeader is the policy for AgglayerClientInterface.GetCertificateHeader, cached
	// (if enabled) by certificate hash -- an immutable lookup key, safe to cache
	GetCertificateHeader MethodPolicy `mapstructure:"GetCertificateHeader"`
	// GetEpochConfiguration is the policy for AgglayerClientInterface.GetEpochConfiguration; it
	// takes no network id, so its cache -- if enabled -- holds a single global entry
	GetEpochConfiguration MethodPolicy `mapstructure:"GetEpochConfiguration"`
	// GetLatestSettledCertificateHeader is the policy for
	// AgglayerClientInterface.GetLatestSettledCertificateHeader, cached (if enabled) by network id
	GetLatestSettledCertificateHeader MethodPolicy `mapstructure:"GetLatestSettledCertificateHeader"`
	// GetLatestPendingCertificateHeader is the policy for
	// AgglayerClientInterface.GetLatestPendingCertificateHeader, cached (if enabled) by network id
	GetLatestPendingCertificateHeader MethodPolicy `mapstructure:"GetLatestPendingCertificateHeader"`
	// GetNetworkInfo is the policy for AgglayerClientInterface.GetNetworkInfo, cached (if enabled)
	// by network id
	GetNetworkInfo MethodPolicy `mapstructure:"GetNetworkInfo"`
}

// Validate checks if the configuration cache settings are valid.
func (c *CacheConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("CacheConfig is nil")
	}
	if c.TTL.Duration <= 0 {
		return fmt.Errorf("invalid TTL %s", c.TTL.String())
	}
	if c.Capacity == 0 {
		return fmt.Errorf("invalid Capacity %d. Must be >0", c.Capacity)
	}
	for _, mp := range c.methodPolicies() {
		if err := mp.policy.Validate(); err != nil {
			return fmt.Errorf("%s: %w", mp.name, err)
		}
	}
	if c.SendCertificate.effective() == MethodPolicyCached {
		return fmt.Errorf(
			"SendCertificate cannot use the %q policy: it is a mutating call, "+
				"caching it would silently skip submitting a certificate", MethodPolicyCached)
	}
	return nil
}

// methodPolicies lists every configured method policy keyed by method name, in the fixed order
// String prints them in
func (c *CacheConfig) methodPolicies() []struct {
	name   string
	policy MethodPolicy
} {
	return []struct {
		name   string
		policy MethodPolicy
	}{
		{"SendCertificate", c.SendCertificate},
		{"GetCertificateHeader", c.GetCertificateHeader},
		{"GetEpochConfiguration", c.GetEpochConfiguration},
		{"GetLatestSettledCertificateHeader", c.GetLatestSettledCertificateHeader},
		{"GetLatestPendingCertificateHeader", c.GetLatestPendingCertificateHeader},
		{"GetNetworkInfo", c.GetNetworkInfo},
	}
}

func (c *CacheConfig) String() string {
	if c == nil {
		return "CacheConfig is nil"
	}
	var s strings.Builder
	fmt.Fprintf(&s, "TTL: %s, Capacity: %d", c.TTL.String(), c.Capacity)
	for _, mp := range c.methodPolicies() {
		fmt.Fprintf(&s, ", %s: %s", mp.name, mp.policy.effective())
	}
	return s.String()
}

// AgglayerClientCache decorates an AgglayerClientInterface, applying the per-method policy
// configured in CacheConfig: a method call is served from its own TTL cache
// (MethodPolicyCached), passed straight through to agglayerClient (MethodPolicyPassthrough, the
// default for an unset method), or refused with ErrMethodForbidden without ever reaching
// agglayerClient (MethodPolicyForbidden). Every per-method cache is keyed by that method's own
// arguments (e.g. certificate hash, network id) and is safe for concurrent use -- the
// underlying ttlcache.Cache handles its own locking, and policy is read-only after construction
type AgglayerClientCache struct {
	agglayerClient AgglayerClientInterface
	policy         CacheConfig

	certificateHeaderCache       *ttlcache.Cache[common.Hash, agglayertypes.CertificateHeader]
	epochConfigurationCache      *ttlcache.Cache[struct{}, agglayertypes.ClockConfiguration]
	latestSettledCertHeaderCache *ttlcache.Cache[uint32, agglayertypes.CertificateHeader]
	latestPendingCertHeaderCache *ttlcache.Cache[uint32, agglayertypes.CertificateHeader]
	networkInfoCache             *ttlcache.Cache[uint32, agglayertypes.NetworkInfo]
}

// NewAgglayerClientCache returns an AgglayerClientCache decorating agglayerClient per cfg (see
// its doc); cfg is assumed already validated (see CacheConfig.Validate, run by
// ClientConfig.Validate before NewAgglayerClient ever reaches this constructor). Every
// per-method cache is built with the same cfg.TTL/cfg.Capacity, even for a method whose policy
// is not MethodPolicyCached -- unused caches cost one empty ttlcache.Cache each, a fixed and
// negligible allocation
func NewAgglayerClientCache(agglayerClient AgglayerClientInterface, cfg CacheConfig) *AgglayerClientCache {
	return &AgglayerClientCache{
		agglayerClient:               agglayerClient,
		policy:                       cfg,
		certificateHeaderCache:       newTTLCache[common.Hash, agglayertypes.CertificateHeader](cfg),
		epochConfigurationCache:      newTTLCache[struct{}, agglayertypes.ClockConfiguration](cfg),
		latestSettledCertHeaderCache: newTTLCache[uint32, agglayertypes.CertificateHeader](cfg),
		latestPendingCertHeaderCache: newTTLCache[uint32, agglayertypes.CertificateHeader](cfg),
		networkInfoCache:             newTTLCache[uint32, agglayertypes.NetworkInfo](cfg),
	}
}

// newTTLCache builds a cache with cfg's TTL/Capacity and hit semantics shared by every
// per-method cache: a hit never extends the entry's TTL, so a value is forgotten TTL after it
// was first stored, no matter how often it is read in between
func newTTLCache[K comparable, V any](cfg CacheConfig) *ttlcache.Cache[K, V] {
	return ttlcache.New(
		ttlcache.WithTTL[K, V](cfg.TTL.Duration),
		ttlcache.WithCapacity[K, V](cfg.Capacity),
		ttlcache.WithDisableTouchOnHit[K, V](),
	)
}

// forbiddenErr wraps ErrMethodForbidden with the refused method's name
func forbiddenErr(method string) error {
	return fmt.Errorf("%s: %w", method, ErrMethodForbidden)
}

// cachedPtr resolves a *V through cache keyed by key: a hit returns a copy of the cached value,
// a miss calls fetch, stores its dereferenced result (propagating a fetch error as-is, without
// caching it) and returns it. Shared by every AgglayerClientCache method returning a pointer
func cachedPtr[K comparable, V any](cache *ttlcache.Cache[K, V], key K, fetch func() (*V, error)) (*V, error) {
	cache.DeleteExpired()
	if item := cache.Get(key); item != nil {
		v := item.Value()
		return &v, nil
	}

	v, err := fetch()
	if err != nil {
		return nil, err
	}
	cache.Set(key, *v, ttlcache.DefaultTTL)
	return v, nil
}

// cachedVal is cachedPtr's counterpart for a method returning V by value instead of *V (see
// GetNetworkInfo)
func cachedVal[K comparable, V any](cache *ttlcache.Cache[K, V], key K, fetch func() (V, error)) (V, error) {
	cache.DeleteExpired()
	if item := cache.Get(key); item != nil {
		return item.Value(), nil
	}

	v, err := fetch()
	if err != nil {
		var zero V
		return zero, err
	}
	cache.Set(key, v, ttlcache.DefaultTTL)
	return v, nil
}

// SendCertificate sends a certificate to the Agglayer client, per c.policy.SendCertificate
// (MethodPolicyCached is never a possible value here, see CacheConfig.Validate)
func (c *AgglayerClientCache) SendCertificate(
	ctx context.Context,
	certificate *agglayertypes.Certificate) (common.Hash, error) {
	if c.policy.SendCertificate.effective() == MethodPolicyForbidden {
		return common.Hash{}, forbiddenErr("SendCertificate")
	}
	return c.agglayerClient.SendCertificate(ctx, certificate)
}

// GetCertificateHeader retrieves the certificate header for certificateID, per
// c.policy.GetCertificateHeader
func (c *AgglayerClientCache) GetCertificateHeader(
	ctx context.Context, certificateID common.Hash) (*agglayertypes.CertificateHeader, error) {
	switch c.policy.GetCertificateHeader.effective() {
	case MethodPolicyForbidden:
		return nil, forbiddenErr("GetCertificateHeader")
	case MethodPolicyCached:
		return cachedPtr(c.certificateHeaderCache, certificateID, func() (*agglayertypes.CertificateHeader, error) {
			return c.agglayerClient.GetCertificateHeader(ctx, certificateID)
		})
	default: // MethodPolicyPassthrough
		return c.agglayerClient.GetCertificateHeader(ctx, certificateID)
	}
}

// GetEpochConfiguration retrieves the current epoch configuration, per
// c.policy.GetEpochConfiguration
func (c *AgglayerClientCache) GetEpochConfiguration(ctx context.Context) (*agglayertypes.ClockConfiguration, error) {
	switch c.policy.GetEpochConfiguration.effective() {
	case MethodPolicyForbidden:
		return nil, forbiddenErr("GetEpochConfiguration")
	case MethodPolicyCached:
		return cachedPtr(c.epochConfigurationCache, struct{}{}, func() (*agglayertypes.ClockConfiguration, error) {
			return c.agglayerClient.GetEpochConfiguration(ctx)
		})
	default: // MethodPolicyPassthrough
		return c.agglayerClient.GetEpochConfiguration(ctx)
	}
}

// GetLatestSettledCertificateHeader retrieves the latest settled certificate header for
// networkID, per c.policy.GetLatestSettledCertificateHeader
func (c *AgglayerClientCache) GetLatestSettledCertificateHeader(ctx context.Context,
	networkID uint32) (*agglayertypes.CertificateHeader, error) {
	switch c.policy.GetLatestSettledCertificateHeader.effective() {
	case MethodPolicyForbidden:
		return nil, forbiddenErr("GetLatestSettledCertificateHeader")
	case MethodPolicyCached:
		return cachedPtr(c.latestSettledCertHeaderCache, networkID, func() (*agglayertypes.CertificateHeader, error) {
			return c.agglayerClient.GetLatestSettledCertificateHeader(ctx, networkID)
		})
	default: // MethodPolicyPassthrough
		return c.agglayerClient.GetLatestSettledCertificateHeader(ctx, networkID)
	}
}

// GetLatestPendingCertificateHeader retrieves the latest pending certificate header for
// networkID, per c.policy.GetLatestPendingCertificateHeader
func (c *AgglayerClientCache) GetLatestPendingCertificateHeader(ctx context.Context,
	networkID uint32) (*agglayertypes.CertificateHeader, error) {
	switch c.policy.GetLatestPendingCertificateHeader.effective() {
	case MethodPolicyForbidden:
		return nil, forbiddenErr("GetLatestPendingCertificateHeader")
	case MethodPolicyCached:
		return cachedPtr(c.latestPendingCertHeaderCache, networkID, func() (*agglayertypes.CertificateHeader, error) {
			return c.agglayerClient.GetLatestPendingCertificateHeader(ctx, networkID)
		})
	default: // MethodPolicyPassthrough
		return c.agglayerClient.GetLatestPendingCertificateHeader(ctx, networkID)
	}
}

// GetNetworkInfo retrieves the network state for networkID, per c.policy.GetNetworkInfo
func (c *AgglayerClientCache) GetNetworkInfo(
	ctx context.Context, networkID uint32) (agglayertypes.NetworkInfo, error) {
	switch c.policy.GetNetworkInfo.effective() {
	case MethodPolicyForbidden:
		return agglayertypes.NetworkInfo{}, forbiddenErr("GetNetworkInfo")
	case MethodPolicyCached:
		return cachedVal(c.networkInfoCache, networkID, func() (agglayertypes.NetworkInfo, error) {
			return c.agglayerClient.GetNetworkInfo(ctx, networkID)
		})
	default: // MethodPolicyPassthrough
		return c.agglayerClient.GetNetworkInfo(ctx, networkID)
	}
}
