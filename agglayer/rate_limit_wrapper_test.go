package agglayer

import (
	"context"
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer/mocks"
	"github.com/agglayer/aggkit/agglayer/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewRateLimitWrapper(t *testing.T) {
	t.Parallel()

	mockClient := mocks.NewAgglayerClientMock(t)
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

	mockLogger := &MockLogger{}
	setupMockLoggerExpectations(mockLogger, "SendCertificate", "GetEpochConfiguration")

	wrapper := NewRateLimitWrapper(mockClient, config, mockLogger)
	require.NotNil(t, wrapper)
	require.Equal(t, mockClient, wrapper.client)
	require.Len(t, wrapper.rateLimiters, 2)
	require.Contains(t, wrapper.rateLimiters, "SendCertificate")
	require.Contains(t, wrapper.rateLimiters, "GetEpochConfiguration")

	mockLogger.AssertExpectations(t)
}

func TestRateLimitWrapper_NoRateLimits(t *testing.T) {
	t.Parallel()

	mockClient := mocks.NewAgglayerClientMock(t)
	config := ClientConfig{
		APIRateLimits: []APIRateLimitConfig{},
	}

	mockLogger := &MockLogger{}
	wrapper := NewRateLimitWrapper(mockClient, config, mockLogger)
	require.NotNil(t, wrapper)
	require.Len(t, wrapper.rateLimiters, 0)

	mockLogger.AssertExpectations(t)
}

