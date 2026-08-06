package sync

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newLogsCompletenessSUT builds a bare EVMDownloaderImplementation for unit-testing
// checkLogsCompleteness directly, without going through GetEventsByBlockRange.
func newLogsCompletenessSUT(t *testing.T, mockEthClient *aggkittypesmocks.MultiDownloader) *EVMDownloaderImplementation {
	t.Helper()
	return &EVMDownloaderImplementation{
		ethClient:        mockEthClient,
		addressesToQuery: []common.Address{contractAddr},
		log:              log.WithFields("test", "logsCompleteness"),
		rh: &RetryHandler{
			RetryAfterErrorPeriod:      time.Millisecond,
			MaxRetryAttemptsAfterError: 5,
		},
	}
}

// TestGetEventsByBlockRange_LogsOmissionHealedOnRetry covers the end-to-end healing path: the
// first eth_getLogs range query silently omits a log for a bloom-positive block, arbitration
// confirms the omission (finds the log by BlockHash), the range is retried, and the second
// attempt returns the log.
func TestGetEventsByBlockRange_LogsOmissionHealedOnRetry(t *testing.T) {
	ctx := context.Background()
	d, clientMock := NewTestDownloader(t, time.Millisecond)

	blockNum := uint64(10)
	logC, updateC := generateEvent(uint32(blockNum))

	header := types.Header{Number: big.NewInt(int64(blockNum)), ParentHash: common.HexToHash("foo")}
	blockHash := header.Hash() // matches logC.BlockHash produced by generateEvent
	parentHash := common.HexToHash("foo")

	var bloom types.Bloom
	bloom.Add(contractAddr.Bytes())

	// lastFinalizedBlock (5) < blockNum (10): the block is in the verified, unfinalized zone.
	clientMock.EXPECT().BlockNumber(mock.Anything, mock.Anything).Return(uint64(5), nil).Once()

	addressQuery := ethereum.FilterQuery{
		Addresses: []common.Address{contractAddr},
		FromBlock: new(big.Int).SetUint64(blockNum),
		ToBlock:   new(big.Int).SetUint64(blockNum),
	}
	// First attempt: eth_getLogs silently omits the log even though the block is bloom-positive.
	clientMock.EXPECT().FilterLogs(mock.Anything, addressQuery).Return([]types.Log{}, nil).Once()

	// checkLogsCompleteness fetches the header for the (apparently) empty block and finds a
	// positive bloom for the queried address.
	clientMock.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(blockNum)).
		Return(&aggkittypes.BlockHeader{
			Number:     blockNum,
			Hash:       blockHash,
			ParentHash: &parentHash,
			LogsBloom:  &bloom,
		}, nil)

	// Arbitration re-query by block hash finds the log: the omission is confirmed genuine.
	arbitrationQuery := ethereum.FilterQuery{
		BlockHash: &blockHash,
		Addresses: []common.Address{contractAddr},
	}
	clientMock.EXPECT().FilterLogs(mock.Anything, arbitrationQuery).Return([]types.Log{*logC}, nil).Once()

	// Second attempt (range retried indefinitely on confirmed omission): the log now comes back.
	clientMock.EXPECT().FilterLogs(mock.Anything, addressQuery).Return([]types.Log{*logC}, nil).Once()

	blocks := d.GetEventsByBlockRange(ctx, blockNum, blockNum)

	require.Len(t, blocks, 1)
	require.Equal(t, blockNum, blocks[0].Num)
	require.Equal(t, []interface{}{updateC}, blocks[0].Events)
	clientMock.AssertExpectations(t)
}

// TestGetEventsByBlockRange_LogsBloomFalsePositive covers the deterministic-false-positive path:
// the block is bloom-positive but genuinely has no log for the queried address, so arbitration
// consistently returns empty. The range must be accepted as-is, with no unbounded retry loop.
func TestGetEventsByBlockRange_LogsBloomFalsePositive(t *testing.T) {
	ctx := context.Background()
	d, clientMock := NewTestDownloader(t, time.Millisecond)

	blockNum := uint64(10)
	header := types.Header{Number: big.NewInt(int64(blockNum)), ParentHash: common.HexToHash("foo")}
	blockHash := header.Hash()
	parentHash := common.HexToHash("foo")

	var bloom types.Bloom
	bloom.Add(contractAddr.Bytes())

	clientMock.EXPECT().BlockNumber(mock.Anything, mock.Anything).Return(uint64(5), nil).Once()

	addressQuery := ethereum.FilterQuery{
		Addresses: []common.Address{contractAddr},
		FromBlock: new(big.Int).SetUint64(blockNum),
		ToBlock:   new(big.Int).SetUint64(blockNum),
	}
	// Only ONE call expected: no retry loop must be triggered by a bloom false positive.
	clientMock.EXPECT().FilterLogs(mock.Anything, addressQuery).Return([]types.Log{}, nil).Once()

	clientMock.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(blockNum)).
		Return(&aggkittypes.BlockHeader{
			Number:     blockNum,
			Hash:       blockHash,
			ParentHash: &parentHash,
			LogsBloom:  &bloom,
		}, nil).Once()

	arbitrationQuery := ethereum.FilterQuery{
		BlockHash: &blockHash,
		Addresses: []common.Address{contractAddr},
	}
	// Arbitration consistently empty (bounded at maxArbitrationAttempts = 2).
	clientMock.EXPECT().FilterLogs(mock.Anything, arbitrationQuery).Return([]types.Log{}, nil).Twice()

	blocks := d.GetEventsByBlockRange(ctx, blockNum, blockNum)

	require.Empty(t, blocks)
	clientMock.AssertExpectations(t)
}

