package reorgdetector

import (
	"errors"
	"fmt"
	"path"
	"testing"
	"time"

	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/etherman"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

const rollbackTestSubscriber = "test-syncer"

// newRollbackTestDetector returns a reorg detector (not started) with rollbackTestSubscriber subscribed and
// tracking the given blocks. The reorgs notified to the subscriber are acknowledged and sent to the channel
func newRollbackTestDetector(t *testing.T, client aggkittypes.BaseEthereumClienter,
	tracked ...Header) (*ReorgDetector, <-chan uint64) {
	t.Helper()

	rd, err := New(client, Config{
		DBPath:              path.Join(t.TempDir(), "reorgdetector.sqlite"),
		CheckReorgsInterval: cfgtypes.NewDuration(time.Hour),
	}, L1)
	require.NoError(t, err)

	sub, err := rd.Subscribe(rollbackTestSubscriber)
	require.NoError(t, err)
	for _, hdr := range tracked {
		require.NoError(t, rd.AddBlockToTrack(t.Context(), rollbackTestSubscriber, hdr.Num, hdr.Hash))
	}

	notified := make(chan uint64, 10)
	go func() {
		for {
			select {
			case <-t.Context().Done():
				return
			case blockNum := <-sub.ReorgedBlock:
				notified <- blockNum
				sub.ReorgProcessed <- true
			}
		}
	}()

	return rd, notified
}

func requireReorgEvents(t *testing.T, rd *ReorgDetector, expected int) []*ReorgEvent {
	t.Helper()

	var events []*ReorgEvent
	require.NoError(t, meddler.QueryAll(rd.db, &events, "SELECT * FROM reorg_event ORDER BY rowid;"))
	require.Len(t, events, expected)

	return events
}

func requireTrackedBlocks(t *testing.T, rd *ReorgDetector, expected ...uint64) {
	t.Helper()

	// in memory
	rd.trackedBlocksLock.RLock()
	hdrs := rd.trackedBlocks[rollbackTestSubscriber]
	rd.trackedBlocksLock.RUnlock()
	inMemory := make([]uint64, 0, len(expected))
	for _, hdr := range hdrs.getSorted() {
		inMemory = append(inMemory, hdr.Num)
	}
	require.ElementsMatch(t, expected, inMemory)

	// in the DB
	fromDB, err := rd.getTrackedBlocks()
	require.NoError(t, err)
	inDB := make([]uint64, 0, len(expected))
	if list, ok := fromDB[rollbackTestSubscriber]; ok {
		for _, hdr := range list.getSorted() {
			inDB = append(inDB, hdr.Num)
		}
	}
	require.ElementsMatch(t, expected, inDB)
}

func requireNoNotification(t *testing.T, notified <-chan uint64) {
	t.Helper()

	select {
	case blockNum := <-notified:
		t.Fatalf("unexpected reorg notification for block %d", blockNum)
	default:
	}
}

func TestDetectReorgs_RollbackToShorterChain(t *testing.T) {
	t.Parallel()

	var (
		finalized = &aggkittypes.BlockHeader{Number: 5, Hash: common.HexToHash("0x5")}
		block8    = Header{Num: 8, Hash: common.HexToHash("0x8")}
		block9    = Header{Num: 9, Hash: common.HexToHash("0x9")}
		block10   = Header{Num: 10, Hash: common.HexToHash("0x10")}
		// What the clients return for a block that doesn't exist: go-ethereum's ethclient returns
		// ethereum.NotFound and DefaultEthClient with HashFromJSON wraps a "not found: ...: block is null"
		errNotFoundVariants = map[string]error{
			"ethereum.NotFound": ethereum.NotFound,
			"wrapped NotFound":  fmt.Errorf("cannot get header: %w", ethereum.NotFound),
			"null block":        errors.New("rpcGetBlockByNumber:not found:  0x9: block is null"),
		}
	)
	blockNumber := aggkittypes.NewBlockNumber
	header := func(h Header) *aggkittypes.BlockHeader { return &aggkittypes.BlockHeader{Number: h.Num, Hash: h.Hash} }

	for name, errNotFound := range errNotFoundVariants {
		t.Run("tracked blocks above the head are a reorg from the first missing one/"+name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			client := aggkittypesmocks.NewBaseEthereumClienter(t)
			client.EXPECT().CustomHeaderByNumber(ctx, &aggkittypes.FinalizedBlock).Return(finalized, nil).Times(2)
			client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(8)).Return(header(block8), nil).Times(2)
			client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(9)).Return(nil, errNotFound).Times(2)
			client.EXPECT().BlockNumber(ctx).Return(uint64(8), nil).Times(2)
			// block 10 is never requested: the check stops at the first missing block

			rd, notified := newRollbackTestDetector(t, client, block8, block9, block10)

			// First pass: the block is above the head, but it is not reported yet
			require.NoError(t, rd.detectReorgInTrackedList(ctx))
			requireNoNotification(t, notified)
			requireReorgEvents(t, rd, 0)
			requireTrackedBlocks(t, rd, 8, 9, 10)

			// Second pass: still above the head, so it is a reorg from block 9
			require.NoError(t, rd.detectReorgInTrackedList(ctx))
			require.Equal(t, uint64(9), <-notified)
			events := requireReorgEvents(t, rd, 1)
			require.Equal(t, uint64(9), events[0].FromBlock)
			require.Equal(t, uint64(10), events[0].ToBlock)
			require.Equal(t, rollbackTestSubscriber, events[0].SubscriberID)
			require.Equal(t, block9.Hash, events[0].TrackedHash)
			require.Equal(t, common.Hash{}, events[0].CurrentHash)
			requireTrackedBlocks(t, rd, 8)

			// The subscriber's event is the one returned
			last, err := rd.GetLastReorgEventBySubscriber(ctx, rollbackTestSubscriber)
			require.NoError(t, err)
			require.Equal(t, *events[0], last)
		})
	}

	t.Run("a transient error getting the header is not a reorg", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		errTransient := errors.New("connection refused")

		client := aggkittypesmocks.NewBaseEthereumClienter(t)
		client.EXPECT().CustomHeaderByNumber(ctx, &aggkittypes.FinalizedBlock).Return(finalized, nil).Times(3)
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(9)).Return(nil, errTransient).Times(3)
		// BlockNumber is not expected: the head is only checked for a "not found"

		rd, notified := newRollbackTestDetector(t, client, block9, block10)

		for range 3 {
			err := rd.detectReorgInTrackedList(ctx)
			require.ErrorIs(t, err, errTransient)
			require.ErrorContains(t, err, "failed to get the header 9")
		}
		requireNoNotification(t, notified)
		requireReorgEvents(t, rd, 0)
		requireTrackedBlocks(t, rd, 9, 10)
	})

	t.Run("a not found block that is not above the head is not a reorg", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		client := aggkittypesmocks.NewBaseEthereumClienter(t)
		client.EXPECT().CustomHeaderByNumber(ctx, &aggkittypes.FinalizedBlock).Return(finalized, nil).Times(3)
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(9)).Return(nil, ethereum.NotFound).Times(3)
		// The head is the block itself, and then beyond it
		client.EXPECT().BlockNumber(ctx).Return(uint64(9), nil).Once()
		client.EXPECT().BlockNumber(ctx).Return(uint64(12), nil).Times(2)

		rd, notified := newRollbackTestDetector(t, client, block9, block10)

		for range 3 {
			err := rd.detectReorgInTrackedList(ctx)
			require.ErrorIs(t, err, ethereum.NotFound)
			require.ErrorContains(t, err, "failed to get the header 9")
		}
		requireNoNotification(t, notified)
		requireReorgEvents(t, rd, 0)
		requireTrackedBlocks(t, rd, 9, 10)
	})

	t.Run("failing to get the head is not a reorg", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		errHead := errors.New("timeout")

		client := aggkittypesmocks.NewBaseEthereumClienter(t)
		client.EXPECT().CustomHeaderByNumber(ctx, &aggkittypes.FinalizedBlock).Return(finalized, nil).Times(2)
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(9)).Return(nil, ethereum.NotFound).Times(2)
		client.EXPECT().BlockNumber(ctx).Return(uint64(0), errHead).Times(2)

		rd, notified := newRollbackTestDetector(t, client, block9)

		for range 2 {
			err := rd.detectReorgInTrackedList(ctx)
			require.ErrorIs(t, err, errHead)
			require.ErrorIs(t, err, ethereum.NotFound)
		}
		requireNoNotification(t, notified)
		requireReorgEvents(t, rd, 0)
		requireTrackedBlocks(t, rd, 9)
	})

	t.Run("a block missing in non consecutive passes is not a reorg", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		client := aggkittypesmocks.NewBaseEthereumClienter(t)
		client.EXPECT().CustomHeaderByNumber(ctx, &aggkittypes.FinalizedBlock).Return(finalized, nil).Times(3)
		// A node that is behind answers the first and the third pass, an up to date one the second
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(9)).Return(nil, ethereum.NotFound).Once()
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(9)).Return(header(block9), nil).Once()
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(9)).Return(nil, ethereum.NotFound).Once()
		client.EXPECT().BlockNumber(ctx).Return(uint64(8), nil).Times(2)

		rd, notified := newRollbackTestDetector(t, client, block9)

		for range 3 {
			require.NoError(t, rd.detectReorgInTrackedList(ctx))
		}
		requireNoNotification(t, notified)
		requireReorgEvents(t, rd, 0)
		requireTrackedBlocks(t, rd, 9)
	})

	t.Run("a head that contradicts the finalized block is not a reorg", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		client := aggkittypesmocks.NewBaseEthereumClienter(t)
		// The finalized block is 9 and then 12, but the node that answers the rest is at block 8
		client.EXPECT().CustomHeaderByNumber(ctx, &aggkittypes.FinalizedBlock).Return(header(block9), nil).Times(2)
		client.EXPECT().CustomHeaderByNumber(ctx, &aggkittypes.FinalizedBlock).
			Return(&aggkittypes.BlockHeader{Number: 12}, nil).Times(2)
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(9)).Return(nil, ethereum.NotFound).Times(4)
		client.EXPECT().BlockNumber(ctx).Return(uint64(8), nil).Times(4)

		rd, notified := newRollbackTestDetector(t, client, block9, block10)

		for range 4 {
			err := rd.detectReorgInTrackedList(ctx)
			require.ErrorIs(t, err, ethereum.NotFound)
			require.ErrorContains(t, err, "inconsistent chain head 8")
		}
		requireNoNotification(t, notified)
		requireReorgEvents(t, rd, 0)
		requireTrackedBlocks(t, rd, 9, 10)
	})

	t.Run("a non final block above the head of a lagging node in two consecutive passes is a reorg", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		// Known trade-off: the detector cannot tell a rollback from a node that stays behind the one used by the
		// subscriber for two consecutive passes, as long as its head is not below the finalized block
		client := aggkittypesmocks.NewBaseEthereumClienter(t)
		client.EXPECT().CustomHeaderByNumber(ctx, &aggkittypes.FinalizedBlock).Return(finalized, nil).Times(2)
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(10)).Return(nil, ethereum.NotFound).Times(2)
		client.EXPECT().BlockNumber(ctx).Return(uint64(9), nil).Times(2)

		rd, notified := newRollbackTestDetector(t, client, block10)

		require.NoError(t, rd.detectReorgInTrackedList(ctx))
		require.NoError(t, rd.detectReorgInTrackedList(ctx))
		require.Equal(t, uint64(10), <-notified)
		requireReorgEvents(t, rd, 1)
	})

	t.Run("a hash mismatch below the block missing in the previous pass", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		reorgedBlock9 := &aggkittypes.BlockHeader{Number: 9, Hash: common.HexToHash("0x99")}

		client := aggkittypesmocks.NewBaseEthereumClienter(t)
		client.EXPECT().CustomHeaderByNumber(ctx, &aggkittypes.FinalizedBlock).Return(finalized, nil).Times(3)
		// First pass: block 9 matches and block 10 is above the head. Second pass: block 9 has another hash
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(9)).Return(header(block9), nil).Once()
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(10)).Return(nil, ethereum.NotFound).Once()
		client.EXPECT().BlockNumber(ctx).Return(uint64(9), nil).Once()

		rd, notified := newRollbackTestDetector(t, client, block9, block10)

		require.NoError(t, rd.detectReorgInTrackedList(ctx))
		requireReorgEvents(t, rd, 0)

		// The header of block 9 is cached with the tracked hash, so the mismatch is seen with the new one
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(9)).Return(reorgedBlock9, nil).Once()
		require.NoError(t, rd.detectReorgInTrackedList(ctx))
		require.Equal(t, uint64(9), <-notified)
		events := requireReorgEvents(t, rd, 1)
		require.Equal(t, uint64(9), events[0].FromBlock)
		require.Equal(t, uint64(10), events[0].ToBlock)
		require.Equal(t, reorgedBlock9.Hash, events[0].CurrentHash)
		requireTrackedBlocks(t, rd)

		// Block 10 is tracked again and it is missing: the pending confirmation of the first pass doesn't count
		require.NoError(t, rd.AddBlockToTrack(ctx, rollbackTestSubscriber, block10.Num, block10.Hash))
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(10)).Return(nil, ethereum.NotFound).Once()
		client.EXPECT().BlockNumber(ctx).Return(uint64(9), nil).Once()
		require.NoError(t, rd.detectReorgInTrackedList(ctx))
		requireNoNotification(t, notified)
		requireReorgEvents(t, rd, 1)
		requireTrackedBlocks(t, rd, 10)
	})

	t.Run("each subscriber confirms its own missing block", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		const otherSubscriber = "other-syncer"

		client := aggkittypesmocks.NewBaseEthereumClienter(t)
		client.EXPECT().CustomHeaderByNumber(ctx, &aggkittypes.FinalizedBlock).Return(finalized, nil).Times(2)
		// One request per subscriber and pass for block 8, that exists. The chain head is block 9:
		// block 10, tracked only by rollbackTestSubscriber, is missing
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(8)).Return(header(block8), nil).Times(4)
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(9)).Return(header(block9), nil).Times(2)
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(10)).Return(nil, ethereum.NotFound).Times(2)
		client.EXPECT().BlockNumber(ctx).Return(uint64(9), nil).Times(2)

		rd, notified := newRollbackTestDetector(t, client, block8, block10)
		otherSub, err := rd.Subscribe(otherSubscriber)
		require.NoError(t, err)
		require.NoError(t, rd.AddBlockToTrack(ctx, otherSubscriber, block8.Num, block8.Hash))
		require.NoError(t, rd.AddBlockToTrack(ctx, otherSubscriber, block9.Num, block9.Hash))

		require.NoError(t, rd.detectReorgInTrackedList(ctx))
		requireReorgEvents(t, rd, 0)
		require.NoError(t, rd.detectReorgInTrackedList(ctx))
		require.Equal(t, uint64(10), <-notified)
		select {
		case blockNum := <-otherSub.ReorgedBlock:
			t.Fatalf("unexpected reorg notification for the other subscriber, block %d", blockNum)
		default:
		}
		events := requireReorgEvents(t, rd, 1)
		require.Equal(t, rollbackTestSubscriber, events[0].SubscriberID)
		requireTrackedBlocks(t, rd, 8)

		other, err := rd.GetLastReorgEventBySubscriber(ctx, otherSubscriber)
		require.NoError(t, err)
		require.Equal(t, ReorgEvent{}, other)
	})

	t.Run("a different missing block restarts the confirmation", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		client := aggkittypesmocks.NewBaseEthereumClienter(t)
		client.EXPECT().CustomHeaderByNumber(ctx, &aggkittypes.FinalizedBlock).Return(finalized, nil).Times(3)
		// First pass: block 9 exists and block 10 is above the head. Then the chain is rolled back below block 9
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(9)).Return(header(block9), nil).Once()
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(10)).Return(nil, ethereum.NotFound).Once()
		client.EXPECT().BlockNumber(ctx).Return(uint64(9), nil).Once()
		client.EXPECT().CustomHeaderByNumber(ctx, blockNumber(9)).Return(nil, ethereum.NotFound).Times(2)
		client.EXPECT().BlockNumber(ctx).Return(uint64(8), nil).Times(2)

		rd, notified := newRollbackTestDetector(t, client, block9, block10)

		require.NoError(t, rd.detectReorgInTrackedList(ctx)) // block 10 missing
		require.NoError(t, rd.detectReorgInTrackedList(ctx)) // block 9 missing: not the same block
		requireNoNotification(t, notified)
		requireReorgEvents(t, rd, 0)

		require.NoError(t, rd.detectReorgInTrackedList(ctx)) // block 9 missing again
		require.Equal(t, uint64(9), <-notified)
		events := requireReorgEvents(t, rd, 1)
		require.Equal(t, uint64(9), events[0].FromBlock)
		require.Equal(t, uint64(10), events[0].ToBlock)
		requireTrackedBlocks(t, rd)
	})
}

