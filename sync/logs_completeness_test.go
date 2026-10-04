package sync

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/prometheus"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

// positiveBloom returns a bloom that is positive for contractAddr.
func positiveBloom() *types.Bloom {
	var bloom types.Bloom
	bloom.Add(contractAddr.Bytes())
	return &bloom
}

// omissionHeader returns the bloom-positive header of blockNum, with the same hash generateEvent uses.
func omissionHeader(blockNum uint64, hash common.Hash) *aggkittypes.BlockHeader {
	parentHash := common.HexToHash("foo")
	return &aggkittypes.BlockHeader{
		Number:     blockNum,
		Hash:       hash,
		ParentHash: &parentHash,
		LogsBloom:  positiveBloom(),
	}
}

// blockHashOf returns the block hash generateEvent assigns to blockNum.
func blockHashOf(blockNum uint64) common.Hash {
	header := types.Header{Number: new(big.Int).SetUint64(blockNum), ParentHash: common.HexToHash("foo")}
	return header.Hash()
}

func rangeQuery(from, to uint64) ethereum.FilterQuery {
	return ethereum.FilterQuery{
		Addresses: []common.Address{contractAddr},
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(to),
	}
}

func arbitrationQueryFor(hash common.Hash) ethereum.FilterQuery {
	return ethereum.FilterQuery{BlockHash: &hash, Addresses: []common.Address{contractAddr}}
}

// newSpliceSUT builds a downloader through the constructor (so the metrics are registered) with a
// syncer ID unique to the test, because the metric counters are process-global.
func newSpliceSUT(
	t *testing.T, syncerID string,
) (*EVMDownloaderImplementation, *aggkittypesmocks.MultiDownloader) {
	t.Helper()
	prometheus.Init()
	clientMock := aggkittypesmocks.NewMultiDownloader(t)
	finalized := aggkittypes.FinalizedBlock
	sut := NewEVMDownloaderImplementation(
		syncerID, clientMock, aggkittypes.LatestBlock, time.Millisecond,
		buildAppender(), []common.Address{contractAddr},
		&RetryHandler{RetryAfterErrorPeriod: time.Millisecond, MaxRetryAttemptsAfterError: 5},
		&finalized, nil, "test-reorg-detector-id",
	)
	return sut, clientMock
}

// omissionCounter reads the process-global omission counters for syncerID.
func omissionCounter(t *testing.T, name, syncerID string) float64 {
	t.Helper()
	cv, ok := prometheus.CounterVec(name)
	require.True(t, ok, name)
	return testutil.ToFloat64(cv.WithLabelValues(syncerID))
}

const (
	confirmedCounterName    = "logs_omission_confirmed_total"
	rangeRetriesCounterName = "logs_omission_range_retries_total"
)

// TestGetEventsByBlockRange_LogsOmissionSplicedFromArbitration covers the end-to-end splice path: the
// eth_getLogs range query silently omits a log for a bloom-positive block, arbitration confirms the
// omission (finds the log by BlockHash) and the recovered log is spliced into the result without
// retrying the range.
func TestGetEventsByBlockRange_LogsOmissionSplicedFromArbitration(t *testing.T) {
	ctx := context.Background()
	const syncerID = "splice-from-arbitration"
	sut, clientMock := newSpliceSUT(t, syncerID)
	confirmedBefore := omissionCounter(t, confirmedCounterName, syncerID)
	retriesBefore := omissionCounter(t, rangeRetriesCounterName, syncerID)

	blockNum := uint64(10)
	logC, updateC := generateEvent(uint32(blockNum))
	blockHash := blockHashOf(blockNum)

	// lastFinalizedBlock (5) < blockNum (10): the block is in the verified, unfinalized zone.
	clientMock.EXPECT().BlockNumber(mock.Anything, mock.Anything).Return(uint64(5), nil).Once()
	// The range query is expected exactly once: no whole-range retry.
	clientMock.EXPECT().FilterLogs(mock.Anything, rangeQuery(blockNum, blockNum)).Return([]types.Log{}, nil).Once()
	clientMock.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(blockNum)).
		Return(omissionHeader(blockNum, blockHash), nil)
	clientMock.EXPECT().FilterLogs(mock.Anything, arbitrationQueryFor(blockHash)).
		Return([]types.Log{*logC}, nil).Once()

	blocks := sut.GetEventsByBlockRange(ctx, blockNum, blockNum)

	require.Len(t, blocks, 1)
	require.Equal(t, blockNum, blocks[0].Num)
	require.Equal(t, []interface{}{updateC}, blocks[0].Events)
	require.Equal(t, confirmedBefore+1, omissionCounter(t, confirmedCounterName, syncerID))
	require.Equal(t, retriesBefore, omissionCounter(t, rangeRetriesCounterName, syncerID))
	clientMock.AssertExpectations(t)
}

