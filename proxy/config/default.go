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
L1BlockFinality = "LatestBlock"
L2BlockFinality = "LatestBlock"
MaxTrackedBridges = 100000
`
