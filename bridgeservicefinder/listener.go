package bridgeservicefinder

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainbase"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonrollupbaseetrog"
	"github.com/agglayer/aggkit/log"
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

// listener is the self-contained, finality-bounded event poller. It scans the two watched events
// (SetTrustedSequencerURL -> source #3, AggchainMetadataSet -> source #2) over the set of watched
// rollup/aggchain contract addresses and applies health-gated, priority-respecting cache updates.
//
// It deliberately avoids the heavy sync/reorgdetector/DB stack and websocket Watch* subscriptions:
// it is a single goroutine driven by a ticker calling eth_getLogs (FilterLogs) over a chunked block
// range bounded by Config.BlockFinality.
type listener struct {
	logger        *log.Logger
	logFilterer   LogFilterer
	healthChecker HealthChecker
	resolver      *resolver
	cache         *cache

	// addrToNetworkID routes an incoming log (keyed by the emitting contract address) back to the
	// networkID whose cache entry it may update. It is the table built by Start's enumeration.
	addrToNetworkID map[common.Address]uint32
	// watchedAddresses is the deterministic slice of addrToNetworkID keys used as the FilterLogs
	// address filter, computed once at construction.
	watchedAddresses []common.Address

	blockFinality  aggkittypes.BlockNumberFinality
	pollInterval   time.Duration
	blockChunkSize uint64

	// topics are the two event signature hashes (topic0) the scan filters on.
	topics []common.Hash
	// aggchainFilterer / rollupFilterer decode matched logs. They are bound to the zero address; the
	// go-ethereum Parse* helpers only use the ABI (not the bound address or backend) to unpack a log,
	// so a single filterer instance decodes logs from every watched contract.
	aggchainFilterer *aggchainbase.AggchainbaseFilterer
	rollupFilterer   *polygonrollupbaseetrog.PolygonrollupbaseetrogFilterer

	// lastScannedBlock is the highest block already processed. It is tracked in-memory only (no DB):
	// on the first tick it is seeded to the current finalized upper bound so the listener reacts to
	// events that happen after Start, not to historical ones (those were already read directly on-
	// chain when the initial cache was built). See run() for the seeding logic.
	lastScannedBlock uint64
	seeded           bool
}

