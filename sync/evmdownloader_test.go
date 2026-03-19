package sync

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	contractAddr   = common.HexToAddress("f00")
	eventSignature = crypto.Keccak256Hash([]byte("foo"))
)

const (
	syncBlockChunck = uint64(10)
)

type testEvent common.Hash

func TestFinality(t *testing.T) {
	d, _ := NewTestDownloader(t, time.Millisecond*100)
	require.Equal(t, aggkittypes.LatestBlock, d.Finality())
}

func TestGetEventsByBlockRange(t *testing.T) {
	type testCase struct {
		description        string
		inputLogs          []types.Log
		fromBlock, toBlock uint64
		expectedBlocks     EVMBlocks
		setupMocks         func(*aggkittypesmocks.MultiDownloader)
		contextCancelled   bool
	}
	testCases := []testCase{}
	ctx := context.Background()
	d, clientMock := NewTestDownloader(t, time.Millisecond*100)

	// case 0: single block, no events
	case0 := testCase{
		description:    "case 0: single block, no events",
		inputLogs:      []types.Log{},
		fromBlock:      1,
		toBlock:        3,
		expectedBlocks: EVMBlocks{},
	}
	testCases = append(testCases, case0)

	// case 1: single block, single event
	logC1, updateC1 := generateEvent(3)
	logsC1 := []types.Log{
		*logC1,
	}
	blocksC1 := EVMBlocks{
		{
			EVMBlockHeader: EVMBlockHeader{
				Num:        logC1.BlockNumber,
				Hash:       logC1.BlockHash,
				ParentHash: common.HexToHash("foo"),
			},
			Events: []interface{}{updateC1},
		},
	}
	case1 := testCase{
		description:    "case 1: single block, single event",
		inputLogs:      logsC1,
		fromBlock:      3,
		toBlock:        3,
		expectedBlocks: blocksC1,
	}
	testCases = append(testCases, case1)

	// case 2: single block, multiple events
	logC2_1, updateC2_1 := generateEvent(5)
	logC2_2, updateC2_2 := generateEvent(5)
	logC2_3, updateC2_3 := generateEvent(5)
	logC2_4, updateC2_4 := generateEvent(5)
	logsC2 := []types.Log{
		*logC2_1,
		*logC2_2,
		*logC2_3,
		*logC2_4,
	}
	blocksC2 := []*EVMBlock{
		{
			EVMBlockHeader: EVMBlockHeader{
				Num:        logC2_1.BlockNumber,
				Hash:       logC2_1.BlockHash,
				ParentHash: common.HexToHash("foo"),
			},
			Events: []interface{}{
				updateC2_1,
				updateC2_2,
				updateC2_3,
				updateC2_4,
			},
		},
	}
	case2 := testCase{
		description:    "case 2: single block, multiple events",
		inputLogs:      logsC2,
		fromBlock:      5,
		toBlock:        5,
		expectedBlocks: blocksC2,
	}
	testCases = append(testCases, case2)

	// case 3: multiple blocks, some events
	logC3_1, updateC3_1 := generateEvent(7)
	logC3_2, updateC3_2 := generateEvent(7)
	logC3_3, updateC3_3 := generateEvent(8)
	logC3_4, updateC3_4 := generateEvent(8)
	logsC3 := []types.Log{
		*logC3_1,
		*logC3_2,
		*logC3_3,
		*logC3_4,
	}
	blocksC3 := EVMBlocks{
		{
			EVMBlockHeader: EVMBlockHeader{
				Num:        logC3_1.BlockNumber,
				Hash:       logC3_1.BlockHash,
				ParentHash: common.HexToHash("foo"),
			},
			Events: []interface{}{
				updateC3_1,
				updateC3_2,
			},
		},
		{
			EVMBlockHeader: EVMBlockHeader{
				Num:        logC3_3.BlockNumber,
				Hash:       logC3_3.BlockHash,
				ParentHash: common.HexToHash("foo"),
			},
			Events: []interface{}{
				updateC3_3,
				updateC3_4,
			},
		},
	}
	case3 := testCase{
		description:    "case 3: multiple blocks, some events",
		inputLogs:      logsC3,
		fromBlock:      7,
		toBlock:        8,
		expectedBlocks: blocksC3,
	}
	testCases = append(testCases, case3)

	// case 4: context cancelled
	case4 := testCase{
		description:      "case 4: context cancelled",
		inputLogs:        []types.Log{},
		fromBlock:        1,
		toBlock:          3,
		expectedBlocks:   nil,
		contextCancelled: true,
	}
	testCases = append(testCases, case4)

	// case 5: block hash mismatch with retry success
	logC5, updateC5 := generateEvent(10)
	logsC5 := []types.Log{*logC5}
	blocksC5 := EVMBlocks{
		{
			EVMBlockHeader: EVMBlockHeader{
				Num:        logC5.BlockNumber,
				Hash:       logC5.BlockHash,
				ParentHash: common.HexToHash("foo"),
			},
			Events: []interface{}{updateC5},
		},
	}
	case5 := testCase{
		description:    "case 5: block hash mismatch with retry success",
		inputLogs:      logsC5,
		fromBlock:      10,
		toBlock:        10,
		expectedBlocks: blocksC5,
		setupMocks: func(clientMock *aggkittypesmocks.MultiDownloader) {
			// First call returns different hash (mismatch)
			parentHash := common.HexToHash("foo")
			header := types.Header{
				Number:     big.NewInt(int64(10)),
				ParentHash: common.HexToHash("foo"),
			}
			blockHash := header.Hash()
			clientMock.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(10)).
				Return(&aggkittypes.BlockHeader{
					Number:     10,
					Hash:       blockHash,
					ParentHash: &parentHash,
				}, nil).Once()
			// Second call returns correct hash
			clientMock.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(10)).
				Return(&aggkittypes.BlockHeader{
					Number:     10,
					Hash:       blockHash,
					ParentHash: &parentHash,
				}, nil).Once()
		},
	}
	testCases = append(testCases, case5)

	// case 6: block hash mismatch with max retries exceeded
	logC6, _ := generateEvent(15)
	logsC6 := []types.Log{*logC6}
	case6 := testCase{
		description:    "case 6: block hash mismatch with max retries exceeded",
		inputLogs:      logsC6,
		fromBlock:      15,
		toBlock:        15,
		expectedBlocks: nil,
		setupMocks: func(clientMock *aggkittypesmocks.MultiDownloader) {
			// Return a different hash than the log's block hash for all retry attempts
			// This will trigger the retry logic and eventually exceed max retries
			for i := 0; i < MaxRetryCountBlockHashMismatch+1; i++ {
				parentHash := common.HexToHash("bar") // Different parent hash to create different block hash
				clientMock.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(15)).
					Return(&aggkittypes.BlockHeader{
						Number:     15,
						ParentHash: &parentHash, // Different parent hash to create different block hash
						// The hash will be different from logC6.BlockHash, causing mismatch
					}, nil).Once()
			}
		},
	}
	testCases = append(testCases, case6)

	// case 7: logs with removed flag should be filtered out
	logC7_1, _ := generateEvent(20)
	logC7_2, updateC7_2 := generateEvent(20)
	logC7_1.Removed = true // This log should be filtered out
	logsC7 := []types.Log{*logC7_1, *logC7_2}
	blocksC7 := EVMBlocks{
		{
			EVMBlockHeader: EVMBlockHeader{
				Num:        logC7_2.BlockNumber,
				Hash:       logC7_2.BlockHash,
				ParentHash: common.HexToHash("foo"),
			},
			Events: []interface{}{updateC7_2}, // Only the non-removed log
		},
	}
	case7 := testCase{
		description:    "case 7: logs with removed flag should be filtered out",
		inputLogs:      logsC7,
		fromBlock:      20,
		toBlock:        20,
		expectedBlocks: blocksC7,
	}
	testCases = append(testCases, case7)

	// case 8: logs with non-matching topics should be filtered out
	logC8_1, updateC8_1 := generateEvent(25)
	logC8_2 := &types.Log{
		Address:     contractAddr,
		BlockNumber: 25,
		Topics: []common.Hash{
			common.HexToHash("0x1234567890abcdef"), // Non-matching topic
			common.HexToHash("0xabcdef1234567890"),
		},
		BlockHash: logC8_1.BlockHash,
		Data:      nil,
	}
	logsC8 := []types.Log{*logC8_1, *logC8_2}
	blocksC8 := EVMBlocks{
		{
			EVMBlockHeader: EVMBlockHeader{
				Num:        logC8_1.BlockNumber,
				Hash:       logC8_1.BlockHash,
				ParentHash: common.HexToHash("foo"),
			},
			Events: []interface{}{updateC8_1}, // Only the matching topic log
		},
	}
	case8 := testCase{
		description:    "case 8: logs with non-matching topics should be filtered out",
		inputLogs:      logsC8,
		fromBlock:      25,
		toBlock:        25,
		expectedBlocks: blocksC8,
	}
	testCases = append(testCases, case8)

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test_case_%d_%s", i, tc.description), func(t *testing.T) {
			// Reset mock for each test case
			clientMock.ExpectedCalls = nil

			query := ethereum.FilterQuery{
				FromBlock: new(big.Int).SetUint64(tc.fromBlock),
				Addresses: []common.Address{contractAddr},
				ToBlock:   new(big.Int).SetUint64(tc.toBlock),
			}

			if tc.contextCancelled {
				// Create a cancelled context
				cancelledCtx, cancel := context.WithCancel(context.Background())
				cancel()
				clientMock.EXPECT().FilterLogs(cancelledCtx, query).Return(tc.inputLogs, nil)
			} else {
				clientMock.EXPECT().FilterLogs(mock.Anything, query).Return(tc.inputLogs, nil)
			}

			// Setup custom mocks if provided
			if tc.setupMocks != nil {
				tc.setupMocks(clientMock)
			} else {
				// Default mock setup for block headers
				for _, b := range tc.expectedBlocks {
					parentHash := common.HexToHash("foo")
					header := types.Header{
						Number:     big.NewInt(int64(b.Num)),
						ParentHash: common.HexToHash("foo"),
					}
					blockHash := header.Hash()
					clientMock.EXPECT().HeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(b.Num)).
						Return(&aggkittypes.BlockHeader{
							Number:     b.Num,
							Hash:       blockHash,
							ParentHash: &parentHash,
						}, nil)
				}
			}

			var actualBlocks EVMBlocks
			if tc.contextCancelled {
				cancelledCtx, cancel := context.WithCancel(context.Background())
				cancel()
				actualBlocks = d.GetEventsByBlockRange(cancelledCtx, tc.fromBlock, tc.toBlock)
			} else {
				actualBlocks = d.GetEventsByBlockRange(ctx, tc.fromBlock, tc.toBlock)
			}

			require.Equal(t, tc.expectedBlocks, actualBlocks, tc.description)
		})
	}
}

