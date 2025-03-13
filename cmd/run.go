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

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxlog "github.com/0xPolygon/zkevm-ethtx-manager/log"
	"github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggoracle"
	"github.com/agglayer/aggkit/aggoracle/chaingersender"
	"github.com/agglayer/aggkit/aggsender"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/claimsponsor"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/healthcheck"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/lastgersync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/prometheus"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/rpc"
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
	l1Client := runL1ClientIfNeeded(components, cfg.Etherman.URL)
	l2Client := runL2ClientIfNeeded(components, getL2RPCUrl(cfg))
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

	rollupID := getRollUpIDIfNeeded(components, cfg.NetworkConfig.L1Config, l1Client)
	l1InfoTreeSync := runL1InfoTreeSyncerIfNeeded(cliCtx.Context, components, *cfg, l1Client, reorgDetectorL1)
	claimSponsor := runClaimSponsorIfNeeded(cliCtx.Context, components, l2Client, cfg.ClaimSponsor)
	l1BridgeSync := runBridgeSyncL1IfNeeded(cliCtx.Context, components, cfg.BridgeL1Sync, reorgDetectorL1,
		l1Client, 0)
	l2BridgeSync := runBridgeSyncL2IfNeeded(cliCtx.Context, components, cfg.BridgeL2Sync, reorgDetectorL2,
		l2Client, rollupID)
	lastGERSync := runLastGERSyncIfNeeded(
		cliCtx.Context, components, cfg.LastGERSync, reorgDetectorL2, l2Client, l1InfoTreeSync,
	)
	var rpcServices []jRPC.Service
	for _, component := range components {
		switch component {
		case aggkitcommon.AGGORACLE:
			aggOracle := createAggoracle(*cfg, l1Client, l2Client, l1InfoTreeSync)
			go aggOracle.Start(cliCtx.Context)
		case aggkitcommon.BRIDGE:
			rpcBridge := createBridgeRPC(
				cfg.RPC,
				cfg.Common.NetworkID,
				claimSponsor,
				l1InfoTreeSync,
				lastGERSync,
				l1BridgeSync,
				l2BridgeSync,
			)
			rpcServices = append(rpcServices, rpcBridge...)

		case aggkitcommon.AGGSENDER:
			aggsender, err := createAggSender(
				cliCtx.Context,
				cfg.AggSender,
				l1Client,
				l1InfoTreeSync,
				l2BridgeSync,
				l2Client,
			)
			if err != nil {
				log.Fatal(err)
			}
			rpcServices = append(rpcServices, aggsender.GetRPCServices()...)

			go aggsender.Start(cliCtx.Context)
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

	waitSignal(nil)

	return nil
}

func createAggSender(
	ctx context.Context,
	cfg aggsender.Config,
	l1EthClient *ethclient.Client,
	l1InfoTreeSync *l1infotreesync.L1InfoTreeSync,
	l2Syncer *bridgesync.BridgeSync,
	l2Client *ethclient.Client) (*aggsender.AggSender, error) {
	logger := log.WithFields("module", aggkitcommon.AGGSENDER)
	agglayerClient := agglayer.NewAggLayerClient(cfg.AggLayerURL)
	blockNotifier, err := aggsender.NewBlockNotifierPolling(l1EthClient, aggsender.ConfigBlockNotifierPolling{
		BlockFinalityType:     etherman.NewBlockNumberFinality(cfg.BlockFinality),
		CheckNewBlockInterval: aggsender.AutomaticBlockInterval,
	}, logger, nil)
	if err != nil {
		return nil, err
	}

	notifierCfg, err := aggsender.NewConfigEpochNotifierPerBlock(agglayerClient, cfg.EpochNotificationPercentage)
	if err != nil {
		return nil, fmt.Errorf("cant generate config for Epoch Notifier because: %w", err)
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
		l1InfoTreeSync, l2Syncer, epochNotifier, l1EthClient, l2Client)
}

func createAggoracle(
	cfg config.Config,
	l1Client,
	l2Client *ethclient.Client,
	l1InfoTreeSyncer *l1infotreesync.L1InfoTreeSync,
) *aggoracle.AggOracle {
	logger := log.WithFields("module", aggkitcommon.AGGORACLE)
	ethermanClient, err := etherman.NewClient(cfg.Etherman, cfg.NetworkConfig.L1Config, cfg.Common)
	if err != nil {
		logger.Fatal(err)
	}
	l2ChainID, err := ethermanClient.GetL2ChainID()
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
		etherman.NewBlockNumberFinality(cfg.AggOracle.BlockFinality),
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
	client *ethclient.Client,
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
	l1Client *ethclient.Client,
	reorgDetector *reorgdetector.ReorgDetector,
) *l1infotreesync.L1InfoTreeSync {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE, aggkitcommon.AGGSENDER,
		aggkitcommon.BRIDGE, aggkitcommon.L1INFOTREESYNC}, components) {
		return nil
	}
	l1InfoTreeSync, err := l1infotreesync.New(
		ctx,
		cfg.L1InfoTreeSync.DBPath,
		cfg.L1InfoTreeSync.GlobalExitRootAddr,
		cfg.L1InfoTreeSync.RollupManagerAddr,
		cfg.L1InfoTreeSync.SyncBlockChunkSize,
		etherman.NewBlockNumberFinality(cfg.L1InfoTreeSync.BlockFinality),
		reorgDetector,
		l1Client,
		cfg.L1InfoTreeSync.WaitForNewBlocksPeriod.Duration,
		cfg.L1InfoTreeSync.InitialBlock,
		cfg.L1InfoTreeSync.RetryAfterErrorPeriod.Duration,
		cfg.L1InfoTreeSync.MaxRetryAttemptsAfterError,
		l1infotreesync.FlagNone,
		etherman.FinalizedBlock,
	)
	if err != nil {
		log.Fatal(err)
	}
	go l1InfoTreeSync.Start(ctx)

	return l1InfoTreeSync
}

