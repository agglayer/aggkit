package etherman

import (
	"fmt"

	"github.com/agglayer/aggkit/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
)

func NewRPCClient(cfg config.L2RPCClientConfig) (aggkittypes.EthClienter, error) {
	switch cfg.Mode {
	case config.RPCModeBasic:
		log.Debugf("Creating basic RPC client with URL %s", cfg.URL)
		ethClient, err := aggkittypes.DialWithRetry(cfg.URL, cfg.MaxRetries, cfg.InitialBackoff.Duration)
		if err != nil {
			return nil, fmt.Errorf("fails to create basic RPC client. Err: %w", err)
		}
		return ethClient, nil
	case config.RPCModeOp:
		return NewRPCClientModeOp(cfg)
	}
	log.Fatalf("Invalid RPC mode %s", cfg.Mode)
	return nil, fmt.Errorf("Invalid RPC mode %s", cfg.Mode)
}
