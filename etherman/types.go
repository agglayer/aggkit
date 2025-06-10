package etherman

import (
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/elderberry/polygonvalidiumetrog"
	"github.com/ethereum/go-ethereum/common"
	"github.com/invopop/jsonschema"
)

// Block struct
type Block struct {
	BlockNumber           uint64
	BlockHash             common.Hash
	ParentHash            common.Hash
	ForcedBatches         []ForcedBatch
	SequencedBatches      [][]SequencedBatch
	VerifiedBatches       []VerifiedBatch
	SequencedForceBatches [][]SequencedForceBatch
	ForkIDs               []ForkID
	ReceivedAt            time.Time
	// GER data
	GlobalExitRoots, L1InfoTree []GlobalExitRoot
}

// GlobalExitRoot struct
type GlobalExitRoot struct {
	BlockNumber       uint64
	MainnetExitRoot   common.Hash
	RollupExitRoot    common.Hash
	GlobalExitRoot    common.Hash
	Timestamp         time.Time
	PreviousBlockHash common.Hash
}

// PolygonZkEVMBatchData represents PolygonZkEVMBatchData
type PolygonZkEVMBatchData struct {
	Transactions       []byte
	GlobalExitRoot     [32]byte
	Timestamp          uint64
	MinForcedTimestamp uint64
}

// SequencedBatch represents virtual batch
type SequencedBatch struct {
	BatchNumber   uint64
	L1InfoRoot    *common.Hash
	SequencerAddr common.Address
	TxHash        common.Hash
	Nonce         uint64
	Coinbase      common.Address
	// Struct used in preEtrog forks
	*PolygonZkEVMBatchData
	// Struct used in Etrog
	*polygonvalidiumetrog.PolygonRollupBaseEtrogBatchData
}

// ForcedBatch represents a ForcedBatch
type ForcedBatch struct {
	BlockNumber       uint64
	ForcedBatchNumber uint64
	Sequencer         common.Address
	GlobalExitRoot    common.Hash
	RawTxsData        []byte
	ForcedAt          time.Time
}

// VerifiedBatch represents a VerifiedBatch
type VerifiedBatch struct {
	BlockNumber uint64
	BatchNumber uint64
	Aggregator  common.Address
	StateRoot   common.Hash
	TxHash      common.Hash
}

// SequencedForceBatch is a sturct to track the ForceSequencedBatches event.
type SequencedForceBatch struct {
	BatchNumber uint64
	Coinbase    common.Address
	TxHash      common.Hash
	Timestamp   time.Time
	Nonce       uint64
	polygonvalidiumetrog.PolygonRollupBaseEtrogBatchData
}

// ForkID is a sturct to track the ForkID event.
type ForkID struct {
	BatchNumber uint64
	ForkID      uint64
	Version     string
}

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

// Sequence represents an operation sent to the PoE smart contract to be
// processed.
type Sequence struct {
	GlobalExitRoot, StateRoot, LocalExitRoot common.Hash
	AccInputHash                             common.Hash
	LastL2BLockTimestamp                     uint64
	BatchL2Data                              []byte
	IsSequenceTooBig                         bool
	BatchNumber                              uint64
	ForcedBatchTimestamp                     int64
	PrevBlockHash                            common.Hash
	LastCoinbase                             common.Address
}

// IsEmpty checks is sequence struct is empty
func (s Sequence) IsEmpty() bool {
	return reflect.DeepEqual(s, Sequence{})
}

type Batch struct {
	L2Data               []byte
	LastCoinbase         common.Address
	ForcedGlobalExitRoot common.Hash
	ForcedBlockHashL1    common.Hash
	ForcedBatchTimestamp uint64
	BatchNumber          uint64
	L1InfoTreeIndex      uint32
	LastL2BLockTimestamp uint64
	GlobalExitRoot       common.Hash
}

type SequenceBanana struct {
	Batches                []Batch
	OldAccInputHash        common.Hash
	AccInputHash           common.Hash
	L1InfoRoot             common.Hash
	MaxSequenceTimestamp   uint64
	CounterL1InfoRoot      uint32
	L2Coinbase             common.Address
	LastVirtualBatchNumber uint64
}

func NewSequenceBanana(batches []Batch, l2Coinbase common.Address) *SequenceBanana {
	var (
		maxSequenceTimestamp uint64
		indexL1InfoRoot      uint32
	)

	for _, batch := range batches {
		if batch.LastL2BLockTimestamp > maxSequenceTimestamp {
			maxSequenceTimestamp = batch.LastL2BLockTimestamp
		}

		if batch.L1InfoTreeIndex > indexL1InfoRoot {
			indexL1InfoRoot = batch.L1InfoTreeIndex
		}
	}

	return &SequenceBanana{
		Batches:              batches,
		MaxSequenceTimestamp: maxSequenceTimestamp,
		CounterL1InfoRoot:    indexL1InfoRoot,
		L2Coinbase:           l2Coinbase,
	}
}

func (s *SequenceBanana) Len() int {
	return len(s.Batches)
}

func (s *SequenceBanana) SetLastVirtualBatchNumber(batchNumber uint64) {
	s.LastVirtualBatchNumber = batchNumber
}
