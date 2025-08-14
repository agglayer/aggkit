package types

type Unclaim struct {
	GlobalIndex [32]byte `json:"global_index"`
	BlockNumber uint64   `json:"block_number"`
	BlockIndex  uint     `json:"block_index"`
}
