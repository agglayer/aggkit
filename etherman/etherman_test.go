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
