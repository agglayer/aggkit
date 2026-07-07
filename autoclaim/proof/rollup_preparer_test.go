package proof

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	"github.com/agglayer/aggkit/tree"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const (
	testSourceNetwork = uint32(1)
	testDepositCount  = uint32(7)
	testVerifyBlock   = uint64(50)
	testFinalIndex    = uint32(10)
)

// rollupProofScenario holds a self-consistent set of proofs and roots for a rollup-origin claim.
type rollupProofScenario struct {
	request        autoclaimtypes.AutoClaimRequest
	leaf           *l1infotreesync.L1InfoTreeLeaf
	actualLER      common.Hash
	proofLocal     treetypes.Proof
	proofRollupExt treetypes.Proof
}

// newRollupProofScenario builds a consistent bridge leaf -> LER -> rollup-exit-root chain so that both
// tree.VerifyProof checks in the preparer pass. leafBlock controls the chosen leaf's block number.
func newRollupProofScenario(destNetwork uint32, leafBlock uint64) rollupProofScenario {
	bridge := testRollupBridgeExit(destNetwork)
	bridgeLeafHash := bridgeExitLeafHash(bridge)

	proofLocal := testProof("0x11", "0x22", "0x33")
	actualLER := tree.CalculateRoot(bridgeLeafHash, proofLocal, testDepositCount)

	proofRollupExt := testProof("0xaa", "0xbb")
	rollupExitRoot := tree.CalculateRoot(actualLER, proofRollupExt, testSourceNetwork-1)

	leaf := &l1infotreesync.L1InfoTreeLeaf{
		BlockNumber:     leafBlock,
		L1InfoTreeIndex: testFinalIndex,
		MainnetExitRoot: common.HexToHash("0xbeef01"),
		RollupExitRoot:  rollupExitRoot,
		GlobalExitRoot:  common.HexToHash("0xbeef02"),
		Hash:            common.HexToHash("0xbeef03"),
	}

	request := autoclaimtypes.AutoClaimRequest{
		Bridge:         bridge,
		LER:            actualLER,
		LeafProof:      proofLocal,
		VerifyBlockNum: testVerifyBlock,
	}

	return rollupProofScenario{
		request:        request,
		leaf:           leaf,
		actualLER:      actualLER,
		proofLocal:     proofLocal,
		proofRollupExt: proofRollupExt,
	}
}

func testRollupBridgeExit(destNetwork uint32) autoclaimtypes.BridgeExit {
	return autoclaimtypes.BridgeExit{
		SourceNetwork:      testSourceNetwork,
		BlockNum:           5,
		LeafType:           bridgesynctypes.LeafTypeAsset,
		OriginNetwork:      testSourceNetwork,
		OriginAddress:      common.HexToAddress("0x1234"),
		DestinationNetwork: destNetwork,
		DestinationAddress: common.HexToAddress("0x5678"),
		Amount:             big.NewInt(1000),
		Metadata:           []byte("meta"),
		DepositCount:       testDepositCount,
	}
}

// fakeRollupL1InfoTree implements RollupL1InfoTreeSyncer for tests.
type fakeRollupL1InfoTree struct {
	firstAfterBlock    map[uint64]*l1infotreesync.L1InfoTreeLeaf
	firstAfterBlockErr error
	infoByIndex        map[uint32]*l1infotreesync.L1InfoTreeLeaf
	infoByIndexErr     error
	localExitRoot      map[common.Hash]common.Hash
	localExitRootErr   map[common.Hash]error
	rollupProof        map[common.Hash]treetypes.Proof
	rollupProofErr     error

	localExitRootCalls []rollupLERCall
	rollupProofCalls   []rollupProofCall
}

type rollupLERCall struct {
	networkID uint32
	root      common.Hash
}

func (f *fakeRollupL1InfoTree) GetInfoByIndex(
	_ context.Context, index uint32,
) (*l1infotreesync.L1InfoTreeLeaf, error) {
	if f.infoByIndexErr != nil {
		return nil, f.infoByIndexErr
	}
	info, ok := f.infoByIndex[index]
	if !ok {
		return nil, db.ErrNotFound
	}
	return info, nil
}

func (f *fakeRollupL1InfoTree) GetFirstInfoAfterBlock(blockNum uint64) (*l1infotreesync.L1InfoTreeLeaf, error) {
	if f.firstAfterBlockErr != nil {
		return nil, f.firstAfterBlockErr
	}
	info, ok := f.firstAfterBlock[blockNum]
	if !ok {
		return nil, db.ErrNotFound
	}
	return info, nil
}

