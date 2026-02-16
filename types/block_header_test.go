package types

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

func TestNewBlockHeader(t *testing.T) {
	number := uint64(123)
	hash := common.HexToHash("0x1234567890abcdef")
	time := uint64(1640995200)
	parentHash := common.HexToHash("0xabcdef1234567890")

	header := NewBlockHeader(number, hash, time, &parentHash)

	require.Equal(t, number, header.Number)
	require.Equal(t, hash, header.Hash)
	require.Equal(t, time, header.Time)
	require.Equal(t, &parentHash, header.ParentHash)
}

func TestNewBlockHeaderFromEthHeader(t *testing.T) {
	t.Run("with valid eth header", func(t *testing.T) {
		ethHeader := &types.Header{
			Number:     big.NewInt(456),
			Time:       1640995300,
			ParentHash: common.HexToHash("0xfedcba0987654321"),
		}

		header := NewBlockHeaderFromEthHeader(ethHeader)

		require.NotNil(t, header)
		require.Equal(t, uint64(456), header.Number)
		require.Equal(t, ethHeader.Hash(), header.Hash)
		require.Equal(t, uint64(1640995300), header.Time)
		require.Equal(t, &ethHeader.ParentHash, header.ParentHash)
	})

	t.Run("with nil eth header", func(t *testing.T) {
		header := NewBlockHeaderFromEthHeader(nil)
		require.Nil(t, header)
	})
}

func TestBlockHeader_String(t *testing.T) {
	t.Run("with valid block header", func(t *testing.T) {
		hash := common.HexToHash("0x1234567890abcdef")
		parentHash := common.HexToHash("0xabcdef1234567890")
		header := &BlockHeader{
			Number:     123,
			Hash:       hash,
			Time:       1640995200,
			ParentHash: &parentHash,
		}

		result := header.String()
		expected := "BlockHeader{Number: 123, Hash: 0x0000000000000000000000000000000000000000000000001234567890abcdef, Time: 1640995200, ParentHash: 0x000000000000000000000000000000000000000000000000abcdef1234567890}"
		require.Equal(t, expected, result)
	})

	t.Run("with nil block header", func(t *testing.T) {
		var header *BlockHeader
		result := header.String()
		require.Equal(t, "<nil>", result)
	})
}

func TestBlockHeader_Brief(t *testing.T) {
	t.Run("with valid block header", func(t *testing.T) {
		hash := common.HexToHash("0x1234567890abcdef")
		parentHash := common.HexToHash("0xabcdef1234567890")
		header := &BlockHeader{
			Number:     123,
			Hash:       hash,
			Time:       1640995200,
			ParentHash: &parentHash,
		}

		result := header.Brief()
		expected := "BlockHeader{Number: 123, Hash: 0x0000000000000000000000000000000000000000000000001234567890abcdef}"
		require.Equal(t, expected, result)
	})

	t.Run("with nil block header", func(t *testing.T) {
		var header *BlockHeader
		result := header.Brief()
		require.Equal(t, "<nil>", result)
	})
}

func TestBlockHeader_Empty(t *testing.T) {
	t.Run("with nil block header", func(t *testing.T) {
		var header *BlockHeader
		result := header.Empty()
		require.True(t, result)
	})

	t.Run("with valid block header", func(t *testing.T) {
		hash := common.HexToHash("0x1234567890abcdef")
		parentHash := common.HexToHash("0xabcdef1234567890")
		header := &BlockHeader{
			Number:     123,
			Hash:       hash,
			Time:       1640995200,
			ParentHash: &parentHash,
		}

		result := header.Empty()
		require.False(t, result)
	})

	t.Run("with zero-valued block header", func(t *testing.T) {
		header := &BlockHeader{
			Number:     0,
			Hash:       common.Hash{},
			Time:       0,
			ParentHash: nil,
		}

		result := header.Empty()
		require.False(t, result)
	})
}
