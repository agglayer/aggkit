package finality

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	finalitymocks "github.com/agglayer/aggkit/bridgetracker/finality/mocks"
	commonmocks "github.com/agglayer/aggkit/common/mocks"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	testNetwork  = uint32(1)
	testBlockNum = uint64(100)
)

var (
	testTx  = common.HexToHash("0x01")
	testTx2 = common.HexToHash("0x02")

	errRPC = errors.New("rpc is down")
)

func boolPtr(b bool) *bool { return &b }

// clientsFor returns a ClientResolver resolving every network to client
func clientsFor(t *testing.T, client aggkittypes.BaseEthereumClienter) *finalitymocks.ClientResolver {
	t.Helper()
	clients := finalitymocks.NewClientResolver(t)
	clients.EXPECT().RPCClientFor(mock.Anything, mock.Anything).Return(client, nil).Maybe()
	return clients
}

// failingClients returns a ClientResolver that fails to resolve any network with err
func failingClients(t *testing.T, err error) *finalitymocks.ClientResolver {
	t.Helper()
	clients := finalitymocks.NewClientResolver(t)
	clients.EXPECT().RPCClientFor(mock.Anything, mock.Anything).Return(nil, err)
	return clients
}

// fakeClock is a manually advanced clock
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type testEnv struct {
	client   *mocks.BaseEthereumClienter
	resolver *RPCResolver
	clock    *fakeClock
}

// newTestEnv builds a resolver over a strict mock client (any call that is not expected fails the
// test) where every network uses finalizedBlock
func newTestEnv(t *testing.T, finalizedBlock aggkittypes.BlockNumberFinality) *testEnv {
	t.Helper()
	return newTestEnvWith(t, Config{
		FinalizedBlock: func(uint32) aggkittypes.BlockNumberFinality { return finalizedBlock },
	})
}

func newTestEnvWith(t *testing.T, cfg Config) *testEnv {
	t.Helper()
	client := mocks.NewBaseEthereumClienter(t)
	resolver, err := NewRPCResolver(clientsFor(t, client), cfg)
	require.NoError(t, err)
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	resolver.now = clock.Now
	return &testEnv{client: client, resolver: resolver, clock: clock}
}

// expectHead makes the finalized head of the network head
func (e *testEnv) expectHead(head uint64) *mocks.BaseEthereumClienter_CustomHeaderByNumber_Call {
	return e.client.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
		Return(&aggkittypes.BlockHeader{Number: head}, nil)
}

// expectReceipt makes the receipt of tx be found, successful, in block
func (e *testEnv) expectReceipt(tx common.Hash, block uint64) *mocks.BaseEthereumClienter_TransactionReceipt_Call {
	return e.client.EXPECT().TransactionReceipt(mock.Anything, tx).
		Return(&gethtypes.Receipt{
			TxHash: tx, BlockNumber: new(big.Int).SetUint64(block), Status: gethtypes.ReceiptStatusSuccessful,
		}, nil)
}

func (e *testEnv) isFinal(tx common.Hash, block uint64, reported *bool) (bool, error) {
	return e.resolver.IsFinal(context.Background(), testNetwork, tx, block, reported)
}

func TestNewRPCResolver_RequiresDependencies(t *testing.T) {
	cfg := Config{FinalizedBlock: func(uint32) aggkittypes.BlockNumberFinality { return aggkittypes.FinalizedBlock }}

	_, err := NewRPCResolver(nil, cfg)
	require.ErrorContains(t, err, "client resolver is required")

	_, err = NewRPCResolver(finalitymocks.NewClientResolver(t), Config{})
	require.ErrorContains(t, err, "FinalizedBlock is required")

	resolver, err := NewRPCResolver(finalitymocks.NewClientResolver(t), cfg)
	require.NoError(t, err)
	require.Equal(t, DefaultHeadTTL, resolver.headTTL)
	require.Equal(t, DefaultCacheSize, resolver.final.limit)
}

func TestIsFinal_ExplicitValueWinsAndNeverUsesTheFallback(t *testing.T) {
	// the mock client fails the test on any call: neither the head nor the receipt may be fetched
	t.Run("true", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		final, err := e.isFinal(testTx, testBlockNum, boolPtr(true))
		require.NoError(t, err)
		require.True(t, final)
	})

	t.Run("false, even though the receipt would prove it final", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		// the fallback would resolve it to final, but it must not be asked
		e.expectHead(testBlockNum).Maybe()
		e.expectReceipt(testTx, testBlockNum).Maybe()

		final, err := e.isFinal(testTx, testBlockNum, boolPtr(false))
		require.NoError(t, err)
		require.False(t, final)
		e.client.AssertNotCalled(t, "CustomHeaderByNumber", mock.Anything, mock.Anything)
		e.client.AssertNotCalled(t, "TransactionReceipt", mock.Anything, mock.Anything)
	})
}

