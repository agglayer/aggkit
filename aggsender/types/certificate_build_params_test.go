package types

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInvalidRangeToBlock(t *testing.T) {
	params := &CertificateBuildParams{
		FromBlock: 100,
		ToBlock:   200,
	}
	_, err := params.Range(100, 0)
	require.Error(t, err, "should return an error for invalid range")
}

func TestInvalidRangeOutsideOriginalRange(t *testing.T) {
	params := &CertificateBuildParams{
		FromBlock: 100,
		ToBlock:   200,
	}
	_, err := params.Range(99, 110)
	require.Error(t, err, "should return an error for invalid range")
}

func TestNumberOfBlocks(t *testing.T) {
	tests := []struct {
		name     string
		params   *CertificateBuildParams
		expected int
	}{
		{
			name:     "Nil params",
			params:   nil,
			expected: 0,
		},
		{
			name: "Normal range",
			params: &CertificateBuildParams{
				FromBlock: 100,
				ToBlock:   200,
			},
			expected: 101,
		},
		{
			name: "Overflow - range exceeds MaxInt",
			params: &CertificateBuildParams{
				FromBlock: 0,
				ToBlock:   uint64(math.MaxInt) + 1,
			},
			expected: math.MaxInt,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.params.NumberOfBlocks()
			require.Equal(t, tt.expected, result)
		})
	}
}
