package reorgdetector

import (
	"fmt"
	"sync"
)

// Subscription is a subscription to reorg events
type Subscription struct {
	ReorgedBlock   chan uint64
	ReorgProcessed chan bool

	// done is closed by Unsubscribe to cancel a notification that is already in
	// progress. A subscriber that has been removed must not keep the detector
	// blocked on the unbuffered ReorgedBlock/ReorgProcessed channels (see
	// notifySubscriber). It is nil for subscriptions not built by
	// newSubscription, so callers that construct the struct by hand (legacy
	// callers and tests) keep the historical blocking behaviour.
	done     chan struct{}
	doneOnce sync.Once
}

// newSubscription returns a subscription whose pending notifications can be
// cancelled with markDone.
func newSubscription() *Subscription {
	return &Subscription{
		ReorgedBlock:   make(chan uint64),
		ReorgProcessed: make(chan bool),
		done:           make(chan struct{}),
	}
}

// markDone cancels the notifications pending for this subscription. It is safe
// to call it more than once and on a subscription without a done channel (nil
// receiver or legacy subscription), so Unsubscribe can call it unconditionally.
func (s *Subscription) markDone() {
	if s == nil || s.done == nil {
		return
	}
	s.doneOnce.Do(func() {
		close(s.done)
	})
}

// Subscribe subscribes to reorg events
func (rd *ReorgDetector) Subscribe(id string) (*Subscription, error) {
	rd.subscriptionsLock.Lock()
	defer rd.subscriptionsLock.Unlock()

	if sub, ok := rd.subscriptions[id]; ok {
		return sub, nil
	}

	// Create a new subscription
	sub := newSubscription()
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
	sub := rd.subscriptions[id]
	delete(rd.subscriptions, id)
	rd.subscriptionsLock.Unlock()

	// Cancel any notification already in progress for this subscription. A
	// polling tick can be blocked notifying the subscription before Unsubscribe
	// runs (for example while a syncer is migrated from legacy to
	// multidownloader); removing it from the map is not enough to unblock it.
	sub.markDone()

	rd.trackedBlocksLock.Lock()
	delete(rd.trackedBlocks, id)
	rd.trackedBlocksLock.Unlock()

	if _, err := rd.db.Exec("DELETE FROM tracked_block WHERE subscriber_id = $1;", id); err != nil {
		return fmt.Errorf("failed to remove the tracked blocks of subscriber %s: %w", id, err)
	}
	return nil
}

// notifySubscriber notifies the subscriber with the block of the reorg.
// It gives up as soon as the subscription is cancelled with Unsubscribe, so a
// reorg detected during the startup migration window cannot block the detector
// on a subscription that nobody reads.
func (rd *ReorgDetector) notifySubscriber(id string, startingBlock Header) {
	// Notify subscriber about this particular reorg
	rd.subscriptionsLock.RLock()
	sub, ok := rd.subscriptions[id]
	rd.subscriptionsLock.RUnlock()

	if !ok {
		return
	}

	select {
	case sub.ReorgedBlock <- startingBlock.Num:
	case <-sub.done:
		// The subscriber was removed: there is nobody to notify.
		return
	}

	select {
	case <-sub.ReorgProcessed:
	case <-sub.done:
		// The subscriber was removed while it was processing the reorg: no ack
		// is expected, the reorg is considered processed.
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
