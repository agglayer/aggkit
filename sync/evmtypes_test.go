package sync

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestEVMBlocks_Len(t *testing.T) {
	t.Run("empty blocks", func(t *testing.T) {
		blocks := EVMBlocks{}
		require.Equal(t, 0, blocks.Len())
	})

	t.Run("single block", func(t *testing.T) {
		blocks := EVMBlocks{
			{EVMBlockHeader: EVMBlockHeader{Num: 1}},
		}
		require.Equal(t, 1, blocks.Len())
	})

	t.Run("multiple blocks", func(t *testing.T) {
		blocks := EVMBlocks{
			{EVMBlockHeader: EVMBlockHeader{Num: 1}},
			{EVMBlockHeader: EVMBlockHeader{Num: 2}},
			{EVMBlockHeader: EVMBlockHeader{Num: 3}},
		}
		require.Equal(t, 3, blocks.Len())
	})

	t.Run("nil blocks slice", func(t *testing.T) {
		var blocks EVMBlocks
		require.Equal(t, 0, blocks.Len())
	})
}

func TestEVMBlocks_LastBlock(t *testing.T) {
	t.Run("empty blocks returns nil", func(t *testing.T) {
		blocks := EVMBlocks{}
		result := blocks.LastBlock()
		require.Nil(t, result)
	})

	t.Run("nil blocks returns nil", func(t *testing.T) {
		var blocks EVMBlocks
		result := blocks.LastBlock()
		require.Nil(t, result)
	})

	t.Run("single block returns that block", func(t *testing.T) {
		expectedBlock := &EVMBlock{
			EVMBlockHeader: EVMBlockHeader{
				Num:  100,
				Hash: common.HexToHash("0x123"),
			},
			IsFinalizedBlock: true,
		}
		blocks := EVMBlocks{expectedBlock}
		result := blocks.LastBlock()
		require.NotNil(t, result)
		require.Equal(t, expectedBlock, result)
		require.Equal(t, uint64(100), result.Num)
		require.Equal(t, common.HexToHash("0x123"), result.Hash)
		require.True(t, result.IsFinalizedBlock)
	})

	t.Run("multiple blocks returns last block", func(t *testing.T) {
		firstBlock := &EVMBlock{
			EVMBlockHeader: EVMBlockHeader{Num: 1},
		}
		secondBlock := &EVMBlock{
			EVMBlockHeader: EVMBlockHeader{Num: 2},
		}
		lastBlock := &EVMBlock{
			EVMBlockHeader: EVMBlockHeader{
				Num:        3,
				Hash:       common.HexToHash("0xLAST"),
				ParentHash: common.HexToHash("0xPARENT"),
				Timestamp:  1234567890,
			},
			IsFinalizedBlock: false,
			Events:           []any{"event1", "event2"},
		}
		blocks := EVMBlocks{firstBlock, secondBlock, lastBlock}
		result := blocks.LastBlock()
		require.NotNil(t, result)
		require.Equal(t, lastBlock, result)
		require.Equal(t, uint64(3), result.Num)
		require.Equal(t, common.HexToHash("0xLAST"), result.Hash)
		require.Equal(t, common.HexToHash("0xPARENT"), result.ParentHash)
		require.Equal(t, uint64(1234567890), result.Timestamp)
		require.False(t, result.IsFinalizedBlock)
		require.Len(t, result.Events, 2)
	})
}

func TestEVMBlock_Brief(t *testing.T) {
	t.Run("nil block returns special string", func(t *testing.T) {
		var block *EVMBlock
		result := block.Brief()
		require.Equal(t, "EVMBlock<nil>", result)
	})

	t.Run("block with no events", func(t *testing.T) {
		block := &EVMBlock{
			EVMBlockHeader: EVMBlockHeader{
				Num: 100,
			},
			IsFinalizedBlock: true,
			Events:           []any{},
		}
		result := block.Brief()
		require.Equal(t, "EVMBlock{Num: 100, IsFinalizedBlock: true, EventsCount: 0}", result)
	})

	t.Run("block with events and finalized", func(t *testing.T) {
		block := &EVMBlock{
			EVMBlockHeader: EVMBlockHeader{
				Num: 12345,
			},
			IsFinalizedBlock: true,
			Events:           []any{"event1", "event2", "event3"},
		}
		result := block.Brief()
		require.Equal(t, "EVMBlock{Num: 12345, IsFinalizedBlock: true, EventsCount: 3}", result)
	})

	t.Run("block not finalized with single event", func(t *testing.T) {
		block := &EVMBlock{
			EVMBlockHeader: EVMBlockHeader{
				Num: 999,
			},
			IsFinalizedBlock: false,
			Events:           []any{"single_event"},
		}
		result := block.Brief()
		require.Equal(t, "EVMBlock{Num: 999, IsFinalizedBlock: false, EventsCount: 1}", result)
	})

	t.Run("block with nil events", func(t *testing.T) {
		block := &EVMBlock{
			EVMBlockHeader: EVMBlockHeader{
				Num: 50,
			},
			IsFinalizedBlock: false,
			Events:           nil,
		}
		result := block.Brief()
		require.Equal(t, "EVMBlock{Num: 50, IsFinalizedBlock: false, EventsCount: 0}", result)
	})

	t.Run("block with complete header information", func(t *testing.T) {
		block := &EVMBlock{
			EVMBlockHeader: EVMBlockHeader{
				Num:        777,
				Hash:       common.HexToHash("0xABC"),
				ParentHash: common.HexToHash("0xDEF"),
				Timestamp:  1640000000,
			},
			IsFinalizedBlock: true,
			Events:           []any{"ev1", "ev2", "ev3", "ev4", "ev5"},
		}
		result := block.Brief()
		// Brief only includes Num, IsFinalizedBlock, and EventsCount
		require.Equal(t, "EVMBlock{Num: 777, IsFinalizedBlock: true, EventsCount: 5}", result)
	})
}
