package types

import "math/big"

type Unclaim struct {
	GlobalIndex *big.Int `json:"global_index"`
	BlockNumber uint64   `json:"block_number"`
	BlockIndex  uint     `json:"block_index"`
}