func (f *fakeRollupL1InfoTree) GetLocalExitRoot(
	_ context.Context, networkID uint32, rollupExitRoot common.Hash,
) (common.Hash, error) {
	f.localExitRootCalls = append(f.localExitRootCalls, rollupLERCall{networkID: networkID, root: rollupExitRoot})
	if f.localExitRootErr != nil {
		if err, ok := f.localExitRootErr[rollupExitRoot]; ok {
			return common.Hash{}, err
		}
	}
	ler, ok := f.localExitRoot[rollupExitRoot]
	if !ok {
		return common.Hash{}, db.ErrNotFound
	}
	return ler, nil
}

func (f *fakeRollupL1InfoTree) GetRollupExitTreeMerkleProof(
	_ context.Context, networkID uint32, root common.Hash,
) (treetypes.Proof, error) {
	f.rollupProofCalls = append(f.rollupProofCalls, rollupProofCall{networkID: networkID, root: root})
	if f.rollupProofErr != nil {
		return treetypes.Proof{}, f.rollupProofErr
	}
	proof, ok := f.rollupProof[root]
	if !ok {
		return treetypes.Proof{}, errors.New("rollup proof not found")
	}
	return proof, nil
}

var _ RollupL1InfoTreeSyncer = (*fakeRollupL1InfoTree)(nil)

// fakeRefresher implements LeafProofRefresher for tests.
type fakeRefresher struct {
	proof treetypes.Proof
	err   error
	calls []refreshCall
}

type refreshCall struct {
	sourceNetwork uint32
	leafIndex     uint32
	depositCount  uint32
}

func (f *fakeRefresher) RefreshLeafProof(
	_ context.Context, sourceNetwork, leafIndex, depositCount uint32,
) (treetypes.Proof, error) {
	f.calls = append(f.calls, refreshCall{
		sourceNetwork: sourceNetwork,
		leafIndex:     leafIndex,
		depositCount:  depositCount,
	})
	return f.proof, f.err
}

var _ LeafProofRefresher = (*fakeRefresher)(nil)

// newExactMatchL1InfoTree wires a fake tree so that the first leaf after the verify block is the
// scenario leaf and its LER matches the stored LER (exact-match, no staleness).
func newExactMatchL1InfoTree(s rollupProofScenario) *fakeRollupL1InfoTree {
	return &fakeRollupL1InfoTree{
		firstAfterBlock: map[uint64]*l1infotreesync.L1InfoTreeLeaf{
			testVerifyBlock: s.leaf,
		},
		infoByIndex: map[uint32]*l1infotreesync.L1InfoTreeLeaf{
			testFinalIndex: s.leaf,
		},
		localExitRoot: map[common.Hash]common.Hash{
			s.leaf.RollupExitRoot: s.actualLER,
		},
		rollupProof: map[common.Hash]treetypes.Proof{
			s.leaf.RollupExitRoot: s.proofRollupExt,
		},
	}
}

func TestRollupPrepareNilL1InfoTreeError(t *testing.T) {
	preparer := &RollupPreparer{}
	_, err := preparer.Prepare(context.Background(), newRollupProofScenario(1101, 60).request)
	require.ErrorContains(t, err, "L1 info tree syncer is not available")
}

func TestRollupPrepareRejectsL1SourceNetwork(t *testing.T) {
	s := newRollupProofScenario(1101, 60)
	s.request.Bridge.SourceNetwork = autoclaimtypes.L1OriginNetwork

	preparer := NewRollupPreparer(newExactMatchL1InfoTree(s), nil, nil)
	_, err := preparer.Prepare(context.Background(), s.request)
	require.ErrorContains(t, err, "rollup source network")
}

