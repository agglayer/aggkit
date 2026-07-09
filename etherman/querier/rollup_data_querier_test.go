package etherman

import (
	"context"
	"errors"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/etherman/mocks"
	"github.com/agglayer/aggkit/test/helpers"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewRollupDataQuerier(t *testing.T) {
	mockAddr := common.HexToAddress("0x123")

	tests := []struct {
		name                    string
		cfg                     ethermanconfig.L1NetworkConfig
		ethClient               aggkittypes.BaseEthereumClienter
		rollupManagerBuilder    RollupManagerFactoryFunc
		populateUpgradeBlocksFn func(
			ctx context.Context,
			rollupManager RollupManagerContract,
			client aggkittypes.BaseEthereumClienter,
			startBlock, blocksChunkSize uint64) (map[uint8]uint64, error)
		expectedErr    string
		expectedRollup uint32
	}{
		{
			name: "success",
			cfg: ethermanconfig.L1NetworkConfig{
				RPC: ethermanconfig.RPCClientConfig{
					URL: "http://localhost:8545",
				},
				RollupAddr:        mockAddr,
				RollupManagerAddr: common.HexToAddress("0xabc"),
			},
			ethClient: aggkittypesmocks.NewBaseEthereumClienter(t),
			rollupManagerBuilder: func(addr common.Address, client aggkittypes.BaseEthereumClienter) (RollupManagerContract, error) {
				rm := mocks.NewRollupManagerContract(t)
				rm.EXPECT().RollupAddressToID(mock.Anything, mock.Anything).Return(uint32(42), nil)
				return rm, nil
			},
			populateUpgradeBlocksFn: func(
				ctx context.Context,
				rollupManager RollupManagerContract,
				client aggkittypes.BaseEthereumClienter,
				startBlock, blocksChunkSize uint64) (map[uint8]uint64, error) {
				return nil, nil
			},
			expectedRollup: 42,
		},
		{
			name: "rollup manager creation fails",
			cfg: ethermanconfig.L1NetworkConfig{
				RPC: ethermanconfig.RPCClientConfig{
					URL: "ok",
				},
				RollupManagerAddr: mockAddr,
			},
			ethClient: aggkittypesmocks.NewBaseEthereumClienter(t),
			rollupManagerBuilder: func(addr common.Address, client aggkittypes.BaseEthereumClienter) (RollupManagerContract, error) {
				return nil, errors.New("factory error")
			},
			expectedErr: "factory error",
		},
		{
			name: "invalid rollup ID",
			cfg: ethermanconfig.L1NetworkConfig{
				RPC: ethermanconfig.RPCClientConfig{
					URL: "ok",
				},
				RollupAddr: mockAddr,
			},
			ethClient: aggkittypesmocks.NewBaseEthereumClienter(t),
			rollupManagerBuilder: func(addr common.Address, client aggkittypes.BaseEthereumClienter) (RollupManagerContract, error) {
				rm := mocks.NewRollupManagerContract(t)
				rm.EXPECT().RollupAddressToID(mock.Anything, mock.Anything).Return(uint32(0), nil)
				return rm, nil
			},
			expectedErr: ErrInvalidRollupID.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.populateUpgradeBlocksFn != nil {
				populateAgglayerManagerInitializedMapFn = tt.populateUpgradeBlocksFn
			}
			client, err := NewRollupDataQuerier(t.Context(), tt.cfg, tt.ethClient, tt.rollupManagerBuilder)
			if tt.expectedErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedRollup, client.RollupID)
			}
		})
	}
}

