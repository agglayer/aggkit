package etherman

import (
	"errors"
	"testing"

	"github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/etherman/mocks"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetLastLocalExitRoot(t *testing.T) {
	t.Parallel()

	// Define ABI types
	tAddress, err := abi.NewType("address", "", nil)
	require.NoError(t, err, "failed to create address type")
	tUint64, err := abi.NewType("uint64", "", nil)
	require.NoError(t, err, "failed to create uint64 type")
	tUint8, err := abi.NewType("uint8", "", nil)
	require.NoError(t, err, "failed to create uint8 type")
	tBytes32, err := abi.NewType("bytes32", "", nil)
	require.NoError(t, err, "failed to create bytes32 type")

	// Set up arguments corresponding to return values
	args := abi.Arguments{
		{Type: tAddress}, // rollupContract
		{Type: tUint64},  // chainID
		{Type: tAddress}, // verifier
		{Type: tUint64},  // forkID
		{Type: tBytes32}, // lastLocalExitRoot
		{Type: tUint64},  // lastBatchSequenced
		{Type: tUint64},  // lastVerifiedBatch
		{Type: tUint64},  // lastVerifiedBatchBeforeUpgrade
		{Type: tUint64},  // rollupTypeID
		{Type: tUint8},   // rollupVerifierType (enum)
		{Type: tBytes32}, // lastPessimisticRoot
		{Type: tBytes32}, // programVKey
	}

	testCases := []struct {
		name          string
		mockFn        func(mockL1Client *mocks.EthClienter)
		expectedLER   common.Hash
		expectedError string
	}{
		{
			name: "error on callContract",
			mockFn: func(mockL1Client *mocks.EthClienter) {
				mockL1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errors.New("some error"))
			},
			expectedError: "error calling contract RollupManager.RollupIDToRollupData",
		},
		{
			name: "success",
			mockFn: func(mockL1Client *mocks.EthClienter) {
				returnBytes, err := args.Pack(
					common.HexToAddress("0x1234567890123456789012345678901234567890"), // rollupContract
					uint64(1), // chainID
					common.HexToAddress("0x1234567890123456789012345678901234567890"), // verifier
					uint64(1), // forkID
					common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"), // lastLocalExitRoot
					uint64(1), // lastBatchSequenced
					uint64(1), // lastVerifiedBatch
					uint64(1), // lastVerifiedBatchBeforeUpgrade
					uint64(1), // rollupTypeID
					uint8(1),  // rollupVerifierType (enum)
					common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000002"), // lastPessimisticRoot
					common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000003"), // programVKey
				)
				require.NoError(t, err, "failed to pack arguments")

				mockL1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
					Return(returnBytes, nil)
			},
			expectedLER: common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL1Client := mocks.NewEthClienter(t)
			mockL1Client.EXPECT().CodeAt(mock.Anything, mock.Anything, mock.Anything).Return([]byte{1, 2, 3}, nil).Maybe()

			if tc.mockFn != nil {
				tc.mockFn(mockL1Client)
			}

			ler, err := GetLastLocalExitRoot(config.L1Config{}, 1, 1, mockL1Client)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedLER, ler, "unexpected last local exit root")
			}
		})
	}
}
