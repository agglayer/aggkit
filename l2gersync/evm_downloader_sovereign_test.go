package l2gersync

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/l1infotreesync"
	l2gersyncmocks "github.com/agglayer/aggkit/l2gersync/mocks"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestDownloaderSovereign_Download(t *testing.T) {
	t.Parallel()

	fromBlock := uint64(100)
	syncBlockChunkSize := uint64(10)
	latestBlock := uint64(120)
	l2GERAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	mockL2Client := aggkittypesmocks.NewBaseEthereumClienter(t)
	mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
	mockL1InfoTreeSync := l2gersyncmocks.NewL1InfoTreeQuerier(t)
	rh := &sync.RetryHandler{
		MaxRetryAttemptsAfterError: 5,
		RetryAfterErrorPeriod:      time.Millisecond,
	}

	testGER := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	testHashChainValue := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	testL1InfoTreeIndex := uint32(42)
	parentHash := common.HexToHash("0xabc123")
	testBlockHeader := &aggkittypes.BlockHeader{
		Number:     fromBlock,
		ParentHash: &parentHash,
		Hash:       common.HexToHash("0xdef456"),
		Time:       uint64(time.Now().Unix()),
	}
	testBlockHash := testBlockHeader.Hash
	testLogs := []ethtypes.Log{
		{
			Address:     l2GERAddr,
			Topics:      []common.Hash{insertGEREventSignature, testGER, testHashChainValue},
			Data:        []byte{},
			BlockNumber: fromBlock,
			TxHash:      common.HexToHash("0x111"),
			TxIndex:     0,
			BlockHash:   testBlockHash,
			Index:       0,
		},
	}

	mockL2Client.EXPECT().ChainID(mock.Anything).Return(big.NewInt(1), nil).Maybe()
	// First call to get latest block header (with nil)
	mockL2Client.EXPECT().CustomHeaderByNumber(mock.Anything, (*aggkittypes.BlockNumberFinality)(nil)).Return(&aggkittypes.BlockHeader{
		Number: latestBlock,
	}, nil).Maybe()
	mockL2Client.EXPECT().CustomHeaderByNumber(mock.Anything, &aggkittypes.LatestBlock).Return(&aggkittypes.BlockHeader{
		Number: latestBlock,
	}, nil).Maybe()
	// Second call to get the offset block header (with latestBlock since offset is 0)
	mockL2Client.EXPECT().CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(latestBlock)).Return(&aggkittypes.BlockHeader{
		Number: latestBlock,
	}, nil).Maybe()
	mockL2Client.EXPECT().CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(fromBlock)).Return(testBlockHeader, nil).Maybe()

	mockL1InfoTreeSync.EXPECT().GetInfoByGlobalExitRoot(testGER).Return(&l1infotreesync.L1InfoTreeLeaf{
		L1InfoTreeIndex:   testL1InfoTreeIndex,
		GlobalExitRoot:    testGER,
		Timestamp:         uint64(time.Now().Unix()),
		PreviousBlockHash: common.Hash{},
		BlockNumber:       fromBlock,
		BlockPosition:     0,
		MainnetExitRoot:   common.Hash{},
		RollupExitRoot:    common.Hash{},
		Hash:              common.Hash{},
	}, nil).Maybe()
	mockL2Client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(testLogs, nil).Maybe()

	downloader, err := newDownloaderSovereign(
		mockL2Client,
		l2GERAddr,
		mockL1InfoTreeSync,
		mockL1Client,
		common.HexToAddress("0x0000000000000000000000000000000000000001"), // l1GERAddr
		rh,
		aggkittypes.LatestBlock,
		time.Millisecond*10, // waitForNewBlocksPeriod
		syncBlockChunkSize,
	)
	require.NoError(t, err)
	downloadedCh := make(chan sync.EVMBlock, 10)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	downloader.Download(ctx, fromBlock, downloadedCh, nil, false)

	// Collect blocks sent through the channel
	for block := range downloadedCh {
		require.Equal(t, fromBlock, block.Num, "Block number should match")

		// Verify event content
		require.Len(t, block.Events, 1, "Should have exactly one event")
		event, ok := block.Events[0].(*Event)
		require.True(t, ok, "Event should be of type *Event")
		require.NotNil(t, event.GERInfo, "Event should have GERInfo")
		require.Equal(t, testGER, event.GERInfo.GlobalExitRoot, "GER should match test data")
		require.Equal(t, testL1InfoTreeIndex, event.GERInfo.L1InfoTreeIndex, "L1InfoTreeIndex should match")
		require.Equal(t, GEREventTypeInsert, event.EventType, "Should be insert event type")
		t.Logf("✅ Successfully verified block with processed GER event!")
	}

	mockL2Client.AssertExpectations(t)
	mockL1InfoTreeSync.AssertExpectations(t)
}

