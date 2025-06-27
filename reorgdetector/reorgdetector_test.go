package reorgdetector

import (
	"context"
	big "math/big"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	cfgtypes "github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	common "github.com/ethereum/go-ethereum/common"
	types "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/require"
)

func Test_ReorgDetector(t *testing.T) {
	const subID = "test"

	ctx := context.Background()

	// Simulated L1
	clientL1 := simulated.NewBackend(nil, simulated.WithBlockGasLimit(10000000))

	// Create test DB dir
	testDir := path.Join(t.TempDir(), "reorgdetectorTest_ReorgDetector.sqlite")
	reorgDetector, err := New(clientL1.Client(),
		Config{
			DBPath:              testDir,
			CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 100),
			FinalizedBlock:      aggkittypes.FinalizedBlock,
		}, L1)
	require.NoError(t, err)

	err = reorgDetector.Start(ctx)
	require.NoError(t, err)

	reorgSub, err := reorgDetector.Subscribe(subID)
	require.NoError(t, err)

	// Block 1
	header1, err := clientL1.Client().HeaderByHash(ctx, clientL1.Commit())
	require.NoError(t, err)
	require.Equal(t, uint64(1), header1.Number.Uint64())
	err = reorgDetector.AddBlockToTrack(ctx, subID, header1.Number.Uint64(), header1.Hash()) // Adding block 1
	require.NoError(t, err)

	// Block 2
	header2, err := clientL1.Client().HeaderByHash(ctx, clientL1.Commit())
	require.NoError(t, err)
	require.Equal(t, uint64(2), header2.Number.Uint64())
	err = reorgDetector.AddBlockToTrack(ctx, subID, header2.Number.Uint64(), header2.Hash()) // Adding block 1
	require.NoError(t, err)

	// Block 3
	header3Reorged, err := clientL1.Client().HeaderByHash(ctx, clientL1.Commit())
	require.NoError(t, err)
	require.Equal(t, uint64(3), header3Reorged.Number.Uint64())
	err = reorgDetector.AddBlockToTrack(ctx, subID, header3Reorged.Number.Uint64(), header3Reorged.Hash()) // Adding block 3
	require.NoError(t, err)

	// Block 4
	header4Reorged, err := clientL1.Client().HeaderByHash(ctx, clientL1.Commit())
	require.Equal(t, uint64(4), header4Reorged.Number.Uint64())
	require.NoError(t, err)
	err = reorgDetector.AddBlockToTrack(ctx, subID, header4Reorged.Number.Uint64(), header4Reorged.Hash()) // Adding block 4
	require.NoError(t, err)

	err = clientL1.Fork(header2.Hash()) // Reorg on block 2 (block 2 is still valid)
	require.NoError(t, err)

	// Make sure that the new canonical chain is longer than the previous one so the reorg is visible to the detector
	header3AfterReorg := clientL1.Commit() // Next block 3 after reorg on block 2
	require.NotEqual(t, header3Reorged.Hash(), header3AfterReorg)
	header4AfterReorg := clientL1.Commit() // Block 4
	require.NotEqual(t, header4Reorged.Hash(), header4AfterReorg)
	clientL1.Commit() // Block 5

	// Expect reorg on added blocks 3 -> all further blocks should be removed
	select {
	case firstReorgedBlock := <-reorgSub.ReorgedBlock:
		reorgSub.ReorgProcessed <- true
		require.Equal(t, header3Reorged.Number.Uint64(), firstReorgedBlock)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for reorg")
	}

	// just wait a little for completion
	time.Sleep(time.Second / 5)

	reorgDetector.trackedBlocksLock.Lock()
	headersList, ok := reorgDetector.trackedBlocks[subID]
	reorgDetector.trackedBlocksLock.Unlock()
	require.True(t, ok)
	require.Equal(t, 2, headersList.len()) // Only blocks 1 and 2 left
	actualHeader1, err := headersList.get(1)
	require.NoError(t, err)
	require.Equal(t, header1.Hash(), actualHeader1.Hash)
	actualHeader2, err := headersList.get(2)
	require.NoError(t, err)
	require.Equal(t, header2.Hash(), actualHeader2.Hash)
}

