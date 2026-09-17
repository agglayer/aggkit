package claimsync

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonzkevmbridge"
	aggkitcommon "github.com/agglayer/aggkit/common"
	logger "github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	tree "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// capturingLogger wraps a real logger and additionally records every Warnf call verbatim, so tests
// can assert on the operator-facing WARN emitted when the RPC's max-range limit forces a chunk-size
// shrink, without depending on log output formatting/capture.
type capturingLogger struct {
	aggkitcommon.Logger
	warnings []string
}

// Warnf records the formatted message and forwards it to the wrapped logger.
func (l *capturingLogger) Warnf(format string, args ...interface{}) {
	l.warnings = append(l.warnings, fmt.Sprintf(format, args...))
	l.Logger.Warnf(format, args...)
}

// newTestClaimSyncForRPC builds the minimal ClaimSync needed by GetLatestBlockNumByGlobalIndexFromRPC.
// When toBlock is nil, the function now defaults to LatestBlock, which requires resolving a block
// header from the RPC; stub that call here (as a no-op default) so tests that don't care about the
// resolved "latest" value (i.e. pass an explicit toBlock, or don't reach that far) are unaffected.
func newTestClaimSyncForRPC(t *testing.T, ethClient aggkittypes.EthClienter) *ClaimSync {
	t.Helper()
	bridgeAddr := common.HexToAddress("0xBridge")
	if mockClient, ok := ethClient.(*mocks.EthClienter); ok {
		mockClient.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
			Return(&aggkittypes.BlockHeader{Number: 1000}, nil).Maybe()
	}
	return &ClaimSync{
		ethClient: ethClient,
		cfg: ConfigStandalone{
			ConfigEmbedded: ConfigEmbedded{BridgeAddr: bridgeAddr},
			BlockFinality:  *aggkittypes.NewBlockNumber(1000),
		},
		logger: logger.WithFields("module", "test"),
	}
}

// buildPreEtrogClaimEventLog packs a valid pre-Etrog ClaimEvent log.
func buildPreEtrogClaimEventLog(t *testing.T, index uint32, blockNum uint64) types.Log {
	t.Helper()
	legacyABI, err := polygonzkevmbridge.PolygonzkevmbridgeMetaData.GetAbi()
	require.NoError(t, err)
	event, err := legacyABI.EventByID(claimEventSignaturePreEtrog)
	require.NoError(t, err)
	data, err := event.Inputs.Pack(index, uint32(1), common.Address{}, common.Address{}, big.NewInt(10))
	require.NoError(t, err)
	return types.Log{
		Topics:      []common.Hash{claimEventSignaturePreEtrog},
		Data:        data,
		BlockNumber: blockNum,
	}
}

// buildDetailedClaimEventLog packs a valid DetailedClaimEvent log with globalIndex as an indexed topic.
func buildDetailedClaimEventLog(t *testing.T, globalIndex *big.Int, blockNum uint64) types.Log {
	t.Helper()
	l2ABI, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	require.NoError(t, err)
	event, err := l2ABI.EventByID(detailedClaimEventSignature)
	require.NoError(t, err)

	var nonIndexed abi.Arguments
	for _, inp := range event.Inputs {
		if !inp.Indexed {
			nonIndexed = append(nonIndexed, inp)
		}
	}
	data, err := nonIndexed.Pack(
		[tree.DefaultHeight][common.HashLength]byte{},
		[tree.DefaultHeight][common.HashLength]byte{},
		[common.HashLength]byte{},
		[common.HashLength]byte{},
		uint8(0),
		uint32(1),
		common.Address{},
		uint32(0),
		big.NewInt(100),
		[]byte{},
	)
	require.NoError(t, err)
	return types.Log{
		Topics: []common.Hash{
			detailedClaimEventSignature,
			common.BigToHash(globalIndex), // globalIndex is an indexed topic
			common.BytesToHash(common.Address{}.Bytes()),
		},
		Data:        data,
		BlockNumber: blockNum,
	}
}

// expectedFilterQuery builds the FilterQuery that GetLatestBlockNumByGlobalIndexFromRPC uses.
func expectedFilterQuery(bridgeAddr common.Address, from, to uint64) ethereum.FilterQuery {
	return ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(to),
		Addresses: []common.Address{bridgeAddr},
		Topics: [][]common.Hash{{
			claimEventSignaturePreEtrog,
			claimEventSignature,
			detailedClaimEventSignature,
		}},
	}
}

