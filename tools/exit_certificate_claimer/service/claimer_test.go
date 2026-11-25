package claimer

import (
	"context"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// fakeLocalTree is an in-memory LocalExitTreeReader: a list of leaf hashes and raw metadata indexed
// by deposit count. A leaf without metadata (nil entry beyond the slice) reports no metadata.
type fakeLocalTree struct {
	hashes   []common.Hash
	metadata [][]byte
	proof    treetypes.Proof
	proofErr error
}

func (f *fakeLocalTree) LeafCount() uint32 { return uint32(len(f.hashes)) }

func (f *fakeLocalTree) LeafHash(depositCount uint32) (common.Hash, bool) {
	if int(depositCount) >= len(f.hashes) {
		return common.Hash{}, false
	}
	return f.hashes[depositCount], true
}

func (f *fakeLocalTree) Metadata(depositCount uint32) ([]byte, bool) {
	if int(depositCount) >= len(f.metadata) {
		return nil, false
	}
	return f.metadata[depositCount], true
}

func (f *fakeLocalTree) Proof(_ context.Context, _ uint32, _ common.Hash) (treetypes.Proof, error) {
	return f.proof, f.proofErr
}

// testHistoricalLeaves is the number of leaves the fake tree holds before the certificate's exits, to
// prove the deposit count is the position in the tree and not the position in the certificate.
const testHistoricalLeaves = 5

// fakeTreeForCert builds a fake tree with testHistoricalLeaves older leaves followed by the
// certificate's exits, each with raw metadata 0xabcd.
func fakeTreeForCert(cert *Certificate) *fakeLocalTree {
	f := &fakeLocalTree{}
	for i := range testHistoricalLeaves {
		f.hashes = append(f.hashes, common.BigToHash(big.NewInt(int64(i+1))))
		f.metadata = append(f.metadata, nil)
	}
	for _, leaf := range cert.Leaves {
		f.hashes = append(f.hashes, leaf.Hash())
		f.metadata = append(f.metadata, []byte{0xab, 0xcd})
	}
	return f
}

// fakeL1 is a fake L1InfoTreeQuerier returning a fixed leaf and proof.
type fakeL1 struct {
	leaf        *l1infotreesync.L1InfoTreeLeaf
	localRoot   common.Hash
	rollupProof treetypes.Proof
}

func (f *fakeL1) GetInfoByGlobalExitRoot(_ common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error) {
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

	localTree := fakeTreeForCert(cert)
	l1 := &fakeL1{
		leaf: &l1infotreesync.L1InfoTreeLeaf{
			L1InfoTreeIndex: 9,
			MainnetExitRoot: common.HexToHash("0x1111"),
			RollupExitRoot:  common.HexToHash("0x2222"),
		},
		localRoot: settledRoot,
	}

	waitResult := &exitcertificate.StepWaitResult{
		UpdateL1InfoTree: &exitcertificate.L1InfoTreeUpdate{
			MainnetExitRoot: common.HexToHash("0x1111"),
			RollupExitRoot:  common.HexToHash("0x2222"),
		},
	}

	claimer := NewClaimer(log.GetDefaultLogger(), cert, localTree, l1, 0, waitResult)
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
	// Raw on-chain metadata from the local exit tree, not the certificate's metadata hash.
	require.Equal(t, "0xabcd", got.Metadata)
	// globalIndex for a rollup (networkID=1 → rollupIndex 0) with deposit count 5.
	require.Equal(t, "5", got.GlobalIndex)
}

func TestBuildClaimParamsNotSettled(t *testing.T) {
	t.Parallel()

	claimer, destAddr := buildTestClaimer(t, common.HexToHash("0xdeadbeef"))
	_, err := claimer.BuildClaimParams(context.Background(), destAddr, nil)
	require.ErrorIs(t, err, ErrLocalExitRootNotSettled)
}
