package multidownloader

import (
	"context"
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/etherman/types"
	"github.com/agglayer/aggkit/log"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

const maxPercent = 100.0

// State represents the current state of the multidownloader,
// it contains the segments that are already synced and the segments that are pending to be synced
type State struct {
	// These are the segments that we have already synced
	// when a syncer does a `FilterLogs`, it is used to check what is already synced
	Synced mdrtypes.SetSyncSegment
	// These are the  segments that we need to sync
	Pending mdrtypes.SetSyncSegment
}

// NewEmptyState creates a new State with empty synced and pending segments
func NewEmptyState() *State {
	return &State{
		Synced:  mdrtypes.NewSetSyncSegment(),
		Pending: mdrtypes.NewSetSyncSegment(),
	}
}

// NewState creates a new State with the given synced and pending segments
func NewState(synced *mdrtypes.SetSyncSegment, pending *mdrtypes.SetSyncSegment) *State {
	return &State{
		Synced:  *synced,
		Pending: *pending,
	}
}

// NewStateFromStorageSyncedBlocks creates a new State from the given storage
// synced blocks and total to sync blocks
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

// String returns a string representation of the State
func (s *State) String() string {
	return "State{Synced: " + s.Synced.String() +
		", Pending: " + s.Pending.String() + "}"
}

// UpdateTargetBlockToNumber updates the target block number for the pending segments
// for that use the blockNotifier
func (s *State) UpdateTargetBlockToNumber(ctx context.Context, blockNotifier types.BlockNotifierManager) error {
	return s.Pending.UpdateTargetBlockToNumber(ctx, blockNotifier)
}

// GetHighestBlockNumberPendingToSync returns the highest block number that is pending to be synced
func (s *State) GetHighestBlockNumberPendingToSync() (uint64, aggkittypes.BlockNumberFinality) {
	return s.Pending.GetHighestBlockNumber()
}

// IsAvailable checks if the given LogQuery is fully available in the synced segments
func (s *State) IsAvailable(query mdrtypes.LogQuery) bool {
	return s.Synced.IsAvailable(query)
}

// IsPartiallyAvailable checks if the given LogQuery is partially available in the synced segments
func (s *State) IsPartiallyAvailable(query mdrtypes.LogQuery) (bool, *mdrtypes.LogQuery) {
	return s.Synced.IsPartiallyAvailable(query)
}

// GetTotalPendingBlockRange returns the total block range that is pending to be synced
func (s *State) GetTotalPendingBlockRange() *aggkitcommon.BlockRange {
	return s.Pending.GetTotalPendingBlockRange()
}

// GetAddressesToSyncForBlockNumber returns the list of addresses that have pending segments
// for the given block number
func (s *State) GetAddressesToSyncForBlockNumber(blockNumber uint64) []common.Address {
	return s.Pending.GetAddressesForBlock(blockNumber)
}

// IsSyncFinished returns true if there are no more segments pending to be synced
func (s *State) IsSyncFinished() bool {
	return s.Pending.Finished()
}

// TotalBlocksPendingToSync returns the total number of blocks that are pending to be synced
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

// SyncedSegmentsByContract returns the list of synced segments for the given contract addresses
func (s *State) SyncedSegmentsByContract(addrs []common.Address) []mdrtypes.SyncSegment {
	return s.Synced.SegmentsByContract(addrs)
}

// NextQueryToSync returns the next LogQuery to sync based on the pending segments and the given chunk size
func (s *State) NextQueryToSync(syncBlockChunkSize uint32, maxBlockNumber uint64) (*mdrtypes.LogQuery, error) {
	return s.Pending.NextQuery(syncBlockChunkSize, maxBlockNumber)
}

func (s *State) CompletionPercentage() map[common.Address]float64 {
	if s == nil {
		return nil
	}
	result := make(map[common.Address]float64)
	contracts := s.Synced.GetContracts()
	for _, contract := range contracts {
		synced, existsSynced := s.Synced.GetByContract(contract)
		if !existsSynced {
			continue
		}
		pending, existsPending := s.Pending.GetByContract(contract)
		if !existsPending {
			result[contract] = maxPercent
			continue
		}

		syncedBlocks := synced.BlockRange.CountBlocks()
		pendingBlocks := pending.BlockRange.CountBlocks()
		totalBlocks := syncedBlocks + pendingBlocks
		log.Infof("CompletionPercentage for contract %s: syncedBlocks=%d, pendingBlocks=%d, totalBlocks=%d",
			contract.Hex(), syncedBlocks, pendingBlocks, totalBlocks)
		if totalBlocks == 0 {
			result[contract] = maxPercent
		} else {
			result[contract] = (float64(syncedBlocks) / float64(totalBlocks)) * maxPercent
		}
	}
	return result
}