func generateEvent(blockNum uint32) (*types.Log, testEvent) {
	h := common.HexToHash(strconv.Itoa(int(blockNum)))
	header := types.Header{
		Number:     big.NewInt(int64(blockNum)),
		ParentHash: common.HexToHash("foo"),
	}
	blockHash := header.Hash()
	log := &types.Log{
		Address:     contractAddr,
		BlockNumber: uint64(blockNum),
		Topics: []common.Hash{
			eventSignature,
			h,
		},
		BlockHash: blockHash,
		Data:      nil,
	}
	return log, testEvent(h)
}

func TestWaitForNewBlocks(t *testing.T) {
	ctx := context.Background()
	d, clientMock := NewTestDownloader(t, time.Millisecond*100)

	// at first attempt
	currentBlock := uint64(5)
	expectedBlock := uint64(6)
	aggkittypesBlockHeader := aggkittypes.NewBlockHeader(6, common.Hash{}, 0, nil)
	clientMock.EXPECT().HeaderByNumber(ctx, mock.Anything).Return(aggkittypesBlockHeader, nil).Once()
	actualBlock := d.WaitForNewBlocks(ctx, currentBlock)
	assert.Equal(t, expectedBlock, actualBlock)

	// 2 iterations
	clientMock.EXPECT().HeaderByNumber(ctx, mock.Anything).Return(aggkittypes.NewBlockHeader(5, common.Hash{}, 0, nil), nil).Once()
	clientMock.EXPECT().HeaderByNumber(ctx, mock.Anything).Return(aggkittypes.NewBlockHeader(6, common.Hash{}, 0, nil), nil).Once()

	actualBlock = d.WaitForNewBlocks(ctx, currentBlock)
	assert.Equal(t, expectedBlock, actualBlock)

	// after error from client
	clientMock.EXPECT().HeaderByNumber(ctx, mock.Anything).Return(nil, errors.New("foo")).Once()
	clientMock.EXPECT().HeaderByNumber(ctx, mock.Anything).Return(aggkittypes.NewBlockHeader(6, common.Hash{}, 0, nil), nil).Once()
	actualBlock = d.WaitForNewBlocks(ctx, currentBlock)
	assert.Equal(t, expectedBlock, actualBlock)
}

