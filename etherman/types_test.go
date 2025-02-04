package etherman

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBlockNumberFinality(t *testing.T) {
	testCases := []struct {
		name           string
		input          string
		expectedResult BlockNumberFinality
		expectedErr    error
	}{
		{
			name:           "valid finalized block",
			input:          "FinalizedBlock",
			expectedResult: FinalizedBlock,
		},
		{
			name:           "valid safe block",
			input:          "SafeBlock",
			expectedResult: SafeBlock,
		},
		{
			name:           "valid pending block",
			input:          "PendingBlock",
			expectedResult: PendingBlock,
		},
		{
			name:           "valid latest block",
			input:          "LatestBlock",
			expectedResult: LatestBlock,
		},
		{
			name:           "valid earliest block",
			input:          "EarliestBlock",
			expectedResult: EarliestBlock,
		},
		{
			name:        "invalid block",
			input:       "InvalidBlock",
			expectedErr: fmt.Errorf("invalid finality keyword: InvalidBlock"),
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var b BlockNumberFinality
			err := b.UnmarshalText([]byte(testCase.input))

			if testCase.expectedErr == nil {
				require.Equal(t, testCase.expectedResult, b)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), testCase.expectedErr.Error())
			}
		})
	}
}