func TestGetTrackedBlocks(t *testing.T) {
	clientL1 := simulated.NewBackend(nil, simulated.WithBlockGasLimit(10000000))
	testDir := path.Join(t.TempDir(), "reorgdetector_TestGetTrackedBlocks.sqlite")
	reorgDetector, err := New(clientL1.Client(), Config{
		DBPath:              testDir,
		CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 100),
	}, L1)
	require.NoError(t, err)

	t.Run("Initial empty tracked blocks", func(t *testing.T) {
		list, err := reorgDetector.getTrackedBlocks()
		require.NoError(t, err)
		require.Empty(t, list, "Expected no tracked blocks at initialization")
	})

	t.Run("Tracked blocks for subscriber Foo", func(t *testing.T) {
		headerFoo2 := header{Num: 2, Hash: common.HexToHash("foofoo")}
		err := reorgDetector.saveTrackedBlock("Foo", headerFoo2)
		require.NoError(t, err)

		headerFoo3 := header{Num: 3, Hash: common.HexToHash("foofoofoo")}
		err = reorgDetector.saveTrackedBlock("Foo", headerFoo3)
		require.NoError(t, err)

		expectedHeadersFoo := map[uint64]header{
			2: headerFoo2,
			3: headerFoo3,
		}
		expectedList := map[string]*headersList{
			"Foo": {headers: expectedHeadersFoo},
		}

		list, err := reorgDetector.getTrackedBlocks()
		require.NoError(t, err)
		require.Equal(t, expectedList, list, "Unexpected tracked blocks for subscriber 'Foo'")
	})

	t.Run("Tracked blocks for subscribers Foo and Bar", func(t *testing.T) {
		headerBar2 := header{Num: 2, Hash: common.HexToHash("barbar")}
		err := reorgDetector.saveTrackedBlock("Bar", headerBar2)
		require.NoError(t, err)

		expectedList := map[string]*headersList{
			"Bar": {
				headers: map[uint64]header{
					2: headerBar2,
				},
			},
			"Foo": {
				headers: map[uint64]header{
					2: {Num: 2, Hash: common.HexToHash("foofoo")},
					3: {Num: 3, Hash: common.HexToHash("foofoofoo")},
				},
			},
		}

		list, err := reorgDetector.getTrackedBlocks()
		require.NoError(t, err)
		require.Equal(t, expectedList, list, "Unexpected tracked blocks after adding subscriber 'Bar'")
	})

	t.Run("Tracked blocks for subscribers Foo, Bar and Zzz", func(t *testing.T) {
		headerZzz6 := header{Num: 6, Hash: common.HexToHash("zzzzzz")}
		err := reorgDetector.saveTrackedBlock("Zzz", headerZzz6)
		require.NoError(t, err)

		expectedList := map[string]*headersList{
			"Bar": {
				headers: map[uint64]header{
					2: {Num: 2, Hash: common.HexToHash("barbar")},
				},
			},
			"Foo": {
				headers: map[uint64]header{
					2: {Num: 2, Hash: common.HexToHash("foofoo")},
					3: {Num: 3, Hash: common.HexToHash("foofoofoo")},
				},
			},
			"Zzz": {
				headers: map[uint64]header{
					6: headerZzz6,
				},
			},
		}

		list, err := reorgDetector.getTrackedBlocks()
		require.NoError(t, err)
		require.Equal(t, expectedList, list, "Unexpected tracked blocks after adding subscriber 'Zzz'")
	})

	t.Run("Load tracked headers updates subscriptions", func(t *testing.T) {
		require.NoError(t, reorgDetector.loadTrackedHeaders())
		_, ok := reorgDetector.subscriptions["Foo"]
		require.True(t, ok, "Expected subscription for 'Foo'")
		_, ok = reorgDetector.subscriptions["Bar"]
		require.True(t, ok, "Expected subscription for 'Bar'")
		_, ok = reorgDetector.subscriptions["Zzz"]
		require.True(t, ok, "Expected subscription for 'Zzz'")
	})
}

