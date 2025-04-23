package common

import (
	"errors"
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
		err := st.Err()
		result := RepackGRPCErrorWithDetails(err)
		expected := "invalid argument - Details: none"
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

		result := RepackGRPCErrorWithDetails(stWithDetails.Err())
		expected := "invalid argument - Details: [Reason: InvalidInput, Domain: example.com. , Metadata: {field: value}]"
		require.Equal(t, expected, result.Error())
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

		result := RepackGRPCErrorWithDetails(stWithDetails.Err())
		expected := "invalid argument - Details: [Reason: InvalidInput, Domain: example.com. , Metadata: {field1: value1};Reason: AnotherReason, Domain: another.com. , Metadata: {field2: value2}]"
		require.Equal(t, expected, result.Error())
	})
}