// --- Tests ---

func TestGetLatestBlockNumByGlobalIndexFromRPC_FilterLogsError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	ethClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(nil, errors.New("rpc unavailable"))

	_, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(1), nil)
	require.False(t, found)
	require.ErrorContains(t, err, "rpc unavailable")
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_NoMatchingLog(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	// Return a log for a different globalIndex
	otherLog := buildClaimEventLog(t, big.NewInt(99), common.HexToHash("0x1"), 50)
	ethClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]types.Log{otherLog}, nil)

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(1), nil)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, uint64(0), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_MatchesClaimEventEtrog(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	globalIndex := big.NewInt(42)
	log := buildClaimEventLog(t, globalIndex, common.HexToHash("0xabc"), 100)
	ethClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]types.Log{log}, nil)

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(100), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_MatchesDetailedClaimEvent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	globalIndex := big.NewInt(7)
	log := buildDetailedClaimEventLog(t, globalIndex, 200)
	ethClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]types.Log{log}, nil)

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(200), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_MatchesPreEtrogClaimEvent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	index := uint32(5)
	log := buildPreEtrogClaimEventLog(t, index, 300)
	ethClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]types.Log{log}, nil)

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(int64(index)), nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(300), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_ReturnsLatestOfMultipleLogs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	globalIndex := big.NewInt(10)
	// Two matching logs at different blocks; FilterLogs returns them in ascending order.
	// The function iterates in reverse so it should return the last (block 500).
	log1 := buildClaimEventLog(t, globalIndex, common.HexToHash("0x1"), 100)
	log2 := buildClaimEventLog(t, globalIndex, common.HexToHash("0x2"), 500)
	ethClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]types.Log{log1, log2}, nil)

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(500), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_ChunkedScan_Found(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)
	bridgeAddr := c.cfg.BridgeAddr

	globalIndex := big.NewInt(3)
	matchingLog := buildClaimEventLog(t, globalIndex, common.HexToHash("0xdef"), 800)

	// First call: full range [0, 1000] fails with a max-range error → chunkSize = 500
	// Chunked scan goes backwards: [501, 1000] then [1, 500] (if needed)
	// The log is at block 800, so it's found in the first chunk [501, 1000].
	maxRangeErr := errors.New("block range too large, max range: 500")
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 0, 1000)).
		Return(nil, maxRangeErr).Once()
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 501, 1000)).
		Return([]types.Log{matchingLog}, nil).Once()

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(800), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_ChunkedScan_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)
	bridgeAddr := c.cfg.BridgeAddr

	// Full range [0, 1000] fails with max-range error → chunkSize = 500
	// Two chunks cover the full range; neither has a match.
	maxRangeErr := errors.New("block range too large, max range: 500")
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 0, 1000)).
		Return(nil, maxRangeErr).Once()
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 501, 1000)).
		Return([]types.Log{}, nil).Once()
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 1, 500)).
		Return([]types.Log{}, nil).Once()
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 0, 0)).
		Return([]types.Log{}, nil).Once()

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(99), nil)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, uint64(0), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_ChunkedScan_ChunkError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)
	bridgeAddr := c.cfg.BridgeAddr

	maxRangeErr := errors.New("block range too large, max range: 500")
	chunkErr := errors.New("network timeout")
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 0, 1000)).
		Return(nil, maxRangeErr).Once()
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 501, 1000)).
		Return(nil, chunkErr).Once()

	_, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(1), nil)
	require.False(t, found)
	require.ErrorContains(t, err, "network timeout")
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_ExplicitToBlock(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	globalIndex := big.NewInt(1)
	toBlock := aggkittypes.NewBlockNumber(200)
	log := buildClaimEventLog(t, globalIndex, common.HexToHash("0x9"), 150)
	ethClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]types.Log{log}, nil)

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, toBlock)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(150), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_LogWithNoTopics_Skipped(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	globalIndex := big.NewInt(1)
	noTopicLog := types.Log{BlockNumber: 50} // no topics
	matchingLog := buildClaimEventLog(t, globalIndex, common.HexToHash("0x1"), 100)
	ethClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]types.Log{noTopicLog, matchingLog}, nil)

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(100), blockNum)
}

