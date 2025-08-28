package types

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/invopop/jsonschema"
)

// BlockNumberFinality represents a block finality with an optional offset
type BlockNumberFinality struct {
	Block  BlockNumber
	Offset int64
	// IfNotFoundReturnsZeroFlag indicates whether to return zero if the block is not found
	IfNotFoundReturnsZeroFlag bool
}

// NewBlockNumberFinality creates a new BlockNumberFinality from a string
// format: <blockName>[/<offset>] e.g: "SafeBlock", "FinalizedBlock/-5", "LatestBlock/+10"
func NewBlockNumberFinality(s string) (BlockNumberFinality, error) {
	result := BlockNumberFinality{}
	splitted := strings.Split(s, blockNameAndOffsetSeparator)
	if len(splitted) == 0 || len(splitted) > 2 {
		return result, fmt.Errorf("invalid block finality format: %s", s)
	}
	block, err := NewBlockNumber(splitted[0])
	if err != nil {
		return result, err
	}
	result.Block = block
	if len(splitted) == 2 { //nolint:mnd
		//_, err := fmt.Sscanf(splitted[1], "%d", &result.Offset)
		offset, err := strconv.ParseInt(splitted[1], 10, 64)
		if err != nil {
			return result, fmt.Errorf("invalid block offset format: %s", splitted[1])
		}
		result.Offset = offset
	}
	if result.Block == Latest && result.Offset > 0 {
		return result, fmt.Errorf("invalid block finality: cannot have positive offset with LatestBlock")
	}
	return result, nil
}

const (
	SafeBlockName      = "SafeBlock"
	FinalizedBlockName = "FinalizedBlock"
	LatestBlockName    = "LatestBlock"
	PendingBlockName   = "PendingBlock"
	EmptyBlockName     = ""

	blockNameAndOffsetSeparator = "/"
)

var (
	FinalizedBlock = BlockNumberFinality{Block: Finalized}
	LatestBlock    = BlockNumberFinality{Block: Latest}
	SafeBlock      = BlockNumberFinality{Block: Safe}
	PendingBlock   = BlockNumberFinality{Block: Pending}
)

// String returns the string representation of the BlockNumberFinality
func (b *BlockNumberFinality) String() string {
	if b == nil {
		return "nil"
	}
	if b.Offset == 0 {
		return b.Block.ToString()
	}
	return fmt.Sprintf("%s%s%d", b.Block.ToString(), blockNameAndOffsetSeparator, b.Offset)
}

// UnmarshalText unmarshalls BlockNumberFinality from text.
func (b *BlockNumberFinality) UnmarshalText(data []byte) error {
	res, err := NewBlockNumberFinality(string(data))
	if err != nil {
		return fmt.Errorf("failed to parse BlockNumberFinality %s: %w", string(data), err)
	}
	b.Block = res.Block
	b.Offset = res.Offset
	return nil
}

// JSONSchema returns the JSON schema for BlockNumberFinality
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

// IsEmpty returns true if v is empty
func (b BlockNumberFinality) IsEmpty() bool {
	return b.Block == Empty
}

// IsLessThan returns true if v is less than other
func (b BlockNumberFinality) IsFinalized() bool {
	return b.Block == Finalized
}
func (b BlockNumberFinality) IsSafe() bool {
	return b.Block == Safe
}

func (b BlockNumberFinality) IsLatest() bool {
	return b.Block == Latest && b.Offset >= 0
}

// BlockNumber gets the safe block number from RPC
func (b *BlockNumberFinality) BlockNumber(ctx context.Context, requester ethereum.ChainReader) (uint64, error) {
	blockHeader, err := requester.HeaderByNumber(ctx, b.Block.toBigInt())
	if err != nil {
		if strings.Contains(err.Error(), "block not found") && b.IfNotFoundReturnsZeroFlag {
			log.Warnf("block %s not found, assuming 0", b.String())
			return 0, nil
		} else {
			log.Errorf("BlockNumberFinality.BlockNumber: Error getting block %s. Err: %s", b.String(), err.Error())
			return 0, err
		}
	}
	return b.Block.ApplyOffset(blockHeader.Number.Uint64(), b.Offset), nil
}

// IsGreaterThan returns true if v is greater than other
// earliest ≤ finalized ≤ safe ≤ latest ≤ pending
func (b *BlockNumberFinality) GreaterThan(other *BlockNumberFinality) bool {
	if b == nil || other == nil {
		return false
	}
	if blockOrder[b.Block] > blockOrder[other.Block] {
		return true
	}
	if b.Block == other.Block {
		return b.Offset > other.Offset
	}
	return false
}

type BlockNumber int64

var (
	blockOrder = map[BlockNumber]int{Finalized: 1, Safe: 2, Latest: 3, Pending: 4, Empty: 0}
)

const (
	Safe      = BlockNumber(rpc.SafeBlockNumber)
	Finalized = BlockNumber(rpc.FinalizedBlockNumber)
	Latest    = BlockNumber(rpc.LatestBlockNumber)
	Pending   = BlockNumber(rpc.PendingBlockNumber)
	Empty     = BlockNumber(0)
)

func NewBlockNumber(s string) (BlockNumber, error) {
	switch strings.ToUpper(s) {
	case strings.ToUpper(FinalizedBlockName):
		return Finalized, nil
	case strings.ToUpper(SafeBlockName):
		return Safe, nil
	case strings.ToUpper(PendingBlockName):
		return Pending, nil
	case strings.ToUpper(LatestBlockName):
		return Latest, nil
	default:
		return 0, fmt.Errorf("invalid finality keyword: %s", s)
	}
}

func (b BlockNumber) ApplyOffset(blockNumber uint64, offset int64) uint64 {
	originalBlockNumber := blockNumber
	if offset < 0 {
		if blockNumber < uint64(-offset) {
			blockNumber = 0
		} else {
			blockNumber += uint64(offset)
		}
	} else {
		blockNumber += uint64(offset)
	}
	// Can't return a block number biggest than Latest, so Latest+10 is the same as Latest+0
	if b == Latest {
		return min(blockNumber, originalBlockNumber)
	}
	return blockNumber
}

func (b BlockNumber) ToString() string {
	switch b {
	case Finalized:
		return FinalizedBlockName
	case Safe:
		return SafeBlockName
	case Pending:
		return PendingBlockName
	case Latest:
		return LatestBlockName
	case Empty:
		return EmptyBlockName
	default:
		return "UnknownBlock"
	}
}

func (b BlockNumber) toBigInt() *big.Int {
	if b == Latest || b == Empty {
		return nil
	}
	return big.NewInt(int64(b))
}
