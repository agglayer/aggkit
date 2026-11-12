package trigger

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	ethermantypesmocks "github.com/agglayer/aggkit/etherman/types/mocks"
	"github.com/agglayer/aggkit/log"
	aggkitsync "github.com/agglayer/aggkit/sync"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	forceRepeats = 10000
	// maxGoroutineGrowth is the room allowed for the helper goroutines a trigger may start
	maxGoroutineGrowth = 3
)

func settledGoroutines() int {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	return runtime.NumGoroutine()
}

// requireSingleEvent reads one event and checks that no other one is available.
func requireSingleEvent(t *testing.T, ch <-chan types.CertificateTriggerEvent) types.CertificateTriggerEvent {
	t.Helper()
	var event types.CertificateTriggerEvent
	select {
	case event = <-ch:
		require.NotNil(t, event)
	case <-time.After(2 * time.Second):
		t.Fatal("expected a trigger event")
	}
	select {
	case extra := <-ch:
		t.Fatalf("unexpected extra trigger event: %v", extra)
	case <-time.After(300 * time.Millisecond):
	}
	return event
}

func TestForceTriggerEventIsCoalesced(t *testing.T) {
	type forceTestCase struct {
		name string
		// build returns the trigger under test
		build func(t *testing.T) types.CertificateSendTrigger
	}
	testCases := []forceTestCase{
		{
			name: "asap",
			build: func(t *testing.T) types.CertificateSendTrigger {
				t.Helper()
				sut, err := newASAPTrigger(log.WithFields("test", "asap"), nil, nil)
				require.NoError(t, err)
				return sut
			},
		},
		{
			name: "epoch",
			build: func(t *testing.T) types.CertificateSendTrigger {
				t.Helper()
				blockNotifier := ethermantypesmocks.NewBlockNotifier(t)
				blockNotifier.EXPECT().GetCurrentBlockNumber().Return(uint64(1100)).Maybe()
				epochNotifier, err := NewEpochNotifierPerBlock(blockNotifier, log.WithFields("test", "epoch"),
					ConfigEpochNotifierPerBlock{
						StartingEpochBlock:          1000,
						NumBlockPerEpoch:            100,
						EpochNotificationPercentage: 80,
					}, nil)
				require.NoError(t, err)
				return &epochBasedTrigger{epochNotifier: epochNotifier, blockNotifier: blockNotifier}
			},
		},
		{
			name: "preconf",
			build: func(t *testing.T) types.CertificateSendTrigger {
				t.Helper()
				l2BridgeSync := mocks.NewL2BridgeSyncer(t)
				l2BridgeSync.EXPECT().SubscribeToSync("aggsender").Return(make(chan aggkitsync.Block)).Maybe()
				l2BridgeSync.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(77), true, nil).Maybe()
				return newPreconfTrigger(log.WithFields("test", "preconf"), l2BridgeSync)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sut := tc.build(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			// Nobody reads from the channel while the trigger is forced
			ch := sut.TriggerCh(ctx)
			before := settledGoroutines()

			for range forceRepeats {
				sut.ForceTriggerEvent()
			}

			require.LessOrEqual(t, settledGoroutines()-before, maxGoroutineGrowth,
				"forcing the trigger must not leave goroutines behind")
			requireSingleEvent(t, ch)

			// a new request after the pending one was read produces a new event
			sut.ForceTriggerEvent()
			requireSingleEvent(t, ch)
		})
	}
}

func TestPreconfTriggerUnconsumedKeepsGoroutinesBounded(t *testing.T) {
	const numBlocks = 10000
	l2BridgeSync := mocks.NewL2BridgeSyncer(t)
	subscriber := aggkitcommon.NewGenericSubscriber[aggkitsync.Block]()
	l2BridgeSync.EXPECT().SubscribeToSync("aggsender").Return(subscriber.Subscribe("aggsender")).Once()
	sut := newPreconfTrigger(log.WithFields("test", "preconf"), l2BridgeSync)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ch := sut.TriggerCh(ctx)
	before := settledGoroutines()

	published := make(chan struct{})
	go func() {
		defer close(published)
		for i := uint64(1); i <= numBlocks; i++ {
			subscriber.Publish(aggkitsync.Block{Num: i})
		}
	}()
	select {
	case <-published:
	case <-time.After(5 * time.Second):
		t.Fatal("publishing blocks blocked while the trigger is not read")
	}
	require.LessOrEqual(t, settledGoroutines()-before, maxGoroutineGrowth,
		"unread blocks must not create goroutines")

	// The reader sees the blocks in order and ends on the latest one
	var last uint64
	for {
		select {
		case event := <-ch:
			block, ok := event.(aggkitsync.Block)
			require.True(t, ok)
			require.Greater(t, block.Num, last, "blocks must be delivered in order")
			last = block.Num
			continue
		case <-time.After(300 * time.Millisecond):
		}
		break
	}
	require.Equal(t, uint64(numBlocks), last)
}

func TestASAPTriggerForceWhileContextIsCancelled(t *testing.T) {
	for range 50 {
		sut, err := newASAPTrigger(log.WithFields("test", "asap"), nil, nil)
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(t.Context())
		ch := sut.TriggerCh(ctx)

		done := make(chan struct{})
		go func() {
			defer close(done)
			for range 200 {
				sut.ForceTriggerEvent()
				sut.OnIdle()
			}
		}()
		cancel()
		<-done

		// the channel ends up closed, without panics from late sends
		require.Eventually(t, func() bool {
			select {
			case _, ok := <-ch:
				return !ok
			default:
				return false
			}
		}, 2*time.Second, time.Millisecond)

		// forcing a closed trigger is harmless
		sut.ForceTriggerEvent()
	}
}
