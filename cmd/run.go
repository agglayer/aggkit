package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonrollupmanager"
	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxlog "github.com/0xPolygon/zkevm-ethtx-manager/log"
	"github.com/agglayer/aggkit"
	agglayer "github.com/agglayer/aggkit/agglayer/grpc"
	"github.com/agglayer/aggkit/aggoracle"
	"github.com/agglayer/aggkit/aggoracle/chaingersender"
	"github.com/agglayer/aggkit/aggsender"
	aggsendercfg "github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/prover"
	"github.com/agglayer/aggkit/bridgeservice"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/healthcheck"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/lastgersync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/pprof"
	"github.com/agglayer/aggkit/prometheus"
	"github.com/agglayer/aggkit/reorgdetector"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/urfave/cli/v2"
)

func start(cliCtx *cli.Context) error {
	cfg, err := config.Load(cliCtx)
	if err != nil {
		return err
	}

	log.Init(cfg.Log)

	if cfg.Log.Environment == log.EnvironmentDevelopment {
		aggkit.PrintVersion(os.Stdout)
		log.Info("Starting application")
	} else if cfg.Log.Environment == log.EnvironmentProduction {
		logVersion()
	}

	if cfg.Prometheus.Enabled {
		prometheus.Init()
	}
	components := cliCtx.StringSlice(config.FlagComponents)
	l1Client := runL1ClientIfNeeded(components, cfg.L1NetworkConfig.URL)
	l2Client := runL2ClientIfNeeded(components, cfg.Common.L2RPC)
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

	rollupDataQuerier, err := createRollupDataQuerier(cfg.L1NetworkConfig, components)
	if err != nil {
		return fmt.Errorf("failed to create etherman client: %w", err)
	}

	l1InfoTreeSync := runL1InfoTreeSyncerIfNeeded(cliCtx.Context, components, *cfg, l1Client, reorgDetectorL1)
	l1BridgeSync := runBridgeSyncL1IfNeeded(cliCtx.Context, components, cfg.BridgeL1Sync, reorgDetectorL1,
		l1Client, 0)
	l2BridgeSync := runBridgeSyncL2IfNeeded(cliCtx.Context, components, cfg.BridgeL2Sync, reorgDetectorL2,
		l2Client, rollupDataQuerier.RollupID)
	lastGERSync := runLastGERSyncIfNeeded(
		cliCtx.Context, components, cfg.LastGERSync, reorgDetectorL2, l2Client, l1InfoTreeSync,
	)
	var rpcServices []jRPC.Service
	for _, component := range components {
		switch component {
		case aggkitcommon.AGGORACLE:
			aggOracle := createAggoracle(rollupDataQuerier, *cfg, l1Client, l2Client, l1InfoTreeSync)
			go aggOracle.Start(cliCtx.Context)

		case aggkitcommon.BRIDGE:
			b := createBridgeService(
				cfg.REST,
				cfg.Common.NetworkID,
				l1InfoTreeSync,
				lastGERSync,
				l1BridgeSync,
				l2BridgeSync,
			)

			go b.Start(cliCtx.Context)
		case aggkitcommon.AGGSENDER:
			aggsender, err := createAggSender(
				cliCtx.Context,
				cfg.AggSender,
				l1Client,
				l1InfoTreeSync,
				l2BridgeSync,
				l2Client,
				rollupDataQuerier,
			)
			if err != nil {
				log.Fatal(err)
			}
			rpcServices = append(rpcServices, aggsender.GetRPCServices()...)

			go aggsender.Start(cliCtx.Context)
		case aggkitcommon.AGGCHAINPROOFGEN:
			aggchainProofGen, err := createAggchainProofGen(
				cliCtx.Context,
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
		go pprof.StartProfilingHTTPServer(cliCtx.Context, cfg.Profiling)
	}

	waitSignal(nil)

	return nil
}

func createAggchainProofGen(
	ctx context.Context,
	cfg prover.Config,
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter,
	l1InfoTreeSync *l1infotreesync.L1InfoTreeSync,
	l2Syncer *bridgesync.BridgeSync) (*prover.AggchainProofGenerationTool, error) {
	logger := log.WithFields("module", aggkitcommon.AGGCHAINPROOFGEN)

	aggchainProofGen, err := prover.NewAggchainProofGenerationTool(
		ctx,
		logger,
		cfg,
		l2Syncer,
		l1InfoTreeSync,
		l1Client,
		l2Client,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create AggchainProofGenerationTool: %w", err)
	}

	return aggchainProofGen, nil
}

func createAggSender(
	ctx context.Context,
	cfg aggsendercfg.Config,
	l1EthClient aggkittypes.BaseEthereumClienter,
	l1InfoTreeSync *l1infotreesync.L1InfoTreeSync,
	l2Syncer *bridgesync.BridgeSync,
	l2Client aggkittypes.BaseEthereumClienter,
	rollupDataQuerier *etherman.RollupDataQuerier) (*aggsender.AggSender, error) {
	logger := log.WithFields("module", aggkitcommon.AGGSENDER)

	if err := cfg.AgglayerClient.Validate(); err != nil {
		return nil, fmt.Errorf("invalid agglayer client config: %w", err)
	}

	agglayerClient, err := agglayer.NewAgglayerGRPCClient(cfg.AgglayerClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create agglayer grpc client: %w", err)
	}

	blockNotifier, err := aggsender.NewBlockNotifierPolling(l1EthClient,
		aggsender.ConfigBlockNotifierPolling{
			BlockFinalityType:     aggkittypes.NewBlockNumberFinality(cfg.BlockFinality),
			CheckNewBlockInterval: aggsender.AutomaticBlockInterval,
		}, logger, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize block notifier: %w", err)
	}

	notifierCfg, err := aggsender.NewConfigEpochNotifierPerBlock(ctx,
		agglayerClient, cfg.EpochNotificationPercentage)
	if err != nil {
		return nil, fmt.Errorf("failed to generate Epoch Notifier config. Reason: %w", err)
	}
	epochNotifier, err := aggsender.NewEpochNotifierPerBlock(
		blockNotifier,
		logger,
		*notifierCfg, nil)
	if err != nil {
		return nil, err
	}
	log.Infof("Starting blockNotifier: %s", blockNotifier.String())
	go blockNotifier.Start(ctx)
	log.Infof("Starting epochNotifier: %s", epochNotifier.String())
	go epochNotifier.Start(ctx)
	return aggsender.New(ctx, logger, cfg, agglayerClient,
		l1InfoTreeSync, l2Syncer, epochNotifier, l1EthClient, l2Client, rollupDataQuerier)
}

func createAggoracle(
	ethermanClient *etherman.RollupDataQuerier,
	cfg config.Config,
	l1Client,
	l2Client aggkittypes.BaseEthereumClienter,
	l1InfoTreeSyncer *l1infotreesync.L1InfoTreeSync,
) *aggoracle.AggOracle {
	logger := log.WithFields("module", aggkitcommon.AGGORACLE)
	l2ChainID, err := ethermanClient.GetRollupChainID()
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
		logger.Infof("AggOracle sender address: %s | GER contract address on L2: %s",
			ethTxManager.From().Hex(),
			cfg.AggOracle.EVMSender.GlobalExitRootL2Addr.Hex(),
		)
		go ethTxManager.Start()
		sender, err = chaingersender.NewEVMChainGERSender(
			logger,
			cfg.AggOracle.EVMSender.GlobalExitRootL2Addr,
			l2Client,
			ethTxManager,
			cfg.AggOracle.EVMSender.GasOffset,
			cfg.AggOracle.EVMSender.WaitPeriodMonitorTx.Duration,
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
		aggkittypes.NewBlockNumberFinality(cfg.AggOracle.BlockFinality),
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

func waitSignal(cancelFuncs []context.CancelFunc) {
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
		for _, caseWhereNeeded := range casesWhereNeeded {
			if actualCase == caseWhereNeeded {
				return true
			}
		}
	}

	return false
}

func runL1InfoTreeSyncerIfNeeded(
	ctx context.Context,
	components []string,
	cfg config.Config,
	l1Client aggkittypes.BaseEthereumClienter,
	reorgDetector *reorgdetector.ReorgDetector,
) *l1infotreesync.L1InfoTreeSync {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE, aggkitcommon.AGGSENDER,
		aggkitcommon.BRIDGE, aggkitcommon.L1INFOTREESYNC,
		aggkitcommon.AGGCHAINPROOFGEN}, components) {
		return nil
	}
	l1InfoTreeSync, err := l1infotreesync.New(
		ctx,
		cfg.L1InfoTreeSync.DBPath,
		cfg.L1InfoTreeSync.GlobalExitRootAddr,
		cfg.L1InfoTreeSync.RollupManagerAddr,
		cfg.L1InfoTreeSync.SyncBlockChunkSize,
		aggkittypes.NewBlockNumberFinality(cfg.L1InfoTreeSync.BlockFinality),
		reorgDetector,
		l1Client,
		cfg.L1InfoTreeSync.WaitForNewBlocksPeriod.Duration,
		cfg.L1InfoTreeSync.InitialBlock,
		cfg.L1InfoTreeSync.RetryAfterErrorPeriod.Duration,
		cfg.L1InfoTreeSync.MaxRetryAttemptsAfterError,
		l1infotreesync.FlagNone,
		aggkittypes.FinalizedBlock,
		cfg.L1InfoTreeSync.RequireStorageContentCompatibility,
	)
	if err != nil {
		log.Fatal(err)
	}
	go l1InfoTreeSync.Start(ctx)

	return l1InfoTreeSync
}

func runL1ClientIfNeeded(components []string, urlRPCL1 string) aggkittypes.EthClienter {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE,
		aggkitcommon.AGGSENDER,
		aggkitcommon.BRIDGE,
		aggkitcommon.L1INFOTREESYNC,
		aggkitcommon.AGGCHAINPROOFGEN,
	}, components) {
		return nil
	}
	log.Debugf("dialing L1 client at: %s", urlRPCL1)
	l1Client, err := ethclient.Dial(urlRPCL1)
	if err != nil {
		log.Fatalf("failed to create client for L1 using URL: %s. Err:%v", urlRPCL1, err)
	}

	return aggkittypes.NewDefaultEthClient(l1Client, l1Client.Client())
}

