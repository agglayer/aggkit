package main

import (
	"encoding/json"
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

const testNetworkID = uint32(10)

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

	allRunning := runningBridgeComponents{
		L1InfoTreeSync: true,
		BridgeL1Sync:   true,
		BridgeL2Sync:   true,
		L2GERSync:      true,
	}
	got, err := buildPublicConfig(cfg, testNetworkID, string(l2gersync.SovereignChain), allRunning)
	require.NoError(t, err)

	expectedSha1Sum, err := cfg.Sha1Sum()
	require.NoError(t, err)
	require.NotEmpty(t, expectedSha1Sum)

	expected := bridgetypes.PublicConfigResponse{
		NetworkID:     testNetworkID,
		ConfigSha1Sum: expectedSha1Sum,
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
			L2GERSync: &bridgetypes.L2GERSyncComponentConfig{
				SyncComponentConfig: bridgetypes.SyncComponentConfig{
					BlockFinality: "LatestBlock", InitialBlock: 0, SyncBlockChunkSize: 100,
				},
				SyncMode: "SovereignChain",
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

func TestBuildPublicConfig_L2GERSyncModeEmptyWhenRunningWithoutMode(t *testing.T) {
	cfg := &config.Config{}

	got, err := buildPublicConfig(cfg, testNetworkID, "", runningBridgeComponents{L2GERSync: true})
	require.NoError(t, err)

	require.NotNil(t, got.Components.L2GERSync)
	require.Empty(t, got.Components.L2GERSync.SyncMode)
}

func TestBuildPublicConfig_ComponentsOmittedWhenNotRunning(t *testing.T) {
	cfg := &config.Config{}

	// None of the components are running: all of them must be omitted from the response,
	// including from its JSON encoding (relies on the omitempty tags in PublicComponentsConfig).
	got, err := buildPublicConfig(cfg, testNetworkID, "", runningBridgeComponents{})
	require.NoError(t, err)

	require.Nil(t, got.Components.L1InfoTreeSync)
	require.Nil(t, got.Components.BridgeL1Sync)
	require.Nil(t, got.Components.BridgeL2Sync)
	require.Nil(t, got.Components.L2GERSync)

	marshaled, err := json.Marshal(got.Components)
	require.NoError(t, err)
	require.JSONEq(t, "{}", string(marshaled))
}

func TestBuildPublicConfig_OnlyRunningComponentsPopulated(t *testing.T) {
	cfg := &config.Config{}

	got, err := buildPublicConfig(cfg, testNetworkID, "", runningBridgeComponents{
		BridgeL1Sync: true,
		L2GERSync:    true,
	})
	require.NoError(t, err)

	require.Nil(t, got.Components.L1InfoTreeSync)
	require.NotNil(t, got.Components.BridgeL1Sync)
	require.Nil(t, got.Components.BridgeL2Sync)
	require.NotNil(t, got.Components.L2GERSync)
}

func TestBuildPublicConfig_ChecksumChangesWithConfig(t *testing.T) {
	cfgA := &config.Config{}
	cfgB := &config.Config{}
	cfgB.BridgeL1Sync.SyncBlockChunkSize = 42

	gotA, err := buildPublicConfig(cfgA, testNetworkID, "", runningBridgeComponents{})
	require.NoError(t, err)
	gotB, err := buildPublicConfig(cfgB, testNetworkID, "", runningBridgeComponents{})
	require.NoError(t, err)

	// Same config -> same checksum, deterministically
	gotAAgain, err := buildPublicConfig(cfgA, testNetworkID, "", runningBridgeComponents{})
	require.NoError(t, err)
	require.Equal(t, gotA.ConfigSha1Sum, gotAAgain.ConfigSha1Sum)

	// Different config -> different checksum
	require.NotEqual(t, gotA.ConfigSha1Sum, gotB.ConfigSha1Sum)
}
