package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxlog "github.com/0xPolygon/zkevm-ethtx-manager/log"
	"github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggoracle"
	"github.com/agglayer/aggkit/aggoracle/chaingersender"
	"github.com/agglayer/aggkit/aggsender"
	aggsendercfg "github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/flows"
	"github.com/agglayer/aggkit/aggsender/prover"
	"github.com/agglayer/aggkit/aggsender/query"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	"github.com/agglayer/aggkit/bridgeservice"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	ethermanquierier "github.com/agglayer/aggkit/etherman/querier"
	"github.com/agglayer/aggkit/healthcheck"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader"
	"github.com/agglayer/aggkit/pprof"
	"github.com/agglayer/aggkit/prometheus"
	"github.com/agglayer/aggkit/reorgdetector"
	aggkitsync "github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/urfave/cli/v2"
)

func start(cliCtx *cli.Context) error {
	// Validate components first before loading configuration
	components := cliCtx.StringSlice(config.FlagComponents)
	if err := aggkitcommon.ValidateComponents(components); err != nil {
		return err
	}

	cfg, err := config.Load(cliCtx)
	if err != nil {
		return err
	}

	if err := cfg.L1NetworkConfig.Validate(); err != nil {
		return fmt.Errorf("invalid L1 network config: %w", err)
	}

	if err := cfg.Common.L2RPC.Validate(); err != nil {
		return fmt.Errorf("invalid L2 RPC config: %w", err)
	}

	log.Init(cfg.Log)

	switch cfg.Log.Environment {
	case log.EnvironmentDevelopment:
		aggkit.PrintVersion(os.Stdout)
		log.Info("Starting application")
	case log.EnvironmentProduction:
		logVersion()
	}

	if cfg.Prometheus.Enabled {
		prometheus.Init()
	}
	l1Client := runL1ClientIfNeeded(cliCtx.Context, cfg.L1NetworkConfig.RPC)
	l2Client := runL2ClientIfNeeded(cliCtx.Context, components, cfg.Common.L2RPC)
	reorgDetectorL1, errChanL1 := runReorgDetectorL1IfNeeded(cliCtx.Context, components, l1Client, &cfg.ReorgDetectorL1)
	go func() {
		if err := <-errChanL1; err != nil {
			log.Fatal("Error from ReorgDetectorL1: ", err)
		}
	}()

	reorgDetectorL2, errChanL2 := runReorgDetectorL2IfNeeded(cliCtx.Context, components, l2Client, &cfg.ReorgDetectorL2)
	go func() {
		if err := <-errChanL2; err != nil {
			log.Fatal("Error from ReorgDetectorL2: ", err)
		}
	}()
	var rpcServices []jRPC.Service
	l1MultiDownloader, l1mdServices, err := runL1MultiDownloaderIfNeeded(l1Client, cfg.L1Multidownloader)
	if err != nil {
		return fmt.Errorf("failed to create L1MultiDownloader: %w", err)
	}
	if l1mdServices != nil {
		rpcServices = append(rpcServices, l1mdServices...)
	}

	rollupDataQuerier, err := createRollupDataQuerier(cliCtx.Context, cfg.L1NetworkConfig, l1Client)
	if err != nil {
		return fmt.Errorf("failed to create rollup data querier: %w", err)
	}

	// Create a cancellable context for graceful shutdown
	ctx, cancel := context.WithCancel(cliCtx.Context)
	defer cancel()

	// Create WaitGroup for backfill goroutines synchronization
	var backfillWg sync.WaitGroup

	l1InfoTreeSync := runL1InfoTreeSyncerIfNeeded(ctx, components, *cfg, reorgDetectorL1,
		l1Client, l1MultiDownloader)
	if l1InfoTreeSync != nil {
		rpcServices = append(rpcServices, l1InfoTreeSync.GetRPCServices()...)
	}
	l1BridgeSync := runBridgeSyncL1IfNeeded(ctx, components, cfg.BridgeL1Sync, reorgDetectorL1,
		l1Client, 0, &backfillWg)
	l2BridgeSync := runBridgeSyncL2IfNeeded(ctx, components, cfg.BridgeL2Sync, reorgDetectorL2,
		l2Client, rollupDataQuerier.RollupID, &backfillWg)
	l2GERSync := runL2GERSyncIfNeeded(
		ctx, components, cfg.L2GERSync, reorgDetectorL2, l2Client, l1InfoTreeSync, l1Client,
	)

	committeeQuerier := runAggsenderMultisigCommitteeIfNeeded(components, cfg.L1NetworkConfig.RollupAddr, l1Client,
		&cfg.AggSender.CommitteeOverride)

	// Check if any bridge-related component is present and start bridge service once
	hasBridgeComponent := false
	for _, component := range components {
		if component == aggkitcommon.BRIDGE ||
			component == aggkitcommon.L1BRIDGESYNC ||
			component == aggkitcommon.L2BRIDGESYNC {
			hasBridgeComponent = true
			break
		}
	}

	if hasBridgeComponent && (l1BridgeSync != nil || l2BridgeSync != nil) {
		b := createBridgeService(
			cfg.REST,
			rollupDataQuerier.RollupID,
			rollupDataQuerier,
			l1InfoTreeSync,
			l2GERSync,
			l1BridgeSync,
			l2BridgeSync,
		)
		go b.Start(ctx)
		log.Info("Bridge service started")
	}
	if l1MultiDownloader != nil {
		log.Info("starting L1 MultiDownloader...")
		err = l1MultiDownloader.Initialize(ctx)
		if err != nil {
			//nolint:gocritic
			log.Fatal("failed to initialize L1 MultiDownloader: ", err)
		}
		go func() {
			err := l1MultiDownloader.Start(ctx)
			if err != nil {
				log.Fatal("l1MultiDownloader stopped: %w", err)
			}
		}()
	}
	if l1InfoTreeSync != nil {
		log.Info("starting L1 Info Tree Syncer...")
		go l1InfoTreeSync.Start(ctx)
	}

	for _, component := range components {
		switch component {
		case aggkitcommon.AGGORACLE:
			aggOracle := createAggoracle(rollupDataQuerier, *cfg, l1Client, l2Client, l1InfoTreeSync)
			go aggOracle.Start(ctx)
		case aggkitcommon.AGGSENDER:
			aggsender, err := createAggSender(
				ctx,
				cfg.AggSender,
				l1Client,
				l1InfoTreeSync,
				l2BridgeSync,
				l2Client,
				rollupDataQuerier,
				committeeQuerier,
			)
			if err != nil {
				log.Fatal(err)
			}
			rpcServices = append(rpcServices, aggsender.GetRPCServices()...)

			go aggsender.Start(ctx)
		case aggkitcommon.AGGCHAINPROOFGEN:
			aggchainProofGen, err := createAggchainProofGen(
				ctx,
				cfg.AggchainProofGen,
				l1Client,
				l2Client,
				l1InfoTreeSync,
				l2BridgeSync,
			)
			if err != nil {
				log.Fatal(err)
			}

			rpcServices = append(rpcServices, aggchainProofGen.GetRPCServices()...)
		case aggkitcommon.AGGSENDERVALIDATOR:
			aggsenderValidator, err := createAggSenderValidator(
				ctx,
				cfg.Validator,
				l1InfoTreeSync,
				l2BridgeSync,
				l1Client,
				l2Client,
				rollupDataQuerier,
				committeeQuerier,
			)
			if err != nil {
				log.Fatal(err)
			}
			go aggsenderValidator.Start(ctx)
		}
	}
	if len(rpcServices) > 0 {
		rpcServer := createRPC(cfg.RPC, rpcServices)
		go func() {
			if err := rpcServer.Start(); err != nil {
				log.Fatal(err)
			}
		}()
	}

	if cfg.Prometheus.Enabled {
		go startPrometheusHTTPServer(cfg.Prometheus)
	} else {
		log.Info("Prometheus metrics server is disabled")
	}

	if cfg.Profiling.ProfilingEnabled {
		go pprof.StartProfilingHTTPServer(ctx, cfg.Profiling)
	}

	waitSignal([]context.CancelFunc{cancel}, &backfillWg)

	return nil
}

