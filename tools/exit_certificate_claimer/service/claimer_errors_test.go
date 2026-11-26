package claimer

import (
	"context"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// cfgL1 is a configurable L1InfoTreeQuerier whose methods can be made to error or return canned data.
type cfgL1 struct {
	leaf           *l1infotreesync.L1InfoTreeLeaf
	localRoot      common.Hash
	localRootErr   error
	rollupProof    treetypes.Proof
	rollupProofErr error
}

func (f *cfgL1) GetInfoByGlobalExitRoot(common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error) {
	return f.leaf, nil
}

func (f *cfgL1) GetLocalExitRoot(context.Context, uint32, common.Hash) (common.Hash, error) {
	return f.localRoot, f.localRootErr
}

func (f *cfgL1) GetRollupExitTreeMerkleProof(context.Context, uint32, common.Hash) (treetypes.Proof, error) {
	return f.rollupProof, f.rollupProofErr
}

func cfgClaimer(t *testing.T, l1 L1InfoTreeQuerier, tree LocalExitTreeReader) (*Claimer, common.Address) {
	t.Helper()
	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)
	waitResult := &exitcertificate.StepWaitResult{
		UpdateL1InfoTree: &exitcertificate.L1InfoTreeUpdate{
			MainnetExitRoot: common.HexToHash("0x1111"),
			RollupExitRoot:  common.HexToHash("0x2222"),
		},
	}
	return NewClaimer(log.GetDefaultLogger(), cert, tree, l1, 0, waitResult), cert.Leaves[0].DestinationAddress
}

func TestBuildClaimParamsGetLocalExitRootError(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	l1 := &cfgL1{leaf: &l1infotreesync.L1InfoTreeLeaf{}, localRootErr: boom}
	c, dest := cfgClaimer(t, l1, &fakeLocalTree{})
	_, err := c.BuildClaimParamsV2(context.Background(), dest, nil)
	require.ErrorIs(t, err, boom)
}

func TestBuildClaimParamsRollupProofError(t *testing.T) {
	t.Parallel()
	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)
	boom := errors.New("rollup boom")
	// localRoot must match the certificate so the settlement check passes, then rollup proof errors.
	l1 := &cfgL1{leaf: &l1infotreesync.L1InfoTreeLeaf{}, localRoot: cert.NewLocalExitRoot, rollupProofErr: boom}
	c, dest := cfgClaimer(t, l1, &fakeLocalTree{})
	_, err = c.BuildClaimParamsV2(context.Background(), dest, nil)
	require.ErrorIs(t, err, boom)
}

func TestBuildClaimParamsTreeShorterThanCertificate(t *testing.T) {
	t.Parallel()
	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)
	l1 := &cfgL1{leaf: &l1infotreesync.L1InfoTreeLeaf{}, localRoot: cert.NewLocalExitRoot}
	// Empty local tree → the certificate's exits cannot be its last leaves.
	c, dest := cfgClaimer(t, l1, &fakeLocalTree{})
	_, err = c.BuildClaimParamsV2(context.Background(), dest, nil)
	require.ErrorContains(t, err, "the local exit tree has only 0 leaves")
}

func TestBuildClaimParamsLeafMismatch(t *testing.T) {
	t.Parallel()
	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)
	l1 := &cfgL1{leaf: &l1infotreesync.L1InfoTreeLeaf{}, localRoot: cert.NewLocalExitRoot}
	// The leaf at the exit's position is another one: the hash cross-check must refuse it.
	tree := fakeTreeForCert(cert)
	tree.hashes[testHistoricalLeaves] = common.HexToHash("0xbad")
	c, dest := cfgClaimer(t, l1, tree)
	_, err = c.BuildClaimParamsV2(context.Background(), dest, nil)
	require.ErrorContains(t, err, "does not match the local exit tree leaf at deposit count 5")
	require.ErrorContains(t, c.Check(context.Background()), "does not match the local exit tree leaf")
}

func TestBuildClaimParamsMetadataMissing(t *testing.T) {
	t.Parallel()
	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)
	l1 := &cfgL1{leaf: &l1infotreesync.L1InfoTreeLeaf{}, localRoot: cert.NewLocalExitRoot}
	// Leaf present but metadata absent.
	tree := fakeTreeForCert(cert)
	tree.metadata = nil
	c, dest := cfgClaimer(t, l1, tree)
	_, err = c.BuildClaimParamsV2(context.Background(), dest, nil)
	require.ErrorContains(t, err, "no metadata in local exit tree")
}

func TestBuildClaimParamsProofError(t *testing.T) {
	t.Parallel()
	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)
	boom := errors.New("proof boom")
	l1 := &cfgL1{leaf: &l1infotreesync.L1InfoTreeLeaf{}, localRoot: cert.NewLocalExitRoot}
	tree := fakeTreeForCert(cert)
	tree.proofErr = boom
	c, dest := cfgClaimer(t, l1, tree)
	_, err = c.BuildClaimParamsV2(context.Background(), dest, nil)
	require.ErrorIs(t, err, boom)
}
