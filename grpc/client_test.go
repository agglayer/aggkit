package grpc

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/config/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRepackGRPCErrorWithDetails(t *testing.T) {
	t.Run("NonGRPCError", func(t *testing.T) {
		err := errors.New("non-gRPC error")
		result := RepackGRPCErrorWithDetails(err)
		require.ErrorIs(t, err, result)
	})

	t.Run("GRPCErrorWithoutDetails", func(t *testing.T) {
		st := status.New(codes.InvalidArgument, "invalid argument")
		err := GRPCError{
			Code:    st.Code(),
			Message: st.Message(),
			Details: nil,
		}
		result := RepackGRPCErrorWithDetails(err)
		expected := err.Error()
		require.Equal(t, expected, result.Error())
	})

	t.Run("GRPCErrorWithDetails", func(t *testing.T) {
		st := status.New(codes.InvalidArgument, "invalid argument")
		detail := &errdetails.ErrorInfo{
			Reason:   "InvalidInput",
			Domain:   "example.com",
			Metadata: map[string]string{"field": "value"},
		}
		stWithDetails, err := st.WithDetails(detail)
		require.NoError(t, err)

		expectedErr := GRPCError{
			Code:    stWithDetails.Code(),
			Message: stWithDetails.Message(),
			Details: []string{"Reason: InvalidInput, Domain: example.com. , Metadata: {field: value}"},
		}

		result := RepackGRPCErrorWithDetails(stWithDetails.Err())
		require.Equal(t, expectedErr.Error(), result.Error())
	})

	t.Run("GRPCErrorWithMultipleDetails", func(t *testing.T) {
		st := status.New(codes.InvalidArgument, "invalid argument")
		detail1 := &errdetails.ErrorInfo{
			Reason:   "InvalidInput",
			Domain:   "example.com",
			Metadata: map[string]string{"field1": "value1"},
		}
		detail2 := &errdetails.ErrorInfo{
			Reason:   "AnotherReason",
			Domain:   "another.com",
			Metadata: map[string]string{"field2": "value2"},
		}
		stWithDetails, err := st.WithDetails(detail1, detail2)
		require.NoError(t, err)

		expectedErr := GRPCError{
			Code:    stWithDetails.Code(),
			Message: stWithDetails.Message(),
			Details: []string{"Reason: InvalidInput, Domain: example.com. , Metadata: {field1: value1}", "Reason: AnotherReason, Domain: another.com. , Metadata: {field2: value2}"},
		}

		result := RepackGRPCErrorWithDetails(stWithDetails.Err())
		require.Equal(t, expectedErr.Error(), result.Error())
	})
}

