package main

import (
	"fmt"

	"github.com/agglayer/aggkit/bridgeservice"
	"github.com/agglayer/aggkit/bridgeservice/feeestimate"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/claimsync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// createBridgeService takes the concrete syncer pointers straight from run.go's wiring (any of
// which may be nil when that syncer isn't running on this instance) and converts each to its
// bridgeservice interface through the *OrNil helpers below before calling bridgeservice.New. A nil
// *T assigned directly to an interface-typed parameter would produce a non-nil interface wrapping
// a nil pointer (the interface's type is set, only its value is nil), which is indistinguishable
// from a configured syncer: bridgeservice.BridgeService's own `!= nil` checks would pass, and the
// first method call on it would panic. Converting here, where the concrete type is still known,
// keeps that check correct.
//
// l1Client and l2Client are the chain clients the fee estimators read through (see
// buildFeeEstimators); either may be nil, in which case that network simply gets no fee estimator
// and both fee estimate endpoints fail closed for it.
func createBridgeService(
	cfg *config.Config,
	l2GERSyncMode string,
	l2NetworkID uint32,
	runningComponents runningBridgeComponents,
	upgradeQuery bridgeservice.AgglayerManagerUpgradeQuerier,
	l1InfoTree *l1infotreesync.L1InfoTreeSync,
	injectedGERs *l2gersync.L2GERSync,
	bridgeL1 *bridgesync.BridgeSync,
	bridgeL2 *bridgesync.BridgeSync,
	claimL1 *claimsync.ClaimSync,
	claimL2 *claimsync.ClaimSync,
	l1Client aggkittypes.EthClienter,
	l2Client aggkittypes.EthClienter,
) *bridgeservice.BridgeService {
	logger := log.WithFields("module", aggkitcommon.BRIDGE)

	publicConfig, err := buildPublicConfig(cfg, l2NetworkID, l2GERSyncMode, runningComponents)
	if err != nil {
		log.Fatalf("failed to build bridge service public config: %v", err)
	}

	feeEstimators := buildFeeEstimators(cfg, l2NetworkID, l1Client, l2Client, claimL1, claimL2, bridgeL1, bridgeL2)

	bridgeCfg := &bridgeservice.Config{
		Logger:        logger,
		ReadTimeout:   cfg.PublicREST.ReadTimeout.Duration,
		WriteTimeout:  cfg.PublicREST.WriteTimeout.Duration,
		NetworkID:     l2NetworkID,
		PublicConfig:  publicConfig,
		FeeEstimators: feeEstimators,
	}

	return bridgeservice.New(
		bridgeCfg,
		upgradeQuery,
		l1InfoTreeSyncerOrNil(l1InfoTree),
		l2GERSyncerOrNil(injectedGERs),
		bridgerOrNil(bridgeL1),
		claimerOrNil(claimL1),
		bridgerOrNil(bridgeL2),
		claimerOrNil(claimL2),
	)
}

// buildFeeEstimators constructs the fee estimators backing GET /claim-fee-estimate and
// GET /bridge-fee-estimate, one per network this instance can serve (0 = L1 and/or its own L2),
// for bridgeservice.Config.FeeEstimators. An estimator is built only when it can actually quote
// something: its network's chain client is available (the L1 client is always dialed; the L2 one
// only when the BRIDGE component runs, see runL2ClientIfNeeded) and at least one of the network's
// claim/bridge syncers runs to sample from. Every other network is left out of the map, so both
// fee estimate endpoints answer 503 "fee estimation is not configured" for it — fail closed
// (an error, never a default value) — instead of serving an estimator that cannot work. The
// syncer pointers go through the *OrNil helpers because a nil *T stored in an interface would
// look configured to feeestimate.New while panicking on first use (see createBridgeService's doc
// comment). Estimator internals (window, percentile, TTLs, compute timeout) deliberately have no
// TOML section: the zero-value feeestimate.Config used here applies the package defaults.
//
// Both chain clients arrive as aggkittypes.EthClienter from runL1ClientIfNeeded /
// runL2ClientIfNeeded, which return untyped nils when not dialing, so the nil guard below cannot
// miss a typed nil.
func buildFeeEstimators(
	cfg *config.Config,
	l2NetworkID uint32,
	l1Client aggkittypes.EthClienter,
	l2Client aggkittypes.EthClienter,
	claimL1 *claimsync.ClaimSync,
	claimL2 *claimsync.ClaimSync,
	bridgeL1 *bridgesync.BridgeSync,
	bridgeL2 *bridgesync.BridgeSync,
) map[uint32]feeestimate.FeeEstimator {
	estimators := make(map[uint32]feeestimate.FeeEstimator)

	addEstimator := func(networkID uint32, bridgeAddr common.Address, chain aggkittypes.EthClienter,
		claims *claimsync.ClaimSync, bridges *bridgesync.BridgeSync) {
		if chain == nil || (claims == nil && bridges == nil) {
			return
		}
		estimator, err := feeestimate.New(feeestimate.Config{
			NetworkID:  networkID,
			BridgeAddr: bridgeAddr,
		}, chain, claimsReaderOrNil(claims), bridgesReaderOrNil(bridges))
		if err != nil {
			// Unreachable given the guard above (chain is non-nil) and the constant view ABI,
			// so a failure here means broken wiring: abort startup rather than serve 503s.
			log.Fatalf("failed to create fee estimator for network %d: %v", networkID, err)
		}
		estimators[networkID] = estimator
	}

	addEstimator(MainnetID, cfg.BridgeL1Sync.BridgeAddr, l1Client, claimL1, bridgeL1)
	// Network 0 is L1 by convention (MainnetID); skip an L2 rollup id of 0 so a broken rollup id
	// can't silently overwrite the L1 estimator under the same map key.
	if l2NetworkID != MainnetID {
		addEstimator(l2NetworkID, cfg.BridgeL2Sync.BridgeAddr, l2Client, claimL2, bridgeL2)
	}

	return estimators
}

// bridgerOrNil returns bridge as a bridgeservice.Bridger, or an untyped nil interface when bridge
// is a nil pointer (see createBridgeService's doc comment for why this matters).
func bridgerOrNil(bridge *bridgesync.BridgeSync) bridgeservice.Bridger {
	if bridge == nil {
		return nil
	}
	return bridge
}

// claimerOrNil returns claimer as a bridgeservice.Claimer, or an untyped nil interface when
// claimer is a nil pointer.
func claimerOrNil(claimer *claimsync.ClaimSync) bridgeservice.Claimer {
	if claimer == nil {
		return nil
	}
	return claimer
}

// claimsReaderOrNil returns claimer as a feeestimate.ClaimsReader, or an untyped nil interface
// when claimer is a nil pointer, so feeestimate.New's nil check sees a genuinely unconfigured
// claims reader (see createBridgeService's doc comment for why this matters).
func claimsReaderOrNil(claimer *claimsync.ClaimSync) feeestimate.ClaimsReader {
	if claimer == nil {
		return nil
	}
	return claimer
}

// bridgesReaderOrNil returns bridge as a feeestimate.BridgesReader, or an untyped nil interface
// when bridge is a nil pointer, so feeestimate.New's nil check sees a genuinely unconfigured
// bridges reader (see createBridgeService's doc comment for why this matters).
func bridgesReaderOrNil(bridge *bridgesync.BridgeSync) feeestimate.BridgesReader {
	if bridge == nil {
		return nil
	}
	return bridge
}

// l1InfoTreeSyncerOrNil returns syncer as a bridgeservice.L1InfoTreeSyncer, or an untyped nil
// interface when syncer is a nil pointer.
func l1InfoTreeSyncerOrNil(syncer *l1infotreesync.L1InfoTreeSync) bridgeservice.L1InfoTreeSyncer {
	if syncer == nil {
		return nil
	}
	return syncer
}

// l2GERSyncerOrNil returns syncer as a bridgeservice.L2GERSyncer, or an untyped nil interface when
// syncer is a nil pointer.
func l2GERSyncerOrNil(syncer *l2gersync.L2GERSync) bridgeservice.L2GERSyncer {
	if syncer == nil {
		return nil
	}
	return syncer
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