// TestGetEventsByBlockRange_LogsOmissionAboveCapFallsBackToRangeRetry verifies that more than
// maxSplicedOmittedBlocks confirmed omissions fall back to the whole-range retry.
func TestGetEventsByBlockRange_LogsOmissionAboveCapFallsBackToRangeRetry(t *testing.T) {
	ctx := context.Background()
	const syncerID = "splice-above-cap"
	sut, clientMock := newSpliceSUT(t, syncerID)
	confirmedBefore := omissionCounter(t, confirmedCounterName, syncerID)
	retriesBefore := omissionCounter(t, rangeRetriesCounterName, syncerID)

	fromBlock := uint64(10)
	toBlock := fromBlock + maxSplicedOmittedBlocks // maxSplicedOmittedBlocks+1 blocks
	var all []types.Log
	for n := fromBlock; n <= toBlock; n++ {
		l, _ := generateEvent(uint32(n))
		all = append(all, *l)
		clientMock.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(n)).
			Return(omissionHeader(n, blockHashOf(n)), nil)
		clientMock.EXPECT().FilterLogs(mock.Anything, arbitrationQueryFor(blockHashOf(n))).
			Return([]types.Log{*l}, nil)
	}

	clientMock.EXPECT().BlockNumber(mock.Anything, mock.Anything).Return(uint64(5), nil).Once()
	clientMock.EXPECT().FilterLogs(mock.Anything, rangeQuery(fromBlock, toBlock)).Return([]types.Log{}, nil).Once()
	clientMock.EXPECT().FilterLogs(mock.Anything, rangeQuery(fromBlock, toBlock)).Return(all, nil).Once()

	blocks := sut.GetEventsByBlockRange(ctx, fromBlock, toBlock)

	require.Len(t, blocks, int(maxSplicedOmittedBlocks)+1)
	require.Equal(t, confirmedBefore+float64(maxSplicedOmittedBlocks+1), omissionCounter(t, confirmedCounterName, syncerID))
	require.Equal(t, retriesBefore+1, omissionCounter(t, rangeRetriesCounterName, syncerID))
	clientMock.AssertExpectations(t)
}

// TestGetEventsByBlockRange_LogsOmissionUnverifiableRetriesRange verifies that a suspicious block
// whose arbitration cannot execute falls back to the whole-range retry.
func TestGetEventsByBlockRange_LogsOmissionUnverifiableRetriesRange(t *testing.T) {
	ctx := context.Background()
	const syncerID = "splice-unverifiable"
	sut, clientMock := newSpliceSUT(t, syncerID)
	retriesBefore := omissionCounter(t, rangeRetriesCounterName, syncerID)

	blockNum := uint64(10)
	logC, updateC := generateEvent(uint32(blockNum))
	blockHash := blockHashOf(blockNum)

	clientMock.EXPECT().BlockNumber(mock.Anything, mock.Anything).Return(uint64(5), nil).Once()
	clientMock.EXPECT().FilterLogs(mock.Anything, rangeQuery(blockNum, blockNum)).Return([]types.Log{}, nil).Once()
	clientMock.EXPECT().FilterLogs(mock.Anything, rangeQuery(blockNum, blockNum)).
		Return([]types.Log{*logC}, nil).Once()
	clientMock.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(blockNum)).
		Return(omissionHeader(blockNum, blockHash), nil)
	clientMock.EXPECT().FilterLogs(mock.Anything, arbitrationQueryFor(blockHash)).
		Return(nil, errors.New("blockHash filter not supported")).Times(maxArbitrationAttempts)

	blocks := sut.GetEventsByBlockRange(ctx, blockNum, blockNum)

	require.Len(t, blocks, 1)
	require.Equal(t, []interface{}{updateC}, blocks[0].Events)
	require.Equal(t, retriesBefore+1, omissionCounter(t, rangeRetriesCounterName, syncerID))
	clientMock.AssertExpectations(t)
}

