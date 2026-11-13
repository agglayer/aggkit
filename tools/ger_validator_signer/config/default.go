package config

// DefaultValues is the default configuration, as the DefaultValues of aggkit (config/default.go). A configuration
// file only needs the mandatory values, that have no default because they depend on the deployment: L1RPCURL,
// L1ChainID, AgglayerGERAddr and [Signer]. Any other key of the file replaces its default.
const DefaultValues = `
L1RequestTimeout = "5s"
# The safest finality by default
BlockFinality = "FinalizedBlock"

[Log]
Environment = "development" # "production" or "development"
Level = "info"
Outputs = ["stderr"]

[PublicREST]
Host = "0.0.0.0"
Port = 8080
ReadTimeout = "15s"
WriteTimeout = "20s"
MaxRequestsPerIPAndSecond = 20
MaxRequestBodyBytes = 4096
MaxRequestsBurstPerIP = 40
RequestTimeout = "15s"
TLSCertFile = ""
TLSKeyFile = ""
InsecureHTTP = false

[Cache]
MaxEntries = 10000
NegativeTTL = "5s"
`