// TestGetLatestBlockNumByGlobalIndexFromRPC_NilToBlockDefaultsToLatestBlock verifies that a nil
// toBlock now resolves against LatestBlock rather than cfg.BlockFinality. cfg.BlockFinality here is
// a constant (1000), which would never trigger a CustomHeaderByNumber call and would scan [0, 1000];
// LatestBlock is resolved dynamically via CustomHeaderByNumber, so scanning [0, 555] proves the
// default changed.
func TestGetLatestBlockNumByGlobalIndexFromRPC_NilToBlockDefaultsToLatestBlock(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	bridgeAddr := common.HexToAddress("0xBridge")
	c := &ClaimSync{
		ethClient: ethClient,
		cfg: ConfigStandalone{
			ConfigEmbedded: ConfigEmbedded{BridgeAddr: bridgeAddr},
			BlockFinality:  *aggkittypes.NewBlockNumber(1000),
		},
		logger: logger.WithFields("module", "test"),
	}

	ethClient.EXPECT().
		CustomHeaderByNumber(mock.Anything, mock.MatchedBy(func(b *aggkittypes.BlockNumberFinality) bool {
			return b != nil && b.BlockName() == aggkittypes.Latest
		})).
		Return(&aggkittypes.BlockHeader{Number: 555}, nil).Once()
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 0, 555)).
		Return([]types.Log{}, nil).Once()

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(1), nil)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, uint64(0), blockNum)
}

// TestGetLatestBlockNumByGlobalIndexFromRPC_ChunkedByDefault_Found verifies that when
// SyncBlockChunkSize is configured, the scan starts chunked immediately (no full-range probe call)
// and returns the first match found in the most recent chunk.
func TestGetLatestBlockNumByGlobalIndexFromRPC_ChunkedByDefault_Found(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)
	c.cfg.SyncBlockChunkSize = 300
	bridgeAddr := c.cfg.BridgeAddr

	globalIndex := big.NewInt(5)
	matchingLog := buildClaimEventLog(t, globalIndex, common.HexToHash("0x55"), 900)

	// toBlock defaults (via the helper's CustomHeaderByNumber stub) to latest=1000; with
	// SyncBlockChunkSize=300 the very first FilterLogs call is already chunked to [701, 1000] --
	// no full-range [0, 1000] probe call is made.
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 701, 1000)).
		Return([]types.Log{matchingLog}, nil).Once()

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(900), blockNum)
}

// TestGetLatestBlockNumByGlobalIndexFromRPC_ChunkedByDefault_NotFound verifies that a configured
// SyncBlockChunkSize walks all chunks backwards to block 0 and correctly reports not-found (no
// error) when no chunk contains a match.
func TestGetLatestBlockNumByGlobalIndexFromRPC_ChunkedByDefault_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)
	c.cfg.SyncBlockChunkSize = 300
	bridgeAddr := c.cfg.BridgeAddr

	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 701, 1000)).
		Return([]types.Log{}, nil).Once()
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 401, 700)).
		Return([]types.Log{}, nil).Once()
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 101, 400)).
		Return([]types.Log{}, nil).Once()
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 0, 100)).
		Return([]types.Log{}, nil).Once()

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(42), nil)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, uint64(0), blockNum)
}

// TestGetLatestBlockNumByGlobalIndexFromRPC_ConfiguredChunkTooLarge_ShrinksAndSucceeds verifies that
// even when SyncBlockChunkSize is configured, a max-range error on a chunk still shrinks the chunk
// size (using the value from the error) and retries the same window before continuing, and that a
// WARN naming the globalIndex, the failing range, and both the old and new chunk sizes is emitted so
// an operator can tell from the logs alone that SyncBlockChunkSize should be lowered.
func TestGetLatestBlockNumByGlobalIndexFromRPC_ConfiguredChunkTooLarge_ShrinksAndSucceeds(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)
	c.cfg.SyncBlockChunkSize = 600
	capturedLogger := &capturingLogger{Logger: c.logger}
	c.logger = capturedLogger
	bridgeAddr := c.cfg.BridgeAddr

	globalIndex := big.NewInt(3)
	matchingLog := buildClaimEventLog(t, globalIndex, common.HexToHash("0x77"), 900)
	maxRangeErr := errors.New("block range too large, max range: 200")

	// First chunk at the configured size (600) fails as too large for this RPC; the scan shrinks
	// to 200 (from the error) and retries the *same* window end (current=1000), not the next one.
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 401, 1000)).
		Return(nil, maxRangeErr).Once()
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 801, 1000)).
		Return([]types.Log{matchingLog}, nil).Once()

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(900), blockNum)

	require.Len(t, capturedLogger.warnings, 1)
	warning := capturedLogger.warnings[0]
	require.Contains(t, warning, "[401, 1000]")
	require.Contains(t, warning, globalIndex.String())
	require.Contains(t, warning, "600") // old (too large) chunk size
	require.Contains(t, warning, "200") // new (shrunk) chunk size
	require.Contains(t, warning, "SyncBlockChunkSize")
}

