package exit_certificate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHexToUint64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected uint64
	}{
		{"zero", "0x0", 0},
		{"simple", "0x1", 1},
		{"hex value", "0xff", 255},
		{"no prefix", "ff", 255},
		{"block number", "0x1a2b3c", 1715004},
		{"large", "0xFFFFFFFF", 4294967295},
		{"mixed case", "0xAbCdEf", 11259375},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := hexToUint64(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}
