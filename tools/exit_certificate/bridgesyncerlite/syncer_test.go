package bridgesyncerlite

import (
	"context"
	"math/big"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tree"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func newTestLeaf(depositCount uint32) BridgeLeaf {
	return BridgeLeaf{
		BlockNum:           uint64(100 + depositCount),
		BlockPos:           uint64(depositCount),
		LeafType:           uint8(depositCount % 2),
		OriginNetwork:      0,
		OriginAddress:      common.BytesToAddress([]byte{byte(depositCount + 1)}),
		DestinationNetwork: 1,
		DestinationAddress: common.BytesToAddress([]byte{byte(depositCount + 2)}),
		Amount:             big.NewInt(int64(depositCount) * 1000),
		Metadata:           []byte{byte(depositCount)},
		DepositCount:       depositCount,
		TxHash:             common.BytesToHash([]byte{byte(depositCount)}),
	}
}

// TestHashMatchesBridgesync guarantees the lite leaf hash is byte-for-byte identical to the
// canonical bridgesync.Bridge.Hash, so the tree this syncer builds matches the real exit tree.
func TestHashMatchesBridgesync(t *testing.T) {
	for dc := uint32(0); dc < 5; dc++ { //nolint:intrange // uint32 counter
		leaf := newTestLeaf(dc)
		ref := bridgesync.Bridge{
			LeafType:           leaf.LeafType,
			OriginNetwork:      leaf.OriginNetwork,
			OriginAddress:      leaf.OriginAddress,
			DestinationNetwork: leaf.DestinationNetwork,
			DestinationAddress: leaf.DestinationAddress,
			Amount:             new(big.Int).Set(leaf.Amount),
			Metadata:           leaf.Metadata,
			DepositCount:       leaf.DepositCount,
		}
		require.Equal(t, ref.Hash(), leaf.Hash(), "deposit count %d", dc)
	}

	// nil amount must be treated as zero (same as bridgesync)
	leaf := newTestLeaf(0)
	leaf.Amount = nil
	ref := bridgesync.Bridge{
		LeafType:           leaf.LeafType,
		OriginNetwork:      leaf.OriginNetwork,
		OriginAddress:      leaf.OriginAddress,
		DestinationNetwork: leaf.DestinationNetwork,
		DestinationAddress: leaf.DestinationAddress,
		Amount:             nil,
		Metadata:           leaf.Metadata,
		DepositCount:       leaf.DepositCount,
	}
	require.Equal(t, ref.Hash(), leaf.Hash())
}

func newTestSyncer(t *testing.T) *BridgeSyncerLite {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "lite.sqlite")
	require.NoError(t, runMigrations(dbPath))
	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	return &BridgeSyncerLite{
		cfg:      Config{BlockChunkSize: defaultBlockChunkSize, Concurrency: defaultConcurrency},
		log:      log.WithFields("module", "bridgesyncerlite-test"),
		db:       database,
		exitTree: tree.NewAppendOnlyTree(database, ""),
	}
}

