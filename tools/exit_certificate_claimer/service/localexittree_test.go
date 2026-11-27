package claimer

import (
	"context"
	"testing"

	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// stubReadTreer is a minimal treetypes.ReadTreer that returns a canned proof, used to exercise
// LocalExitTree.Proof without a real SQLite-backed tree.
type stubReadTreer struct {
	proof treetypes.Proof
}

func (s stubReadTreer) GetProof(_ context.Context, _ uint32, _ common.Hash) (treetypes.Proof, error) {
	return s.proof, nil
}

func (s stubReadTreer) GetRootByIndex(_ context.Context, _ uint32) (treetypes.Root, error) {
	return treetypes.Root{}, nil
}

func (s stubReadTreer) GetRootByHash(_ context.Context, _ common.Hash) (*treetypes.Root, error) {
	return nil, nil
}

func (s stubReadTreer) GetLastRoot(_ dbtypes.Querier) (treetypes.Root, error) {
	return treetypes.Root{}, nil
}

func (s stubReadTreer) GetLeaf(_ dbtypes.Querier, _ uint32, _ common.Hash) (common.Hash, error) {
	return common.Hash{}, nil
}

func TestLocalExitTreeLeaves(t *testing.T) {
	t.Parallel()

	leaf := common.HexToHash("0xabc")
	// Two leaves with the same hash keep their own position.
	lt := &LocalExitTree{leaves: []localLeaf{{hash: leaf, metadata: []byte{0x01}}, {hash: leaf, metadata: []byte{0x02}}}}
	require.Equal(t, uint32(2), lt.LeafCount())

	for dc, wantMetadata := range [][]byte{{0x01}, {0x02}} {
		h, ok := lt.LeafHash(uint32(dc))
		require.True(t, ok)
		require.Equal(t, leaf, h)
		m, ok := lt.Metadata(uint32(dc))
		require.True(t, ok)
		require.Equal(t, wantMetadata, m)
	}

	_, ok := lt.LeafHash(2)
	require.False(t, ok)
	_, ok = lt.Metadata(2)
	require.False(t, ok)
}

func TestIndexLeaves(t *testing.T) {
	t.Parallel()

	// Unordered input is indexed by deposit count.
	leaves, err := indexLeaves([]bridgesyncerlite.BridgeLeaf{
		{DepositCount: 1, Metadata: []byte{0x01}}, {DepositCount: 0, Metadata: []byte{0x00}},
	})
	require.NoError(t, err)
	require.Equal(t, []byte{0x00}, leaves[0].metadata)
	require.Equal(t, []byte{0x01}, leaves[1].metadata)

	_, err = indexLeaves([]bridgesyncerlite.BridgeLeaf{{DepositCount: 0}, {DepositCount: 2}})
	require.ErrorContains(t, err, "out of range")

	_, err = indexLeaves([]bridgesyncerlite.BridgeLeaf{{DepositCount: 0}, {DepositCount: 0}})
	require.ErrorContains(t, err, "appears twice")
}

func TestLocalExitTreeProof(t *testing.T) {
	t.Parallel()

	var want treetypes.Proof
	want[0] = common.HexToHash("0x99")
	lt := &LocalExitTree{tree: stubReadTreer{proof: want}}

	got, err := lt.Proof(context.Background(), 0, common.Hash{})
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestLocalExitTreeCloseNilDB(t *testing.T) {
	t.Parallel()

	lt := &LocalExitTree{}
	require.NoError(t, lt.Close())
}
