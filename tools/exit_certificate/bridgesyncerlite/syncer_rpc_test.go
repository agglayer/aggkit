package bridgesyncerlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

func tempDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "lite.sqlite")
}

func TestNewRequiresRPCorDB(t *testing.T) {
	_, err := New(context.Background(), Config{}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least one of RPCURL or DBPath")
}

func TestNewDBOnly(t *testing.T) {
	s, err := New(context.Background(), Config{DBPath: tempDBPath(t)}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	// defaults applied
	require.Equal(t, defaultBlockChunkSize, s.cfg.BlockChunkSize)
	require.Equal(t, defaultConcurrency, s.cfg.Concurrency)
	// DB-backed, no RPC client
	require.NotNil(t, s.db)
	require.Nil(t, s.client)
	require.NotNil(t, s.exitTree)

	// RPC-only operations must fail without a client
	_, err = s.LatestBlock(context.Background())
	require.Error(t, err)
	_, err = s.FetchBridges(context.Background(), 0, 10)
	require.Error(t, err)
}

func TestNewFullMode(t *testing.T) {
	srv := newRPCServer(t, func() uint64 { return 0 }, func() []types.Log { return nil })
	s, err := New(context.Background(), Config{
		DBPath:         tempDBPath(t),
		RPCURL:         srv.URL,
		BridgeAddr:     common.HexToAddress("0xbeef"),
		BlockChunkSize: 5,
		Concurrency:    2,
	}, log.WithFields("module", "test"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	require.NotNil(t, s.db)
	require.NotNil(t, s.client)
	require.NotNil(t, s.contract)
	require.NotNil(t, s.exitTree)
}

func TestNewDialErrorClosesDB(t *testing.T) {
	// An unsupported URL scheme makes ethclient.DialContext fail; with DBPath set this also exercises
	// the database-cleanup branch in New.
	_, err := New(context.Background(), Config{
		DBPath: tempDBPath(t),
		RPCURL: "invalid-scheme://nowhere",
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "dial RPC")
}

func TestCloseDBOnly(t *testing.T) {
	s, err := New(context.Background(), Config{DBPath: tempDBPath(t)}, nil)
	require.NoError(t, err)
	require.NoError(t, s.Close())
}

func TestCloseNoResources(t *testing.T) {
	s := &BridgeSyncerLite{log: log.WithFields("module", "test")}
	require.NoError(t, s.Close())
}

func TestLatestBlock(t *testing.T) {
	srv := newRPCServer(t, func() uint64 { return 12345 }, func() []types.Log { return nil })
	s, err := New(context.Background(), Config{RPCURL: srv.URL, BridgeAddr: common.HexToAddress("0xbeef")}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	bn, err := s.LatestBlock(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(12345), bn)
}

func TestLatestBlockNoClient(t *testing.T) {
	s := &BridgeSyncerLite{log: log.WithFields("module", "test")}
	_, err := s.LatestBlock(context.Background())
	require.Error(t, err)
}

// TestFetchBridgesFromServer drives fetchBridges → fetchWindow → classifyLogs → parseBridgeEvent
// across multiple windows against the fake JSON-RPC server, and verifies FetchBridges returns the
// leaves sorted by deposit count.
func TestFetchBridgesFromServer(t *testing.T) {
	want := []BridgeLeaf{newTestLeaf(2), newTestLeaf(0), newTestLeaf(1)}
	logs := make([]types.Log, len(want))
	for i, l := range want {
		logs[i] = packBridgeEventLog(t, l)
	}
	srv := newRPCServer(t, func() uint64 { return 100 }, func() []types.Log { return logs })

	s, err := New(context.Background(), Config{
		RPCURL:         srv.URL,
		BridgeAddr:     common.HexToAddress("0xbeef"),
		BlockChunkSize: 10,
		Concurrency:    3,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	// range spanning several windows so the parallel window loop runs
	got, err := s.FetchBridges(context.Background(), 0, 100)
	require.NoError(t, err)
	require.NotEmpty(t, got)
	// sorted by deposit count
	for i := 1; i < len(got); i++ {
		require.LessOrEqual(t, got[i-1].DepositCount, got[i].DepositCount)
	}
}

func TestFetchBridgesNoClient(t *testing.T) {
	s := &BridgeSyncerLite{
		log: log.WithFields("module", "test"),
		cfg: Config{BlockChunkSize: defaultBlockChunkSize, Concurrency: defaultConcurrency},
	}
	_, err := s.FetchBridges(context.Background(), 0, 10)
	require.Error(t, err)
	require.Contains(t, err.Error(), "RPC-backed")
}

func TestFetchBridgesInvalidRange(t *testing.T) {
	srv := newRPCServer(t, func() uint64 { return 0 }, func() []types.Log { return nil })
	s, err := New(context.Background(), Config{RPCURL: srv.URL, BridgeAddr: common.HexToAddress("0xbeef")}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	_, err = s.FetchBridges(context.Background(), 10, 5)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid block range")
}

// TestSyncAndAddBlocks drives Sync and AddBlocks end-to-end: fetch from the fake server, persist to
// the DB, then build the tree and check the root is non-zero.
func TestSyncAndAddBlocks(t *testing.T) {
	first := []BridgeLeaf{newTestLeaf(0), newTestLeaf(1)}
	second := []BridgeLeaf{newTestLeaf(2), newTestLeaf(3)}
	phase := 0
	srv := newRPCServer(t, func() uint64 { return 100 }, func() []types.Log {
		var src []BridgeLeaf
		if phase == 0 {
			src = first
		} else {
			src = second
		}
		logs := make([]types.Log, len(src))
		for i, l := range src {
			logs[i] = packBridgeEventLog(t, l)
		}
		return logs
	})

	s, err := New(context.Background(), Config{
		DBPath:         tempDBPath(t),
		RPCURL:         srv.URL,
		BridgeAddr:     common.HexToAddress("0xbeef"),
		BlockChunkSize: 100,
		Concurrency:    1,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	ctx := context.Background()
	require.NoError(t, s.Sync(ctx, 0, 50))
	phase = 1
	require.NoError(t, s.AddBlocks(ctx, 51, 100))

	count, err := s.CountBridges(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, count)

	root, err := s.BuildTree(ctx)
	require.NoError(t, err)
	require.NotEqual(t, common.Hash{}, root)
}

func TestSyncNoClient(t *testing.T) {
	s := &BridgeSyncerLite{
		log: log.WithFields("module", "test"),
		cfg: Config{BlockChunkSize: defaultBlockChunkSize, Concurrency: defaultConcurrency},
	}
	require.Error(t, s.Sync(context.Background(), 0, 10))
	require.Error(t, s.AddBlocks(context.Background(), 0, 10))
}

// --- DB-less error branches ----------------------------------------------------------------------

func TestDBOperationsRequireDB(t *testing.T) {
	s := &BridgeSyncerLite{log: log.WithFields("module", "test")}
	ctx := context.Background()

	require.Error(t, s.StoreBridges(ctx, []BridgeLeaf{newTestLeaf(0)}))
	_, err := s.BuildTree(ctx)
	require.Error(t, err)
	_, err = s.LocalExitRoot()
	require.Error(t, err)
	_, err = s.CountBridges(ctx)
	require.Error(t, err)
	_, err = s.NextDepositCount(ctx)
	require.Error(t, err)
	_, err = s.GetBridges(ctx)
	require.Error(t, err)
}

func TestStoreBridgesEmpty(t *testing.T) {
	s := newTestSyncer(t)
	require.NoError(t, s.StoreBridges(context.Background(), nil))
	count, err := s.CountBridges(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestBuildTreeEmpty(t *testing.T) {
	s := newTestSyncer(t)
	root, err := s.BuildTree(context.Background())
	require.NoError(t, err)
	require.Equal(t, common.Hash{}, root)
}
