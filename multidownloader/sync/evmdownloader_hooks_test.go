package multidownloader

import (
	"context"
	"errors"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

var (
	testTopicA = common.HexToHash("0xa")
	testTopicB = common.HexToHash("0xb")
)

func newDownloaderForHooksTest(appender sync.LogAppenderMap) *EVMDownloader {
	return NewEVMDownloader(nil, log.WithFields("module", "test"),
		&sync.RetryHandler{RetryAfterErrorPeriod: time.Millisecond, MaxRetryAttemptsAfterError: -1},
		appender, time.Second, time.Millisecond)
}

// newResponseForHooksTest returns a response with 2 blocks: block 10 has a log of each topic (both from
// the same transaction) and block 11 has a log of topic A
func newResponseForHooksTest() mdrtypes.LogQueryResponse {
	txHash := common.HexToHash("0x1234")
	return mdrtypes.LogQueryResponse{
		Blocks: []mdrtypes.BlockWithLogs{
			{
				Header: aggkittypes.BlockHeader{Number: 10, Hash: common.HexToHash("0x10")},
				Logs: []mdrtypes.Log{
					{BlockNumber: 10, Index: 0, TxHash: txHash, Topics: []common.Hash{testTopicA}},
					{BlockNumber: 10, Index: 1, TxHash: txHash, Topics: []common.Hash{testTopicB}},
				},
			},
			{
				Header: aggkittypes.BlockHeader{Number: 11, Hash: common.HexToHash("0x11")},
				Logs: []mdrtypes.Log{
					{BlockNumber: 11, Index: 0, Topics: []common.Hash{testTopicA}},
				},
			},
		},
		ResponseRange: aggkitcommon.NewBlockRange(10, 11),
	}
}

func appenderOfTopic(event string) func(b *sync.EVMBlock, l types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		b.Events = append(b.Events, event)
		return nil
	}
}

func TestLogQueryResponseToEVMBlocks_LogsHook(t *testing.T) {
	downloader := newDownloaderForHooksTest(sync.LogAppenderMap{
		testTopicA: appenderOfTopic("A"),
		testTopicB: appenderOfTopic("B"),
	})
	var hookFrom, hookTo uint64
	// The hook drops the logs of topic B
	downloader.SetLogsHook(func(_ context.Context, fromBlock, toBlock uint64, logs []types.Log) []types.Log {
		hookFrom, hookTo = fromBlock, toBlock
		filtered := make([]types.Log, 0, len(logs))
		for _, l := range logs {
			if l.Topics[0] != testTopicB {
				filtered = append(filtered, l)
			}
		}
		return filtered
	})

	blocks, err := downloader.logQueryResponseToEVMBlocks(t.Context(), newResponseForHooksTest())
	require.NoError(t, err)
	require.Equal(t, uint64(10), hookFrom)
	require.Equal(t, uint64(11), hookTo)
	require.Len(t, blocks, 2)
	require.Equal(t, []any{"A"}, blocks[0].Events)
	require.Equal(t, []any{"A"}, blocks[1].Events)
}

func TestLogQueryResponseToEVMBlocks_LogsHookReturnsUnknownBlock(t *testing.T) {
	downloader := newDownloaderForHooksTest(sync.LogAppenderMap{testTopicA: appenderOfTopic("A")})
	downloader.SetLogsHook(func(_ context.Context, _, _ uint64, logs []types.Log) []types.Log {
		return append(logs, types.Log{BlockNumber: 99, Topics: []common.Hash{testTopicA}})
	})

	_, err := downloader.logQueryResponseToEVMBlocks(t.Context(), newResponseForHooksTest())
	require.ErrorContains(t, err, "not part of the response")
}

