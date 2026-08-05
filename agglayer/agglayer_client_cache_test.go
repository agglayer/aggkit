package agglayer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/jellydator/ttlcache/v3"
	"github.com/stretchr/testify/require"
)

// cachedConfig returns a CacheConfig with the given TTL/capacity and every method left unset
// (MethodPolicyPassthrough), for tests that only care about one method's policy
func cachedConfig(ttl time.Duration, capacity uint64) CacheConfig {
	return CacheConfig{TTL: configtypes.Duration{Duration: ttl}, Capacity: capacity}
}

func TestGetCertificateHeader(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	checkHasCertFn := func(certCache *AgglayerClientCache, expectedCert *agglayertypes.CertificateHeader, certID common.Hash) {
		certificateHeader, err := certCache.GetCertificateHeader(ctx, certID)
		require.NoError(t, err)
		require.True(t, certCache.certificateHeaderCache.Has(certID)) // Ensure the cache has the certificate
		require.Equal(t, expectedCert, certificateHeader)
	}

	checkCacheIsEmptyFn := func(certCache *AgglayerClientCache, certID common.Hash, ttl time.Duration) {
		time.Sleep(ttl)
		require.False(t, certCache.certificateHeaderCache.Has(certID)) // Ensure the cache is empty after TTL
		require.Zero(t, certCache.certificateHeaderCache.Len())
	}

	ttl := 500 * time.Millisecond
	capacity := uint64(1)
	certificateID := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	certificateHeader := &agglayertypes.CertificateHeader{
		CertificateID: certificateID,
		Height:        1,
		NetworkID:     1,
		Status:        agglayertypes.Settled,
	}

	mockAgglayerClient := mocks.NewAgglayerClientMock(t)
	cfg := cachedConfig(ttl, capacity)
	cfg.GetCertificateHeader = MethodPolicyCached
	certCache := NewAgglayerClientCache(mockAgglayerClient, cfg)

	// Test cache doesn't have the certificate header initially
	mockAgglayerClient.EXPECT().GetCertificateHeader(ctx, certificateID).Return(certificateHeader, nil).Once()
	checkHasCertFn(certCache, certificateHeader, certificateID)
	checkCacheIsEmptyFn(certCache, certificateID, ttl)

	// Test cache has the certificate header after it was fetched
	certCache.certificateHeaderCache.Set(certificateID, *certificateHeader, ttlcache.DefaultTTL)
	checkHasCertFn(certCache, certificateHeader, certificateID)
	checkCacheIsEmptyFn(certCache, certificateID, ttl)

	// Test cache hit limit
	mockAgglayerClient.EXPECT().GetCertificateHeader(ctx, certificateID).Return(certificateHeader, nil).Once()
	checkHasCertFn(certCache, certificateHeader, certificateID)

	newCertID := common.HexToHash("0x1")
	newCert := &agglayertypes.CertificateHeader{
		CertificateID: newCertID,
		Height:        2,
		NetworkID:     1,
		Status:        agglayertypes.Pending,
	}
	mockAgglayerClient.EXPECT().GetCertificateHeader(ctx, newCertID).Return(newCert, nil).Once()
	checkHasCertFn(certCache, newCert, newCertID)
	require.False(t, certCache.certificateHeaderCache.Has(certificateID)) // Ensure the old certificate is evicted
	checkCacheIsEmptyFn(certCache, certificateID, ttl)

	// Test client returns an error
	mockAgglayerClient.EXPECT().GetCertificateHeader(ctx, certificateID).Return(nil, errors.New("some error")).Once()
	_, err := certCache.GetCertificateHeader(ctx, certificateID)
	require.ErrorContains(t, err, "some error")
	require.Zero(t, certCache.certificateHeaderCache.Len()) // Ensure the cache is empty after
}

func TestExpiration(t *testing.T) {
	t.Parallel()
	ttl := 500 * time.Millisecond
	capacity := uint64(1)
	certificateID := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	certificateHeader := &agglayertypes.CertificateHeader{
		CertificateID: certificateID,
		Height:        1,
		NetworkID:     1,
		Status:        agglayertypes.Settled,
	}

	mockAgglayerClient := mocks.NewAgglayerClientMock(t)
	cfg := cachedConfig(ttl, capacity)
	cfg.GetCertificateHeader = MethodPolicyCached
	certCache := NewAgglayerClientCache(mockAgglayerClient, cfg)

	mockAgglayerClient.EXPECT().GetCertificateHeader(t.Context(), certificateID).Return(certificateHeader, nil).Times(2)
	// Insert item on cache
	_, err := certCache.GetCertificateHeader(t.Context(), certificateID)
	require.NoError(t, err)
	// Hit cache
	_, err = certCache.GetCertificateHeader(t.Context(), certificateID)
	require.NoError(t, err)
	time.Sleep(ttl)
	// Cache is expire and request again the Certificate
	_, err = certCache.GetCertificateHeader(t.Context(), certificateID)
	require.NoError(t, err)
}

