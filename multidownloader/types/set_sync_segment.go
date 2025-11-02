package types

import (
	"context"
	"fmt"
	"log"
	"math/big"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrFinished = fmt.Errorf("No more segments to sync")
)

type SetSyncSegment struct {
	segments []*SyncSegment
}

func (s *SetSyncSegment) String() string {
	result := "SetSyncSegment: "
	for i, segment := range s.segments {
		result += fmt.Sprintf("SyncSegment[%d]=%s\n", i, segment.BlockRange.String())
	}
	return result
}

func NewSetSyncSegment() SetSyncSegment {
	return SetSyncSegment{
		segments: []*SyncSegment{},
	}
}

func (s *SetSyncSegment) Segments() []SyncSegment {
	result := make([]SyncSegment, 0, len(s.segments))
	for _, segment := range s.segments {
		result = append(result, *segment)
	}
	return result
}

func (s *SetSyncSegment) Add(segment SyncSegment) {
	// Check if exists
	current := s.GetByContract(segment.ContractAddr)
	if current == nil {
		// Add new segment
		s.segments = append(s.segments, &segment)
		return
	}
	// Merge syncers
	current.BlockRange = current.BlockRange.Merge(segment.BlockRange)
}

func (s *SetSyncSegment) GetByContract(addr common.Address) *SyncSegment {
	if s == nil {
		return nil
	}
	for _, segment := range s.segments {
		if segment.ContractAddr == addr {
			return segment.Clone()
		}
	}
	return nil
}

// Substract removes the block ranges defined in segments from the current SetSyncSegment
// This is the pending data to synchronize
func (f *SetSyncSegment) Substract(segments *SetSyncSegment) *SetSyncSegment {
	result := NewSetSyncSegment()
	if segments == nil {
		return f
	}

	for _, current := range f.segments {
		toSub := segments.GetByContract(current.ContractAddr)
		if toSub != nil {
			blockRanges := current.BlockRange.Substract(toSub.BlockRange)
			// Add as many segments as blockRange generated (0, 1 or 2)
			for _, br := range blockRanges {
				result.Add(current.NewBlockRange(br))
			}
		} else {
			// Keep current
			result.Add(*current)
		}
	}
	return &result
}

func (f *SetSyncSegment) TotalBlocks() uint64 {
	if f == nil {
		return 0
	}
	minToBLock := ^uint64(0)
	maxFromBlock := uint64(0)
	for _, segment := range f.segments {
		if segment.BlockRange.FromBlock < minToBLock {
			minToBLock = segment.BlockRange.FromBlock
		}
		if segment.BlockRange.ToBlock > maxFromBlock {
			maxFromBlock = segment.BlockRange.ToBlock
		}
	}
	bn := aggkitcommon.NewBlockRange(minToBLock, maxFromBlock)
	return bn.CountBlocks()
}

func (f *SetSyncSegment) UpdateToBlock(ctx context.Context, blockNotifierGetter BlockNotifierManagerGetter) {
	if f == nil {
		return
	}
	for _, segment := range f.segments {
		bn, err := blockNotifierGetter.GetBlockNotifier(ctx, segment.TargetToBlock)
		if err != nil {
			log.Fatalf("Error getting BlockNotifier for finality=%s: %v", segment.TargetToBlock.String(), err)
		}
		currentBlock := bn.GetCurrentBlockNumber()
		segment.UpdateToBlock(uint64(currentBlock))
	}
}

func (f *SetSyncSegment) IsAvailable(query LogQuery) bool {
	if f == nil {
		return false
	}
	for _, addr := range query.Addrs {
		segment := f.GetByContract(addr)
		if segment == nil || !segment.BlockRange.Overlaps(query.BlockRange) {
			return false
		}
	}
	return true
}

type LogQuery struct {
	Addrs      []common.Address
	BlockRange aggkitcommon.BlockRange
}

func NewLogQueryFromEthereumFilter(query ethereum.FilterQuery) LogQuery {
	return LogQuery{
		Addrs:      query.Addresses,
		BlockRange: aggkitcommon.NewBlockRange(query.FromBlock.Uint64(), query.ToBlock.Uint64()),
	}
}

