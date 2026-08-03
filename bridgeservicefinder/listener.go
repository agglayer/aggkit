package bridgeservicefinder

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainbase"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonrollupbaseetrog"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// bridgeServiceURLKeyTopic is keccak256("BRIDGE_SERVICE_URL"), the indexed topic of an
// AggchainMetadataSet event whose key is the bridge service URL entry. An AggchainMetadataSet log is
// only relevant when its indexed key topic equals this value (see the design note in doc.go: the
// binding stores the indexed string key pre-hashed as a common.Hash, so matching is a direct topic
// comparison, no hashing of the incoming value is required).
var bridgeServiceURLKeyTopic = crypto.Keccak256Hash([]byte(MetadataBridgeServiceURLKey))

// listener is the self-contained, finality-bounded event poller. It scans two kinds of events:
//
//   - Per-rollup URL events on the watched rollup/aggchain contracts: SetTrustedSequencerURL
//     (-> source #3) and AggchainMetadataSet (-> source #2). These drive health-gated,
//     priority-respecting cache updates for already-known networks.
//   - Rollup-manager lifecycle events on the rollup manager address: CreateNewRollup,
//     CreateNewAggchain and AddExistingRollup. These announce networks attached after Start, which
//     the listener then resolves and registers live so no restart is required to serve them.
//
// It deliberately avoids the heavy sync/reorgdetector/DB stack and websocket Watch* subscriptions:
// it is a single goroutine driven by a ticker calling eth_getLogs (FilterLogs) over a chunked block
// range bounded by Config.BlockFinality.
type listener struct {
	logger        aggkitcommon.Logger
	logFilterer   LogFilterer
	healthChecker HealthChecker
	resolver      *resolver
	cache         *cache

	// rollupManagerAddr is the rollup manager contract address. It is watched (alongside the rollup
	// contracts) so newly attached rollups can be discovered live, and it is how processLog tells a
	// rollup-manager lifecycle log apart from a per-rollup URL log.
	rollupManagerAddr common.Address
	// readerFactory / ethClient build a RollupContractReader for a rollup discovered after Start, so
	// its URL can be resolved exactly like the initial-cache networks were.
	readerFactory RollupContractReaderFactory
	ethClient     aggkittypes.BaseEthereumClienter

	// addrToNetworkID routes an incoming log (keyed by the emitting contract address) back to the
	// networkID whose cache entry it may update. It is the table built by Start's enumeration and is
	// extended in place when a new rollup is discovered. It is only ever mutated on the listener
	// goroutine (after Start hands it over), so no extra locking is required.
	addrToNetworkID map[common.Address]uint32
	// watchedAddresses is the slice used as the FilterLogs address filter: the rollup manager address
	// plus every known rollup contract. It is appended to when a rollup is discovered live and, like
	// addrToNetworkID, is only mutated on the listener goroutine.
	watchedAddresses []common.Address

	blockFinality  aggkittypes.BlockNumberFinality
	pollInterval   time.Duration
	blockChunkSize uint64

	// topics are the event signature hashes (topic0) the scan filters on: the two per-rollup URL
	// events plus the three rollup-manager lifecycle events.
	topics []common.Hash
	// createNewRollupTopic / createNewAggchainTopic / addExistingRollupTopic are the topic0 hashes of
	// the rollup-manager lifecycle events, kept for routing in processRollupManagerLog.
	createNewRollupTopic   common.Hash
	createNewAggchainTopic common.Hash
	addExistingRollupTopic common.Hash
	// aggchainFilterer / rollupFilterer / mgrFilterer decode matched logs. They are bound to the zero
	// address; the go-ethereum Parse* helpers only use the ABI (not the bound address or backend) to
	// unpack a log, so a single filterer instance decodes logs from every watched contract.
	aggchainFilterer *aggchainbase.AggchainbaseFilterer
	rollupFilterer   *polygonrollupbaseetrog.PolygonrollupbaseetrogFilterer
	mgrFilterer      *agglayermanager.AgglayermanagerFilterer

	// lastScannedBlock is the highest block already processed. It is tracked in-memory only (no DB):
	// it is seeded, at construction time, to the finalized upper bound resolved right then, so the
	// first tick's scan covers everything from that instant onward. Events emitted before it (i.e.
	// before or during buildInitialCache) were already reflected in the cache by the direct on-chain
	// reads performed while building the initial cache. See newListener for the seeding.
	lastScannedBlock uint64
}

