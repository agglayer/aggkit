package sync

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestNewSubscriberManager(t *testing.T) {
	logger := log.WithFields("test", "subscription")
	sm := NewSubscriberManager(logger)

	require.NotNil(t, sm)
	require.NotNil(t, sm.subscriptions)
	require.Equal(t, 0, len(sm.subscriptions))
	require.Equal(t, logger, sm.log)
}

func TestSubscribe(t *testing.T) {
	t.Run("Subscribe with buffered channel", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)

		sub := sm.Subscribe("test-subscriber", 10)

		require.NotNil(t, sub)
		require.Equal(t, "test-subscriber", sub.ID)
		require.NotNil(t, sub.BlockCh)
		require.NotNil(t, sub.ReorgCh)
		require.Equal(t, 10, cap(sub.BlockCh))
		require.Equal(t, 10, cap(sub.ReorgCh))
	})

	t.Run("Subscribe with unbuffered channel", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)

		sub := sm.Subscribe("test-subscriber", 0)

		require.NotNil(t, sub)
		require.Equal(t, 0, cap(sub.BlockCh))
		require.Equal(t, 0, cap(sub.ReorgCh))
	})

	t.Run("Subscribe multiple subscribers", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)

		sub1 := sm.Subscribe("subscriber-1", 5)
		sub2 := sm.Subscribe("subscriber-2", 10)
		sub3 := sm.Subscribe("subscriber-3", 15)

		require.Equal(t, 3, len(sm.subscriptions))
		require.Equal(t, "subscriber-1", sub1.ID)
		require.Equal(t, "subscriber-2", sub2.ID)
		require.Equal(t, "subscriber-3", sub3.ID)
	})

	t.Run("Subscribe with nil manager returns nil", func(t *testing.T) {
		var sm *SubscriberManager
		sub := sm.Subscribe("test", 10)
		require.Nil(t, sub)
	})

	t.Run("Subscribe replaces existing subscription with same ID", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)

		sub1 := sm.Subscribe("same-id", 5)
		sub2 := sm.Subscribe("same-id", 10)

		require.Equal(t, 1, len(sm.subscriptions))
		require.Equal(t, 10, cap(sub2.BlockCh))
		require.NotEqual(t, sub1.BlockCh, sub2.BlockCh)
	})
}

func TestUnsubscribe(t *testing.T) {
	t.Run("Unsubscribe existing subscriber", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)

		sub := sm.Subscribe("test-subscriber", 10)
		require.NotNil(t, sub)
		require.Equal(t, 1, len(sm.subscriptions))

		sm.Unsubscribe("test-subscriber")

		require.Equal(t, 0, len(sm.subscriptions))

		// Verify channels are closed
		_, ok := <-sub.BlockCh
		require.False(t, ok, "BlockCh should be closed")
		_, ok = <-sub.ReorgCh
		require.False(t, ok, "ReorgCh should be closed")
	})

	t.Run("Unsubscribe non-existent subscriber", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)

		// Should not panic
		sm.Unsubscribe("non-existent")
		require.Equal(t, 0, len(sm.subscriptions))
	})

	t.Run("Unsubscribe with nil manager", func(t *testing.T) {
		var sm *SubscriberManager
		// Should not panic
		sm.Unsubscribe("test")
	})

	t.Run("Unsubscribe one of multiple subscribers", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)

		sub1 := sm.Subscribe("subscriber-1", 5)
		sub2 := sm.Subscribe("subscriber-2", 5)
		sm.Subscribe("subscriber-3", 5)

		require.Equal(t, 3, len(sm.subscriptions))

		sm.Unsubscribe("subscriber-2")

		require.Equal(t, 2, len(sm.subscriptions))
		require.NotNil(t, sm.subscriptions["subscriber-1"])
		require.Nil(t, sm.subscriptions["subscriber-2"])
		require.NotNil(t, sm.subscriptions["subscriber-3"])

		// Verify only sub2 channels are closed
		_, ok := <-sub2.BlockCh
		require.False(t, ok)

		// sub1 should still be open
		select {
		case <-sub1.BlockCh:
			t.Fatal("sub1.BlockCh should not be closed")
		default:
			// Expected - channel is still open
		}
	})
}