func TestIsFinal_OmittedResolvedByTheReceiptFallback(t *testing.T) {
	e := newTestEnv(t, aggkittypes.FinalizedBlock)
	e.expectHead(testBlockNum).Once()
	e.expectReceipt(testTx, testBlockNum).Once()

	final, err := e.isFinal(testTx, testBlockNum, nil)
	require.NoError(t, err)
	require.True(t, final)

	// one RPC call per entry, once: the answer is cached (the mock expects each call only once)
	final, err = e.isFinal(testTx, testBlockNum, nil)
	require.NoError(t, err)
	require.True(t, final)
}

func TestIsFinal_OmittedBlockAtTheFinalizedHeadIsFinal(t *testing.T) {
	e := newTestEnv(t, aggkittypes.FinalizedBlock)
	e.expectHead(testBlockNum).Once() // the block is the finalized head itself
	e.expectReceipt(testTx, testBlockNum).Once()

	final, err := e.isFinal(testTx, testBlockNum, nil)
	require.NoError(t, err)
	require.True(t, final)
}

func TestIsFinal_OmittedNotResolvedByTheFallback(t *testing.T) {
	t.Run("block above the finalized head: not final, and the receipt is not even fetched", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		e.expectHead(testBlockNum - 1).Once()

		final, err := e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.False(t, final)
	})

	t.Run("receipt not found: not final and not an error", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		e.expectHead(testBlockNum + 10).Once()
		e.client.EXPECT().TransactionReceipt(mock.Anything, testTx).Return(nil, ethereum.NotFound).Once()

		final, err := e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.False(t, final)
	})

	t.Run("receipt in another block than block_num: not final", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		// both blocks are finalized: only the tx's position tells the stored block was replaced
		e.expectHead(testBlockNum + 10).Once()
		e.expectReceipt(testTx, testBlockNum+1).Once()

		final, err := e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.False(t, final)
	})

	t.Run("receipt reverted in block_num (re-mined by a reorg): not final and not cached", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		e.expectHead(testBlockNum + 10).Once()
		e.client.EXPECT().TransactionReceipt(mock.Anything, testTx).Return(&gethtypes.Receipt{
			TxHash: testTx, BlockNumber: new(big.Int).SetUint64(testBlockNum), Status: gethtypes.ReceiptStatusFailed,
		}, nil).Twice()

		final, err := e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.False(t, final)

		// not cached as final: the receipt is asked again (the mock expects exactly two calls)
		final, err = e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.False(t, final)
	})

	t.Run("receipt without block number: not final", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		e.expectHead(testBlockNum + 10).Once()
		e.client.EXPECT().TransactionReceipt(mock.Anything, testTx).Return(&gethtypes.Receipt{}, nil).Once()

		final, err := e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.False(t, final)
	})

	t.Run("a not final answer is not cached: the entry is final once its block is finalized", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		e.expectHead(testBlockNum - 1).Once()
		final, err := e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.False(t, final)

		// the head is cached for HeadTTL: still not final, with no RPC call
		final, err = e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.False(t, final)

		e.clock.Advance(DefaultHeadTTL)
		e.expectHead(testBlockNum).Once()
		e.expectReceipt(testTx, testBlockNum).Once()
		final, err = e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.True(t, final)
	})
}

func TestIsFinal_RPCErrorsStayNotFinal(t *testing.T) {
	t.Run("receipt error", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		e.expectHead(testBlockNum).Once()
		e.client.EXPECT().TransactionReceipt(mock.Anything, testTx).Return(nil, errRPC).Once()

		final, err := e.isFinal(testTx, testBlockNum, nil)
		require.ErrorIs(t, err, errRPC)
		require.False(t, final, "the answer to use is false whenever there is an error")

		// nothing was cached: the next call retries and resolves
		e.expectReceipt(testTx, testBlockNum).Once()
		final, err = e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.True(t, final)
	})

	t.Run("finalized head error", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		e.client.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).Return(nil, errRPC).Once()

		final, err := e.isFinal(testTx, testBlockNum, nil)
		require.ErrorIs(t, err, errRPC)
		require.False(t, final)

		// a failed fetch is not cached either
		e.expectHead(testBlockNum).Once()
		e.expectReceipt(testTx, testBlockNum).Once()
		final, err = e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.True(t, final)
	})

	t.Run("no JSON-RPC client for the network", func(t *testing.T) {
		resolver, err := NewRPCResolver(failingClients(t, errRPC), Config{
			FinalizedBlock: func(uint32) aggkittypes.BlockNumberFinality { return aggkittypes.FinalizedBlock },
		})
		require.NoError(t, err)

		final, err := resolver.IsFinal(context.Background(), testNetwork, testTx, testBlockNum, nil)
		require.ErrorIs(t, err, errRPC)
		require.False(t, final)
	})

	t.Run("an explicit value never needs the RPC", func(t *testing.T) {
		resolver, err := NewRPCResolver(finalitymocks.NewClientResolver(t), Config{
			FinalizedBlock: func(uint32) aggkittypes.BlockNumberFinality { return aggkittypes.FinalizedBlock },
		})
		require.NoError(t, err)

		final, err := resolver.IsFinal(context.Background(), testNetwork, testTx, testBlockNum, boolPtr(true))
		require.NoError(t, err)
		require.True(t, final)
	})

	t.Run("cancelled context while waiting for the head", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		e.client.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
			RunAndReturn(func(context.Context, *aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, error) {
				defer close(done)
				close(started)
				<-release
				return &aggkittypes.BlockHeader{Number: testBlockNum}, nil
			}).Once()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		final, err := e.resolver.IsFinal(ctx, testNetwork, testTx, testBlockNum, nil)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, final)

		// the shared fetch is not tied to the cancelled caller: it completes and is cached
		<-started
		close(release)
		<-done
		require.Eventually(t, func() bool {
			_, cached := e.resolver.cachedHead(testNetwork)
			return cached
		}, time.Second, time.Millisecond)
	})
}