func TestNotSubscribed(t *testing.T) {
	clientL1 := simulated.NewBackend(nil, simulated.WithBlockGasLimit(10000000))
	testDir := path.Join(t.TempDir(), "reorgdetectorTestNotSubscribed.sqlite")
	reorgDetector, err := New(clientL1.Client(), Config{DBPath: testDir, CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 100)}, L1)
	require.NoError(t, err)
	err = reorgDetector.AddBlockToTrack(context.Background(), "foo", 1, common.Hash{})
	require.True(t, strings.Contains(err.Error(), "is not subscribed"))
}

func TestDetectReorgs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	syncerID := "test-syncer"
	trackedBlock := &types.Header{Number: big.NewInt(9)}

	t.Run("Block not finalized", func(t *testing.T) {
		t.Parallel()

		lastFinalizedBlock := &types.Header{Number: big.NewInt(8)}
		client := aggkittypesmocks.NewBaseEthereumClienter(t)
		client.On("HeaderByNumber", ctx, big.NewInt(int64(rpc.FinalizedBlockNumber))).Return(lastFinalizedBlock, nil)
		client.On("HeaderByNumber", ctx, trackedBlock.Number).Return(trackedBlock, nil)

		testDir := path.Join(t.TempDir(), "reorgdetectorTestDetectReorgs.sqlite")
		reorgDetector, err := New(client, Config{DBPath: testDir, CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 100)}, L1)
		require.NoError(t, err)

		_, err = reorgDetector.Subscribe(syncerID)
		require.NoError(t, err)
		require.NoError(t, reorgDetector.AddBlockToTrack(ctx, syncerID, trackedBlock.Number.Uint64(), trackedBlock.Hash()))

		require.NoError(t, reorgDetector.detectReorgInTrackedList(ctx))

		trackedBlocks, err := reorgDetector.getTrackedBlocks()
		require.NoError(t, err)
		require.Equal(t, 1, len(trackedBlocks))

		syncerTrackedBlocks, ok := trackedBlocks[syncerID]
		require.True(t, ok)
		require.Equal(t, 1, syncerTrackedBlocks.len())
	})

	t.Run("Block finalized", func(t *testing.T) {
		t.Parallel()

		lastFinalizedBlock := trackedBlock
		client := aggkittypesmocks.NewBaseEthereumClienter(t)
		client.On("HeaderByNumber", ctx, big.NewInt(int64(rpc.FinalizedBlockNumber))).Return(lastFinalizedBlock, nil)

		testDir := path.Join(t.TempDir(), "reorgdetectorTestDetectReorgs.sqlite")
		reorgDetector, err := New(client, Config{DBPath: testDir, CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 100)}, L1)
		require.NoError(t, err)

		_, err = reorgDetector.Subscribe(syncerID)
		require.NoError(t, err)
		require.NoError(t, reorgDetector.AddBlockToTrack(ctx, syncerID, trackedBlock.Number.Uint64(), trackedBlock.Hash()))

		require.NoError(t, reorgDetector.detectReorgInTrackedList(ctx))

		trackedBlocks, err := reorgDetector.getTrackedBlocks()
		require.NoError(t, err)
		require.Equal(t, 0, len(trackedBlocks))
	})

	t.Run("Reorg happened", func(t *testing.T) {
		t.Parallel()

		lastFinalizedBlock := &types.Header{Number: big.NewInt(5)}
		reorgedTrackedBlock := &types.Header{Number: trackedBlock.Number, Extra: []byte("reorged")} // Different hash

		client := aggkittypesmocks.NewBaseEthereumClienter(t)
		client.On("HeaderByNumber", ctx, big.NewInt(int64(rpc.FinalizedBlockNumber))).Return(lastFinalizedBlock, nil)
		client.On("HeaderByNumber", ctx, trackedBlock.Number).Return(reorgedTrackedBlock, nil)

		testDir := path.Join(t.TempDir(), "reorgdetectorTestDetectReorgs.sqlite")
		reorgDetector, err := New(client, Config{DBPath: testDir, CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 100)}, L1)
		require.NoError(t, err)

		subscription, err := reorgDetector.Subscribe(syncerID)
		require.NoError(t, err)

		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			<-subscription.ReorgedBlock
			subscription.ReorgProcessed <- true

			wg.Done()
		}()

		require.NoError(t, reorgDetector.AddBlockToTrack(ctx, syncerID, trackedBlock.Number.Uint64(), trackedBlock.Hash()))

		require.NoError(t, reorgDetector.detectReorgInTrackedList(ctx))

		wg.Wait() // we wait here to make sure the reorg is processed

		trackedBlocks, err := reorgDetector.getTrackedBlocks()
		require.NoError(t, err)
		require.Equal(t, 0, len(trackedBlocks)) // shouldn't be any since a reorg happened on that block
	})
}
func TestLoadTrackedHeaders(t *testing.T) {
	clientL1 := simulated.NewBackend(nil, simulated.WithBlockGasLimit(10000000))
	testDir := path.Join(t.TempDir(), "reorgdetectorTestLoadTrackedHeaders.sqlite")
	reorgDetector, err := New(clientL1.Client(), Config{
		DBPath:              testDir,
		CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 100),
	}, L1)
	require.NoError(t, err)

	// Save tracked blocks for multiple subscribers
	headerFoo := header{Num: 1, Hash: common.HexToHash("foo")}
	require.NoError(t, reorgDetector.saveTrackedBlock("Foo", headerFoo))
	headerBar := header{Num: 2, Hash: common.HexToHash("bar")}
	require.NoError(t, reorgDetector.saveTrackedBlock("Bar", headerBar))

	// Clear in-memory trackedBlocks and subscriptions to simulate fresh load
	reorgDetector.trackedBlocks = make(map[string]*headersList)
	reorgDetector.subscriptions = make(map[string]*Subscription)

	// Call loadTrackedHeaders and verify trackedBlocks and subscriptions are populated
	require.NoError(t, reorgDetector.loadTrackedHeaders())

	reorgDetector.trackedBlocksLock.RLock()
	defer reorgDetector.trackedBlocksLock.RUnlock()
	require.Contains(t, reorgDetector.trackedBlocks, "Foo")
	require.Contains(t, reorgDetector.trackedBlocks, "Bar")
	require.Equal(t, headerFoo, reorgDetector.trackedBlocks["Foo"].headers[1])
	require.Equal(t, headerBar, reorgDetector.trackedBlocks["Bar"].headers[2])

	reorgDetector.subscriptionsLock.RLock()
	defer reorgDetector.subscriptionsLock.RUnlock()
	require.Contains(t, reorgDetector.subscriptions, "Foo")
	require.Contains(t, reorgDetector.subscriptions, "Bar")
	require.NotNil(t, reorgDetector.subscriptions["Foo"].ReorgedBlock)
	require.NotNil(t, reorgDetector.subscriptions["Bar"].ReorgedBlock)
}

func TestLoadTrackedHeaders_LockSafety(t *testing.T) {
	clientL1 := simulated.NewBackend(nil, simulated.WithBlockGasLimit(10000000))
	testDir := path.Join(t.TempDir(), "reorgdetectorTestLoadTrackedHeadersLockSafety.sqlite")
	reorgDetector, err := New(clientL1.Client(), Config{
		DBPath:              testDir,
		CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 100),
	}, L1)
	require.NoError(t, err)

	// Save a tracked block
	headerFoo := header{Num: 1, Hash: common.HexToHash("foo")}
	require.NoError(t, reorgDetector.saveTrackedBlock("Foo", headerFoo))

	// Simulate concurrent access: one goroutine loads headers, another reads trackedBlocks
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		require.NoError(t, reorgDetector.loadTrackedHeaders())
	}()

	go func() {
		defer wg.Done()
		// Wait a bit to increase the chance of overlap
		time.Sleep(10 * time.Millisecond)
		reorgDetector.trackedBlocksLock.RLock()
		_ = len(reorgDetector.trackedBlocks)
		reorgDetector.trackedBlocksLock.RUnlock()
	}()

	wg.Wait()
}
