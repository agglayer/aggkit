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
	"github.com/agglayer/aggkit/sync"
	ethmanmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewRunner(t *testing.T) {
	tests := []struct {
		name                string
		mode                types.AggsenderMode
		setupMocks          func() (*mocks.Logger, *ethmanmocks.BaseEthereumClienter, *mocks.L2BridgeSyncer, *agglayermocks.AgglayerClientMock)
		expectedRunnerType  string
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

				// Mock subscription creation for preconfRunner
				subscription := &sync.Subscription{
					ID:      "aggsender",
					BlockCh: make(chan sync.BlockNotification, bufferSizeBlockNotifier),
					ReorgCh: make(chan sync.ReorgNotification, bufferSizeBlockNotifier),
				}
				l2BridgeSync.EXPECT().SubscribeToSync("aggsender", bufferSizeBlockNotifier).Return(subscription)

				return logger, l1Client, l2BridgeSync, agglayerClient
			},
			expectedRunnerType: "*aggsender.preconfRunner",
			expectError:        false,
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
			expectedRunnerType: "*aggsender.epochBasedRunner",
			expectError:        false,
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
			expectedRunnerType:  "",
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

			runner, err := NewRunner(ctx, cfg, logger, l1Client, l2BridgeSync, agglayerClient)

			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, runner)
				if tt.expectedErrorString != "" {
					require.Contains(t, err.Error(), tt.expectedErrorString)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, runner)
				assert.ObjectsAreEqual(runner, tt.expectedRunnerType)
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

		runner, err := newEpochBasedRunner(ctx, cfg, logger, l1Client, agglayerClient)

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

		runner, err := newEpochBasedRunner(ctx, cfg, logger, l1Client, agglayerClient)

		require.Error(t, err)
		require.Nil(t, runner)
		require.Contains(t, err.Error(), "failed to generate Epoch Notifier config")
		require.Contains(t, err.Error(), "connection timeout")
	})
}

func TestEpochBasedRunner_Status(t *testing.T) {
	mockEpochNotifier := mocks.NewEpochNotifier(t)
	mockBlockNotifier := mocks.NewBlockNotifier(t)

	expectedStatus := types.EpochStatus{
		Epoch:        5,
		PercentEpoch: 0.75,
	}
	mockEpochNotifier.EXPECT().GetEpochStatus().Return(expectedStatus)

	runner := &epochBasedRunner{
		epochNotifier: mockEpochNotifier,
		blockNotifier: mockBlockNotifier,
	}

	status := runner.Status()
	require.Contains(t, status, "EpochStatus: [5, 75.00%]")
}

func TestEpochBasedRunner_Run(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	mockEpochNotifier := mocks.NewEpochNotifier(t)
	mockBlockNotifier := mocks.NewBlockNotifier(t)
	mockCertSender := mocks.NewCertificateSender(t)

	mockBlockNotifier.EXPECT().String().Return("BlockNotifier[polling]")
	mockEpochNotifier.EXPECT().String().Return("EpochNotifier[per-block]")
	mockBlockNotifier.EXPECT().Start(ctx).Return()
	mockEpochNotifier.EXPECT().Start(ctx).Return()

	// Mock certificate sender - this should be the main blocking call
	mockCertSender.EXPECT().SendEpochBasedCertificates(
		mock.MatchedBy(func(ctx context.Context) bool { return ctx != nil }),
		mockEpochNotifier,
		0,
	).Run(func(ctx context.Context, epochNotifier types.EpochNotifier, iterations int) {
		// Simulate some work then exit due to context cancellation
		<-ctx.Done()
	})

	runner := &epochBasedRunner{
		epochNotifier: mockEpochNotifier,
		blockNotifier: mockBlockNotifier,
	}

	// This should not panic and should complete when context is cancelled
	runner.Run(ctx, mockCertSender)
}

