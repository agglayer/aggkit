package bridgeservicefinder

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// moduleName is the log field value identifying this package's log lines.
const moduleName = "bridgeservicefinder"

var (
	// ErrNilRollupManagerQuerier is returned by New when no rollup-manager querier is available and
	// none could be constructed from the config (missing RollupManagerAddr / eth client).
	ErrNilRollupManagerQuerier = errors.New("rollup manager querier is required")
	// ErrServicesUnhealthyOnStart is returned by Start when Config.RequireAllHealthyOnStart is true
	// and at least one resolved bridge service was unreachable during initial cache construction.
	ErrServicesUnhealthyOnStart = errors.New("one or more resolved bridge services were unreachable at start")
)

// Options carries the injectable dependencies for New. Any nil field is replaced by a default
// implementation built from cfg (and, where needed, the shared eth client). Injecting these is how
// tests substitute mocks for the rollup-manager querier, per-rollup reader factory, health checker
// and log filterer without touching real RPC.
type Options struct {
	// EthClient is the shared L1 eth client used to build the default rollup-manager querier, the
	// default per-rollup reader factory and the default log filterer. It may be nil only if all of
	// RollupManager, ReaderFactory and LogFilterer are supplied explicitly.
	EthClient aggkittypes.BaseEthereumClienter
	// RollupManager enumerates the attached rollups. Defaults to the agglayermanager binding bound
	// to Config.RollupManagerAddr using EthClient.
	RollupManager RollupManagerQuerier
	// ReaderFactory builds a per-rollup contract reader. Defaults to newContractReader.
	ReaderFactory RollupContractReaderFactory
	// HealthChecker probes resolved URLs. Defaults to the HTTP /health checker built from cfg.
	HealthChecker HealthChecker
	// HTTPClient is passed to the default HealthChecker. Defaults to a client with the configured
	// health-check timeout. Ignored when HealthChecker is supplied.
	HTTPClient *http.Client
	// LogFilterer is the eth-client surface the (S4) listener uses. Stored now for the later live-
	// update step; not exercised by Start's initial cache build. Defaults to EthClient.
	LogFilterer LogFilterer
	// Logger is the logger used by the finder. Defaults to log.WithFields("module", moduleName).
	Logger *log.Logger
}

// finder is the concrete Finder. It holds the resolved dependencies, the config, and the
// networkID -> URL cache. See doc.go for the full design.
type finder struct {
	cfg           Config
	logger        *log.Logger
	rollupManager RollupManagerQuerier
	readerFactory RollupContractReaderFactory
	healthChecker HealthChecker
	logFilterer   LogFilterer
	ethClient     aggkittypes.BaseEthereumClienter
	resolver      *resolver
	cache         *cache

	// addrToNetworkID maps each watched rollup contract address to its networkID. It is populated at
	// Start from the enumeration and is the routing table the (S4) event listener will use to apply
	// an incoming log to the correct cache entry.
	addrToNetworkID map[common.Address]uint32
}

// New constructs a bridge service finder from cfg and the injectable dependencies in opts. Any
// dependency left nil in opts is built with a default implementation derived from cfg (which
// requires opts.EthClient to be set). It validates that a rollup-manager querier is available and
// applies Config defaults for unset fields.
func New(cfg Config, opts Options) (Finder, error) {
	cfg = applyConfigDefaults(cfg)

	logger := opts.Logger
	if logger == nil {
		logger = log.WithFields("module", moduleName)
	}

	rollupManager := opts.RollupManager
	if rollupManager == nil {
		if opts.EthClient == nil {
			return nil, ErrNilRollupManagerQuerier
		}

		mgr, err := agglayermanager.NewAgglayermanagerCaller(cfg.RollupManagerAddr, opts.EthClient)
		if err != nil {
			return nil, fmt.Errorf("failed to bind rollup manager at %s: %w", cfg.RollupManagerAddr, err)
		}

		rollupManager = mgr
	}

	readerFactory := opts.ReaderFactory
	if readerFactory == nil {
		readerFactory = newContractReader
	}

	healthChecker := opts.HealthChecker
	if healthChecker == nil {
		healthChecker = newHTTPHealthChecker(
			opts.HTTPClient, cfg.HealthCheckPath, cfg.HealthCheckTimeout.Duration, logger)
	}

	logFilterer := opts.LogFilterer
	if logFilterer == nil {
		logFilterer = opts.EthClient
	}

	return &finder{
		cfg:             cfg,
		logger:          logger,
		rollupManager:   rollupManager,
		readerFactory:   readerFactory,
		healthChecker:   healthChecker,
		logFilterer:     logFilterer,
		ethClient:       opts.EthClient,
		resolver:        newResolver(cfg.URLs, DefaultBridgeServicePort),
		cache:           newCache(),
		addrToNetworkID: make(map[common.Address]uint32),
	}, nil
}

// applyConfigDefaults returns cfg with every unset field replaced by its Default* value.
func applyConfigDefaults(cfg Config) Config {
	if cfg.BlockFinality.IsEmpty() {
		if bf, err := aggkittypes.NewBlockNumberFinality(DefaultBlockFinality); err == nil {
			cfg.BlockFinality = *bf
		}
	}

	if cfg.PollInterval.Duration == 0 {
		cfg.PollInterval = DefaultPollInterval
	}

	if cfg.BlockChunkSize == 0 {
		cfg.BlockChunkSize = DefaultBlockChunkSize
	}

	if cfg.HealthCheckPath == "" {
		cfg.HealthCheckPath = DefaultHealthCheckPath
	}

	if cfg.HealthCheckTimeout.Duration == 0 {
		cfg.HealthCheckTimeout = DefaultHealthCheckTimeout
	}

	return cfg
}

