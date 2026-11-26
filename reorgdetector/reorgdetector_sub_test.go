package reorgdetector

import (
	"context"
	"path"
	"testing"
	"time"

	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/etherman"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/stretchr/testify/require"
)

// newNotifyTestDetector returns a reorg detector with the given subscriber and no polling, so
// notifySubscriber and Unsubscribe can be exercised directly.
func newNotifyTestDetector(t *testing.T, id string) (*ReorgDetector, *Subscription) {
	t.Helper()

	clientL1 := simulated.NewBackend(nil, simulated.WithBlockGasLimit(10000000))
	rd, err := New(etherman.NewDefaultEthClient(clientL1.Client(), nil, nil), Config{
		DBPath:              path.Join(t.TempDir(), "reorgdetectorNotifyTest.sqlite"),
		CheckReorgsInterval: cfgtypes.NewDuration(time.Hour),
	}, L1)
	require.NoError(t, err)

	sub, err := rd.Subscribe(id)
	require.NoError(t, err)

	return rd, sub
}

// requireStillBlocked fails if done is closed within a short window, i.e. notifySubscriber returned
// before it was unsubscribed.
func requireStillBlocked(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
		t.Fatal("notifySubscriber returned before Unsubscribe")
	case <-time.After(100 * time.Millisecond):
	}
}

// requireReturned fails if done is not closed within a generous window.
func requireReturned(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("notifySubscriber did not return after Unsubscribe")
	}
}

// TestNotifySubscriber_UnsubscribeUnblocksSend covers the case reported by the reviewer: the
// detector is blocked sending on the unbuffered ReorgedBlock of a subscription nobody reads (a
// syncer migrated from legacy to multidownloader). Unsubscribe must unblock it.
func TestNotifySubscriber_UnsubscribeUnblocksSend(t *testing.T) {
	const id = "send-syncer"
	rd, _ := newNotifyTestDetector(t, id)

	notified := make(chan struct{})
	go func() {
		rd.notifySubscriber(id, Header{Num: 5, Hash: common.HexToHash("05")})
		close(notified)
	}()

	requireStillBlocked(t, notified)

	require.NoError(t, rd.Unsubscribe(id))
	requireReturned(t, notified)
}

// TestNotifySubscriber_UnsubscribeUnblocksAck covers the second wait of notifySubscriber: the
// subscriber took the block but never acknowledged it. Unsubscribe must unblock the detector
// without requiring the ack.
func TestNotifySubscriber_UnsubscribeUnblocksAck(t *testing.T) {
	const id = "ack-syncer"
	rd, sub := newNotifyTestDetector(t, id)

	received := make(chan struct{})
	go func() {
		<-sub.ReorgedBlock // take the reorg but never acknowledge it
		close(received)
	}()

	notified := make(chan struct{})
	go func() {
		rd.notifySubscriber(id, Header{Num: 5, Hash: common.HexToHash("05")})
		close(notified)
	}()

	// Wait until notifySubscriber handed the block over and is waiting for the ack.
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("notifySubscriber did not send the reorg")
	}

	requireStillBlocked(t, notified)

	require.NoError(t, rd.Unsubscribe(id))
	requireReturned(t, notified)
}

// TestNotifySubscriber_UnsubscribeUnblocksRestoredSubscription covers the exact startup window of
// the migration: the subscription is restored from the tracked blocks in the DB (as Start does) and
// nobody reads it yet. Unsubscribe must unblock the detector.
func TestNotifySubscriber_UnsubscribeUnblocksRestoredSubscription(t *testing.T) {
	ctx := context.Background()
	const id = "restored-syncer"

	clientL1 := simulated.NewBackend(nil, simulated.WithBlockGasLimit(10000000))
	defer clientL1.Close()

	cfg := Config{
		DBPath:              path.Join(t.TempDir(), "reorgdetectorRestoredSub.sqlite"),
		CheckReorgsInterval: cfgtypes.NewDuration(time.Hour),
	}
	seeded, err := New(etherman.NewDefaultEthClient(clientL1.Client(), nil, nil), cfg, L1)
	require.NoError(t, err)
	_, err = seeded.Subscribe(id)
	require.NoError(t, err)
	require.NoError(t, seeded.AddBlockToTrack(ctx, id, 5, common.HexToHash("05")))

	// A detector restarted over the same DB restores the subscription from the tracked blocks.
	restored, err := New(etherman.NewDefaultEthClient(clientL1.Client(), nil, nil), cfg, L1)
	require.NoError(t, err)
	require.NoError(t, restored.loadTrackedHeaders())

	restoredSub, ok := restored.subscriptions[id]
	require.True(t, ok)
	require.NotNil(t, restoredSub.done, "a subscription restored from the DB must be cancellable")

	notified := make(chan struct{})
	go func() {
		restored.notifySubscriber(id, Header{Num: 5, Hash: common.HexToHash("05")})
		close(notified)
	}()

	requireStillBlocked(t, notified)

	require.NoError(t, restored.Unsubscribe(id))
	requireReturned(t, notified)
}

