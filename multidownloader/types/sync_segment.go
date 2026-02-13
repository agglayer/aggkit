package types

import (
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// SyncSegment represents a segment of blocks, it is used for synced segments but also
// for representing segments to be synced
type SyncSegment struct {
	ContractAddr common.Address
	// BlockRange can be empty  BlockRange.IsEmpty()
	BlockRange    aggkitcommon.BlockRange
	TargetToBlock aggkittypes.BlockNumberFinality
}

// NewSyncSegment creates a new SyncSegment
func NewSyncSegment(contractAddr common.Address,
	blockRange aggkitcommon.BlockRange,
	targetToBlock aggkittypes.BlockNumberFinality,
	requiredBlockHeader bool) SyncSegment {
	return SyncSegment{
		ContractAddr:  contractAddr,
		BlockRange:    blockRange,
		TargetToBlock: targetToBlock,
	}
}

// String returns a string representation of the SyncSegment
func (s *SyncSegment) String() string {
	return "SyncSegment{ contracts:" + s.ContractAddr.Hex() + " range:" + s.BlockRange.String() +
		" TargetToBlock:" + fmt.Sprintf("%v", s.TargetToBlock) + "}"
}

// NewBlockRange creates a new SyncSegment changing only the BlockRange
func (s SyncSegment) NewBlockRange(br aggkitcommon.BlockRange) SyncSegment {
	return SyncSegment{
		ContractAddr:  s.ContractAddr,
		BlockRange:    br,
		TargetToBlock: s.TargetToBlock,
	}
}

// Clone creates a deep copy of the SyncSegment
func (s *SyncSegment) Clone() *SyncSegment {
	return &SyncSegment{
		ContractAddr:  s.ContractAddr,
		BlockRange:    s.BlockRange,
		TargetToBlock: s.TargetToBlock,
	}
}

// Empty sets the SyncSegment (fromBlock > toBlock) to indicate it is empty
func (s *SyncSegment) Empty() {
	if s == nil {
		return
	}
	// Set FromBlock greater than ToBlock to indicate empty segment
	s.BlockRange = aggkitcommon.BlockRangeZero
}

func (s *SyncSegment) IsEmpty() bool {
	if s == nil {
		return true
	}
	return s.BlockRange.IsEmpty()
}

// There are special values like BlockRange(0,0)
// that we want to consider invalid for multidownloader,
// so we need this method to check the validity of the SyncSegment
func (s *SyncSegment) IsValid() bool {
	if s.IsEmpty() {
		return true
	}
	// We use value {0,0} to represent empty range in DB, so it's forbidden
	// to use the BlockRange(0,0) for multidownloader
	if !s.BlockRange.IsEmpty() && s.BlockRange.FromBlock == 0 && s.BlockRange.ToBlock == 0 {
		return false
	}
	return true
}

// Equal checks if two SyncSegments are equal
func (s SyncSegment) Equal(other SyncSegment) bool {
	return s.ContractAddr == other.ContractAddr &&
		s.BlockRange.Equal(other.BlockRange) &&
		s.TargetToBlock.Equal(other.TargetToBlock)
}
