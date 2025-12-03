package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLeafTypeUint8(t *testing.T) {
	tests := []struct {
		name     string
		leafType LeafType
		expected uint8
	}{
		{"LeafTypeAsset", LeafTypeAsset, 0},
		{"LeafTypeMessage", LeafTypeMessage, 1},
		{"ArbitraryValue", LeafType(7), 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.leafType.Uint8())
		})
	}
}

func TestLeafTypeString(t *testing.T) {
	tests := []struct {
		name     string
		leafType LeafType
		expected string
	}{
		{"LeafTypeAsset", LeafTypeAsset, "Transfer"},
		{"LeafTypeMessage", LeafTypeMessage, "Message"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.leafType.String())
		})
	}
}

func TestLeafTypeUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
		want    LeafType
		wantErr bool
	}{
		{"StringTransfer", `"Transfer"`, LeafTypeAsset, false},
		{"StringMessage", `"Message"`, LeafTypeMessage, false},
		{"NumericZero", `0`, LeafType(0), false},
		{"NumericOne", `1`, LeafType(1), false},
		{"InvalidString", `"Unknown"`, 0, true},
		{"InvalidNumber", `"12x"`, 12, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got LeafType
			err := json.Unmarshal([]byte(tc.jsonStr), &got)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
		})
	}
}