func TestGRPCCodeCanonicalString(t *testing.T) {
	tests := []struct {
		code     codes.Code
		expected string
	}{
		{codes.OK, "OK"},
		{codes.Canceled, "CANCELED"},
		{codes.Unknown, "UNKNOWN"},
		{codes.InvalidArgument, "INVALID_ARGUMENT"},
		{codes.DeadlineExceeded, "DEADLINE_EXCEEDED"},
		{codes.NotFound, "NOT_FOUND"},
		{codes.AlreadyExists, "ALREADY_EXISTS"},
		{codes.PermissionDenied, "PERMISSION_DENIED"},
		{codes.ResourceExhausted, "RESOURCE_EXHAUSTED"},
		{codes.FailedPrecondition, "FAILED_PRECONDITION"},
		{codes.Aborted, "ABORTED"},
		{codes.OutOfRange, "OUT_OF_RANGE"},
		{codes.Unimplemented, "UNIMPLEMENTED"},
		{codes.Internal, "INTERNAL"},
		{codes.Unavailable, "UNAVAILABLE"},
		{codes.DataLoss, "DATA_LOSS"},
		{codes.Unauthenticated, "UNAUTHENTICATED"},
	}

	for _, tt := range tests {
		t.Run(tt.code.String(), func(t *testing.T) {
			result := grpcCodeCanonicalString(tt.code)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestClientConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *ClientConfig
		wantErr string
	}{
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: "gRPC client configuration cannot be nil",
		},
		{
			name: "empty URL",
			cfg: &ClientConfig{
				URL:               "",
				MinConnectTimeout: types.Duration{Duration: 1 * time.Second},
				RequestTimeout:    types.Duration{Duration: 5 * time.Second},
				Retry: &RetryConfig{
					InitialBackoff:    types.Duration{Duration: 500 * time.Millisecond},
					MaxBackoff:        types.Duration{Duration: 5 * time.Second},
					BackoffMultiplier: 2.0,
					MaxAttempts:       3,
				},
			},
			wantErr: "gRPC client URL cannot be empty",
		},
		{
			name: "zero MinConnectTimeout",
			cfg: &ClientConfig{
				URL:               "localhost:1234",
				MinConnectTimeout: types.Duration{Duration: 0},
				RequestTimeout:    types.Duration{Duration: 5 * time.Second},
				Retry: &RetryConfig{
					InitialBackoff:    types.Duration{Duration: 500 * time.Millisecond},
					MaxBackoff:        types.Duration{Duration: 5 * time.Second},
					BackoffMultiplier: 2.0,
					MaxAttempts:       3,
				},
			},
			wantErr: "MinConnectTimeout must be greater than zero",
		},
		{
			name: "initial backoff >= max backoff",
			cfg: &ClientConfig{
				URL:               "localhost:1234",
				MinConnectTimeout: types.Duration{Duration: 1 * time.Second},
				RequestTimeout:    types.Duration{Duration: 5 * time.Second},
				Retry: &RetryConfig{
					InitialBackoff:    types.Duration{Duration: 5 * time.Second},
					MaxBackoff:        types.Duration{Duration: 2 * time.Second},
					BackoffMultiplier: 2.0,
					MaxAttempts:       3,
				},
			},
			wantErr: "InitialBackoff must be less than MaxBackoff",
		},
		{
			name: "backoff multiplier too small",
			cfg: &ClientConfig{
				URL:               "localhost:1234",
				MinConnectTimeout: types.Duration{Duration: 1 * time.Second},
				RequestTimeout:    types.Duration{Duration: 5 * time.Second},
				Retry: &RetryConfig{
					InitialBackoff:    types.Duration{Duration: 1 * time.Second},
					MaxBackoff:        types.Duration{Duration: 5 * time.Second},
					BackoffMultiplier: 0.5,
					MaxAttempts:       3,
				},
			},
			wantErr: "BackoffMultiplier must be greater than 1.0",
		},
		{
			name: "max attempts too small",
			cfg: &ClientConfig{
				URL:               "localhost:1234",
				MinConnectTimeout: types.Duration{Duration: 1 * time.Second},
				RequestTimeout:    types.Duration{Duration: 5 * time.Second},
				Retry: &RetryConfig{
					InitialBackoff:    types.Duration{Duration: 1 * time.Second},
					MaxBackoff:        types.Duration{Duration: 5 * time.Second},
					BackoffMultiplier: 2.0,
					MaxAttempts:       0,
				},
			},
			wantErr: "MaxAttempts must be at least 1",
		},
		{
			name: "request timeout too short",
			cfg: &ClientConfig{
				URL:               "localhost:1234",
				MinConnectTimeout: types.Duration{Duration: 1 * time.Second},
				RequestTimeout:    types.Duration{Duration: 1 * time.Second}, // too short
				Retry: &RetryConfig{
					InitialBackoff:    types.Duration{Duration: 1 * time.Second},
					MaxBackoff:        types.Duration{Duration: 10 * time.Second},
					BackoffMultiplier: 2.0,
					MaxAttempts:       5,
				},
			},
			wantErr: "RequestTimeout (1s) is too short", // partial match
		},
		{
			name: "initial backoff invalid",
			cfg: &ClientConfig{
				URL:               "localhost:1234",
				MinConnectTimeout: types.Duration{Duration: 1 * time.Second},
				RequestTimeout:    types.Duration{Duration: 1 * time.Second}, // too short
				Retry: &RetryConfig{
					InitialBackoff:    types.Duration{Duration: 0},
					MaxBackoff:        types.Duration{Duration: 0},
					BackoffMultiplier: 2.0,
					MaxAttempts:       5,
				},
			},
			wantErr: "InitialBackoff must be greater than zero",
		},
		{
			name: "max backoff invalid",
			cfg: &ClientConfig{
				URL:               "localhost:1234",
				MinConnectTimeout: types.Duration{Duration: 1 * time.Second},
				RequestTimeout:    types.Duration{Duration: 1 * time.Second}, // too short
				Retry: &RetryConfig{
					InitialBackoff:    types.Duration{Duration: 1 * time.Second},
					MaxBackoff:        types.Duration{Duration: 0},
					BackoffMultiplier: 2.0,
					MaxAttempts:       5,
				},
			},
			wantErr: "MaxBackoff must be greater than zero",
		},
		{
			name: "valid config",
			cfg: &ClientConfig{
				URL:               "localhost:1234",
				MinConnectTimeout: types.Duration{Duration: 1 * time.Second},
				RequestTimeout:    types.Duration{Duration: 5 * time.Second},
				Retry: &RetryConfig{
					InitialBackoff:    types.Duration{Duration: 500 * time.Millisecond},
					MaxBackoff:        types.Duration{Duration: 5 * time.Second},
					BackoffMultiplier: 1.5,
					MaxAttempts:       3,
				},
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" && err != nil {
				t.Errorf("expected no error, got %v", err)
			} else if tt.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tt.wantErr) {
					t.Errorf("expected error prefix: %q, got: %v", tt.wantErr, err)
				}
			}
		})
	}
}

