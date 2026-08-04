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
HealthCheckPath = "/health"
HealthCheckTimeout = "5s"
RequireAllHealthyOnStart = false

[REST]
Host = "0.0.0.0"
Port = 8080
ReadTimeout = "5m"
WriteTimeout = "5m"
MaxRequestsPerIPAndSecond = 10

[Tracker]
RetentionPeriod = "10m"
RegisterResolveTimeout = "3s"
L1BlockFinality = "LatestBlock"
L2BlockFinality = "LatestBlock"
MaxTrackedBridges = 100000

[Tracker.AgglayerClient]
Cached = true
[Tracker.AgglayerClient.ConfigurationCache]
TTL = "1s"
Capacity = 100
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