// TestGetEventsByBlockRange_SplicedBlockHashMismatchRetries verifies that a reorg between arbitration
// and block assembly is handled by the existing hash-mismatch retry for spliced logs.
func TestGetEventsByBlockRange_SplicedBlockHashMismatchRetries(t *testing.T) {
	ctx := context.Background()
	sut, clientMock := newSpliceSUT(t, "splice-hash-mismatch")

	blockNum := uint64(10)
	logC, updateC := generateEvent(uint32(blockNum))
	h1 := blockHashOf(blockNum)
	h2 := common.HexToHash("0xbeef")
	reorgedLog := *logC
	reorgedLog.BlockHash = h2

	clientMock.EXPECT().BlockNumber(mock.Anything, mock.Anything).Return(uint64(5), nil).Once()
	clientMock.EXPECT().FilterLogs(mock.Anything, rangeQuery(blockNum, blockNum)).Return([]types.Log{}, nil).Once()
	clientMock.EXPECT().FilterLogs(mock.Anything, rangeQuery(blockNum, blockNum)).
		Return([]types.Log{reorgedLog}, nil).Once()
	clientMock.EXPECT().FilterLogs(mock.Anything, arbitrationQueryFor(h1)).Return([]types.Log{*logC}, nil).Once()
	// 1st header call: completeness check (H1). 2nd: assembly of the spliced block, which now
	// reports H2 (reorg). Later calls keep reporting H2.
	clientMock.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(blockNum)).
		Return(omissionHeader(blockNum, h1), nil).Once()
	clientMock.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(blockNum)).
		Return(omissionHeader(blockNum, h2), nil)

	blocks := sut.GetEventsByBlockRange(ctx, blockNum, blockNum)

	require.Len(t, blocks, 1)
	require.Equal(t, h2, blocks[0].Hash)
	require.Equal(t, []interface{}{updateC}, blocks[0].Events)
	clientMock.AssertExpectations(t)
}

// TestGetEventsByBlockRange_SplicedLogsPassThroughFilterAndHook verifies that spliced logs go through
// the topic filter and the logs hook like range logs.
func TestGetEventsByBlockRange_SplicedLogsPassThroughFilterAndHook(t *testing.T) {
	ctx := context.Background()
	sut, clientMock := newSpliceSUT(t, "splice-filter-hook")

	blockNum := uint64(10)
	logC, updateC := generateEvent(uint32(blockNum))
	blockHash := blockHashOf(blockNum)
	logC.Index = 0
	nonQueried := types.Log{
		Address:     contractAddr,
		BlockNumber: blockNum,
		BlockHash:   blockHash,
		Index:       1,
		Topics:      []common.Hash{common.HexToHash("0xabc123")},
	}

	var hookLogs []types.Log
	sut.SetLogsHook(func(_ context.Context, _, _ uint64, logs []types.Log) []types.Log {
		hookLogs = append(hookLogs, logs...)
		return logs
	})

	clientMock.EXPECT().BlockNumber(mock.Anything, mock.Anything).Return(uint64(5), nil).Once()
	clientMock.EXPECT().FilterLogs(mock.Anything, rangeQuery(blockNum, blockNum)).Return([]types.Log{}, nil).Once()
	clientMock.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(blockNum)).
		Return(omissionHeader(blockNum, blockHash), nil)
	clientMock.EXPECT().FilterLogs(mock.Anything, arbitrationQueryFor(blockHash)).
		Return([]types.Log{nonQueried, *logC}, nil).Once()

	blocks := sut.GetEventsByBlockRange(ctx, blockNum, blockNum)

	require.Len(t, hookLogs, 1)
	require.Equal(t, *logC, hookLogs[0])
	require.Len(t, blocks, 1)
	require.Equal(t, []interface{}{updateC}, blocks[0].Events)
	clientMock.AssertExpectations(t)
}

// capturedLog is one entry recorded by capturingLogger.
type capturedLog struct {
	level string
	msg   string
}

// capturingLogger is an aggkitcommon.Logger that records every message with its level.
type capturingLogger struct {
	entries []capturedLog
}

var _ aggkitcommon.Logger = (*capturingLogger)(nil)

func (c *capturingLogger) add(level, msg string) {
	c.entries = append(c.entries, capturedLog{level: level, msg: msg})
}

func (c *capturingLogger) count(level, contains string) int {
	n := 0
	for _, e := range c.entries {
		if e.level == level && strings.Contains(e.msg, contains) {
			n++
		}
	}
	return n
}