// newListener builds the event listener from the finder's already-resolved dependencies. It computes
// the watched-address slice and the event topics up front and binds the decoding filterers.
func newListener(
	logger *log.Logger,
	logFilterer LogFilterer,
	healthChecker HealthChecker,
	res *resolver,
	c *cache,
	addrToNetworkID map[common.Address]uint32,
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

	// topic0 for the two watched events. Both the aggchainbase and polygonrollupbaseetrog ABIs
	// produce the identical SetTrustedSequencerURL signature hash, so a single topic covers both.
	metadataTopic := aggchainABI.Events["AggchainMetadataSet"].ID
	seqURLTopic := rollupABI.Events["SetTrustedSequencerURL"].ID

	aggchainFilterer, err := aggchainbase.NewAggchainbaseFilterer(common.Address{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build aggchainbase filterer: %w", err)
	}

	rollupFilterer, err := polygonrollupbaseetrog.NewPolygonrollupbaseetrogFilterer(common.Address{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build polygonrollupbaseetrog filterer: %w", err)
	}

	watched := make([]common.Address, 0, len(addrToNetworkID))
	for addr := range addrToNetworkID {
		watched = append(watched, addr)
	}

	return &listener{
		logger:           logger,
		logFilterer:      logFilterer,
		healthChecker:    healthChecker,
		resolver:         res,
		cache:            c,
		addrToNetworkID:  addrToNetworkID,
		watchedAddresses: watched,
		blockFinality:    cfg.BlockFinality,
		pollInterval:     cfg.PollInterval.Duration,
		blockChunkSize:   cfg.BlockChunkSize,
		topics:           []common.Hash{metadataTopic, seqURLTopic},
		aggchainFilterer: aggchainFilterer,
		rollupFilterer:   rollupFilterer,
	}, nil
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
// (lastScannedBlock+1 .. upper] in chunks of blockChunkSize.
//
// Seeding (no persistence): on the first invocation lastScannedBlock is set to the current upper
// bound and no logs are scanned. Historical events prior to Start were already reflected in the
// cache by the direct on-chain reads performed while building the initial cache, so replaying them
// would be redundant; the listener only needs to observe changes that happen after Start.
func (l *listener) scanOnce(ctx context.Context) error {
	upper, err := l.finalizedUpperBound(ctx)
	if err != nil {
		return fmt.Errorf("failed to resolve finalized upper block for finality %s: %w", l.blockFinality.String(), err)
	}

	if !l.seeded {
		l.lastScannedBlock = upper
		l.seeded = true
		l.logger.Debugf("event listener seeded at finalized block %d", upper)

		return nil
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

// processLog decodes a single matched log, extracts the candidate URL and source, routes it to a
// networkID and applies the priority + health-gated cache update. Unrecognised or irrelevant logs
// are ignored.
func (l *listener) processLog(ctx context.Context, lg types.Log) {
	networkID, ok := l.addrToNetworkID[lg.Address]
	if !ok {
		// A log from an address we do not route (should not happen given the address filter).
		return
	}

	if len(lg.Topics) == 0 {
		return
	}

	candidateURL, source, ok := l.decodeLog(networkID, lg)
	if !ok {
		return
	}

	l.applyUpdate(ctx, networkID, candidateURL, source, lg)
}

// decodeLog turns a matched log into a candidate (url, source). It returns ok=false when the log is
// not relevant (unknown topic, wrong metadata key, empty/unparsable value, or a port-substitution
// failure). For SetTrustedSequencerURL the port is substituted with DefaultBridgeServicePort.
func (l *listener) decodeLog(networkID uint32, lg types.Log) (string, Source, bool) {
	switch lg.Topics[0] {
	case l.topics[0]: // AggchainMetadataSet -> source #2 (metadata)
		ev, err := l.aggchainFilterer.ParseAggchainMetadataSet(lg)
		if err != nil {
			l.logger.Debugf("failed to parse AggchainMetadataSet log for network %d: %v", networkID, err)
			return "", SourceMetadata, false
		}

		// The indexed string key arrives pre-hashed; only the BRIDGE_SERVICE_URL key is relevant.
		if ev.Key != bridgeServiceURLKeyTopic {
			return "", SourceMetadata, false
		}

		if ev.Value == "" {
			l.logger.Debugf("ignoring empty BRIDGE_SERVICE_URL metadata for network %d", networkID)
			return "", SourceMetadata, false
		}

		return ev.Value, SourceMetadata, true

	case l.topics[1]: // SetTrustedSequencerURL -> source #3 (sequencer URL + port)
		ev, err := l.rollupFilterer.ParseSetTrustedSequencerURL(lg)
		if err != nil {
			l.logger.Debugf("failed to parse SetTrustedSequencerURL log for network %d: %v", networkID, err)
			return "", SourceSequencerURL, false
		}

		if ev.NewTrustedSequencerURL == "" {
			return "", SourceSequencerURL, false
		}

		url, err := withPort(ev.NewTrustedSequencerURL, l.resolver.bridgeServicePort)
		if err != nil {
			l.logger.Warnf("failed to substitute port in sequencer url %q for network %d: %v",
				ev.NewTrustedSequencerURL, networkID, err)
			return "", SourceSequencerURL, false
		}

		return url, SourceSequencerURL, true

	default:
		return "", SourceSequencerURL, false
	}
}

// applyUpdate applies the strict priority rules and the health-gating rule (both spelled out in
// doc.go) for a candidate (url, source) targeting networkID.
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
	ctx context.Context, networkID uint32, candidateURL string, source Source, lg types.Log,
) {
	cur, exists := l.cache.get(networkID)

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

	l.cache.set(networkID, cacheEntry{url: candidateURL, source: source, healthy: newHealthy})
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