func TestLogQueryResponseToEVMBlocks_LogsEnricher(t *testing.T) {
	t.Run("it receives the logs after the hook and before the appenders", func(t *testing.T) {
		enriched := false
		downloader := newDownloaderForHooksTest(sync.LogAppenderMap{
			testTopicA: func(b *sync.EVMBlock, l types.Log) error {
				require.True(t, enriched, "the enricher must run before the appenders")
				b.Events = append(b.Events, "A")
				return nil
			},
		})
		downloader.SetLogsHook(func(_ context.Context, _, _ uint64, logs []types.Log) []types.Log {
			return logs[:1]
		})
		downloader.SetLogsEnricher(func(_ context.Context, logs []types.Log) error {
			require.Len(t, logs, 1)
			require.Equal(t, common.HexToHash("0x10"), logs[0].BlockHash)
			enriched = true
			return nil
		})

		blocks, err := downloader.logQueryResponseToEVMBlocks(t.Context(), newResponseForHooksTest())
		require.NoError(t, err)
		require.Len(t, blocks, 2)
		require.Equal(t, []any{"A"}, blocks[0].Events)
		require.Empty(t, blocks[1].Events)
	})

	t.Run("its error fails the conversion", func(t *testing.T) {
		downloader := newDownloaderForHooksTest(sync.LogAppenderMap{testTopicA: appenderOfTopic("A")})
		downloader.SetLogsEnricher(func(context.Context, []types.Log) error {
			return aggkittypes.ErrTransactionInclusionMismatch
		})

		_, err := downloader.logQueryResponseToEVMBlocks(t.Context(), newResponseForHooksTest())
		require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
	})
}

func TestAppendLog_ContextualAppender(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(t.Context(), ctxKey{}, "download-ctx")
	block := &sync.EVMBlock{}
	ethLog := types.Log{BlockNumber: 10, Topics: []common.Hash{testTopicA}}

	t.Run("it takes precedence and receives the download context", func(t *testing.T) {
		downloader := newDownloaderForHooksTest(sync.LogAppenderMap{
			testTopicA: func(*sync.EVMBlock, types.Log) error {
				return errors.New("the plain appender must not be called")
			},
		})
		downloader.SetContextualLogAppenders(sync.ContextualLogAppenderMap{
			testTopicA: func(ctx context.Context, b *sync.EVMBlock, _ types.Log) error {
				require.Equal(t, "download-ctx", ctx.Value(ctxKey{}))
				b.Events = append(b.Events, "contextual")
				return nil
			},
		})

		require.NoError(t, downloader.appendLog(ctx, block, ethLog))
		require.Equal(t, []any{"contextual"}, block.Events)
	})

	t.Run("a stale inclusion is not retried", func(t *testing.T) {
		calls := 0
		downloader := newDownloaderForHooksTest(nil)
		downloader.SetContextualLogAppenders(sync.ContextualLogAppenderMap{
			testTopicA: func(context.Context, *sync.EVMBlock, types.Log) error {
				calls++
				return aggkittypes.ErrTransactionInclusionMismatch
			},
		})

		err := downloader.appendLog(ctx, block, ethLog)
		require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
		require.Equal(t, 1, calls)
	})

	t.Run("a canceled context stops the retries", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		downloader := newDownloaderForHooksTest(nil)
		downloader.SetContextualLogAppenders(sync.ContextualLogAppenderMap{
			testTopicA: func(context.Context, *sync.EVMBlock, types.Log) error {
				cancel()
				return errors.New("some error")
			},
		})

		require.ErrorIs(t, downloader.appendLog(cancelCtx, block, ethLog), context.Canceled)
	})
}

func TestAppendLog_Ignored(t *testing.T) {
	block := &sync.EVMBlock{}
	downloader := newDownloaderForHooksTest(sync.LogAppenderMap{testTopicA: appenderOfTopic("A")})

	// A log without topics
	require.NoError(t, downloader.appendLog(t.Context(), block, types.Log{}))
	// A log of a topic without appender
	require.NoError(t, downloader.appendLog(t.Context(), block, types.Log{Topics: []common.Hash{testTopicB}}))
	require.Empty(t, block.Events)
}
