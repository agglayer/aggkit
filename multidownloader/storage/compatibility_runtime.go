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
func (r DBRuntimeData) IsCompatible(storage DBRuntimeData) error {
	if r.NetworkID != storage.NetworkID {
		return fmt.Errorf("network ID mismatch: %d != %d", r.NetworkID, storage.NetworkID)
	}
	return nil
}
