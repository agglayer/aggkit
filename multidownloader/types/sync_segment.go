package types

import (
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

type SyncSegment struct {
	ContractAddr        common.Address
	BlockRange          aggkitcommon.BlockRange
	TargetToBlock       aggkittypes.BlockNumberFinality
	RequiredBlockHeader bool
}

func (s *SyncSegment) String() string {
	return "SyncSegment{ contracts:" + s.ContractAddr.Hex() + " range:" + s.BlockRange.String() + " blockHeader:" + fmt.Sprintf("%v", s.RequiredBlockHeader) + "}"
}

func (s SyncSegment) NewBlockRange(br aggkitcommon.BlockRange) SyncSegment {
	return SyncSegment{
		ContractAddr:        s.ContractAddr,
		BlockRange:          br,
		TargetToBlock:       s.TargetToBlock,
		RequiredBlockHeader: s.RequiredBlockHeader,
	}
}

func (s *SyncSegment) Clone() *SyncSegment {
	return &SyncSegment{
		ContractAddr:        s.ContractAddr,
		BlockRange:          s.BlockRange,
		TargetToBlock:       s.TargetToBlock,
		RequiredBlockHeader: s.RequiredBlockHeader,
	}
}

func (s *SyncSegment) UpdateToBlock(newToBlock uint64) {
	if s == nil {
		return
	}
	s.BlockRange.ToBlock = newToBlock
}

func (s SyncSegment) Equal(other SyncSegment) bool {
	return s.ContractAddr == other.ContractAddr &&
		s.BlockRange.Equal(other.BlockRange) &&
		s.TargetToBlock.Equal(other.TargetToBlock)
}