func createAggchainProofGen(
	ctx context.Context,
	cfg prover.Config,
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter,
	l1InfoTreeSync *l1infotreesync.L1InfoTreeSync,
	l2Syncer *bridgesync.BridgeSync,
) (*prover.AggchainProofGenerationTool, error) {
	logger := log.WithFields("module", aggkitcommon.AGGCHAINPROOFGEN)

	aggchainProofGen, err := prover.NewAggchainProofGenerationTool(
		ctx,
		logger,
		cfg,
		l1Client,
		l2Client,
		l2Syncer,
		l1InfoTreeSync,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create AggchainProofGenerationTool: %w", err)
	}

	return aggchainProofGen, nil
}

func createAggSenderValidator(ctx context.Context,
	cfg validator.Config,
	l1InfoTreeSync *l1infotreesync.L1InfoTreeSync,
	l2Syncer *bridgesync.BridgeSync,
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter,
	rollupDataQuerier *ethermanquierier.RollupDataQuerier,
	committeeQuerier aggsendertypes.MultisigQuerier,
) (*aggsender.AggsenderValidator, error) {
	mode, err := committeeQuerier.ResolveAutoMode(cfg.Mode)
	if err != nil {
		return nil, err
	}
	// Override configuration with the resolved mode
	if cfg.Mode != mode {
		log.Infof("aggsenderValidator mode from %s to %s", cfg.Mode, mode)
		cfg.Mode = mode
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid aggsender validator config: %w", err)
	}

	logger := log.WithFields("module", aggkitcommon.AGGSENDERVALIDATOR)
	agglayerClient, err := agglayer.NewAgglayerClient(cfg.AgglayerClient, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create agglayer grpc client: %w", err)
	}

	aggchainFEPQuerier, err := query.NewAggchainFEPQuerier(
		logger,
		cfg.Mode,
		cfg.FEPConfig.SovereignRollupAddr,
		l1Client,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create AggchainFEPQuerier: %w", err)
	}

	certQuerier := query.NewCertificateQuerier(
		l2Syncer,
		aggchainFEPQuerier,
		agglayerClient,
	)

	flow, flowParams, err := flows.NewVerifierFlow(
		ctx,
		cfg,
		logger,
		l1Client,
		l2Client,
		l1InfoTreeSync,
		l2Syncer,
		rollupDataQuerier,
		committeeQuerier,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create verifier flow: %w", err)
	}

	return aggsender.NewAggsenderValidator(
		ctx, logger, cfg, flow,
		flowParams.L1InfoTreeDataQuerier,
		agglayerClient,
		certQuerier,
		aggchainFEPQuerier,
		flowParams.LERQuerier,
		flowParams.Signer,
	)
}