// newListener builds the event listener from the finder's already-resolved dependencies. It computes
// the watched-address slice and the event topics up front, binds the decoding filterers, and seeds
// lastScannedBlock to the finalized upper bound resolved right now (ctx). Seeding here — rather than
// on the listener's first tick, pollInterval later — closes the gap between it and the direct
// on-chain reads buildInitialCache just performed: any URL update or rollup-creation event emitted
// in that window would otherwise never be scanned, since the first tick would seed straight to its
// own (later) upper bound without reading any logs.
func newListener(
	ctx context.Context,
	logger aggkitcommon.Logger,
	logFilterer LogFilterer,
	healthChecker HealthChecker,
	res *resolver,
	c *cache,
	addrToNetworkID map[common.Address]uint32,
	rollupManagerAddr common.Address,
	readerFactory RollupContractReaderFactory,
	ethClient aggkittypes.BaseEthereumClienter,
	cfg Config,
) (*listener, error) {
	aggchainABI, err := aggchainbase.AggchainbaseMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("failed to load aggchainbase ABI: %w", err)
	}

	rollupABI, err := polygonrollupbaseetrog.PolygonrollupbaseetrogMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("failed to load polygonrollupbaseetrog ABI: %w", err)
	}

	mgrABI, err := agglayermanager.AgglayermanagerMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("failed to load agglayermanager ABI: %w", err)
	}

	// topic0 for the two per-rollup URL events. Both the aggchainbase and polygonrollupbaseetrog ABIs
	// produce the identical SetTrustedSequencerURL signature hash, so a single topic covers both.
	metadataTopic := aggchainABI.Events["AggchainMetadataSet"].ID
	seqURLTopic := rollupABI.Events["SetTrustedSequencerURL"].ID

	// topic0 for the three rollup-manager lifecycle events that announce a newly attached rollup.
	createNewRollupTopic := mgrABI.Events["CreateNewRollup"].ID
	createNewAggchainTopic := mgrABI.Events["CreateNewAggchain"].ID
	addExistingRollupTopic := mgrABI.Events["AddExistingRollup"].ID

	aggchainFilterer, err := aggchainbase.NewAggchainbaseFilterer(common.Address{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build aggchainbase filterer: %w", err)
	}

	rollupFilterer, err := polygonrollupbaseetrog.NewPolygonrollupbaseetrogFilterer(common.Address{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build polygonrollupbaseetrog filterer: %w", err)
	}

	mgrFilterer, err := agglayermanager.NewAgglayermanagerFilterer(common.Address{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build agglayermanager filterer: %w", err)
	}

	// Watch the rollup manager (for lifecycle events) plus every known rollup contract (for URL
	// events). The manager address is always present so the first rollups can be discovered even when
	// the initial enumeration found none.
	watched := make([]common.Address, 0, len(addrToNetworkID)+1)
	watched = append(watched, rollupManagerAddr)
	for addr := range addrToNetworkID {
		watched = append(watched, addr)
	}

	l := &listener{
		logger:                 logger,
		logFilterer:            logFilterer,
		healthChecker:          healthChecker,
		resolver:               res,
		cache:                  c,
		rollupManagerAddr:      rollupManagerAddr,
		readerFactory:          readerFactory,
		ethClient:              ethClient,
		addrToNetworkID:        addrToNetworkID,
		watchedAddresses:       watched,
		blockFinality:          cfg.BlockFinality,
		pollInterval:           cfg.PollInterval.Duration,
		blockChunkSize:         cfg.BlockChunkSize,
		topics:                 []common.Hash{metadataTopic, seqURLTopic, createNewRollupTopic, createNewAggchainTopic, addExistingRollupTopic}, //nolint:lll
		createNewRollupTopic:   createNewRollupTopic,
		createNewAggchainTopic: createNewAggchainTopic,
		addExistingRollupTopic: addExistingRollupTopic,
		aggchainFilterer:       aggchainFilterer,
		rollupFilterer:         rollupFilterer,
		mgrFilterer:            mgrFilterer,
	}

	upper, err := l.finalizedUpperBound(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve initial finalized upper bound: %w", err)
	}
	l.lastScannedBlock = upper

	return l, nil
}

// run is the polling loop. It ticks every pollInterval, resolves the finalized upper block bound,
// scans the unprocessed range for the watched events and applies updates. It returns when ctx is
// cancelled. If there are no watched addresses there is nothing to scan, so it exits immediately.
func (l *listener) run(ctx context.Context) {
	if len(l.watchedAddresses) == 0 {
		l.logger.Info("no watched rollup contracts, event listener will not run")
		return
	}

	l.logger.Infof("starting bridge service event listener: watching %d contracts, poll interval %s, finality %s",
		len(l.watchedAddresses), l.pollInterval, l.blockFinality.String())

	ticker := time.NewTicker(l.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			l.logger.Info("bridge service event listener stopped")
			return
		case <-ticker.C:
			if err := l.scanOnce(ctx); err != nil {
				l.logger.Warnf("bridge service event scan failed: %v", err)
			}
		}
	}
}

// scanOnce resolves the current finalized upper bound and processes the block range
// (lastScannedBlock+1 .. upper] in chunks of blockChunkSize. lastScannedBlock starts out seeded
// (see newListener) to the finalized upper bound resolved at construction time, so even the first
// tick scans everything finalized since then.
func (l *listener) scanOnce(ctx context.Context) error {
	upper, err := l.finalizedUpperBound(ctx)
	if err != nil {
		return fmt.Errorf("failed to resolve finalized upper block for finality %s: %w", l.blockFinality.String(), err)
	}

	if upper <= l.lastScannedBlock {
		// No new finalized blocks since the last scan.
		return nil
	}

	from := l.lastScannedBlock + 1
	for from <= upper {
		to := from + l.blockChunkSize - 1
		if to > upper {
			to = upper
		}

		if err := l.scanRange(ctx, from, to); err != nil {
			// Leave lastScannedBlock where it is so the failed range is retried next tick.
			return fmt.Errorf("failed to scan blocks %d..%d: %w", from, to, err)
		}

		l.lastScannedBlock = to
		from = to + 1
	}

	return nil
}

// finalizedUpperBound resolves Config.BlockFinality (a tag such as FinalizedBlock, with any offset)
// to a concrete block number via CustomHeaderByNumber. A constant/specific finality resolves to its
// literal value without an RPC call.
func (l *listener) finalizedUpperBound(ctx context.Context) (uint64, error) {
	if l.blockFinality.IsConstant() {
		return l.blockFinality.Specific, nil
	}

	header, err := l.logFilterer.CustomHeaderByNumber(ctx, &l.blockFinality)
	if err != nil {
		return 0, fmt.Errorf("resolve header for %s: %w", l.blockFinality.String(), err)
	}

	return header.Number, nil
}

// scanRange fetches and processes the watched-event logs in [from, to] across all watched addresses.
func (l *listener) scanRange(ctx context.Context, from, to uint64) error {
	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(to),
		Addresses: l.watchedAddresses,
		// A single-element outer slice with both topic0 hashes matches logs whose first topic is
		// either of the two watched event signatures (topic0 OR semantics).
		Topics: [][]common.Hash{l.topics},
	}

	logs, err := l.logFilterer.FilterLogs(ctx, query)
	if err != nil {
		return fmt.Errorf("filter logs: %w", err)
	}

	for i := range logs {
		l.processLog(ctx, logs[i])
	}

	return nil
}