func (l *LogQuery) String() string {
	if l == nil {
		return "LogQuery: <nil>"
	}
	return fmt.Sprintf("LogQuery: addrs=%v, blockRange=%s", l.Addrs, l.BlockRange.String())
}

func (l *LogQuery) ToRPCFilterQuery() ethereum.FilterQuery {
	return ethereum.FilterQuery{
		Addresses: l.Addrs,
		FromBlock: new(big.Int).SetUint64(l.BlockRange.FromBlock),
		ToBlock:   new(big.Int).SetUint64(l.BlockRange.ToBlock),
	}
}

func (f *SetSyncSegment) NextQuery(syncBlockChunkSize uint32, maxBlockNumber uint64) (*LogQuery, error) {
	if f == nil || len(f.segments) == 0 {
		return nil, ErrFinished
	}
	lowestSegment := f.GetLowestFromBlockSegment()
	if lowestSegment == nil {
		return nil, ErrFinished
	}
	br := lowestSegment.BlockRange.Intersect(aggkitcommon.NewBlockRange(
		lowestSegment.BlockRange.FromBlock,
		lowestSegment.BlockRange.FromBlock+uint64(syncBlockChunkSize)-1,
	))
	if maxBlockNumber > 0 {
		br = br.Cap(maxBlockNumber)
	}
	if br.IsEmpty() {
		return nil, ErrFinished
	}
	addrs := f.GetAddressesForBlockRange(br)
	if len(addrs) == 0 {
		return nil, fmt.Errorf("INTERNAL ERROR: no addresses found for block range: %s", br.String())
	}
	return &LogQuery{
		Addrs:      addrs,
		BlockRange: br,
	}, nil
}

func (f *SetSyncSegment) GetLowestFromBlockSegment() *SyncSegment {
	if f == nil || len(f.segments) == 0 {
		return nil
	}
	var lower *SyncSegment
	for _, segment := range f.segments {
		if lower == nil || segment.BlockRange.FromBlock < lower.BlockRange.FromBlock {
			lower = segment
		}
	}
	return lower.Clone()
}

func (f *SetSyncSegment) GetAddressesForBlockRange(blockRange aggkitcommon.BlockRange) []common.Address {
	addresses := []common.Address{}
	for _, segment := range f.segments {
		if segment.BlockRange.Overlaps(blockRange) {
			addresses = append(addresses, segment.ContractAddr)
		}
	}
	return addresses
}

func (f *SetSyncSegment) Finished() bool {
	return f == nil || len(f.segments) == 0
}

func (f *SetSyncSegment) Clone() *SetSyncSegment {
	if f == nil {
		return nil
	}
	newSet := NewSetSyncSegment()
	for _, segment := range f.segments {
		newSet.Add(*segment)
	}
	return &newSet
}

func (f *SetSyncSegment) Remove(segmentToRemove *SyncSegment) {
	if f == nil || segmentToRemove == nil {
		return
	}
	newSegments := []*SyncSegment{}
	for _, s := range f.segments {
		if !s.Equal(*segmentToRemove) {
			newSegments = append(newSegments, s)
		}
	}
	f.segments = newSegments
}

func (f *SetSyncSegment) UpdateBlockRange(segment *SyncSegment, newBlockRange aggkitcommon.BlockRange) {
	if f == nil || segment == nil {
		return
	}
	for i, s := range f.segments {
		if s.Equal(*segment) {
			f.segments[i].BlockRange = newBlockRange
			return
		}
	}
}

func (f *SetSyncSegment) UpdateSyncingAfterDoingQuery(logQuery *LogQuery) *SetSyncSegment {
	if f == nil || logQuery == nil {
		return f
	}
	newSegments := f.Clone()
	for _, addr := range logQuery.Addrs {
		segment := f.GetByContract(addr)
		if segment != nil {
			brs := segment.BlockRange.Substract(logQuery.BlockRange)
			switch len(brs) {
			case 0:
				newSegments.Remove(segment)
			case 1:
				newSegments.UpdateBlockRange(segment, brs[0])
			default:
				log.Fatal("Not supported")
			}
		}
	}
	return newSegments
}