// TestMethodPolicyPassthroughIsTheDefault pins that a method left unset in CacheConfig behaves
// as MethodPolicyPassthrough: every call reaches the underlying client, none are served from
// cache
func TestMethodPolicyPassthroughIsTheDefault(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	mockAgglayerClient := mocks.NewAgglayerClientMock(t)
	cache := NewAgglayerClientCache(mockAgglayerClient, cachedConfig(time.Minute, 10))

	header := &agglayertypes.CertificateHeader{NetworkID: 1}
	mockAgglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(header, nil).Twice()

	_, err := cache.GetLatestSettledCertificateHeader(ctx, 1)
	require.NoError(t, err)
	_, err = cache.GetLatestSettledCertificateHeader(ctx, 1)
	require.NoError(t, err)
	// mockery enforces the exact .Twice() call count via t.Cleanup: a cache hit on the second
	// call would leave one expected call unfulfilled and fail the test
}

// TestMethodPolicyForbidden pins that every AgglayerClientInterface method configured as
// MethodPolicyForbidden fails with ErrMethodForbidden without ever reaching the underlying
// client: mockAgglayerClient has no expectations set up at all, so an accidental pass-through
// would panic the test instead of silently succeeding
func TestMethodPolicyForbidden(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	mockAgglayerClient := mocks.NewAgglayerClientMock(t)
	cache := NewAgglayerClientCache(mockAgglayerClient, CacheConfig{
		TTL:                               configtypes.Duration{Duration: time.Minute},
		Capacity:                          10,
		SendCertificate:                   MethodPolicyForbidden,
		GetCertificateHeader:              MethodPolicyForbidden,
		GetEpochConfiguration:             MethodPolicyForbidden,
		GetLatestSettledCertificateHeader: MethodPolicyForbidden,
		GetLatestPendingCertificateHeader: MethodPolicyForbidden,
		GetNetworkInfo:                    MethodPolicyForbidden,
	})

	_, err := cache.SendCertificate(ctx, &agglayertypes.Certificate{})
	require.ErrorIs(t, err, ErrMethodForbidden)

	_, err = cache.GetCertificateHeader(ctx, common.Hash{})
	require.ErrorIs(t, err, ErrMethodForbidden)

	_, err = cache.GetEpochConfiguration(ctx)
	require.ErrorIs(t, err, ErrMethodForbidden)

	_, err = cache.GetLatestSettledCertificateHeader(ctx, 1)
	require.ErrorIs(t, err, ErrMethodForbidden)

	_, err = cache.GetLatestPendingCertificateHeader(ctx, 1)
	require.ErrorIs(t, err, ErrMethodForbidden)

	_, err = cache.GetNetworkInfo(ctx, 1)
	require.ErrorIs(t, err, ErrMethodForbidden)
}

// TestGetNetworkInfoCachedByNetworkID pins that a cached GetNetworkInfo (a value, not a
// pointer, unlike every other cached method) is keyed by network id: repeating a call for the
// same network is a hit, a different network id is its own entry
func TestGetNetworkInfoCachedByNetworkID(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	mockAgglayerClient := mocks.NewAgglayerClientMock(t)
	cfg := cachedConfig(time.Minute, 10)
	cfg.GetNetworkInfo = MethodPolicyCached
	cache := NewAgglayerClientCache(mockAgglayerClient, cfg)

	info1 := agglayertypes.NetworkInfo{NetworkID: 1, Status: "active"}
	info2 := agglayertypes.NetworkInfo{NetworkID: 2, Status: "syncing"}
	mockAgglayerClient.EXPECT().GetNetworkInfo(ctx, uint32(1)).Return(info1, nil).Once()
	mockAgglayerClient.EXPECT().GetNetworkInfo(ctx, uint32(2)).Return(info2, nil).Once()

	got, err := cache.GetNetworkInfo(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, info1, got)

	// repeated call for the same network: served from cache (the .Once() above still holds)
	got, err = cache.GetNetworkInfo(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, info1, got)

	// a different network id is a separate cache entry
	got, err = cache.GetNetworkInfo(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, info2, got)
}

// TestGetEpochConfigurationCachedGlobally pins that GetEpochConfiguration -- the only cached
// method with no network id or hash argument -- caches a single global entry shared by every
// call
func TestGetEpochConfigurationCachedGlobally(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	mockAgglayerClient := mocks.NewAgglayerClientMock(t)
	cfg := cachedConfig(time.Minute, 10)
	cfg.GetEpochConfiguration = MethodPolicyCached
	cache := NewAgglayerClientCache(mockAgglayerClient, cfg)

	clockCfg := &agglayertypes.ClockConfiguration{EpochDuration: 10, GenesisBlock: 100}
	mockAgglayerClient.EXPECT().GetEpochConfiguration(ctx).Return(clockCfg, nil).Once()

	got, err := cache.GetEpochConfiguration(ctx)
	require.NoError(t, err)
	require.Equal(t, clockCfg, got)

	got, err = cache.GetEpochConfiguration(ctx)
	require.NoError(t, err)
	require.Equal(t, clockCfg, got)
}