func TestRateLimitWrapper_SendCertificate(t *testing.T) {
	t.Parallel()

	mockClient := mocks.NewAgglayerClientMock(t)
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

	mockLogger := &MockLogger{}
	setupMockLoggerExpectationsWithRateLimit(mockLogger, "SendCertificate")

	wrapper := NewRateLimitWrapper(mockClient, config, mockLogger)

	// First call should succeed immediately
	cert := &types.Certificate{}
	expectedHash := common.HexToHash("0x123")
	mockClient.EXPECT().SendCertificate(mock.Anything, cert).Return(expectedHash, nil).Once()

	start := time.Now()
	hash, err := wrapper.SendCertificate(context.Background(), cert)
	duration := time.Since(start)

	require.NoError(t, err)
	require.Equal(t, expectedHash, hash)
	require.Less(t, duration, 50*time.Millisecond) // Should be fast

	// Second call should be rate limited
	mockClient.EXPECT().SendCertificate(mock.Anything, cert).Return(expectedHash, nil).Once()

	// Add a small delay to ensure the calls are not happening in the same microsecond
	time.Sleep(1 * time.Millisecond)

	start = time.Now()
	hash, err = wrapper.SendCertificate(context.Background(), cert)
	duration = time.Since(start)

	require.NoError(t, err)
	require.Equal(t, expectedHash, hash)
	require.GreaterOrEqual(t, duration, 95*time.Millisecond) // Should be rate limited (allowing for timing precision)

	mockClient.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestRateLimitWrapper_String(t *testing.T) {
	t.Parallel()

	mockClient := mocks.NewAgglayerClientMock(t)
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

	mockLogger := &MockLogger{}
	setupMockLoggerExpectations(mockLogger, "SendCertificate")

	wrapper := NewRateLimitWrapper(mockClient, config, mockLogger)
	str := wrapper.String()
	require.Contains(t, str, "RateLimitWrapper")
	require.Contains(t, str, "SendCertificate")

	mockLogger.AssertExpectations(t)
}

func TestNewRateLimitWrapper_WithLogger(t *testing.T) {
	t.Parallel()

	mockClient := mocks.NewAgglayerClientMock(t)
	mockLogger := &MockLogger{}
	config := ClientConfig{
		APIRateLimits: []APIRateLimitConfig{
			{
				MethodName: "SendCertificate",
				RateLimit: aggkitcommon.RateLimitConfig{
					NumRequests: 2,
					Interval:    configtypes.Duration{Duration: time.Second},
				},
			},
		},
	}

	// Expect logger to be called during initialization
	mockLogger.On("Infof", "Rate limiting enabled for method '%s': %s", "SendCertificate", mock.MatchedBy(func(s string) bool {
		return s != ""
	})).Return()

	wrapper := NewRateLimitWrapper(mockClient, config, mockLogger)
	require.NotNil(t, wrapper)
	require.Equal(t, mockClient, wrapper.client)
	require.Len(t, wrapper.rateLimiters, 1)
	require.Contains(t, wrapper.rateLimiters, "SendCertificate")

	// Verify logger was called
	mockLogger.AssertExpectations(t)
}

func TestNewRateLimitWrapper_WithDisabledRateLimits(t *testing.T) {
	t.Parallel()

	mockClient := mocks.NewAgglayerClientMock(t)
	config := ClientConfig{
		APIRateLimits: []APIRateLimitConfig{
			{
				MethodName: "SendCertificate",
				RateLimit: aggkitcommon.RateLimitConfig{
					NumRequests: 0, // Disabled
					Interval:    configtypes.Duration{Duration: time.Second},
				},
			},
			{
				MethodName: "GetEpochConfiguration",
				RateLimit: aggkitcommon.RateLimitConfig{
					NumRequests: 1,
					Interval:    configtypes.Duration{Duration: 0}, // Disabled
				},
			},
		},
	}

	mockLogger := &MockLogger{}
	wrapper := NewRateLimitWrapper(mockClient, config, mockLogger)
	require.NotNil(t, wrapper)
	require.Len(t, wrapper.rateLimiters, 0) // No rate limiters should be created
}

func TestRateLimitWrapper_ApplyRateLimit_NonExistentMethod(t *testing.T) {
	t.Parallel()

	mockClient := mocks.NewAgglayerClientMock(t)
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

	mockLogger := &MockLogger{}
	setupMockLoggerExpectations(mockLogger, "SendCertificate")

	wrapper := NewRateLimitWrapper(mockClient, config, mockLogger)

	// Test applyRateLimit with a method that doesn't exist
	// This should return early without doing anything
	wrapper.applyRateLimit("NonExistentMethod")

	// No assertions needed as this should complete without error
	mockLogger.AssertExpectations(t)
}

func TestRateLimitWrapper_ApplyRateLimit_WithLogger(t *testing.T) {
	t.Parallel()

	mockClient := mocks.NewAgglayerClientMock(t)
	mockLogger := &MockLogger{}
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

	// Expect logger to be called during initialization
	mockLogger.On("Infof", "Rate limiting enabled for method '%s': %s", "SendCertificate", mock.MatchedBy(func(s string) bool {
		return s != ""
	})).Return()

	// Expect rate limiting warning call
	mockLogger.On("Warnf", "rate limit reached for %s, sleeping for %s. Rate:%s",
		"SendCertificate",
		mock.MatchedBy(func(s string) bool {
			return s != ""
		}),
		mock.MatchedBy(func(s string) bool {
			return s != ""
		})).Return()

	wrapper := NewRateLimitWrapper(mockClient, config, mockLogger)

	// First call should not trigger rate limiting
	wrapper.applyRateLimit("SendCertificate")

	// Add a small delay to ensure the calls are not happening in the same microsecond
	time.Sleep(1 * time.Millisecond)

	// Second call should trigger rate limiting and logger
	wrapper.applyRateLimit("SendCertificate")

	mockLogger.AssertExpectations(t)
}

func TestRateLimitWrapper_GetCertificateHeader(t *testing.T) {
	t.Parallel()

	mockClient := mocks.NewAgglayerClientMock(t)
	config := ClientConfig{
		APIRateLimits: []APIRateLimitConfig{
			{
				MethodName: "GetCertificateHeader",
				RateLimit: aggkitcommon.RateLimitConfig{
					NumRequests: 1,
					Interval:    configtypes.Duration{Duration: 100 * time.Millisecond},
				},
			},
		},
	}

	mockLogger := &MockLogger{}
	setupMockLoggerExpectationsWithRateLimit(mockLogger, "GetCertificateHeader")

	wrapper := NewRateLimitWrapper(mockClient, config, mockLogger)

	// Test first call
	certHash := common.HexToHash("0x123")
	expectedHeader := &types.CertificateHeader{
		NetworkID: 1,
		Height:    100,
	}
	mockClient.EXPECT().GetCertificateHeader(mock.Anything, certHash).Return(expectedHeader, nil).Once()

	start := time.Now()
	header, err := wrapper.GetCertificateHeader(context.Background(), certHash)
	duration := time.Since(start)

	require.NoError(t, err)
	require.Equal(t, expectedHeader, header)
	require.Less(t, duration, 50*time.Millisecond) // Should be fast

	// Test second call (rate limited)
	mockClient.EXPECT().GetCertificateHeader(mock.Anything, certHash).Return(expectedHeader, nil).Once()

	time.Sleep(1 * time.Millisecond)

	start = time.Now()
	header, err = wrapper.GetCertificateHeader(context.Background(), certHash)
	duration = time.Since(start)

	require.NoError(t, err)
	require.Equal(t, expectedHeader, header)
	require.GreaterOrEqual(t, duration, 95*time.Millisecond) // Should be rate limited

	mockClient.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestRateLimitWrapper_GetEpochConfiguration(t *testing.T) {
	t.Parallel()

	mockClient := mocks.NewAgglayerClientMock(t)
	clientConfig := ClientConfig{
		APIRateLimits: []APIRateLimitConfig{
			{
				MethodName: "GetEpochConfiguration",
				RateLimit: aggkitcommon.RateLimitConfig{
					NumRequests: 1,
					Interval:    configtypes.Duration{Duration: 100 * time.Millisecond},
				},
			},
		},
	}

	mockLogger := &MockLogger{}
	setupMockLoggerExpectationsWithRateLimit(mockLogger, "GetEpochConfiguration")

	wrapper := NewRateLimitWrapper(mockClient, clientConfig, mockLogger)

	// Test first call
	expectedConfig := &types.ClockConfiguration{
		EpochDuration: 1000,
		GenesisBlock:  0,
	}
	mockClient.EXPECT().GetEpochConfiguration(mock.Anything).Return(expectedConfig, nil).Once()

	start := time.Now()
	epochConfig, err := wrapper.GetEpochConfiguration(context.Background())
	duration := time.Since(start)

	require.NoError(t, err)
	require.Equal(t, expectedConfig, epochConfig)
	require.Less(t, duration, 50*time.Millisecond) // Should be fast

	// Test second call (rate limited)
	mockClient.EXPECT().GetEpochConfiguration(mock.Anything).Return(expectedConfig, nil).Once()

	time.Sleep(1 * time.Millisecond)

	start = time.Now()
	epochConfig, err = wrapper.GetEpochConfiguration(context.Background())
	duration = time.Since(start)

	require.NoError(t, err)
	require.Equal(t, expectedConfig, epochConfig)
	require.GreaterOrEqual(t, duration, 95*time.Millisecond) // Should be rate limited

	mockClient.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

// testCertificateHeaderMethod is a helper function to test certificate header methods with rate limiting
func testCertificateHeaderMethod(t *testing.T, methodName string, height uint64, wrapper *RateLimitWrapper, mockClient *mocks.AgglayerClientMock, mockLogger *MockLogger) {
	t.Helper()

	// Test first call
	networkID := uint32(1)
	expectedHeader := &types.CertificateHeader{
		NetworkID: networkID,
		Height:    height,
	}
	mockClient.On(methodName, mock.Anything, networkID).Return(expectedHeader, nil).Once()

	start := time.Now()
	var header *types.CertificateHeader
	var err error

	if methodName == "GetLatestSettledCertificateHeader" {
		header, err = wrapper.GetLatestSettledCertificateHeader(context.Background(), networkID)
	} else {
		header, err = wrapper.GetLatestPendingCertificateHeader(context.Background(), networkID)
	}
	duration := time.Since(start)

	require.NoError(t, err)
	require.Equal(t, expectedHeader, header)
	require.Less(t, duration, 50*time.Millisecond) // Should be fast

	// Test second call (rate limited)
	mockClient.On(methodName, mock.Anything, networkID).Return(expectedHeader, nil).Once()

	time.Sleep(1 * time.Millisecond)

	start = time.Now()
	if methodName == "GetLatestSettledCertificateHeader" {
		header, err = wrapper.GetLatestSettledCertificateHeader(context.Background(), networkID)
	} else {
		header, err = wrapper.GetLatestPendingCertificateHeader(context.Background(), networkID)
	}
	duration = time.Since(start)

	require.NoError(t, err)
	require.Equal(t, expectedHeader, header)
	require.GreaterOrEqual(t, duration, 95*time.Millisecond) // Should be rate limited

	mockClient.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestRateLimitWrapper_GetLatestSettledCertificateHeader(t *testing.T) {
	t.Parallel()

	mockClient := mocks.NewAgglayerClientMock(t)
	config := ClientConfig{
		APIRateLimits: []APIRateLimitConfig{
			{
				MethodName: "GetLatestSettledCertificateHeader",
				RateLimit: aggkitcommon.RateLimitConfig{
					NumRequests: 1,
					Interval:    configtypes.Duration{Duration: 100 * time.Millisecond},
				},
			},
		},
	}
	mockLogger := &MockLogger{}
	setupMockLoggerExpectationsWithRateLimit(mockLogger, "GetLatestSettledCertificateHeader")

	wrapper := NewRateLimitWrapper(mockClient, config, mockLogger)

	testCertificateHeaderMethod(t, "GetLatestSettledCertificateHeader", 200, wrapper, mockClient, mockLogger)
}

func TestRateLimitWrapper_GetLatestPendingCertificateHeader(t *testing.T) {
	t.Parallel()

	mockClient := mocks.NewAgglayerClientMock(t)
	config := ClientConfig{
		APIRateLimits: []APIRateLimitConfig{
			{
				MethodName: "GetLatestPendingCertificateHeader",
				RateLimit: aggkitcommon.RateLimitConfig{
					NumRequests: 1,
					Interval:    configtypes.Duration{Duration: 100 * time.Millisecond},
				},
			},
		},
	}
	mockLogger := &MockLogger{}
	setupMockLoggerExpectationsWithRateLimit(mockLogger, "GetLatestPendingCertificateHeader")

	wrapper := NewRateLimitWrapper(mockClient, config, mockLogger)

	testCertificateHeaderMethod(t, "GetLatestPendingCertificateHeader", 300, wrapper, mockClient, mockLogger)
}

// MockLogger is a mock implementation for testing
type MockLogger struct {
	mock.Mock
}

// setupMockLoggerExpectations sets up the mock logger to expect initialization calls
func setupMockLoggerExpectations(mockLogger *MockLogger, methodNames ...string) {
	for _, methodName := range methodNames {
		mockLogger.On("Infof", "Rate limiting enabled for method '%s': %s", methodName, mock.MatchedBy(func(s string) bool {
			return s != ""
		})).Return()
	}
}

// setupMockLoggerExpectationsWithRateLimit sets up the mock logger to expect both initialization and rate limiting calls
func setupMockLoggerExpectationsWithRateLimit(mockLogger *MockLogger, methodNames ...string) {
	for _, methodName := range methodNames {
		// Expect initialization call
		mockLogger.On("Infof", "Rate limiting enabled for method '%s': %s", methodName, mock.MatchedBy(func(s string) bool {
			return s != ""
		})).Return()

		// Expect rate limiting warning call
		mockLogger.On("Warnf", "rate limit reached for %s, sleeping for %s. Rate:%s",
			methodName,
			mock.MatchedBy(func(s string) bool {
				return s != ""
			}),
			mock.MatchedBy(func(s string) bool {
				return s != ""
			})).Return()
	}
}

func (m *MockLogger) Panicf(format string, args ...interface{}) {
	allArgs := make([]interface{}, 0, len(args)+1)
	allArgs = append(allArgs, format)
	allArgs = append(allArgs, args...)
	m.Called(allArgs...)
}

func (m *MockLogger) Fatalf(format string, args ...interface{}) {
	allArgs := make([]interface{}, 0, len(args)+1)
	allArgs = append(allArgs, format)
	allArgs = append(allArgs, args...)
	m.Called(allArgs...)
}

func (m *MockLogger) Debugf(format string, args ...interface{}) {
	allArgs := make([]interface{}, 0, len(args)+1)
	allArgs = append(allArgs, format)
	allArgs = append(allArgs, args...)
	m.Called(allArgs...)
}

func (m *MockLogger) Infof(format string, args ...interface{}) {
	allArgs := make([]interface{}, 0, len(args)+1)
	allArgs = append(allArgs, format)
	allArgs = append(allArgs, args...)
	m.Called(allArgs...)
}

func (m *MockLogger) Warnf(format string, args ...interface{}) {
	allArgs := make([]interface{}, 0, len(args)+1)
	allArgs = append(allArgs, format)
	allArgs = append(allArgs, args...)
	m.Called(allArgs...)
}

func (m *MockLogger) Errorf(format string, args ...interface{}) {
	allArgs := make([]interface{}, 0, len(args)+1)
	allArgs = append(allArgs, format)
	allArgs = append(allArgs, args...)
	m.Called(allArgs...)
}

func (m *MockLogger) Debug(args ...interface{}) {
	m.Called(args)
}

func (m *MockLogger) Info(args ...interface{}) {
	m.Called(args)
}

func (m *MockLogger) Warn(args ...interface{}) {
	m.Called(args)
}

func (m *MockLogger) Error(args ...interface{}) {
	m.Called(args)
}
