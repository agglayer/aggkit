package etherman

import (
	"errors"
	"testing"

	"github.com/agglayer/aggkit/etherman/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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
					Return(struct {
						RollupContract                 common.Address
						ChainID                        uint64
						Verifier                       common.Address
						ForkID                         uint64
						LastLocalExitRoot              [32]byte
						LastBatchSequenced             uint64
						LastVerifiedBatch              uint64
						LastPendingState               uint64
						LastPendingStateConsolidated   uint64
						LastVerifiedBatchBeforeUpgrade uint64
						RollupTypeID                   uint64
						RollupCompatibilityID          uint8
					}{
						ChainID: 999,
					}, nil)
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
					Return(struct {
						RollupContract                 common.Address
						ChainID                        uint64
						Verifier                       common.Address
						ForkID                         uint64
						LastLocalExitRoot              [32]byte
						LastBatchSequenced             uint64
						LastVerifiedBatch              uint64
						LastPendingState               uint64
						LastPendingStateConsolidated   uint64
						LastVerifiedBatchBeforeUpgrade uint64
						RollupTypeID                   uint64
						RollupCompatibilityID          uint8
					}{}, errors.New("call failed"))
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
					Return(struct {
						RollupContract                 common.Address
						ChainID                        uint64
						Verifier                       common.Address
						ForkID                         uint64
						LastLocalExitRoot              [32]byte
						LastBatchSequenced             uint64
						LastVerifiedBatch              uint64
						LastPendingState               uint64
						LastPendingStateConsolidated   uint64
						LastVerifiedBatchBeforeUpgrade uint64
						RollupTypeID                   uint64
						RollupCompatibilityID          uint8
					}{
						ChainID: 0,
					}, nil)
			},
			expectedID:  0,
			expectedErr: "error: chainID received is 0",
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

func TestGetRollupID(t *testing.T) {
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
			expectedErrMsg: "invalid rollup id value",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockRollupManager := tc.setupMock()

			id, err := getRollupID(mockRollupManager, testAddr)

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
