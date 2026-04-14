package exit_certificate

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHexToBigInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected *big.Int
	}{
		{"zero", "0x0", big.NewInt(0)},
		{"simple", "0x1", big.NewInt(1)},
		{"larger", "0xff", big.NewInt(255)},
		{"no prefix", "ff", big.NewInt(255)},
		{"empty", "", new(big.Int)},
		{"just 0x", "0x", new(big.Int)},
		{
			"large number",
			"0xde0b6b3a7640000",
			func() *big.Int {
				n, _ := new(big.Int).SetString("de0b6b3a7640000", 16)
				return n
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := hexToBigInt(tt.input)
			require.Equal(t, tt.expected.String(), result.String())
		})
	}
}