// referenceRoot builds an independent AppendOnlyTree and inserts the leaves in deposit-count order,
// returning the resulting root — the value BuildTree must reproduce.
func referenceRoot(t *testing.T, leaves []BridgeLeaf) common.Hash {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ref.sqlite")
	require.NoError(t, runMigrations(dbPath))
	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	refTree := tree.NewAppendOnlyTree(database, "")
	tx, err := db.NewTx(context.Background(), database)
	require.NoError(t, err)
	for i := uint32(0); i < uint32(len(leaves)); i++ {
		var leaf BridgeLeaf
		for _, l := range leaves {
			if l.DepositCount == i {
				leaf = l
				break
			}
		}
		_, err := refTree.PutLeaf(tx, leaf.BlockNum, leaf.BlockPos, treetypes.Leaf{
			Index: leaf.DepositCount,
			Hash:  leaf.Hash(),
		})
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	root, err := refTree.GetLastRoot(database)
	require.NoError(t, err)
	return root.Hash
}

func TestStoreBridgesAndBuildTree(t *testing.T) {
	s := newTestSyncer(t)
	ctx := context.Background()

	// empty tree → zero root
	root, err := s.LocalExitRoot()
	require.NoError(t, err)
	require.Equal(t, common.Hash{}, root)

	// store leaves out of deposit-count order, across two calls (genesis→fork + shadow-fork);
	// StoreBridges must sort them and BuildTree must assemble the whole tree once.
	leaves := []BridgeLeaf{
		newTestLeaf(2), newTestLeaf(0), newTestLeaf(4), newTestLeaf(1), newTestLeaf(3),
	}
	require.NoError(t, s.StoreBridges(ctx, leaves))

	more := []BridgeLeaf{newTestLeaf(6), newTestLeaf(5)}
	require.NoError(t, s.StoreBridges(ctx, more))

	// tree not built yet → still zero root
	root, err = s.LocalExitRoot()
	require.NoError(t, err)
	require.Equal(t, common.Hash{}, root)

	// GetBridges returns all stored leaves ordered by deposit count
	all := append(append([]BridgeLeaf{}, leaves...), more...)
	stored, err := s.GetBridges(ctx)
	require.NoError(t, err)
	require.Len(t, stored, len(all))
	for i, b := range stored {
		require.Equal(t, uint32(i), b.DepositCount)
	}

	// build the whole tree once; its root must match an independently built reference tree
	root, err = s.BuildTree(ctx)
	require.NoError(t, err)
	require.Equal(t, referenceRoot(t, all), root)

	// LocalExitRoot now reflects the built tree
	ler, err := s.LocalExitRoot()
	require.NoError(t, err)
	require.Equal(t, root, ler)
}

func TestNextDepositCount(t *testing.T) {
	s := newTestSyncer(t)
	ctx := context.Background()

	// empty DB → next deposit count is 0
	next, err := s.NextDepositCount(ctx)
	require.NoError(t, err)
	require.Equal(t, uint32(0), next)

	// store leaves 0..4 (out of order) → next is max(deposit_count)+1 = 5
	require.NoError(t, s.StoreBridges(ctx, []BridgeLeaf{
		newTestLeaf(2), newTestLeaf(0), newTestLeaf(4), newTestLeaf(1), newTestLeaf(3),
	}))
	next, err = s.NextDepositCount(ctx)
	require.NoError(t, err)
	require.Equal(t, uint32(5), next)
}

func TestBuildTreeNonContiguousFails(t *testing.T) {
	s := newTestSyncer(t)
	// missing deposit count 1 → tree build must fail with invalid index
	require.NoError(t, s.StoreBridges(context.Background(), []BridgeLeaf{newTestLeaf(0), newTestLeaf(2)}))
	_, err := s.BuildTree(context.Background())
	require.Error(t, err)
}

func TestClassifyLogsForbiddenEvents(t *testing.T) {
	for topic, name := range forbiddenEventSignatures {
		logs := []types.Log{{
			Topics:      []common.Hash{topic},
			BlockNumber: 42,
			TxHash:      common.HexToHash("0xabc"),
		}}
		_, err := classifyLogs(nil, logs, false, nil)
		require.Error(t, err, "event %s should be rejected", name)
		require.Contains(t, err.Error(), name)
	}
}

// TestClassifyLogsIgnoreUnsupported verifies that with ignoreUnsupported set, a forbidden event is
// skipped (logged as a warning) instead of aborting the classification.
func TestClassifyLogsIgnoreUnsupported(t *testing.T) {
	logger := log.WithFields("module", "bridgesyncerlite-test")
	for topic, name := range forbiddenEventSignatures {
		logs := []types.Log{{
			Topics:      []common.Hash{topic},
			BlockNumber: 42,
			TxHash:      common.HexToHash("0xabc"),
		}}
		out, err := classifyLogs(nil, logs, true, logger)
		require.NoError(t, err, "event %s should be allowed", name)
		require.Empty(t, out, "forbidden event %s must not produce a leaf", name)
	}
}

func TestClassifyLogsIgnoresUnrelated(t *testing.T) {
	// NewWrappedToken is intentionally NOT forbidden: it is neither indexed nor processed, so it must
	// be ignored like any other unrelated event rather than aborting the sync.
	newWrappedToken := crypto.Keccak256Hash([]byte("NewWrappedToken(uint32,address,address,bytes)"))
	require.NotContains(t, forbiddenEventSignatures, newWrappedToken)

	logs := []types.Log{
		{Topics: []common.Hash{newWrappedToken}},                // NewWrappedToken — ignored, not an error
		{Topics: []common.Hash{common.HexToHash("0xdeadbeef")}}, // unrelated event
		{Topics: nil}, // anonymous / no topics
	}
	out, err := classifyLogs(nil, logs, false, nil)
	require.NoError(t, err)
	require.Empty(t, out)
}
