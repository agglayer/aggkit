package types

import "github.com/ethereum/go-ethereum/common"

// BridgeRequest is the request of GET /tracker/v1/network/{network_id}/tx/{tx_hash}.
// Both values come from the URL path, so they are mandatory
type BridgeRequest struct {
	// NetworkID is the network where the bridge transaction was sent (0 -> Mainnet)
	NetworkID uint32 `json:"network_id"`
	// TxHash is the hash of the transaction that created the bridge (bridgeAsset)
	TxHash common.Hash `json:"tx_hash"`
}
