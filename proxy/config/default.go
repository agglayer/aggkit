package config

// DefaultValues is the default configuration
const DefaultValues = `
[Log]
Environment = "development" # "production" or "development"
Level = "info"
Outputs = ["stderr"]

[L1RPC]
URL = "http://localhost:8545"
Mode = "basic"
RetryMode = "backoff"
MaxRetries = 5

[BridgeServiceFinder]
RollupManagerAddr = "0x0000000000000000000000000000000000000000"
BlockFinality = "FinalizedBlock"
PollInterval = "30s"
BlockChunkSize = 10000
HealthCheckPath = "/"
HealthCheckTimeout = "5s"
RequireAllHealthyOnStart = false
IgnoreNetworkIDs = []

[BridgeServiceFinder.BridgeURLs]

[BridgeServiceFinder.RPCURLs]

[BridgeServiceFinder.BridgeAddress]

[REST]
Host = "0.0.0.0"
Port = 8080
ReadTimeout = "5m"
WriteTimeout = "5m"
MaxRequestsPerIPAndSecond = 0

[REST.CORS]
# Disabled by default: enable it if a browser-based frontend on a different
# origin needs to call this REST server directly.
Enabled = false
# Empty denies every origin once Enabled is true; use ["*"] to allow any origin.
AllowedOrigins = []
AllowedMethods = ["GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"]
AllowedHeaders = ["*"]
AllowCredentials = false
MaxAge = "12h"

[Tracker]
# DBPath: SQLite file the supervised-bridges registry persists to, so an already-resolved
# bridge survives a restart instead of being re-resolved from scratch. Empty (the default)
# keeps the in-memory adapter, exactly as before this field existed.
#
# Opt in deliberately, not by pointing this at a shared/world-writable directory like /tmp: the
# filename is fixed, so any other local user able to write there could pre-create or symlink it
# before this binary starts. Point it at a path under this binary's own data directory instead.
#
# Pruning/retention (RetentionPeriod, IdleTimeout, MaxTrackedBridges below) is also currently a
# no-op on the SQLite-backed adapter — see bridgetracker/db.sqliteRegistry.PruneTerminal/
# PruneIdle — so tracked_bridge/activity_bridge grow unboundedly for as long as this is set,
# until the DB-side retention policy is implemented (agglayer/aggkit#1822).
DBPath = ""

# RetentionPeriod: how long a terminal bridge (finished, or failed to ever resolve) stays
# queryable before the tracker forgets it; a later request for the same tx re-registers it and
# tracking restarts from scratch. Only enforced by the default in-memory adapter — see DBPath.
RetentionPeriod = "10m"

# IdleTimeout: how long a bridge -- terminal or still active -- stays supervised once nobody has
# read it (REST poll) and it has no active WebSocket subscriber. Unlike RetentionPeriod, this
# applies regardless of status, so a bridge that never resolves and that nobody is watching does
# not stay in memory forever. Only enforced by the default in-memory adapter -- see DBPath.
IdleTimeout = "30m"

# ActivityIdleTimeout: how long a from_address's activity cache (GET /activity/from/{address})
# stays supervised with no request for it, before being forgotten entirely -- same idea as
# IdleTimeout, a separate knob because it governs a different cache.
ActivityIdleTimeout = "30m"

# ActivityPollInterval: how often the activity engine refreshes every supervised from_address in
# the background, independent of any incoming request.
ActivityPollInterval = "30s"

# ActivityRegisterResolveTimeout: how long the first request for a freshly registered
# from_address waits for the activity engine's immediate refresh attempt before answering, so it
# has a shot at real data instead of an empty result; a lookup of an already-registered address
# never waits.
ActivityRegisterResolveTimeout = "10s"

# ActivityMaxConcurrentRefreshes: how many supervised addresses the activity engine's poll tick
# refreshes at once -- each refresh is a full multi-network scan, so this is kept lower than
# MaxConcurrentResolutions.
ActivityMaxConcurrentRefreshes = 10

# RegisterResolveTimeout: how long the first request for a freshly registered tx waits for the
# engine's immediate resolution attempt before answering, so it has a shot at real progress
# instead of the bare "registered" state; a lookup of an already-registered tx never waits.
RegisterResolveTimeout = "3s"

# L1BlockFinality / L2BlockFinality: the finality a bridge's creating tx receipt must reach on
# L1/L2 before the tracker accepts it, so a later reorg cannot leave it permanently following an
# orphaned deposit (a resolved bridge is never re-checked).
L1BlockFinality = "LatestBlock"
L2BlockFinality = "LatestBlock"

# MaxTrackedBridges: caps the supervised list (in-memory or SQLite-backed, see DBPath); a
# request beyond it fails instead of registering the bridge -- reaching the cap never evicts an
# existing entry to make room. On the default in-memory adapter, RetentionPeriod and IdleTimeout
# keep the registry under it during normal operation; the SQLite-backed adapter does not yet
# evict anything (see DBPath), so the cap is effectively permanent there once reached.
MaxTrackedBridges = 100000

# L2InjectionLookbackBlocks: how many blocks the L2GlobalExitRootAddress fallback scans backwards
# from the destination network's head before giving up, instead of continuing all the way back
# to genesis.
L2InjectionLookbackBlocks = 1000

# MaxConcurrentResolutions: how many active bridges the engine's poll tick resolves at once --
# independent of MaxTrackedBridges, which bounds the registry's size, not how much of it is in
# flight during a single tick.
MaxConcurrentResolutions = 50

[Tracker.ActivitySourceBridgeService]
# PageSize: page size used while paging through a network's own GET /bridge/v1/bridges scanning
# for a given from_address.
PageSize = 100

# MaxConcurrentNetworkScans: how many networks GET /activity/from/{address} scans at once --
# each one, in turn, queries this bridge-service source and the RPC fallback concurrently, so the
# actual number of in-flight calls is up to twice this.
MaxConcurrentNetworkScans = 10

[Tracker.ActivitySourceRPC]
# Enabled: when true, GET /activity/from/{address} additionally scans each network's own bridge
# contract directly via RPC over [RangeFromBlock, RangeToBlock], in parallel with
# ActivitySourceBridgeService, and merges in whatever bridges that bridge service has not indexed
# yet -- a safety net for a bridge just submitted while the bridge service is lagging or
# resyncing (agglayer/aggkit#1837). false disables the fallback entirely (bridge-service-only,
# today's behavior).
Enabled = true

# RangeFromBlock / RangeToBlock: the block-range window this source scans on each network,
# expressed as a block finality with an optional offset (see L1BlockFinality above).
# "LatestBlock/-90" means "90 blocks behind that network's own latest block" -- wide enough to
# cover a bridge submitted a few minutes ago, narrow enough that the eth_getLogs call stays cheap
# on every poll. Widen the offset for slower-blocktime networks, narrow it for high-throughput
# ones.
RangeFromBlock = "LatestBlock/-90"
RangeToBlock = "LatestBlock"

[Tracker.AgglayerClient]
Cached = true
[Tracker.AgglayerClient.ConfigurationCache]
TTL = "1s"
Capacity = 100
# The tracker only ever reads agglayer state -- it must never be able to submit a certificate.
SendCertificate = "forbidden"
GetCertificateHeader = "cached"
GetEpochConfiguration = "cached"
GetLatestPendingCertificateHeader = "cached"
GetNetworkInfo = "cached"
[Tracker.AgglayerClient.GRPC]
#URL = "https://agglayer-dev.polygon.technology"
UseTLS = false
MinConnectTimeout = "5s"
RequestTimeout = "300s"

[Tracker.AgglayerClient.GRPC.Retry]
InitialBackoff = "1s"
MaxBackoff = "10s"
BackoffMultiplier = 2.0
MaxAttempts = 20
`
