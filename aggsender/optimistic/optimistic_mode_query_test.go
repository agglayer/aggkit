package optimistic

import (
	"errors"
	"testing"

	"github.com/agglayer/aggkit/aggsender/optimistic/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestIsOptimisticModeOn(t *testing.T) {

	testCases := []struct {
		name                  string
		contractReturn        bool
		contractReturnError   error
		expectedErrorContains string
		expectedResult        bool
	}{
		{
			name:                  "Optimistic mode is on",
			contractReturn:        true,
			expectedErrorContains: "",
			expectedResult:        true,
		},
		{
			name:                  "Optimistic mode is off",
			contractReturn:        false,
			expectedErrorContains: "",
			expectedResult:        false,
		},
		{
			name:                  "Contract call fails",
			contractReturn:        false,
			contractReturnError:   errors.New("contract error"),
			expectedErrorContains: "contract error",
			expectedResult:        false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockContract := mocks.NewFEPContractQuerier(t)
			mockAddress := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

			querier := &OptimisticModeQuerierFromContract{
				aggchainFEPContract: mockContract,
				aggchainFEPAddr:     mockAddress,
			}
			mockContract.EXPECT().OptimisticMode(mock.Anything).Return(tc.contractReturn, tc.contractReturnError)
			result, err := querier.IsOptimisticModeOn()
			if tc.expectedErrorContains != "" {
				require.Contains(t, err.Error(), tc.expectedErrorContains)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedResult, result)
			}
		})
	}

}