func TestDownloaderSovereign_GetInfoByGlobalExitRootErrorHandlingInAppender(t *testing.T) {
	t.Parallel()

	fromBlock := uint64(100)
	syncBlockChunkSize := uint64(10)
	latestBlock := uint64(120)
	l2GERAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	testGER := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	testHashChainValue := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	testBlockHeader := &ethtypes.Header{
		Number:      big.NewInt(int64(fromBlock)),
		ParentHash:  common.HexToHash("0xabc123"),
		Root:        common.HexToHash("0xdef456"),
		TxHash:      common.HexToHash("0x789abc"),
		ReceiptHash: common.HexToHash("0x101112"),
		Time:        uint64(time.Now().Unix()),
		GasLimit:    8000000,
		GasUsed:     21000,
	}
	testBlockHash := testBlockHeader.Hash()
	testLogs := []ethtypes.Log{
		{
			Address:     l2GERAddr,
			Topics:      []common.Hash{insertGEREventSignature, testGER, testHashChainValue},
			Data:        []byte{},
			BlockNumber: fromBlock,
			TxHash:      common.HexToHash("0x111"),
			TxIndex:     0,
			BlockHash:   testBlockHash,
			Index:       0,
		},
	}

	tests := []struct {
		name                string
		getInfoByGERError   error
		l1ContractTimestamp *big.Int
		l1ContractError     error
		// removalLogFound simulates the (S-log) UpdateRemovalHashChainValue scan finding (or not
		// finding) a matching removal event for the GER. It is a necessary (but not sufficient)
		// condition for the appender to skip the stale insert: see isGERRemovedFromL2's AND of
		// S-log and S-map (evm_downloader_sovereign.go).
		removalLogFound      bool
		l2ContractTimestamp  *big.Int
		l2ContractError      error
		expectError          bool
		expectedErrorMessage string
	}{
		{
			// With no removal event on L2 (S-log empty), the appender stays blocked.
			name:                 "no_removal_event_stays_blocked",
			getInfoByGERError:    fmt.Errorf("GER lookup failed"),
			expectError:          true,
			expectedErrorMessage: "failed to fetch l1 info tree for global exit root",
		},
		{
			// The L1 contract read is informational only (does not gate the skip decision); with
			// no removal event on L2, the appender stays blocked even though the GER "exists" on
			// L1.
			name:                 "l1_contract_ger_exists_no_removal_event_stays_blocked",
			getInfoByGERError:    fmt.Errorf("GER lookup failed"),
			l1ContractTimestamp:  big.NewInt(1234567890), // timestamp > 0 means GER exists
			expectError:          true,
			expectedErrorMessage: "failed to fetch l1 info tree for global exit root",
		},
		{
			// Both S-log (removal event found) and S-map (L2 map reads 0) agree: removed. The
			// appender skips the stale insert.
			name:                 "removal_event_and_zero_l2_map_unsticks",
			getInfoByGERError:    fmt.Errorf("GER lookup failed"),
			l1ContractTimestamp:  big.NewInt(0), // timestamp = 0 means GER not found (informational)
			removalLogFound:      true,
			l2ContractTimestamp:  big.NewInt(0), // timestamp = 0 means GER removed from L2 (S-map)
			expectError:          false,         // Should return nil when GER is removed from L2
			expectedErrorMessage: "",
		},
		{
			// S-log finds a removal event, but S-map still reads a non-zero L2 timestamp (e.g. the
			// GER was re-injected after removal): the AND fails, so the appender stays blocked.
			name:                 "removal_event_but_nonzero_l2_map_stays_blocked",
			getInfoByGERError:    fmt.Errorf("GER lookup failed"),
			l1ContractTimestamp:  big.NewInt(0), // timestamp = 0 means GER not found (informational)
			removalLogFound:      true,
			l2ContractTimestamp:  big.NewInt(1234567890), // timestamp > 0 means GER exists on L2
			expectError:          true,
			expectedErrorMessage: "failed to fetch l1 info tree for global exit root",
		},
		{
			// S2 §3 reorg scenario: the insert block reorgs out with NO actual
			// removeGlobalExitRoots event ever emitted (S-log stays empty, since none was ever
			// emitted for this GER). A bare reorg-out must NOT falsely unstick the syncer:
			// isGERRemovedFromL2 short-circuits on the empty S-log scan and never even calls
			// S-map (GlobalExitRootMap) -- so it does not matter what S-map would have reported,
			// the appender must keep returning the blocking error. l2ContractTimestamp is
			// deliberately left unset: the harness only mocks CallContract on the L2 client when
			// removalLogFound is true, so if isGERRemovedFromL2 ever called the L2 map here, the
			// unmocked call would panic and fail this test.
			name:                 "reorg_insert_block_reorged_out_no_removal_event_never_unsticks",
			getInfoByGERError:    fmt.Errorf("GER lookup failed"),
			l1ContractTimestamp:  big.NewInt(0),
			removalLogFound:      false,
			expectError:          true,
			expectedErrorMessage: "failed to fetch l1 info tree for global exit root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockL2Client := aggkittypesmocks.NewBaseEthereumClienter(t)
			mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
			mockL1InfoTreeSync := l2gersyncmocks.NewL1InfoTreeQuerier(t)
			rh := &sync.RetryHandler{
				MaxRetryAttemptsAfterError: 5,
				RetryAfterErrorPeriod:      time.Millisecond,
			}

			// Set up mock expectations
			mockL2Client.EXPECT().ChainID(mock.Anything).Return(big.NewInt(1), nil).Maybe()
			// First call to get latest block header (with nil)
			mockL2Client.EXPECT().HeaderByNumber(mock.Anything, (*big.Int)(nil)).Return(&ethtypes.Header{
				Number: big.NewInt(int64(latestBlock)),
			}, nil).Maybe()
			// Second call to get the offset block header (with latestBlock since offset is 0)
			mockL2Client.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(int64(latestBlock))).Return(&ethtypes.Header{
				Number: big.NewInt(int64(latestBlock)),
			}, nil).Maybe()
			mockL2Client.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(int64(fromBlock))).Return(testBlockHeader, nil).Maybe()

			mockL1InfoTreeSync.EXPECT().GetInfoByGlobalExitRoot(testGER).Return(nil, tt.getInfoByGERError).Maybe()

			// The L1 contract read is now unconditional (informational only, does not gate the
			// skip decision), so it must always be mocked.
			l1CallResult := make([]byte, 32)
			if tt.l1ContractTimestamp != nil {
				tt.l1ContractTimestamp.FillBytes(l1CallResult)
			}
			// Even on error, return a valid byte array so contract binding can decode it
			mockL1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
				Return(l1CallResult, tt.l1ContractError).Maybe()

			// (S-log) the removal-event scan (FilterUpdateRemovalHashChainValue) always runs; return
			// a matching removal log only for the rows that simulate a found removal event.
			removalLogs := []ethtypes.Log{}
			if tt.removalLogFound {
				removalLogs = []ethtypes.Log{
					{
						Address:     l2GERAddr,
						Topics:      []common.Hash{removeGEREventSignature, testGER, testHashChainValue},
						Data:        []byte{},
						BlockNumber: fromBlock,
						TxHash:      common.HexToHash("0x222"),
						TxIndex:     0,
						BlockHash:   testBlockHash,
						Index:       1,
					},
				}
			}
			mockL2Client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(removalLogs, nil).Maybe()

			// (S-map) the L2 GlobalExitRootMap read is only reached once S-log has found a removal
			// event (isGERRemovedFromL2 short-circuits otherwise), so only mock it for those rows.
			if tt.removalLogFound {
				var l2CallResult []byte
				if tt.l2ContractTimestamp != nil {
					l2CallResult = make([]byte, 32)
					tt.l2ContractTimestamp.FillBytes(l2CallResult)
				}
				mockL2Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
					Return(l2CallResult, tt.l2ContractError).Maybe()
			}

			downloader, err := newDownloaderSovereign(
				mockL2Client,
				l2GERAddr,
				mockL1InfoTreeSync,
				mockL1Client,
				common.HexToAddress("0x0000000000000000000000000000000000000001"), // l1GERAddr
				rh,
				aggkittypes.LatestBlock,
				time.Millisecond*10,
				syncBlockChunkSize,
			)
			require.NoError(t, err)

			// Test the appender function directly to cover the error paths
			appender := downloader.buildAppender(downloader.l2GERManager)
			insertAppender := appender[insertGEREventSignature]

			block := &sync.EVMBlock{
				EVMBlockHeader: sync.EVMBlockHeader{
					Num: fromBlock,
				},
				Events: []any{},
			}

			// This should trigger the error path (or return nil in some cases)
			err = insertAppender(block, testLogs[0])
			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedErrorMessage)
			} else {
				require.NoError(t, err, "Expected no error when GER is removed from L2")
			}
			// Whether blocked (error) or genuinely skipped (nil), the stale insert must never be
			// recorded as an event (plan S4 intent 2: the insert event is skipped, not just the error
			// suppressed).
			require.Empty(t, block.Events, "insert event must never be appended for an unresolved GER")

			mockL2Client.AssertExpectations(t)
			mockL1Client.AssertExpectations(t)
			mockL1InfoTreeSync.AssertExpectations(t)
		})
	}
}

