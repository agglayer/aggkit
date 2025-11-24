package etherman

import (
	"context"
	"fmt"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
)

// NewRPCClient creates a new RPC client based on the provided configuration.
// It supports both basic RPC mode and OPNode mode.
// In basic mode, it simply creates a client with the given URL.
// In OPNode mode, it creates a client that uses the OPNode client to get the finalized block.
func NewRPCClient(ctx context.Context, cfg ethermanconfig.L2RPCClientConfig) (aggkittypes.EthClienter, error) {
	switch cfg.Mode {
	case ethermanconfig.RPCModeBasic:
		log.Debugf("Creating basic RPC client with URL %s", cfg.URL)
		retryHandler, err := cfg.NewRetryHandler()
		if err != nil {
			return nil, fmt.Errorf("failed to create retry handler: %w", err)
		}
		ethClient, err := aggkittypes.DialWithRetry(ctx, cfg.URL, retryHandler)
		if err != nil {
			return nil, fmt.Errorf("fails to create basic RPC client. Err: %w", err)
		}
		return ethClient, nil
	case ethermanconfig.RPCModeOp:
		return NewRPCClientModeOp(ctx, cfg)
	}
	return nil, fmt.Errorf("invalid RPC mode %s", cfg.Mode)
}