// Start builds the initial networkID -> URL cache for all attached networks, probes each resolved
// service via /health, and (in a later step) launches the finality-based event-polling loop. It
// blocks until the initial cache is built or ctx is cancelled.
//
// Behaviour on failure to resolve or reach a service:
//
//   - A network for which no source yields a URL (ErrNoSourceAvailable) is skipped: no cache entry
//     is created and GetURL will later return ErrURLNotFound for it. Enumeration continues.
//   - A resolved URL that fails its /health probe is still cached with healthy=false so a later
//     on-chain update can heal it. Whether Start returns ErrServicesUnhealthyOnStart in that case is
//     governed by Config.RequireAllHealthyOnStart (default false: record and continue).
//   - A genuine (non-fall-through) resolution error for a network is logged and that network is
//     skipped; it does not abort the whole enumeration.
func (f *finder) Start(ctx context.Context) error {
	if err := f.buildInitialCache(ctx); err != nil {
		return err
	}

	unhealthy := f.probeAll(ctx)
	if unhealthy > 0 && f.cfg.RequireAllHealthyOnStart {
		return fmt.Errorf("%w: %d unreachable", ErrServicesUnhealthyOnStart, unhealthy)
	}

	// Launch the finality-based event-polling loop. The listener is built here (not in New) because
	// it needs the addrToNetworkID routing table, which is only populated by buildInitialCache above.
	// It runs in the background until ctx is done and shuts down cleanly on ctx.Done().
	lst, err := newListener(
		f.logger, f.logFilterer, f.healthChecker, f.resolver, f.cache, f.addrToNetworkID, f.cfg)
	if err != nil {
		return fmt.Errorf("failed to build event listener: %w", err)
	}

	go lst.run(ctx)

	return nil
}

// buildInitialCache enumerates rollups 1..RollupCount(), resolves each network's URL and installs a
// cache entry (source-tagged, healthy defaults to false and is set by probeAll). Config-only
// networks (e.g. network 0 / L1) present in Config.URLs are also installed. Networks with no source
// are skipped.
func (f *finder) buildInitialCache(ctx context.Context) error {
	// Seed config-only entries first (including network 0 / L1) so they are served even if they are
	// not among the enumerated rollups. Enumeration will overwrite an entry only via SourceConfig
	// again (identical), so ordering is harmless.
	for networkID, url := range f.cfg.URLs {
		if url == "" {
			continue
		}

		f.cache.set(networkID, cacheEntry{url: url, source: SourceConfig})
	}

	count, err := f.rollupManager.RollupCount(&bind.CallOpts{Context: ctx})
	if err != nil {
		return fmt.Errorf("failed to read rollup count: %w", err)
	}

	f.logger.Infof("enumerating %d rollups from rollup manager %s", count, f.cfg.RollupManagerAddr)

	for rollupID := uint32(1); rollupID <= count; rollupID++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		f.resolveNetwork(ctx, rollupID)
	}

	return nil
}

// resolveNetwork resolves a single rollupID (== networkID), building its contract reader, running
// the priority resolver and installing the resulting cache entry. Fall-through / no-source cases are
// logged and skipped; the network is left without an entry.
func (f *finder) resolveNetwork(ctx context.Context, rollupID uint32) {
	// Config override short-circuits everything and needs no on-chain read.
	if url, ok := f.cfg.URLs[rollupID]; ok && url != "" {
		f.cache.set(rollupID, cacheEntry{url: url, source: SourceConfig})
		f.logger.Debugf("network %d resolved from config: %s", rollupID, url)

		return
	}

	data, err := f.rollupManager.RollupIDToRollupData(&bind.CallOpts{Context: ctx}, rollupID)
	if err != nil {
		f.logger.Warnf("failed to read rollup data for network %d, skipping: %v", rollupID, err)
		return
	}

	addr := data.RollupContract
	f.addrToNetworkID[addr] = rollupID

	reader, err := f.readerFactory(addr, f.ethClient)
	if err != nil {
		f.logger.Warnf("failed to build contract reader for network %d (%s), skipping: %v", rollupID, addr, err)
		return
	}

	url, source, err := f.resolver.resolve(ctx, rollupID, reader)
	if err != nil {
		if errors.Is(err, ErrNoSourceAvailable) {
			f.logger.Warnf("no bridge service url source available for network %d (%s), skipping", rollupID, addr)
		} else {
			f.logger.Warnf("failed to resolve bridge service url for network %d (%s), skipping: %v", rollupID, addr, err)
		}

		return
	}

	f.cache.set(rollupID, cacheEntry{url: url, source: source})
	f.logger.Infof("network %d resolved bridge service url %s (source=%d)", rollupID, url, source)
}

// probeAll runs a /health probe against every cached entry, updating each entry's healthy flag, and
// returns the number of entries that were unreachable.
func (f *finder) probeAll(ctx context.Context) int {
	unhealthy := 0

	f.cache.mu.Lock()
	for networkID, entry := range f.cache.entries {
		healthy := f.healthChecker.IsHealthy(ctx, entry.url)
		entry.healthy = healthy
		f.cache.entries[networkID] = entry

		if !healthy {
			unhealthy++
			f.logger.Warnf("bridge service for network %d at %s is unreachable at start", networkID, entry.url)
		}
	}
	f.cache.mu.Unlock()

	return unhealthy
}

// GetURL returns the currently cached bridge service URL for networkID, or ErrURLNotFound if none is
// cached. It reads under the cache read lock so it is safe to call concurrently.
func (f *finder) GetURL(networkID uint32) (string, error) {
	entry, ok := f.cache.get(networkID)
	if !ok {
		return "", fmt.Errorf("%w: network %d", ErrURLNotFound, networkID)
	}

	return entry.url, nil
}