// TestDownloaderSovereign_IsGERRemovedFromL2_RecoversFromMaxBlockRangeError is a regression test for a
// production incident: isGERRemovedFromL2 scans for the (S-log) removal event from fromBlock (the
// insert block, which can be arbitrarily far behind the head) to "latest" (bind.FilterOpts.End == nil).
// Some RPC providers cap eth_getLogs to a maximum block range and reject that open-ended query with e.g.
// "query exceeds max block range 100000" once fromBlock is more than that many blocks behind the head.
// Before the fix, this error was just logged and treated as "not removed" forever, so the recovery path
// could never unstick a stale insert once the chain had advanced past the provider's range cap. The fix
// (scanRemovedGERs) detects that specific error, resolves the current head, and retries chunked -
// mirroring L2EVMGERReader.GetRemovedGERsForRange.
func TestDownloaderSovereign_IsGERRemovedFromL2_RecoversFromMaxBlockRangeError(t *testing.T) {
	t.Parallel()

	fromBlock := uint64(5)
	latestBlock := uint64(250)
	l2GERAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	testGER := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	testHashChainValue := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")

	mockL2Client := aggkittypesmocks.NewBaseEthereumClienter(t)
	mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
	mockL1InfoTreeSync := l2gersyncmocks.NewL1InfoTreeQuerier(t)
	rh := &sync.RetryHandler{
		MaxRetryAttemptsAfterError: 5,
		RetryAfterErrorPeriod:      time.Millisecond,
	}

	// 1st attempt: the open-ended (fromBlock -> latest) scan is rejected by the provider.
	mockL2Client.EXPECT().FilterLogs(mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("query exceeds max block range 100")).Once()

	// The current head is resolved so the scan can be retried with an explicit, chunkable toBlock.
	mockL2Client.EXPECT().CustomHeaderByNumber(mock.Anything, &aggkittypes.LatestBlock).
		Return(&aggkittypes.BlockHeader{Number: latestBlock}, nil).Once()

	// Chunk 1 [5,104]: no removal event.
	mockL2Client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil).Once()
	// Chunk 2 [105,204]: the removal event lives here, proving results across chunks are combined
	// rather than only the first or last chunk being considered.
	removalLog := ethtypes.Log{
		Address:     l2GERAddr,
		Topics:      []common.Hash{removeGEREventSignature, testGER, testHashChainValue},
		Data:        []byte{},
		BlockNumber: 150,
		TxHash:      common.HexToHash("0x222"),
		TxIndex:     0,
		BlockHash:   common.HexToHash("0xdef456"),
		Index:       1,
	}
	mockL2Client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]ethtypes.Log{removalLog}, nil).Once()
	// Chunk 3 [205,250]: no removal event.
	mockL2Client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil).Once()

	// (S-map) L2 globalExitRootMap reads 0, so the AND of S-log and S-map confirms the GER is removed.
	mockL2Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
		Return(make([]byte, 32), nil).Once()

	downloader, err := newDownloaderSovereign(
		mockL2Client,
		l2GERAddr,
		mockL1InfoTreeSync,
		mockL1Client,
		common.HexToAddress("0x0000000000000000000000000000000000000001"), // l1GERAddr
		rh,
		aggkittypes.LatestBlock,
		time.Millisecond*10,
		uint64(10), // syncBlockChunkSize (unrelated to the removal-scan chunking under test)
	)
	require.NoError(t, err)

	removed := downloader.isGERRemovedFromL2(context.Background(), fromBlock, testGER)
	require.True(t, removed, "GER must be reported removed once the chunked scan finds the removal event")

	mockL2Client.AssertExpectations(t)
	mockL1Client.AssertExpectations(t)
	mockL1InfoTreeSync.AssertExpectations(t)
}

