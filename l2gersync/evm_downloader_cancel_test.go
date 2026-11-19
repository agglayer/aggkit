package l2gersync

import (
	"context"
	"math/big"
	"path"
	"testing"
	"time"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/l1infotreesync"
	l2gersyncmocks "github.com/agglayer/aggkit/l2gersync/mocks"
	"github.com/agglayer/aggkit/sync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	// blockedSendSettleTime gives the downloader time to reach the blocked send after the
	// last mocked call before the context is canceled.
	blockedSendSettleTime = 200 * time.Millisecond
	// downloaderExitTimeout bounds how long the test waits for Download to return.
	downloaderExitTimeout = 5 * time.Second
)

// requireDownloadExitsOnCancel cancels ctx once the downloader is blocked sending to a channel
// nobody reads (as when the driver cancels a generation on a reorg) and checks that Download
// returns and closes the channel instead of blocking the generation join forever.
func requireDownloadExitsOnCancel(t *testing.T, reachedSend <-chan struct{}, cancel context.CancelFunc,
	downloadDone <-chan struct{}, downloadedCh <-chan sync.EVMBlock) {
	t.Helper()

	select {
	case <-reachedSend:
	case <-time.After(downloaderExitTimeout):
		t.Fatal("the downloader never reached the block send")
	}
	time.Sleep(blockedSendSettleTime)
	cancel()

	select {
	case <-downloadDone:
	case <-time.After(downloaderExitTimeout):
		t.Fatal("Download did not return after cancellation while blocked sending a block")
	}
	_, ok := <-downloadedCh
	require.False(t, ok, "the download channel must be closed on cancellation")
}

func TestDownloaderSovereign_Download_CancelWhileBlockedOnSend(t *testing.T) {
	t.Parallel()

	fromBlock := uint64(100)
	latestBlock := uint64(120)
	l2GERAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	testGER := common.HexToHash("0x1234")
	parentHash := common.HexToHash("0xabc123")
	testBlockHeader := &aggkittypes.BlockHeader{
		Number:     fromBlock,
		ParentHash: &parentHash,
		Hash:       common.HexToHash("0xdef456"),
		Time:       uint64(time.Now().Unix()),
	}

	mockL2Client := aggkittypesmocks.NewBaseEthereumClienter(t)
	mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
	mockL1InfoTreeSync := l2gersyncmocks.NewL1InfoTreeQuerier(t)
	rh := &sync.RetryHandler{MaxRetryAttemptsAfterError: 5, RetryAfterErrorPeriod: time.Millisecond}

	mockL2Client.EXPECT().ChainID(mock.Anything).Return(big.NewInt(1), nil).Maybe()
	mockL2Client.EXPECT().CustomHeaderByNumber(mock.Anything, &aggkittypes.LatestBlock).
		Return(&aggkittypes.BlockHeader{Number: latestBlock}, nil).Maybe()
	mockL2Client.EXPECT().CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(fromBlock)).
		Return(testBlockHeader, nil).Maybe()
	mockL2Client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]ethtypes.Log{{
		Address:     l2GERAddr,
		Topics:      []common.Hash{insertGEREventSignature, testGER, common.HexToHash("0x5678")},
		Data:        []byte{},
		BlockNumber: fromBlock,
		TxHash:      common.HexToHash("0x111"),
		BlockHash:   testBlockHeader.Hash,
	}}, nil).Maybe()

	// The appender resolves the GER right before the block is sent to the driver.
	reachedSend := make(chan struct{})
	mockL1InfoTreeSync.EXPECT().GetInfoByGlobalExitRoot(testGER).
		Run(func(common.Hash) {
			select {
			case <-reachedSend:
			default:
				close(reachedSend)
			}
		}).
		Return(&l1infotreesync.L1InfoTreeLeaf{GlobalExitRoot: testGER, BlockNumber: fromBlock}, nil).Maybe()

	downloader, err := newDownloaderSovereign(mockL2Client, l2GERAddr, mockL1InfoTreeSync, mockL1Client,
		common.HexToAddress("0x01"), rh, aggkittypes.LatestBlock, 10*time.Millisecond, 10)
	require.NoError(t, err)

	// Unbuffered and never read: the downloader blocks on its first send.
	downloadedCh := make(chan sync.EVMBlock)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	downloadDone := make(chan struct{})
	go func() {
		defer close(downloadDone)
		downloader.Download(ctx, fromBlock, downloadedCh, nil, false)
	}()

	requireDownloadExitsOnCancel(t, reachedSend, cancel, downloadDone, downloadedCh)
}

func TestDownloaderLegacy_Download_CancelWhileBlockedOnSend(t *testing.T) {
	t.Parallel()

	fromBlock := uint64(100)
	latestBlock := uint64(120)
	parentHash := common.HexToHash("0xabc123")
	header := &aggkittypes.BlockHeader{
		Number:     latestBlock,
		ParentHash: &parentHash,
		Hash:       common.HexToHash("0xdef456"),
		Time:       uint64(time.Now().Unix()),
	}

	proc, err := newProcessor(path.Join(t.TempDir(), "l2gersync_legacy_cancel.sqlite"))
	require.NoError(t, err)

	mockL2Client := aggkittypesmocks.NewBaseEthereumClienter(t)
	mockL1InfoTreeSync := l2gersyncmocks.NewL1InfoTreeQuerier(t)
	rh := &sync.RetryHandler{MaxRetryAttemptsAfterError: 5, RetryAfterErrorPeriod: time.Millisecond}

	// Both the head polling and the header of the block to report return the same header.
	mockL2Client.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).Return(header, nil).Maybe()

	// No GER to report: the block is sent without events right after this call.
	reachedSend := make(chan struct{})
	mockL1InfoTreeSync.EXPECT().GetLastL1InfoTreeRoot(mock.Anything).
		Run(func(context.Context) {
			select {
			case <-reachedSend:
			default:
				close(reachedSend)
			}
		}).
		Return(treetypes.Root{}, db.ErrNotFound).Maybe()

	downloader, err := newDownloaderLegacy(mockL2Client, common.HexToAddress("0x1234"), mockL1InfoTreeSync,
		proc, rh, aggkittypes.LatestBlock, 10*time.Millisecond)
	require.NoError(t, err)

	// Unbuffered and never read: the downloader blocks on its first send.
	downloadedCh := make(chan sync.EVMBlock)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	downloadDone := make(chan struct{})
	go func() {
		defer close(downloadDone)
		downloader.Download(ctx, fromBlock, downloadedCh, nil, false)
	}()

	requireDownloadExitsOnCancel(t, reachedSend, cancel, downloadDone, downloadedCh)
}
