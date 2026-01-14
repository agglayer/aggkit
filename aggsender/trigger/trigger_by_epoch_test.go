package trigger

import (
	"context"
	"errors"
	"testing"
	"time"

	agglayermocks "github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/config"
	aggsendermocks "github.com/agglayer/aggkit/aggsender/mocks"
	types "github.com/agglayer/aggkit/aggsender/trigger/types"
	"github.com/agglayer/aggkit/aggsender/trigger/types/mocks"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	ethermantypesmocks "github.com/agglayer/aggkit/etherman/types/mocks"
	"github.com/agglayer/aggkit/log"
	ethmanmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewEpochBasedRunner(t *testing.T) {
	t.Run("successful creation", func(t *testing.T) {
		ctx := context.Background()
		cfg := config.TriggerEpochBasedConfig{
			EpochNotificationPercentage: 80.0,
		}
		logger := aggsendermocks.NewLogger(t)
		l1Client := ethmanmocks.NewBaseEthereumClienter(t)
		agglayerClient := agglayermocks.NewAgglayerClientMock(t)

		// Mock successful epoch notifier config generation
		epochConfig := &agglayertypes.ClockConfiguration{
			GenesisBlock:  1000,
			EpochDuration: 100,
		}
		agglayerClient.EXPECT().GetEpochConfiguration(mock.Anything).Return(epochConfig, nil)

		// Mock successful block notifier creation (HeaderByNumber might be called)
		l1Client.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(nil, nil).Maybe()

		runner, err := newEpochBasedTrigger(ctx, cfg, logger, l1Client, agglayerClient)

		require.NoError(t, err)
		require.NotNil(t, runner)
		require.NotNil(t, runner.epochNotifier)
		require.NotNil(t, runner.blockNotifier)
	})

	t.Run("fails when agglayer client returns error", func(t *testing.T) {
		ctx := context.Background()
		cfg := config.TriggerEpochBasedConfig{
			EpochNotificationPercentage: 80.0,
		}
		logger := aggsendermocks.NewLogger(t)
		l1Client := ethmanmocks.NewBaseEthereumClienter(t)
		agglayerClient := agglayermocks.NewAgglayerClientMock(t)

		// Mock failed epoch notifier config generation
		agglayerClient.EXPECT().GetEpochConfiguration(mock.Anything).Return(nil, errors.New("connection timeout"))

		runner, err := newEpochBasedTrigger(ctx, cfg, logger, l1Client, agglayerClient)

		require.Error(t, err)
		require.Nil(t, runner)
		require.Contains(t, err.Error(), "failed to generate Epoch Notifier config")
		require.Contains(t, err.Error(), "connection timeout")
	})
}

func TestEpochBasedRunner_Status(t *testing.T) {
	mockEpochNotifier := mocks.NewEpochNotifier(t)
	mockBlockNotifier := ethermantypesmocks.NewBlockNotifier(t)

	expectedStatus := types.EpochStatus{
		Epoch:        5,
		PercentEpoch: 0.75,
	}
	mockEpochNotifier.EXPECT().GetEpochStatus().Return(expectedStatus)

	runner := &epochBasedTrigger{
		epochNotifier: mockEpochNotifier,
		blockNotifier: mockBlockNotifier,
	}

	status := runner.Status()
	require.Contains(t, status, "EpochStatus: [5, 75.00%]")
}

func TestEpochBasedRunner_Setup(t *testing.T) {
	mockEpochNotifier := mocks.NewEpochNotifier(t)
	mockBlockNotifier := ethermantypesmocks.NewBlockNotifier(t)

	// Mock the String() methods for logging
	mockBlockNotifier.EXPECT().String().Return("BlockNotifier")
	mockEpochNotifier.EXPECT().String().Return("EpochNotifier")

	// Mock the Start() methods to expect the canceled context
	mockBlockNotifier.EXPECT().Start(mock.Anything).Return().Once()
	mockEpochNotifier.EXPECT().Start(mock.Anything).Return().Once()

	runner := &epochBasedTrigger{
		epochNotifier: mockEpochNotifier,
		blockNotifier: mockBlockNotifier,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	runner.Setup(ctx)

	// Give a small amount of time for goroutines to start and validate all expectations
	time.Sleep(200 * time.Millisecond)

	mockBlockNotifier.AssertExpectations(t)
	mockEpochNotifier.AssertExpectations(t)
}

func TestEpochBasedRunner_TriggerCh(t *testing.T) {
	t.Run("forwards events from epoch notifier subscription", func(t *testing.T) {
		mockEpochNotifier := mocks.NewEpochNotifier(t)
		mockBlockNotifier := ethermantypesmocks.NewBlockNotifier(t)

		// Create a mock subscription channel
		epochCh := make(chan types.EpochEvent, 1)
		mockEpochNotifier.EXPECT().Subscribe("aggsender").Return(epochCh)

		runner := &epochBasedTrigger{
			epochNotifier: mockEpochNotifier,
			blockNotifier: mockBlockNotifier,
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		triggerCh := runner.TriggerCh(ctx)

		// Create a mock epoch event
		mockEvent := types.EpochEvent{
			Epoch: 42,
		}

		// Send event to epoch channel
		epochCh <- mockEvent

		// Verify event is forwarded to trigger channel
		select {
		case receivedEvent := <-triggerCh:
			require.Equal(t, mockEvent, receivedEvent)
		case <-time.After(1 * time.Second):
			t.Fatal("Expected event was not received")
		}
	})

	t.Run("closes channel when context is canceled", func(t *testing.T) {
		mockEpochNotifier := mocks.NewEpochNotifier(t)
		mockBlockNotifier := ethermantypesmocks.NewBlockNotifier(t)

		// Create a mock subscription channel
		epochCh := make(chan types.EpochEvent)
		mockEpochNotifier.EXPECT().Subscribe("aggsender").Return(epochCh)

		runner := &epochBasedTrigger{
			epochNotifier: mockEpochNotifier,
			blockNotifier: mockBlockNotifier,
		}

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
		mockEpochNotifier := mocks.NewEpochNotifier(t)
		mockBlockNotifier := ethermantypesmocks.NewBlockNotifier(t)

		// Create a mock subscription channel
		epochCh := make(chan types.EpochEvent, 3)
		mockEpochNotifier.EXPECT().Subscribe("aggsender").Return(epochCh)

		runner := &epochBasedTrigger{
			epochNotifier: mockEpochNotifier,
			blockNotifier: mockBlockNotifier,
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		triggerCh := runner.TriggerCh(ctx)

		// Create mock events
		mockEvent1 := types.EpochEvent{Epoch: 10}
		mockEvent2 := types.EpochEvent{Epoch: 11}
		mockEvent3 := types.EpochEvent{Epoch: 12}

		// Send multiple events
		epochCh <- mockEvent1
		epochCh <- mockEvent2
		epochCh <- mockEvent3

		// Verify all events are forwarded
		receivedEvents := make([]aggsendertypes.CertificateTriggerEvent, 0, 3)
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

func TestEpochBasedTriggerForceTriggerEvent(t *testing.T) {
	mockBlockNotifier := ethermantypesmocks.NewBlockNotifier(t)
	logger := log.WithFields("test", "test")
	mockEpochNotifier, err := NewEpochNotifierPerBlock(
		mockBlockNotifier,
		logger,
		ConfigEpochNotifierPerBlock{
			StartingEpochBlock:          1000,
			NumBlockPerEpoch:            100,
			EpochNotificationPercentage: 80.0,
		},
		nil,
	)
	require.NoError(t, err)
	runner := &epochBasedTrigger{
		epochNotifier: mockEpochNotifier,
		blockNotifier: mockBlockNotifier,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	triggerCh := runner.TriggerCh(ctx)
	mockBlockNotifier.EXPECT().GetCurrentBlockNumber().Return(uint64(1100)).Times(1)
	runner.ForceTriggerEvent()

	// Verify all events are forwarded
	receivedEvents := make([]aggsendertypes.CertificateTriggerEvent, 0, 1)
	for i := 0; i < 1; i++ {
		select {
		case event := <-triggerCh:
			receivedEvents = append(receivedEvents, event)
		case <-time.After(1 * time.Second):
			t.Fatalf("Expected event was not received after 1 sec")
		}
	}

	require.Len(t, receivedEvents, 1)
}
