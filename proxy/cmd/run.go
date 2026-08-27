package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/sources"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/proxy"
	proxyconfig "github.com/agglayer/aggkit/proxy/config"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v2"
)

func start(cliCtx *cli.Context) error {
	// Validate components first before loading configuration
	components := cliCtx.StringSlice(proxyconfig.FlagComponents)
	if err := proxyconfig.ValidateComponents(components); err != nil {
		return err
	}

	cfg, err := proxyconfig.Load(cliCtx)
	if err != nil {
		return err
	}

	// Fingerprint of the effective configuration, exposed by the tracker health endpoint
	configSHA1, err := proxyconfig.SHA1(cfg)
	if err != nil {
		return err
	}

	if err := cfg.L1RPC.Validate(); err != nil {
		return fmt.Errorf("invalid L1RPC config: %w", err)
	}

	log.Init(cfg.Log)

	switch cfg.Log.Environment {
	case log.EnvironmentDevelopment:
		aggkit.PrintVersion(os.Stdout)
		log.Info("Starting " + appName)
	case log.EnvironmentProduction:
		log.Infof("Starting %s: %s", appName, aggkit.GetVersion().Brief())
	}

	log.Debugf("Components to run: %v", components)

	// Create a cancellable context for graceful shutdown
	ctx, cancel := context.WithCancel(cliCtx.Context)
	defer cancel()

	l1Client := runL1Client(ctx, cfg.L1RPC)

	// The finder serves the per-network bridge service URL / JSON-RPC endpoint and is shared by
	// every component of this binary.
	finder := runBridgeServiceFinder(ctx, cfg.BridgeServiceFinder, l1Client)

	// Shared REST/WS server: every component registers its routes on it before Start
	restServer := proxy.NewRESTServer(cfg.REST, log.WithFields("module", "rest"))

	for _, component := range components {
		switch component {
		case proxyconfig.PROXY:
			runProxy(ctx, cfg, finder, restServer)
		case proxyconfig.TRACKER:
			runTracker(ctx, cfg, finder, configSHA1, l1Client, restServer)
		}
	}

	// No-op if no component registered routes
	if err := restServer.Start(ctx); err != nil {
		return err
	}

	waitSignal()

	return nil
}