func TestRollupPrepareReadyL2Destination(t *testing.T) {
	ctx := context.Background()
	s := newRollupProofScenario(1101, 60)
	preparedAt := time.Unix(200, 0).UTC()

	l1InfoTree := newExactMatchL1InfoTree(s)
	gerSyncer := &fakeL2GERSyncer{
		info: l2gersync.GlobalExitRootInfo{L1InfoTreeIndex: testFinalIndex},
	}
	refresher := &fakeRefresher{}

	preparer := NewRollupPreparer(l1InfoTree, gerSyncer, refresher)
	preparer.now = func() time.Time { return preparedAt }

	result, err := preparer.Prepare(ctx, s.request)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Ready)
	require.NotNil(t, result.Proof)

	proof := result.Proof
	require.Equal(t, testFinalIndex, proof.L1InfoTreeIndex)
	require.Equal(t, s.leaf, proof.L1InfoTreeLeaf)
	require.Equal(t, s.leaf.MainnetExitRoot, proof.MainnetExitRoot)
	require.Equal(t, s.leaf.RollupExitRoot, proof.RollupExitRoot)
	require.Equal(t, s.leaf.GlobalExitRoot, proof.GlobalExitRoot)
	require.Equal(t, s.proofLocal, proof.ProofLocalExitRoot)
	require.Equal(t, s.proofRollupExt, proof.ProofRollupExitRoot)
	require.Equal(t, autoclaimtypes.ProofToABIProof(s.proofLocal), proof.ABILocalExitRoot)
	require.Equal(t, autoclaimtypes.ProofToABIProof(s.proofRollupExt), proof.ABIRollupExitRoot)
	require.Equal(t, preparedAt, proof.PreparedAt)

	// Exact match: no staleness refresh performed.
	require.Empty(t, refresher.calls)
	// Rollup exit proof requested for the source network at the leaf's rollup exit root.
	require.Equal(t, []rollupProofCall{{networkID: testSourceNetwork, root: s.leaf.RollupExitRoot}},
		l1InfoTree.rollupProofCalls)
}

func TestRollupPrepareReadyL1Destination(t *testing.T) {
	ctx := context.Background()
	// L1 destination (network 0): constructed with a nil gerSyncer.
	s := newRollupProofScenario(autoclaimtypes.L1OriginNetwork, 60)

	l1InfoTree := newExactMatchL1InfoTree(s)
	preparer := NewRollupPreparer(l1InfoTree, nil, &fakeRefresher{})

	result, err := preparer.Prepare(ctx, s.request)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Ready)
	require.NotNil(t, result.Proof)
	require.Equal(t, testFinalIndex, result.Proof.L1InfoTreeIndex)
	require.Equal(t, s.proofLocal, result.Proof.ProofLocalExitRoot)
	require.Equal(t, s.proofRollupExt, result.Proof.ProofRollupExitRoot)
}

func TestRollupPrepareNotReadyNoGERInjected(t *testing.T) {
	ctx := context.Background()
	s := newRollupProofScenario(1101, 60)

	l1InfoTree := newExactMatchL1InfoTree(s)
	gerSyncer := &fakeL2GERSyncer{err: db.ErrNotFound}

	preparer := NewRollupPreparer(l1InfoTree, gerSyncer, &fakeRefresher{})
	result, err := preparer.Prepare(ctx, s.request)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Ready)
	require.Nil(t, result.Proof)

	// PrepareProof surface returns (nil, nil) for a not-ready request.
	proof, err := preparer.PrepareProof(ctx, s.request)
	require.NoError(t, err)
	require.Nil(t, proof)
}

func TestRollupPrepareNotReadyLeafNotSynced(t *testing.T) {
	ctx := context.Background()
	s := newRollupProofScenario(autoclaimtypes.L1OriginNetwork, 60)

	// No leaf synced at/after the verify block.
	l1InfoTree := &fakeRollupL1InfoTree{
		firstAfterBlock: map[uint64]*l1infotreesync.L1InfoTreeLeaf{},
	}

	preparer := NewRollupPreparer(l1InfoTree, nil, &fakeRefresher{})
	result, err := preparer.Prepare(ctx, s.request)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Ready)
	require.Nil(t, result.Proof)
}

func TestRollupPrepareGERSyncerErrorPropagated(t *testing.T) {
	ctx := context.Background()
	s := newRollupProofScenario(1101, 60)

	l1InfoTree := newExactMatchL1InfoTree(s)
	syncerErr := errors.New("rpc failure")
	preparer := NewRollupPreparer(l1InfoTree, &fakeL2GERSyncer{err: syncerErr}, &fakeRefresher{})

	result, err := preparer.Prepare(ctx, s.request)
	require.Nil(t, result)
	require.ErrorContains(t, err, "get first injected GER after index")
	require.ErrorContains(t, err, syncerErr.Error())
}

