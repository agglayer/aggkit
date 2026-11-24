package types

import "github.com/ethereum/go-ethereum/common"

// BridgeRequest is the request of GET /tracker/v1/network/{network_id}/tx/{tx_hash} (and of
// its /ws variant). NetworkID and TxHash come from the URL path, so they are mandatory; LogIndex
// comes from the optional log_index query parameter
type BridgeRequest struct {
	// NetworkID is the network where the bridge transaction was sent (0 -> Mainnet)
	NetworkID uint32 `json:"network_id"`
	// TxHash is the hash of the transaction that created the bridge (bridgeAsset)
	TxHash common.Hash `json:"tx_hash"`
	// LogIndex selects which of the tx's BridgeEvents to track, by the index of its log (the
	// bridge's log_index/block_pos). nil tracks the tx's only BridgeEvent, and fails as
	// ambiguous if the tx emitted several
	LogIndex *uint32 `json:"log_index,omitempty"`
}