func createAggSender(
	ctx context.Context,
	cfg aggsendercfg.Config,
	l1EthClient aggkittypes.BaseEthereumClienter,
	l1InfoTreeSync aggsendertypes.L1InfoTreeSyncer,
	l2Syncer aggsendertypes.L2BridgeSyncer,
	l2Client aggkittypes.BaseEthereumClienter,
	rollupDataQuerier aggsendertypes.RollupDataQuerier,
	committeeQuerier aggsendertypes.MultisigQuerier) (*aggsender.AggSender, error) {
	logger := log.WithFields("module", aggkitcommon.AGGSENDER)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid aggsender config: %w", err)
	}

	agglayerClient, err := agglayer.NewAgglayerClient(cfg.AgglayerClient, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create agglayer grpc client: %w", err)
	}

	aggsender, err := aggsender.New(ctx, logger, cfg, agglayerClient,
		l1InfoTreeSync, l2Syncer, l1EthClient, l2Client, rollupDataQuerier, committeeQuerier)
	if err != nil {
		return nil, fmt.Errorf("failed to create AggSender: %w", err)
	}

	return aggsender, nil
}

func createAggoracle(
	rollupDataQuerier *ethermanquierier.RollupDataQuerier,
	cfg config.Config,
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter,
	l1InfoTreeSyncer aggoracle.L1InfoTreeSyncer,
) *aggoracle.AggOracle {
	logger := log.WithFields("module", aggkitcommon.AGGORACLE)
	l2ChainID, err := rollupDataQuerier.GetRollupChainID()
	if err != nil {
		logger.Errorf("Failed to retrieve L2ChainID: %v", err)
	}

	// sanity check for the aggOracle ChainID
	if cfg.AggOracle.EVMSender.EthTxManager.Etherman.L1ChainID != l2ChainID {
		logger.Warnf("Incorrect ChainID in aggOracle provided: %d expected: %d",
			cfg.AggOracle.EVMSender.EthTxManager.Etherman.L1ChainID,
			l2ChainID,
		)
	}

	var sender aggoracle.ChainSender
	switch cfg.AggOracle.TargetChainType {
	case aggoracle.EVMChain:
		cfg.AggOracle.EVMSender.EthTxManager.Log = ethtxlog.Config{
			Environment: ethtxlog.LogEnvironment(cfg.Log.Environment),
			Level:       cfg.Log.Level,
			Outputs:     cfg.Log.Outputs,
		}
		ethTxManager, err := ethtxmanager.New(cfg.AggOracle.EVMSender.EthTxManager)
		if err != nil {
			log.Fatal(err)
		}

		l2GERManagerAddr := cfg.AggOracle.EVMSender.GlobalExitRootL2Addr
		logger.Infof("AggOracle sender address: %s | GER contract address on L2: %s",
			ethTxManager.From().Hex(),
			l2GERManagerAddr.Hex(),
		)
		go ethTxManager.Start()

		l2GERManager, err := agglayergerl2.NewAgglayergerl2(
			l2GERManagerAddr, l2Client)
		if err != nil {
			log.Fatalf("failed to create binding for GER L2 manager (SC address: %s): %w", l2GERManagerAddr, err)
		}

		sender, err = chaingersender.NewEVMChainGERSender(
			logger,
			cfg.AggOracle.EVMSender,
			l2Client,
			l2GERManager,
			ethTxManager,
			cfg.AggOracle.EnableAggOracleCommittee,
		)
		if err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatalf(
			"Unsupported chaintype %s. Supported values: %v",
			cfg.AggOracle.TargetChainType, aggoracle.SupportedChainTypes,
		)
	}
	aggOracle, err := aggoracle.New(
		logger,
		sender,
		l1Client,
		l1InfoTreeSyncer,
		cfg.AggOracle.WaitPeriodNextGER.Duration,
	)
	if err != nil {
		logger.Fatal(err)
	}

	return aggOracle
}