func TestNotifyBlockProcessed(t *testing.T) {
	t.Run("Notify single subscriber", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)
		ctx := context.Background()

		sub := sm.Subscribe("test-subscriber", 10)
		require.NotNil(t, sub)

		testBlock := Block{
			Num:    100,
			Hash:   common.HexToHash("0x123"),
			Events: []any{},
		}

		sm.NotifyBlockProcessed(ctx, testBlock)

		// Receive notification
		select {
		case notification := <-sub.BlockCh:
			require.Equal(t, uint64(100), notification.Block.Num)
			require.Equal(t, common.HexToHash("0x123"), notification.Block.Hash)
		case <-time.After(1 * time.Second):
			t.Fatal("timeout waiting for block notification")
		}
	})

	t.Run("Notify multiple subscribers", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)
		ctx := context.Background()

		sub1 := sm.Subscribe("subscriber-1", 10)
		sub2 := sm.Subscribe("subscriber-2", 10)
		sub3 := sm.Subscribe("subscriber-3", 10)

		testBlock := Block{
			Num:    200,
			Hash:   common.HexToHash("0x456"),
			Events: []any{"event1", "event2"},
		}

		sm.NotifyBlockProcessed(ctx, testBlock)

		// All subscribers should receive the notification
		for _, sub := range []*Subscription{sub1, sub2, sub3} {
			select {
			case notification := <-sub.BlockCh:
				require.Equal(t, uint64(200), notification.Block.Num)
				require.Equal(t, 2, len(notification.Block.Events))
			case <-time.After(1 * time.Second):
				t.Fatalf("timeout waiting for block notification on %s", sub.ID)
			}
		}
	})

	t.Run("Notify with nil manager", func(t *testing.T) {
		var sm *SubscriberManager
		ctx := context.Background()
		testBlock := Block{Num: 100}

		// Should not panic
		sm.NotifyBlockProcessed(ctx, testBlock)
	})

	t.Run("Notify with no subscribers", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)
		ctx := context.Background()

		testBlock := Block{Num: 100}

		// Should not panic or block
		sm.NotifyBlockProcessed(ctx, testBlock)
	})

	t.Run("Notify with full channel (non-blocking)", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)
		ctx := context.Background()

		// Create subscriber with buffer size of 1
		sub := sm.Subscribe("test-subscriber", 1)

		// Fill the buffer
		testBlock1 := Block{Num: 100}
		sm.NotifyBlockProcessed(ctx, testBlock1)

		// Try to send another notification (should not block)
		testBlock2 := Block{Num: 101}
		done := make(chan bool)
		go func() {
			sm.NotifyBlockProcessed(ctx, testBlock2)
			done <- true
		}()

		// Should complete immediately (non-blocking)
		select {
		case <-done:
			// Success - notification was non-blocking
		case <-time.After(500 * time.Millisecond):
			t.Fatal("NotifyBlockProcessed blocked when channel was full")
		}

		// Drain the channel and verify only first notification was delivered
		notification := <-sub.BlockCh
		require.Equal(t, uint64(100), notification.Block.Num)

		// No more notifications should be in the channel
		select {
		case <-sub.BlockCh:
			t.Fatal("unexpected notification in channel")
		default:
			// Expected - channel is empty
		}
	})

	t.Run("Notify with canceled context", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)
		ctx, cancel := context.WithCancel(context.Background())

		sub1 := sm.Subscribe("subscriber-1", 0) // Unbuffered
		sm.Subscribe("subscriber-2", 0)         // Unbuffered

		cancel() // Cancel context before notifying

		testBlock := Block{Num: 100}

		// Should return early due to canceled context
		sm.NotifyBlockProcessed(ctx, testBlock)

		// Verify no notifications were sent
		select {
		case <-sub1.BlockCh:
			t.Fatal("notification should not be sent with canceled context")
		default:
			// Expected - no notification
		}
	})
}

func TestNotifyReorg(t *testing.T) {
	t.Run("Notify single subscriber of reorg", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)
		ctx := context.Background()

		sub := sm.Subscribe("test-subscriber", 10)
		require.NotNil(t, sub)

		sm.NotifyReorg(ctx, 500)

		// Receive notification
		select {
		case notification := <-sub.ReorgCh:
			require.Equal(t, uint64(500), notification.FirstReorgedBlock)
		case <-time.After(1 * time.Second):
			t.Fatal("timeout waiting for reorg notification")
		}
	})

	t.Run("Notify multiple subscribers of reorg", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)
		ctx := context.Background()

		sub1 := sm.Subscribe("subscriber-1", 10)
		sub2 := sm.Subscribe("subscriber-2", 10)
		sub3 := sm.Subscribe("subscriber-3", 10)

		sm.NotifyReorg(ctx, 1000)

		// All subscribers should receive the notification
		for _, sub := range []*Subscription{sub1, sub2, sub3} {
			select {
			case notification := <-sub.ReorgCh:
				require.Equal(t, uint64(1000), notification.FirstReorgedBlock)
			case <-time.After(1 * time.Second):
				t.Fatalf("timeout waiting for reorg notification on %s", sub.ID)
			}
		}
	})

	t.Run("Notify reorg with nil manager", func(t *testing.T) {
		var sm *SubscriberManager
		ctx := context.Background()

		// Should not panic
		sm.NotifyReorg(ctx, 100)
	})

	t.Run("Notify reorg with no subscribers", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)
		ctx := context.Background()

		// Should not panic or block
		sm.NotifyReorg(ctx, 100)
	})

	t.Run("Notify reorg with full channel (non-blocking)", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)
		ctx := context.Background()

		// Create subscriber with buffer size of 1
		sub := sm.Subscribe("test-subscriber", 1)

		// Fill the buffer
		sm.NotifyReorg(ctx, 100)

		// Try to send another notification (should not block)
		done := make(chan bool)
		go func() {
			sm.NotifyReorg(ctx, 200)
			done <- true
		}()

		// Should complete immediately (non-blocking)
		select {
		case <-done:
			// Success - notification was non-blocking
		case <-time.After(500 * time.Millisecond):
			t.Fatal("NotifyReorg blocked when channel was full")
		}

		// Drain the channel and verify only first notification was delivered
		notification := <-sub.ReorgCh
		require.Equal(t, uint64(100), notification.FirstReorgedBlock)

		// No more notifications should be in the channel
		select {
		case <-sub.ReorgCh:
			t.Fatal("unexpected notification in channel")
		default:
			// Expected - channel is empty
		}
	})

	t.Run("Notify reorg with canceled context", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)
		ctx, cancel := context.WithCancel(context.Background())

		sub1 := sm.Subscribe("subscriber-1", 0) // Unbuffered
		sm.Subscribe("subscriber-2", 0)         // Unbuffered

		cancel() // Cancel context before notifying

		// Should return early due to canceled context
		sm.NotifyReorg(ctx, 100)

		// Verify no notifications were sent
		select {
		case <-sub1.ReorgCh:
			t.Fatal("notification should not be sent with canceled context")
		default:
			// Expected - no notification
		}
	})
}