func TestWaitForNewBlocksWithReorgDetection(t *testing.T) {
	ctx := context.Background()

	t.Run("reorg detected - different hash", func(t *testing.T) {
		rh := &RetryHandler{
			MaxRetryAttemptsAfterError: 5,
			RetryAfterErrorPeriod:      time.Millisecond,
		}
		clientMock := aggkittypesmocks.NewMultiDownloader(t)
		reorgDetectorMock := NewReorgDetectorMock(t)

		d, err := NewEVMDownloader("test",
			clientMock, syncBlockChunck, aggkittypes.LatestBlock, time.Millisecond,
			buildAppender(), []common.Address{contractAddr}, rh,
			aggkittypes.FinalizedBlock,
			reorgDetectorMock, "test-reorg-detector-id",
		)
		require.NoError(t, err)

		latestSyncedBlock := uint64(5)
		currentBlockNumber := uint64(4)

		latestHeader := &types.Header{Number: big.NewInt(int64(currentBlockNumber))}

		headerHash := latestHeader.Hash()
		trackedBlock := &reorgdetector.Header{Hash: common.HexToHash("0x456")}

		clientMock.EXPECT().HeaderByNumber(ctx, mock.Anything).Return(
			aggkittypes.NewBlockHeaderFromEthHeader(latestHeader), nil).Once()
		reorgDetectorMock.EXPECT().GetTrackedBlockByBlockNumber("test-reorg-detector-id", currentBlockNumber).Return(trackedBlock, nil).Once()
		reorgDetectorMock.EXPECT().AddBlockToTrack(ctx, "test-reorg-detector-id", currentBlockNumber, headerHash).Return(nil).Once()

		actualBlock := d.WaitForNewBlocks(ctx, latestSyncedBlock)
		assert.Equal(t, uint64(4), actualBlock)

		reorgDetectorMock.AssertExpectations(t)
		clientMock.AssertExpectations(t)
	})

	t.Run("reorg detector error", func(t *testing.T) {
		rh := &RetryHandler{
			MaxRetryAttemptsAfterError: 5,
			RetryAfterErrorPeriod:      time.Millisecond,
		}
		clientMock := aggkittypesmocks.NewMultiDownloader(t)
		reorgDetectorMock := NewReorgDetectorMock(t)

		d, err := NewEVMDownloader("test",
			clientMock, syncBlockChunck, aggkittypes.LatestBlock, time.Millisecond,
			buildAppender(), []common.Address{contractAddr}, rh,
			aggkittypes.FinalizedBlock,
			reorgDetectorMock, "test-reorg-detector-id",
		)
		require.NoError(t, err)

		latestSyncedBlock := uint64(5)
		currentBlockNumber := uint64(5)

		latestHeader := &types.Header{Number: big.NewInt(int64(currentBlockNumber))}
		latestHeaderNext := &types.Header{Number: big.NewInt(int64(currentBlockNumber + 1))}

		clientMock.EXPECT().HeaderByNumber(ctx, mock.Anything).Return(
			aggkittypes.NewBlockHeaderFromEthHeader(latestHeader), nil).Once()
		reorgDetectorMock.EXPECT().GetTrackedBlockByBlockNumber("test-reorg-detector-id", currentBlockNumber).Return(nil, errors.New("database error")).Once()
		clientMock.EXPECT().HeaderByNumber(ctx, mock.Anything).Return(aggkittypes.NewBlockHeaderFromEthHeader(latestHeaderNext), nil).Once()
		headerHashNext := latestHeaderNext.Hash()
		reorgDetectorMock.EXPECT().AddBlockToTrack(ctx, "test-reorg-detector-id", currentBlockNumber+1, headerHashNext).Return(nil).Once()

		actualBlock := d.WaitForNewBlocks(ctx, latestSyncedBlock)
		assert.Equal(t, uint64(6), actualBlock)

		reorgDetectorMock.AssertExpectations(t)
		clientMock.AssertExpectations(t)
	})
}