// TestGetLatestBlockNumByGlobalIndexFromRPC_TooManyResults_ShrinksHeuristicallyAndSucceeds verifies
// that a "too many results" style error (no explicit reported cap) also shrinks the chunk -- by
// half, via the shared aggkitcommon.NextEthGetLogsWindow -- and not just an explicit max-range error.
func TestGetLatestBlockNumByGlobalIndexFromRPC_TooManyResults_ShrinksHeuristicallyAndSucceeds(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)
	c.cfg.SyncBlockChunkSize = 1000
	bridgeAddr := c.cfg.BridgeAddr

	globalIndex := big.NewInt(3)
	matchingLog := buildClaimEventLog(t, globalIndex, common.HexToHash("0x77"), 900)
	tooManyResultsErr := errors.New("Query returned more than 20000 results.")

	// First chunk [1, 1000] (chunk size 1000) fails with "too many results" (no explicit cap) ->
	// chunk halves to 500 and retries the same window end (current=1000).
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 1, 1000)).
		Return(nil, tooManyResultsErr).Once()
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 501, 1000)).
		Return([]types.Log{matchingLog}, nil).Once()

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(900), blockNum)
}

// TestGetLatestBlockNumByGlobalIndexFromRPC_LoopTerminatesAtMinimumChunkSize verifies that repeated
// "too many results" errors shrink the chunk size down to 1 and then abort with an error instead of
// retrying forever, once no smaller window can be computed.
func TestGetLatestBlockNumByGlobalIndexFromRPC_LoopTerminatesAtMinimumChunkSize(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)
	c.cfg.SyncBlockChunkSize = 4

	tooManyResultsErr := errors.New("Query returned more than 20000 results.")
	// chunkSize starts at 4 and halves on every failing attempt: 4 -> 2 -> 1, after which
	// NextEthGetLogsWindow reports not-ok (currentWindow <= 1) and the scan must abort.
	ethClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(nil, tooManyResultsErr)

	_, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(1), nil)
	require.False(t, found)
	require.ErrorContains(t, err, "Query returned more than 20000 results")
}

// TestGetLatestBlockNumByGlobalIndexFromRPC_UnsetChunkSize_UsesDefaultAndNeverFullRange verifies that
// when SyncBlockChunkSize is unset (0), the scan uses defaultRPCScanBlockChunkSize (10000) and never
// issues a single call spanning [0, toBlockNum] -- this reproduces the real production shape (a chain
// at block ~3,009,445 with no SyncBlockChunkSize configured) at a smaller scale. The mock only expects
// the three chunked calls below; mockery fails the test if any other call (e.g. a [0, toBlockNum]
// full-range probe) is made.
func TestGetLatestBlockNumByGlobalIndexFromRPC_UnsetChunkSize_UsesDefaultAndNeverFullRange(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)
	require.Zero(t, c.cfg.SyncBlockChunkSize, "precondition: SyncBlockChunkSize must be unset")
	bridgeAddr := c.cfg.BridgeAddr

	toBlock := aggkittypes.NewBlockNumber(25000)

	// defaultRPCScanBlockChunkSize (10000) walks backwards from 25000 in three chunks:
	// [15001, 25000], [5001, 15000], [0, 5000]. None of them spans the full [0, 25000] range.
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 15001, 25000)).
		Return([]types.Log{}, nil).Once()
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 5001, 15000)).
		Return([]types.Log{}, nil).Once()
	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 0, 5000)).
		Return([]types.Log{}, nil).Once()

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(1), toBlock)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, uint64(0), blockNum)
}