// processLog decodes a single matched log and dispatches it. A log emitted by the rollup manager is
// routed to rollup discovery; a log emitted by a watched rollup contract is turned into a candidate
// URL and applied to that network's cache entry. Unrecognised or irrelevant logs are ignored.
func (l *listener) processLog(ctx context.Context, lg types.Log) {
	if len(lg.Topics) == 0 {
		return
	}

	if lg.Address == l.rollupManagerAddr {
		l.processRollupManagerLog(ctx, lg)
		return
	}

	networkID, ok := l.addrToNetworkID[lg.Address]
	if !ok {
		// A log from an address we do not route (should not happen given the address filter).
		return
	}

	if lg.Topics[0] == l.topics[0] && l.isMetadataServiceURLCleared(networkID, lg) {
		l.refreshFromChain(ctx, networkID, lg.Address)
		return
	}

	candidateURL, jsonRPCURL, source, ok := l.decodeLog(networkID, lg)
	if !ok {
		return
	}

	l.applyUpdate(ctx, networkID, candidateURL, jsonRPCURL, source, lg)
}

// isMetadataServiceURLCleared reports whether lg is an AggchainMetadataSet log for the
// BRIDGE_SERVICE_URL key carrying an empty value — i.e. the operator cleared it on-chain.
// decodeLog folds this case into an ignored no-op (no usable candidate), but it must not be
// silently dropped: with the stale metadata-sourced entry still cached, metadata continuing to
// outrank sequencer-derived updates would leave the finder stuck on it forever. Callers route this
// case to refreshFromChain instead, which re-resolves the network from scratch.
func (l *listener) isMetadataServiceURLCleared(networkID uint32, lg types.Log) bool {
	ev, err := l.aggchainFilterer.ParseAggchainMetadataSet(lg)
	if err != nil {
		l.logger.Debugf("failed to parse AggchainMetadataSet log for network %d: %v", networkID, err)
		return false
	}

	return ev.Key == bridgeServiceURLKeyTopic && ev.Value == ""
}

