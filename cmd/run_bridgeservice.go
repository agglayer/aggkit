package main

import (
	"fmt"

	"github.com/agglayer/aggkit/bridgeservice"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config"
	"github.com/agglayer/aggkit/log"
)

func createBridgeService(
	cfg *config.Config,
	l2GERSyncMode string,
	l2NetworkID uint32,
	runningComponents runningBridgeComponents,
	upgradeQuery bridgeservice.AgglayerManagerUpgradeQuerier,
	l1InfoTree bridgeservice.L1InfoTreeSyncer,
	injectedGERs bridgeservice.L2GERSyncer,
	bridgeL1 bridgeservice.Bridger,
	bridgeL2 bridgeservice.Bridger,
	claimL1 bridgeservice.Claimer,
	claimL2 bridgeservice.Claimer,
) *bridgeservice.BridgeService {
	logger := log.WithFields("module", aggkitcommon.BRIDGE)

	publicConfig, err := buildPublicConfig(cfg, l2NetworkID, l2GERSyncMode, runningComponents)
	if err != nil {
		log.Fatalf("failed to build bridge service public config: %v", err)
	}

	bridgeCfg := &bridgeservice.Config{
		Logger:       logger,
		ReadTimeout:  cfg.PublicREST.ReadTimeout.Duration,
		WriteTimeout: cfg.PublicREST.WriteTimeout.Duration,
		NetworkID:    l2NetworkID,
		PublicConfig: publicConfig,
	}

	return bridgeservice.New(
		bridgeCfg,
		upgradeQuery,
		l1InfoTree,
		injectedGERs,
		bridgeL1,
		claimL1,
		bridgeL2,
		claimL2,
	)
}

// runningBridgeComponents flags which syncer components are actually running on this bridge
// service instance, so buildPublicConfig only reports the configuration of components that are
// really up instead of advertising all of them unconditionally.
type runningBridgeComponents struct {
	L1InfoTreeSync bool
	BridgeL1Sync   bool
	BridgeL2Sync   bool
	L2GERSync      bool
}

// buildPublicConfig builds the sanitized view of the configuration served on the bridge
// service's public config endpoint (GET /bridge/v1/config): only the parameters a client needs
// to configure itself against this instance, with contract addresses deduplicated by network
// instead of repeated across every component config that uses them. It never includes RPC URLs,
// DB paths, private keys or any other internal/sensitive configuration value.
//
// l2NetworkID is the rollup/network ID this bridge service instance's bridge/claim syncers are
// listening on.
//
// l2GERSyncMode is the l2gersync.SyncMode ("Legacy"/"SovereignChain") resolved for this instance
// at startup, or empty when the L2GERSync component isn't running on it. It's not configuration
// (it's auto-detected by probing the L2 GER contract) but useful operational information, so it's
// reported alongside the L2GERSync component's config.
//
// runningComponents flags which syncer components are actually running on this instance: only
// those are populated in the response, so a client can't be misled into configuring itself
// against a component that isn't backing this instance.
func buildPublicConfig(
	cfg *config.Config, l2NetworkID uint32, l2GERSyncMode string, runningComponents runningBridgeComponents,
) (bridgetypes.PublicConfigResponse, error) {
	internalConfigChecksum, err := cfg.Checksum()
	if err != nil {
		return bridgetypes.PublicConfigResponse{}, fmt.Errorf("failed to compute configuration checksum: %w", err)
	}

	var components bridgetypes.PublicComponentsConfig
	if runningComponents.L1InfoTreeSync {
		components.L1InfoTreeSync = &bridgetypes.SyncComponentConfig{
			BlockFinality:      cfg.L1InfoTreeSync.BlockFinality.String(),
			InitialBlock:       cfg.L1InfoTreeSync.InitialBlock,
			SyncBlockChunkSize: cfg.L1InfoTreeSync.SyncBlockChunkSize,
		}
	}
	if runningComponents.BridgeL1Sync {
		components.BridgeL1Sync = &bridgetypes.SyncComponentConfig{
			BlockFinality:      cfg.BridgeL1Sync.BlockFinality.String(),
			InitialBlock:       cfg.BridgeL1Sync.InitialBlockNum,
			SyncBlockChunkSize: cfg.BridgeL1Sync.SyncBlockChunkSize,
		}
	}
	if runningComponents.BridgeL2Sync {
		components.BridgeL2Sync = &bridgetypes.SyncComponentConfig{
			BlockFinality:      cfg.BridgeL2Sync.BlockFinality.String(),
			InitialBlock:       cfg.BridgeL2Sync.InitialBlockNum,
			SyncBlockChunkSize: cfg.BridgeL2Sync.SyncBlockChunkSize,
		}
	}
	if runningComponents.L2GERSync {
		components.L2GERSync = &bridgetypes.L2GERSyncComponentConfig{
			SyncComponentConfig: bridgetypes.SyncComponentConfig{
				BlockFinality:      cfg.L2GERSync.BlockFinality.String(),
				InitialBlock:       cfg.L2GERSync.InitialBlockNum,
				SyncBlockChunkSize: cfg.L2GERSync.SyncBlockChunkSize,
			},
			SyncMode: l2GERSyncMode,
		}
	}

	publicConfig := bridgetypes.PublicConfigResponse{
		NetworkID:  l2NetworkID,
		Components: components,
		Contracts: bridgetypes.PublicContractsConfig{
			L1: bridgetypes.L1ContractsConfig{
				GlobalExitRootAddr: bridgetypes.Address(cfg.L1NetworkConfig.GlobalExitRootManagerAddr.Hex()),
				RollupManagerAddr:  bridgetypes.Address(cfg.L1NetworkConfig.RollupManagerAddr.Hex()),
				BridgeAddr:         bridgetypes.Address(cfg.BridgeL1Sync.BridgeAddr.Hex()),
			},
			L2: bridgetypes.L2ContractsConfig{
				GlobalExitRootAddr: bridgetypes.Address(cfg.L2GERSync.GlobalExitRootL2Addr.Hex()),
				BridgeAddr:         bridgetypes.Address(cfg.BridgeL2Sync.BridgeAddr.Hex()),
			},
		},
	}

	publicConfigChecksum, err := publicConfig.PublicChecksum()
	if err != nil {
		return bridgetypes.PublicConfigResponse{}, fmt.Errorf("failed to compute public configuration checksum: %w", err)
	}

	publicConfig.InternalConfigChecksum = internalConfigChecksum
	publicConfig.PublicConfigChecksum = publicConfigChecksum

	return publicConfig, nil
}
