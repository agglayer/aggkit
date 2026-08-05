package api

import (
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/ethereum/go-ethereum/common"
)

// BridgeStatus is part of the response of GET /tracker/v1/tx/{txHash} (see TrackingData),
// identifying the bridge that TrackingData.AllSteps describes
type BridgeStatus struct {
	// BridgeType is the string representation of the bridge's direction (e.g. "L1->L2")
	BridgeType string `json:"bridge_type"`
	// BlockNumber is the block, on the origin network, where the BridgeEvent was emitted
	BlockNumber uint64 `json:"block_number"`
	// LogIndex is the position of the BridgeEvent log within BlockNumber
	LogIndex uint32 `json:"log_index"`
	// BlockTimestamp is the timestamp of the block, on the origin network, where the BridgeEvent was emitted
	BlockTimestamp uint64 `json:"block_timestamp"`
	// Event holds the facts unpacked directly from the on-chain BridgeEvent log
	Event BridgeEventData `json:"event"`
}

// BridgeEventData holds the fields taken directly from the on-chain BridgeEvent log, as opposed
// to context resolved around it (block number/index/timestamp, tracking network) — see
// BridgeStatus
type BridgeEventData struct {
	// LeafType is the string representation of the kind of leaf the bridge created (asset or message)
	LeafType string `json:"leaf_type"`
	// OriginNetwork is the network where the bridged asset originates from
	OriginNetwork uint32 `json:"origin_network"`
	// OriginAddress is the address of the asset on the origin network
	OriginAddress common.Address `json:"origin_address"`
	// DestinationNetwork is the network the bridge exits to (0 -> Mainnet)
	DestinationNetwork uint32 `json:"destination_network"`
	// DestinationAddress is the address that receives the asset on the destination network
	DestinationAddress common.Address `json:"destination_address"`
	// Amount is the amount of the asset being bridged, as a decimal string — a plain JSON
	// number would risk losing precision on wei-scale amounts in clients that decode numbers
	// as float64 (e.g. JavaScript)
	Amount string `json:"amount"`
	// DepositCount is the index of the bridge leaf in the origin exit tree
	DepositCount uint32 `json:"deposit_count"`
}

// newBridgeStatus builds the wire BridgeStatus from the bridge facts resolved by the engine;
// nil until FindBridge resolves them (see domain.TrackingBridgeTx.Info)
func newBridgeStatus(info *domain.BridgeInfo) *BridgeStatus {
	if info == nil {
		return nil
	}
	return &BridgeStatus{
		BridgeType:     info.BridgeType().String(),
		BlockNumber:    info.BlockNumber,
		LogIndex:       info.LogIndex,
		BlockTimestamp: info.BlockTimestamp,
		Event: BridgeEventData{
			LeafType:           info.LeafType.String(),
			OriginNetwork:      info.OriginNetwork,
			OriginAddress:      info.OriginAddress,
			DestinationNetwork: info.DestinationNetwork,
			DestinationAddress: info.DestinationAddress,
			Amount:             info.Amount.String(),
			DepositCount:       info.DepositCount,
		},
	}
}
