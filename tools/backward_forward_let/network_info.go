package backward_forward_let

import (
	"context"
	"errors"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"google.golang.org/grpc/codes"
)

type networkInfoClient interface {
	GetNetworkInfo(ctx context.Context, networkID uint32) (agglayertypes.NetworkInfo, error)
}

func getNetworkInfoAllowNotFound(
	ctx context.Context,
	client networkInfoClient,
	networkID uint32,
) (agglayertypes.NetworkInfo, bool, error) {
	info, err := client.GetNetworkInfo(ctx, networkID)
	if err == nil {
		return info, false, nil
	}

	var grpcErr aggkitgrpc.GRPCError
	if errors.As(err, &grpcErr) && grpcErr.Code == codes.NotFound {
		return agglayertypes.NetworkInfo{}, true, nil
	}
	return agglayertypes.NetworkInfo{}, false, fmt.Errorf("get network info from agglayer: %w", err)
}