func TestConcurrency(t *testing.T) {
	t.Run("Concurrent subscribe and unsubscribe", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)

		var wg sync.WaitGroup
		numGoroutines := 100

		// Concurrent subscribes
		for i := range numGoroutines {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				sm.Subscribe(fmt.Sprintf("subscriber-%d", id), 10)
			}(i)
		}

		wg.Wait()

		// Concurrent unsubscribes
		for i := range numGoroutines {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				sm.Unsubscribe(fmt.Sprintf("subscriber-%d", id))
			}(i)
		}

		wg.Wait()

		require.Equal(t, 0, len(sm.subscriptions))
	})

	t.Run("Concurrent notifications", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)
		ctx := context.Background()

		sub := sm.Subscribe("test-subscriber", 1000)

		var wg sync.WaitGroup
		numNotifications := 100

		// Send notifications concurrently
		for i := range numNotifications {
			wg.Add(1)
			go func(blockNum uint64) {
				defer wg.Done()
				sm.NotifyBlockProcessed(ctx, Block{Num: blockNum})
			}(uint64(i))
		}

		wg.Go(func() {
			for i := range numNotifications {
				sm.NotifyReorg(ctx, uint64(i*10))
			}
		})

		wg.Wait()

		// Verify we received notifications (may not be all due to non-blocking)
		blockCount := 0
		reorgCount := 0

	loop:
		for {
			select {
			case <-sub.BlockCh:
				blockCount++
			case <-sub.ReorgCh:
				reorgCount++
			case <-time.After(100 * time.Millisecond):
				break loop
			}
		}

		// We should have received at least some notifications
		require.Greater(t, blockCount, 0, "should receive at least one block notification")
		require.Greater(t, reorgCount, 0, "should receive at least one reorg notification")
	})

	t.Run("Concurrent subscribe and notify", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)
		ctx := context.Background()

		var wg sync.WaitGroup

		// Goroutines subscribing
		for i := range 50 {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				sub := sm.Subscribe(fmt.Sprintf("subscriber-%d", id), 100)
				if sub != nil {
					// Read some notifications
					for range 10 {
						select {
						case <-sub.BlockCh:
						case <-sub.ReorgCh:
						case <-time.After(10 * time.Millisecond):
							return
						}
					}
				}
			}(i)
		}

		// Goroutines notifying
		for i := range 50 {
			wg.Add(1)
			go func(blockNum uint64) {
				defer wg.Done()
				sm.NotifyBlockProcessed(ctx, Block{Num: blockNum})
				sm.NotifyReorg(ctx, blockNum)
			}(uint64(i))
		}

		wg.Wait()
		// If we get here without deadlock, test passes
	})
}

func TestSubscriptionChannelBehavior(t *testing.T) {
	t.Run("Channels remain open until unsubscribe", func(t *testing.T) {
		logger := log.WithFields("test", "subscription")
		sm := NewSubscriberManager(logger)
		ctx := context.Background()

		sub := sm.Subscribe("test-subscriber", 10)

		// Send multiple notifications
		for i := 0; i < 5; i++ {
			sm.NotifyBlockProcessed(ctx, Block{Num: uint64(i)})
		}

		// Should be able to receive all notifications
		for i := range 5 {
			select {
			case notification := <-sub.BlockCh:
				require.Equal(t, uint64(i), notification.Block.Num)
			case <-time.After(1 * time.Second):
				t.Fatal("timeout waiting for notification")
			}
		}

		// Unsubscribe closes channels
		sm.Unsubscribe("test-subscriber")

		_, ok := <-sub.BlockCh
		require.False(t, ok, "channel should be closed after unsubscribe")
	})
}
