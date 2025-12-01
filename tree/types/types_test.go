package types

import (
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestRootString(t *testing.T) {
	root := Root{
		Hash:          common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
		Index:         1,
		BlockNum:      100,
		BlockPosition: 10,
	}
	expected := "Root{Hash: 0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef, Index: 1, BlockNum: 100, BlockPosition: 10}"
	require.Equal(t, expected, root.String(), "Root String method should return the expected string representation")
}

func TestProof_String(t *testing.T) {
	tests := []struct {
		name     string
		proofRaw [DefaultHeight][common.HashLength]byte
		want     string
	}{
		{
			name: "mixed values",
			proofRaw: func() [DefaultHeight][common.HashLength]byte {
				var arr [DefaultHeight][common.HashLength]byte
				copy(arr[0][:], []byte{0xAA})
				copy(arr[1][:], []byte{0xBB})
				return arr
			}(),
			want: func() string {
				h1 := common.Hash{0xAA}
				h2 := common.Hash{0xBB}

				parts := []string{h1.String(), h2.String()}
				for len(parts) < int(DefaultHeight) {
					parts = append(parts, common.Hash{}.String())
				}
				return "Proof{[" + strings.Join(parts, ", ") + "]}"
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewProof(tt.proofRaw)
			require.Equal(t, tt.want, p.String())
		})
	}
}
