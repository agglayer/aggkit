package proof

import (
	"context"
	"testing"

	"github.com/agglayer/aggkit/autoclaim/types"
	"github.com/stretchr/testify/require"
)

// fakeProofPreparer implements types.ProofPreparer for tests.
type fakeProofPreparer struct {
	name  string
	proof *types.ClaimProof
	err   error
	calls int
}

func (f *fakeProofPreparer) PrepareProof(_ context.Context, _ types.AutoClaimRequest) (*types.ClaimProof, error) {
	f.calls++
	return f.proof, f.err
}

func TestNewSourceAwarePreparerNilArgs(t *testing.T) {
	_, err := NewSourceAwarePreparer(nil, &fakeProofPreparer{})
	require.ErrorContains(t, err, "L1 preparer is nil")

	_, err = NewSourceAwarePreparer(&fakeProofPreparer{}, nil)
	require.ErrorContains(t, err, "rollup preparer is nil")
}

func TestSourceAwarePreparerRoutesL1OriginToL1Preparer(t *testing.T) {
	l1Preparer := &fakeProofPreparer{name: "l1", proof: &types.ClaimProof{}}
	rollupPreparer := &fakeProofPreparer{name: "rollup"}

	dispatcher, err := NewSourceAwarePreparer(l1Preparer, rollupPreparer)
	require.NoError(t, err)

	request := types.AutoClaimRequest{Bridge: types.BridgeExit{SourceNetwork: types.L1OriginNetwork}}
	proof, err := dispatcher.PrepareProof(context.Background(), request)
	require.NoError(t, err)
	require.Same(t, l1Preparer.proof, proof)
	require.Equal(t, 1, l1Preparer.calls)
	require.Equal(t, 0, rollupPreparer.calls)
}

func TestSourceAwarePreparerRoutesRollupOriginToRollupPreparer(t *testing.T) {
	l1Preparer := &fakeProofPreparer{name: "l1"}
	rollupPreparer := &fakeProofPreparer{name: "rollup", proof: &types.ClaimProof{}}

	dispatcher, err := NewSourceAwarePreparer(l1Preparer, rollupPreparer)
	require.NoError(t, err)

	request := types.AutoClaimRequest{Bridge: types.BridgeExit{SourceNetwork: 7}}
	proof, err := dispatcher.PrepareProof(context.Background(), request)
	require.NoError(t, err)
	require.Same(t, rollupPreparer.proof, proof)
	require.Equal(t, 0, l1Preparer.calls)
	require.Equal(t, 1, rollupPreparer.calls)
}