// refreshFromChain re-resolves networkID's URLs directly on-chain (the same source-priority
// algorithm as the initial cache build), applying the result under the health-gating rule only —
// not the source-priority rule, since this is a full re-evaluation meant to let a lower-priority
// source take over once a higher-priority one (e.g. aggchain metadata) is cleared on-chain.
// Failures are logged and the stale entry, if any, is left in place so a later event can still
// refresh it.
func (l *listener) refreshFromChain(ctx context.Context, networkID uint32, addr common.Address) {
	reader, err := l.readerFactory(addr, l.ethClient)
	if err != nil {
		l.logger.Warnf("network %d: failed to build contract reader to refresh after metadata clear: %v", networkID, err)
		return
	}

	urls, source, err := l.resolver.resolve(ctx, networkID, reader)
	if err != nil {
		if errors.Is(err, ErrNoSourceAvailable) {
			l.logger.Infof(
				"network %d: bridge service metadata cleared and no other source available; keeping last known url",
				networkID)
		} else {
			l.logger.Warnf("network %d: failed to re-resolve bridge service url after metadata clear: %v", networkID, err)
		}

		return
	}

	cur, exists := l.cache.get(networkID)
	if exists && cur.url == urls.BridgeURL && cur.source == source {
		return
	}

	healthy := l.healthChecker.IsHealthy(ctx, urls.BridgeURL)
	if exists && cur.healthy && !healthy {
		l.logger.Debugf("network %d: keeping current healthy url %s over unhealthy fallback %s",
			networkID, cur.url, urls.BridgeURL)
		return
	}

	l.cache.set(networkID, cacheEntry{url: urls.BridgeURL, jsonRPCURL: urls.JSONRPCURL, source: source, healthy: healthy})
	l.logger.Infof("network %d bridge service url refreshed to %s after metadata clear (source=%d, healthy=%t)",
		networkID, urls.BridgeURL, source, healthy)
}

// processRollupManagerLog decodes a rollup-manager lifecycle log announcing a newly attached rollup
// (CreateNewRollup / CreateNewAggchain / AddExistingRollup) and hands its rollupID + contract address
// to discoverRollup. All three events carry the rollupID and the rollup contract address directly, so
// no follow-up RollupIDToRollupData call is needed. Any other rollup-manager event is ignored.
func (l *listener) processRollupManagerLog(ctx context.Context, lg types.Log) {
	var (
		rollupID uint32
		addr     common.Address
	)

	switch lg.Topics[0] {
	case l.createNewRollupTopic:
		ev, err := l.mgrFilterer.ParseCreateNewRollup(lg)
		if err != nil {
			l.logger.Debugf("failed to parse CreateNewRollup log: %v", err)
			return
		}

		rollupID, addr = ev.RollupID, ev.RollupAddress

	case l.createNewAggchainTopic:
		ev, err := l.mgrFilterer.ParseCreateNewAggchain(lg)
		if err != nil {
			l.logger.Debugf("failed to parse CreateNewAggchain log: %v", err)
			return
		}

		rollupID, addr = ev.RollupID, ev.RollupAddress

	case l.addExistingRollupTopic:
		ev, err := l.mgrFilterer.ParseAddExistingRollup(lg)
		if err != nil {
			l.logger.Debugf("failed to parse AddExistingRollup log: %v", err)
			return
		}

		rollupID, addr = ev.RollupID, ev.RollupAddress

	default:
		return
	}

	l.discoverRollup(ctx, rollupID, addr)
}

