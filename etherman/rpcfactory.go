package etherman

import (
	"fmt"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/ethclient"
	ethrpc "github.com/ethereum/go-ethereum/rpc"
)

type EthClienter interface {
	ethereum.LogFilterer
	ethereum.BlockNumberReader
	ethereum.ChainReader
	ethereum.ChainIDReader
	bind.ContractBackend
	Client() *ethrpc.Client
}

func NewRPCClient(cfg ethermanconfig.RPCClientConfig) (EthClienter, error) {
	switch cfg.Mode {
	case ethermanconfig.RPCModeBasic:
		log.Debugf("Creating basic RPC client with URL %s", cfg.URL)
		basicClient, err := ethclient.Dial(cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("fails to create basic RPC client. Err: %w", err)
		}
		return basicClient, nil
	case ethermanconfig.RPCModeOp:
		return NewRPCClientModeOp(cfg)
	}
	log.Fatalf("Invalid RPC mode %s", cfg.Mode)
	return nil, fmt.Errorf("Invalid RPC mode %s", cfg.Mode)
}
