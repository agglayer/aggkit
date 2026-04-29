package dvnworker

// Config holds the configuration for the DVN worker service.
type Config struct {
	// SourceChain identifies which chain is the LZ source (e.g., "l1" or "l2").
	SourceChain string `mapstructure:"SourceChain"`
	// DestinationChain identifies the LZ destination chain.
	DestinationChain string `mapstructure:"DestinationChain"`
	// CoordinatorAddr is the AggLayerDVNCoordinator contract address on the destination chain.
	CoordinatorAddr string `mapstructure:"CoordinatorAddr"`
	// OFTReceiverAddr is the AggLayerOFTReceiver contract address on the destination chain.
	OFTReceiverAddr string `mapstructure:"OFTReceiverAddr"`
	// SigningKeyPath is the path to the keystore file used to sign destination txs.
	SigningKeyPath string `mapstructure:"SigningKeyPath"`
	// SettlementPollInterval is how often to check for AggLayer settlement (duration string).
	SettlementPollInterval string `mapstructure:"SettlementPollInterval"`
	// RetryBudget is the maximum number of submission retries.
	RetryBudget int `mapstructure:"RetryBudget"`
	// RPCURL is the destination chain RPC endpoint.
	RPCURL string `mapstructure:"RPCUrl"`
}
