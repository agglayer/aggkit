package backward_forward_let

import (
	"testing"

	"github.com/agglayer/aggkit/tree"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// TestMakeZeroHashes verifies the zero hash generation.
func TestMakeZeroHashes(t *testing.T) {
	t.Parallel()

	zeros := makeZeroHashes()
	require.Len(t, zeros, 33)

	// Index 0 must be the empty hash.
	require.Equal(t, common.Hash{}, zeros[0])

	// Each subsequent entry is keccak256(prev, prev).
	for i := 1; i <= 32; i++ {
		expected := crypto.Keccak256Hash(zeros[i-1].Bytes(), zeros[i-1].Bytes())
		require.Equal(t, expected, zeros[i], "mismatch at index %d", i)
	}
}

// TestComputeFrontier verifies the frontier after inserting 2 known leaves.
func TestComputeFrontier(t *testing.T) {
	t.Parallel()

	l0 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	l1 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000002")

	frontier, err := computeFrontier([]common.Hash{l0, l1}, 2)
	require.NoError(t, err)

	// After inserting l0 (index 0, all left children) and l1 (index 1):
	// frontier[0] = l0  (set by leaf 0's left-child at h=0; not changed by leaf 1's right-child at h=0)
	// frontier[1] = hash(l0, l1) (set by leaf 1's left-child at h=1)
	require.Equal(t, l0, frontier[0])
	require.Equal(t, crypto.Keccak256Hash(l0.Bytes(), l1.Bytes()), frontier[1])
}

// TestComputeFrontier_Empty verifies that frontier for 0 leaves is the zero-hashes frontier.
func TestComputeFrontier_Empty(t *testing.T) {
	t.Parallel()

	frontier, err := computeFrontier([]common.Hash{}, 0)
	require.NoError(t, err)

	zeros := makeZeroHashes()
	for h := range 32 {
		require.Equal(t, zeros[h], frontier[h], "frontier[%d] should be zero hash", h)
	}
}

// TestComputeFrontier_ErrorInsufficientLeaves verifies the error when targetIndex > len(hashes).
func TestComputeFrontier_ErrorInsufficientLeaves(t *testing.T) {
	t.Parallel()

	_, err := computeFrontier([]common.Hash{{}}, 5)
	require.Error(t, err)
	require.Contains(t, err.Error(), "insufficient leaf hashes")
}

// TestComputeMerkleProof verifies that the proof satisfies tree.CalculateRoot.
func TestComputeMerkleProof(t *testing.T) {
	t.Parallel()

	l0 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	l1 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000002")
	l2 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000003")
	leaves := []common.Hash{l0, l1, l2}

	// Compute the expected root using computeRootFromFrontier from scratch.
	expectedRoot, err := computeRootFromFrontier([32]common.Hash{}, 0, leaves)
	require.NoError(t, err)

	for idx := uint32(0); idx < uint32(len(leaves)); idx++ {
		proof, err := computeMerkleProof(leaves, idx)
		require.NoError(t, err)

		// tree.CalculateRoot expects [32]common.Hash (which is types.Proof).
		got := tree.CalculateRoot(leaves[idx], proof, idx)
		require.Equal(t, expectedRoot, got, "proof for index %d failed to reproduce root", idx)
	}
}

// TestComputeMerkleProof_TwoLeaves verifies proof for a 2-leaf tree.
func TestComputeMerkleProof_TwoLeaves(t *testing.T) {
	t.Parallel()

	l0 := common.HexToHash("0xAAAA000000000000000000000000000000000000000000000000000000000000")
	l1 := common.HexToHash("0xBBBB000000000000000000000000000000000000000000000000000000000000")
	leaves := []common.Hash{l0, l1}

	expectedRoot, err := computeRootFromFrontier([32]common.Hash{}, 0, leaves)
	require.NoError(t, err)

	for idx := range uint32(2) {
		proof, err := computeMerkleProof(leaves, idx)
		require.NoError(t, err)
		got := tree.CalculateRoot(leaves[idx], proof, idx)
		require.Equal(t, expectedRoot, got, "proof for index %d failed", idx)
	}
}

// TestComputeMerkleProof_ErrorOutOfRange verifies the error when targetIndex >= len(leaves).
func TestComputeMerkleProof_ErrorOutOfRange(t *testing.T) {
	t.Parallel()

	_, err := computeMerkleProof([]common.Hash{{}}, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}

// TestComputeBackwardLETParams verifies the end-to-end parameter computation.
// It checks that CalculateRoot(nextLeaf, proof, targetIndex) == root of the full tree.
func TestComputeBackwardLETParams(t *testing.T) {
	t.Parallel()

	l0 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000011")
	l1 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000022")
	l2 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000033")
	l3 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000044")
	allLeaves := []common.Hash{l0, l1, l2, l3}

	// Test rolling back to DivergencePoint=2 (leaves 0 and 1 remain, leaf 2 is nextLeaf).
	targetIndex := uint32(2)
	frontier, nextLeaf, proof, err := ComputeBackwardLETParams(allLeaves, targetIndex)
	require.NoError(t, err)

	// nextLeaf must be the leaf at targetIndex.
	require.Equal(t, l2, nextLeaf)

	// CalculateRoot(nextLeaf, proof, targetIndex) must equal the root of allLeaves.
	expectedRoot, err := computeRootFromFrontier([32]common.Hash{}, 0, allLeaves)
	require.NoError(t, err)

	got := tree.CalculateRoot(nextLeaf, proof, targetIndex)
	require.Equal(t, expectedRoot, got, "proof does not reproduce the tree root")

	// Frontier should match computeFrontier for the same targetIndex.
	expectedFrontier, err := computeFrontier(allLeaves, targetIndex)
	require.NoError(t, err)
	require.Equal(t, expectedFrontier, frontier)
}

// TestComputeRootFromFrontier_Incremental verifies that building the tree in two steps
// (frontier then continuation) yields the same root as a single-pass computation.
func TestComputeRootFromFrontier_Incremental(t *testing.T) {
	t.Parallel()

	l0 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	l1 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000002")
	l2 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000003")
	allLeaves := []common.Hash{l0, l1, l2}

	// Single-pass root.
	singlePassRoot, err := computeRootFromFrontier([32]common.Hash{}, 0, allLeaves)
	require.NoError(t, err)

	// Two-step: compute frontier for first 2 leaves, then continue with the 3rd.
	frontier, err := computeFrontier(allLeaves, 2)
	require.NoError(t, err)

	twoStepRoot, err := computeRootFromFrontier(frontier, 2, []common.Hash{l2})
	require.NoError(t, err)

	require.Equal(t, singlePassRoot, twoStepRoot,
		"incremental root must equal single-pass root")
}

// TestComputeRootFromFrontier_ErrorEmpty verifies the error for an empty newLeafHashes slice.
func TestComputeRootFromFrontier_ErrorEmpty(t *testing.T) {
	t.Parallel()

	_, err := computeRootFromFrontier([32]common.Hash{}, 0, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must not be empty")
}
