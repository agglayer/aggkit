package multidownloader

import (
	"errors"
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var errRPCUnavailable = errors.New("rpc unavailable")

func bloomForAddress(addr common.Address) *ethtypes.Bloom {
	var bloom ethtypes.Bloom
	bloom.Add(addr.Bytes())
	return &bloom
}

func TestFindSuspiciousBlockNumbers(t *testing.T) {
	addr := common.HexToAddress("0x1")
	other := common.HexToAddress("0x2")

	t.Run("bloom-positive and empty logs is flagged", func(t *testing.T) {
		headers := []*aggkittypes.BlockHeader{
			{Number: 100, Hash: common.HexToHash("0xa"), LogsBloom: bloomForAddress(addr)},
		}
		suspicious := findSuspiciousBlockNumbers(headers, nil, uniformAddrsForBlock([]common.Address{addr}))
		require.Equal(t, []uint64{100}, suspicious)
	})

	t.Run("bloom-negative and empty logs is not flagged", func(t *testing.T) {
		headers := []*aggkittypes.BlockHeader{
			{Number: 100, Hash: common.HexToHash("0xa"), LogsBloom: bloomForAddress(other)},
		}
		suspicious := findSuspiciousBlockNumbers(headers, nil, uniformAddrsForBlock([]common.Address{addr}))
		require.Empty(t, suspicious)
	})

	t.Run("bloom-positive with a matching log is not flagged", func(t *testing.T) {
		headers := []*aggkittypes.BlockHeader{
			{Number: 100, Hash: common.HexToHash("0xa"), LogsBloom: bloomForAddress(addr)},
		}
		logs := []ethtypes.Log{{BlockNumber: 100, Address: addr}}
		suspicious := findSuspiciousBlockNumbers(headers, logs, uniformAddrsForBlock([]common.Address{addr}))
		require.Empty(t, suspicious)
	})

	t.Run("nil bloom is always skipped", func(t *testing.T) {
		headers := []*aggkittypes.BlockHeader{
			{Number: 100, Hash: common.HexToHash("0xa"), LogsBloom: nil},
		}
		suspicious := findSuspiciousBlockNumbers(headers, nil, uniformAddrsForBlock([]common.Address{addr}))
		require.Empty(t, suspicious)
	})

	t.Run("nil header is skipped", func(t *testing.T) {
		headers := []*aggkittypes.BlockHeader{nil}
		suspicious := findSuspiciousBlockNumbers(headers, nil, uniformAddrsForBlock([]common.Address{addr}))
		require.Empty(t, suspicious)
	})
}

func TestEVMMultidownloader_ArbitrateSuspiciousBlock(t *testing.T) {
	addrs := []common.Address{common.HexToAddress("0x1")}
	blockHash := common.HexToHash("0xabc")
	hdr := &aggkittypes.BlockHeader{Number: 100, Hash: blockHash}

	byHashQuery := func(q ethereum.FilterQuery) bool {
		return q.BlockHash != nil && *q.BlockHash == blockHash
	}

	t.Run("refetch returns a matching log -> genuine omission error", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		omittedLog := ethtypes.Log{BlockHash: blockHash, BlockNumber: 100}
		data.mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.MatchedBy(byHashQuery)).
			Return([]ethtypes.Log{omittedLog}, nil).Once()

		err := data.mdr.arbitrateSuspiciousBlock(t.Context(), hdr, addrs)
		require.Error(t, err)
		require.Contains(t, err.Error(), "omitted 1 log")
	})

	t.Run("refetch returns no logs twice -> accepted as bloom false positive", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.MatchedBy(byHashQuery)).
			Return([]ethtypes.Log{}, nil).Twice()

		err := data.mdr.arbitrateSuspiciousBlock(t.Context(), hdr, addrs)
		require.NoError(t, err)
	})

	t.Run("refetch returns a log for a different block hash -> not counted, accepted", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		wrongHashLog := ethtypes.Log{BlockHash: common.HexToHash("0xdead"), BlockNumber: 100}
		data.mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.MatchedBy(byHashQuery)).
			Return([]ethtypes.Log{wrongHashLog}, nil).Twice()

		err := data.mdr.arbitrateSuspiciousBlock(t.Context(), hdr, addrs)
		require.NoError(t, err)
	})

	t.Run("all attempts fail to execute -> conservative error, not silent accept", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.MatchedBy(byHashQuery)).
			Return(nil, errRPCUnavailable).Twice()

		err := data.mdr.arbitrateSuspiciousBlock(t.Context(), hdr, addrs)
		require.Error(t, err)
		require.Contains(t, err.Error(), "could not arbitrate")
	})
}

func TestEVMMultidownloader_StepSafe_LogsCompleteness(t *testing.T) {
	addr := common.HexToAddress("0x1")
	hashBlock100 := common.HexToHash("0xaaa100")

	setup := func(t *testing.T) *testDataEVMMultidownloader {
		t.Helper()
		data := newEVMMultidownloaderTestData(t, false)
		data.mockEthClient.EXPECT().ChainID(mock.Anything).Return(common.Big1, nil)
		err := data.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
			SyncerID:          "syncer1",
			ContractAddresses: []common.Address{addr},
			FromBlock:         100,
			ToBlock:           aggkittypes.FinalizedBlock,
		})
		require.NoError(t, err)
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.FinalizedBlock).
			Return(uint64(101), nil).Maybe()
		err = data.mdr.Initialize(t.Context())
		require.NoError(t, err)

		// The range-wide eth_getLogs response silently omits the log for block 100.
		data.mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.MatchedBy(func(q ethereum.FilterQuery) bool {
			return q.BlockHash == nil
		})).Return([]ethtypes.Log{}, nil).Once()

		rpcResult := aggkittypes.NewBlockHeadersResult()
		rpcResult.AddHeader(100, &aggkittypes.BlockHeader{Number: 100, Hash: hashBlock100, LogsBloom: bloomForAddress(addr)})
		rpcResult.AddHeader(101, &aggkittypes.BlockHeader{Number: 101, Hash: common.HexToHash("0xaaa101")})
		data.mockEthClient.EXPECT().
			RetrieveBlockHeaders(mock.Anything, []uint64{100, 101}, data.mdr.cfg.MaxParallelBlockHeaderRetrieval).
			Return(rpcResult, nil).Once()

		return data
	}

	byHashQuery := func(hash common.Hash) func(q ethereum.FilterQuery) bool {
		return func(q ethereum.FilterQuery) bool {
			return q.BlockHash != nil && *q.BlockHash == hash
		}
	}

	t.Run("genuine omission detected -> step returns error", func(t *testing.T) {
		data := setup(t)
		omittedLog := ethtypes.Log{BlockHash: hashBlock100, BlockNumber: 100, Address: addr}
		data.mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.MatchedBy(byHashQuery(hashBlock100))).
			Return([]ethtypes.Log{omittedLog}, nil).Once()

		_, err := data.mdr.StepSafe(t.Context())
		require.Error(t, err)
		require.Contains(t, err.Error(), "omitted 1 log")
	})

	t.Run("bloom false positive -> step succeeds", func(t *testing.T) {
		data := setup(t)
		data.mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.MatchedBy(byHashQuery(hashBlock100))).
			Return([]ethtypes.Log{}, nil).Twice()

		finished, err := data.mdr.StepSafe(t.Context())
		require.NoError(t, err)
		require.True(t, finished)
	})
}
