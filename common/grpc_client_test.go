package common

import (
	"errors"
	"fmt"
	"testing"

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
