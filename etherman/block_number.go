package etherman

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/invopop/jsonschema"
)

type BlockNumberFinality struct {
	string `validate:"required"`
}

func NewBlockNumberFinality(s string) BlockNumberFinality {
	return BlockNumberFinality{s}
}

var (
	SafeBlock      = BlockNumberFinality{"SafeBlock"}
	FinalizedBlock = BlockNumberFinality{"FinalizedBlock"}
	LatestBlock    = BlockNumberFinality{"LatestBlock"}
	PendingBlock   = BlockNumberFinality{"PendingBlock"}
	EarliestBlock  = BlockNumberFinality{"EarliestBlock"}
)

func (b *BlockNumberFinality) ToBlockNum() (*big.Int, error) {
	switch strings.ToUpper(b.String()) {
	case strings.ToUpper(FinalizedBlock.String()):
		return big.NewInt(int64(Finalized)), nil
	case strings.ToUpper(SafeBlock.String()):
		return big.NewInt(int64(Safe)), nil
	case strings.ToUpper(PendingBlock.String()):
		return big.NewInt(int64(Pending)), nil
	case strings.ToUpper(LatestBlock.String()):
		return big.NewInt(int64(Latest)), nil
	case strings.ToUpper(EarliestBlock.String()):
		return big.NewInt(int64(Earliest)), nil
	default:
		return nil, fmt.Errorf("invalid finality keyword: %s", b.String())
	}
}
func (b BlockNumberFinality) String() string {
	return b.string
}

// UnmarshalText unmarshalls BlockNumberFinality from text.
func (d *BlockNumberFinality) UnmarshalText(data []byte) error {
	res := BlockNumberFinality{string(data)}
	_, err := res.ToBlockNum()
	if err != nil {
		return fmt.Errorf("failed to parse BlockNumberFinality %s: %w", string(data), err)
	}
	d.string = res.string
	return nil
}

func (BlockNumberFinality) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "string",
		Title:       "BlockNumberFinality",
		Description: "BlockNumberFinality is a block finality name",
		Examples: []interface{}{
			"SafeBlock",
			"LatestBlock",
		},
	}
}

func (b BlockNumberFinality) IsEmpty() bool { //nolint:stylecheck
	return b.string == ""
}

func (b BlockNumberFinality) IsFinalized() bool {
	return b == FinalizedBlock
}

func (b BlockNumberFinality) IsSafe() bool {
	return b == SafeBlock
}

type BlockNumber int64

const (
	Safe      = BlockNumber(-4)
	Finalized = BlockNumber(-3)
	Latest    = BlockNumber(-2)
	Pending   = BlockNumber(-1)
	Earliest  = BlockNumber(0)
)
