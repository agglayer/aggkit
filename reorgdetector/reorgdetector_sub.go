package reorgdetector

import "fmt"

// Subscription is a subscription to reorg events
type Subscription struct {
	ReorgedBlock   chan uint64
	ReorgProcessed chan bool
}

// Subscribe subscribes to reorg events
func (rd *ReorgDetector) Subscribe(id string) (*Subscription, error) {
	rd.subscriptionsLock.Lock()
	defer rd.subscriptionsLock.Unlock()

	if sub, ok := rd.subscriptions[id]; ok {
		return sub, nil
	}

	// Create a new subscription
	sub := &Subscription{
		ReorgedBlock:   make(chan uint64),
		ReorgProcessed: make(chan bool),
	}
	rd.subscriptions[id] = sub

	// Create a new tracked blocks list for the subscriber
	rd.trackedBlocksLock.Lock()
	rd.trackedBlocks[id] = newHeadersList()
	rd.trackedBlocksLock.Unlock()

	return sub, nil
}

// Unsubscribe removes the subscriber and stops tracking its blocks, both in memory and in the DB.
// It is meant for a syncer that used to be subscribed (so the DB can still have tracked blocks of it,
// that are loaded as a subscription on start) but no longer relies on this reorg detector: without
// removing it, a reorg of one of those blocks would be notified to a subscription that nobody reads,
// which blocks the detection of reorgs.
// It is a no-op if the id is not subscribed.
func (rd *ReorgDetector) Unsubscribe(id string) error {
	if rd == nil {
		return nil
	}
	rd.subscriptionsLock.Lock()
	delete(rd.subscriptions, id)
	rd.subscriptionsLock.Unlock()

	rd.trackedBlocksLock.Lock()
	delete(rd.trackedBlocks, id)
	rd.trackedBlocksLock.Unlock()

	if _, err := rd.db.Exec("DELETE FROM tracked_block WHERE subscriber_id = $1;", id); err != nil {
		return fmt.Errorf("failed to remove the tracked blocks of subscriber %s: %w", id, err)
	}
	return nil
}

// notifySubscriber notifies the subscriber with the block of the reorg
func (rd *ReorgDetector) notifySubscriber(id string, startingBlock Header) {
	// Notify subscriber about this particular reorg
	rd.subscriptionsLock.RLock()
	sub, ok := rd.subscriptions[id]
	rd.subscriptionsLock.RUnlock()

	if ok {
		sub.ReorgedBlock <- startingBlock.Num
		<-sub.ReorgProcessed
	}
}

// getSubscriberIDs returns a list of subscriber IDs
func (rd *ReorgDetector) getSubscriberIDs() []string {
	rd.subscriptionsLock.RLock()
	defer rd.subscriptionsLock.RUnlock()

	ids := make([]string, 0, len(rd.subscriptions))
	for id := range rd.subscriptions {
		ids = append(ids, id)
	}

	return ids
}
