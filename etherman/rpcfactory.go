package etherman

import (
	"fmt"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

func NewRPCClient(cfg ethermanconfig.RPCClientConfig) (aggkittypes.EthClienter, error) {
	switch cfg.Mode {
	case ethermanconfig.RPCModeBasic:
		log.Debugf("Creating basic RPC client with URL %s", cfg.URL)
		basicClient, err := ethclient.Dial(cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("fails to create basic RPC client. Err: %w", err)
		}
		return aggkittypes.NewDefaultEthClient(basicClient, basicClient.Client()), nil
	case ethermanconfig.RPCModeOp:
		return NewRPCClientModeOp(cfg)
	}
	log.Fatalf("Invalid RPC mode %s", cfg.Mode)
	return nil, fmt.Errorf("Invalid RPC mode %s", cfg.Mode)
}
