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

[REST]
Host = "0.0.0.0"
Port = 8080
ReadTimeout = "5m"
WriteTimeout = "5m"
MaxRequestsPerIPAndSecond = 0

[Tracker]
# RetentionPeriod: how long a terminal bridge (finished, or failed to ever resolve) stays
# queryable before the tracker forgets it; a later request for the same tx re-registers it and
# tracking restarts from scratch.
RetentionPeriod = "10m"

# IdleTimeout: how long a bridge -- terminal or still active -- stays supervised once nobody has
# read it (REST poll) and it has no active WebSocket subscriber. Unlike RetentionPeriod, this
# applies regardless of status, so a bridge that never resolves and that nobody is watching does
# not stay in memory forever.
IdleTimeout = "30m"

# RegisterResolveTimeout: how long the first request for a freshly registered tx waits for the
# engine's immediate resolution attempt before answering, so it has a shot at real progress
# instead of the bare "registered" state; a lookup of an already-registered tx never waits.
RegisterResolveTimeout = "3s"

# L1BlockFinality / L2BlockFinality: the finality a bridge's creating tx receipt must reach on
# L1/L2 before the tracker accepts it, so a later reorg cannot leave it permanently following an
# orphaned deposit (a resolved bridge is never re-checked).
L1BlockFinality = "LatestBlock"
L2BlockFinality = "LatestBlock"

# MaxTrackedBridges: caps the in-memory supervised list; a request beyond it fails instead of
# registering the bridge -- reaching the cap never evicts an existing entry to make room, so
# RetentionPeriod and IdleTimeout are what keep the registry under it during normal operation.
MaxTrackedBridges = 100000

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