func logVersion() {
	log.Infow("Starting application",
		// version is already logged by default
		"gitRevision", aggkit.GitRev,
		"gitBranch", aggkit.GitBranch,
		"goVersion", runtime.Version(),
		"built", aggkit.BuildDate,
		"os/arch", fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	)
}

func waitSignal(cancelFuncs []context.CancelFunc, wg *sync.WaitGroup) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)

	for sig := range signals {
		switch sig {
		case os.Interrupt, os.Kill:
			log.Info("terminating application gracefully...")

			exitStatus := 0
			for _, cancel := range cancelFuncs {
				cancel()
			}

			// Wait for all backfill goroutines to complete
			if wg != nil {
				log.Info("waiting for backfill processes to complete...")
				wg.Wait()
				log.Info("all backfill processes completed")
			}

			os.Exit(exitStatus)
		}
	}
}

func newReorgDetector(
	cfg *reorgdetector.Config,
	client aggkittypes.BaseEthereumClienter,
	network reorgdetector.Network,
) *reorgdetector.ReorgDetector {
	rd, err := reorgdetector.New(client, *cfg, network)
	if err != nil {
		log.Fatal(err)
	}

	return rd
}

func isNeeded(casesWhereNeeded, actualCases []string) bool {
	for _, actualCase := range actualCases {
		if slices.Contains(casesWhereNeeded, actualCase) {
			return true
		}
	}

	return false
}