func TestGetBlockHeader(t *testing.T) {
	ctx := context.Background()
	d, clientMock := NewTestDownloader(t, time.Millisecond)

	blockNum := uint64(5)
	blockNumBig := aggkittypes.NewBlockNumber(blockNum)
	parentHash := common.HexToHash("0x4343")
	returnedBlockEth := &aggkittypes.BlockHeader{
		Number:     blockNum,
		Hash:       common.HexToHash("0xabc"),
		ParentHash: &parentHash,
	}
	returnedBlock := &aggkittypes.BlockHeader{
		Number:     blockNum,
		Hash:       returnedBlockEth.Hash,
		ParentHash: returnedBlockEth.ParentHash,
	}
	expectedBlock := EVMBlockHeader{
		Num:        5,
		Hash:       returnedBlockEth.Hash,
		ParentHash: *returnedBlockEth.ParentHash,
	}

	// at first attempt
	clientMock.EXPECT().HeaderByNumber(ctx, blockNumBig).Return(returnedBlock, nil).Once()
	actualBlock, isCanceled := d.GetBlockHeader(ctx, blockNum)
	assert.Equal(t, expectedBlock, actualBlock)
	assert.False(t, isCanceled)

	// after error from client
	clientMock.EXPECT().HeaderByNumber(ctx, blockNumBig).Return(nil, errors.New("foo")).Once()
	clientMock.EXPECT().HeaderByNumber(ctx, blockNumBig).Return(returnedBlock, nil).Once()
	actualBlock, isCanceled = d.GetBlockHeader(ctx, blockNum)
	assert.Equal(t, expectedBlock, actualBlock)
	assert.False(t, isCanceled)

	// header not found default
	clientMock.EXPECT().HeaderByNumber(ctx, blockNumBig).Return(nil, ethereum.NotFound).Once()
	clientMock.EXPECT().HeaderByNumber(ctx, blockNumBig).Return(returnedBlock, nil).Once()
	actualBlock, isCanceled = d.GetBlockHeader(ctx, 5)
	assert.Equal(t, expectedBlock, actualBlock)
	assert.False(t, isCanceled)

	// header not found default TO
	d, clientMock = NewTestDownloader(t, 0)
	clientMock.EXPECT().HeaderByNumber(ctx, blockNumBig).Return(nil, ethereum.NotFound).Once()
	clientMock.EXPECT().HeaderByNumber(ctx, blockNumBig).Return(returnedBlock, nil).Once()
	actualBlock, isCanceled = d.GetBlockHeader(ctx, 5)
	assert.Equal(t, expectedBlock, actualBlock)
	assert.False(t, isCanceled)
}

func TestFilterQueryToString(t *testing.T) {
	addr1 := common.HexToAddress("0xf000")
	addr2 := common.HexToAddress("0xabcd")
	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(1000),
		Addresses: []common.Address{addr1, addr2},
		ToBlock:   new(big.Int).SetUint64(1100),
	}

	assert.Equal(t, "FromBlock: 1000, ToBlock: 1100, Addresses: [0x000000000000000000000000000000000000f000 0x000000000000000000000000000000000000ABcD], Topics: []", filterQueryToString(query))

	query = ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(1000),
		Addresses: []common.Address{addr1, addr2},
		ToBlock:   new(big.Int).SetUint64(1100),
		Topics:    [][]common.Hash{{common.HexToHash("0x1234"), common.HexToHash("0x5678")}},
	}
	assert.Equal(t, "FromBlock: 1000, ToBlock: 1100, Addresses: [0x000000000000000000000000000000000000f000 0x000000000000000000000000000000000000ABcD], Topics: [[0x0000000000000000000000000000000000000000000000000000000000001234 0x0000000000000000000000000000000000000000000000000000000000005678]]", filterQueryToString(query))
}

