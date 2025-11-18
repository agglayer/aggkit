package types

import (
	"context"
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	ethermantypes "github.com/agglayer/aggkit/etherman/types"
	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrFinished = fmt.Errorf("no more segments to sync")
)

type SetSyncSegment struct {
	segments []*SyncSegment
}

// String returns a string representation of the SetSyncSegment
func (s *SetSyncSegment) String() string {
	result := "SetSyncSegment: "
	for i, segment := range s.segments {
		result += fmt.Sprintf("SyncSegment[%d]=%s\n", i, segment.BlockRange.String())
	}
	return result
}

// NewSetSyncSegment creates a new empty SetSyncSegment
func NewSetSyncSegment() SetSyncSegment {
	return SetSyncSegment{
		segments: []*SyncSegment{},
	}
}

// NewSetSyncSegmentFromLogQuery creates a new SetSyncSegment from a LogQuery
func NewSetSyncSegmentFromLogQuery(logQuery *LogQuery) SetSyncSegment {
	set := NewSetSyncSegment()
	for _, addr := range logQuery.Addrs {
		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   logQuery.BlockRange,
		}
		set.Add(segment)
	}
	return set
}

// Segments returns all SyncSegments in the SetSyncSegment
func (s *SetSyncSegment) Segments() []SyncSegment {
	result := make([]SyncSegment, 0, len(s.segments))
	for _, segment := range s.segments {
		result = append(result, *segment)
	}
	return result
}

// Add adds a new SyncSegment to the SetSyncSegment, merging block ranges
// if the contract address already exists
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
	s.Replace(current)
}

// Replace replaces an existing segment with the provided one instead of merging
func (s *SetSyncSegment) Replace(segment *SyncSegment) {
	if s == nil || segment == nil {
		return
	}
	for i, existing := range s.segments {
		if existing.ContractAddr == segment.ContractAddr {
			s.segments[i] = segment
			return
		}
	}
}

// GetByContract returns the SyncSegment for the given contract address
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

// SubtractSegments removes the block ranges defined in segments from the current SetSyncSegment
// This is the pending data to synchronize
func (f *SetSyncSegment) SubtractSegments(segments *SetSyncSegment) error {
	if f == nil || segments == nil {
		return nil
	}
	newSegments := f.Clone()
	for _, segment := range segments.Segments() {
		previousSegment := newSegments.GetByContract(segment.ContractAddr)
		if previousSegment != nil {
			brs := previousSegment.BlockRange.Subtract(segment.BlockRange)
			switch len(brs) {
			case 0:
				newSegments.Remove(previousSegment)
			case 1:
				newSegments.UpdateBlockRange(previousSegment, brs[0])
			default:
				return fmt.Errorf("setSyncSegment.SubtractSegments: cannot split segment for %s into multiple ranges  %+v",
					segment.String(), brs)
			}
		}
	}
	f.segments = newSegments.segments
	return nil
}

// SubtractLogQuery removes the block ranges defined in the logQuery from the current SetSyncSegment
// This is used to update the pendingSync after doing a FilterLogs query
func (f *SetSyncSegment) SubtractLogQuery(logQuery *LogQuery) error {
	if logQuery == nil {
		return nil
	}
	newSegments := NewSetSyncSegmentFromLogQuery(logQuery)
	return f.SubtractSegments(&newSegments)
}

// TotalBlocks returns the total number pending to synchronize
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

// UpdateToBlock updates the ToBlock to real blockNumber
func (f *SetSyncSegment) UpdateToBlock(ctx context.Context,
	blockNotifierGetter ethermantypes.BlockNotifierManager) error {
	if f == nil {
		return nil
	}
	for _, segment := range f.segments {
		bn, err := blockNotifierGetter.GetBlockNotifier(ctx, segment.TargetToBlock)
		if err != nil {
			return fmt.Errorf("setSyncSegment.UpdateToBlock: error getting BlockNotifier for finality=%s: %w",
				segment.TargetToBlock.String(), err)
		}
		currentBlock := bn.GetCurrentBlockNumber()
		segment.UpdateToBlock(currentBlock)
	}
	return nil
}

// IsAvailable checks if the required LogQuery data is already synced
func (f *SetSyncSegment) IsAvailable(query LogQuery) bool {
	if f == nil {
		return false
	}
	for _, addr := range query.Addrs {
		segment := f.GetByContract(addr)
		if segment == nil || !segment.BlockRange.Contains(query.BlockRange) {
			return false
		}
	}
	return true
}

// NextQuery generates the next LogQuery to sync based on the lowest FromBlock pending
// to synchronize
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

// ExtendSegments extends the current SetSyncSegment with the block ranges defined in the logQuery
// that is used to update the syncedSegments after doing a FilterLogs query
func (f *SetSyncSegment) ExtendSegments(logQuery *LogQuery) error {
	if f == nil || logQuery == nil {
		return nil
	}
	newSegments := f.Clone()
	for _, addr := range logQuery.Addrs {
		segment := newSegments.GetByContract(addr)
		if segment != nil {
			mergedRange := segment.BlockRange.Merge(logQuery.BlockRange)
			newSegments.UpdateBlockRange(segment, mergedRange)
		} else {
			newSegments.Add(SyncSegment{
				ContractAddr: addr,
				BlockRange:   logQuery.BlockRange,
			})
		}
	}
	f.segments = newSegments.segments
	return nil
}

// SegmentsByContract returns segments for the given contract addresses
func (s *SetSyncSegment) SegmentsByContract(addrs []common.Address) []SyncSegment {
	result := make([]SyncSegment, 0, len(addrs))
	for _, addr := range addrs {
		segment := s.GetByContract(addr)
		if segment != nil {
			result = append(result, *segment)
		}
	}
	return result
}