func runL1InfoTreeSyncerIfNeeded(
	ctx context.Context,
	components []string,
	cfg config.Config,
	reorgDetectorL1 aggkitsync.ReorgDetector,
	l1EthClient aggkittypes.BaseEthereumClienter,
	l1MultiDownloader *multidownloader.EVMMultidownloader,
) *l1infotreesync.L1InfoTreeSync {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE, aggkitcommon.AGGSENDER, aggkitcommon.AGGSENDERVALIDATOR,
		aggkitcommon.BRIDGE, aggkitcommon.L1INFOTREESYNC,
		aggkitcommon.L2GERSYNC, aggkitcommon.AGGCHAINPROOFGEN}, components) {
		return nil
	}
	var l1InfoTreeSync *l1infotreesync.L1InfoTreeSync
	var err error
	if l1MultiDownloader != nil {
		log.Info("L1 Info Tree Syncer using MultiDownloader based implementation")
		l1InfoTreeSync, err = l1infotreesync.NewMultidownloadBased(
			ctx,
			cfg.L1InfoTreeSync,
			l1MultiDownloader,
			l1infotreesync.FlagNone,
		)
	} else {
		log.Info("L1 Info Tree Syncer using legacy sync implementation")
		l1Client := aggkitsync.NewAdapterEthClientToMultidownloader(l1EthClient)
		l1InfoTreeSync, err = l1infotreesync.NewLegacy(
			ctx,
			cfg.L1InfoTreeSync,
			l1Client,
			reorgDetectorL1,
			l1infotreesync.FlagNone,
		)
	}
	if err != nil {
		log.Fatal(err)
	}
	return l1InfoTreeSync
}

func runL1ClientIfNeeded(ctx context.Context,
	rpcClientCfg ethermanconfig.RPCClientConfig) aggkittypes.EthClienter {
	// Always is required because is used to create a L1InfoTreeDataQuerier
	log.Debugf("dialing L1 client at: %s", rpcClientCfg.URL)

	if rpcClientCfg.Mode != ethermanconfig.RPCModeBasic {
		log.Fatalf("only basic RPC mode is supported for L1 client, got: %s", rpcClientCfg.Mode)
	}
	logger := log.WithFields("module", "l1client")
	ethClient, err := etherman.NewRPCClient(ctx, logger, rpcClientCfg)
	if err != nil {
		log.Fatalf("failed to create client for L1 using URL: %s. Err:%v", rpcClientCfg.URL, err)
	}

	return ethClient
}

func runL2ClientIfNeeded(ctx context.Context,
	components []string, urlRPCL2 ethermanconfig.RPCClientConfig) aggkittypes.EthClienter {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE,
		aggkitcommon.BRIDGE,
		aggkitcommon.AGGSENDER,
		aggkitcommon.AGGSENDERVALIDATOR,
		aggkitcommon.AGGCHAINPROOFGEN,
		aggkitcommon.L2BRIDGESYNC,
		aggkitcommon.L2GERSYNC}, components) {
		return nil
	}
	logger := log.WithFields("module", "l2client")
	l2Client, err := etherman.NewRPCClient(ctx, logger, urlRPCL2)
	if err != nil {
		log.Fatalf("failed to create client for L2 using URL: %s. Err:%v", urlRPCL2, err)
	}

	return l2Client
}