func TestGetLogs(t *testing.T) {
	t.Run("timeout scenario", func(t *testing.T) {
		mockEthClient := aggkittypesmocks.NewMultiDownloader(t)
		sut := EVMDownloaderImplementation{
			ethClient:        mockEthClient,
			addressesToQuery: []common.Address{contractAddr},
			log:              log.WithFields("test", "EVMDownloaderImplementation"),
			rh: &RetryHandler{
				RetryAfterErrorPeriod:      time.Millisecond,
				MaxRetryAttemptsAfterError: 5,
			},
		}
		ctx := context.TODO()
		// First call times out
		mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("network error %w", context.DeadlineExceeded)).After(10 * time.Millisecond).Once()
		// Second call succeeds after retry
		mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(nil, nil).Once()
		logs := sut.GetLogs(ctx, 0, 1)
		require.Equal(t, []types.Log{}, logs)
	})

	t.Run("success scenario", func(t *testing.T) {
		mockEthClient := aggkittypesmocks.NewMultiDownloader(t)
		sut := EVMDownloaderImplementation{
			ethClient:        mockEthClient,
			addressesToQuery: []common.Address{contractAddr},
			log:              log.WithFields("test", "EVMDownloaderImplementation"),
			rh: &RetryHandler{
				RetryAfterErrorPeriod:      time.Millisecond,
				MaxRetryAttemptsAfterError: 5,
			},
		}
		ctx := context.TODO()
		// Call succeeds immediately
		mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(nil, nil).Once()
		logs := sut.GetLogs(ctx, 0, 1)
		require.Equal(t, []types.Log{}, logs)
	})
}

func TestDownloadBeforeFinalized(t *testing.T) {
	steps := []evmTestStep{
		{finalizedBlock: 33, fromBlock: 1, toBlock: 11, waitForNewBlocks: true, waitForNewBlocksRequest: 0, waitForNewBlockReply: 35, getBlockHeader: &EVMBlockHeader{Num: 11}},
		{finalizedBlock: 33, fromBlock: 12, toBlock: 22, eventsReponse: EVMBlocks{createEVMBlock(t, 14, true)}, getBlockHeader: &EVMBlockHeader{Num: 22}},
		// It returns the last block of range, so it don't need to create a empty one
		{finalizedBlock: 33, fromBlock: 23, toBlock: 33, eventsReponse: EVMBlocks{createEVMBlock(t, 33, true)}},
		// It reach the top of chain (block 35)
		{finalizedBlock: 33, fromBlock: 34, toBlock: 35},
		// Previous iteration we reach top of chain so we need update the latest block
		{finalizedBlock: 33, fromBlock: 34, toBlock: 54, waitForNewBlocks: true, waitForNewBlocksRequest: 35, waitForNewBlockReply: 60},
		// finalized block is 35, so we can reduce emit an emptyBlock and reduce the range
		{finalizedBlock: 35, fromBlock: 34, toBlock: 60, getBlockHeader: &EVMBlockHeader{Num: 35}},
		{finalizedBlock: 35, fromBlock: 36, toBlock: 46},
		{finalizedBlock: 35, fromBlock: 36, toBlock: 56, eventsReponse: EVMBlocks{createEVMBlock(t, 36, false)}},
		// Block 36 is the new last block,so it reduce the range again to [37-47]
		{finalizedBlock: 35, fromBlock: 37, toBlock: 47},
		{finalizedBlock: 57, fromBlock: 37, toBlock: 57, eventsReponse: EVMBlocks{createEVMBlock(t, 57, false)}},
		{finalizedBlock: 61, fromBlock: 58, toBlock: 60, eventsReponse: EVMBlocks{createEVMBlock(t, 60, false)}},
		{finalizedBlock: 61, fromBlock: 61, toBlock: 61, waitForNewBlocks: true, waitForNewBlocksRequest: 60, waitForNewBlockReply: 61, getBlockHeader: &EVMBlockHeader{Num: 61}},
		{finalizedBlock: 61, fromBlock: 62, toBlock: 62, waitForNewBlocks: true, waitForNewBlocksRequest: 61, waitForNewBlockReply: 62},
	}
	runSteps(t, 1, steps)
}

func TestCaseAskLastBlockIfFinalitySameAsTargetBlock(t *testing.T) {
	steps := []evmTestStep{
		{finalizedBlock: 105, fromBlock: 99, toBlock: 105, waitForNewBlocks: true, waitForNewBlocksRequest: 0, waitForNewBlockReply: 105, getBlockHeader: &EVMBlockHeader{Num: 105}},
		{finalizedBlock: 110, fromBlock: 106, toBlock: 110, waitForNewBlocks: true, waitForNewBlocksRequest: 105, waitForNewBlockReply: 110, getBlockHeader: &EVMBlockHeader{Num: 110}},
		// Here is the bug:
		// - the range 111-115 returns block: 106. So the code must emit the block 106 and also the block 115 as empty (last block)
		{finalizedBlock: 115, fromBlock: 111, toBlock: 115, waitForNewBlocks: true, waitForNewBlocksRequest: 110, waitForNewBlockReply: 115, eventsReponse: EVMBlocks{createEVMBlock(t, 106, false)}, getBlockHeader: &EVMBlockHeader{Num: 115}},
	}
	runSteps(t, 99, steps)
}

func buildAppender() LogAppenderMap {
	appender := make(LogAppenderMap)
	appender[eventSignature] = func(b *EVMBlock, l types.Log) error {
		b.Events = append(b.Events, testEvent(l.Topics[1]))
		return nil
	}
	return appender
}

