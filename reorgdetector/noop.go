package reorgdetector

import (
	"context"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// NoOpReorgDetector is a no-operation implementation of sync.ReorgDetector
// that does nothing when called, used when reorg detection is not needed
type NoOpReorgDetector struct {
}

// NewNoOpReorgDetector creates a new no-op reorg detector
func NewNoOpReorgDetector() *NoOpReorgDetector {
	return &NoOpReorgDetector{}
}

// Subscribe implements sync.ReorgDetector interface
func (n *NoOpReorgDetector) Subscribe(id string) (*Subscription, error) {
	// Return a no-op subscription that does nothing
	return &Subscription{
		ReorgedBlock:   make(chan uint64),
		ReorgProcessed: make(chan bool),
	}, nil
}

// AddBlockToTrack implements sync.ReorgDetector interface
func (n *NoOpReorgDetector) AddBlockToTrack(
	ctx context.Context,
	id string,
	blockNum uint64,
	blockHash common.Hash,
) error {
	// No-op: do nothing
	return nil
}

// GetFinalizedBlockType implements sync.ReorgDetector interface
func (n *NoOpReorgDetector) GetFinalizedBlockType() aggkittypes.BlockNumberFinality {
	return aggkittypes.FinalizedBlock
}

// String implements sync.ReorgDetector interface
func (n *NoOpReorgDetector) String() string {
	return "NoOpReorgDetector"
}

// GetLastReorgEvent returns an empty reorg event since no-op detector never detects reorgs
func (n *NoOpReorgDetector) GetLastReorgEvent(ctx context.Context) (ReorgEvent, error) {
	// Return empty reorg event since no-op detector never detects reorgs
	return ReorgEvent{}, nil
}

// GetTrackedBlockByBlockNumber implements sync.ReorgDetector interface
func (n *NoOpReorgDetector) GetTrackedBlockByBlockNumber(id string, blockNumber uint64) (*Header, error) {
	// Return nil since no-op detector never detects reorgs
	return nil, nil
}
