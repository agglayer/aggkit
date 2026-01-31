package types

import (
	"context"
	"fmt"
	"strings"

	aggkitcommon "github.com/agglayer/aggkit/common"
	ethermantypes "github.com/agglayer/aggkit/etherman/types"
	aggkittypes "github.com/agglayer/aggkit/types"
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
	var builder strings.Builder
	builder.WriteString("SetSyncSegment: ")
	for i, segment := range s.segments {
		builder.WriteString(fmt.Sprintf("SyncSegment[%d]=%s\n", i, segment.BlockRange.String()))
	}
	return builder.String()
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

// Add adds a new SyncSegment to the SetSyncSegment, merging block ranges
// if the contract address already exists
func (s *SetSyncSegment) Add(segment SyncSegment) {
	// Check if exists
	current, exists := s.GetByContract(segment.ContractAddr)
	if !exists {
		// Add new segment
		s.segments = append(s.segments, &segment)
		return
	}
	// Merge syncers
	s.UpdateBlockRange(&current, current.BlockRange.Extend(segment.BlockRange))
}

// GetByContract returns the SyncSegment for the given contract address

func (s *SetSyncSegment) GetByContract(addr common.Address) (SyncSegment, bool) {
	if s == nil {
		return SyncSegment{}, false
	}
	for _, segment := range s.segments {
		if segment.ContractAddr == addr {
			return *segment, true
		}
	}
	return SyncSegment{}, false
}

// SubtractSegments removes the block ranges defined in segments from the current SetSyncSegment
// This is the pending data to synchronize
func (f *SetSyncSegment) SubtractSegments(segments *SetSyncSegment) error {
	if f == nil || segments == nil {
		return nil
	}
	newSegments := f.Clone()
	for _, segment := range segments.segments {
		previousSegment, exists := newSegments.GetByContract(segment.ContractAddr)
		if exists {
			brs := previousSegment.BlockRange.Subtract(segment.BlockRange)
			switch len(brs) {
			case 0:
				newSegments.Empty(&previousSegment)
			case 1:
				newSegments.UpdateBlockRange(&previousSegment, brs[0])
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
func isIncluded(ranges []aggkitcommon.BlockRange, br aggkitcommon.BlockRange) bool {
	for _, r := range ranges {
		if r.Contains(br) {
			return true
		}
	}
	return false
}

// TotalBlocks returns the total number pending blocks to synchronize
func (f *SetSyncSegment) TotalBlocks() uint64 {
	if f == nil || len(f.segments) == 0 {
		return 0
	}
	expanded := make([]aggkitcommon.BlockRange, 0, len(f.segments))
	// Add first segment
	expanded = append(expanded, f.segments[0].BlockRange)
	for _, segment := range f.segments[1:] {
		newExpanded := make([]aggkitcommon.BlockRange, 0, len(expanded))
		for _, br := range expanded {
			merged := br.Merge(segment.BlockRange)
			for _, m := range merged {
				if !isIncluded(newExpanded, m) {
					newExpanded = append(newExpanded, m)
				}
			}
		}
		expanded = newExpanded
	}
	total := uint64(0)
	for _, br := range expanded {
		total += br.CountBlocks()
	}
	return total
}

// UpdateTargetBlockToNumber updates the ToBlock to real blockNumber
func (f *SetSyncSegment) UpdateTargetBlockToNumber(ctx context.Context,
	blockNotifierGetter ethermantypes.BlockNotifierManager) error {
	if f == nil {
		return nil
	}
	for _, segment := range f.segments {
		currentBlock, err := blockNotifierGetter.GetCurrentBlockNumber(ctx, segment.TargetToBlock)
		if err != nil {
			return fmt.Errorf("setSyncSegment.UpdateToBlock: error getting BlockNotifier for finality=%s: %w",
				segment.TargetToBlock.String(), err)
		}
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
		segment, exists := f.GetByContract(addr)
		if !exists || !segment.BlockRange.Contains(query.BlockRange) {
			return false
		}
	}
	return true
}

// IsPartiallyAvailable checks if some part of the LogQuery is already synced
// always starting from FromBlock
// If there are any data avaible, it returns true and the LogQuery with the available data
func (f *SetSyncSegment) IsPartiallyAvailable(query LogQuery) (bool, *LogQuery) {
	if f == nil || len(query.Addrs) == 0 {
		return false, nil
	}

	// Find the maximum contiguous range starting from FromBlock that is available
	// for all addresses in the query
	var maxAvailableToBlock *uint64

	for _, addr := range query.Addrs {
		segment, exists := f.GetByContract(addr)
		if !exists {
			// If any address is not synced at all, nothing is available
			return false, nil
		}

		// Calculate the intersection between the segment and the query range
		intersection := segment.BlockRange.Intersect(query.BlockRange)
		if intersection.IsEmpty() {
			// If there's no overlap, nothing is available
			return false, nil
		}

		// Check if the intersection starts at FromBlock
		// If not, there's a gap at the beginning, so nothing is available
		if intersection.FromBlock != query.BlockRange.FromBlock {
			return false, nil
		}

		// Update the minimum ToBlock (the bottleneck across all addresses)
		if maxAvailableToBlock == nil || intersection.ToBlock < *maxAvailableToBlock {
			maxAvailableToBlock = &intersection.ToBlock
		}
	}

	if maxAvailableToBlock == nil {
		return false, nil
	}

	// Create the available LogQuery
	availableQuery := &LogQuery{
		Addrs: query.Addrs,
		BlockRange: aggkitcommon.NewBlockRange(
			query.BlockRange.FromBlock,
			*maxAvailableToBlock,
		),
	}

	return true, availableQuery
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
func (f *SetSyncSegment) GetHighestBlockNumber() (uint64, aggkittypes.BlockNumberFinality) {
	if f == nil || len(f.segments) == 0 {
		return 0, aggkittypes.LatestBlock
	}
	highest := uint64(0)
	finality := aggkittypes.LatestBlock
	for _, segment := range f.segments {
		if segment.BlockRange.ToBlock > highest {
			highest = segment.BlockRange.ToBlock
			finality = segment.TargetToBlock
		}
	}
	return highest, finality
}

func (f *SetSyncSegment) GetTotalPendingBlockRange() *aggkitcommon.BlockRange {
	if f == nil || len(f.segments) == 0 {
		return nil
	}
	var totalRange *aggkitcommon.BlockRange
	for _, segment := range f.segments {
		// Skip empty segments to avoid creating invalid BlockRanges
		if segment.IsEmpty() {
			continue
		}
		if totalRange == nil {
			br := segment.BlockRange
			totalRange = &br
		} else {
			extended := totalRange.Extend(segment.BlockRange)
			totalRange = &extended
		}
	}
	return totalRange
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

func (f *SetSyncSegment) GetAddressesForBlock(blockNumber uint64) []common.Address {
	blockRange := aggkitcommon.NewBlockRange(blockNumber, blockNumber)
	return f.GetAddressesForBlockRange(blockRange)
}

func (f *SetSyncSegment) Finished() bool {
	if f == nil || len(f.segments) == 0 {
		return true
	}
	for _, segment := range f.segments {
		if !segment.IsEmpty() {
			return false
		}
	}
	return true
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

func (f *SetSyncSegment) Empty(segment *SyncSegment) {
	for _, s := range f.segments {
		if s.Equal(*segment) {
			s.Empty()
			return
		}
	}
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
	for _, s := range f.segments {
		if s.Equal(*segment) {
			s.BlockRange = newBlockRange
			return
		}
	}
}

// AddLogQuery adds all segments from the LogQuery to the SetSyncSegment
// used to update the syncedSegments after a successful FilterLogs
func (f *SetSyncSegment) AddLogQuery(logQuery *LogQuery) error {
	if f == nil || logQuery == nil {
		return nil
	}
	for _, addr := range logQuery.Addrs {
		f.Add(SyncSegment{
			ContractAddr: addr,
			BlockRange:   logQuery.BlockRange,
		})
	}
	return nil
}

// SegmentsByContract returns segments for the given contract addresses
func (s *SetSyncSegment) SegmentsByContract(addrs []common.Address) []SyncSegment {
	result := make([]SyncSegment, 0, len(addrs))
	for _, addr := range addrs {
		segment, exists := s.GetByContract(addr)
		if exists {
			result = append(result, segment)
		}
	}
	return result
}
