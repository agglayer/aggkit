// Package bridgeservicefinder resolves and serves the bridge service REST URL and the JSON-RPC
// endpoint for every network attached to a given rollup manager.
//
// # Overview
//
// The finder builds and maintains an in-memory cache mapping networkID -> network URLs (the bridge
// service REST URL plus the network's JSON-RPC endpoint). On Start it enumerates every rollup
// attached to the configured rollup manager, resolves the URLs for each, asserts each resolved
// bridge service is reachable (via its /health endpoint), and then keeps the cache fresh by
// polling on-chain events. GetURL returns the currently cached NetworkURLs for a given networkID.
//
// The event poller watches both per-rollup URL changes and rollup-manager lifecycle events, so a
// rollup attached to the manager AFTER Start is discovered and served live, without a restart (see
// "Dynamic rollup discovery" below).
//
// This package is intentionally lightweight. It does NOT depend on the heavy sync /
// reorgdetector / DB stack, and it does NOT rely on websocket Watch* subscriptions. Event
// listening is a self-contained polling loop built on eth_getLogs (FilterLogs) over a
// configurable, finality-bounded block window.
//
// # NetworkID / RollupID mapping (CONFIRMED)
//
// In the agglayer/CDK data model a network is identified by a uint32 "networkID":
//
//   - networkID == 0 is the L1 / mainnet network. It is NOT a rollup and is NOT enumerated by
//     the rollup manager. The rollup manager only knows about rollups 1..N. L1 (network 0) has
//     no bridge service resolvable from the rollup manager. If GetURL is asked about network 0,
//     the finder returns the config values if provided (Config.BridgeURLs / Config.RPCURLs),
//     otherwise a not-found error. network 0 is out of scope for on-chain enumeration and event
//     watching.
//   - For attached rollups, networkID == rollupID. Rollups are numbered 1..RollupCount(). This
//     matches how the repo already treats the identifier: etherman/querier/rollup_data_querier.go
//     obtains the rollup's id via RollupAddressToID and feeds that same id into
//     RollupIDToRollupData; l1infotreesync and bridgeservice interfaces likewise use the terms
//     rollupID and networkID interchangeably for L2 networks (see GetRollupExitTreeMerkleProof,
//     GetLastVerifiedBatches, etc.). ErrInvalidRollupID guards the id==0 case precisely because
//     0 is reserved for L1, not a rollup.
//
// Therefore enumeration iterates rollupID = 1 .. RollupCount() inclusive, and each rollupID is
// used directly as the networkID key in the cache.
//
// # Sources and priority (STRICT)
//
// A URL for a network is resolved from three sources, in strict descending priority:
//
//  1. Config      - a user-provided static map networkID -> url (Config.BridgeURLs). Highest
//     priority. A config-sourced entry is NEVER overridden by an on-chain event.
//  2. On-chain     - the aggchain contract's aggchainMetadata(string) mapping, read with key
//     metadata     "BRIDGE_SERVICE_URL". Present only on aggchain-type rollups. Outranks #3.
//  3. Sequencer    - the rollup contract's trustedSequencerURL(), with its port replaced by the
//     URL+port    hardcoded default bridge REST port (DefaultBridgeServicePort = 5577). Lowest
//     priority. This is the universal fallback available on legacy rollups too.
//
// Resolution algorithm for a single network (resolver.go). The trusted sequencer URL is read up
// front because it serves double duty: verbatim it is the network's JSON-RPC endpoint, and with
// the bridge port substituted it is bridge source #3:
//
//	rpc := config.RPCURLs[networkID] // "" if absent; non-empty is terminal
//	if url, ok := config.BridgeURLs[networkID]; ok {
//	    return {url, rpc or bestEffort(seqURL)}, SourceConfig
//	}
//	seqURL := reader.TrustedSequencerURL(ctx) // ErrSourceNotAvailable -> "", hard error aborts
//	if url, err := reader.AggchainMetadata(ctx, "BRIDGE_SERVICE_URL"); err == nil && url != "" {
//	    return {url, rpc or seqURL}, SourceMetadata
//	}   // a revert / "method not found" / empty value falls through, it is NOT a hard error
//	if seqURL != "" {
//	    return {withPort(seqURL, DefaultBridgeServicePort), rpc or seqURL}, SourceSequencerURL
//	}
//	return {}, ErrNoSourceAvailable
//
// # JSON-RPC endpoint (independent of the bridge priority)
//
// Alongside the bridge service URL, each cache entry carries the network's JSON-RPC endpoint,
// resolved config-first with the same override semantics as the bridge URL:
//
//  1. Config.RPCURLs[networkID], if present. Served verbatim and terminal: never refreshed or
//     overwritten by on-chain events. This is also the only way to provide a JSON-RPC endpoint for
//     network 0 (L1). A network with BOTH config overrides skips on-chain inspection entirely.
//  2. Otherwise the rollup's trustedSequencerURL() served VERBATIM (no port substitution).
//
// The on-chain-sourced endpoint is informational and orthogonal to the bridge-URL rules:
//
//   - It is populated regardless of which source produced the bridge URL (config included: a
//     bridge-only-overridden network is still inspected on-chain, best-effort, to fill it in). It
//     stays empty when the read is unavailable (network 0 / L1, method absent, RPC failure on a
//     config-covered network).
//   - A live SetTrustedSequencerURL event always refreshes it (unless config-overridden) - even on
//     a config-sourced bridge entry and even when the event's bridge-URL candidate is rejected by
//     the priority or health-gating rules. It is not subject to /health probing (the health check
//     targets the bridge service).
//
// Fallthrough (graceful degradation): different rollup types expose different methods. Legacy
// (polygonrollupbaseetrog) rollups have trustedSequencerURL() but NOT aggchainMetadata(); calling
// a method that does not exist, or a call that reverts, must be treated as "source not available"
// and fall through to the next source rather than aborting resolution. The contract-reader
// interface surfaces this by returning a distinguished error (ErrSourceNotAvailable) so the
// resolver can tell "not supported / reverted" apart from a genuine transport/RPC failure.
//
// # Cache and per-entry source tracking (cache.go)
//
// The cache stores, per networkID, the URLs and the Source that produced the bridge URL:
//
//	type cacheEntry struct {
//	    url        string
//	    jsonRPCURL string // config override or raw trustedSequencerURL; exempt from the rules below
//	    source     Source // SourceConfig | SourceMetadata | SourceSequencerURL
//	    healthy    bool   // result of the most recent /health probe for this url
//	}
//
// Recording the source is what makes config entries immune to on-chain updates and enforces the
// metadata-over-sequencer precedence during live updates:
//
//   - SourceConfig entries are terminal: no event ever replaces them.
//   - A SetTrustedSequencerURL event (source #3) must NOT overwrite an entry currently sourced
//     from metadata (#2), because #2 outranks #3.
//   - An AggchainMetadataSet event (source #2) MAY overwrite an entry currently sourced from #3.
//   - The healthy flag is required to implement the health-gating rule below.
//
// The cache is guarded by a sync.RWMutex; GetURL takes a read lock, updates take a write lock.
//
// # Finality-based event polling (listener.go)
//
// The listener runs a single goroutine loop, ticking every Config.PollInterval:
//
//   - Determine the target upper bound = Config.BlockFinality resolved against the current chain
//     head (aggkittypes.BlockNumberFinality.BlockNumber). This bounds scanning to sufficiently
//     final blocks so we do not react to logs that may be reorged away.
//   - FilterLogs from lastScannedBlock+1 .. target for the two event topics we care about, across
//     the relevant contract addresses (all attached rollup contracts + aggchain contracts).
//     Iterating in chunks of Config.BlockChunkSize keeps individual eth_getLogs requests bounded.
//   - Decode each log using the real bindings' Parse* helpers and apply a health-gated update.
//   - Advance lastScannedBlock to target.
//
// Watched events (topic0 verified against the real bindings):
//
//   - SetTrustedSequencerURL(string newTrustedSequencerURL) on the rollup contract
//     (polygonrollupbaseetrog and aggchainbase both emit it). Maps to source #3. The new URL has
//     its port replaced with DefaultBridgeServicePort before being considered.
//   - AggchainMetadataSet(string indexed key, string value) on the aggchain contract. Maps to
//     source #2. Because key is an indexed string, its topic is keccak256(key); we match
//     Key == keccak256("BRIDGE_SERVICE_URL") and use Value. In the Go binding the decoded event's
//     Key field is a common.Hash (the keccak of the string), and Value is the plaintext string.
//
// The finder maps each watched contract address back to a networkID (built at Start from the
// enumeration) so an incoming log can be routed to the correct cache entry.
//
// # Dynamic rollup discovery
//
// The initial enumeration is only a snapshot of the rollups attached at Start. To avoid requiring a
// restart when a new rollup is added later, the listener also watches the rollup manager address for
// its lifecycle events and registers the announced network live:
//
//   - CreateNewRollup, CreateNewAggchain, AddExistingRollup (on the rollup manager). Each carries the
//     new rollupID and the rollup contract address directly, so no follow-up RollupIDToRollupData
//     call is needed. On such an event the listener resolves the new network's URL (same three-source
//     priority as the initial build), installs a cache entry, and adds the rollup contract to the
//     watched-address set so its subsequent URL events are picked up too. A rollup that exposes no
//     source yet is still added to the watched set so a later URL event can populate it.
//
// The rollup manager address is always part of the watched-address filter (even when the initial
// enumeration found zero rollups), so the very first rollups can be discovered this way.
//
// # Error handling at Start (fail loudly vs graceful skip)
//
// During the initial cache build a per-network outcome is classified as either a hard failure or a
// benign skip:
//
//   - A hard failure - a genuine RPC/transport error reading a rollup's data, a reader-construction
//     error, or a genuine (non-fall-through) resolution error - is returned from Start. These mean
//     the network could not even be inspected (e.g. a broken L1 endpoint), which must not be
//     swallowed into silent partial coverage. Exception: a network covered by a config override
//     never aborts Start this way - its bridge URL is installed from config and only the JSON-RPC
//     endpoint is lost (logged as a warning), so config overrides keep working without on-chain
//     access.
//   - A benign "no source available" outcome (ErrNoSourceAvailable) is logged and skipped: the
//     network is left without a cache entry (GetURL returns ErrURLNotFound) but its address stays in
//     the routing table so a later on-chain URL event, or discovery, can still populate it.
//
// Live discovery, by contrast, runs on the background polling goroutine and never tears it down: its
// failures are logged, not propagated.
//
// # Health gating (EXACT RULE)
//
// /health probing is performed by an injectable HealthChecker (default: HTTP GET on
// Config.HealthCheckPath with Config.HealthCheckTimeout; healthy iff 2xx).
//
//   - At Start: after building the initial cache, each resolved URL is probed. A failed probe is
//     recorded (healthy=false) but does not abort startup by itself; whether Start returns an
//     error on unreachable services is governed by Config.RequireAllHealthyOnStart. Either way the
//     entry's healthy flag is set from the probe so live updates behave correctly.
//
//   - On a live event that yields a candidate new URL for a network (and that passes the source-
//     priority rules above), apply this replacement rule:
//
//     Let cur = the current cache entry, new = the candidate URL.
//
//   - If cur.healthy is true: replace ONLY IF the new URL probes healthy. If new is unhealthy,
//     keep the current (healthy) URL.
//
//   - If cur.healthy is false (the previously cached URL was itself unreachable): replace
//     regardless of whether new probes healthy. Record new's probe result as the new healthy
//     flag.
//
//   - If there was no prior entry: install new and record its probe result.
//
//     In short: a reachable cached URL is only displaced by another reachable URL; an unreachable
//     cached URL is always displaced by the newest candidate (which may or may not itself be
//     reachable).
//
// # Public interface
//
//	New(cfg Config, deps ...) (*Finder, error) // construct with config + injected dependencies
//	(f *Finder) Start(ctx context.Context) error // build initial cache, health-gate, start polling
//	(f *Finder) GetURL(networkID uint32) (NetworkURLs, error) // cached URLs or a not-found error
//
// See interfaces.go for the dependency interfaces (rollup-manager querier, per-rollup contract
// reader, FilterLogs-capable eth client, health checker) and config.go for the Config struct and
// its defaults.
//
// # Binding names verified against github.com/0xPolygon/cdk-contracts-tooling@v0.0.13
//
// (module path: github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/...)
//
//   - agglayermanager.Agglayermanager.RollupCount(opts) (uint32, error)
//   - agglayermanager.Agglayermanager.RollupIDToRollupData(opts, rollupID uint32)
//     (agglayermanager.AgglayerManagerRollupDataReturn, error)
//     -> struct field RollupContract common.Address, ChainID uint64 (the rollup contract address).
//   - agglayermanager rollup lifecycle events, each with a RollupID uint32 and RollupAddress
//     common.Address field; Parse helpers ParseCreateNewRollup / ParseCreateNewAggchain /
//     ParseAddExistingRollup. Used to discover rollups attached after Start:
//     CreateNewRollup(uint32 indexed rollupID, uint32 rollupTypeID, address rollupAddress, ...);
//     CreateNewAggchain(uint32 indexed rollupID, uint32 rollupTypeID, address rollupAddress, ...);
//     AddExistingRollup(uint32 indexed rollupID, uint64 forkID, address rollupAddress, ...).
//   - aggchainbase.Aggchainbase.AggchainMetadata(opts, key string) (string, error)
//     [Solidity: aggchainMetadata(string) view returns (string)]
//   - aggchainbase event AggchainMetadataSet(string indexed key, string value); binding struct
//     aggchainbase.AggchainbaseAggchainMetadataSet{Key common.Hash; Value string; Raw types.Log};
//     Filter/Parse: FilterAggchainMetadataSet, ParseAggchainMetadataSet.
//   - polygonrollupbaseetrog.Polygonrollupbaseetrog.TrustedSequencerURL(opts) (string, error)
//     [Solidity: trustedSequencerURL() view returns (string)]
//   - polygonrollupbaseetrog event SetTrustedSequencerURL(string newTrustedSequencerURL); binding
//     struct polygonrollupbaseetrog.PolygonrollupbaseetrogSetTrustedSequencerURL{
//     NewTrustedSequencerURL string; Raw types.Log}; Filter/Parse: FilterSetTrustedSequencerURL,
//     ParseSetTrustedSequencerURL. aggchainbase also emits SetTrustedSequencerURL identically.
//
// All names above match the plan's assumptions. No discrepancies were found. Note the exact
// capitalisation quirks the implementer must respect: the caller method is RollupIDToRollupData
// (upper-case ID), the returned struct type is agglayermanager.AgglayerManagerRollupDataReturn
// (mixed-case "AgglayerManager"), and the aggchain metadata getter is AggchainMetadata (the
// getter is method-named without the "Set").
package bridgeservicefinder