func NewTestDownloader(t *testing.T, retryPeriod time.Duration) (*EVMDownloader, *aggkittypesmocks.MultiDownloader) {
	t.Helper()

	rh := &RetryHandler{
		MaxRetryAttemptsAfterError: 5,
		RetryAfterErrorPeriod:      retryPeriod,
	}
	clientMock := aggkittypesmocks.NewMultiDownloader(t)
	d, err := NewEVMDownloader("test",
		clientMock, syncBlockChunck, aggkittypes.LatestBlock, time.Millisecond,
		buildAppender(), []common.Address{contractAddr}, rh,
		aggkittypes.FinalizedBlock,
		nil,                      // reorgDetector - nil for tests
		"test-reorg-detector-id", // reorgDetectorID
	)
	require.NoError(t, err)
	return d, clientMock
}

func createEVMBlock(t *testing.T, num uint64, isSafeBlock bool) *EVMBlock {
	t.Helper()
	return &EVMBlock{
		IsFinalizedBlock: isSafeBlock,
		EVMBlockHeader: EVMBlockHeader{
			Num:        num,
			Hash:       common.HexToHash(fmt.Sprintf("0x%.2X", num)),
			ParentHash: common.HexToHash(fmt.Sprintf("0x%.2X", num-1)),
			Timestamp:  uint64(time.Now().Unix()),
		},
	}
}

type evmTestStep struct {
	finalizedBlock          uint64
	fromBlock, toBlock      uint64
	eventsReponse           EVMBlocks
	waitForNewBlocks        bool
	waitForNewBlocksRequest uint64
	waitForNewBlockReply    uint64
	getBlockHeader          *EVMBlockHeader
}

func runSteps(t *testing.T, fromBlock uint64, steps []evmTestStep) {
	t.Helper()
	mockEthDownloader := NewEVMDownloaderMock(t)

	ctx := context.Background()
	ctx1, cancel := context.WithCancel(ctx)
	defer cancel()

	downloader, _ := NewTestDownloader(t, time.Millisecond)
	downloader.EVMDownloaderInterface = mockEthDownloader

	for i := 0; i < len(steps); i++ {
		log.Info("iteration: ", i, "------------------------------------------------")
		downloadCh := make(chan EVMBlock, 100)
		downloader, _ := NewTestDownloader(t, time.Millisecond)
		downloader.EVMDownloaderInterface = mockEthDownloader
		downloader.setStopDownloaderOnIterationN(i + 1)
		expectedBlocks := EVMBlocks{}
		for _, step := range steps[:i+1] {
			mockEthDownloader.EXPECT().GetLastFinalizedBlock(mock.Anything).Return(step.finalizedBlock, nil).Once()
			if step.waitForNewBlocks {
				mockEthDownloader.EXPECT().WaitForNewBlocks(mock.Anything, step.waitForNewBlocksRequest).Return(step.waitForNewBlockReply).Once()
			}
			mockEthDownloader.EXPECT().GetEventsByBlockRange(mock.Anything, step.fromBlock, step.toBlock).
				Return(step.eventsReponse).Once()
			expectedBlocks = append(expectedBlocks, step.eventsReponse...)
			if step.getBlockHeader != nil {
				log.Infof("iteration:%d : GetBlockHeader(%d) ", i, step.getBlockHeader.Num)
				mockEthDownloader.EXPECT().GetBlockHeader(mock.Anything, step.getBlockHeader.Num).Return(*step.getBlockHeader, false).Once()
				expectedBlocks = append(expectedBlocks, &EVMBlock{
					EVMBlockHeader:   *step.getBlockHeader,
					IsFinalizedBlock: step.getBlockHeader.Num <= step.finalizedBlock,
				})
			}
		}
		downloader.Download(ctx1, fromBlock, downloadCh, nil, false)
		mockEthDownloader.AssertExpectations(t)
		for _, expectedBlock := range expectedBlocks {
			log.Debugf("waiting block %d ", expectedBlock.Num)
			actualBlock := <-downloadCh
			log.Debugf("block %d received!", actualBlock.Num)
			require.Equal(t, *expectedBlock, actualBlock)
		}
	}
}

func TestTooManyResultsErrorHandling(t *testing.T) {
	mockEthClient := aggkittypesmocks.NewMultiDownloader(t)
	sut := EVMDownloaderImplementation{
		ethClient:        mockEthClient,
		addressesToQuery: []common.Address{contractAddr},
		log:              log.WithFields("test", "EVMDownloaderImplementation"),
		rh: &RetryHandler{
			RetryAfterErrorPeriod:      time.Millisecond,
			MaxRetryAttemptsAfterError: 5,
		},
	}

	ctx := context.Background()
	fromBlock := uint64(100)
	toBlock := uint64(200)

	// First call returns "too many results" error
	tooManyResultsErr := errors.New("Query returned more than 20000 results.")
	mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(nil, tooManyResultsErr).Once()

	// Second call for first batch (100-149) succeeds
	firstBatchLogs := []types.Log{
		{
			Address:     contractAddr,
			BlockNumber: 125,
			Topics:      []common.Hash{eventSignature},
			BlockHash:   common.HexToHash("0x123"),
		},
	}
	mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(firstBatchLogs, nil).Once()

	// Third call for second batch (150-199) succeeds
	secondBatchLogs := []types.Log{
		{
			Address:     contractAddr,
			BlockNumber: 175,
			Topics:      []common.Hash{eventSignature},
			BlockHash:   common.HexToHash("0x456"),
		},
	}
	mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(secondBatchLogs, nil).Once()

	// Fourth call for third batch (200-200) succeeds
	thirdBatchLogs := []types.Log{
		{
			Address:     contractAddr,
			BlockNumber: 200,
			Topics:      []common.Hash{eventSignature},
			BlockHash:   common.HexToHash("0x789"),
		},
	}
	mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(thirdBatchLogs, nil).Once()

	result := sut.getUnfilteredLogs(ctx, fromBlock, toBlock)

	// Should combine all batches
	expected := make([]types.Log, 0, len(firstBatchLogs)+len(secondBatchLogs)+len(thirdBatchLogs))
	expected = append(expected, firstBatchLogs...)
	expected = append(expected, secondBatchLogs...)
	expected = append(expected, thirdBatchLogs...)
	assert.Equal(t, expected, result)
}