func runL1ClientIfNeeded(components []string, urlRPCL1 string) *ethclient.Client {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE,
		aggkitcommon.AGGSENDER,
		aggkitcommon.BRIDGE,
		aggkitcommon.L1INFOTREESYNC,
	}, components) {
		return nil
	}
	log.Debugf("dialing L1 client at: %s", urlRPCL1)
	l1CLient, err := ethclient.Dial(urlRPCL1)
	if err != nil {
		log.Fatalf("failed to create client for L1 using URL: %s. Err:%v", urlRPCL1, err)
	}

	return l1CLient
}

func getRollUpIDIfNeeded(components []string, networkConfig ethermanconfig.L1Config,
	l1Client *ethclient.Client) uint32 {
	if !isNeeded([]string{
		aggkitcommon.AGGSENDER,
	}, components) {
		return 0
	}
	rollupID, err := etherman.GetRollupID(networkConfig, networkConfig.ZkEVMAddr, l1Client)
	if err != nil {
		log.Fatal(err)
	}
	return rollupID
}

func runL2ClientIfNeeded(components []string, urlRPCL2 string) *ethclient.Client {
	if !isNeeded([]string{aggkitcommon.AGGORACLE, aggkitcommon.BRIDGE, aggkitcommon.AGGSENDER}, components) {
		return nil
	}

	log.Infof("dialing L2 client at: %s", urlRPCL2)
	l2CLient, err := ethclient.Dial(urlRPCL2)
	if err != nil {
		log.Fatal(err)
	}

	return l2CLient
}

