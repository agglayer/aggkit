package claimsync

import (
	"context"
	"math/big"
	"sync"
	"testing"
	"time"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	configtypes "github.com/agglayer/aggkit/config/types"
	aggsync "github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mdLazyTip is the fake chain tip the backend reports for any block tag.
const mdLazyTip uint64 = 15

// mdLazyChainID is the fake local chain ID exposed by the backend.
const mdLazyChainID uint64 = 1337

// mdLazyFilterRange is an inclusive block range recorded for a FilterLogs call.
type mdLazyFilterRange struct {
	from uint64
	to   uint64
}

// mdLazyBackend is the fake multidownloader backend used by the lazy integration test. It embeds the
// existing fakeMultidownloader (for RegisterSyncer/EthClient/Start) and overrides the RPC surface so
// the real legacy EVMDriver + EVMDownloader can run against it: the chain has a fixed tip and no
// logs, and every height has a valid, deterministic header.
//
// The multidownloader is supposed to have already downloaded/preloaded the historical logs before the
// syncer is wired up, so the backend answers FilterLogs for any range but never implies that the
// syncer has a processed checkpoint: the checkpoint must be created lazily.
type mdLazyBackend struct {
	fakeMultidownloader

	mu           sync.Mutex
	chainIDCalls int
	filters      []mdLazyFilterRange

	chainIDOnce  sync.Once
	chainIDReady chan struct{}
}

var _ aggkittypes.MultiDownloaderLegacy = (*mdLazyBackend)(nil)

func newMDLazyBackend() *mdLazyBackend {
	return &mdLazyBackend{chainIDReady: make(chan struct{})}
}

// mdLazyHash returns a deterministic, non-zero hash for the given height.
func mdLazyHash(height uint64) common.Hash {
	return common.BigToHash(new(big.Int).SetUint64(height + 1))
}

// headerFor builds a valid header for the given numeric height, including a non-nil parent hash.
func (b *mdLazyBackend) headerFor(height uint64) *aggkittypes.BlockHeader {
	parent := mdLazyHash(0)
	if height > 0 {
		parent = mdLazyHash(height - 1)
	}
	return &aggkittypes.BlockHeader{
		Number:     height,
		Hash:       mdLazyHash(height),
		Time:       1_700_000_000 + height,
		ParentHash: &parent,
	}
}

// ChainID is called by the real compatibility check; it closes the ready signal once so the test can
// deterministically wait for the driver to start without sleeping.
func (b *mdLazyBackend) ChainID(context.Context) (uint64, error) {
	b.mu.Lock()
	b.chainIDCalls++
	b.mu.Unlock()
	b.chainIDOnce.Do(func() { close(b.chainIDReady) })
	return mdLazyChainID, nil
}

// BlockNumber resolves every finality to the fixed tip.
func (b *mdLazyBackend) BlockNumber(context.Context, aggkittypes.BlockNumberFinality) (uint64, error) {
	return mdLazyTip, nil
}

// HeaderByNumber resolves tags to the fixed tip and numeric references to their concrete height.
func (b *mdLazyBackend) HeaderByNumber(
	_ context.Context, number *aggkittypes.BlockNumberFinality,
) (*aggkittypes.BlockHeader, error) {
	if number == nil {
		return b.headerFor(mdLazyTip), nil
	}
	if number.Block == aggkittypes.Constant {
		return b.headerFor(number.Specific), nil
	}
	return b.headerFor(mdLazyTip), nil
}

// FilterLogs records the inclusive range and returns no logs, emulating a history that was already
// downloaded by the multidownloader.
func (b *mdLazyBackend) FilterLogs(_ context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.filters = append(b.filters, mdLazyFilterRange{from: q.FromBlock.Uint64(), to: q.ToBlock.Uint64()})
	return nil, nil
}

func (b *mdLazyBackend) filterRanges() []mdLazyFilterRange {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]mdLazyFilterRange, len(b.filters))
	copy(out, b.filters)
	return out
}