func TestGetLastFinalizedBlock(t *testing.T) {
	ctx := context.Background()

	t.Run("With finalizedBlockType set", func(t *testing.T) {
		mockClient := aggkittypesmocks.NewMultiDownloader(t)
		finalizedBlockType := aggkittypes.FinalizedBlock
		blockFinality := aggkittypes.LatestBlock

		sut := EVMDownloaderImplementation{
			ethClient:          mockClient,
			finalizedBlockType: &finalizedBlockType,
			blockFinality:      blockFinality,
			log:                log.WithFields("test", "EVMDownloaderImplementation"),
		}

		mockClient.EXPECT().BlockNumber(ctx,
			finalizedBlockType).Return(uint64(100), nil).Once()

		blockNumber, err := sut.GetLastFinalizedBlock(ctx)
		require.NoError(t, err)
		require.Equal(t, uint64(100), blockNumber)
	})

	t.Run("With finalizedBlockType nil - uses blockFinality", func(t *testing.T) {
		mockClient := aggkittypesmocks.NewMultiDownloader(t)
		blockFinality := aggkittypes.LatestBlock

		sut := EVMDownloaderImplementation{
			ethClient:          mockClient,
			finalizedBlockType: nil,
			blockFinality:      blockFinality,
			log:                log.WithFields("test", "EVMDownloaderImplementation"),
		}

		mockClient.EXPECT().BlockNumber(ctx,
			blockFinality).Return(uint64(200), nil).Once()

		blockNumber, err := sut.GetLastFinalizedBlock(ctx)
		require.NoError(t, err)
		require.Equal(t, uint64(200), blockNumber)
	})
}

// newMockDownloader returns an EVMDownloader with a mocked EVMDownloaderInterface injected.
func newMockDownloader(t *testing.T) (*EVMDownloader, *EVMDownloaderMock) {
	t.Helper()
	downloader, _ := NewTestDownloader(t, time.Millisecond)
	iface := NewEVMDownloaderMock(t)
	downloader.EVMDownloaderInterface = iface
	return downloader, iface
}

func blockHeader(num uint64) EVMBlockHeader {
	return EVMBlockHeader{
		Num:  num,
		Hash: common.HexToHash(fmt.Sprintf("0x%x", num)),
	}
}

func drainChannel(ch chan EVMBlock) []EVMBlock {
	var result []EVMBlock
	for b := range ch {
		result = append(result, b)
	}
	return result
}