func runL2ClientIfNeeded(components []string, urlRPCL2 ethermanconfig.RPCClientConfig) aggkittypes.EthClienter {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE,
		aggkitcommon.BRIDGE,
		aggkitcommon.AGGSENDER,
		aggkitcommon.AGGCHAINPROOFGEN}, components) {
		return nil
	}
	l2Client, err := etherman.NewRPCClient(urlRPCL2)
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
		aggkitcommon.AGGORACLE, aggkitcommon.AGGSENDER,
		aggkitcommon.BRIDGE, aggkitcommon.L1INFOTREESYNC,
		aggkitcommon.AGGCHAINPROOFGEN},
		components) {
		return nil, nil
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
		aggkitcommon.AGGCHAINPROOFGEN}, components) {
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

func runLastGERSyncIfNeeded(
	ctx context.Context,
	components []string,
	cfg lastgersync.Config,
	reorgDetectorL2 *reorgdetector.ReorgDetector,
	l2Client aggkittypes.BaseEthereumClienter,
	l1InfoTreeSync *l1infotreesync.L1InfoTreeSync,
) *lastgersync.LastGERSync {
	if !isNeeded([]string{aggkitcommon.BRIDGE}, components) {
		return nil
	}
	lastGERSync, err := lastgersync.New(
		ctx,
		cfg.DBPath,
		reorgDetectorL2,
		l2Client,
		cfg.GlobalExitRootL2Addr,
		l1InfoTreeSync,
		cfg.RetryAfterErrorPeriod.Duration,
		cfg.MaxRetryAttemptsAfterError,
		aggkittypes.NewBlockNumberFinality(cfg.BlockFinality),
		cfg.WaitForNewBlocksPeriod.Duration,
		cfg.DownloadBufferSize,
		cfg.RequireStorageContentCompatibility,
		cfg.SyncMode,
	)
	if err != nil {
		log.Fatalf("error creating lastGERSync: %s", err)
	}

	go func() {
		if err := lastGERSync.Start(ctx); err != nil {
			log.Fatalf("lastGERSync failed: %s", err)
		}
	}()

	return lastGERSync
}

func runBridgeSyncL1IfNeeded(
	ctx context.Context,
	components []string,
	cfg bridgesync.Config,
	reorgDetectorL1 *reorgdetector.ReorgDetector,
	l1Client aggkittypes.EthClienter,
	rollupID uint32,
) *bridgesync.BridgeSync {
	if !isNeeded([]string{aggkitcommon.BRIDGE}, components) {
		return nil
	}

	bridgeSyncL1, err := bridgesync.NewL1(
		ctx,
		cfg.DBPath,
		cfg.BridgeAddr,
		cfg.SyncBlockChunkSize,
		aggkittypes.NewBlockNumberFinality(cfg.BlockFinality),
		reorgDetectorL1,
		l1Client,
		cfg.InitialBlockNum,
		cfg.WaitForNewBlocksPeriod.Duration,
		cfg.RetryAfterErrorPeriod.Duration,
		cfg.MaxRetryAttemptsAfterError,
		rollupID,
		true,
		cfg.RequireStorageContentCompatibility,
	)
	if err != nil {
		log.Fatalf("error creating bridgeSyncL1: %s", err)
	}
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
) *bridgesync.BridgeSync {
	if !isNeeded([]string{
		aggkitcommon.BRIDGE,
		aggkitcommon.AGGSENDER,
		aggkitcommon.AGGCHAINPROOFGEN}, components) {
		return nil
	}

	bridgeSyncL2, err := bridgesync.NewL2(
		ctx,
		cfg.DBPath,
		cfg.BridgeAddr,
		cfg.SyncBlockChunkSize,
		aggkittypes.NewBlockNumberFinality(cfg.BlockFinality),
		reorgDetectorL2,
		l2Client,
		cfg.InitialBlockNum,
		cfg.WaitForNewBlocksPeriod.Duration,
		cfg.RetryAfterErrorPeriod.Duration,
		cfg.MaxRetryAttemptsAfterError,
		rollupID,
		true,
		cfg.RequireStorageContentCompatibility,
	)
	if err != nil {
		log.Fatalf("error creating bridgeSyncL2: %s", err)
	}
	go bridgeSyncL2.Start(ctx)

	return bridgeSyncL2
}

func createBridgeService(
	cfg aggkitcommon.RESTConfig,
	l2NetworkID uint32,
	l1InfoTree *l1infotreesync.L1InfoTreeSync,
	injectedGERs *lastgersync.LastGERSync,
	bridgeL1 *bridgesync.BridgeSync,
	bridgeL2 *bridgesync.BridgeSync,
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
func createRollupDataQuerier(cfg config.L1NetworkConfig, components []string) (*etherman.RollupDataQuerier, error) {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE,
		aggkitcommon.AGGCHAINPROOFGEN,
		aggkitcommon.AGGSENDER,
		aggkitcommon.BRIDGE,
	}, components) {
		return nil, nil
	}

	return etherman.NewRollupDataQuerier(cfg,
		func(url string) (aggkittypes.BaseEthereumClienter, error) {
			return ethclient.Dial(url)
		},
		func(rollupAddr common.Address,
			client aggkittypes.BaseEthereumClienter) (etherman.RollupManagerContract, error) {
			return polygonrollupmanager.NewPolygonrollupmanager(rollupAddr, client)
		})
}
