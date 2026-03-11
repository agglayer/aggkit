package types

// ClaimSyncerID represents the type of bridge syncer
type ClaimSyncerID int

const (
	L1ClaimSyncer ClaimSyncerID = iota
	L2ClaimSyncer

	// CurrentDBVersion represents the current version of the bridge syncer's database schema.
	// It is used to ensure the database is reset if an upgrade requires a full resync.
	// Increment this value whenever the database schema changes in a way that is not backward-compatible.
	CurrentDBVersion = 1
)

func (b ClaimSyncerID) String() string {
	return [...]string{"L1ClaimSyncer", "L2ClaimSyncer"}[b]
}
