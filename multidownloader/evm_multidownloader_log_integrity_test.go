package multidownloader

import (
	"fmt"
	"testing"

	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestEVMMultidownloader_StepSafeRejectsLogHeaderMismatch pins that a safe step
// fails closed when a downloaded log does not match the block header retrieved
// for its block: StepSafe must return the typed ErrLogBlockHeaderMismatch before
// any state or storage mutation, so neither the in-memory coverage nor the
// storage is advanced.
func TestEVMMultidownloader_StepSafeRejectsLogHeaderMismatch(t *testing.T) {
	data := newEVMMultidownloaderTestData(t, false)
	data.mockEthClient.EXPECT().ChainID(mock.Anything).Return(common.Big1, nil)

	syncerAddr := common.HexToAddress("0x1")
	require.NoError(t, data.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID:          "syncer1",
		ContractAddresses: []common.Address{syncerAddr},
		FromBlock:         100,
		ToBlock:           aggkittypes.FinalizedBlock,
	}))
	data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.FinalizedBlock).
		Return(uint64(150), nil).Maybe()
	require.NoError(t, data.mdr.Initialize(t.Context()))

	const blockNumber = uint64(120)
	logHash := common.HexToHash("0xabc")
	headerHash := common.HexToHash("0xdef")
	require.NotEqual(t, logHash, headerHash, "fixture requires two distinct hashes")

	// The downloaded log carries one hash, the retrieved header another.
	data.mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).
		Return([]ethtypes.Log{{
			Address:        syncerAddr,
			BlockNumber:    blockNumber,
			BlockHash:      logHash,
			BlockTimestamp: 1630000000,
			Topics:         []common.Hash{},
		}}, nil).Once()

	rpcResult := aggkittypes.NewBlockHeadersResult()
	rpcResult.AddHeader(blockNumber, aggkittypes.NewBlockHeader(blockNumber, headerHash, 1630000000, nil))
	data.mockEthClient.EXPECT().
		RetrieveBlockHeaders(mock.Anything, []uint64{blockNumber}, data.mdr.cfg.MaxParallelBlockHeaderRetrieval).
		Return(rpcResult, nil).Once()

	finished, err := data.mdr.StepSafe(t.Context())
	require.Error(t, err)
	require.ErrorIs(t, err, mdrtypes.ErrLogBlockHeaderMismatch)
	require.False(t, finished)

	// Nothing reached storage: no header, no log.
	storedHeader, _, err := data.realStorage.GetBlockHeaderByNumber(nil, blockNumber)
	require.NoError(t, err)
	require.Nil(t, storedHeader, "the contradictory header must not be stored")
	storedLogs, err := data.realStorage.GetEthLogs(nil,
		mdrtypes.NewLogQuery(blockNumber, blockNumber, []common.Address{syncerAddr}))
	require.NoError(t, err)
	require.Empty(t, storedLogs, "the contradictory log must not be stored")

	// The in-memory synced coverage was not advanced either: the registered
	// contract keeps an empty segment, nothing covers the rejected block.
	for _, seg := range data.mdr.state.SyncedSegmentsByContract([]common.Address{syncerAddr}) {
		require.True(t, seg.IsEmpty(),
			"a rejected batch must not advance the in-memory coverage (got %s)", seg.String())
	}
}

// TestEVMMultidownloader_StepSafeRejectsPartialHeaderResult pins that a safe
// step fails closed when block header retrieval only partially succeeds: the
// downloaded logs match the headers that were retrieved, but one block's header
// is missing because its retrieval errored. StepSafe must return the typed
// ErrLogBlockHeaderMismatch before any state or storage mutation, so neither the
// in-memory coverage nor the storage is advanced.
func TestEVMMultidownloader_StepSafeRejectsPartialHeaderResult(t *testing.T) {
	data := newEVMMultidownloaderTestData(t, false)
	data.mockEthClient.EXPECT().ChainID(mock.Anything).Return(common.Big1, nil)

	syncerAddr := common.HexToAddress("0x1")
	require.NoError(t, data.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID:          "syncer1",
		ContractAddresses: []common.Address{syncerAddr},
		FromBlock:         100,
		ToBlock:           aggkittypes.FinalizedBlock,
	}))
	data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.FinalizedBlock).
		Return(uint64(150), nil).Maybe()
	require.NoError(t, data.mdr.Initialize(t.Context()))

	const (
		blockNumberA = uint64(120)
		blockNumberB = uint64(121)
	)
	hashA := common.HexToHash("0xaaa")
	hashB := common.HexToHash("0xbbb")

	// Two downloaded logs, each carrying the hash of its own block header.
	data.mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).
		Return([]ethtypes.Log{
			{
				Address:        syncerAddr,
				BlockNumber:    blockNumberA,
				BlockHash:      hashA,
				BlockTimestamp: 1630000000,
				Topics:         []common.Hash{},
			},
			{
				Address:        syncerAddr,
				BlockNumber:    blockNumberB,
				BlockHash:      hashB,
				BlockTimestamp: 1630000001,
				Topics:         []common.Hash{},
			},
		}, nil).Once()

	// Partial header result: block 120 succeeds, block 121 errors out, so the
	// log for block 121 has no header to match against.
	rpcResult := aggkittypes.NewBlockHeadersResult()
	rpcResult.AddHeader(blockNumberA, aggkittypes.NewBlockHeader(blockNumberA, hashA, 1630000000, nil))
	rpcResult.AddError(blockNumberB, fmt.Errorf("rpc error"))
	data.mockEthClient.EXPECT().
		RetrieveBlockHeaders(mock.Anything, []uint64{blockNumberA, blockNumberB},
			data.mdr.cfg.MaxParallelBlockHeaderRetrieval).
		Return(rpcResult, nil).Once()

	finished, err := data.mdr.StepSafe(t.Context())
	require.Error(t, err)
	require.ErrorIs(t, err, mdrtypes.ErrLogBlockHeaderMismatch)
	require.False(t, finished)

	// Nothing reached storage: no header for either block, no log.
	for _, blockNumber := range []uint64{blockNumberA, blockNumberB} {
		storedHeader, _, err := data.realStorage.GetBlockHeaderByNumber(nil, blockNumber)
		require.NoError(t, err)
		require.Nil(t, storedHeader, "the partially retrieved batch must not be stored")
	}
	storedLogs, err := data.realStorage.GetEthLogs(nil,
		mdrtypes.NewLogQuery(blockNumberA, blockNumberB, []common.Address{syncerAddr}))
	require.NoError(t, err)
	require.Empty(t, storedLogs, "the partially retrieved batch must not be stored")

	// The in-memory synced coverage was not advanced either: the registered
	// contract keeps an empty segment, nothing covers the rejected blocks.
	for _, seg := range data.mdr.state.SyncedSegmentsByContract([]common.Address{syncerAddr}) {
		require.True(t, seg.IsEmpty(),
			"a rejected batch must not advance the in-memory coverage (got %s)", seg.String())
	}
}
