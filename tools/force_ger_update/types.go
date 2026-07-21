package force_ger_update

import (
	"context"
	"time"

	aggoracletypes "github.com/agglayer/aggkit/aggoracle/types"
)

// GERUpdateEvent represents an observed UpdateL1InfoTree event on L1: the block in which the GER
// was updated and that block's timestamp (used to reset the "time since last GER update" clock).
type GERUpdateEvent struct {
	// BlockNumber is the L1 block number in which the UpdateL1InfoTree event was emitted.
	BlockNumber uint64
	// BlockTimestamp is the timestamp (wall-clock, from the block header) of BlockNumber.
	BlockTimestamp time.Time
}

// GERMonitor watches L1 for UpdateL1InfoTree events without relying on any syncer, reorg detector,
// or persistent storage: a boot-time scan establishes the last known GER update, and Start streams
// every subsequently observed event so the caller can reset its elapsed-time timer.
type GERMonitor interface {
	// LastGERUpdate performs a boot-time (backward, chunked FilterLogs) scan for the most recent
	// UpdateL1InfoTree event on L1 and returns the timestamp of the block in which it was emitted.
	// If no such event is found within the configured lookback window, implementations return the
	// zero time.Time (no error) — the caller treats that as "stale" and forces an update on the
	// first tick.
	LastGERUpdate() (time.Time, error)

	// Start begins watching (WS subscription) or polling (FilterLogs) for new UpdateL1InfoTree
	// events, depending on configuration, and returns a channel on which every observed event is
	// delivered in order. The returned channel is closed once ctx is cancelled.
	Start(ctx context.Context) (<-chan GERUpdateEvent, error)
}

// ForcedUpdateSender sends the forced-GER-update bridgeMessage transaction on L1
// (forceUpdateGlobalExitRoot = true) through the ethtxmanager.
type ForcedUpdateSender interface {
	// SendForcedGERUpdate submits (or, in DryRun mode, only logs) a bridgeMessage transaction with
	// forceUpdateGlobalExitRoot = true, and waits for it to be mined before returning.
	SendForcedGERUpdate(ctx context.Context) error
}

// EthTxManager is the narrow ethtxmanager interface (Add/Result/From/...) used to submit and track
// the forced-update transaction. It is a reference to the interface already defined in
// aggoracle/types/types.go — not a redefinition — so the sender (S3) depends on the same
// mockable contract the rest of the codebase uses.
type EthTxManager = aggoracletypes.EthTxManager
