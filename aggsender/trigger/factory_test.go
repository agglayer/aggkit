package trigger

import (
	"context"
	"errors"
	"testing"

	agglayermocks "github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	ethmanmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewRunner(t *testing.T) {
	tests := []struct {
		name                string
		mode                types.AggsenderMode
		triggerMode         types.CertificateSendTriggerMode
		setupMocks          func() (*mocks.Logger, *ethmanmocks.BaseEthereumClienter, *mocks.L2BridgeSyncer, *agglayermocks.AgglayerClientMock)
		expectError         bool
		expectedErrorString string
	}{
		{
			name:        "PreconfPP mode returns preconfRunner",
			mode:        types.PreconfPPMode,
			triggerMode: types.AutoTriggerMode,
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
			name:        "AggchainProofMode mode returns epochBasedRunner successfully",
			mode:        types.AggchainProofMode,
			triggerMode: types.AutoTriggerMode,
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
			name:        "PessimisticProofMode creation fails due to agglayer client error",
			mode:        types.PessimisticProofMode,
			triggerMode: types.AutoTriggerMode,
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
				Mode:            tt.mode,
				TriggerCertMode: tt.triggerMode,
				TriggerEpochBased: config.TriggerEpochBasedConfig{
					EpochNotificationPercentage: 75.0,
				},
			}

			_, l1Client, l2BridgeSync, agglayerClient := tt.setupMocks()
			logger := log.WithFields("module", "test")

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
