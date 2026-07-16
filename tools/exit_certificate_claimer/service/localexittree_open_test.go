package claimer

import (
	"context"
	"math/big"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// buildLocalExitTreeDB creates a DB-only lite bridge syncer at dbPath, persists the given bridges and
// builds the exit tree, returning the resulting local exit root. It mirrors what exit_certificate
// Step G2 leaves on disk for the claimer to open.
func buildLocalExitTreeDB(t *testing.T, dbPath string, bridges []bridgesyncerlite.BridgeLeaf) common.Hash {
	t.Helper()
	ctx := context.Background()
	syncer, err := bridgesyncerlite.New(ctx, bridgesyncerlite.Config{DBPath: dbPath}, log.GetDefaultLogger())
	require.NoError(t, err)
	require.NoError(t, syncer.StoreBridges(ctx, bridges))
	root, err := syncer.BuildTree(ctx)
	require.NoError(t, err)
	require.NoError(t, syncer.Close())
	return root
}

func sampleBridges() []bridgesyncerlite.BridgeLeaf {
	return []bridgesyncerlite.BridgeLeaf{
		{
			BlockNum: 1, BlockPos: 0, LeafType: 0, OriginNetwork: 0,
			OriginAddress: common.Address{}, DestinationNetwork: 1,
			DestinationAddress: common.HexToAddress("0xaaaa"), Amount: big.NewInt(100),
			Metadata: []byte{0x01, 0x02}, DepositCount: 0,
		},
		{
			BlockNum: 2, BlockPos: 0, LeafType: 0, OriginNetwork: 0,
			OriginAddress: common.HexToAddress("0xbbbb"), DestinationNetwork: 1,
			DestinationAddress: common.HexToAddress("0xcccc"), Amount: big.NewInt(200),
			Metadata: nil, DepositCount: 1,
		},
	}
}

func TestOpenLocalExitTree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "step-g-l2bridgesyncerlite.sqlite")
	bridges := sampleBridges()
	root := buildLocalExitTreeDB(t, dbPath, bridges)

	lt, err := OpenLocalExitTree(ctx, dbPath, log.GetDefaultLogger())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lt.Close()) })

	// Each persisted bridge is indexed by its leaf hash → deposit count and raw metadata.
	for _, b := range bridges {
		bb := b
		dc, ok := lt.DepositCount(bb.Hash())
		require.True(t, ok)
		require.Equal(t, bb.DepositCount, dc)

		meta, ok := lt.Metadata(bb.Hash())
		require.True(t, ok)
		require.Equal(t, bb.Metadata, meta)
	}

	// A proof for deposit count 0 against the built local exit root resolves.
	proof, err := lt.Proof(ctx, 0, root)
	require.NoError(t, err)
	require.NotEqual(t, common.Hash{}, proof[0])
}

func TestOpenLocalExitTreeMissingDB(t *testing.T) {
	t.Parallel()
	// A path under a non-existent directory cannot be opened/migrated.
	_, err := OpenLocalExitTree(
		context.Background(),
		filepath.Join(t.TempDir(), "nope", "missing.sqlite"),
		log.GetDefaultLogger(),
	)
	require.Error(t, err)
}