// TestDownload_LastBlockNum_BlockWithEvents verifies that when lastBlockNum is set and the
// target block has events, the block is reported and Download stops.
func TestDownload_LastBlockNum_BlockWithEvents(t *testing.T) {
	t.Parallel()
	downloader, iface := newMockDownloader(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	lastBlock := uint64(5)
	// Initial WaitForNewBlocks called with 0
	iface.EXPECT().WaitForNewBlocks(mock.Anything, uint64(0)).Return(uint64(10)).Once()
	iface.EXPECT().GetLastFinalizedBlock(mock.Anything).Return(uint64(10), nil).Once()
	eventsBlock := &EVMBlock{
		IsFinalizedBlock: true,
		EVMBlockHeader:   blockHeader(5),
		Events:           []any{testEvent(common.HexToHash("0xAA"))},
	}
	iface.EXPECT().GetEventsByBlockRange(mock.Anything, uint64(5), uint64(5)).Return(EVMBlocks{eventsBlock}).Once()

	downloadCh := make(chan EVMBlock, 10)
	downloader.Download(ctx, 5, downloadCh, &lastBlock, false)

	received := drainChannel(downloadCh)
	require.Len(t, received, 1)
	require.Equal(t, uint64(5), received[0].Num)
	require.Equal(t, eventsBlock.Events, received[0].Events)
	require.True(t, received[0].IsFinalizedBlock)
}

// TestDownload_LastBlockNum_EmptyFinalizedBlock verifies that when lastBlockNum is set and the
// target block is empty in the finalized zone, an empty block is reported and Download stops.
func TestDownload_LastBlockNum_EmptyFinalizedBlock(t *testing.T) {
	t.Parallel()
	downloader, iface := newMockDownloader(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	lastBlock := uint64(5)
	iface.EXPECT().WaitForNewBlocks(mock.Anything, uint64(0)).Return(uint64(10)).Once()
	iface.EXPECT().GetLastFinalizedBlock(mock.Anything).Return(uint64(10), nil).Once()
	iface.EXPECT().GetEventsByBlockRange(mock.Anything, uint64(5), uint64(5)).Return(EVMBlocks{}).Once()
	hdr := blockHeader(5)
	iface.EXPECT().GetBlockHeader(mock.Anything, uint64(5)).Return(hdr, false).Once()

	downloadCh := make(chan EVMBlock, 10)
	downloader.Download(ctx, 5, downloadCh, &lastBlock, false)

	received := drainChannel(downloadCh)
	require.Len(t, received, 1)
	require.Equal(t, uint64(5), received[0].Num)
	require.Empty(t, received[0].Events)
	require.True(t, received[0].IsFinalizedBlock)
}

// TestDownload_IncludeEmptyFirstBlock_FinalizedZone verifies that with includeEmptyFirstBlock=true,
// the initial block is reported via the pre-report path (not doubled) when in the finalized zone.
func TestDownload_IncludeEmptyFirstBlock_FinalizedZone(t *testing.T) {
	t.Parallel()
	downloader, iface := newMockDownloader(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	lastBlock := uint64(5)
	iface.EXPECT().WaitForNewBlocks(mock.Anything, uint64(0)).Return(uint64(10)).Once()
	iface.EXPECT().GetLastFinalizedBlock(mock.Anything).Return(uint64(10), nil).Once()
	iface.EXPECT().GetEventsByBlockRange(mock.Anything, uint64(5), uint64(5)).Return(EVMBlocks{}).Once()
	// GetBlockHeader called exactly once — by the pre-report path, not duplicated by the finalized zone path
	hdr := blockHeader(5)
	iface.EXPECT().GetBlockHeader(mock.Anything, uint64(5)).Return(hdr, false).Once()

	downloadCh := make(chan EVMBlock, 10)
	downloader.Download(ctx, 5, downloadCh, &lastBlock, true)

	received := drainChannel(downloadCh)
	require.Len(t, received, 1)
	require.Equal(t, uint64(5), received[0].Num)
	require.Empty(t, received[0].Events)
	require.True(t, received[0].IsFinalizedBlock)
}

// TestDownload_IncludeEmptyFirstBlock_NotFinalizedZone verifies that with includeEmptyFirstBlock=true,
// the initial block is reported immediately even when it is not yet finalized.
func TestDownload_IncludeEmptyFirstBlock_NotFinalizedZone(t *testing.T) {
	t.Parallel()
	downloader, iface := newMockDownloader(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	lastBlock := uint64(5)
	// finalizedBlock=3 is below fromBlock=5 → not-finalized zone
	iface.EXPECT().WaitForNewBlocks(mock.Anything, uint64(0)).Return(uint64(10)).Once()
	iface.EXPECT().GetLastFinalizedBlock(mock.Anything).Return(uint64(3), nil).Once()
	iface.EXPECT().GetEventsByBlockRange(mock.Anything, uint64(5), uint64(5)).Return(EVMBlocks{}).Once()
	hdr := blockHeader(5)
	iface.EXPECT().GetBlockHeader(mock.Anything, uint64(5)).Return(hdr, false).Once()

	downloadCh := make(chan EVMBlock, 10)
	downloader.Download(ctx, 5, downloadCh, &lastBlock, true)

	received := drainChannel(downloadCh)
	require.Len(t, received, 1)
	require.Equal(t, uint64(5), received[0].Num)
	require.Empty(t, received[0].Events)
	// Block 5 > finalizedBlock 3 → not finalized
	require.False(t, received[0].IsFinalizedBlock)
}

// TestDownload_LastBlockNum_MultipleBlocksInRange verifies that when lastBlockNum > fromBlock,
// all blocks up to lastBlockNum are reported and Download stops after that.
func TestDownload_LastBlockNum_MultipleBlocksInRange(t *testing.T) {
	t.Parallel()
	downloader, iface := newMockDownloader(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	lastBlock := uint64(7)
	iface.EXPECT().WaitForNewBlocks(mock.Anything, uint64(0)).Return(uint64(10)).Once()
	iface.EXPECT().GetLastFinalizedBlock(mock.Anything).Return(uint64(10), nil).Once()
	// Block 6 has events; block 7 is empty → reportEmptyBlock(7) called
	eventsBlock6 := &EVMBlock{
		IsFinalizedBlock: true,
		EVMBlockHeader:   blockHeader(6),
		Events:           []any{testEvent(common.HexToHash("0xBB"))},
	}
	iface.EXPECT().GetEventsByBlockRange(mock.Anything, uint64(5), uint64(7)).Return(EVMBlocks{eventsBlock6}).Once()
	hdr7 := blockHeader(7)
	iface.EXPECT().GetBlockHeader(mock.Anything, uint64(7)).Return(hdr7, false).Once()

	downloadCh := make(chan EVMBlock, 10)
	downloader.Download(ctx, 5, downloadCh, &lastBlock, false)

	received := drainChannel(downloadCh)
	require.Len(t, received, 2)
	require.Equal(t, uint64(6), received[0].Num)
	require.Equal(t, eventsBlock6.Events, received[0].Events)
	require.Equal(t, uint64(7), received[1].Num)
	require.Empty(t, received[1].Events)
	require.True(t, received[1].IsFinalizedBlock)
}
