package claimer

import (
	"context"
	"testing"

	dbtypes "github.com/agglayer/aggkit/db/types"
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

func TestLocalExitTreeDepositCount(t *testing.T) {
	t.Parallel()

	leaf := common.HexToHash("0xabc")
	lt := &LocalExitTree{depositCount: map[common.Hash]uint32{leaf: 7}}

	dc, ok := lt.DepositCount(leaf)
	require.True(t, ok)
	require.Equal(t, uint32(7), dc)

	_, ok = lt.DepositCount(common.HexToHash("0xdead"))
	require.False(t, ok)
}

func TestLocalExitTreeMetadata(t *testing.T) {
	t.Parallel()

	leaf := common.HexToHash("0xabc")
	lt := &LocalExitTree{metadata: map[common.Hash][]byte{leaf: {0x01, 0x02}}}

	m, ok := lt.Metadata(leaf)
	require.True(t, ok)
	require.Equal(t, []byte{0x01, 0x02}, m)

	_, ok = lt.Metadata(common.HexToHash("0xdead"))
	require.False(t, ok)
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
