package types

import (
	"fmt"
	"math/big"
	"strings"
)

type Unclaim struct {
	GlobalIndex *big.Int `json:"global_index"`
	BlockNumber uint64   `json:"block_number"`
	LogIndex    uint64   `json:"log_index"`
}

const (
	LeafTypeAsset LeafType = iota
	LeafTypeMessage
)

type LeafType uint8

func (l LeafType) Uint8() uint8 {
	return uint8(l)
}

func (l LeafType) String() string {
	return [...]string{"Transfer", "Message"}[l]
}

func (l *LeafType) UnmarshalJSON(raw []byte) error {
	rawStr := strings.Trim(string(raw), "\"")
	switch rawStr {
	case "Transfer":
		*l = LeafTypeAsset
	case "Message":
		*l = LeafTypeMessage
	default:
		var value int
		if _, err := fmt.Sscanf(rawStr, "%d", &value); err != nil {
			return fmt.Errorf("invalid LeafType: %s", rawStr)
		}
		*l = LeafType(value)
	}
	return nil
}
