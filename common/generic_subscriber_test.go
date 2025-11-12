package common

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGenericSubscriber_Subscribe(t *testing.T) {
	t.Run("single subscriber", func(t *testing.T) {
		sub := NewGenericSubscriber[string]()

		ch := sub.Subscribe("subscriber1")

		require.NotNil(t, ch)
		require.Len(t, sub.subs, 1)

		// Verify the channel is in the map with correct name
		found := false
		for subscriberCh, name := range sub.subs {
			if subscriberCh == ch && name == "subscriber1" {
				found = true
				break
			}
		}
		require.True(t, found, "Subscriber channel should be in the map with correct name")
	})

	t.Run("multiple subscribers", func(t *testing.T) {
		sub := NewGenericSubscriber[int]()

		ch1 := sub.Subscribe("subscriber1")
		ch2 := sub.Subscribe("subscriber2")
		ch3 := sub.Subscribe("subscriber3")

		require.NotNil(t, ch1)
		require.NotNil(t, ch2)
		require.NotNil(t, ch3)
		require.Len(t, sub.subs, 3)

		// Verify all names are stored correctly
		expectedNames := map[string]bool{
			"subscriber1": false,
			"subscriber2": false,
			"subscriber3": false,
		}

		for _, name := range sub.subs {
			if _, exists := expectedNames[name]; exists {
				expectedNames[name] = true
			}
		}

		for name, found := range expectedNames {
			require.True(t, found, "Subscriber %s should be found", name)
		}
	})

	t.Run("same subscriber name multiple times", func(t *testing.T) {
		sub := NewGenericSubscriber[string]()

		ch1 := sub.Subscribe("same_name")
		ch2 := sub.Subscribe("same_name")

		require.NotNil(t, ch1)
		require.NotNil(t, ch2)
		require.NotEqual(t, ch1, ch2)
		require.Len(t, sub.subs, 2)

		// Both channels should have the same name
		count := 0
		for _, name := range sub.subs {
			if name == "same_name" {
				count++
			}
		}
		require.Equal(t, 2, count)
	})
}

func TestGenericSubscriber_Publish(t *testing.T) {
	t.Run("publish to single subscriber", func(t *testing.T) {
		sub := NewGenericSubscriber[string]()
		ch := sub.Subscribe("subscriber1")

		testData := "test message"

		// Publish in a goroutine to avoid blocking
		sub.Publish(testData)

		// Receive the published data
		select {
		case received := <-ch:
			require.Equal(t, testData, received)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Expected to receive published data within timeout")
		}
	})

	t.Run("publish to multiple subscribers", func(t *testing.T) {
		sub := NewGenericSubscriber[int]()
		ch1 := sub.Subscribe("subscriber1")
		ch2 := sub.Subscribe("subscriber2")
		ch3 := sub.Subscribe("subscriber3")

		testData := 42

		// Publish in a goroutine to avoid blocking
		sub.Publish(testData)

		// All subscribers should receive the data
		received := make([]int, 0, 3)

		for range 3 {
			select {
			case data := <-ch1:
				received = append(received, data)
				ch1 = nil // Mark as received
			case data := <-ch2:
				received = append(received, data)
				ch2 = nil // Mark as received
			case data := <-ch3:
				received = append(received, data)
				ch3 = nil // Mark as received
			case <-time.After(100 * time.Millisecond):
				t.Fatal("Expected to receive published data within timeout")
			}
		}

		require.Len(t, received, 3)
		for _, data := range received {
			require.Equal(t, testData, data)
		}
	})

	t.Run("publish with no subscribers", func(t *testing.T) {
		sub := NewGenericSubscriber[string]()

		// This should not panic or block
		sub.Publish("test message")

		// Verify no channels exist
		require.Empty(t, sub.subs)
	})
}

func TestGenericSubscriber_ConcurrentAccess(t *testing.T) {
	sub := NewGenericSubscriber[string]()

	numMessages := 20
	numSubscribers := 5
	timeout := 2 * time.Second

	// Channel to collect results from each subscriber
	results := make(chan map[string]bool, numSubscribers)

	// Start subscribers first
	var subscriberWg sync.WaitGroup
	for i := range numSubscribers {
		subscriberWg.Add(1)
		go func(id int) {
			defer subscriberWg.Done()

			ch := sub.Subscribe(fmt.Sprintf("subscriber_%d", id))
			receivedMessages := make(map[string]bool)

			// Try to receive all messages within timeout
			timeoutCh := time.After(timeout)
			for len(receivedMessages) < numMessages {
				select {
				case msg := <-ch:
					receivedMessages[msg] = true
				case <-timeoutCh:
					// Send what we received so far and exit
					results <- receivedMessages
					return
				}
			}

			// Send successful results
			results <- receivedMessages
		}(i)
	}

	// Give subscribers time to set up
	time.Sleep(50 * time.Millisecond)

	// Start publisher
	var publisherWg sync.WaitGroup
	publisherWg.Go(func() {
		for i := range numMessages {
			sub.Publish(fmt.Sprintf("message_%d", i))
			time.Sleep(5 * time.Millisecond) // Small delay between messages
		}
	})

	// Wait for publisher to finish
	publisherWg.Wait()

	// Wait for all subscribers to finish
	subscriberWg.Wait()
	close(results)

	// Collect and verify results
	allResults := make([]map[string]bool, 0, numSubscribers)
	for result := range results {
		allResults = append(allResults, result)
	}

	require.Len(t, allResults, numSubscribers, "All subscribers should have reported results")

	for i, result := range allResults {
		require.Len(t, result, numMessages,
			"Subscriber %d should have received all %d messages, but got %d",
			i, numMessages, len(result))
	}

	// Verify no race conditions occurred
	require.Equal(t, numSubscribers, len(sub.subs),
		"Should have exactly %d active subscribers", numSubscribers)
}
