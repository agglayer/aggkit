package claimsync

import (
	"context"
	"testing"
	"time"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/multidownloader/sync/mdrsynctest"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const mdTestWaitTimeout = 15 * time.Second

// newMDSyncerForTest creates an L1 claim syncer over an in-memory multidownloader whose chain has
// the blocks [1, lastBlock], all of them without logs. There is no legacy reorg detector at all.
func newMDSyncerForTest(
	t *testing.T, ctx context.Context, autoStart configtypes.TrueFalseAutoMode, lastBlock int,
) (*ClaimSync, *mdrsynctest.FakeMultidownloader, *mocksethclient.EthClienter) {
	t.Helper()
	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	cfg := mdStandaloneConfig(t, bridge, configtypes.Multidownloader)
	cfg.InitialBlockNum = 1
	cfg.BlockFinality = aggkittypes.FinalizedBlock
	cfg.RetryAfterErrorPeriod = configtypes.NewDuration(time.Millisecond)
	cfg.WaitForNewBlocksPeriod = configtypes.NewDuration(time.Millisecond)
	cfg.RequireStorageContentCompatibility = true
	cfg.AutoStart = autoStart

	ethClient := mocksethclient.NewEthClienter(t)
	expectSovereignBridgeDeployment(t, ethClient, bridge)

	backend := mdrsynctest.NewFakeMultidownloader(cfg.InitialBlockNum)
	backend.AddEmptyBlocks(lastBlock)

	cs, err := NewStandaloneClaimSyncWithDownloader(ctx, cfg, ethClient, claimsynctypes.L1ClaimSyncer, 0, backend)
	require.NoError(t, err)
	require.NotNil(t, cs)
	return cs, backend, ethClient
}

// startMDSyncerForTest starts the syncer, it returns a function that stops it and waits until it
// has stopped
func startMDSyncerForTest(t *testing.T, ctx context.Context, cs *ClaimSync) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	startDone := make(chan struct{})
	go func() {
		defer close(startDone)
		cs.Start(ctx)
	}()
	return func() {
		cancel()
		select {
		case <-startDone:
		case <-time.After(mdTestWaitTimeout):
			require.Fail(t, "ClaimSync.Start did not return after context cancellation")
		}
	}
}

func requireMDLastProcessedBlock(t *testing.T, cs *ClaimSync, expected uint64) {
	t.Helper()
	require.Eventually(t, func() bool {
		last, found, err := cs.GetLastProcessedBlock(t.Context())
		return err == nil && found && last == expected
	}, mdTestWaitTimeout, time.Millisecond, "last processed block must be %d", expected)
}

// TestMDLazyIntegration_BootstrapsOnDemand verifies the end-to-end lazy integration between the
// multidownloader and the syncer: a preloaded (empty) history must not imply a processed checkpoint,
// the first explicit bootstrap processes exactly the requested block instead of cfg.InitialBlockNum,
// and the incremental sync then resumes right after that bootstrap.
func TestMDLazyIntegration_BootstrapsOnDemand(t *testing.T) {
	ctx := t.Context()
	const tip = 15
	cs, backend, ethClient := newMDSyncerForTest(t, ctx, configtypes.FalseMode, tip)
	stop := startMDSyncerForTest(t, ctx, cs)
	defer stop()

	// The multidownloader's preloaded history must not be mistaken for a processed checkpoint: the
	// syncer waits for an explicit bootstrap
	require.Never(t, func() bool {
		_, found, err := cs.GetLastProcessedBlock(ctx)
		return err != nil || found
	}, 100*time.Millisecond, 5*time.Millisecond, "preloaded history must not imply a processed checkpoint")

	// Lazy bootstrap: the first required block is 10, not cfg.InitialBlockNum (1).
	require.NoError(t, cs.SetNextRequiredBlock(ctx, 10))

	// The incremental sync resumes after the bootstrap block and reaches the chain tip
	requireMDLastProcessedBlock(t, cs, tip)
	firstBlock, found, err := cs.GetFirstProcessedBlock(ctx)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(10), firstBlock,
		"the bootstrap must process the requested block, not cfg.InitialBlockNum")

	// The stored hashes are the ones of the chain
	for _, blockNum := range []uint64{10, tip} {
		header, _, err := backend.StorageHeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNum))
		require.NoError(t, err)
		hash, found, err := cs.reader.GetBlockHash(ctx, nil, blockNum)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, header.Hash, hash)
	}

	// A second bootstrap is a no-op
	require.NoError(t, cs.SetNextRequiredBlock(ctx, 12))
	require.NoError(t, cs.SyncNextBlock(ctx, 12))

	// Only the constructor probe (eth_call) may reach the original client: no raw RPC, no debug
	// tracing and no direct FilterLogs on the client (logs come from the multidownloader).
	ethClient.AssertNumberOfCalls(t, "CallContract", 1)
	ethClient.AssertNotCalled(t, "Call", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	ethClient.AssertNotCalled(t, "FilterLogs", mock.Anything, mock.Anything)
}

// TestMDIntegration_ReorgAndFinality verifies that a syncer that is synced through the
// multidownloader rolls back the blocks of a reorg that the multidownloader detects, syncs the new
// ones, and reads the finality of the blocks from the multidownloader.
func TestMDIntegration_ReorgAndFinality(t *testing.T) {
	ctx := t.Context()
	cs, backend, _ := newMDSyncerForTest(t, ctx, configtypes.TrueMode, 5)
	backend.SetFinalizedBlock(5)
	stop := startMDSyncerForTest(t, ctx, cs)
	defer stop()

	// Each step processes the last block of the downloaded range: 5, then 8
	requireMDLastProcessedBlock(t, cs, 5)
	block5, _, err := backend.StorageHeaderByNumber(ctx, aggkittypes.NewBlockNumber(5))
	require.NoError(t, err)
	backend.AddEmptyBlocks(3)
	requireMDLastProcessedBlock(t, cs, 8)
	block8, _, err := backend.StorageHeaderByNumber(ctx, aggkittypes.NewBlockNumber(8))
	require.NoError(t, err)

	finalized, found, err := cs.IsBlockFinalized(ctx, 5, block5.Hash)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, finalized)
	finalized, found, err = cs.IsBlockFinalized(ctx, 8, block8.Hash)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, finalized)

	// The multidownloader detects a reorg from block 7: the block 8 is removed and the syncer
	// stays at the last block that is still available
	backend.Reorg(7)
	requireMDLastProcessedBlock(t, cs, 6)
	_, found, err = cs.reader.GetBlockHash(ctx, nil, 8)
	require.NoError(t, err)
	require.False(t, found)

	// The new chain is synced
	backend.AddEmptyBlocks(3)
	requireMDLastProcessedBlock(t, cs, 9)
	_, found, err = cs.IsBlockFinalized(ctx, 8, block8.Hash)
	require.NoError(t, err)
	require.False(t, found, "the reorged block is unknown")
}