// TestDownloaderSovereign_IsGERRemovedFromL2_CachesLearnedMaxRangeAcrossCalls proves the fix for the
// noisy follow-up to the max-range bug: isGERRemovedFromL2 runs on every appender retry while a GER stays
// unresolved, so without caching the learned range cap, the doomed open-ended scan (and its ERROR log)
// would repeat on every single retry forever, even though the recovery path itself already works. Once
// the cap is learned from a first "query exceeds max block range" error, a second call (any ger/fromBlock)
// must skip straight to the chunked scan: only mocking the head lookup + one chunked FilterLogs call
// (and no error-returning "wide open" call, which is not even stubbed here) proves it never retries the
// doomed unbounded query again.
func TestDownloaderSovereign_IsGERRemovedFromL2_CachesLearnedMaxRangeAcrossCalls(t *testing.T) {
	t.Parallel()

	l2GERAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	firstGER := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	secondGER := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef12345678ab")

	mockL2Client := aggkittypesmocks.NewBaseEthereumClienter(t)
	mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
	mockL1InfoTreeSync := l2gersyncmocks.NewL1InfoTreeQuerier(t)
	rh := &sync.RetryHandler{
		MaxRetryAttemptsAfterError: 5,
		RetryAfterErrorPeriod:      time.Millisecond,
	}

	// --- 1st call: learns the range cap the same way as the recovery test above. ---
	firstLatestBlock := uint64(150)
	mockL2Client.EXPECT().FilterLogs(mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("query exceeds max block range 1000")).Once()
	mockL2Client.EXPECT().CustomHeaderByNumber(mock.Anything, &aggkittypes.LatestBlock).
		Return(&aggkittypes.BlockHeader{Number: firstLatestBlock}, nil).Once()
	mockL2Client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil).Once()

	downloader, err := newDownloaderSovereign(
		mockL2Client,
		l2GERAddr,
		mockL1InfoTreeSync,
		mockL1Client,
		common.HexToAddress("0x0000000000000000000000000000000000000001"), // l1GERAddr
		rh,
		aggkittypes.LatestBlock,
		time.Millisecond*10, // waitForNewBlocksPeriod
		uint64(10),          // syncBlockChunkSize (unrelated to the removal-scan chunking under test)
	)
	require.NoError(t, err)

	removed := downloader.isGERRemovedFromL2(context.Background(), uint64(5), firstGER)
	require.False(t, removed, "no removal event found on the (single, cap-fitting) chunk")
	require.Equal(t, uint64(1000), downloader.removalScanMaxRange, "the learned cap must be cached")

	// --- 2nd call (different GER/block): must go straight to the chunked path. Only the head lookup and
	// one chunked FilterLogs call are stubbed; if the code repeated the unbounded call first, testify
	// would panic on an unexpected FilterLogs invocation instead of matching one of these. ---
	secondLatestBlock := uint64(300)
	mockL2Client.EXPECT().CustomHeaderByNumber(mock.Anything, &aggkittypes.LatestBlock).
		Return(&aggkittypes.BlockHeader{Number: secondLatestBlock}, nil).Once()
	mockL2Client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil).Once()

	removed = downloader.isGERRemovedFromL2(context.Background(), uint64(20), secondGER)
	require.False(t, removed, "no removal event found on the (single, cap-fitting) chunk")

	mockL2Client.AssertExpectations(t)
	mockL1Client.AssertExpectations(t)
	mockL1InfoTreeSync.AssertExpectations(t)
}