// TestNotifySubscriber_Acknowledged checks that a subscriber that reads and acknowledges the reorg
// keeps the previous behaviour.
func TestNotifySubscriber_Acknowledged(t *testing.T) {
	const id = "normal-syncer"
	rd, sub := newNotifyTestDetector(t, id)

	go func() {
		<-sub.ReorgedBlock
		sub.ReorgProcessed <- true
	}()

	notified := make(chan struct{})
	go func() {
		rd.notifySubscriber(id, Header{Num: 7, Hash: common.HexToHash("07")})
		close(notified)
	}()

	requireReturned(t, notified)
}

// TestDetectReorgInTrackedList_UnsubscribeUnblocksNotification reproduces the reviewer scenario end
// to end: a reorg is detected for a restored subscription nobody reads, the detection pass blocks on
// the notification, and Unsubscribe lets it finish.
func TestDetectReorgInTrackedList_UnsubscribeUnblocksNotification(t *testing.T) {
	ctx := t.Context()
	const id = "detect-restored-syncer"

	clientL1 := simulated.NewBackend(nil, simulated.WithBlockGasLimit(10000000))
	defer clientL1.Close()

	cfg := Config{
		DBPath:              path.Join(t.TempDir(), "reorgdetectorDetectRestored.sqlite"),
		CheckReorgsInterval: cfgtypes.NewDuration(time.Hour),
		FinalizedBlock:      aggkittypes.FinalizedBlock,
	}
	rd, err := New(etherman.NewDefaultEthClient(clientL1.Client(), nil, nil), cfg, L1)
	require.NoError(t, err)
	_, err = rd.Subscribe(id)
	require.NoError(t, err)

	hashes := make([]common.Hash, 0, 3)
	for num := uint64(1); num <= 3; num++ {
		hash := clientL1.Commit()
		hashes = append(hashes, hash)
		require.NoError(t, rd.AddBlockToTrack(ctx, id, num, hash))
	}

	// Fork from block 1 and build a longer chain so the tracked block 2 is reorged.
	require.NoError(t, clientL1.Fork(hashes[0]))
	clientL1.Commit() // block 2 after the reorg
	clientL1.Commit() // block 3 after the reorg
	clientL1.Commit() // block 4 after the reorg

	// A detector restarted over the same DB restores the subscription from the tracked blocks.
	restored, err := New(etherman.NewDefaultEthClient(clientL1.Client(), nil, nil), cfg, L1)
	require.NoError(t, err)
	require.NoError(t, restored.loadTrackedHeaders())

	detected := make(chan error, 1)
	go func() {
		detected <- restored.detectReorgInTrackedList(ctx)
	}()

	// The pass blocks notifying the restored subscription that nobody reads.
	select {
	case err := <-detected:
		t.Fatalf("detectReorgInTrackedList returned before Unsubscribe: %v", err)
	case <-time.After(500 * time.Millisecond):
	}

	require.NoError(t, restored.Unsubscribe(id))

	select {
	case err := <-detected:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("detectReorgInTrackedList remained blocked after Unsubscribe")
	}
}

// TestUnsubscribe_LegacySubscriptionWithoutDone checks that Unsubscribe does not panic on a
// subscription built by hand (legacy caller or test) without a done channel, and stays a no-op when
// called twice.
func TestUnsubscribe_LegacySubscriptionWithoutDone(t *testing.T) {
	const id = "legacy-syncer"
	rd, _ := newNotifyTestDetector(t, "placeholder-syncer")

	rd.subscriptionsLock.Lock()
	rd.subscriptions[id] = &Subscription{
		ReorgedBlock:   make(chan uint64),
		ReorgProcessed: make(chan bool),
	}
	rd.subscriptionsLock.Unlock()

	rd.trackedBlocksLock.Lock()
	rd.trackedBlocks[id] = newHeadersList()
	rd.trackedBlocksLock.Unlock()

	require.NotPanics(t, func() {
		require.NoError(t, rd.Unsubscribe(id))
	})
	// A second unsubscribe is a no-op and must not close the done channel again.
	require.NotPanics(t, func() {
		require.NoError(t, rd.Unsubscribe(id))
	})
}

// TestUnsubscribe_DoubleCloseWithDone checks that calling Unsubscribe twice on a subscription with a
// done channel does not panic (double close) and keeps the other subscribers untouched.
func TestUnsubscribe_DoubleCloseWithDone(t *testing.T) {
	const removedID, keptID = "removed-syncer", "kept-syncer"
	rd, removedSub := newNotifyTestDetector(t, removedID)
	_, err := rd.Subscribe(keptID)
	require.NoError(t, err)

	require.NoError(t, rd.Unsubscribe(removedID))
	require.NotPanics(t, func() {
		require.NoError(t, rd.Unsubscribe(removedID))
	})

	// The done channel of the removed subscription is closed exactly once.
	select {
	case <-removedSub.done:
	default:
		t.Fatal("the done channel of the removed subscription was not closed")
	}
	require.NotContains(t, rd.getSubscriberIDs(), removedID)
	require.Contains(t, rd.getSubscriberIDs(), keptID)
}
