package aggsender

import (
	"context"
	"errors"
	"testing"
	"time"

	agglayermocks "github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	ethermantypesmocks "github.com/agglayer/aggkit/etherman/types/mocks"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	ethmanmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewRunner(t *testing.T) {
	tests := []struct {
		name                string
		mode                types.AggsenderMode
		setupMocks          func() (*mocks.Logger, *ethmanmocks.BaseEthereumClienter, *mocks.L2BridgeSyncer, *agglayermocks.AgglayerClientMock)
		expectError         bool
		expectedErrorString string
	}{
		{
			name: "PreconfPP mode returns preconfRunner",
			mode: types.PreconfPPMode,
			setupMocks: func() (*mocks.Logger, *ethmanmocks.BaseEthereumClienter, *mocks.L2BridgeSyncer, *agglayermocks.AgglayerClientMock) {
				logger := mocks.NewLogger(t)
				l1Client := ethmanmocks.NewBaseEthereumClienter(t)
				l2BridgeSync := mocks.NewL2BridgeSyncer(t)
				agglayerClient := agglayermocks.NewAgglayerClientMock(t)

				return logger, l1Client, l2BridgeSync, agglayerClient
			},
			expectError: false,
		},
		{
			name: "Default mode returns epochBasedRunner successfully",
			mode: types.AutoMode,
			setupMocks: func() (*mocks.Logger, *ethmanmocks.BaseEthereumClienter, *mocks.L2BridgeSyncer, *agglayermocks.AgglayerClientMock) {
				logger := mocks.NewLogger(t)
				l1Client := ethmanmocks.NewBaseEthereumClienter(t)
				l2BridgeSync := mocks.NewL2BridgeSyncer(t)
				agglayerClient := agglayermocks.NewAgglayerClientMock(t)

				// Mock successful block notifier creation (will be called internally)
				l1Client.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(nil, nil).Maybe()

				// Mock successful epoch notifier config generation
				epochConfig := &agglayertypes.ClockConfiguration{
					GenesisBlock:  1000,
					EpochDuration: 100,
				}
				agglayerClient.EXPECT().GetEpochConfiguration(mock.Anything).Return(epochConfig, nil)

				return logger, l1Client, l2BridgeSync, agglayerClient
			},
			expectError: false,
		},
		{
			name: "EpochBasedRunner creation fails due to agglayer client error",
			mode: types.AutoMode,
			setupMocks: func() (*mocks.Logger, *ethmanmocks.BaseEthereumClienter, *mocks.L2BridgeSyncer, *agglayermocks.AgglayerClientMock) {
				logger := mocks.NewLogger(t)
				l1Client := ethmanmocks.NewBaseEthereumClienter(t)
				l2BridgeSync := mocks.NewL2BridgeSyncer(t)
				agglayerClient := agglayermocks.NewAgglayerClientMock(t)

				// Mock failed epoch notifier config generation
				agglayerClient.EXPECT().GetEpochConfiguration(mock.Anything).Return(nil, errors.New("agglayer connection failed"))

				return logger, l1Client, l2BridgeSync, agglayerClient
			},
			expectError:         true,
			expectedErrorString: "failed to generate Epoch Notifier config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			cfg := config.Config{
				Mode:                        tt.mode,
				EpochNotificationPercentage: 75.0,
			}

			logger, l1Client, l2BridgeSync, agglayerClient := tt.setupMocks()

			runner, err := NewCertificateSendTrigger(ctx, cfg, logger, l1Client, l2BridgeSync, agglayerClient)

			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, runner)
				if tt.expectedErrorString != "" {
					require.Contains(t, err.Error(), tt.expectedErrorString)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, runner)
			}
		})
	}
}

func TestNewEpochBasedRunner(t *testing.T) {
	t.Run("successful creation", func(t *testing.T) {
		ctx := context.Background()
		cfg := config.Config{
			EpochNotificationPercentage: 80.0,
		}
		logger := mocks.NewLogger(t)
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
		cfg := config.Config{
			EpochNotificationPercentage: 80.0,
		}
		logger := mocks.NewLogger(t)
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
	receivedEvents := make([]types.CertificateTriggerEvent, 0, 1)
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

func TestPreconfTriggerForceTriggerEvent(t *testing.T) {
	logger := log.WithFields("test", "test")
	mockL2BridgeSync := mocks.NewL2BridgeSyncer(t)

	// Create a mock subscription channel
	syncCh := make(chan sync.Block, 3)
	mockL2BridgeSync.EXPECT().SubscribeToSync("aggsender").Return(syncCh)
	mockL2BridgeSync.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(12345), nil).Once()
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
