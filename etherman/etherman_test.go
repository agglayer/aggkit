package etherman

import (
	"errors"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonrollupmanager"
	"github.com/agglayer/aggkit/config"
	"github.com/agglayer/aggkit/etherman/mocks"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	mockAddr := common.HexToAddress("0x123")

	tests := []struct {
		name           string
		cfg            config.L1NetworkConfig
		mockDial       DialFunc
		mockFactory    RollupManagerFactoryFunc
		expectedErr    string
		expectedRollup uint32
	}{
		{
			name: "success",
			cfg: config.L1NetworkConfig{
				URL:               "http://localhost:8545",
				RollupAddr:        mockAddr,
				RollupManagerAddr: common.HexToAddress("0xabc"),
			},
			mockDial: func(rawurl string) (aggkittypes.BaseEthereumClienter, error) {
				return aggkittypesmocks.NewBaseEthereumClienter(t), nil
			},
			mockFactory: func(addr common.Address, client aggkittypes.BaseEthereumClienter) (RollupManagerContract, error) {
				rm := mocks.NewRollupManagerContract(t)
				rm.EXPECT().RollupAddressToID(mock.Anything, mock.Anything).Return(uint32(42), nil)
				return rm, nil
			},
			expectedRollup: 42,
		},
		{
			name: "dial fails",
			cfg:  config.L1NetworkConfig{URL: "fail"},
			mockDial: func(rawurl string) (aggkittypes.BaseEthereumClienter, error) {
				return nil, errors.New("dial error")
			},
			mockFactory: nil,
			expectedErr: "dial error",
		},
		{
			name: "rollup manager creation fails",
			cfg: config.L1NetworkConfig{
				URL:               "ok",
				RollupManagerAddr: mockAddr,
			},
			mockDial: func(_ string) (aggkittypes.BaseEthereumClienter, error) {
				return aggkittypesmocks.NewBaseEthereumClienter(t), nil
			},
			mockFactory: func(addr common.Address, client aggkittypes.BaseEthereumClienter) (RollupManagerContract, error) {
				return nil, errors.New("factory error")
			},
			expectedErr: "factory error",
		},
		{
			name: "invalid rollup ID",
			cfg: config.L1NetworkConfig{
				URL:        "ok",
				RollupAddr: mockAddr,
			},
			mockDial: func(_ string) (aggkittypes.BaseEthereumClienter, error) {
				return aggkittypesmocks.NewBaseEthereumClienter(t), nil
			},
			mockFactory: func(addr common.Address, client aggkittypes.BaseEthereumClienter) (RollupManagerContract, error) {
				rm := mocks.NewRollupManagerContract(t)
				rm.EXPECT().RollupAddressToID(mock.Anything, mock.Anything).Return(uint32(0), nil)
				return rm, nil
			},
			expectedErr: ErrInvalidRollupID.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.cfg, tt.mockDial, tt.mockFactory)

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

func TestClient_GetL2ChainID(t *testing.T) {
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
					Return(polygonrollupmanager.PolygonRollupManagerRollupDataReturn{ChainID: 999}, nil)
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
					Return(polygonrollupmanager.PolygonRollupManagerRollupDataReturn{ChainID: 999}, errors.New("call failed"))
			},
			expectedID:  0,
			expectedErr: "call failed",
		},
		{
			name:     "returns error if ChainID is 0",
			rollupID: 3,
			mockSetup: func(m *mocks.RollupManagerContract) {
				m.EXPECT().
					RollupIDToRollupData(mock.Anything, mock.Anything).
					Return(polygonrollupmanager.PolygonRollupManagerRollupDataReturn{ChainID: 0}, nil)
			},
			expectedID:  0,
			expectedErr: ErrInvalidChainID.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRM := mocks.NewRollupManagerContract(t)
			tt.mockSetup(mockRM)

			client := &Client{
				rollupManagerSC: mockRM,
				RollupID:        tt.rollupID,
			}

			id, err := client.GetL2ChainID()

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
