package dvnsyncer

// Config holds the configuration for the DVN syncer service.
type Config struct {
	// RPCURL is the RPC endpoint for the chain to sync.
	RPCURL string `mapstructure:"RPCUrl"`
	// EndpointV2Addr is the LayerZero EndpointV2 contract address on this chain.
	EndpointV2Addr string `mapstructure:"EndpointV2Addr"`
	// AggLayerDVNAddr is the AggLayerDVN contract address on this chain.
	AggLayerDVNAddr string `mapstructure:"AggLayerDVNAddr"`
	// SyncStartBlock is the block number to start syncing from.
	SyncStartBlock uint64 `mapstructure:"SyncStartBlock"`
	// ConfirmationDepth is the number of block confirmations before finalizing events.
	ConfirmationDepth uint64 `mapstructure:"ConfirmationDepth"`
	// DBPath is the path to the SQLite database file.
	DBPath string `mapstructure:"DBPath"`
}