// discoverRollup registers a rollup that was attached to the rollup manager after Start. It resolves
// the rollup's bridge service URL (same priority rules as the initial cache build), installs a cache
// entry and adds the rollup contract to the watched set so its later URL-changing events are picked
// up. It is a no-op if the rollup contract is already watched.
//
// Unlike the initial cache build (which aborts Start on a hard error), discovery runs on the polling
// goroutine and must not tear it down, so failures are logged rather than propagated. The address is
// still registered on failure so a subsequent URL event can populate the entry.
func (l *listener) discoverRollup(ctx context.Context, rollupID uint32, addr common.Address) {
	if _, known := l.addrToNetworkID[addr]; known {
		return
	}

	reader, err := l.readerFactory(addr, l.ethClient)
	if err != nil {
		l.logger.Warnf("discovered network %d (%s): failed to build contract reader: %v", rollupID, addr, err)
		return
	}

	urls, source, err := l.resolver.resolve(ctx, rollupID, reader)
	if err != nil {
		// Register the address regardless so a later URL-changing event can still populate the entry.
		l.addrToNetworkID[addr] = rollupID
		l.watchedAddresses = append(l.watchedAddresses, addr)

		if errors.Is(err, ErrNoSourceAvailable) {
			l.logger.Infof(
				"discovered network %d (%s) with no bridge service url source yet; watching for updates",
				rollupID, addr)
		} else {
			l.logger.Warnf("discovered network %d (%s): failed to resolve bridge service url: %v", rollupID, addr, err)
		}

		return
	}

	healthy := l.healthChecker.IsHealthy(ctx, urls.BridgeURL)

	l.addrToNetworkID[addr] = rollupID
	l.watchedAddresses = append(l.watchedAddresses, addr)
	l.cache.set(rollupID, cacheEntry{
		url: urls.BridgeURL, jsonRPCURL: urls.JSONRPCURL, source: source, healthy: healthy})

	l.logger.Infof("discovered network %d bridge service url %s, json-rpc url %s (source=%d, healthy=%t)",
		rollupID, urls.BridgeURL, urls.JSONRPCURL, source, healthy)
}

// decodeLog turns a matched log into a candidate (bridge url, json-rpc url, source). It returns
// ok=false when the log is not relevant (unknown topic, wrong metadata key, empty/unparsable value,
// or a port-substitution failure). For SetTrustedSequencerURL the bridge candidate has its port
// substituted with DefaultBridgeServicePort while the json-rpc url is the announced URL verbatim;
// an AggchainMetadataSet log carries no json-rpc information (empty).
func (l *listener) decodeLog(networkID uint32, lg types.Log) (string, string, Source, bool) {
	switch lg.Topics[0] {
	case l.topics[0]: // AggchainMetadataSet -> source #2 (metadata)
		ev, err := l.aggchainFilterer.ParseAggchainMetadataSet(lg)
		if err != nil {
			l.logger.Debugf("failed to parse AggchainMetadataSet log for network %d: %v", networkID, err)
			return "", "", SourceMetadata, false
		}

		// The indexed string key arrives pre-hashed; only the BRIDGE_SERVICE_URL key is relevant.
		if ev.Key != bridgeServiceURLKeyTopic {
			return "", "", SourceMetadata, false
		}

		if ev.Value == "" {
			l.logger.Debugf("ignoring empty BRIDGE_SERVICE_URL metadata for network %d", networkID)
			return "", "", SourceMetadata, false
		}

		return ev.Value, "", SourceMetadata, true

	case l.topics[1]: // SetTrustedSequencerURL -> source #3 (sequencer URL + port) and json-rpc url
		ev, err := l.rollupFilterer.ParseSetTrustedSequencerURL(lg)
		if err != nil {
			l.logger.Debugf("failed to parse SetTrustedSequencerURL log for network %d: %v", networkID, err)
			return "", "", SourceSequencerURL, false
		}

		if ev.NewTrustedSequencerURL == "" {
			return "", "", SourceSequencerURL, false
		}

		url, err := withPort(ev.NewTrustedSequencerURL, l.resolver.bridgeServicePort)
		if err != nil {
			l.logger.Warnf("failed to substitute port in sequencer url %q for network %d: %v",
				ev.NewTrustedSequencerURL, networkID, err)
			return "", "", SourceSequencerURL, false
		}

		return url, ev.NewTrustedSequencerURL, SourceSequencerURL, true

	default:
		return "", "", SourceSequencerURL, false
	}
}

