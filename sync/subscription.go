package sync

import (
	"context"
	"sync"

	"github.com/agglayer/aggkit/log"
)

// BlockNotification contains the block that was successfully processed
type BlockNotification struct {
	Block Block // The raw block with events
}

// ReorgNotification contains information about a reorg that has been processed
type ReorgNotification struct {
	FirstReorgedBlock uint64 // The first block number that was reorged
}

// Subscription represents a subscription to block and reorg events
type Subscription struct {
	ID string
	// BlockCh receives notifications when blocks are successfully processed
	BlockCh chan BlockNotification
	// ReorgCh receives notifications when reorgs are successfully processed
	ReorgCh chan ReorgNotification
}

// SubscriberManager manages subscriptions and sends notifications via channels
type SubscriberManager struct {
	subscriptions map[string]*Subscription
	mu            sync.RWMutex
	log           *log.Logger
}

// NewSubscriberManager creates a new subscriber manager
func NewSubscriberManager(logger *log.Logger) *SubscriberManager {
	return &SubscriberManager{
		subscriptions: make(map[string]*Subscription),
		log:           logger,
	}
}

// Subscribe creates a new subscription with buffered channels
// bufferSize determines the channel buffer size (0 for unbuffered)
// The caller is responsible for reading from the channels and closing them when done
func (sm *SubscriberManager) Subscribe(id string, bufferSize int) *Subscription {
	if sm == nil {
		return nil
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	sub := &Subscription{
		ID:      id,
		BlockCh: make(chan BlockNotification, bufferSize),
		ReorgCh: make(chan ReorgNotification, bufferSize),
	}

	sm.subscriptions[id] = sub
	sm.log.Infof("subscriber %s registered with buffer size %d", id, bufferSize)

	return sub
}

// Unsubscribe removes a subscription and closes its channels
func (sm *SubscriberManager) Unsubscribe(id string) {
	if sm == nil {
		return
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sub, exists := sm.subscriptions[id]; exists {
		close(sub.BlockCh)
		close(sub.ReorgCh)
		delete(sm.subscriptions, id)
		sm.log.Infof("subscriber %s unregistered and channels closed", id)
	}
}

// NotifyBlockProcessed notifies all subscribers that a block has been successfully processed
// This should be called AFTER the block has been committed to the database
// Non-blocking sends are used - if a subscriber's channel is full, a warning is logged
func (sm *SubscriberManager) NotifyBlockProcessed(ctx context.Context, block Block) {
	if sm == nil {
		return
	}

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if len(sm.subscriptions) == 0 {
		return
	}

	notification := BlockNotification{
		Block: block,
	}

	// Send to all subscribers (non-blocking)
	for id, sub := range sm.subscriptions {
		select {
		case sub.BlockCh <- notification:
			// Successfully sent
		case <-ctx.Done():
			sm.log.Warnf("context canceled while notifying subscriber %s of block %d", id, block.Num)
			return
		default:
			// Channel is full, log warning but don't block
			sm.log.Warnf("subscriber %s channel is full, dropping block %d notification", id, block.Num)
		}
	}
}

// NotifyReorg notifies all subscribers about a reorg
// This should be called AFTER the reorg has been successfully processed
// Non-blocking sends are used - if a subscriber's channel is full, a warning is logged
func (sm *SubscriberManager) NotifyReorg(ctx context.Context, firstReorgedBlock uint64) {
	if sm == nil {
		return
	}

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if len(sm.subscriptions) == 0 {
		return
	}

	notification := ReorgNotification{
		FirstReorgedBlock: firstReorgedBlock,
	}

	// Send to all subscribers (non-blocking)
	for id, sub := range sm.subscriptions {
		select {
		case sub.ReorgCh <- notification:
			// Successfully sent
		case <-ctx.Done():
			sm.log.Warnf("context canceled while notifying subscriber %s of reorg at block %d", id, firstReorgedBlock)
			return
		default:
			// Channel is full, log warning but don't block
			sm.log.Warnf("subscriber %s channel is full, dropping reorg notification for block %d", id, firstReorgedBlock)
		}
	}
}