func runReorgDetectorL1IfNeeded(
	ctx context.Context,
	components []string,
	l1Client aggkittypes.BaseEthereumClienter,
	cfg *reorgdetector.Config,
) (*reorgdetector.ReorgDetector, chan error) {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE, aggkitcommon.AGGSENDER, aggkitcommon.AGGSENDERVALIDATOR,
		aggkitcommon.BRIDGE, aggkitcommon.L1BRIDGESYNC, aggkitcommon.L1INFOTREESYNC,
		aggkitcommon.L2GERSYNC, aggkitcommon.AGGCHAINPROOFGEN},
		components) {
		return nil, nil
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid ReorgDetectorL1 config: %v", err)
	}

	rd := newReorgDetector(cfg, l1Client, reorgdetector.L1)
	errChan := make(chan error)
	go func() {
		if err := rd.Start(ctx); err != nil {
			errChan <- err
		}
		close(errChan)
	}()

	return rd, errChan
}

func runL1MultiDownloaderIfNeeded(
	l1Client aggkittypes.EthClienter,
	cfg multidownloader.Config,
) (*multidownloader.EVMMultidownloader, []jRPC.Service, error) {
	// The requirements are the same as L1Client
	if l1Client == nil {
		return nil, nil, nil
	}
	// If it's disable It creates a direct eth client
	if !cfg.Enabled {
		log.Warnf("L1 MultiDownloader is disabled, don't creating the service.")
		return nil, nil, nil
	}
	logger := log.WithFields("module", "L1MultiDownloader")

	downloader, err := multidownloader.NewEVMMultidownloader(
		logger,
		cfg,
		"l1",
		l1Client, // ethClient
		l1Client, // rpcClient
		nil,      // storage (created inside the multidownloader if nil)
		nil,      // blockNotifierManager (created inside the multidownloader if nil)
		nil,      // reorgProcessor (created inside the multidownloader if nil)
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create L1 MultiDownloader: %w", err)
	}
	rpcServices := downloader.GetRPCServices()
	return downloader, rpcServices, nil
}

func runReorgDetectorL2IfNeeded(
	ctx context.Context,
	components []string,
	l2Client aggkittypes.BaseEthereumClienter,
	cfg *reorgdetector.Config,
) (*reorgdetector.ReorgDetector, chan error) {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE,
		aggkitcommon.BRIDGE,
		aggkitcommon.AGGSENDER,
		aggkitcommon.AGGSENDERVALIDATOR,
		aggkitcommon.AGGCHAINPROOFGEN,
		aggkitcommon.L2BRIDGESYNC,
		aggkitcommon.L2GERSYNC}, components) {
		return nil, nil
	}
	rd := newReorgDetector(cfg, l2Client, reorgdetector.L2)

	errChan := make(chan error)
	go func() {
		if err := rd.Start(ctx); err != nil {
			errChan <- err
		}
		close(errChan)
	}()

	return rd, errChan
}

func runL2GERSyncIfNeeded(
	ctx context.Context,
	components []string,
	cfg l2gersync.Config,
	reorgDetectorL2 *reorgdetector.ReorgDetector,
	l2Client aggkittypes.BaseEthereumClienter,
	l1InfoTreeSync *l1infotreesync.L1InfoTreeSync,
	l1Client aggkittypes.BaseEthereumClienter,
) *l2gersync.L2GERSync {
	if !isNeeded([]string{aggkitcommon.BRIDGE, aggkitcommon.L2GERSYNC}, components) {
		return nil
	}
	l2GERSync, err := l2gersync.New(
		ctx,
		cfg,
		reorgDetectorL2,
		l2Client,
		l1InfoTreeSync,
		l1Client,
	)
	if err != nil {
		log.Fatalf("error creating l2GERSync: %s", err)
	}

	go l2GERSync.Start(ctx)

	return l2GERSync
}