// applyUpdate applies the strict priority rules and the health-gating rule (both spelled out in
// doc.go) for a candidate (url, source) targeting networkID. jsonRPCURL is the network's JSON-RPC
// endpoint carried by a SetTrustedSequencerURL event (empty for metadata events); it is refreshed
// on the current entry up front, exempt from the rules below, because it is independent of which
// source serves the bridge URL and has no /health semantics. The one restriction is a Config.RPCURLs
// override, which is terminal and makes the event's json-rpc payload ignored entirely.
//
// Priority (strict, using the current entry's recorded source):
//   - SourceConfig entries are terminal: never overwritten by any event.
//   - A SourceMetadata event may overwrite a current SourceSequencerURL or SourceMetadata entry.
//   - A SourceSequencerURL event must NOT overwrite a current SourceMetadata entry (metadata
//     outranks sequencer-derived). It may overwrite a current SourceSequencerURL entry (refresh) or
//     install where no entry exists.
//
// Health-gating (applied only once the priority rules allow the update):
//   - No prior entry: install the candidate and record its probe result.
//   - Prior entry healthy: replace only if the candidate probes healthy; otherwise keep the
//     current (healthy) URL.
//   - Prior entry unhealthy: replace regardless of the candidate's probe result (avoid getting
//     stuck on a dead URL), recording the candidate's probe result.
func (l *listener) applyUpdate(
	ctx context.Context, networkID uint32, candidateURL, jsonRPCURL string, source Source, lg types.Log,
) {
	// A config-overridden JSON-RPC endpoint is terminal, exactly like a config-sourced bridge URL:
	// drop the event's json-rpc payload so it can neither refresh nor be installed below.
	if l.resolver.configRPCURL(networkID) != "" {
		jsonRPCURL = ""
	}

	cur, exists := l.cache.get(networkID)

	if exists && jsonRPCURL != "" && cur.jsonRPCURL != jsonRPCURL {
		cur.jsonRPCURL = jsonRPCURL
		l.cache.set(networkID, cur)
		l.logger.Infof("network %d json-rpc url updated to %s (block=%d)", networkID, jsonRPCURL, lg.BlockNumber)
	}

	if exists {
		// SourceConfig is terminal.
		if cur.source == SourceConfig {
			l.logger.Debugf("ignoring %s event for network %d: current entry is config-sourced (immutable)",
				eventName(source), networkID)
			return
		}

		// A lower-priority (higher Source value) event cannot overwrite a higher-priority entry.
		// Concretely: a SourceSequencerURL event cannot displace a SourceMetadata entry.
		if source > cur.source {
			l.logger.Debugf("ignoring %s event for network %d: outranked by current source %d",
				eventName(source), networkID, cur.source)
			return
		}
	}

	// If the candidate URL is identical to the cached one AND the source is unchanged, there is
	// nothing to do (avoids a redundant health probe on duplicate events).
	if exists && cur.url == candidateURL && cur.source == source {
		return
	}

	newHealthy := l.healthChecker.IsHealthy(ctx, candidateURL)

	if exists && cur.healthy && !newHealthy {
		// The current URL is reachable; do not displace it with an unreachable candidate.
		l.logger.Debugf(
			"rejecting %s update for network %d: candidate %s is unreachable and current %s is healthy",
			eventName(source), networkID, candidateURL, cur.url)
		return
	}

	entry := cacheEntry{url: candidateURL, jsonRPCURL: cur.jsonRPCURL, source: source, healthy: newHealthy}
	if jsonRPCURL != "" {
		entry.jsonRPCURL = jsonRPCURL
	}

	l.cache.set(networkID, entry)
	l.logger.Infof("network %d bridge service url updated to %s via %s event (source=%d, healthy=%t, block=%d)",
		networkID, candidateURL, eventName(source), source, newHealthy, lg.BlockNumber)
}

// eventName returns a human-readable name for the source a live event maps to, for logging.
func eventName(source Source) string {
	switch source {
	case SourceMetadata:
		return "AggchainMetadataSet"
	case SourceSequencerURL:
		return "SetTrustedSequencerURL"
	case SourceConfig:
		return "config"
	default:
		return "unknown"
	}
}
