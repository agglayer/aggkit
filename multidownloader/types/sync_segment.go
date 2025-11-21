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
	ContractAddr  common.Address
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

// UpdateToBlock updates the ToBlock of the SyncSegment
func (s *SyncSegment) UpdateToBlock(newToBlock uint64) {
	if s == nil {
		return
	}
	s.BlockRange.ToBlock = newToBlock
}

// Equal checks if two SyncSegments are equal
func (s SyncSegment) Equal(other SyncSegment) bool {
	return s.ContractAddr == other.ContractAddr &&
		s.BlockRange.Equal(other.BlockRange) &&
		s.TargetToBlock.Equal(other.TargetToBlock)
}