// TestGetLatestBlockNumByGlobalIndexFromRPC_HungRPC_RespectsTimeout verifies that each FilterLogs
// call is made with its own per-call deadline (sync.DefaultFilterLogsTimeout, currently 2 minutes),
// so a hung/slow RPC is eventually unblocked instead of blocking the caller forever, even when the
// caller's own context carries no deadline at all.
//
// This deliberately does NOT drive the mock to actually hang until that deadline fires: 2 minutes
// is too slow for a unit test, and previously this test used a short *outer* context deadline (20ms)
// and asserted on that timing out -- which passes via plain ctx propagation regardless of whether
// the per-call timeout inside scanRange exists at all, so it never actually exercised the fix (see
// the RED-phase evidence in the review notes/PR description). Instead, the outer context here is
// t.Context() (no deadline of its own), and the mock captures and returns immediately without
// blocking; the assertion is on the deadline observed on the context FilterLogs was actually called
// with, which can only have come from a per-call context.WithTimeout(ctx, ...) inside production
// code, since the caller-supplied ctx has none.
func TestGetLatestBlockNumByGlobalIndexFromRPC_HungRPC_RespectsTimeout(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	var (
		observedDeadline time.Time
		deadlineSet      bool
	)
	beforeCall := time.Now()
	ethClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).
		RunAndReturn(func(callCtx context.Context, _ ethereum.FilterQuery) ([]types.Log, error) {
			observedDeadline, deadlineSet = callCtx.Deadline()
			return nil, nil
		})
	_, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(1), nil)
	afterCall := time.Now()

	require.NoError(t, err)
	require.False(t, found)
	require.True(t, deadlineSet,
		"FilterLogs must be called with a context carrying a deadline (the per-call timeout), "+
			"even though the caller's own context has none")
	require.False(t, observedDeadline.Before(beforeCall.Add(sync.DefaultFilterLogsTimeout)),
		"observed deadline is earlier than beforeCall+DefaultFilterLogsTimeout: %s", observedDeadline)
	require.False(t, observedDeadline.After(afterCall.Add(sync.DefaultFilterLogsTimeout)),
		"observed deadline is later than afterCall+DefaultFilterLogsTimeout: %s", observedDeadline)
}

// TestGetLatestBlockNumByGlobalIndexFromRPC_OverallScanDeadline_ExceededDuringManyChunks verifies
// that the *overall* scan deadline (maxRPCScanDuration in production, passed here as a short
// scanDeadline via the unexported entry point so the test doesn't need to wait on the real 5-minute
// constant) cuts a scan short and returns an error -- not a plain "not found" -- when the requested
// range would need far more chunks than can be scanned within that deadline.
//
// chunkSize is configured to 1, so covering toBlock (100,000,000) backwards to 0 would need on the
// order of 100 million chunks; each mocked FilterLogs call sleeps 5ms, so only a handful of chunks
// can complete inside the 20ms scanDeadline, proving the scan is cut short long before reaching
// block 0.
func TestGetLatestBlockNumByGlobalIndexFromRPC_OverallScanDeadline_ExceededDuringManyChunks(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)
	c.cfg.SyncBlockChunkSize = 1
	toBlock := aggkittypes.NewBlockNumber(100_000_000)

	ethClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).
		RunAndReturn(func(callCtx context.Context, _ ethereum.FilterQuery) ([]types.Log, error) {
			time.Sleep(5 * time.Millisecond)
			return nil, nil
		})

	_, found, err := c.getLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(1), toBlock, 20*time.Millisecond)
	require.False(t, found)
	require.Error(t, err)
	require.ErrorContains(t, err, "overall RPC scan deadline")
	require.ErrorContains(t, err, "20ms")
	require.ErrorContains(t, err, "100000000") // requested range is reported for diagnosability
}

// TestGetLatestBlockNumByGlobalIndexFromRPC_OverallScanDeadline_NormalScanUnaffected verifies that a
// scan which comfortably completes within the overall scan deadline still returns its correct
// result -- i.e. the new overall-deadline bookkeeping does not interfere with an ordinary,
// fast-completing multi-chunk scan.
func TestGetLatestBlockNumByGlobalIndexFromRPC_OverallScanDeadline_NormalScanUnaffected(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)
	c.cfg.SyncBlockChunkSize = 300
	bridgeAddr := c.cfg.BridgeAddr

	globalIndex := big.NewInt(5)
	matchingLog := buildClaimEventLog(t, globalIndex, common.HexToHash("0x55"), 900)

	ethClient.EXPECT().FilterLogs(mock.Anything, expectedFilterQuery(bridgeAddr, 701, 1000)).
		Return([]types.Log{matchingLog}, nil).Once()

	// A generous 1s scanDeadline that a handful of immediately-returning mock calls will never
	// come close to exhausting.
	blockNum, found, err := c.getLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, nil, time.Second)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(900), blockNum)
}
