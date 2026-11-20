package bridgesync

import (
	"context"
	"errors"
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/sync/mdrsynctest"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// bridgeEventLogForMultidownloader builds a parseable BridgeEvent log, with the given deposit
// count, as the multidownloader returns it
func bridgeEventLogForMultidownloader(t *testing.T, bridge common.Address, depositCount uint32) mdrtypes.Log {
	t.Helper()
	abi, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	require.NoError(t, err)
	event, err := abi.EventByID(bridgeEventSignature)
	require.NoError(t, err)
	data, err := event.Inputs.Pack(bridgeLeafTypeAsset, uint32(1), common.HexToAddress("0x50"), uint32(2),
		common.HexToAddress("0x30"), big.NewInt(100), []byte{}, depositCount)
	require.NoError(t, err)
	return mdrtypes.Log{
		Address: bridge,
		Topics:  []common.Hash{bridgeEventSignature},
		Data:    data,
		TxHash:  common.BigToHash(big.NewInt(int64(depositCount) + 1)),
	}
}

func requireLastProcessedBlock(t *testing.T, bs *BridgeSync, expected uint64) {
	t.Helper()
	require.Eventually(t, func() bool {
		last, found, err := bs.GetLastProcessedBlock(t.Context())
		return err == nil && found && last == expected
	}, 10*time.Second, 5*time.Millisecond, "last processed block must be %d", expected)
}

// TestMultidownloaderSyncer_SyncsAndHandlesReorg runs an L1 bridge syncer over an in-memory
// multidownloader, with no legacy reorg detector at all. It verifies that the syncer downloads and
// processes the blocks, notifies them to the subscribers, reads the finality of the blocks from the
// multidownloader and, when the multidownloader detects a reorg, rolls back the reorged blocks and
// syncs the new ones.
func TestMultidownloaderSyncer_SyncsAndHandlesReorg(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	cfg := mdTestConfig(t, bridge, cfgtypes.Multidownloader)
	cfg.WaitForNewBlocksPeriod = cfgtypes.NewDuration(time.Millisecond)
	cfg.RetryAfterErrorPeriod = cfgtypes.NewDuration(10 * time.Millisecond)
	cfg.MaxRetryAttemptsAfterError = -1

	// The only RPC calls are the ones that check the contract: the logs, the headers and the
	// transactions come from the multidownloader
	ethClient := mocksethclient.NewEthClienter(t)
	expectBridgeDeploymentAndSanity(t, ethClient, bridge)

	// Chain: [1000, 1001] empty, 1002 bridge 0, 1003 empty, 1004 bridge 1, 1005 empty
	backend := mdrsynctest.NewFakeMultidownloader(cfg.InitialBlockNum)
	bridgeAddr := bridge.Hex()
	// The transactions are sent directly to the bridge, so no trace is needed
	backend.Tx = &aggkittypes.RPCTransaction{FromRaw: "0x1111111111111111111111111111111111111111", To: &bridgeAddr}
	backend.AddEmptyBlocks(2)
	block1002 := backend.AddBlock(bridgeEventLogForMultidownloader(t, bridge, 0))
	backend.AddEmptyBlocks(1)
	block1004 := backend.AddBlock(bridgeEventLogForMultidownloader(t, bridge, 1))
	block1005 := backend.AddBlock()
	backend.SetFinalizedBlock(1002)

	bs, err := NewL1WithDownloader(ctx, cfg, ethClient, 0, backend, backend)
	require.NoError(t, err)
	blocksCh := bs.SubscribeToSync("test")
	go bs.Start(ctx)

	// It processes the blocks with events and the last block of the range
	requireLastProcessedBlock(t, bs, 1005)
	bridges, err := bs.GetBridges(ctx, 1000, 1005)
	require.NoError(t, err)
	require.Len(t, bridges, 2)
	require.Equal(t, uint64(1002), bridges[0].BlockNum)
	require.Equal(t, uint64(1004), bridges[1].BlockNum)
	require.Equal(t, common.HexToAddress(backend.Tx.FromRaw), bridges[0].TxnSender)

	// The processed blocks are notified in order
	for _, expected := range []aggkittypes.BlockHeader{block1002, block1004, block1005} {
		select {
		case block := <-blocksCh:
			require.Equal(t, expected.Number, block.Num)
			require.Equal(t, expected.Hash, block.Hash)
		case <-time.After(10 * time.Second):
			require.Fail(t, "block not notified", "block %d", expected.Number)
		}
	}

	// The finality of the blocks is the one of the multidownloader
	finalized, found, err := bs.IsBlockFinalized(ctx, 1002, block1002.Hash)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, finalized)
	finalized, found, err = bs.IsBlockFinalized(ctx, 1004, block1004.Hash)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, finalized)

	// No reorg yet
	lastReorg, err := bs.GetLastReorgEvent(ctx)
	require.NoError(t, err)
	require.Equal(t, &LastReorg{}, lastReorg)

	// The multidownloader detects a reorg from block 1004: the syncer removes the reorged blocks
	// and stays at the last block that is still available
	backend.Reorg(1004)
	requireLastProcessedBlock(t, bs, 1003)
	bridges, err = bs.GetBridges(ctx, 1000, 1005)
	require.NoError(t, err)
	require.Len(t, bridges, 1)

	// In the new chain the bridge 1 is in block 1005
	backend.AddEmptyBlocks(1)
	newBlock1005 := backend.AddBlock(bridgeEventLogForMultidownloader(t, bridge, 1))
	backend.AddEmptyBlocks(1)
	requireLastProcessedBlock(t, bs, 1006)
	bridges, err = bs.GetBridges(ctx, 1000, 1006)
	require.NoError(t, err)
	require.Len(t, bridges, 2)
	require.Equal(t, uint64(1005), bridges[1].BlockNum)

	// The reorged block is unknown, the new one is not finalized
	_, found, err = bs.IsBlockFinalized(ctx, 1004, block1004.Hash)
	require.NoError(t, err)
	require.False(t, found)
	finalized, found, err = bs.IsBlockFinalized(ctx, 1005, newBlock1005.Hash)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, finalized)

	lastReorg, err = bs.GetLastReorgEvent(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1004), lastReorg.FromBlock)
	require.Equal(t, uint64(1005), lastReorg.ToBlock)
}

// reorgDataBackend is a multidownloader that only answers the last reorg
type reorgDataBackend struct {
	MultidownloaderBackend
	reorgData *mdrtypes.ReorgData
	err       error
}

func (b *reorgDataBackend) GetLastReorgData(context.Context) (*mdrtypes.ReorgData, error) {
	return b.reorgData, b.err
}

func TestGetLastReorgEventFromMultidownloader_Errors(t *testing.T) {
	expectedErr := errors.New("storage error")
	bs := &BridgeSync{
		processor:       &processor{log: log.WithFields("module", "test")},
		multidownloader: &reorgDataBackend{err: expectedErr},
	}
	_, err := bs.GetLastReorgEvent(t.Context())
	require.ErrorIs(t, err, expectedErr)

	bs.multidownloader = &reorgDataBackend{reorgData: &mdrtypes.ReorgData{DetectedTimestamp: math.MaxUint64}}
	_, err = bs.GetLastReorgEvent(t.Context())
	require.ErrorContains(t, err, "overflows")
}
