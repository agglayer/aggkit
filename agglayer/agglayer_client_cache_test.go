package agglayer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/jellydator/ttlcache/v3"
	"github.com/stretchr/testify/require"
)

func TestGetCertificateHeader(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	checkHasCertFn := func(certCache *AgglayerClientCache, expectedCert *agglayertypes.CertificateHeader, certID common.Hash) {
		certificateHeader, err := certCache.GetCertificateHeader(ctx, certID)
		require.NoError(t, err)
		require.True(t, certCache.cache.Has(certID)) // Ensure the cache has the certificate
		require.Equal(t, expectedCert, certificateHeader)
	}

	checkCacheIsEmptyFn := func(certCache *AgglayerClientCache, certID common.Hash, ttl time.Duration) {
		time.Sleep(ttl)
		require.False(t, certCache.cache.Has(certID)) // Ensure the cache is empty after TTL
		require.Zero(t, certCache.cache.Len())
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
	certCache := NewCertificateCache(mockAgglayerClient, ttl, capacity)

	// Test cache doesn't have the certificate header initially
	mockAgglayerClient.EXPECT().GetCertificateHeader(ctx, certificateID).Return(certificateHeader, nil).Once()
	checkHasCertFn(certCache, certificateHeader, certificateID)
	checkCacheIsEmptyFn(certCache, certificateID, ttl)

	// Test cache has the certificate header after it was fetched
	certCache.cache.Set(certificateID, *certificateHeader, ttlcache.DefaultTTL)
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
	require.False(t, certCache.cache.Has(certificateID)) // Ensure the old certificate is evicted from the cache
	checkCacheIsEmptyFn(certCache, certificateID, ttl)

	// Test client returns an error
	mockAgglayerClient.EXPECT().GetCertificateHeader(ctx, certificateID).Return(nil, errors.New("some error")).Once()
	_, err := certCache.GetCertificateHeader(ctx, certificateID)
	require.ErrorContains(t, err, "some error")
	require.Zero(t, certCache.cache.Len()) // Ensure the cache is empty after
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
	certCache := NewCertificateCache(mockAgglayerClient, ttl, capacity)

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