func TestGenerateServiceConfig(t *testing.T) {
	defaultRetry := RetryConfig{
		MaxAttempts:       4,
		InitialBackoff:    types.NewDuration(100 * time.Millisecond),
		MaxBackoff:        types.NewDuration(2 * time.Second),
		BackoffMultiplier: 1.5,
	}

	t.Run("retry policy applied to all methods", func(t *testing.T) {
		cfg := defaultRetry
		cfg.Excluded = nil

		sc, err := createServiceConfig(&cfg)
		require.NoError(t, err)
		require.Contains(t, sc, `"name":[{}]`)
		require.Contains(t, sc, `"retryPolicy"`)
	})

	t.Run("retry excluded for one method", func(t *testing.T) {
		cfg := defaultRetry
		cfg.Excluded = []Method{{ServiceName: "some.Service", MethodName: "Foo"}}

		sc, err := createServiceConfig(&cfg)
		require.NoError(t, err)
		require.Contains(t, sc, `"name":[{"service":"some.Service","method":"Foo"}]`)
		require.Contains(t, sc, `"name":[{}]`) // default retry for others
	})

	t.Run("retry excluded for entire service", func(t *testing.T) {
		cfg := defaultRetry
		cfg.Excluded = []Method{{ServiceName: "some.Service", MethodName: ""}}

		sc, err := createServiceConfig(&cfg)
		require.NoError(t, err)
		require.Contains(t, sc, `"name":[{"service":"some.Service"}]`)
		require.Contains(t, sc, `"name":[{}]`) // default retry for all others
	})
}