// runL1Client dials the L1 JSON-RPC endpoint. It is always required: the bridge service finder
// enumerates the rollup manager's networks and polls its events through it.
func runL1Client(ctx context.Context, rpcClientCfg ethermanconfig.RPCClientConfig) aggkittypes.EthClienter {
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

// runBridgeServiceFinder builds and starts the bridge service finder, which resolves and keeps
// fresh the bridge service URL and JSON-RPC endpoint of every network attached to the configured
// rollup manager.
func runBridgeServiceFinder(
	ctx context.Context,
	cfg bridgeservicefinder.Config,
	l1Client aggkittypes.EthClienter,
) bridgeservicefinder.Finder {
	if cfg.RollupManagerAddr == (common.Address{}) {
		log.Fatal("BridgeServiceFinder.RollupManagerAddr is required")
	}

	finder, err := bridgeservicefinder.New(cfg, bridgeservicefinder.Options{EthClient: l1Client})
	if err != nil {
		log.Fatalf("failed to create bridge service finder: %v", err)
	}

	if err := finder.Start(ctx); err != nil {
		log.Fatalf("failed to start bridge service finder: %v", err)
	}

	return finder
}

// runProxy starts the bridge-service proxy component: it routes incoming bridge REST requests to
// the per-network bridge service resolved by the finder.
func runProxy(
	_ context.Context,
	_ *proxyconfig.Config,
	finder bridgeservicefinder.Finder,
	restServer *proxy.RESTServer,
) {
	svc := proxy.New(proxy.Config{Logger: log.WithFields("module", "proxy")}, finder)
	restServer.Register(svc)
	log.Info("proxy component started")
}

// runTracker starts the bridge tracker component: the supervised-bridges registry shared by
// the REST/WS handlers and the tracking engine resolving bridge statuses through the
// per-network sources.
func runTracker(
	ctx context.Context,
	cfg *proxyconfig.Config,
	finder bridgeservicefinder.Finder,
	configSHA1 string,
	l1Client aggkittypes.EthClienter,
	restServer *proxy.RESTServer,
) {
	trackerCfg := cfg.Tracker
	if err := trackerCfg.Validate(); err != nil {
		log.Fatalf("invalid tracker config: %v", err)
	}
	registry := bridgetracker.NewMemoryRegistry(trackerCfg.MaxTrackedBridges)
	trackerCfg.Logger = log.WithFields("module", "bridgetracker")
	trackerCfg.ConfigSHA1 = configSHA1
	trackerCfg.Registry = registry
	// The tracker's WebSocket endpoint enforces the same origin policy as the REST server it's
	// served alongside (see aggkitcommon.CORSConfig.OriginAllowed for why it can't just reuse
	// the REST CORS headers).
	trackerCfg.CORS = cfg.REST.CORS

	if err := trackerCfg.AgglayerClient.Validate(); err != nil {
		log.Fatalf("invalid agglayer client config: %v", err)
	}
	agglayerClient, err := agglayer.NewAgglayerClient(
		trackerCfg.AgglayerClient, log.WithFields("module", "bridgetracker-agglayerclient"))
	if err != nil {
		log.Fatalf("failed to create agglayer client: %v", err)
	}

	// Per-network JSON-RPC clients resolve through the finder; L1 (network 0) is pinned to
	// the proxy's own L1 client, which carries the configured retry policy
	rpcClients := sources.NewFinderClients(
		log.WithFields("module", "bridgetracker-rpcclients"), finder, sources.StaticClients{0: l1Client})
	bridgeEvents, err := sources.NewBridgeEventSource(
		rpcClients, trackerCfg.L1BlockFinality, trackerCfg.L2BlockFinality, trackerCfg.BridgeAddrs)
	if err != nil {
		log.Fatalf("failed to create bridge event source: %v", err)
	}
	gerSource := sources.NewGERSource(finder, rpcClients, trackerCfg.L1GlobalExitRootAddress,
		trackerCfg.L1BlockFinality, log.WithFields("module", "bridgetracker-gersource"))

	// GET /activity/from/{from_address} scans every network the finder knows about (via
	// finder.NetworkIDs) for bridges sent by an address, and resolves their claim state through
	// the same per-network JSON-RPC clients and BridgeAddrs used above
	activitySource := sources.NewActivitySource(finder, rpcClients, trackerCfg.BridgeAddrs)
	trackerCfg.ActivityScanner = activitySource
	trackerCfg.ActivityClaims = activitySource

	tracker := bridgetracker.New(&trackerCfg)

	engine, err := bridgetracker.NewEngine(
		bridgetracker.EngineConfig{
			RetentionPeriod: trackerCfg.RetentionPeriod.Duration,
			IdleTimeout:     trackerCfg.IdleTimeout.Duration,
		},
		log.WithFields("module", "bridgetracker-engine"),
		registry,
		bridgetracker.EngineSources{
			Bridges: bridgeEvents,
			Certificates: sources.NewCertificateSource(
				agglayerClient, finder, log.WithFields("module", "bridgetracker-certificatesource")),
			GERs:                   gerSource,
			WaitingGERUpdateSource: gerSource,
			LERs:                   sources.NewLERSource(rpcClients),
			Claims:                 sources.NewClaimSource(finder),
			Settlement: sources.NewSettlementSource(
				rpcClients, trackerCfg.L1BlockFinality, trackerCfg.L1GlobalExitRootAddress),
		},
	)
	if err != nil {
		log.Fatalf("failed to create bridge tracker engine: %v", err)
	}
	engine.Start(ctx)

	restServer.Register(tracker.API())
	log.Info("tracker component started")
}

func waitSignal() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	sig := <-signals
	log.Infof("Received signal %s, shutting down %s", sig, appName)
}