func TestNewPreconfRunner(t *testing.T) {
	logger := mocks.NewLogger(t)
	l2BridgeSync := mocks.NewL2BridgeSyncer(t)

	// Mock subscription creation
	expectedSubscription := &sync.Subscription{
		ID:      "aggsender",
		BlockCh: make(chan sync.BlockNotification, bufferSizeBlockNotifier),
		ReorgCh: make(chan sync.ReorgNotification, bufferSizeBlockNotifier),
	}
	l2BridgeSync.EXPECT().SubscribeToSync("aggsender", bufferSizeBlockNotifier).Return(expectedSubscription)

	runner := newPreconfRunner(logger, l2BridgeSync)

	require.NotNil(t, runner)
	require.Equal(t, logger, runner.log)
	require.Equal(t, l2BridgeSync, runner.l2BridgeSync)
	require.Equal(t, expectedSubscription, runner.subscription)
}

func TestPreconfRunner_Status(t *testing.T) {
	logger := mocks.NewLogger(t)
	l2BridgeSync := mocks.NewL2BridgeSyncer(t)

	// Mock subscription creation
	subscription := &sync.Subscription{
		ID:      "aggsender",
		BlockCh: make(chan sync.BlockNotification, bufferSizeBlockNotifier),
		ReorgCh: make(chan sync.ReorgNotification, bufferSizeBlockNotifier),
	}
	l2BridgeSync.EXPECT().SubscribeToSync("aggsender", bufferSizeBlockNotifier).Return(subscription)

	runner := newPreconfRunner(logger, l2BridgeSync)
	status := runner.Status()

	require.Equal(t, "PreconfPP Runner: listening to bridge sync events", status)
}

func TestPreconfRunner_Run(t *testing.T) {
	t.Run("processes block notifications successfully", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		logger := mocks.NewLogger(t)
		l2BridgeSync := mocks.NewL2BridgeSyncer(t)
		mockCertSender := mocks.NewCertificateSender(t)

		// Create subscription with channels
		blockCh := make(chan sync.BlockNotification, bufferSizeBlockNotifier)
		subscription := &sync.Subscription{
			ID:      "aggsender",
			BlockCh: blockCh,
			ReorgCh: make(chan sync.ReorgNotification, bufferSizeBlockNotifier),
		}
		l2BridgeSync.EXPECT().SubscribeToSync("aggsender", bufferSizeBlockNotifier).Return(subscription)

		// Mock logging calls
		logger.EXPECT().Info("PreconfPP mode: listening to bridge sync events")
		logger.EXPECT().Infof("PreconfPP: received block %d with %d events", uint64(100), 2).Maybe()
		logger.EXPECT().Info("PreconfPP runner stopped")

		runner := newPreconfRunner(logger, l2BridgeSync)

		// Send a test block notification
		go func() {
			time.Sleep(10 * time.Millisecond)
			blockNotification := sync.BlockNotification{
				Block: sync.Block{
					Num:    100,
					Events: make([]any, 2), // 2 events
				},
			}
			blockCh <- blockNotification
		}()

		// Run the runner - should exit when context is cancelled
		runner.Run(ctx, mockCertSender)
	})

	t.Run("exits gracefully on context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		logger := mocks.NewLogger(t)
		l2BridgeSync := mocks.NewL2BridgeSyncer(t)
		mockCertSender := mocks.NewCertificateSender(t)

		// Create subscription
		subscription := &sync.Subscription{
			ID:      "aggsender",
			BlockCh: make(chan sync.BlockNotification, bufferSizeBlockNotifier),
			ReorgCh: make(chan sync.ReorgNotification, bufferSizeBlockNotifier),
		}
		l2BridgeSync.EXPECT().SubscribeToSync("aggsender", bufferSizeBlockNotifier).Return(subscription)

		// Mock logging calls
		logger.EXPECT().Info("PreconfPP mode: listening to bridge sync events")
		logger.EXPECT().Info("PreconfPP runner stopped")

		runner := newPreconfRunner(logger, l2BridgeSync)

		// Cancel context immediately to test graceful shutdown
		cancel()

		// Should exit quickly without hanging
		runner.Run(ctx, mockCertSender)
	})
}