// TestCheckLogsCompleteness_FinalizedZoneSkipped verifies that a block at or below
// lastFinalizedBlock is never checked: no header is fetched and no arbitration query is issued,
// regardless of what its bloom would have said.
func TestCheckLogsCompleteness_FinalizedZoneSkipped(t *testing.T) {
	ctx := context.Background()
	mockEthClient := aggkittypesmocks.NewMultiDownloader(t)
	sut := newLogsCompletenessSUT(t, mockEthClient)

	// fromBlock == toBlock == lastFinalizedBlock: entirely within the finalized zone.
	confirmed := sut.checkLogsCompleteness(ctx, 10, 10, 10, nil)

	require.False(t, confirmed)
	mockEthClient.AssertExpectations(t) // no HeaderByNumber/FilterLogs calls expected or made
}

// TestCheckLogsCompleteness_NilBloomSkipped verifies graceful degradation: when the header's bloom
// is nil (not provided by the retrieval path), the block is never treated as suspicious and no
// arbitration query is issued.
func TestCheckLogsCompleteness_NilBloomSkipped(t *testing.T) {
	ctx := context.Background()
	mockEthClient := aggkittypesmocks.NewMultiDownloader(t)
	sut := newLogsCompletenessSUT(t, mockEthClient)

	blockNum := uint64(10)
	header := types.Header{Number: big.NewInt(int64(blockNum)), ParentHash: common.HexToHash("foo")}
	blockHash := header.Hash()
	parentHash := common.HexToHash("foo")

	mockEthClient.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(blockNum)).
		Return(&aggkittypes.BlockHeader{
			Number:     blockNum,
			Hash:       blockHash,
			ParentHash: &parentHash,
			LogsBloom:  nil,
		}, nil).Once()

	// lastFinalizedBlock = blockNum-1 puts blockNum inside the verify window; unfilteredLogs is
	// empty for it, so the header is fetched, but its nil bloom must skip the check.
	confirmed := sut.checkLogsCompleteness(ctx, blockNum, blockNum, blockNum-1, nil)

	require.False(t, confirmed)
	mockEthClient.AssertExpectations(t) // header fetched once; no arbitration FilterLogs call
}

// TestCheckLogsCompleteness_NonQueriedTopicNotSuspicious verifies the topic subtlety: a block
// whose contract emitted only a non-queried event has an unfiltered log (address matched) even
// though filterLogs would later drop it on topic. Since checkLogsCompleteness is given the
// unfiltered logs, that block must never be flagged as suspicious.
func TestCheckLogsCompleteness_NonQueriedTopicNotSuspicious(t *testing.T) {
	ctx := context.Background()
	mockEthClient := aggkittypesmocks.NewMultiDownloader(t)
	sut := newLogsCompletenessSUT(t, mockEthClient)

	blockNum := uint64(10)
	nonQueriedLog := types.Log{
		Address:     contractAddr,
		BlockNumber: blockNum,
		Topics:      []common.Hash{common.HexToHash("0xabc123")}, // not eventSignature
	}

	confirmed := sut.checkLogsCompleteness(ctx, blockNum, blockNum, blockNum-1, []types.Log{nonQueriedLog})

	require.False(t, confirmed)
	// No HeaderByNumber/FilterLogs calls: the block is already accounted for by the unfiltered log.
	mockEthClient.AssertExpectations(t)
}

// TestCheckLogsCompleteness_UnverifiableSuspicionRetriesRange verifies the conservative verdict:
// when a block is bloom-positive with no logs and every arbitration re-query fails to execute, no
// verdict can be reached, and the range must be retried rather than the suspicious block being
// silently accepted (silent acceptance is exactly the failure mode this check exists to prevent).
func TestCheckLogsCompleteness_UnverifiableSuspicionRetriesRange(t *testing.T) {
	ctx := context.Background()
	mockEthClient := aggkittypesmocks.NewMultiDownloader(t)
	sut := newLogsCompletenessSUT(t, mockEthClient)

	blockNum := uint64(10)
	header := types.Header{Number: big.NewInt(int64(blockNum)), ParentHash: common.HexToHash("foo")}
	blockHash := header.Hash()
	parentHash := common.HexToHash("foo")

	var bloom types.Bloom
	bloom.Add(contractAddr.Bytes())

	mockEthClient.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(blockNum)).
		Return(&aggkittypes.BlockHeader{
			Number:     blockNum,
			Hash:       blockHash,
			ParentHash: &parentHash,
			LogsBloom:  &bloom,
		}, nil).Once()

	// Every arbitration attempt fails to execute (RPC/transport error, not an empty result).
	arbitrationQuery := ethereum.FilterQuery{
		BlockHash: &blockHash,
		Addresses: []common.Address{contractAddr},
	}
	mockEthClient.EXPECT().FilterLogs(mock.Anything, arbitrationQuery).
		Return(nil, errors.New("blockHash filter not supported")).Times(maxArbitrationAttempts)

	confirmed := sut.checkLogsCompleteness(ctx, blockNum, blockNum, blockNum-1, nil)

	require.True(t, confirmed)
	mockEthClient.AssertExpectations(t)
}
