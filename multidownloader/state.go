package multidownloader

import (
	"context"
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/etherman/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	"github.com/ethereum/go-ethereum/common"
)

type State struct {
	// These are the segments that we have already synced
	// when a syncer does a `FilterLogs`, it is used to check what is already synced
	Synced mdrtypes.SetSyncSegment
	// These are the  segments that we need to sync
	Pending mdrtypes.SetSyncSegment
}

func NewEmptyState() *State {
	return &State{
		Synced:  mdrtypes.NewSetSyncSegment(),
		Pending: mdrtypes.NewSetSyncSegment(),
	}
}

func NewState(synced *mdrtypes.SetSyncSegment, pending *mdrtypes.SetSyncSegment) *State {
	return &State{
		Synced:  *synced,
		Pending: *pending,
	}
}

func NewStateFromStorageSyncedBlocks(storageSynced mdrtypes.SetSyncSegment,
	totalToSync mdrtypes.SetSyncSegment) (*State, error) {
	err := totalToSync.SubtractSegments(&storageSynced)
	if err != nil {
		return nil, fmt.Errorf("Initialize: cannot calculate pendingSync: %w", err)
	}
	return NewState(&storageSynced, &totalToSync), nil
}

// Clone creates a deep copy of the State
// This ensures that modifications to the cloned state don't affect the original
func (s *State) Clone() *State {
	if s == nil {
		return nil
	}

	// Use Clone() from SetSyncSegment which does deep copy
	clonedSynced := s.Synced.Clone()
	clonedPending := s.Pending.Clone()

	return &State{
		Synced:  *clonedSynced,
		Pending: *clonedPending,
	}
}
func (s *State) String() string {
	return "State{Synced: " + s.Synced.String() +
		", Pending: " + s.Pending.String() + "}"
}

func (s *State) UpdateTargetBlockToNumber(ctx context.Context, blockNotifier types.BlockNotifierManager) error {
	return s.Pending.UpdateTargetBlockToNumber(ctx, blockNotifier)
}

func (s *State) GetHighestBlockNumberPendingToSync() uint64 {
	return s.Pending.GetHighestBlockNumber()
}

func (s *State) IsAvailable(query mdrtypes.LogQuery) bool {
	return s.Synced.IsAvailable(query)
}

func (s *State) IsPartiallyAvailable(query mdrtypes.LogQuery) (bool, *mdrtypes.LogQuery) {
	return s.Synced.IsPartiallyAvailable(query)
}

func (s *State) GetTotalPendingBlockRange() *aggkitcommon.BlockRange {
	return s.Pending.GetTotalPendingBlockRange()
}

func (s *State) GetAddressesToSyncForBlockNumber(blockNumber uint64) []common.Address {
	return s.Pending.GetAddressesForBlock(blockNumber)
}
func (s *State) IsSyncFinished() bool {
	return s.Pending.Finished()
}

func (s *State) TotalBlocksPendingToSync() uint64 {
	return s.Pending.TotalBlocks()
}

// OnNewSyncedLogQuery updates the state to mark a LogQuery as synced
// This function is transactional - if either operation fails, the state remains unchanged
func (s *State) OnNewSyncedLogQuery(logQuery *mdrtypes.LogQuery) error {
	if s == nil {
		return fmt.Errorf("OnNewSyncedLogQuery: state is nil")
	}
	if logQuery == nil {
		return fmt.Errorf("OnNewSyncedLogQuery: logQuery is nil")
	}

	// Clone both sets to ensure atomicity
	// If either operation fails, the original state remains unchanged
	clonedSynced := s.Synced.Clone()
	clonedPending := s.Pending.Clone()

	// Try to add to synced
	err := clonedSynced.AddLogQuery(logQuery)
	if err != nil {
		return fmt.Errorf("OnNewSyncedLogQuery: adding synced segment: %w", err)
	}

	// Try to subtract from pending
	err = clonedPending.SubtractLogQuery(logQuery)
	if err != nil {
		return fmt.Errorf("OnNewSyncedLogQuery: subtracting pending segment: %w", err)
	}

	// Both operations succeeded, commit the changes
	s.Synced = *clonedSynced
	s.Pending = *clonedPending

	return nil
}

func (s *State) SyncedSegmentsByContract(addrs []common.Address) []mdrtypes.SyncSegment {
	return s.Synced.SegmentsByContract(addrs)
}

func (s *State) NextQueryToSync(syncBlockChunkSize uint32, maxBlockNumber uint64) (*mdrtypes.LogQuery, error) {
	return s.Pending.NextQuery(syncBlockChunkSize, maxBlockNumber)
}
