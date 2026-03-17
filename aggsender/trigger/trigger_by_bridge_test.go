package trigger

import (
	"context"
	"testing"
	"time"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewPreconfRunner(t *testing.T) {
	logger := mocks.NewLogger(t)
	l2BridgeSync := mocks.NewL2BridgeSyncer(t)

	runner := newPreconfTrigger(logger, l2BridgeSync)

	require.NotNil(t, runner)
	require.Equal(t, logger, runner.log)
	require.Equal(t, l2BridgeSync, runner.l2BridgeSync)
}

func TestPreconfRunner_Status(t *testing.T) {
	logger := mocks.NewLogger(t)
	l2BridgeSync := mocks.NewL2BridgeSyncer(t)

	runner := newPreconfTrigger(logger, l2BridgeSync)
	status := runner.Status()

	require.Equal(t, "PreconfPP Runner: listening to bridge sync events", status)
}

func TestPreconfRunner_Run(t *testing.T) {
	logger := mocks.NewLogger(t)
	l2BridgeSync := mocks.NewL2BridgeSyncer(t)

	runner := newPreconfTrigger(logger, l2BridgeSync)

	// Run the runner - preconf runner's Run method does not block
	// so this should complete immediately without issues
	runner.Setup(t.Context())
}

func TestPreconfRunner_TriggerCh(t *testing.T) {
	t.Run("forwards events from l2BridgeSync subscription", func(t *testing.T) {
		logger := mocks.NewLogger(t)
		l2BridgeSync := mocks.NewL2BridgeSyncer(t)

		// Create a mock subscription channel
		syncCh := make(chan sync.Block, 1)
		l2BridgeSync.EXPECT().SubscribeToSync("aggsender").Return(syncCh)

		runner := newPreconfTrigger(logger, l2BridgeSync)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		triggerCh := runner.TriggerCh(ctx)

		// Create a mock event
		mockEvent := sync.Block{Num: 123, Events: []any{}, Hash: common.HexToHash("0x1")}

		// Send event to sync channel
		syncCh <- mockEvent

		// Verify event is forwarded to trigger channel
		select {
		case receivedEvent := <-triggerCh:
			require.Equal(t, mockEvent, receivedEvent)
		case <-time.After(1 * time.Second):
			t.Fatal("Expected event was not received")
		}
	})

	t.Run("closes channel when context is canceled", func(t *testing.T) {
		logger := mocks.NewLogger(t)
		l2BridgeSync := mocks.NewL2BridgeSyncer(t)

		// Create a mock subscription channel
		syncCh := make(chan sync.Block)
		l2BridgeSync.EXPECT().SubscribeToSync("aggsender").Return(syncCh)

		runner := newPreconfTrigger(logger, l2BridgeSync)

		ctx, cancel := context.WithCancel(context.Background())
		triggerCh := runner.TriggerCh(ctx)

		// Cancel context
		cancel()

		// Verify channel is closed
		select {
		case _, ok := <-triggerCh:
			require.False(t, ok, "Channel should be closed")
		case <-time.After(1 * time.Second):
			t.Fatal("Channel was not closed within timeout")
		}
	})

	t.Run("handles multiple events", func(t *testing.T) {
		logger := mocks.NewLogger(t)
		l2BridgeSync := mocks.NewL2BridgeSyncer(t)

		// Create a mock subscription channel
		syncCh := make(chan sync.Block, 3)
		l2BridgeSync.EXPECT().SubscribeToSync("aggsender").Return(syncCh)

		runner := newPreconfTrigger(logger, l2BridgeSync)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		triggerCh := runner.TriggerCh(ctx)

		// Create mock events
		mockEvent1 := sync.Block{Num: 124, Events: []any{}, Hash: common.HexToHash("0x2")}
		mockEvent2 := sync.Block{Num: 125, Events: []any{}, Hash: common.HexToHash("0x3")}
		mockEvent3 := sync.Block{Num: 126, Events: []any{}, Hash: common.HexToHash("0x4")}

		// Send multiple events

		syncCh <- mockEvent1
		syncCh <- mockEvent2
		syncCh <- mockEvent3

		// Verify all events are forwarded
		receivedEvents := make([]types.CertificateTriggerEvent, 0, 3)
		for i := 0; i < 3; i++ {
			select {
			case event := <-triggerCh:
				receivedEvents = append(receivedEvents, event)
			case <-time.After(1 * time.Second):
				t.Fatalf("Expected event %d was not received", i+1)
			}
		}

		require.Len(t, receivedEvents, 3)
		require.Equal(t, mockEvent1, receivedEvents[0])
		require.Equal(t, mockEvent2, receivedEvents[1])
		require.Equal(t, mockEvent3, receivedEvents[2])
	})
}
func TestPreconfTriggerForceTriggerEvent(t *testing.T) {
	logger := log.WithFields("test", "test")
	mockL2BridgeSync := mocks.NewL2BridgeSyncer(t)

	// Create a mock subscription channel
	syncCh := make(chan sync.Block, 3)
	mockL2BridgeSync.EXPECT().SubscribeToSync("aggsender").Return(syncCh)
	mockL2BridgeSync.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(12345), true, nil).Once()
	sut := newPreconfTrigger(
		logger,
		mockL2BridgeSync,
	)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	triggerCh := sut.TriggerCh(ctx)
	go sut.ForceTriggerEvent()

	select {
	case event := <-triggerCh:
		t.Logf("Received event: %+v", event)
		break
	case <-ctx.Done():
		t.Fatalf("Expected event was not received after 1 sec")
	}
}