func TestRollupPrepareStalenessRefreshesAndUsesProof(t *testing.T) {
	ctx := context.Background()
	// Leaf strictly after the verify block, LER differs from the stored (superseded) LER.
	s := newRollupProofScenario(1101, testVerifyBlock+10)
	staleLER := common.HexToHash("0xdeadbeef")
	staleProof := testProof("0xdead")
	s.request.LER = staleLER
	s.request.LeafProof = staleProof

	l1InfoTree := newExactMatchL1InfoTree(s)
	gerSyncer := &fakeL2GERSyncer{
		info: l2gersync.GlobalExitRootInfo{L1InfoTreeIndex: testFinalIndex},
	}
	// The refresher returns the correct proof against the actual (newer) LER.
	refresher := &fakeRefresher{proof: s.proofLocal}

	preparer := NewRollupPreparer(l1InfoTree, gerSyncer, refresher)
	result, err := preparer.Prepare(ctx, s.request)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Ready)
	require.NotNil(t, result.Proof)

	// The refreshed proof (not the stale stored one) is used, so it is persisted with the claim proof.
	require.Equal(t, s.proofLocal, result.Proof.ProofLocalExitRoot)
	require.NotEqual(t, staleProof, result.Proof.ProofLocalExitRoot)
	require.Equal(t, []refreshCall{{
		sourceNetwork: testSourceNetwork,
		leafIndex:     testFinalIndex,
		depositCount:  testDepositCount,
	}}, refresher.calls)
}

func TestRollupPrepareStalenessRefreshFailureIsNotReady(t *testing.T) {
	ctx := context.Background()
	s := newRollupProofScenario(1101, testVerifyBlock+10)
	s.request.LER = common.HexToHash("0xdeadbeef")
	s.request.LeafProof = testProof("0xdead")

	l1InfoTree := newExactMatchL1InfoTree(s)
	gerSyncer := &fakeL2GERSyncer{
		info: l2gersync.GlobalExitRootInfo{L1InfoTreeIndex: testFinalIndex},
	}
	refresher := &fakeRefresher{err: errors.New("bridge service unavailable")}

	preparer := NewRollupPreparer(l1InfoTree, gerSyncer, refresher)
	result, err := preparer.Prepare(ctx, s.request)
	// Transient refresh failure is treated as not-ready (retry next cycle), not a hard error.
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Ready)
	require.Nil(t, result.Proof)
	require.Len(t, refresher.calls, 1)
}

func TestRollupPrepareStalenessNoRefresherConfiguredIsError(t *testing.T) {
	ctx := context.Background()
	s := newRollupProofScenario(1101, testVerifyBlock+10)
	s.request.LER = common.HexToHash("0xdeadbeef")

	l1InfoTree := newExactMatchL1InfoTree(s)
	gerSyncer := &fakeL2GERSyncer{
		info: l2gersync.GlobalExitRootInfo{L1InfoTreeIndex: testFinalIndex},
	}

	preparer := NewRollupPreparer(l1InfoTree, gerSyncer, nil)
	result, err := preparer.Prepare(ctx, s.request)
	require.Nil(t, result)
	require.ErrorContains(t, err, "no leaf proof refresher is configured")
}

func TestRollupPrepareRollupExitProofVerificationFailureIsHardError(t *testing.T) {
	ctx := context.Background()
	s := newRollupProofScenario(1101, 60)

	l1InfoTree := newExactMatchL1InfoTree(s)
	// Corrupt the LER-to-rollup-exit-root proof so it no longer reconstructs the leaf's rollup exit root.
	l1InfoTree.rollupProof[s.leaf.RollupExitRoot] = testProof("0xffff", "0xeeee")
	gerSyncer := &fakeL2GERSyncer{
		info: l2gersync.GlobalExitRootInfo{L1InfoTreeIndex: testFinalIndex},
	}

	preparer := NewRollupPreparer(l1InfoTree, gerSyncer, &fakeRefresher{})
	result, err := preparer.Prepare(ctx, s.request)
	require.Nil(t, result)
	require.ErrorContains(t, err, "verify LER-to-rollup-exit-root proof")
}

