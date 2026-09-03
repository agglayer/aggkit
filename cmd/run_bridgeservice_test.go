package main

import (
	"testing"

	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/config"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestBuildPublicConfig(t *testing.T) {
	finalizedBlock, err := aggkittypes.NewBlockNumberFinality("FinalizedBlock")
	require.NoError(t, err)
	latestBlock, err := aggkittypes.NewBlockNumberFinality("LatestBlock")
	require.NoError(t, err)

	globalExitRootManagerAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	rollupManagerAddr := common.HexToAddress("0x2222222222222222222222222222222222222222")
	bridgeL1Addr := common.HexToAddress("0x3333333333333333333333333333333333333333")
	globalExitRootL2Addr := common.HexToAddress("0x4444444444444444444444444444444444444444")
	bridgeL2Addr := common.HexToAddress("0x5555555555555555555555555555555555555555")

	cfg := &config.Config{
		L1NetworkConfig: ethermanconfig.L1NetworkConfig{
			GlobalExitRootManagerAddr: globalExitRootManagerAddr,
			RollupManagerAddr:         rollupManagerAddr,
		},
		L1InfoTreeSync: l1infotreesync.Config{
			BlockFinality:      *finalizedBlock,
			InitialBlock:       100,
			SyncBlockChunkSize: 50,
		},
		BridgeL1Sync: bridgesync.Config{
			BlockFinality:      *latestBlock,
			InitialBlockNum:    0,
			SyncBlockChunkSize: 100,
			BridgeAddr:         bridgeL1Addr,
		},
		BridgeL2Sync: bridgesync.Config{
			BlockFinality:      *latestBlock,
			InitialBlockNum:    0,
			SyncBlockChunkSize: 100,
			BridgeAddr:         bridgeL2Addr,
		},
		L2GERSync: l2gersync.Config{
			BlockFinality:        *latestBlock,
			InitialBlockNum:      0,
			SyncBlockChunkSize:   100,
			GlobalExitRootL2Addr: globalExitRootL2Addr,
		},
	}

	got := buildPublicConfig(cfg)

	expected := bridgetypes.PublicConfigResponse{
		Components: bridgetypes.PublicComponentsConfig{
			L1InfoTreeSync: &bridgetypes.SyncComponentConfig{
				BlockFinality: "FinalizedBlock", InitialBlock: 100, SyncBlockChunkSize: 50,
			},
			BridgeL1Sync: &bridgetypes.SyncComponentConfig{
				BlockFinality: "LatestBlock", InitialBlock: 0, SyncBlockChunkSize: 100,
			},
			BridgeL2Sync: &bridgetypes.SyncComponentConfig{
				BlockFinality: "LatestBlock", InitialBlock: 0, SyncBlockChunkSize: 100,
			},
			L2GERSync: &bridgetypes.SyncComponentConfig{
				BlockFinality: "LatestBlock", InitialBlock: 0, SyncBlockChunkSize: 100,
			},
		},
		Contracts: bridgetypes.PublicContractsConfig{
			L1: bridgetypes.L1ContractsConfig{
				GlobalExitRootAddr: bridgetypes.Address(globalExitRootManagerAddr.Hex()),
				RollupManagerAddr:  bridgetypes.Address(rollupManagerAddr.Hex()),
				BridgeAddr:         bridgetypes.Address(bridgeL1Addr.Hex()),
			},
			L2: bridgetypes.L2ContractsConfig{
				GlobalExitRootAddr: bridgetypes.Address(globalExitRootL2Addr.Hex()),
				BridgeAddr:         bridgetypes.Address(bridgeL2Addr.Hex()),
			},
		},
	}

	require.Equal(t, expected, got)
}