func (c *capturingLogger) Panicf(format string, args ...interface{}) {
	c.add("panic", fmt.Sprintf(format, args...))
}
func (c *capturingLogger) Fatalf(format string, args ...interface{}) {
	c.add("fatal", fmt.Sprintf(format, args...))
}
func (c *capturingLogger) Info(args ...interface{}) { c.add("info", fmt.Sprint(args...)) }
func (c *capturingLogger) Infof(format string, args ...interface{}) {
	c.add("info", fmt.Sprintf(format, args...))
}
func (c *capturingLogger) Error(args ...interface{}) { c.add("error", fmt.Sprint(args...)) }
func (c *capturingLogger) Errorf(format string, args ...interface{}) {
	c.add("error", fmt.Sprintf(format, args...))
}
func (c *capturingLogger) Warn(args ...interface{}) { c.add("warn", fmt.Sprint(args...)) }
func (c *capturingLogger) Warnf(format string, args ...interface{}) {
	c.add("warn", fmt.Sprintf(format, args...))
}
func (c *capturingLogger) Debug(args ...interface{}) { c.add("debug", fmt.Sprint(args...)) }
func (c *capturingLogger) Debugf(format string, args ...interface{}) {
	c.add("debug", fmt.Sprintf(format, args...))
}

// TestGetEventsByBlockRange_LogsOmissionLogLevels verifies that the splice path logs no Error, one
// Warn per spliced range, and the per-block confirmation at Debug.
func TestGetEventsByBlockRange_LogsOmissionLogLevels(t *testing.T) {
	ctx := context.Background()
	sut, clientMock := newSpliceSUT(t, "splice-log-levels")
	logger := &capturingLogger{}
	sut.log = logger

	blockNum := uint64(10)
	logC, _ := generateEvent(uint32(blockNum))
	blockHash := blockHashOf(blockNum)

	clientMock.EXPECT().BlockNumber(mock.Anything, mock.Anything).Return(uint64(5), nil).Once()
	clientMock.EXPECT().FilterLogs(mock.Anything, rangeQuery(blockNum, blockNum)).Return([]types.Log{}, nil).Once()
	clientMock.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(blockNum)).
		Return(omissionHeader(blockNum, blockHash), nil)
	clientMock.EXPECT().FilterLogs(mock.Anything, arbitrationQueryFor(blockHash)).
		Return([]types.Log{*logC}, nil).Once()

	blocks := sut.GetEventsByBlockRange(ctx, blockNum, blockNum)

	require.Len(t, blocks, 1)
	require.Zero(t, logger.count("error", ""))
	require.Equal(t, 1, logger.count("warn", ""))
	require.Equal(t, 1, logger.count("warn", "spliced"))
	require.Equal(t, 1, logger.count("debug", "confirmed eth_getLogs omission for block"))
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
	res := sut.checkLogsCompleteness(ctx, 10, 10, 10, nil)

	require.Equal(t, completenessResult{}, res)
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
	res := sut.checkLogsCompleteness(ctx, blockNum, blockNum, blockNum-1, nil)

	require.Equal(t, completenessResult{}, res)
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

	res := sut.checkLogsCompleteness(ctx, blockNum, blockNum, blockNum-1, []types.Log{nonQueriedLog})

	require.Equal(t, completenessResult{}, res)
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

	res := sut.checkLogsCompleteness(ctx, blockNum, blockNum, blockNum-1, nil)

	require.True(t, res.needsRangeRetry)
	require.Empty(t, res.splicedLogs)
	require.False(t, res.canceled)
	mockEthClient.AssertExpectations(t)
}

// expectOmittedBlock registers the header and a confirming by-hash arbitration for blockNum.
func expectOmittedBlock(t *testing.T, mockEthClient *aggkittypesmocks.MultiDownloader, blockNum uint64) types.Log {
	t.Helper()
	l, _ := generateEvent(uint32(blockNum))
	mockEthClient.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(blockNum)).
		Return(omissionHeader(blockNum, blockHashOf(blockNum)), nil).Once()
	mockEthClient.EXPECT().FilterLogs(mock.Anything, arbitrationQueryFor(blockHashOf(blockNum))).
		Return([]types.Log{*l}, nil).Once()
	return *l
}

// TestCheckLogsCompleteness_CollectsAllConfirmedOmissions verifies that every confirmed omission is
// arbitrated and its logs collected, without requesting a range retry.
func TestCheckLogsCompleteness_CollectsAllConfirmedOmissions(t *testing.T) {
	ctx := context.Background()
	mockEthClient := aggkittypesmocks.NewMultiDownloader(t)
	sut := newLogsCompletenessSUT(t, mockEthClient)

	var want []types.Log
	for n := uint64(10); n <= 12; n++ {
		want = append(want, expectOmittedBlock(t, mockEthClient, n))
	}

	res := sut.checkLogsCompleteness(ctx, 10, 12, 9, nil)

	require.False(t, res.needsRangeRetry)
	require.False(t, res.canceled)
	require.Equal(t, 3, res.omittedBlocks)
	require.Equal(t, want, res.splicedLogs)
	mockEthClient.AssertExpectations(t)
}