// TestMDLazyIntegration_BootstrapsOnDemand verifies the end-to-end lazy integration between the
// multidownloader backend and the real legacy EVMDriver: a preloaded (empty) history must not imply a
// processed checkpoint, the first explicit bootstrap processes exactly the requested block instead of
// cfg.InitialBlockNum, and the incremental sync then resumes right after that bootstrap.
func TestMDLazyIntegration_BootstrapsOnDemand(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	cfg := mdStandaloneConfig(t, bridge, configtypes.Multidownloader)
	cfg.InitialBlockNum = 1
	cfg.BlockFinality = aggkittypes.FinalizedBlock
	cfg.RetryAfterErrorPeriod = configtypes.NewDuration(time.Millisecond)
	cfg.WaitForNewBlocksPeriod = configtypes.NewDuration(time.Millisecond)
	cfg.RequireStorageContentCompatibility = true

	ethClient := mocksethclient.NewEthClienter(t)
	expectSovereignBridgeDeployment(t, ethClient, bridge)

	// Constructor-time reorg detector wiring (finalized block type + subscription) plus the
	// reorg-detector calls the downloader makes while it tracks the tip. Tracking is best-effort, so
	// the extra expectations are optional.
	rd := expectMDReorgDetector(t)
	rd.EXPECT().AddBlockToTrack(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	rd.EXPECT().GetTrackedBlockByBlockNumber(mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	backend := newMDLazyBackend()

	cs, err := NewStandaloneClaimSyncWithDownloader(
		ctx, cfg, rd, ethClient, claimsynctypes.L1ClaimSyncer, 0, backend)
	require.NoError(t, err)
	require.NotNil(t, cs)

	// Subscribe before starting so no post-commit publication is missed.
	blocksCh := cs.driver.SubscribeToNewBlocks("md-lazy-test")

	startDone := make(chan struct{})
	go func() {
		defer close(startDone)
		cs.Start(ctx)
	}()

	const waitTimeout = 15 * time.Second

	// Wait for the real compatibility check to reach the backend (ChainID). This is a signal, not a
	// sleep: it guarantees the real driver has started and is running its bootstrap-wait loop.
	select {
	case <-backend.chainIDReady:
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for the driver to start (backend ChainID was never called)")
	}

	// The multidownloader's preloaded history must not be mistaken for a processed checkpoint.
	lastProcessed, found, err := cs.processor.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	require.False(t, found, "preloaded history must not imply a processed checkpoint")
	require.Zero(t, lastProcessed)
	require.Empty(t, backend.filterRanges(),
		"the driver must not download logs before an explicit bootstrap creates a checkpoint")

	// Lazy bootstrap: the first required block is 10, not cfg.InitialBlockNum (1).
	require.NoError(t, cs.SetNextRequiredBlock(ctx, 10))

	recvBlock := func() aggsync.Block {
		t.Helper()
		select {
		case b := <-blocksCh:
			return b
		case <-time.After(waitTimeout):
			t.Fatal("timed out waiting for a processed block publication")
			return aggsync.Block{}
		}
	}

	first := recvBlock()
	require.Equal(t, uint64(10), first.Num,
		"the bootstrap must process the requested block, not cfg.InitialBlockNum")
	require.Equal(t, mdLazyHash(10), first.Hash)

	second := recvBlock()
	require.Equal(t, mdLazyTip, second.Num, "the incremental sync must reach the chain tip")
	require.Equal(t, mdLazyHash(mdLazyTip), second.Hash,
		"the checkpoint hash must match the chain header for that height")

	// The first processed block is the bootstrap block; the incremental sync resumes at 11 (not 1).
	lastProcessed, found, err = cs.processor.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, mdLazyTip, lastProcessed)

	ranges := backend.filterRanges()
	require.Len(t, ranges, 2, "exactly the bootstrap range and the incremental range must be downloaded")
	require.Equal(t, mdLazyFilterRange{from: 10, to: 10}, ranges[0],
		"the first download must be the bootstrap block only")
	require.Equal(t, uint64(11), ranges[1].from,
		"the incremental sync must resume after the bootstrap, not at cfg.InitialBlockNum")

	// Only the constructor probe (eth_call) may reach the original client: no raw RPC, no debug
	// tracing and no direct FilterLogs on the client (logs come from the multidownloader backend).
	ethClient.AssertNumberOfCalls(t, "CallContract", 1)
	ethClient.AssertNotCalled(t, "Call", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	ethClient.AssertNotCalled(t, "FilterLogs", mock.Anything, mock.Anything)

	// Cancel and join the Start goroutine: the driver must stop cleanly and leak no worker.
	cancel()
	select {
	case <-startDone:
	case <-time.After(waitTimeout):
		t.Fatal("ClaimSync.Start did not return after context cancellation")
	}

	require.Len(t, backend.filterRanges(), 2, "no log download may happen after cancellation")

	select {
	case b := <-blocksCh:
		t.Fatalf("unexpected publication after cancellation: %v", b)
	default:
	}
}
