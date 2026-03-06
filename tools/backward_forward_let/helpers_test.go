package backward_forward_let

import (
	"context"
	"errors"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/tree"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

const (
	testOriginAddr = "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	testDestAddr   = "0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
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
// For targetIndex=2 (binary 10), only position 1 should be set (bit 1 is 1);
// position 0 is inactive (bit 0 of 2 = 0) and must be zero for the contract.
func TestComputeFrontier(t *testing.T) {
	t.Parallel()

	l0 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	l1 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000002")

	frontier, err := computeFrontier([]common.Hash{l0, l1}, 2)
	require.NoError(t, err)

	// For targetIndex=2 (binary 10): bit 0 is 0 → frontier[0] zeroed; bit 1 is 1 → frontier[1] active.
	require.Equal(t, common.Hash{}, frontier[0], "frontier[0] must be zero (inactive position for count=2)")
	require.Equal(t, crypto.Keccak256Hash(l0.Bytes(), l1.Bytes()), frontier[1])
}

// TestComputeFrontier_Empty verifies that frontier for 0 leaves is all bytes32(0).
// This matches the contract's initial _branch storage state (before any leaf insertions),
// as required by _checkValidSubtreeFrontier which rejects non-zero unused positions.
func TestComputeFrontier_Empty(t *testing.T) {
	t.Parallel()

	frontier, err := computeFrontier([]common.Hash{}, 0)
	require.NoError(t, err)

	for h := range 32 {
		require.Equal(t, common.Hash{}, frontier[h], "frontier[%d] should be bytes32(0)", h)
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

// TestBridgeExitToLeafData verifies conversion from BridgeExit to LeafData.
//
//nolint:dupl
func TestBridgeExitToLeafData(t *testing.T) {
	t.Parallel()

	originAddr := common.HexToAddress(testOriginAddr)
	destAddr := common.HexToAddress(testDestAddr)
	amount := big.NewInt(12345)

	be := &agglayertypes.BridgeExit{
		LeafType:           0,
		TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 1, OriginTokenAddress: originAddr},
		DestinationNetwork: 2,
		DestinationAddress: destAddr,
		Amount:             amount,
		Metadata:           []byte{0xde, 0xad},
	}

	ld := BridgeExitToLeafData(be)

	require.Equal(t, be.LeafType.Uint8(), ld.LeafType)
	require.Equal(t, uint32(1), ld.OriginNetwork)
	require.Equal(t, originAddr, ld.OriginAddress)
	require.Equal(t, uint32(2), ld.DestinationNetwork)
	require.Equal(t, destAddr, ld.DestinationAddress)
	require.Equal(t, amount, ld.Amount)
	require.Equal(t, []byte{0xde, 0xad}, ld.Metadata)
}

// TestBridgeExitToLeafData_NilTokenInfo verifies nil TokenInfo results in zero origin fields.
func TestBridgeExitToLeafData_NilTokenInfo(t *testing.T) {
	t.Parallel()

	be := &agglayertypes.BridgeExit{
		LeafType:           0,
		TokenInfo:          nil,
		DestinationNetwork: 3,
		DestinationAddress: common.HexToAddress("0x1234"),
		Amount:             nil,
	}

	ld := BridgeExitToLeafData(be)

	require.Equal(t, common.Address{}, ld.OriginAddress)
	require.Equal(t, uint32(0), ld.OriginNetwork)
	require.Equal(t, big.NewInt(0), ld.Amount)
}

// TestBridgeResponseToLeafData verifies conversion from BridgeResponse to LeafData.
func TestBridgeResponseToLeafData(t *testing.T) {
	t.Parallel()

	originAddr := testOriginAddr
	destAddr := testDestAddr

	br := &bridgeservicetypes.BridgeResponse{
		LeafType:           1,
		OriginNetwork:      2,
		OriginAddress:      bridgeservicetypes.Address(originAddr),
		DestinationNetwork: 3,
		DestinationAddress: bridgeservicetypes.Address(destAddr),
		Amount:             bridgeservicetypes.BigIntString("999"),
		Metadata:           "0xdeadbeef",
	}

	ld := BridgeResponseToLeafData(br)

	require.Equal(t, uint8(1), ld.LeafType)
	require.Equal(t, uint32(2), ld.OriginNetwork)
	require.Equal(t, common.HexToAddress(originAddr), ld.OriginAddress)
	require.Equal(t, uint32(3), ld.DestinationNetwork)
	require.Equal(t, common.HexToAddress(destAddr), ld.DestinationAddress)
	require.Equal(t, big.NewInt(999), ld.Amount)
	require.Equal(t, []byte{0xde, 0xad, 0xbe, 0xef}, ld.Metadata)
}

// TestParseAmount verifies all parseAmount paths.
func TestParseAmount(t *testing.T) {
	t.Parallel()

	require.Equal(t, big.NewInt(0), parseAmount(""))
	require.Equal(t, big.NewInt(42), parseAmount("42"))
	require.Equal(t, big.NewInt(0), parseAmount("not-a-number"))
}

// TestDecodeMetadata verifies all decodeMetadata paths.
func TestDecodeMetadata(t *testing.T) {
	t.Parallel()

	require.Nil(t, decodeMetadata(""))
	require.Nil(t, decodeMetadata("0x"))
	require.Equal(t, []byte{0xde, 0xad}, decodeMetadata("0xdead"))
	require.Equal(t, []byte{0xde, 0xad}, decodeMetadata("dead"))
	require.Nil(t, decodeMetadata("0xzz"))        // invalid hex
	require.Nil(t, decodeMetadata("0xzzInvalid")) // invalid hex no 0x prefix handling
}

// TestComputeLERForNewLeaves verifies LER computation after appending new leaves.
func TestComputeLERForNewLeaves(t *testing.T) {
	t.Parallel()

	l0 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	l1 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000002")
	l2 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000003")

	// Build the expected root for all 3 leaves in one pass.
	expectedRoot, err := computeRootFromFrontier([32]common.Hash{}, 0, []common.Hash{l0, l1, l2})
	require.NoError(t, err)

	// ComputeLERForNewLeaves should append l2 to existing [l0, l1].
	root, err := ComputeLERForNewLeaves([]common.Hash{l0, l1}, []common.Hash{l2})
	require.NoError(t, err)
	require.Equal(t, expectedRoot, root)
}

// TestComputeLERForNewLeaves_EmptyExisting verifies the case of no existing leaves.
func TestComputeLERForNewLeaves_EmptyExisting(t *testing.T) {
	t.Parallel()

	l0 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	expectedRoot, err := computeRootFromFrontier([32]common.Hash{}, 0, []common.Hash{l0})
	require.NoError(t, err)

	root, err := ComputeLERForNewLeaves(nil, []common.Hash{l0})
	require.NoError(t, err)
	require.Equal(t, expectedRoot, root)
}

// TestLeafDataLeafHash verifies the leaf hash computation for LeafData.
func TestLeafDataLeafHash(t *testing.T) {
	t.Parallel()

	originAddr := common.HexToAddress(testOriginAddr)
	destAddr := common.HexToAddress(testDestAddr)
	amount := big.NewInt(5000)

	be := &agglayertypes.BridgeExit{
		LeafType:           0,
		TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 1, OriginTokenAddress: originAddr},
		DestinationNetwork: 2,
		DestinationAddress: destAddr,
		Amount:             amount,
		Metadata:           nil,
	}

	ld := BridgeExitToLeafData(be)

	// leafDataLeafHash must match BridgeExitLeafHash for the same data.
	require.Equal(t, BridgeExitLeafHash(be), leafDataLeafHash(ld))
}

// TestLeafDataLeafHash_NilAmount verifies nil amount is treated as zero.
func TestLeafDataLeafHash_NilAmount(t *testing.T) {
	t.Parallel()

	ld := bridgesync.LeafData{
		LeafType:           0,
		OriginNetwork:      1,
		OriginAddress:      common.HexToAddress("0x1111"),
		DestinationNetwork: 2,
		DestinationAddress: common.HexToAddress("0x2222"),
		Amount:             nil,
		Metadata:           nil,
	}
	// Should not panic; nil amount treated as 0.
	h := leafDataLeafHash(ld)
	require.NotEqual(t, common.Hash{}, h)
}

// TestComputeBackwardLETParams_OutOfRange verifies the error when targetIndex >= len(allLeaves).
func TestComputeBackwardLETParams_OutOfRange(t *testing.T) {
	t.Parallel()

	leaves := []common.Hash{
		common.HexToHash("0x01"),
		common.HexToHash("0x02"),
	}
	_, _, _, err := ComputeBackwardLETParams(leaves, 2) //nolint:dogsled // index 2, len=2 → out of range
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}

// TestBridgeExitToContractLeaf verifies the conversion from BridgeExit to contract leaf type.
//
//nolint:dupl
func TestBridgeExitToContractLeaf(t *testing.T) {
	t.Parallel()

	originAddr := common.HexToAddress(testOriginAddr)
	destAddr := common.HexToAddress(testDestAddr)
	amount := big.NewInt(777)

	be := &agglayertypes.BridgeExit{
		LeafType:           0,
		TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 5, OriginTokenAddress: originAddr},
		DestinationNetwork: 6,
		DestinationAddress: destAddr,
		Amount:             amount,
		Metadata:           []byte{0x01, 0x02},
	}

	leaf := bridgeExitToContractLeaf(be)

	require.Equal(t, be.LeafType.Uint8(), leaf.LeafType)
	require.Equal(t, uint32(5), leaf.OriginNetwork)
	require.Equal(t, originAddr, leaf.OriginAddress)
	require.Equal(t, uint32(6), leaf.DestinationNetwork)
	require.Equal(t, destAddr, leaf.DestinationAddress)
	require.Equal(t, amount, leaf.Amount)
	require.Equal(t, []byte{0x01, 0x02}, leaf.Metadata)
}

// TestBridgeExitToContractLeaf_NilTokenInfo verifies nil TokenInfo results in zero origin fields.
func TestBridgeExitToContractLeaf_NilTokenInfo(t *testing.T) {
	t.Parallel()

	be := &agglayertypes.BridgeExit{
		LeafType:           0,
		TokenInfo:          nil,
		DestinationNetwork: 3,
		DestinationAddress: common.HexToAddress("0x1234"),
		Amount:             nil,
	}

	leaf := bridgeExitToContractLeaf(be)

	require.Equal(t, common.Address{}, leaf.OriginAddress)
	require.Equal(t, uint32(0), leaf.OriginNetwork)
	require.Equal(t, big.NewInt(0), leaf.Amount)
}

// TestLeafDataToContractLeaf verifies the conversion from LeafData to contract leaf type.
func TestLeafDataToContractLeaf(t *testing.T) {
	t.Parallel()

	originAddr := common.HexToAddress("0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC")
	destAddr := common.HexToAddress("0xDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD")
	amount := big.NewInt(333)

	ld := bridgesync.LeafData{
		LeafType:           1,
		OriginNetwork:      7,
		OriginAddress:      originAddr,
		DestinationNetwork: 8,
		DestinationAddress: destAddr,
		Amount:             amount,
		Metadata:           []byte{0xAB, 0xCD},
	}

	leaf := leafDataToContractLeaf(ld)

	require.Equal(t, uint8(1), leaf.LeafType)
	require.Equal(t, uint32(7), leaf.OriginNetwork)
	require.Equal(t, originAddr, leaf.OriginAddress)
	require.Equal(t, uint32(8), leaf.DestinationNetwork)
	require.Equal(t, destAddr, leaf.DestinationAddress)
	require.Equal(t, amount, leaf.Amount)
	require.Equal(t, []byte{0xAB, 0xCD}, leaf.Metadata)
}

// TestLeafDataToContractLeaf_NilAmount verifies nil Amount is coerced to 0.
func TestLeafDataToContractLeaf_NilAmount(t *testing.T) {
	t.Parallel()

	ld := bridgesync.LeafData{
		LeafType:           0,
		OriginNetwork:      0,
		DestinationNetwork: 1,
		Amount:             nil,
	}

	leaf := leafDataToContractLeaf(ld)
	require.Equal(t, big.NewInt(0), leaf.Amount)
}

// TestComputeLERForNewLeaves_FrontierError verifies the frontier error path.
// computeFrontier returns an error when existingLeafHashes has fewer entries
// than targetIndex (i.e. existingCount). We craft that by passing mismatched slices.
// Actually computeLERForNewLeaves uses len(existingLeafHashes) as targetIndex,
// so to trigger a frontier error we need computeFrontier to fail, which requires
// len(existing) < targetIndex. Since targetIndex = len(existing), the only way to
// fail is if the frontier itself is inconsistent - but that's impossible here.
// Instead, test the empty-new-leaves error path from computeRootFromFrontier.
func TestComputeLERForNewLeaves_EmptyNewLeaves(t *testing.T) {
	t.Parallel()

	// ComputeLERForNewLeaves passes newLeafHashes directly to computeRootFromFrontier,
	// which returns an error if it's empty.
	_, err := ComputeLERForNewLeaves(nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must not be empty")
}

// TestFetchL2LeafHashesUpTo_HappyPath verifies fetching leaf hashes from the bridge service.
func TestFetchL2LeafHashesUpTo_HappyPath(t *testing.T) {
	t.Parallel()

	originAddr := testOriginAddr
	destAddr := testDestAddr

	br0 := &bridgeservicetypes.BridgeResponse{
		LeafType:           0,
		OriginNetwork:      1,
		OriginAddress:      bridgeservicetypes.Address(originAddr),
		DestinationNetwork: 2,
		DestinationAddress: bridgeservicetypes.Address(destAddr),
		Amount:             bridgeservicetypes.BigIntString("1000"),
	}
	br1 := &bridgeservicetypes.BridgeResponse{
		LeafType:           0,
		OriginNetwork:      1,
		OriginAddress:      bridgeservicetypes.Address(originAddr),
		DestinationNetwork: 3,
		DestinationAddress: bridgeservicetypes.Address(destAddr),
		Amount:             bridgeservicetypes.BigIntString("2000"),
	}

	env := &Env{
		BridgeService: &stubBridgeService{
			bridges: map[uint32]*bridgeservicetypes.BridgeResponse{
				0: br0,
				1: br1,
			},
		},
		L2NetworkID: 1,
	}

	hashes, err := fetchL2LeafHashesUpTo(context.Background(), env, 2)
	require.NoError(t, err)
	require.Len(t, hashes, 2)
	require.Equal(t, BridgeResponseLeafHash(br0), hashes[0])
	require.Equal(t, BridgeResponseLeafHash(br1), hashes[1])
}

// TestFetchL2LeafHashesUpTo_Error verifies an error from the bridge service is propagated.
func TestFetchL2LeafHashesUpTo_Error(t *testing.T) {
	t.Parallel()

	env := &Env{
		BridgeService: &stubBridgeService{
			errAtDC: map[uint32]error{0: errors.New("service unavailable")},
		},
		L2NetworkID: 1,
	}

	_, err := fetchL2LeafHashesUpTo(context.Background(), env, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "DC=0")
}