func runBridgeSyncL1IfNeeded(
	ctx context.Context,
	components []string,
	cfg bridgesync.Config,
	reorgDetectorL1 bridgesync.ReorgDetector,
	l1Client aggkittypes.EthClienter,
	rollupID uint32,
	wg *sync.WaitGroup,
) *bridgesync.BridgeSync {
	if !isNeeded([]string{aggkitcommon.BRIDGE, aggkitcommon.L1BRIDGESYNC}, components) {
		return nil
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid BridgeL1Sync config: %v", err)
	}

	bridgeSyncL1, err := bridgesync.NewL1(
		ctx,
		cfg,
		reorgDetectorL1,
		l1Client,
		rollupID,
	)
	if err != nil {
		log.Fatalf("error creating bridgeSyncL1: %s", err)
	}

	// Run txn_sender backfilling in a separate goroutine
	wg.Add(1)
	go func() {
		if err := runTxnSenderBackfill(ctx, cfg, l1Client, wg); err != nil {
			log.Errorf("txn_sender backfilling failed: %v", err)
			// Don't fail the entire process, just log the error and continue
		}
		log.Infof("txn_sender backfilling completed for L1 bridge sync")
	}()

	go bridgeSyncL1.Start(ctx)

	return bridgeSyncL1
}

func runBridgeSyncL2IfNeeded(
	ctx context.Context,
	components []string,
	cfg bridgesync.Config,
	reorgDetectorL2 *reorgdetector.ReorgDetector,
	l2Client aggkittypes.EthClienter,
	rollupID uint32,
	wg *sync.WaitGroup,
) *bridgesync.BridgeSync {
	fullClaimsNeeded := isNeeded([]string{
		aggkitcommon.BRIDGE,
		aggkitcommon.AGGSENDER,
		aggkitcommon.AGGCHAINPROOFGEN,
		aggkitcommon.L2BRIDGESYNC,
		aggkitcommon.AGGSENDERVALIDATOR}, components)

	fullClaimsNotNeeded := isNeeded([]string{}, components)

	if !fullClaimsNeeded && !fullClaimsNotNeeded {
		// no bridge sync needed
		return nil
	}

	bridgeSyncL2, err := bridgesync.NewL2(
		ctx,
		cfg,
		reorgDetectorL2,
		l2Client,
		rollupID,
		fullClaimsNeeded,
	)
	if err != nil {
		log.Fatalf("error creating bridgeSyncL2: %s", err)
	}

	// Run txn_sender backfilling in a separate goroutine
	wg.Add(1)
	go func() {
		if err := runTxnSenderBackfill(ctx, cfg, l2Client, wg); err != nil {
			log.Errorf("txn_sender backfilling failed: %v", err)
			// Don't fail the entire process, just log the error and continue
		}
	}()

	go bridgeSyncL2.Start(ctx)

	return bridgeSyncL2
}

func runAggsenderMultisigCommitteeIfNeeded(
	components []string,
	rollupAddr common.Address,
	l1Client aggkittypes.BaseEthereumClienter,
	cfg *query.CommitteeOverride,
) aggsendertypes.MultisigQuerier {
	if !isNeeded([]string{aggkitcommon.AGGSENDER, aggkitcommon.AGGSENDERVALIDATOR}, components) {
		return nil
	}

	committeeQuerier, err := query.NewBaseMultisigCommitteeQuery(rollupAddr, l1Client, cfg)
	if err != nil {
		log.Fatalf("failed to create ECDSA multisig committee querier (SC address: %s): %s", rollupAddr, err)
	}

	return committeeQuerier
}

func createBridgeService(
	cfg aggkitcommon.RESTConfig,
	l2NetworkID uint32,
	upgradeQuery bridgeservice.AgglayerManagerUpgradeQuerier,
	l1InfoTree bridgeservice.L1InfoTreeSyncer,
	injectedGERs bridgeservice.L2GERSyncer,
	bridgeL1 bridgeservice.Bridger,
	bridgeL2 bridgeservice.Bridger,
) *bridgeservice.BridgeService {
	logger := log.WithFields("module", aggkitcommon.BRIDGE)

	bridgeCfg := &bridgeservice.Config{
		Logger:       logger,
		Address:      cfg.Address(),
		ReadTimeout:  cfg.ReadTimeout.Duration,
		WriteTimeout: cfg.WriteTimeout.Duration,
		NetworkID:    l2NetworkID,
	}

	return bridgeservice.New(
		bridgeCfg,
		upgradeQuery,
		l1InfoTree,
		injectedGERs,
		bridgeL1,
		bridgeL2,
	)
}

