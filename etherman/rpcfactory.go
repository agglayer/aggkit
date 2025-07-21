package etherman

import (
	"fmt"

	"github.com/agglayer/aggkit/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
)

// NewRPCClient creates a new RPC client based on the provided configuration.
// It supports both basic RPC mode and OPNode mode.
// In basic mode, it simply creates a client with the given URL.
// In OPNode mode, it creates a client that uses the OPNode client to get the finalized block.
func NewRPCClient(cfg config.L2RPCClientConfig) (aggkittypes.EthClienter, error) {
	switch cfg.Mode {
	case config.RPCModeBasic:
		log.Debugf("Creating basic RPC client with URL %s", cfg.URL)
		ethClient, err := aggkittypes.DialWithRetry(cfg.URL, cfg.MaxRetries,
			cfg.InitialBackoff.Duration, cfg.MaxBackoff.Duration)
		if err != nil {
			return nil, fmt.Errorf("fails to create basic RPC client. Err: %w", err)
		}
		return ethClient, nil
	case config.RPCModeOp:
		return NewRPCClientModeOp(cfg)
	}
	return nil, fmt.Errorf("invalid RPC mode %s", cfg.Mode)
}
