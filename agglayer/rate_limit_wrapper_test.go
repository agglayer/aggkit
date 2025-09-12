package agglayer

import (
	"context"
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockAgglayerClientInterface is a mock implementation for testing
type MockAgglayerClientInterface struct {
	mock.Mock
}

func (m *MockAgglayerClientInterface) SendCertificate(ctx context.Context, certificate *types.Certificate, validatorSignature []byte) (common.Hash, error) {
	args := m.Called(ctx, certificate, validatorSignature)
	return args.Get(0).(common.Hash), args.Error(1) //nolint:forcetypeassert
}

func (m *MockAgglayerClientInterface) GetCertificateHeader(ctx context.Context, certificateHash common.Hash) (*types.CertificateHeader, error) {
	args := m.Called(ctx, certificateHash)
	return args.Get(0).(*types.CertificateHeader), args.Error(1) //nolint:forcetypeassert
}

func (m *MockAgglayerClientInterface) GetEpochConfiguration(ctx context.Context) (*types.ClockConfiguration, error) {
	args := m.Called(ctx)
	return args.Get(0).(*types.ClockConfiguration), args.Error(1) //nolint:forcetypeassert
}

func (m *MockAgglayerClientInterface) GetLatestSettledCertificateHeader(ctx context.Context, networkID uint32) (*types.CertificateHeader, error) {
	args := m.Called(ctx, networkID)
	return args.Get(0).(*types.CertificateHeader), args.Error(1)
}

func (m *MockAgglayerClientInterface) GetLatestPendingCertificateHeader(ctx context.Context, networkID uint32) (*types.CertificateHeader, error) {
	args := m.Called(ctx, networkID)
	return args.Get(0).(*types.CertificateHeader), args.Error(1)
}

func TestNewRateLimitWrapper(t *testing.T) {
	t.Parallel()

	mockClient := &MockAgglayerClientInterface{}
	config := ClientConfig{
		APIRateLimits: []APIRateLimitConfig{
			{
				MethodName: "SendCertificate",
				RateLimit: aggkitcommon.RateLimitConfig{
					NumRequests: 2,
					Interval:    configtypes.Duration{Duration: time.Second},
				},
			},
			{
				MethodName: "GetEpochConfiguration",
				RateLimit: aggkitcommon.RateLimitConfig{
					NumRequests: 1,
					Interval:    configtypes.Duration{Duration: 500 * time.Millisecond},
				},
			},
		},
	}

	wrapper := NewRateLimitWrapper(mockClient, config, nil)
	require.NotNil(t, wrapper)
	require.Equal(t, mockClient, wrapper.client)
	require.Len(t, wrapper.rateLimiters, 2)
	require.Contains(t, wrapper.rateLimiters, "SendCertificate")
	require.Contains(t, wrapper.rateLimiters, "GetEpochConfiguration")
}

func TestRateLimitWrapper_NoRateLimits(t *testing.T) {
	t.Parallel()

	mockClient := &MockAgglayerClientInterface{}
	config := ClientConfig{
		APIRateLimits: []APIRateLimitConfig{},
	}

	wrapper := NewRateLimitWrapper(mockClient, config, nil)
	require.NotNil(t, wrapper)
	require.Len(t, wrapper.rateLimiters, 0)
}

func TestRateLimitWrapper_SendCertificate(t *testing.T) {
	t.Parallel()

	mockClient := &MockAgglayerClientInterface{}
	config := ClientConfig{
		APIRateLimits: []APIRateLimitConfig{
			{
				MethodName: "SendCertificate",
				RateLimit: aggkitcommon.RateLimitConfig{
					NumRequests: 1,
					Interval:    configtypes.Duration{Duration: 100 * time.Millisecond},
				},
			},
		},
	}

	wrapper := NewRateLimitWrapper(mockClient, config, nil)

	// First call should succeed immediately
	cert := &types.Certificate{}
	expectedHash := common.HexToHash("0x123")
	mockClient.On("SendCertificate", mock.Anything, cert, []byte(nil)).Return(expectedHash, nil).Once()

	start := time.Now()
	hash, err := wrapper.SendCertificate(context.Background(), cert, nil)
	duration := time.Since(start)

	require.NoError(t, err)
	require.Equal(t, expectedHash, hash)
	require.Less(t, duration, 50*time.Millisecond) // Should be fast

	// Second call should be rate limited
	mockClient.On("SendCertificate", mock.Anything, cert, []byte(nil)).Return(expectedHash, nil).Once()

	start = time.Now()
	hash, err = wrapper.SendCertificate(context.Background(), cert, nil)
	duration = time.Since(start)

	require.NoError(t, err)
	require.Equal(t, expectedHash, hash)
	require.GreaterOrEqual(t, duration, 100*time.Millisecond) // Should be rate limited

	mockClient.AssertExpectations(t)
}

func TestRateLimitWrapper_String(t *testing.T) {
	t.Parallel()

	mockClient := &MockAgglayerClientInterface{}
	config := ClientConfig{
		APIRateLimits: []APIRateLimitConfig{
			{
				MethodName: "SendCertificate",
				RateLimit: aggkitcommon.RateLimitConfig{
					NumRequests: 1,
					Interval:    configtypes.Duration{Duration: time.Second},
				},
			},
		},
	}

	wrapper := NewRateLimitWrapper(mockClient, config, nil)
	str := wrapper.String()
	require.Contains(t, str, "RateLimitWrapper")
	require.Contains(t, str, "SendCertificate")
}
