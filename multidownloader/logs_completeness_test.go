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

// uniformAddrsForBlock returns an addrsForBlock func that yields the same addresses for every block.
func uniformAddrsForBlock(addrs []common.Address) func(uint64) []common.Address {
	return func(uint64) []common.Address { return addrs }
}

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

// StepSafe must not fetch headers of blocks without logs nor run the logsBloom completeness check:
// doing so made the initial sync dramatically slower (issue #1930).
func TestEVMMultidownloader_StepSafe_NoCheckRPCLogsAgainstBloom(t *testing.T) {
	addr := common.HexToAddress("0x1")

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
	require.NoError(t, data.mdr.Initialize(t.Context()))

	// Range-wide eth_getLogs returns no logs. Neither RetrieveBlockHeaders nor a by-hash FilterLogs
	// is expected (the mock fails on any unexpected call).
	data.mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.MatchedBy(func(q ethereum.FilterQuery) bool {
		return q.BlockHash == nil
	})).Return([]ethtypes.Log{}, nil).Once()

	finished, err := data.mdr.StepSafe(t.Context())
	require.NoError(t, err)
	require.True(t, finished)
}

// TestEVMMultidownloader_GetAddrsForBlockNumbers_OnlySyncersWithCheckEnabled verifies that the
// completeness check (StepUnsafe) only considers the addresses of syncers that enabled it.
func TestEVMMultidownloader_GetAddrsForBlockNumbers_OnlySyncersWithCheckEnabled(t *testing.T) {
	checkedAddr := common.HexToAddress("0x1")
	uncheckedAddr := common.HexToAddress("0x2")

	data := newEVMMultidownloaderTestData(t, false)
	data.mockEthClient.EXPECT().ChainID(mock.Anything).Return(common.Big1, nil)
	for _, syncer := range []aggkittypes.SyncerConfig{
		{
			SyncerID: "checked", ContractAddresses: []common.Address{checkedAddr},
			FromBlock: 100, ToBlock: aggkittypes.FinalizedBlock, CheckRPCLogsAgainstBloom: true,
		},
		{
			SyncerID: "unchecked", ContractAddresses: []common.Address{uncheckedAddr},
			FromBlock: 100, ToBlock: aggkittypes.FinalizedBlock,
		},
	} {
		require.NoError(t, data.mdr.RegisterSyncer(syncer))
	}
	data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.FinalizedBlock).
		Return(uint64(101), nil).Maybe()
	require.NoError(t, data.mdr.Initialize(t.Context()))

	addrs := data.mdr.getAddrsForBlockNumbers([]uint64{100})
	require.Equal(t, []common.Address{checkedAddr}, addrs[100])
}
