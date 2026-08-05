package main

import (
	"context"
	"fmt"
	"math/big"
	"os"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	ethermanquerier "github.com/agglayer/aggkit/etherman/querier"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v2"
)

func main() {
	app := cli.NewApp()
	app.Name = "get-initial-ler"
	app.Usage = "Query the initial Local Exit Root (LER) for a rollup from the RollupManager contract"
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:     "l1-rpc",
			Usage:    "L1 RPC URL",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "rollup-manager-addr",
			Usage:    "Address of the RollupManager contract on L1",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "rollup-addr",
			Usage:    "Address of the rollup contract on L1 (used to look up rollup ID)",
			Required: true,
		},
		&cli.Uint64Flag{
			Name:     "l1-genesis-block",
			Usage:    "L1 block number at which to query the rollup data",
			Required: true,
		},
	}
	app.Action = run

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(cliCtx *cli.Context) error {
	ctx := context.Background()

	l1RPCURL := cliCtx.String("l1-rpc")
	rollupManagerAddr := common.HexToAddress(cliCtx.String("rollup-manager-addr"))
	rollupAddr := common.HexToAddress(cliCtx.String("rollup-addr"))
	l1GenesisBlock := cliCtx.Uint64("l1-genesis-block")

	rpcCfg := ethermanconfig.NewDefaultRPCClientConfig()
	rpcCfg.URL = l1RPCURL

	ethClient, err := etherman.DialWithRetry(ctx, nil, rpcCfg)
	if err != nil {
		return fmt.Errorf("connect to L1 RPC: %w", err)
	}

	l1Config := ethermanconfig.L1NetworkConfig{
		RPC:                        *rpcCfg,
		RollupManagerAddr:          rollupManagerAddr,
		RollupAddr:                 rollupAddr,
		BlocksChunkSize:            1000, //nolint:mnd
		RollupManagerCreationBlock: 1,
	}

	querier, err := ethermanquerier.NewRollupDataQuerier(ctx, l1Config, ethClient,
		func(addr common.Address, client aggkittypes.BaseEthereumClienter) (ethermanquerier.RollupManagerContract, error) {
			return agglayermanager.NewAgglayermanager(addr, client)
		})
	if err != nil {
		return fmt.Errorf("create rollup data querier: %w", err)
	}

	rollupData, err := querier.GetRollupData(new(big.Int).SetUint64(l1GenesisBlock))
	if err != nil {
		return fmt.Errorf("get rollup data at block %d: %w", l1GenesisBlock, err)
	}

	ler := common.Hash(rollupData.LastLocalExitRoot)
	if ler == aggkitcommon.ZeroHash {
		ler = bridgesynctypes.EmptyLER
		fmt.Printf("LastLocalExitRoot is zero at block %d -> using EmptyLER\n", l1GenesisBlock)
	}

	fmt.Printf("RollupID:             %d\n", querier.RollupID)
	fmt.Printf("L1 genesis block:     %d\n", l1GenesisBlock)
	fmt.Printf("InitialLocalExitRoot: %s\n", ler.Hex())

	return nil
}
