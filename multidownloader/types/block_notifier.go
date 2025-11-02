package types

import (
	"context"
	"time"

	aggkittypes "github.com/agglayer/aggkit/types"
)

type EventNewBlock struct {
	BlockNumber       uint64
	BlockFinalityType aggkittypes.BlockNumberFinality
	BlockRate         time.Duration
}

// BlockNotifier is the interface that wraps the basic methods to notify a new block.
type BlockNotifier interface {
	// NotifyEpochStarted notifies the epoch has started.
	Subscribe(id string) <-chan EventNewBlock
	GetCurrentBlockNumber() uint64
	String() string
	// Initialize initializes the block notifier.
	Initialize(ctx context.Context) error
	// Start starts the block notifier.
	Start(ctx context.Context)
}
