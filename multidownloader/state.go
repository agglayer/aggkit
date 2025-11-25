package multidownloader

import mdrtypes "github.com/agglayer/aggkit/multidownloader/types"

type State struct {
	SyncedSegments mdrtypes.SetSyncSegment
	PendingSync    mdrtypes.SetSyncSegment
}

func NewState(syncedSegments *mdrtypes.SetSyncSegment, pendingSync *mdrtypes.SetSyncSegment) *State {
	return &State{
		SyncedSegments: *syncedSegments,
		PendingSync:    *pendingSync,
	}
}

func (s *State) Clone() *State {
	return &State{
		SyncedSegments: s.SyncedSegments,
		PendingSync:    s.PendingSync,
	}
}
