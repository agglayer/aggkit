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
	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggoracle"
	"github.com/agglayer/aggkit/aggoracle/chaingersender"
	"github.com/agglayer/aggkit/aggsender"
	aggsendercfg "github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/flows"
	"github.com/agglayer/aggkit/aggsender/optimistic"
	"github.com/agglayer/aggkit/aggsender/prover"
	"github.com/agglayer/aggkit/aggsender/query"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	"github.com/agglayer/aggkit/bridgeservice"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/healthcheck"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/pprof"
	"github.com/agglayer/aggkit/prometheus"
	"github.com/agglayer/aggkit/reorgdetector"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/urfave/cli/v2"
)

func start(cliCtx *cli.Context) error {
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
	components := cliCtx.StringSlice(config.FlagComponents)
	l1Client := runL1ClientIfNeeded(cliCtx.Context, components, cfg.L1NetworkConfig.RPC)
	l2Client := runL2ClientIfNeeded(cliCtx.Context, components, cfg.Common.L2RPC)

	reorgDetectorL2, errChanL2 := runReorgDetectorL2IfNeeded(cliCtx.Context, components, l2Client, &cfg.ReorgDetectorL2)
	go func() {
		if err := <-errChanL2; err != nil {
			log.Fatal("Error from ReorgDetectorL2: ", err)
		}
	}()

	rollupDataQuerier, err := createRollupDataQuerier(cliCtx.Context, cfg.L1NetworkConfig, components)
	if err != nil {
		return fmt.Errorf("failed to create rollup data querier: %w", err)
	}

	l1InfoTreeSync := runL1InfoTreeSyncerIfNeeded(cliCtx.Context, components, *cfg, l1Client)
	l1BridgeSync := runBridgeSyncL1IfNeeded(cliCtx.Context, components, cfg.BridgeL1Sync, l1Client, 0)
	l2BridgeSync := runBridgeSyncL2IfNeeded(cliCtx.Context, components, cfg.BridgeL2Sync, reorgDetectorL2,
		l2Client, rollupDataQuerier.RollupID)
	l2GERSync := runL2GERSyncIfNeeded(
		cliCtx.Context, components, cfg.L2GERSync, reorgDetectorL2, l2Client, l1InfoTreeSync,
	)

	committeeQuerier := runAggsenderMultisigCommitteeIfNeeded(components, cfg.L1NetworkConfig.RollupAddr, l1Client)

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
				l2GERSync,
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
				committeeQuerier,
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
		case aggkitcommon.AGGSENDERVALIDATOR:
			aggsenderValidator, err := createAggSenderValidator(
				cliCtx.Context,
				cfg.Validator,
				l1InfoTreeSync,
				l2BridgeSync,
				l1Client,
				rollupDataQuerier,
				committeeQuerier,
			)
			if err != nil {
				log.Fatal(err)
			}
			rpcServices = append(rpcServices, aggsenderValidator.GetRPCServices()...)
			go aggsenderValidator.Start(cliCtx.Context)
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
	rollupDataQuerier *etherman.RollupDataQuerier,
	committeeQuerier *query.ECDSAMultisigCommitteeQuery,
) (*aggsender.AggsenderValidator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid aggsender validator config: %w", err)
	}

	logger := log.WithFields("module", aggkitcommon.AGGSENDERVALIDATOR)
	agglayerClient, err := agglayer.NewAgglayerClient(cfg.AgglayerClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create agglayer grpc client: %w", err)
	}

	var (
		flow                 validator.FlowInterface
		commonFlowComponents *flows.CommonFlowComponents
	)

	switch cfg.Mode {
	case aggsendertypes.PessimisticProofMode.String():
		commonFlowComponents, err = flows.CreateCommonFlowComponents(
			ctx, logger,
			nil, // storage is not used in validator,
			l1Client, l1InfoTreeSync, l2Syncer, rollupDataQuerier, committeeQuerier, 0, false,
			cfg.MaxCertSize, cfg.LerQuerier.RollupCreationBlockL1, cfg.DelayBetweenRetries.Duration, cfg.Signer,
			false, // full claims are not needed in validator mode
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create common flow components: %w", err)
		}

		flow = flows.NewPPFlow(
			logger,
			commonFlowComponents.BaseFlow,
			nil, // storage is not used in validator
			commonFlowComponents.L1InfoTreeDataQuerier,
			commonFlowComponents.L2BridgeQuerier,
			commonFlowComponents.Signer,
			cfg.PPConfig.RequireOneBridgeInPPCertificate,
			cfg.MaxL2BlockNumber,
		)
	case aggsendertypes.AggchainProofMode.String():
		commonFlowComponents, err = flows.CreateCommonFlowComponents(
			ctx, logger,
			nil, // storage is not used in validator,
			l1Client, l1InfoTreeSync, l2Syncer, rollupDataQuerier, committeeQuerier,
			0, cfg.FEPConfig.RequireNoBlockGap,
			cfg.MaxCertSize, cfg.LerQuerier.RollupCreationBlockL1,
			cfg.DelayBetweenRetries.Duration, cfg.Signer,
			false, // full claims are not needed in validator mode
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create common flow components: %w", err)
		}

		optimisticModeQuerier, err := optimistic.NewOptimisticModeQuerierFromContract(
			cfg.FEPConfig.SovereignRollupAddr, l1Client)
		if err != nil {
			return nil, fmt.Errorf("failed to create optimistic mode querier: %w", err)
		}

		flow = flows.NewAggchainProverFlow(
			logger,
			flows.NewAggchainProverFlowConfig(cfg.MaxL2BlockNumber),
			commonFlowComponents.BaseFlow,
			nil, // storage is not used in validator
			commonFlowComponents.L1InfoTreeDataQuerier,
			commonFlowComponents.L2BridgeQuerier,
			l1Client,
			commonFlowComponents.Signer,
			optimisticModeQuerier,
			nil, // we don't query the prover in validator mode
		)
	default:
		return nil, fmt.Errorf("unsupported mode %s", cfg.Mode)
	}

	aggchainFEPQuerier, err := query.NewAggchainFEPQuerier(
		logger,
		aggsendertypes.AggsenderMode(cfg.Mode),
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

	return aggsender.NewAggsenderValidator(
		ctx, logger, cfg, flow,
		commonFlowComponents.L1InfoTreeDataQuerier,
		agglayerClient,
		certQuerier,
		commonFlowComponents.LERQuerier,
		commonFlowComponents.Signer,
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

	agglayerClient, err := agglayer.NewAgglayerClient(cfg.AgglayerClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create agglayer grpc client: %w", err)
	}
	blockNotifier, err := aggsender.NewBlockNotifierPolling(l1EthClient,
		aggsender.ConfigBlockNotifierPolling{
			BlockFinalityType:     aggkittypes.LatestBlock,
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

	aggsender, err := aggsender.New(ctx, logger, cfg, agglayerClient,
		l1InfoTreeSync, l2Syncer, epochNotifier, l1EthClient, l2Client, rollupDataQuerier, committeeQuerier)
	if err != nil {
		return nil, fmt.Errorf("failed to create AggSender: %w", err)
	}

	return aggsender, nil
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
			cfg.AggOracle.EVMSender.AggOracleCommitteeAddr,
			l2Client,
			ethTxManager,
			cfg.AggOracle.EVMSender.GasOffset,
			cfg.AggOracle.EVMSender.WaitPeriodMonitorTx.Duration,
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
) *l1infotreesync.L1InfoTreeSync {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE, aggkitcommon.AGGSENDER, aggkitcommon.AGGSENDERVALIDATOR,
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
		aggkittypes.FinalizedBlock,
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

func runL1ClientIfNeeded(ctx context.Context,
	components []string, rpcClientCfg config.RPCClientConfig) aggkittypes.EthClienter {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE,
		aggkitcommon.AGGSENDER,
		aggkitcommon.AGGSENDERVALIDATOR,
		aggkitcommon.BRIDGE,
		aggkitcommon.L1INFOTREESYNC,
		aggkitcommon.AGGCHAINPROOFGEN,
	}, components) {
		return nil
	}
	log.Debugf("dialing L1 client at: %s", rpcClientCfg.URL)

	retryHandler, err := rpcClientCfg.NewRetryHandler()
	if err != nil {
		log.Fatalf("failed to create retry handler: %w", err)
	}

	ethClient, err := aggkittypes.DialWithRetry(ctx, rpcClientCfg.URL, retryHandler)
	if err != nil {
		log.Fatalf("failed to create client for L1 using URL: %s. Err:%v", rpcClientCfg.URL, err)
	}

	return ethClient
}

func runL2ClientIfNeeded(ctx context.Context,
	components []string, urlRPCL2 config.L2RPCClientConfig) aggkittypes.EthClienter {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE,
		aggkitcommon.BRIDGE,
		aggkitcommon.AGGSENDER,
		aggkitcommon.AGGSENDERVALIDATOR,
		aggkitcommon.AGGCHAINPROOFGEN}, components) {
		return nil
	}
	l2Client, err := etherman.NewRPCClient(ctx, urlRPCL2)
	if err != nil {
		log.Fatalf("failed to create client for L2 using URL: %s. Err:%v", urlRPCL2, err)
	}

	return l2Client
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

func runL2GERSyncIfNeeded(
	ctx context.Context,
	components []string,
	cfg l2gersync.Config,
	reorgDetectorL2 *reorgdetector.ReorgDetector,
	l2Client aggkittypes.BaseEthereumClienter,
	l1InfoTreeSync *l1infotreesync.L1InfoTreeSync,
) *l2gersync.L2GERSync {
	if !isNeeded([]string{aggkitcommon.BRIDGE}, components) {
		return nil
	}
	l2GERSync, err := l2gersync.New(
		ctx,
		cfg.DBPath,
		reorgDetectorL2,
		l2Client,
		cfg.GlobalExitRootL2Addr,
		l1InfoTreeSync,
		cfg.RetryAfterErrorPeriod.Duration,
		cfg.MaxRetryAttemptsAfterError,
		cfg.BlockFinality,
		cfg.WaitForNewBlocksPeriod.Duration,
		cfg.DownloadBufferSize,
		cfg.RequireStorageContentCompatibility,
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
		aggkittypes.FinalizedBlock,
		l1Client,
		cfg.InitialBlockNum,
		cfg.WaitForNewBlocksPeriod.Duration,
		cfg.RetryAfterErrorPeriod.Duration,
		cfg.MaxRetryAttemptsAfterError,
		rollupID,
		true,
		cfg.RequireStorageContentCompatibility,
		cfg.DBQueryTimeout.Duration,
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
	fullClaimsNeeded := isNeeded([]string{
		aggkitcommon.BRIDGE,
		aggkitcommon.AGGSENDER,
		aggkitcommon.AGGCHAINPROOFGEN}, components)

	fullClaimsNotNeeded := isNeeded([]string{
		aggkitcommon.AGGSENDERVALIDATOR,
	}, components)

	if !fullClaimsNeeded && !fullClaimsNotNeeded {
		// no bridge sync needed
		return nil
	}

	bridgeSyncL2, err := bridgesync.NewL2(
		ctx,
		cfg.DBPath,
		cfg.BridgeAddr,
		cfg.SyncBlockChunkSize,
		cfg.BlockFinality,
		reorgDetectorL2,
		l2Client,
		cfg.InitialBlockNum,
		cfg.WaitForNewBlocksPeriod.Duration,
		cfg.RetryAfterErrorPeriod.Duration,
		cfg.MaxRetryAttemptsAfterError,
		rollupID,
		fullClaimsNeeded,
		cfg.RequireStorageContentCompatibility,
		cfg.DBQueryTimeout.Duration,
	)
	if err != nil {
		log.Fatalf("error creating bridgeSyncL2: %s", err)
	}
	go bridgeSyncL2.Start(ctx)

	return bridgeSyncL2
}

func runAggsenderMultisigCommitteeIfNeeded(
	components []string,
	rollupAddr common.Address,
	l1Client aggkittypes.BaseEthereumClienter,
) *query.ECDSAMultisigCommitteeQuery {
	if !isNeeded([]string{aggkitcommon.AGGSENDER, aggkitcommon.AGGSENDERVALIDATOR}, components) {
		return nil
	}

	committeeQuerier, err := query.NewECDSAMultisigCommitteeQuery(rollupAddr, l1Client)
	if err != nil {
		log.Fatalf("failed to create ECDSA multisig committee querier (SC address: %s): %s", rollupAddr, err)
	}

	return committeeQuerier
}

func createBridgeService(
	cfg aggkitcommon.RESTConfig,
	l2NetworkID uint32,
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
	cfg config.L1NetworkConfig,
	components []string) (*etherman.RollupDataQuerier, error) {
	if !isNeeded([]string{
		aggkitcommon.AGGORACLE,
		aggkitcommon.AGGCHAINPROOFGEN,
		aggkitcommon.AGGSENDER,
		aggkitcommon.AGGSENDERVALIDATOR,
		aggkitcommon.BRIDGE,
		aggkitcommon.L1INFOTREESYNC,
	}, components) {
		return &etherman.RollupDataQuerier{}, nil
	}

	retryHandler, err := cfg.RPC.NewRetryHandler()
	if err != nil {
		log.Fatalf("failed to create retry handler: %w", err)
	}

	ethClient, err := aggkittypes.DialWithRetry(ctx, cfg.RPC.URL, retryHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to create Ethereum client for L1 using URL: %s. Err: %w", cfg.RPC.URL, err)
	}

	return etherman.NewRollupDataQuerier(cfg, ethClient,
		func(rollupAddr common.Address,
			client aggkittypes.BaseEthereumClienter) (etherman.RollupManagerContract, error) {
			return polygonrollupmanager.NewPolygonrollupmanager(rollupAddr, client)
		})
}