func TestIsFinal_Monotonic(t *testing.T) {
	t.Run("seen final through the bridge service, later omitted", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock) // any RPC call fails the test
		final, err := e.isFinal(testTx, testBlockNum, boolPtr(true))
		require.NoError(t, err)
		require.True(t, final)

		// another bridge-service instance omits it: still final, and the fallback is not used
		final, err = e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.True(t, final)
	})

	t.Run("seen final through the bridge service, later reported false", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		_, err := e.isFinal(testTx, testBlockNum, boolPtr(true))
		require.NoError(t, err)

		// e.g. an instance that is lagging behind
		final, err := e.isFinal(testTx, testBlockNum, boolPtr(false))
		require.NoError(t, err)
		require.True(t, final)
	})

	t.Run("a false after a true is logged as a warning, an omitted one is not", func(t *testing.T) {
		logger := commonmocks.NewLogger(t)
		e := newTestEnvWith(t, Config{
			FinalizedBlock: func(uint32) aggkittypes.BlockNumberFinality { return aggkittypes.FinalizedBlock },
			Logger:         logger,
		})
		_, err := e.isFinal(testTx, testBlockNum, boolPtr(true))
		require.NoError(t, err)

		// omitted: still final, nothing logged (any Warnf call fails the test)
		final, err := e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.True(t, final)

		logger.EXPECT().Warnf(mock.Anything, testTx, testBlockNum, testNetwork).Once()
		final, err = e.isFinal(testTx, testBlockNum, boolPtr(false))
		require.NoError(t, err)
		require.True(t, final)
	})

	t.Run("seen final through the fallback, later reported false", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		e.expectHead(testBlockNum).Once()
		e.expectReceipt(testTx, testBlockNum).Once()
		final, err := e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.True(t, final)

		final, err = e.isFinal(testTx, testBlockNum, boolPtr(false))
		require.NoError(t, err)
		require.True(t, final)
	})

	t.Run("false is not remembered", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		final, err := e.isFinal(testTx, testBlockNum, boolPtr(false))
		require.NoError(t, err)
		require.False(t, final)

		final, err = e.isFinal(testTx, testBlockNum, boolPtr(true))
		require.NoError(t, err)
		require.True(t, final)
	})

	t.Run("the cache is per network, tx and block", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		_, err := e.isFinal(testTx, testBlockNum, boolPtr(true))
		require.NoError(t, err)

		// same tx in another block (a reorg moved it): not final just because of the old position
		e.expectHead(testBlockNum - 1).Times(2) // once per network
		final, err := e.isFinal(testTx, testBlockNum+1, nil)
		require.NoError(t, err)
		require.False(t, final)

		// another tx in the same block
		final, err = e.isFinal(testTx2, testBlockNum, nil)
		require.NoError(t, err)
		require.False(t, final)

		// same tx and block on another network
		final, err = e.resolver.IsFinal(context.Background(), testNetwork+1, testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.False(t, final)
	})
}