func createRPC(cfg jRPC.Config, services []jRPC.Service) *jRPC.Server {
	logger := log.WithFields("module", "RPC")

	healthHandler := healthcheck.NewHealthCheckHandler(logger)
	logger.Infof("Starting RPC server at %s:%d", cfg.Host, cfg.Port)
	return jRPC.NewServer(cfg, services,
		jRPC.WithLogger(logger.GetSugaredLogger()),
		jRPC.WithHealthHandler(healthHandler))
}

func startPrometheusHTTPServer(c prometheus.Config) {
	const ten = 10
	mux := http.NewServeMux()
	address := fmt.Sprintf("%s:%d", c.Host, c.Port)
	lis, err := net.Listen("tcp", address)
	if err != nil {
		log.Errorf("failed to create tcp listener for metrics: %v", err)
		return
	}
	mux.Handle(prometheus.Endpoint, promhttp.Handler())

	metricsServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: ten * time.Second,
		ReadTimeout:       ten * time.Second,
	}
	log.Infof("prometheus server listening on port %d", c.Port)
	if err := metricsServer.Serve(lis); err != nil {
		if err == http.ErrServerClosed {
			log.Warnf("prometheus http server stopped")
			return
		}
		log.Errorf("closed http connection for prometheus server: %v", err)
		return
	}
}

// createRollupDataQuerier initializes and returns the rollup data querier if any of the required components
// (AGGORACLE, AGGCHAINPROOFGEN, AGGSENDER, BRIDGE) are needed. The client is configured with
// the provided L1 network configuration and uses default implementations for creating Ethereum
// clients and rollup manager contracts. Returns (nil, nil) if none of the required components are needed.
func createRollupDataQuerier(ctx context.Context,
	cfg ethermanconfig.L1NetworkConfig,
	l1Client aggkittypes.BaseEthereumClienter,
) (*ethermanquierier.RollupDataQuerier, error) {
	return ethermanquierier.NewRollupDataQuerier(ctx, cfg, l1Client,
		func(rollupManagerAddr common.Address,
			client aggkittypes.BaseEthereumClienter) (ethermanquierier.RollupManagerContract, error) {
			return agglayermanager.NewAgglayermanager(rollupManagerAddr, client)
		})
}

// runTxnSenderBackfill runs the txn_sender backfilling process
func runTxnSenderBackfill(
	ctx context.Context,
	cfg bridgesync.Config,
	client aggkittypes.EthClienter,
	wg *sync.WaitGroup,
) error {
	// Only run backfilling if we have a database path configured
	if cfg.DBPath == "" {
		log.Debug("No database path configured, skipping txn_sender backfilling")
		return nil
	}

	// Defer WaitGroup Done to ensure cleanup on exit
	defer wg.Done()

	log.Info("Starting txn_sender backfilling process")

	// Create backfill instance
	backfiller, err := bridgesync.NewBackfillTxnSender(
		cfg.DBPath,
		client,
		cfg.BridgeAddr,
		log.WithFields("module", "tx-sender-backfill"),
	)
	if err != nil {
		return fmt.Errorf("failed to create backfill instance: %w", err)
	}
	defer backfiller.Close()

	// Run backfilling with the original context to respect parent cancellation
	start := time.Now()
	if err := backfiller.BackfillAll(ctx); err != nil {
		// Check if the error is due to context cancellation
		if ctx.Err() != nil {
			log.Infof("txn_sender backfilling cancelled: %v", ctx.Err())
			return nil // Don't treat cancellation as an error
		}
		log.Errorf("txn_sender backfilling failed: %v", err)
		// Don't fail the entire process, just log the error and continue
		return err
	}

	duration := time.Since(start)
	log.Infof("txn_sender backfilling completed in %v", duration)

	return nil
}
