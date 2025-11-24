package types

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/invopop/jsonschema"
)

const (
	SafeBlockName      = "SafeBlock"
	FinalizedBlockName = "FinalizedBlock"
	LatestBlockName    = "LatestBlock"
	PendingBlockName   = "PendingBlock"
	EmptyBlockName     = ""

	blockNameAndOffsetSeparator = "/"

	// Maximum positive offset limits for each block finality type
	MaxPositiveOffsetLatest    = int64(0)  // LatestBlock cannot have positive offset (cannot go beyond latest)
	MaxPositiveOffsetFinalized = int64(32) // ~1 epoch on Ethereum
	MaxPositiveOffsetSafe      = int64(64) // ~2 epochs
	MaxPositiveOffsetPending   = int64(0)  // Pending blocks don't exist yet, cannot go forward
)

var (
	FinalizedBlock = BlockNumberFinality{Block: Finalized}
	LatestBlock    = BlockNumberFinality{Block: Latest}
	SafeBlock      = BlockNumberFinality{Block: Safe}
	PendingBlock   = BlockNumberFinality{Block: Pending}
)

// BlockNumberFinality represents a block finality with an optional offset
type BlockNumberFinality struct {
	Block  BlockNumber
	Offset int64
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
		offset, err := strconv.ParseInt(splitted[1], 10, 64)
		if err != nil {
			return result, fmt.Errorf("invalid block offset format: %s", splitted[1])
		}
		result.Offset = offset
	}
	return result, nil
}
func (b *BlockNumberFinality) Equal(other BlockNumberFinality) bool {
	if b == nil {
		return false
	}
	return b.Block == other.Block && b.Offset == other.Offset
}

// String returns the string representation of the BlockNumberFinality
func (b *BlockNumberFinality) String() string {
	if b == nil {
		return "nil"
	}
	if b.Offset == 0 {
		return b.Block.String()
	}
	return fmt.Sprintf("%s%s%d", b.Block.String(), blockNameAndOffsetSeparator, b.Offset)
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
			"LatestBlock/-5",
			"FinalizedBlock/10",
		},
	}
}

// IsEmpty returns true if b is empty
func (b *BlockNumberFinality) IsEmpty() bool {
	if b == nil {
		return true
	}
	return b.Block == Empty
}

// IsFinalized returns true if b is finalized
func (b *BlockNumberFinality) IsFinalized() bool {
	if b == nil {
		return false
	}
	return b.Block == Finalized
}

// IsSafe returns true if b is safe
func (b *BlockNumberFinality) IsSafe() bool {
	if b == nil {
		return false
	}
	return b.Block == Safe
}

// Validate validates the BlockNumberFinality configuration, ensuring that:
// - The block name is valid (one of LatestBlock, SafeBlock, FinalizedBlock, or PendingBlock)
// - The positive offset does not exceed the maximum allowed for the specific block finality type
//   - LatestBlock: cannot have positive offset (limit = 0)
//   - PendingBlock: cannot have positive offset (limit = 0) as pending blocks don't exist yet
//   - SafeBlock: maximum positive offset is MaxPositiveOffsetSafe
//   - FinalizedBlock: maximum positive offset is MaxPositiveOffsetFinalized (most restrictive)
func (b BlockNumberFinality) Validate() error {
	if b.Block != Latest && b.Block != Pending && b.Block != Safe && b.Block != Finalized {
		return fmt.Errorf(
			"invalid block finality: block type must be one of LatestBlock, SafeBlock, "+
				"FinalizedBlock, or PendingBlock (got: %s)",
			b.String(),
		)
	}

	var maxOffset int64
	switch b.Block {
	case Latest:
		maxOffset = MaxPositiveOffsetLatest
	case Pending:
		maxOffset = MaxPositiveOffsetPending
	case Safe:
		maxOffset = MaxPositiveOffsetSafe
	case Finalized:
		maxOffset = MaxPositiveOffsetFinalized
	}

	// Validate offset limits (negative or zero offsets are always valid)
	if b.Offset > maxOffset {
		return fmt.Errorf(
			"positive offset %d exceeds maximum allowed %d for %s (got: %s)",
			b.Offset, maxOffset, b.Block.String(), b.String(),
		)
	}
	return nil
}

// BlockNumber gets the block number from RPC with offset taken into account
func (b *BlockNumberFinality) BlockNumber(
	ctx context.Context,
	requester ethereum.ChainReader,
) (uint64, error) {
	if b.IsEmpty() {
		return 0, fmt.Errorf("BlockNumberFinality.BlockNumber: cannot get block number for empty finality")
	}
	blockHeader, err := requester.HeaderByNumber(ctx, b.Block.toBigInt())
	if err != nil {
		return 0, fmt.Errorf("BlockNumberFinality.BlockNumber: Error getting block %s. Err: %w", b.String(), err)
	}
	return b.Block.ApplyOffset(blockHeader.Number.Uint64(), b.Offset), nil
}

// BlockHeader gets the block header from RPC with offset taken into account
func (b *BlockNumberFinality) BlockHeader(
	ctx context.Context,
	requester ethereum.ChainReader,
) (*types.Header, error) {
	blockHeader, err := requester.HeaderByNumber(ctx, b.Block.toBigInt())
	if err != nil {
		log.Errorf(
			"BlockNumberFinality.BlockHeader: Error getting base header (block=%s, offset=%d). Err: %s",
			b.String(), b.Offset, err.Error(),
		)
		return nil, err
	}

	blockNum := b.Block.ApplyOffset(blockHeader.Number.Uint64(), b.Offset)
	if blockNum == blockHeader.Number.Uint64() {
		return blockHeader, nil
	}
	return requester.HeaderByNumber(ctx, new(big.Int).SetUint64(blockNum))
}

// LessFinalThan returns true if b is less strict commitment level than other.
// In case commitment level keywords are the same, it compares the offsets.
// finalized ≤ safe ≤ latest ≤ pending
func (b *BlockNumberFinality) LessFinalThan(other BlockNumberFinality) bool {
	if b == nil {
		return true
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
	blockOrder = map[BlockNumber]int{Finalized: 1, Safe: 2, Latest: 3, Pending: 4, Empty: 5} //nolint:mnd
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
	// Can't return a block number bigger than Latest, so Latest+10 is the same as Latest+0
	if b == Latest {
		return min(blockNumber, originalBlockNumber)
	}
	return blockNumber
}

func (b BlockNumber) String() string {
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
