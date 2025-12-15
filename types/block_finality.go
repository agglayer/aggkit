package types

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/invopop/jsonschema"
)

const (
	ConstantBlockName  = "ConstantBlock"
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
	Block    BlockName
	Offset   int64
	Specific uint64 // Specific block number, Block must be Constant
}

func convertStringToNumber[T any](s string) (T, error) {
	zeroValue := *new(T)
	for _, base := range []int{10, 0, 16} {
		var result interface{}
		var err error

		// Check if T is uint64 or int64 and call appropriate parser
		switch any(zeroValue).(type) {
		case uint64:
			result, err = strconv.ParseUint(s, base, 64)
		case int64:
			result, err = strconv.ParseInt(s, base, 64)
		default:
			return zeroValue, fmt.Errorf("unsupported type for number conversion")
		}

		if err == nil {
			res, ok := result.(T)
			if !ok {
				return zeroValue, fmt.Errorf("type assertion failed during number conversion")
			}
			return res, nil
		}
	}
	return zeroValue, fmt.Errorf("invalid block number format: %s", s)
}

func NewBlockNumber(number uint64) *BlockNumberFinality {
	return &BlockNumberFinality{
		Block:    Constant,
		Specific: number,
	}
}

// NewBlockNumberFinality creates a new BlockNumberFinality from a string
// format: <blockName>[/<offset>] e.g: "SafeBlock", "FinalizedBlock/-5", "LatestBlock/+10"
// can be directly a number
func NewBlockNumberFinality(s string) (*BlockNumberFinality, error) {
	result := BlockNumberFinality{}
	splitted := strings.Split(s, blockNameAndOffsetSeparator)
	if len(splitted) == 0 || len(splitted) > 2 {
		return nil, fmt.Errorf("invalid block finality format: %s", s)
	}
	block, err := NewBlockName(splitted[0])
	if err != nil {
		// It's a constant block?
		n, errParse := convertStringToNumber[uint64](splitted[0])
		if errParse == nil {
			return NewBlockNumber(n), nil
		}
		return nil, err
	}

	result.Block = block
	if len(splitted) == 2 { //nolint:mnd
		offset, err := convertStringToNumber[int64](splitted[1])
		if err != nil {
			return nil, fmt.Errorf("invalid block offset format: %s", splitted[1])
		}
		result.Offset = offset
	}
	return &result, nil
}
func (b *BlockNumberFinality) Equal(other BlockNumberFinality) bool {
	if b == nil {
		return false
	}
	if b.Block.IsConstant() {
		return b.Block == other.Block && b.Specific == other.Specific
	}
	return b.Block == other.Block && b.Offset == other.Offset
}

// String returns the string representation of the BlockNumberFinality
func (b *BlockNumberFinality) String() string {
	if b == nil {
		return "nil"
	}
	if b.Block.IsConstant() {
		return fmt.Sprintf("%d", b.Specific)
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
	*b = *res
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

func (b *BlockNumberFinality) IsConstant() bool {
	if b == nil {
		return false
	}
	return b.Block.IsConstant()
}

func (b *BlockNumberFinality) HasOffset() bool {
	if b == nil {
		return false
	}
	return !b.Block.IsConstant() && b.Offset != 0
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
func (b *BlockNumberFinality) ToBigInt() *big.Int {
	if b == nil {
		return nil
	}
	if b.Block.IsConstant() {
		return big.NewInt(int64(b.Specific))
	}
	return b.Block.ToBigInt()
}

func (b *BlockNumberFinality) ApplyOffset(blockNumber uint64) uint64 {
	return b.Block.ApplyOffset(blockNumber, b.Offset)
}

func (b *BlockNumberFinality) BlockName() BlockName {
	if b == nil {
		return Empty
	}
	return b.Block
}

func (c *BlockNumberFinality) CalculateBlockNumber(baseBlockNumber uint64) uint64 {
	if c == nil {
		return 0
	}
	if c.IsConstant() {
		return c.Specific
	}
	return c.Block.ApplyOffset(baseBlockNumber, c.Offset)
}

// BlockNumber gets the block number from RPC with offset taken into account
func (b *BlockNumberFinality) BlockNumber(
	ctx context.Context,
	requester EthChainReader,
) (uint64, error) {
	if b.IsEmpty() {
		return 0, fmt.Errorf("BlockNumberFinality.BlockNumber: cannot get block number for empty finality")
	}
	if b.IsConstant() {
		return b.Specific, nil
	}

	blockHeader, err := requester.HeaderByNumber(ctx, b.Block.ToBigInt())
	if err != nil {
		return 0, fmt.Errorf("BlockNumberFinality.BlockNumber: Error getting block %s. Err: %w", b.String(), err)
	}
	return b.Block.ApplyOffset(blockHeader.Number.Uint64(), b.Offset), nil
}

// BlockHeader gets the block header from RPC with offset taken into account
func (b *BlockNumberFinality) BlockHeader(
	ctx context.Context,
	requester EthChainReader,
) (*types.Header, error) {
	numberBigInt := b.ToBigInt()
	blockHeader, err := requester.HeaderByNumber(ctx, numberBigInt)
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
func (b *BlockNumberFinality) LessFinalThan(other BlockNumberFinality) (bool, error) {
	if b.Block.IsConstant() && other.Block.IsConstant() {
		return b.Specific < other.Specific, nil
	}
	if b.Block.IsConstant() || other.Block.IsConstant() {
		return true, fmt.Errorf("cannot compare constant block with non-constant block")
	}
	if b == nil {
		return true, nil
	}
	if blockOrder[b.Block] > blockOrder[other.Block] {
		return true, nil
	}
	if b.Block == other.Block {
		return b.Offset > other.Offset, nil
	}
	return false, nil
}

type BlockName int64

var (
	blockOrder = map[BlockName]int{Finalized: 1, Safe: 2, Latest: 3, Pending: 4, Empty: 5} //nolint:mnd
)

const (
	Constant  = BlockName(rpc.EarliestBlockNumber - 1)
	Safe      = BlockName(rpc.SafeBlockNumber)
	Finalized = BlockName(rpc.FinalizedBlockNumber)
	Latest    = BlockName(rpc.LatestBlockNumber)
	Pending   = BlockName(rpc.PendingBlockNumber)
	Empty     = BlockName(0)
)

func NewBlockName(s string) (BlockName, error) {
	switch strings.ToUpper(s) {
	case strings.ToUpper(ConstantBlockName):
		return Constant, nil
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

func (b BlockName) ApplyOffset(blockNumber uint64, offset int64) uint64 {
	if b.IsConstant() {
		return blockNumber
	}
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

func (b BlockName) String() string {
	switch b {
	case Constant:
		return ConstantBlockName
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

func (b BlockName) IsConstant() bool {
	return b == Constant
}

func (b BlockName) ToBigInt() *big.Int {
	switch b {
	case Latest, Empty:
		return nil
	case Constant:
		return big.NewInt(0)
	default:
		return big.NewInt(int64(b))
	}
}