func runReorgDetectorL1IfNeeded(
	ctx context.Context,
	components []string,
	l1Client *ethclient.Client,
	cfg *reorgdetector.Config,
) (*reorgdetector.ReorgDetector, chan error) {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE, aggkitcommon.AGGSENDER,
		aggkitcommon.BRIDGE, aggkitcommon.L1INFOTREESYNC},
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
	l2Client *ethclient.Client,
	cfg *reorgdetector.Config,
) (*reorgdetector.ReorgDetector, chan error) {
	if !isNeeded([]string{aggkitcommon.AGGORACLE, aggkitcommon.BRIDGE, aggkitcommon.AGGSENDER}, components) {
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

func runClaimSponsorIfNeeded(
	ctx context.Context,
	components []string,
	l2Client *ethclient.Client,
	cfg claimsponsor.EVMClaimSponsorConfig,
) *claimsponsor.ClaimSponsor {
	if !isNeeded([]string{aggkitcommon.BRIDGE}, components) || !cfg.Enabled {
		return nil
	}

	logger := log.WithFields("module", aggkitcommon.CLAIM_SPONSOR)
	// In the future there may support different backends other than EVM, and this will require different config.
	// But today only EVM is supported
	ethTxManagerL2, err := ethtxmanager.New(cfg.EthTxManager)
	if err != nil {
		logger.Fatal(err)
	}
	go ethTxManagerL2.Start()
	cs, err := claimsponsor.NewEVMClaimSponsor(
		logger,
		cfg.DBPath,
		l2Client,
		cfg.BridgeAddrL2,
		cfg.SenderAddr,
		cfg.MaxGas,
		cfg.GasOffset,
		ethTxManagerL2,
		cfg.RetryAfterErrorPeriod.Duration,
		cfg.MaxRetryAttemptsAfterError,
		cfg.WaitTxToBeMinedPeriod.Duration,
		cfg.WaitTxToBeMinedPeriod.Duration,
	)
	if err != nil {
		logger.Fatalf("error creating claim sponsor: %s", err)
	}
	go cs.Start(ctx)

	return cs
}

func runLastGERSyncIfNeeded(
	ctx context.Context,
	components []string,
	cfg lastgersync.Config,
	reorgDetectorL2 *reorgdetector.ReorgDetector,
	l2Client *ethclient.Client,
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
		etherman.NewBlockNumberFinality(cfg.BlockFinality),
		cfg.WaitForNewBlocksPeriod.Duration,
		cfg.DownloadBufferSize,
	)
	if err != nil {
		log.Fatalf("error creating lastGERSync: %s", err)
	}
	go lastGERSync.Start(ctx)

	return lastGERSync
}

func runBridgeSyncL1IfNeeded(
	ctx context.Context,
	components []string,
	cfg bridgesync.Config,
	reorgDetectorL1 *reorgdetector.ReorgDetector,
	l1Client *ethclient.Client,
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
		etherman.NewBlockNumberFinality(cfg.BlockFinality),
		reorgDetectorL1,
		l1Client,
		cfg.InitialBlockNum,
		cfg.WaitForNewBlocksPeriod.Duration,
		cfg.RetryAfterErrorPeriod.Duration,
		cfg.MaxRetryAttemptsAfterError,
		rollupID,
		false,
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
	l2Client *ethclient.Client,
	rollupID uint32,
) *bridgesync.BridgeSync {
	if !isNeeded([]string{aggkitcommon.BRIDGE, aggkitcommon.AGGSENDER}, components) {
		return nil
	}

	bridgeSyncL2, err := bridgesync.NewL2(
		ctx,
		cfg.DBPath,
		cfg.BridgeAddr,
		cfg.SyncBlockChunkSize,
		etherman.NewBlockNumberFinality(cfg.BlockFinality),
		reorgDetectorL2,
		l2Client,
		cfg.InitialBlockNum,
		cfg.WaitForNewBlocksPeriod.Duration,
		cfg.RetryAfterErrorPeriod.Duration,
		cfg.MaxRetryAttemptsAfterError,
		rollupID,
		true,
	)
	if err != nil {
		log.Fatalf("error creating bridgeSyncL2: %s", err)
	}
	go bridgeSyncL2.Start(ctx)

	return bridgeSyncL2
}

func createBridgeRPC(
	cfg jRPC.Config,
	l2NetworkID uint32,
	sponsor *claimsponsor.ClaimSponsor,
	l1InfoTree *l1infotreesync.L1InfoTreeSync,
	injectedGERs *lastgersync.LastGERSync,
	bridgeL1 *bridgesync.BridgeSync,
	bridgeL2 *bridgesync.BridgeSync,
) []jRPC.Service {
	logger := log.WithFields("module", aggkitcommon.BRIDGE)
	services := []jRPC.Service{
		{
			Name: rpc.BRIDGE,
			Service: rpc.NewBridgeEndpoints(
				logger,
				cfg.WriteTimeout.Duration,
				cfg.ReadTimeout.Duration,
				l2NetworkID,
				sponsor,
				l1InfoTree,
				injectedGERs,
				bridgeL1,
				bridgeL2,
			),
		},
	}
	return services
}

func createRPC(cfg jRPC.Config, services []jRPC.Service) *jRPC.Server {
	logger := log.WithFields("module", "RPC")

	healthHandler := healthcheck.NewHealthCheckHandler(logger)
	return jRPC.NewServer(cfg, services,
		jRPC.WithLogger(logger.GetSugaredLogger()),
		jRPC.WithHealthHandler(healthHandler))
}

func getL2RPCUrl(c *config.Config) string {
	if c.AggSender.URLRPCL2 != "" {
		return c.AggSender.URLRPCL2
	}

	return c.AggOracle.EVMSender.URLRPCL2
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
