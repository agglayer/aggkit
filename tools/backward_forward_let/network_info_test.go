package backward_forward_let

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
)

type stubNetworkInfoClient struct {
	info agglayertypes.NetworkInfo
	err  error
}

func (s stubNetworkInfoClient) GetNetworkInfo(
	_ context.Context,
	_ uint32,
) (agglayertypes.NetworkInfo, error) {
	return s.info, s.err
}

func TestGetNetworkInfoAllowNotFound(t *testing.T) {
	t.Parallel()

	info, notFound, err := getNetworkInfoAllowNotFound(context.Background(), stubNetworkInfoClient{
		err: aggkitgrpc.GRPCError{Code: codes.NotFound, Message: "not found"},
	}, 1)

	require.NoError(t, err)
	require.True(t, notFound)
	require.Empty(t, info)
}

func TestGetNetworkInfoAllowNotFound_OtherError(t *testing.T) {
	t.Parallel()

	_, notFound, err := getNetworkInfoAllowNotFound(context.Background(), stubNetworkInfoClient{
		err: errors.New("boom"),
	}, 1)

	require.Error(t, err)
	require.False(t, notFound)
}