func TestRollupDataQuerier_GetRollupChainID(t *testing.T) {
	tests := []struct {
		name        string
		rollupID    uint32
		mockSetup   func(m *mocks.RollupManagerContract)
		expectedID  uint64
		expectedErr string
	}{
		{
			name:     "successfully returns chain ID",
			rollupID: 1,
			mockSetup: func(m *mocks.RollupManagerContract) {
				m.EXPECT().
					RollupIDToRollupData(mock.Anything, mock.Anything).
					Return(agglayermanager.AgglayerManagerRollupDataReturn{ChainID: 999}, nil)
			},
			expectedID:  999,
			expectedErr: "",
		},
		{
			name:     "returns error from contract call",
			rollupID: 2,
			mockSetup: func(m *mocks.RollupManagerContract) {
				m.EXPECT().
					RollupIDToRollupData(mock.Anything, mock.Anything).
					Return(agglayermanager.AgglayerManagerRollupDataReturn{ChainID: 999}, errors.New("call failed"))
			},
			expectedID:  0,
			expectedErr: "failed to retrieve rollup data for rollup id 2: call failed",
		},
		{
			name:     "returns error if ChainID is 0",
			rollupID: 3,
			mockSetup: func(m *mocks.RollupManagerContract) {
				m.EXPECT().
					RollupIDToRollupData(mock.Anything, mock.Anything).
					Return(agglayermanager.AgglayerManagerRollupDataReturn{ChainID: 0}, nil)
			},
			expectedID:  0,
			expectedErr: ErrInvalidChainID.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRM := mocks.NewRollupManagerContract(t)
			tt.mockSetup(mockRM)

			client := &RollupDataQuerier{
				rollupManagerSC: mockRM,
				RollupID:        tt.rollupID,
			}

			id, err := client.GetRollupChainID()

			require.Equal(t, tt.expectedID, id)
			if tt.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tt.expectedErr)
			}
		})
	}
}

func TestFetchRollupID(t *testing.T) {
	testAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	tests := []struct {
		name           string
		setupMock      func() *mocks.RollupManagerContract
		expectedID     uint32
		expectedErrMsg string
	}{
		{
			name: "success",
			setupMock: func() *mocks.RollupManagerContract {
				mockRollupManager := mocks.NewRollupManagerContract(t)
				mockRollupManager.EXPECT().
					RollupAddressToID(mock.Anything, mock.Anything).
					Return(uint32(42), nil)

				return mockRollupManager
			},
			expectedID:     42,
			expectedErrMsg: "",
		},
		{
			name: "error from contract",
			setupMock: func() *mocks.RollupManagerContract {
				mockRollupManager := mocks.NewRollupManagerContract(t)
				mockRollupManager.EXPECT().
					RollupAddressToID(mock.Anything, mock.Anything).
					Return(uint32(0), errors.New("contract call failed"))

				return mockRollupManager
			},
			expectedID:     0,
			expectedErrMsg: "failed to retrieve rollup id from rollup manager contract",
		},
		{
			name: "zero rollup id",
			setupMock: func() *mocks.RollupManagerContract {
				mockRollupManager := mocks.NewRollupManagerContract(t)
				mockRollupManager.EXPECT().
					RollupAddressToID(mock.Anything, mock.Anything).
					Return(uint32(0), nil)

				return mockRollupManager
			},
			expectedID:     0,
			expectedErrMsg: ErrInvalidRollupID.Error(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockRollupManager := tc.setupMock()

			id, err := fetchRollupID(mockRollupManager, testAddr)
			if tc.expectedErrMsg != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedErrMsg)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedID, id)
			}
		})
	}
}

func TestRollupDataQuerier_GetUpgradeBlock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	const (
		latestAgglayerManagerVersion = uint8(5)
		startBlock                   = uint64(0)
		blocksChunkSize              = 10
	)
	l1Setup, _ := helpers.NewSimulatedEVMEnvironment(t, helpers.DefaultEnvironmentConfig(helpers.LegacyL2GERContract))

	upgradedMap, err := populateAgglayerManagerInitializedMap(t.Context(),
		l1Setup.AgglayerManagerContract, etherman.NewDefaultEthClient(l1Setup.SimBackend.Client(), nil, nil), startBlock, blocksChunkSize)
	require.NoError(t, err)
	require.Len(t, upgradedMap, 1)
	require.Contains(t, upgradedMap, latestAgglayerManagerVersion)

	rollupDataQuerier := &RollupDataQuerier{
		rollupManagerUpgradedMap: upgradedMap,
	}

	cases := []struct {
		name                   string
		agglayerManagerVersion uint8
		shouldFind             bool
	}{
		{
			name:                   "existing version",
			agglayerManagerVersion: latestAgglayerManagerVersion,
			shouldFind:             true,
		},
		{
			name:                   "non-existing version",
			agglayerManagerVersion: latestAgglayerManagerVersion + 1,
			shouldFind:             false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upgradeBlock := rollupDataQuerier.GetUpgradeBlock(t.Context(), tc.agglayerManagerVersion)
			if tc.shouldFind {
				require.NotZero(t, upgradeBlock)
			} else {
				require.Zero(t, upgradeBlock)
			}
		})
	}
}
