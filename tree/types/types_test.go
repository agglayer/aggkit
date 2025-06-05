package types

import (
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