func TestRollupPrepareLeafToLERProofVerificationFailureIsHardError(t *testing.T) {
	ctx := context.Background()
	s := newRollupProofScenario(1101, 60)

	l1InfoTree := newExactMatchL1InfoTree(s)
	gerSyncer := &fakeL2GERSyncer{
		info: l2gersync.GlobalExitRootInfo{L1InfoTreeIndex: testFinalIndex},
	}
	// Corrupt the stored leaf-to-LER proof so it no longer reconstructs the LER.
	s.request.LeafProof = testProof("0x0bad")

	preparer := NewRollupPreparer(l1InfoTree, gerSyncer, &fakeRefresher{})
	result, err := preparer.Prepare(ctx, s.request)
	require.Nil(t, result)
	require.ErrorContains(t, err, "verify leaf-to-LER proof")
}

func TestRollupPrepareAdvancesPastSameBlockNonCoveringLeaf(t *testing.T) {
	ctx := context.Background()
	// The covering leaf is at the verify block (exact-match not available there), so the selector must
	// advance to the next leaf, which is strictly after the verify block and carries the covering LER.
	s := newRollupProofScenario(autoclaimtypes.L1OriginNetwork, testVerifyBlock+5)

	// A same-block leaf at index testFinalIndex-1 whose rollup exit root maps to a different, non-stored
	// LER (ambiguous: may predate the verify) — must be skipped.
	sameBlockLeaf := &l1infotreesync.L1InfoTreeLeaf{
		BlockNumber:     testVerifyBlock,
		L1InfoTreeIndex: testFinalIndex - 1,
		MainnetExitRoot: common.HexToHash("0xcafe01"),
		RollupExitRoot:  common.HexToHash("0xcafe10"),
		GlobalExitRoot:  common.HexToHash("0xcafe02"),
	}

	l1InfoTree := newExactMatchL1InfoTree(s)
	l1InfoTree.firstAfterBlock[testVerifyBlock] = sameBlockLeaf
	l1InfoTree.infoByIndex[testFinalIndex-1] = sameBlockLeaf
	l1InfoTree.localExitRoot[sameBlockLeaf.RollupExitRoot] = common.HexToHash("0xotherler")

	preparer := NewRollupPreparer(l1InfoTree, nil, &fakeRefresher{})
	result, err := preparer.Prepare(ctx, s.request)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Ready)
	require.Equal(t, testFinalIndex, result.Proof.L1InfoTreeIndex)
}

func TestRollupPrepareAdvancesPastNotYetVerifiedLeaf(t *testing.T) {
	ctx := context.Background()
	// First leaf after the verify block has no LER for the source network yet (ErrNotFound) — the
	// selector advances to the next leaf which carries the covering LER.
	s := newRollupProofScenario(autoclaimtypes.L1OriginNetwork, testVerifyBlock+5)

	notYetLeaf := &l1infotreesync.L1InfoTreeLeaf{
		BlockNumber:     testVerifyBlock,
		L1InfoTreeIndex: testFinalIndex - 1,
		MainnetExitRoot: common.HexToHash("0xcafe01"),
		RollupExitRoot:  common.HexToHash("0xcafe10"),
		GlobalExitRoot:  common.HexToHash("0xcafe02"),
	}

	l1InfoTree := newExactMatchL1InfoTree(s)
	l1InfoTree.firstAfterBlock[testVerifyBlock] = notYetLeaf
	l1InfoTree.infoByIndex[testFinalIndex-1] = notYetLeaf
	// notYetLeaf.RollupExitRoot intentionally absent from localExitRoot -> ErrNotFound.

	preparer := NewRollupPreparer(l1InfoTree, nil, &fakeRefresher{})
	result, err := preparer.Prepare(ctx, s.request)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Ready)
	require.Equal(t, testFinalIndex, result.Proof.L1InfoTreeIndex)
}

func TestRollupPrepareUsesPresetL1InfoTreeIndex(t *testing.T) {
	ctx := context.Background()
	s := newRollupProofScenario(1101, 60)
	preset := testFinalIndex
	s.request.L1InfoTreeIndex = &preset

	l1InfoTree := newExactMatchL1InfoTree(s)
	// Remove the firstAfterBlock wiring to prove the preset index path bypasses leaf selection.
	l1InfoTree.firstAfterBlock = map[uint64]*l1infotreesync.L1InfoTreeLeaf{}
	// gerSyncer present but should be skipped for a preset index.
	gerSyncer := &fakeL2GERSyncer{err: db.ErrNotFound}

	preparer := NewRollupPreparer(l1InfoTree, gerSyncer, &fakeRefresher{})
	result, err := preparer.Prepare(ctx, s.request)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Ready)
	require.Equal(t, testFinalIndex, result.Proof.L1InfoTreeIndex)
}
