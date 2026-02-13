package types

import (
	"math/big"
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestLogQuery_NewLogQuery(t *testing.T) {
	addrs := []common.Address{common.HexToAddress("0x123")}
	query := NewLogQuery(1, 10, addrs)

	require.Equal(t, addrs, query.Addrs)
	require.Equal(t, uint64(1), query.BlockRange.FromBlock)
	require.Equal(t, uint64(10), query.BlockRange.ToBlock)
}

func TestLogQuery_NewLogQueryFromEthereumFilter(t *testing.T) {
	addrs := []common.Address{common.HexToAddress("0x123")}
	filter := ethereum.FilterQuery{
		Addresses: addrs,
		FromBlock: big.NewInt(1),
		ToBlock:   big.NewInt(10),
	}

	query := NewLogQueryFromEthereumFilter(filter)
	require.Equal(t, addrs, query.Addrs)
	require.Equal(t, uint64(1), query.BlockRange.FromBlock)
	require.Equal(t, uint64(10), query.BlockRange.ToBlock)
}

func TestLogQuery_String(t *testing.T) {
	t.Run("nil query", func(t *testing.T) {
		var query *LogQuery
		result := query.String()
		require.Equal(t, "LogQuery: <nil>", result)
	})

	t.Run("valid query", func(t *testing.T) {
		query := &LogQuery{
			Addrs:      []common.Address{common.HexToAddress("0x123")},
			BlockRange: aggkitcommon.NewBlockRange(1, 10),
		}
		result := query.String()
		require.Contains(t, result, "LogQuery:")
		require.Contains(t, result, "addrs=")
		require.Contains(t, result, "blockRange=")
	})
}

func TestLogQuery_ToRPCFilterQuery(t *testing.T) {
	addrs := []common.Address{common.HexToAddress("0x123")}
	query := &LogQuery{
		Addrs:      addrs,
		BlockRange: aggkitcommon.NewBlockRange(1, 10),
	}

	filter := query.ToRPCFilterQuery()
	require.Equal(t, addrs, filter.Addresses)
	require.Equal(t, big.NewInt(1), filter.FromBlock)
	require.Equal(t, big.NewInt(10), filter.ToBlock)
}

func TestLogQuery_BlockHash(t *testing.T) {
	lq := NewLogQueryBlockHash(1234, common.HexToHash("0xabc"), []common.Address{common.HexToAddress("0x123")})
	require.Equal(t, common.HexToHash("0xabc"), *lq.BlockHash)
	require.Equal(t, []common.Address{common.HexToAddress("0x123")}, lq.Addrs)
	blockHash := common.HexToHash("0xabc")
	lq2 := NewLogQueryFromEthereumFilter(ethereum.FilterQuery{
		Addresses: []common.Address{common.HexToAddress("0x123")},
		BlockHash: &blockHash,
	})
	require.Equal(t, "LogQuery: addrs=[0x0000000000000000000000000000000000000123], blockHash=0x0000000000000000000000000000000000000000000000000000000000000abc (?)",
		lq2.String())

	rpcFilter := lq.ToRPCFilterQuery()
	require.Equal(t, common.HexToHash("0xabc"), *rpcFilter.BlockHash)
	require.Equal(t, []common.Address{common.HexToAddress("0x123")}, rpcFilter.Addresses)
	require.Equal(t, "LogQuery: addrs=[0x0000000000000000000000000000000000000123], blockHash=0x0000000000000000000000000000000000000000000000000000000000000abc (1234)",
		lq.String())
}
func TestLogQuery_IsEmpty(t *testing.T) {
	var lq *LogQuery
	require.True(t, lq.IsEmpty())

	lq = &LogQuery{}
	require.True(t, lq.IsEmpty())

	lq.BlockRange = aggkitcommon.NewBlockRange(1, 10)
	require.False(t, lq.IsEmpty())

	lq.BlockRange = aggkitcommon.BlockRangeZero
	require.True(t, lq.IsEmpty())

	lq.BlockHash = new(common.Hash)
	require.False(t, lq.IsEmpty())
}

func TestLogQuery_IsValid(t *testing.T) {
	var lq *LogQuery
	require.True(t, lq.IsValid())
	lq = &LogQuery{}
	require.True(t, lq.IsValid(), "blockRange is {0,0} bu is empty")
	lq.BlockRange = aggkitcommon.NewBlockRange(0, 0)
	require.False(t, lq.IsValid())
	lq.BlockHash = new(common.Hash)
	require.True(t, lq.IsValid(), "bn={0,0} but it use blockHash")
}
