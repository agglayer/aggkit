package storage

import "fmt"

const (
	// DataVersionCurrent is the current data version
	DataVersionCurrent = 1
)

type DBRuntimeData struct {
	NetworkID   uint64
	DataVersion int
}

func (r DBRuntimeData) String() string {
	return fmt.Sprintf("NetworkID: %d, DataVersion: %d", r.NetworkID, r.DataVersion)
}
func (r DBRuntimeData) IsCompatible(storage DBRuntimeData) (*DBRuntimeData, error) {
	if r.NetworkID != storage.NetworkID {
		return nil, fmt.Errorf("network ID mismatch: expected: %d != storage: %d", r.NetworkID, storage.NetworkID)
	}
	if r.DataVersion != storage.DataVersion {
		return nil, fmt.Errorf("data version mismatch: expected: %d != storage: %d", r.DataVersion, storage.DataVersion)
	}
	return nil, nil
}
