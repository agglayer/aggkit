package types

import (
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

var EmptyLER = common.HexToHash("0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757")

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