func TestGRPCError_Is(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		err1     error
		err2     error
		expected bool
	}{
		{
			name: "MatchSameCodeAndMessage",
			err1: &GRPCError{
				Code:    codes.InvalidArgument,
				Message: "invalid argument",
			},
			err2: &GRPCError{
				Code:    codes.InvalidArgument,
				Message: "invalid argument",
			},
			expected: true,
		},
		{
			name: "MatchSameCodeAndPartialMessage",
			err1: &GRPCError{
				Code:    codes.InvalidArgument,
				Message: "invalid argument with extra info",
			},
			err2: &GRPCError{
				Code:    codes.InvalidArgument,
				Message: "invalid argument",
			},
			expected: true,
		},
		{
			name: "MatchSameCodeAndDifferentCaseMessage",
			err1: &GRPCError{
				Code:    codes.InvalidArgument,
				Message: "Invalid Argument",
			},
			err2: &GRPCError{
				Code:    codes.InvalidArgument,
				Message: "invalid argument",
			},
			expected: true,
		},
		{
			name: "DifferentCode",
			err1: &GRPCError{
				Code:    codes.InvalidArgument,
				Message: "invalid argument",
			},
			err2: &GRPCError{
				Code:    codes.NotFound,
				Message: "invalid argument",
			},
			expected: false,
		},
		{
			name: "DifferentMessage",
			err1: &GRPCError{
				Code:    codes.InvalidArgument,
				Message: "invalid argument",
			},
			err2: &GRPCError{
				Code:    codes.InvalidArgument,
				Message: "different message",
			},
			expected: false,
		},
		{
			name: "UnknownCode",
			err1: &GRPCError{
				Code:    codes.Unknown,
				Message: "unknown error",
			},
			err2: &GRPCError{
				Code:    codes.InvalidArgument,
				Message: "invalid argument",
			},
			expected: false,
		},
		{
			name: "NonGRPCErrorAgainstGRPCError",
			err1: errors.New("non-gRPC error"),
			err2: &GRPCError{
				Code:    codes.InvalidArgument,
				Message: "invalid argument",
			},
			expected: false,
		},
		{
			name: "WrappedGRPCError",
			err1: fmt.Errorf("wrapped error: %w", &GRPCError{
				Code:    codes.InvalidArgument,
				Message: "invalid argument",
			}),
			err2: &GRPCError{
				Code:    codes.InvalidArgument,
				Message: "invalid argument",
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.expected, errors.Is(tc.err1, tc.err2), "Is should match expected result")
		})
	}
}

func TestValidateRequestTimeout(t *testing.T) {
	tests := []struct {
		name         string
		timeout      types.Duration
		retryConfig  *RetryConfig
		expectError  bool
		errorMessage string
	}{
		{
			name:    "valid - enough timeout for backoff and calls",
			timeout: types.NewDuration(30 * time.Second),
			retryConfig: &RetryConfig{
				InitialBackoff:    types.NewDuration(1 * time.Second),
				MaxBackoff:        types.NewDuration(10 * time.Second),
				BackoffMultiplier: 2.0,
				MaxAttempts:       5,
			},
			expectError: false,
		},
		{
			name:    "valid - just enough for backoff",
			timeout: types.NewDuration(7 * time.Second),
			retryConfig: &RetryConfig{
				InitialBackoff:    types.NewDuration(1 * time.Second),
				MaxBackoff:        types.NewDuration(10 * time.Second),
				BackoffMultiplier: 2.0,
				MaxAttempts:       4,
			},
			expectError: false,
		},
		{
			name:    "invalid - timeout too short for retry backoff",
			timeout: types.NewDuration(2 * time.Second),
			retryConfig: &RetryConfig{
				InitialBackoff:    types.NewDuration(1 * time.Second),
				MaxBackoff:        types.NewDuration(10 * time.Second),
				BackoffMultiplier: 2.0,
				MaxAttempts:       4,
			},
			expectError:  true,
			errorMessage: "RequestTimeout",
		},
		{
			name:    "valid - single attempt, no retry",
			timeout: types.NewDuration(1 * time.Second),
			retryConfig: &RetryConfig{
				InitialBackoff:    types.NewDuration(1 * time.Second),
				MaxBackoff:        types.NewDuration(1 * time.Second),
				BackoffMultiplier: 2.0,
				MaxAttempts:       1,
			},
			expectError: false,
		},
		{
			name:    "invalid - zero timeout",
			timeout: types.NewDuration(0),
			retryConfig: &RetryConfig{
				InitialBackoff:    types.NewDuration(1 * time.Second),
				MaxBackoff:        types.NewDuration(10 * time.Second),
				BackoffMultiplier: 2.0,
				MaxAttempts:       3,
			},
			expectError:  true,
			errorMessage: "RequestTimeout",
		},
		{
			name:    "valid - large timeout with high retries",
			timeout: types.NewDuration(2 * time.Minute),
			retryConfig: &RetryConfig{
				InitialBackoff:    types.NewDuration(2 * time.Second),
				MaxBackoff:        types.NewDuration(15 * time.Second),
				BackoffMultiplier: 1.5,
				MaxAttempts:       10,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientCfg := &ClientConfig{
				RequestTimeout: tt.timeout,
				Retry:          tt.retryConfig,
			}
			err := clientCfg.validateRequestTimeout()

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errorMessage)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
