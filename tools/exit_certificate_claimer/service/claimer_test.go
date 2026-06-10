package claimer

import (
	"context"
	"testing"

	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// fakeLocalTree is an in-memory LocalExitTreeReader: it maps the certificate leaves to deposit
// counts by their position in the certificate.
type fakeLocalTree struct {
	depositByHash map[common.Hash]uint32
	proof         treetypes.Proof
}

func (f *fakeLocalTree) DepositCount(leafHash common.Hash) (uint32, bool) {
	dc, ok := f.depositByHash[leafHash]
	return dc, ok
}

func (f *fakeLocalTree) Proof(_ context.Context, _ uint32, _ common.Hash) (treetypes.Proof, error) {
	return f.proof, nil
}

// fakeL1 is a fake L1InfoTreeQuerier returning a fixed leaf and proof.
type fakeL1 struct {
	leaf        *l1infotreesync.L1InfoTreeLeaf
	localRoot   common.Hash
	rollupProof treetypes.Proof
}

func (f *fakeL1) GetLatestL1InfoLeaf(_ context.Context) (*l1infotreesync.L1InfoTreeLeaf, error) {
	return f.leaf, nil
}

func (f *fakeL1) GetLocalExitRoot(_ context.Context, _ uint32, _ common.Hash) (common.Hash, error) {
	return f.localRoot, nil
}

func (f *fakeL1) GetRollupExitTreeMerkleProof(
	_ context.Context, _ uint32, _ common.Hash,
) (treetypes.Proof, error) {
	return f.rollupProof, nil
}

func buildTestClaimer(t *testing.T, settledRoot common.Hash) (*Claimer, common.Address) {
	t.Helper()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)

	depositByHash := make(map[common.Hash]uint32, len(cert.Leaves))
	for i, leaf := range cert.Leaves {
		depositByHash[leaf.Hash()] = uint32(i + 5) // offset to prove the count is carried through
	}

	localTree := &fakeLocalTree{depositByHash: depositByHash}
	l1 := &fakeL1{
		leaf: &l1infotreesync.L1InfoTreeLeaf{
			L1InfoTreeIndex: 9,
			MainnetExitRoot: common.HexToHash("0x1111"),
			RollupExitRoot:  common.HexToHash("0x2222"),
		},
		localRoot: settledRoot,
	}

	claimer := NewClaimer(log.GetDefaultLogger(), cert, localTree, l1, 0, nil)
	return claimer, cert.Leaves[0].DestinationAddress
}

func TestListBridges(t *testing.T) {
	t.Parallel()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)
	destAddr := cert.Leaves[0].DestinationAddress

	claimer, _ := buildTestClaimer(t, cert.NewLocalExitRoot)
	bridges, err := claimer.ListBridges(destAddr)
	require.NoError(t, err)
	require.Len(t, bridges, 1)
	require.Equal(t, destAddr.Hex(), bridges[0].DestinationAddress)
	require.Equal(t, uint32(5), bridges[0].DepositCount)
}

func TestBuildClaimParams(t *testing.T) {
	t.Parallel()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)
	destAddr := cert.Leaves[0].DestinationAddress

	claimer, _ := buildTestClaimer(t, cert.NewLocalExitRoot)
	claims, err := claimer.BuildClaimParams(context.Background(), destAddr, nil)
	require.NoError(t, err)
	require.Len(t, claims, 1)

	got := claims[0]
	require.Equal(t, uint32(1), claimer.NetworkID()) // defaulted from certificate
	require.Equal(t, uint32(5), got.DepositCount)
	require.Equal(t, uint32(9), got.L1InfoTreeIndex)
	require.Equal(t, common.HexToHash("0x1111").Hex(), got.MainnetExitRoot)
	require.Equal(t, common.HexToHash("0x2222").Hex(), got.RollupExitRoot)
	require.Equal(t, "20000005400000000", got.Amount)
	// globalIndex for a rollup (networkID=1 → rollupIndex 0) with deposit count 5.
	require.Equal(t, "5", got.GlobalIndex)
}

func TestBuildClaimParamsNotSettled(t *testing.T) {
	t.Parallel()

	claimer, destAddr := buildTestClaimer(t, common.HexToHash("0xdeadbeef"))
	_, err := claimer.BuildClaimParams(context.Background(), destAddr, nil)
	require.ErrorIs(t, err, ErrLocalExitRootNotSettled)
}