// Test_ReorgDetector_RollbackToShorterChain checks, with a real client, that dropping blocks without building
// others in their place is detected while the chain is still shorter than the tracked blocks
func Test_ReorgDetector_RollbackToShorterChain(t *testing.T) {
	const subID = "test"

	ctx := t.Context()
	clientL1 := simulated.NewBackend(nil, simulated.WithBlockGasLimit(10000000))
	defer clientL1.Close()

	reorgDetector, err := New(etherman.NewDefaultEthClient(clientL1.Client(), nil, nil),
		Config{
			DBPath:              path.Join(t.TempDir(), "reorgdetector.sqlite"),
			CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 100),
			FinalizedBlock:      aggkittypes.FinalizedBlock,
		}, L1)
	require.NoError(t, err)
	// Start replaces the subscriptions with the ones of the DB, so it goes before Subscribe
	require.NoError(t, reorgDetector.Start(ctx))

	reorgSub, err := reorgDetector.Subscribe(subID)
	require.NoError(t, err)

	hashes := make([]common.Hash, 0)
	for num := uint64(1); num <= 4; num++ {
		hash := clientL1.Commit()
		hashes = append(hashes, hash)
		require.NoError(t, reorgDetector.AddBlockToTrack(ctx, subID, num, hash))
	}

	// Drop blocks 3 and 4: the chain head is block 2 and nothing is built on top of it
	require.NoError(t, clientL1.Fork(hashes[1]))
	head, err := clientL1.Client().BlockNumber(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), head)

	select {
	case firstReorgedBlock := <-reorgSub.ReorgedBlock:
		reorgSub.ReorgProcessed <- true
		require.Equal(t, uint64(3), firstReorgedBlock)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for reorg")
	}

	require.Eventually(t, func() bool {
		reorgDetector.trackedBlocksLock.RLock()
		defer reorgDetector.trackedBlocksLock.RUnlock()
		return reorgDetector.trackedBlocks[subID].len() == 2 // only blocks 1 and 2 left
	}, 5*time.Second, 10*time.Millisecond)

	event, err := reorgDetector.GetLastReorgEventBySubscriber(ctx, subID)
	require.NoError(t, err)
	require.Equal(t, uint64(3), event.FromBlock)
	require.Equal(t, uint64(4), event.ToBlock)
	require.Equal(t, hashes[2], event.TrackedHash)
	require.Equal(t, common.Hash{}, event.CurrentHash)
}