// TestCheckLogsCompleteness_ExceedsSpliceCapRequestsRangeRetry verifies that the check stops at the
// first confirmation beyond maxSplicedOmittedBlocks and asks for a range retry without logs.
func TestCheckLogsCompleteness_ExceedsSpliceCapRequestsRangeRetry(t *testing.T) {
	ctx := context.Background()
	mockEthClient := aggkittypesmocks.NewMultiDownloader(t)
	sut := newLogsCompletenessSUT(t, mockEthClient)

	fromBlock := uint64(10)
	toBlock := fromBlock + maxSplicedOmittedBlocks + 1 // maxSplicedOmittedBlocks+2 suspicious blocks
	// Exactly maxSplicedOmittedBlocks+1 blocks are arbitrated; the last block is never touched (the
	// mock fails the test on any unexpected call).
	for n := fromBlock; n < toBlock; n++ {
		expectOmittedBlock(t, mockEthClient, n)
	}

	res := sut.checkLogsCompleteness(ctx, fromBlock, toBlock, fromBlock-1, nil)

	require.True(t, res.needsRangeRetry)
	require.Nil(t, res.splicedLogs)
	require.Equal(t, maxSplicedOmittedBlocks+1, res.omittedBlocks)
	mockEthClient.AssertExpectations(t)
}

// TestCheckLogsCompleteness_ArbitrationLogWithWrongBlockNumberIsUnverifiable verifies refinement R1:
// a log with the right hash but another block number is never kept.
func TestCheckLogsCompleteness_ArbitrationLogWithWrongBlockNumberIsUnverifiable(t *testing.T) {
	ctx := context.Background()
	mockEthClient := aggkittypesmocks.NewMultiDownloader(t)
	sut := newLogsCompletenessSUT(t, mockEthClient)

	blockNum := uint64(10)
	blockHash := blockHashOf(blockNum)
	wrong, _ := generateEvent(uint32(blockNum))
	wrong.BlockNumber = blockNum + 1

	mockEthClient.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(blockNum)).
		Return(omissionHeader(blockNum, blockHash), nil).Once()
	mockEthClient.EXPECT().FilterLogs(mock.Anything, arbitrationQueryFor(blockHash)).
		Return([]types.Log{*wrong}, nil).Times(maxArbitrationAttempts)

	res := sut.checkLogsCompleteness(ctx, blockNum, blockNum, blockNum-1, nil)

	require.True(t, res.needsRangeRetry)
	require.Empty(t, res.splicedLogs)
	mockEthClient.AssertExpectations(t)
}

// TestCheckLogsCompleteness_CanceledContext verifies that a canceled context is reported as such.
func TestCheckLogsCompleteness_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mockEthClient := aggkittypesmocks.NewMultiDownloader(t)
	sut := newLogsCompletenessSUT(t, mockEthClient)

	res := sut.checkLogsCompleteness(ctx, 10, 12, 9, nil)

	require.True(t, res.canceled)
	require.False(t, res.needsRangeRetry)
	require.Empty(t, res.splicedLogs)
}

// TestMergeSplicedLogs_OrderAndStability verifies that spliced blocks land between their neighbours,
// range-sourced logs keep their relative order, and spliced logs keep their Index order.
func TestMergeSplicedLogs_OrderAndStability(t *testing.T) {
	mk := func(block uint64, index uint) types.Log { return types.Log{BlockNumber: block, Index: index} }
	// Block 12 is deliberately not Index-ordered to prove range-sourced logs are never reordered.
	rangeLogs := []types.Log{mk(10, 0), mk(12, 5), mk(12, 2)}
	spliced := []types.Log{mk(11, 0), mk(11, 1)}

	merged := mergeSplicedLogs(rangeLogs, spliced)

	require.Equal(t, []types.Log{mk(10, 0), mk(11, 0), mk(11, 1), mk(12, 5), mk(12, 2)}, merged)
	// Inputs are left untouched.
	require.Equal(t, []types.Log{mk(10, 0), mk(12, 5), mk(12, 2)}, rangeLogs)
}
