// Package bridgeservicefinder resolves and serves the bridge service REST URL for every
// network attached to a given rollup manager.
//
// # Overview
//
// The finder builds and maintains an in-memory cache mapping networkID -> bridge service URL.
// On Start it enumerates every rollup attached to the configured rollup manager, resolves a
// bridge service URL for each, asserts each resolved service is reachable (via its /health
// endpoint), and then keeps the cache fresh by polling on-chain events. GetURL returns the
// currently cached URL for a given networkID.
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
//     the finder returns the config value if one was provided for it, otherwise a not-found
//     error. network 0 is out of scope for on-chain enumeration and event watching.
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
//  1. Config      - a user-provided static map networkID -> url (Config.URLs). Highest priority.
//     A config-sourced entry is NEVER overridden by an on-chain event.
//  2. On-chain     - the aggchain contract's aggchainMetadata(string) mapping, read with key
//     metadata     "BRIDGE_SERVICE_URL". Present only on aggchain-type rollups. Outranks #3.
//  3. Sequencer    - the rollup contract's trustedSequencerURL(), with its port replaced by the
//     URL+port    hardcoded default bridge REST port (DefaultBridgeServicePort = 5577). Lowest
//     priority. This is the universal fallback available on legacy rollups too.
//
// Resolution algorithm for a single network (resolver.go):
//
//	if url, ok := config.URLs[networkID]; ok { return url, SourceConfig }
//	if url, err := reader.AggchainMetadata(ctx, "BRIDGE_SERVICE_URL"); err == nil && url != "" {
//	    return url, SourceMetadata
//	}   // a revert / "method not found" / empty value falls through, it is NOT a hard error
//	if seqURL, err := reader.TrustedSequencerURL(ctx); err == nil && seqURL != "" {
//	    return withPort(seqURL, DefaultBridgeServicePort), SourceSequencerURL
//	}   // a revert / "method not found" falls through
//	return "", ErrNoSourceAvailable
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
// The cache stores, per networkID, both the URL and the Source that produced it:
//
//	type cacheEntry struct {
//	    url     string
//	    source  Source // SourceConfig | SourceMetadata | SourceSequencerURL
//	    healthy bool   // result of the most recent /health probe for this url
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
//	(f *Finder) GetURL(networkID uint32) (string, error) // return cached URL or a not-found error
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
