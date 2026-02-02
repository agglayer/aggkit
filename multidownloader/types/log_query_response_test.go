package types

import (
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestLogQueryResponse_CountLogs_Nil(t *testing.T) {
	var lqr *LogQueryResponse
	count := lqr.CountLogs()
	require.Equal(t, 0, count)
}

func TestLogQueryResponse_CountLogs_EmptyBlocks(t *testing.T) {
	lqr := &LogQueryResponse{
		Blocks:        []BlockWithLogs{},
		ResponseRange: aggkitcommon.NewBlockRange(100, 200),
		UnsafeRange:   aggkitcommon.NewBlockRange(0, 0),
	}
	count := lqr.CountLogs()
	require.Equal(t, 0, count)
}

func TestLogQueryResponse_CountLogs_SingleBlockWithLogs(t *testing.T) {
	parentHash := common.HexToHash("0x1234")
	block := BlockWithLogs{
		Header: *aggkittypes.NewBlockHeader(100, common.HexToHash("0xabc"), 1234567890, &parentHash),
		IsFinal: true,
		Logs: []Log{
			{
				Address:        common.HexToAddress("0x1111"),
				Topics:         []common.Hash{common.HexToHash("0x5678")},
				Data:           []byte("data1"),
				BlockNumber:    100,
				TxHash:         common.HexToHash("0xdef"),
				TxIndex:        0,
				BlockTimestamp: 1234567890,
				Index:          0,
				Removed:        false,
			},
			{
				Address:        common.HexToAddress("0x2222"),
				Topics:         []common.Hash{common.HexToHash("0x9abc")},
				Data:           []byte("data2"),
				BlockNumber:    100,
				TxHash:         common.HexToHash("0xdef"),
				TxIndex:        1,
				BlockTimestamp: 1234567890,
				Index:          1,
				Removed:        false,
			},
		},
	}

	lqr := &LogQueryResponse{
		Blocks:        []BlockWithLogs{block},
		ResponseRange: aggkitcommon.NewBlockRange(100, 100),
		UnsafeRange:   aggkitcommon.NewBlockRange(0, 0),
	}

	count := lqr.CountLogs()
	require.Equal(t, 2, count)
}

func TestLogQueryResponse_CountLogs_MultipleBlocksWithLogs(t *testing.T) {
	parentHash1 := common.HexToHash("0x1234")
	parentHash2 := common.HexToHash("0x5678")

	block1 := BlockWithLogs{
		Header: *aggkittypes.NewBlockHeader(100, common.HexToHash("0xabc"), 1234567890, &parentHash1),
		IsFinal: true,
		Logs: []Log{
			{
				Address:        common.HexToAddress("0x1111"),
				Topics:         []common.Hash{common.HexToHash("0x5678")},
				Data:           []byte("data1"),
				BlockNumber:    100,
				TxHash:         common.HexToHash("0xdef"),
				TxIndex:        0,
				BlockTimestamp: 1234567890,
				Index:          0,
				Removed:        false,
			},
			{
				Address:        common.HexToAddress("0x2222"),
				Topics:         []common.Hash{common.HexToHash("0x9abc")},
				Data:           []byte("data2"),
				BlockNumber:    100,
				TxHash:         common.HexToHash("0xdef"),
				TxIndex:        1,
				BlockTimestamp: 1234567890,
				Index:          1,
				Removed:        false,
			},
		},
	}

	block2 := BlockWithLogs{
		Header: *aggkittypes.NewBlockHeader(101, common.HexToHash("0xdef"), 1234567900, &parentHash2),
		IsFinal: false,
		Logs: []Log{
			{
				Address:        common.HexToAddress("0x3333"),
				Topics:         []common.Hash{common.HexToHash("0xaaa")},
				Data:           []byte("data3"),
				BlockNumber:    101,
				TxHash:         common.HexToHash("0xghi"),
				TxIndex:        0,
				BlockTimestamp: 1234567900,
				Index:          0,
				Removed:        false,
			},
		},
	}

	lqr := &LogQueryResponse{
		Blocks:        []BlockWithLogs{block1, block2},
		ResponseRange: aggkitcommon.NewBlockRange(100, 101),
		UnsafeRange:   aggkitcommon.NewBlockRange(101, 101),
	}

	count := lqr.CountLogs()
	require.Equal(t, 3, count)
}

func TestLogQueryResponse_CountLogs_MixedBlocks(t *testing.T) {
	parentHash1 := common.HexToHash("0x1234")
	parentHash2 := common.HexToHash("0x5678")
	parentHash3 := common.HexToHash("0x9abc")

	blockWithLogs := BlockWithLogs{
		Header: *aggkittypes.NewBlockHeader(100, common.HexToHash("0xabc"), 1234567890, &parentHash1),
		IsFinal: true,
		Logs: []Log{
			{
				Address:        common.HexToAddress("0x1111"),
				Topics:         []common.Hash{common.HexToHash("0x5678")},
				Data:           []byte("data1"),
				BlockNumber:    100,
				TxHash:         common.HexToHash("0xdef"),
				TxIndex:        0,
				BlockTimestamp: 1234567890,
				Index:          0,
				Removed:        false,
			},
		},
	}

	blockWithoutLogs := BlockWithLogs{
		Header:  *aggkittypes.NewBlockHeader(101, common.HexToHash("0xdef"), 1234567900, &parentHash2),
		IsFinal: true,
		Logs:    []Log{},
	}

	blockWithMultipleLogs := BlockWithLogs{
		Header: *aggkittypes.NewBlockHeader(102, common.HexToHash("0xghi"), 1234567910, &parentHash3),
		IsFinal: false,
		Logs: []Log{
			{
				Address:        common.HexToAddress("0x2222"),
				Topics:         []common.Hash{common.HexToHash("0xaaa")},
				Data:           []byte("data2"),
				BlockNumber:    102,
				TxHash:         common.HexToHash("0xjkl"),
				TxIndex:        0,
				BlockTimestamp: 1234567910,
				Index:          0,
				Removed:        false,
			},
			{
				Address:        common.HexToAddress("0x3333"),
				Topics:         []common.Hash{common.HexToHash("0xbbb")},
				Data:           []byte("data3"),
				BlockNumber:    102,
				TxHash:         common.HexToHash("0xjkl"),
				TxIndex:        1,
				BlockTimestamp: 1234567910,
				Index:          1,
				Removed:        false,
			},
			{
				Address:        common.HexToAddress("0x4444"),
				Topics:         []common.Hash{common.HexToHash("0xccc")},
				Data:           []byte("data4"),
				BlockNumber:    102,
				TxHash:         common.HexToHash("0xjkl"),
				TxIndex:        2,
				BlockTimestamp: 1234567910,
				Index:          2,
				Removed:        true,
			},
		},
	}

	lqr := &LogQueryResponse{
		Blocks:        []BlockWithLogs{blockWithLogs, blockWithoutLogs, blockWithMultipleLogs},
		ResponseRange: aggkitcommon.NewBlockRange(100, 102),
		UnsafeRange:   aggkitcommon.NewBlockRange(102, 102),
	}

	count := lqr.CountLogs()
	require.Equal(t, 4, count)
}

func TestLogQueryResponse_CountLogs_BlocksWithNilLogs(t *testing.T) {
	parentHash := common.HexToHash("0x1234")

	blockWithNilLogs := BlockWithLogs{
		Header:  *aggkittypes.NewBlockHeader(100, common.HexToHash("0xabc"), 1234567890, &parentHash),
		IsFinal: true,
		Logs:    nil,
	}

	lqr := &LogQueryResponse{
		Blocks:        []BlockWithLogs{blockWithNilLogs},
		ResponseRange: aggkitcommon.NewBlockRange(100, 100),
		UnsafeRange:   aggkitcommon.NewBlockRange(0, 0),
	}

	count := lqr.CountLogs()
	require.Equal(t, 0, count)
}

func TestLog_Structure(t *testing.T) {
	log := Log{
		Address:        common.HexToAddress("0x1111"),
		Topics:         []common.Hash{common.HexToHash("0x5678"), common.HexToHash("0x9abc")},
		Data:           []byte("test data"),
		BlockNumber:    100,
		TxHash:         common.HexToHash("0xdef"),
		TxIndex:        5,
		BlockTimestamp: 1234567890,
		Index:          10,
		Removed:        false,
	}

	require.Equal(t, common.HexToAddress("0x1111"), log.Address)
	require.Equal(t, 2, len(log.Topics))
	require.Equal(t, common.HexToHash("0x5678"), log.Topics[0])
	require.Equal(t, common.HexToHash("0x9abc"), log.Topics[1])
	require.Equal(t, []byte("test data"), log.Data)
	require.Equal(t, uint64(100), log.BlockNumber)
	require.Equal(t, common.HexToHash("0xdef"), log.TxHash)
	require.Equal(t, uint(5), log.TxIndex)
	require.Equal(t, uint64(1234567890), log.BlockTimestamp)
	require.Equal(t, uint(10), log.Index)
	require.False(t, log.Removed)
}

func TestBlockWithLogs_Structure(t *testing.T) {
	parentHash := common.HexToHash("0x1234")
	header := aggkittypes.NewBlockHeader(100, common.HexToHash("0xabc"), 1234567890, &parentHash)

	logs := []Log{
		{
			Address:     common.HexToAddress("0x1111"),
			Topics:      []common.Hash{common.HexToHash("0x5678")},
			Data:        []byte("data1"),
			BlockNumber: 100,
			Removed:     false,
		},
	}

	block := BlockWithLogs{
		Header:  *header,
		IsFinal: true,
		Logs:    logs,
	}

	require.Equal(t, uint64(100), block.Header.Number)
	require.Equal(t, common.HexToHash("0xabc"), block.Header.Hash)
	require.True(t, block.IsFinal)
	require.Equal(t, 1, len(block.Logs))
	require.Equal(t, common.HexToAddress("0x1111"), block.Logs[0].Address)
}

func TestLogQueryResponse_Structure(t *testing.T) {
	parentHash := common.HexToHash("0x1234")

	block := BlockWithLogs{
		Header:  *aggkittypes.NewBlockHeader(100, common.HexToHash("0xabc"), 1234567890, &parentHash),
		IsFinal: true,
		Logs: []Log{
			{
				Address:     common.HexToAddress("0x1111"),
				BlockNumber: 100,
			},
		},
	}

	responseRange := aggkitcommon.NewBlockRange(100, 200)
	unsafeRange := aggkitcommon.NewBlockRange(150, 200)

	lqr := &LogQueryResponse{
		Blocks:        []BlockWithLogs{block},
		ResponseRange: responseRange,
		UnsafeRange:   unsafeRange,
	}

	require.Equal(t, 1, len(lqr.Blocks))
	require.Equal(t, responseRange, lqr.ResponseRange)
	require.Equal(t, unsafeRange, lqr.UnsafeRange)
	require.Equal(t, uint64(100), lqr.ResponseRange.FromBlock)
	require.Equal(t, uint64(200), lqr.ResponseRange.ToBlock)
	require.Equal(t, uint64(150), lqr.UnsafeRange.FromBlock)
	require.Equal(t, uint64(200), lqr.UnsafeRange.ToBlock)
}