func TestIsFinal_FinalizedHeadCache(t *testing.T) {
	t.Run("one fetch for many entries of a network, refreshed once the TTL expires", func(t *testing.T) {
		e := newTestEnvWith(t, Config{
			FinalizedBlock: func(uint32) aggkittypes.BlockNumberFinality { return aggkittypes.FinalizedBlock },
			HeadTTL:        3 * time.Second,
		})
		e.expectHead(testBlockNum - 1).Once()
		for i := range uint64(10) {
			final, err := e.isFinal(common.BigToHash(new(big.Int).SetUint64(i)), testBlockNum+i, nil)
			require.NoError(t, err)
			require.False(t, final)
		}

		e.clock.Advance(2 * time.Second)
		final, err := e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.False(t, final, "still cached 2s later")

		e.clock.Advance(time.Second)
		e.expectHead(testBlockNum).Once()
		e.expectReceipt(testTx, testBlockNum).Once()
		final, err = e.isFinal(testTx, testBlockNum, nil)
		require.NoError(t, err)
		require.True(t, final, "refreshed once the TTL expired")
	})

	t.Run("one cached head per network", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		e.expectHead(testBlockNum - 1).Times(2)

		for _, networkID := range []uint32{1, 2, 1, 2} {
			final, err := e.resolver.IsFinal(context.Background(), networkID, testTx, testBlockNum, nil)
			require.NoError(t, err)
			require.False(t, final)
		}
	})

	t.Run("concurrent callers share one fetch", func(t *testing.T) {
		e := newTestEnv(t, aggkittypes.FinalizedBlock)
		e.client.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
			RunAndReturn(func(context.Context, *aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, error) {
				time.Sleep(50 * time.Millisecond)
				return &aggkittypes.BlockHeader{Number: testBlockNum - 1}, nil
			}).Once()

		const callers = 20
		type result struct {
			final bool
			err   error
		}
		results := make(chan result, callers)
		var wg sync.WaitGroup
		for i := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				final, err := e.isFinal(common.BigToHash(big.NewInt(int64(i))), testBlockNum, nil)
				results <- result{final: final, err: err}
			}()
		}
		wg.Wait()
		close(results)
		for r := range results {
			require.NoError(t, r.err)
			require.False(t, r.final)
		}
	})
}

func TestIsFinal_ConfiguredFinalizedBlock(t *testing.T) {
	latestMinus5 := aggkittypes.BlockNumberFinality{Block: aggkittypes.Latest, Offset: -5}
	byNetwork := map[uint32]aggkittypes.BlockNumberFinality{
		1: aggkittypes.FinalizedBlock,
		2: aggkittypes.SafeBlock,
		3: latestMinus5,
		4: {}, // empty: the finalized tag
	}
	client := mocks.NewBaseEthereumClienter(t)
	resolver, err := NewRPCResolver(clientsFor(t, client), Config{
		FinalizedBlock: func(networkID uint32) aggkittypes.BlockNumberFinality { return byNetwork[networkID] },
	})
	require.NoError(t, err)

	for networkID, want := range map[uint32]aggkittypes.BlockNumberFinality{
		1: aggkittypes.FinalizedBlock,
		2: aggkittypes.SafeBlock,
		3: latestMinus5,
		4: aggkittypes.FinalizedBlock,
	} {
		client.EXPECT().CustomHeaderByNumber(mock.Anything, mock.MatchedBy(func(f *aggkittypes.BlockNumberFinality) bool {
			return f != nil && f.Equal(want)
		})).Return(&aggkittypes.BlockHeader{Number: testBlockNum - 1}, nil).Once()

		final, err := resolver.IsFinal(context.Background(), networkID, testTx, testBlockNum, nil)
		require.NoError(t, err, fmt.Sprintf("network %d", networkID))
		require.False(t, final)
	}
}

func TestIsFinal_BoundedCache(t *testing.T) {
	e := newTestEnvWith(t, Config{
		FinalizedBlock: func(uint32) aggkittypes.BlockNumberFinality { return aggkittypes.FinalizedBlock },
		CacheSize:      2,
	})
	tx := func(i int64) common.Hash { return common.BigToHash(big.NewInt(i)) }

	for i := int64(1); i <= 2; i++ {
		_, err := e.isFinal(tx(i), testBlockNum, boolPtr(true))
		require.NoError(t, err)
	}
	// touching 1 makes 2 the least recently used one
	final, err := e.isFinal(tx(1), testBlockNum, nil)
	require.NoError(t, err)
	require.True(t, final)

	_, err = e.isFinal(tx(3), testBlockNum, boolPtr(true))
	require.NoError(t, err)
	require.Equal(t, 2, e.resolver.final.len(), "the cache never grows beyond its size")

	final, err = e.isFinal(tx(1), testBlockNum, nil)
	require.NoError(t, err)
	require.True(t, final, "1 was kept")
	final, err = e.isFinal(tx(3), testBlockNum, nil)
	require.NoError(t, err)
	require.True(t, final, "3 was kept")

	// 2 was evicted: it is only a cache miss, it falls back to the receipt like any other entry
	e.expectHead(testBlockNum).Once()
	e.expectReceipt(tx(2), testBlockNum).Once()
	final, err = e.isFinal(tx(2), testBlockNum, nil)
	require.NoError(t, err)
	require.True(t, final)
}
