package proof

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/bridgeservice"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/l1infotreesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestPrepareNilBridgeL1Error(t *testing.T) {
	preparer := &Preparer{}
	_, err := preparer.Prepare(context.Background(), testRequest(1))
	require.ErrorContains(t, err, "L1 bridge syncer is not available")
}

func TestPrepareNilL1InfoTreeError(t *testing.T) {
	preparer := &Preparer{bridgeL1: &fakeL1BridgeSyncer{}}
	_, err := preparer.Prepare(context.Background(), testRequest(1))
	require.ErrorContains(t, err, "L1 info tree syncer is not available")
}

func TestPrepareProofPropagatesError(t *testing.T) {
	ctx := context.Background()
	preparer := &Preparer{}

	proof, err := preparer.PrepareProof(ctx, testRequest(1))
	require.Nil(t, proof)
	require.Error(t, err)
	require.ErrorContains(t, err, "L1 bridge syncer is not available")
}

func TestPrepareGetInfoByIndexNonErrNotFoundError(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(12)
	infoErr := errors.New("unexpected RPC failure")
	bridge := &fakeL1BridgeSyncer{}
	l1InfoTree := &fakeL1InfoTreeSyncer{infoByIndexErr: infoErr}
	configureSuccessfulIndexLookup(t, depositCount, bridge, l1InfoTree)

	preparer := NewPreparer(bridge, l1InfoTree, nil)
	result, err := preparer.Prepare(ctx, testRequest(depositCount))

	require.Nil(t, result)
	require.ErrorIs(t, err, infoErr)
	require.ErrorContains(t, err, "get L1 info tree leaf at index")
}

func TestPrepareGetInfoByIndexNilResultError(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(12)
	bridge := &fakeL1BridgeSyncer{}
	l1InfoTree := &fakeL1InfoTreeSyncer{}
	configureSuccessfulIndexLookup(t, depositCount, bridge, l1InfoTree)
	// Insert a nil entry to trigger the nil info error path.
	l1InfoTree.infoByIndex[depositCount] = nil

	preparer := NewPreparer(bridge, l1InfoTree, nil)
	result, err := preparer.Prepare(ctx, testRequest(depositCount))

	require.Nil(t, result)
	require.ErrorContains(t, err, "empty result")
}

func TestPrepareWithRequestL1InfoTreeIndex(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(12)
	selectedIndex := uint32(77)
	selectedInfo := testL1InfoTreeLeaf(20, selectedIndex, "0x7700")
	bridge := &fakeL1BridgeSyncer{proof: testProof("0xaa")}
	l1InfoTree := &fakeL1InfoTreeSyncer{
		infoByIndex: map[uint32]*l1infotreesync.L1InfoTreeLeaf{
			selectedIndex: selectedInfo,
		},
		rollupProof: testProof("0xbb"),
	}

	request := testRequest(depositCount)
	request.L1InfoTreeIndex = &selectedIndex

	preparer := NewPreparer(bridge, l1InfoTree, nil)
	result, err := preparer.Prepare(ctx, request)
	require.NoError(t, err)
	require.True(t, result.Ready)
	require.Equal(t, selectedIndex, result.Proof.L1InfoTreeIndex)
}

func TestPreparePendingWhenBridgeNotYetIncludedOnL1InfoTree(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(42)
	lastInfo := testL1InfoTreeLeaf(10, 100, "0x100")

	preparer := NewPreparer(
		&fakeL1BridgeSyncer{
			rootsByLER: map[common.Hash]*treetypes.Root{
				lastInfo.MainnetExitRoot: {Index: depositCount - 1},
			},
		},
		&fakeL1InfoTreeSyncer{
			lastInfo: lastInfo,
		},
		nil,
	)

	result, err := preparer.Prepare(ctx, testRequest(depositCount))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Ready)
	require.Nil(t, result.Proof)

	proof, err := preparer.PrepareProof(ctx, testRequest(depositCount))
	require.NoError(t, err)
	require.Nil(t, proof)
}

func TestPreparePendingWhenL1InfoTreeSyncIsBehindBridgeBlock(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(42)
	lastInfo := testL1InfoTreeLeaf(100, depositCount, "0x100")
	request := testRequest(depositCount)
	request.Bridge.BlockNum = lastInfo.BlockNumber + 1

	preparer := NewPreparer(
		&fakeL1BridgeSyncer{
			rootsByLER: map[common.Hash]*treetypes.Root{
				lastInfo.MainnetExitRoot: {Index: depositCount},
			},
		},
		&fakeL1InfoTreeSyncer{
			lastInfo: lastInfo,
		},
		nil,
	)

	result, err := preparer.Prepare(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Ready)
	require.Nil(t, result.Proof)
}

func TestPreparePendingWhenL1InfoTreeLeafHasZeroExitRoots(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(42)
	emptyInfo := &l1infotreesync.L1InfoTreeLeaf{
		BlockNumber:     100,
		L1InfoTreeIndex: depositCount,
	}

	preparer := NewPreparer(
		&fakeL1BridgeSyncer{
			rootsByLER: map[common.Hash]*treetypes.Root{
				emptyInfo.MainnetExitRoot: {Index: depositCount},
			},
		},
		&fakeL1InfoTreeSyncer{
			firstInfo: emptyInfo,
			lastInfo:  emptyInfo,
			infoAfterBlock: map[uint64]*l1infotreesync.L1InfoTreeLeaf{
				emptyInfo.BlockNumber: emptyInfo,
			},
			infoByIndex: map[uint32]*l1infotreesync.L1InfoTreeLeaf{
				depositCount: emptyInfo,
			},
		},
		nil,
	)

	result, err := preparer.Prepare(ctx, testRequest(depositCount))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Ready)
	require.Nil(t, result.Proof)
}

func TestPrepareReturnsProofLookupFailures(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(12)
	localProofErr := errors.New("local proof failed")
	rollupProofErr := errors.New("rollup proof failed")

	testCases := []struct {
		name          string
		bridge        *fakeL1BridgeSyncer
		l1InfoTree    *fakeL1InfoTreeSyncer
		expectedErr   string
		expectedCalls int
	}{
		{
			name: "local exit root proof lookup failure",
			bridge: &fakeL1BridgeSyncer{
				proofErr: localProofErr,
			},
			l1InfoTree: &fakeL1InfoTreeSyncer{},
			expectedErr: "get L1 local exit root proof: " +
				localProofErr.Error(),
			expectedCalls: 0,
		},
		{
			name: "rollup exit root proof lookup failure",
			bridge: &fakeL1BridgeSyncer{
				proof: testProof("0x1"),
			},
			l1InfoTree: &fakeL1InfoTreeSyncer{
				rollupProofErr: rollupProofErr,
			},
			expectedErr: "get rollup exit root proof: " +
				rollupProofErr.Error(),
			expectedCalls: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			configureSuccessfulIndexLookup(t, depositCount, tc.bridge, tc.l1InfoTree)

			preparer := NewPreparer(tc.bridge, tc.l1InfoTree, nil)
			result, err := preparer.Prepare(ctx, testRequest(depositCount))

			require.Nil(t, result)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedErr)
			require.Len(t, tc.l1InfoTree.rollupProofCalls, tc.expectedCalls)
		})
	}
}

func TestPrepareBuildsSuccessfulL1OriginClaimProof(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(12)
	preparedAt := time.Unix(100, 0).UTC()
	localProof := testProof("0x11", "0x12", "0x13")
	rollupProof := testProof("0x21", "0x22")
	bridge := &fakeL1BridgeSyncer{
		proof: localProof,
	}
	l1InfoTree := &fakeL1InfoTreeSyncer{
		rollupProof: rollupProof,
	}
	configureSuccessfulIndexLookup(t, depositCount, bridge, l1InfoTree)

	preparer := NewPreparer(bridge, l1InfoTree, nil)
	preparer.now = func() time.Time { return preparedAt }

	result, err := preparer.Prepare(ctx, testRequest(depositCount))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Ready)
	require.NotNil(t, result.Proof)

	proof := result.Proof
	require.Equal(t, depositCount, proof.L1InfoTreeIndex)
	require.Equal(t, l1InfoTree.infoByIndex[depositCount], proof.L1InfoTreeLeaf)
	require.Equal(t, l1InfoTree.infoByIndex[depositCount].MainnetExitRoot, proof.MainnetExitRoot)
	require.Equal(t, l1InfoTree.infoByIndex[depositCount].RollupExitRoot, proof.RollupExitRoot)
	require.Equal(t, l1InfoTree.infoByIndex[depositCount].GlobalExitRoot, proof.GlobalExitRoot)
	require.Equal(t, localProof, proof.ProofLocalExitRoot)
	require.Equal(t, rollupProof, proof.ProofRollupExitRoot)
	require.Equal(t, autoclaimtypes.ProofToABIProof(localProof), proof.ABILocalExitRoot)
	require.Equal(t, autoclaimtypes.ProofToABIProof(rollupProof), proof.ABIRollupExitRoot)
	require.Equal(t, preparedAt, proof.PreparedAt)

	require.Equal(t, []getProofCall{{
		depositCount:  depositCount,
		localExitRoot: l1InfoTree.infoByIndex[depositCount].MainnetExitRoot,
	}}, bridge.proofCalls)
	require.Equal(t, []rollupProofCall{{
		networkID: autoclaimtypes.L1OriginNetwork,
		root:      l1InfoTree.infoByIndex[depositCount].RollupExitRoot,
	}}, l1InfoTree.rollupProofCalls)
}

func TestPrepareUsesPresetL1InfoTreeIndex(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(12)
	selectedIndex := uint32(44)
	selectedInfo := testL1InfoTreeLeaf(44, selectedIndex, "0x4400")
	bridge := &fakeL1BridgeSyncer{
		proof: testProof("0x1"),
	}
	l1InfoTree := &fakeL1InfoTreeSyncer{
		infoByIndex: map[uint32]*l1infotreesync.L1InfoTreeLeaf{
			selectedIndex: selectedInfo,
		},
		rollupProof: testProof("0x2"),
	}
	request := testRequest(depositCount)
	request.Bridge.L1InfoTreeIndex = &selectedIndex

	preparer := NewPreparer(bridge, l1InfoTree, nil)
	result, err := preparer.Prepare(ctx, request)
	require.NoError(t, err)
	require.True(t, result.Ready)
	require.Equal(t, selectedIndex, result.Proof.L1InfoTreeIndex)
	require.Equal(t, selectedInfo.MainnetExitRoot, bridge.proofCalls[0].localExitRoot)
}

func TestPreparePendingWhenPresetL1InfoTreeIndexLeafNotSynced(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(12)
	selectedIndex := uint32(44)
	bridge := &fakeL1BridgeSyncer{
		proof: testProof("0x1"),
	}
	request := testRequest(depositCount)
	request.Bridge.L1InfoTreeIndex = &selectedIndex

	preparer := NewPreparer(bridge, &fakeL1InfoTreeSyncer{
		infoByIndexErr: db.ErrNotFound,
	}, nil)
	result, err := preparer.Prepare(ctx, request)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Ready)
	require.Empty(t, bridge.proofCalls)
}

func TestPreparePendingWhenNoGERInjectedYet(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(12)
	bridge := &fakeL1BridgeSyncer{}
	l1InfoTree := &fakeL1InfoTreeSyncer{}
	configureSuccessfulIndexLookup(t, depositCount, bridge, l1InfoTree)

	preparer := NewPreparer(bridge, l1InfoTree, &fakeL2GERSyncer{err: ErrGERNotInjected})
	result, err := preparer.Prepare(ctx, testRequest(depositCount))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Ready)
	require.Empty(t, bridge.proofCalls)
}

func TestPrepareUsesFirstCoveringGERIndex(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(12)
	injectedIndex := uint32(20)
	injectedInfo := testL1InfoTreeLeaf(40, injectedIndex, "0x4000")
	bridge := &fakeL1BridgeSyncer{}
	l1InfoTree := &fakeL1InfoTreeSyncer{
		rollupProof: testProof("0x2"),
	}
	configureSuccessfulIndexLookup(t, depositCount, bridge, l1InfoTree)
	l1InfoTree.infoByIndex[injectedIndex] = injectedInfo

	preparer := NewPreparer(bridge, l1InfoTree, &fakeL2GERSyncer{index: injectedIndex})
	result, err := preparer.Prepare(ctx, testRequest(depositCount))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Ready)
	require.Equal(t, injectedIndex, result.Proof.L1InfoTreeIndex)
	require.Equal(t, injectedInfo.MainnetExitRoot, bridge.proofCalls[0].localExitRoot)
}

func TestPrepareGERSyncerErrorPropagated(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(12)
	bridge := &fakeL1BridgeSyncer{}
	l1InfoTree := &fakeL1InfoTreeSyncer{}
	configureSuccessfulIndexLookup(t, depositCount, bridge, l1InfoTree)

	syncerErr := errors.New("rpc failure")
	preparer := NewPreparer(bridge, l1InfoTree, &fakeL2GERSyncer{err: syncerErr})
	result, err := preparer.Prepare(ctx, testRequest(depositCount))
	require.Nil(t, result)
	require.Error(t, err)
	require.ErrorContains(t, err, "get first injected GER after index")
	require.ErrorContains(t, err, syncerErr.Error())
}

func TestPrepareNilGERSyncerUsesOldPath(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(12)
	localProof := testProof("0xaa")
	rollupProof := testProof("0xbb")
	bridge := &fakeL1BridgeSyncer{proof: localProof}
	l1InfoTree := &fakeL1InfoTreeSyncer{rollupProof: rollupProof}
	configureSuccessfulIndexLookup(t, depositCount, bridge, l1InfoTree)

	// Restore the proof overwritten by configureSuccessfulIndexLookup
	bridge.proof = localProof
	l1InfoTree.rollupProof = rollupProof

	preparer := NewPreparer(bridge, l1InfoTree, nil)
	result, err := preparer.Prepare(ctx, testRequest(depositCount))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Ready)
	require.Equal(t, depositCount, result.Proof.L1InfoTreeIndex)
}

func TestProofToABIProofPreservesExpectedShape(t *testing.T) {
	proof := testProof("0x1", "0x2", "0x3")

	abiProof := autoclaimtypes.ProofToABIProof(proof)

	require.Len(t, abiProof, int(treetypes.DefaultHeight))
	require.Equal(t, common.HexToHash("0x1"), common.Hash(abiProof[0]))
	require.Equal(t, common.HexToHash("0x2"), common.Hash(abiProof[1]))
	require.Equal(t, common.HexToHash("0x3"), common.Hash(abiProof[2]))
	require.Equal(t, common.Hash{}, common.Hash(abiProof[3]))
	require.Equal(t, common.Hash{}, common.Hash(abiProof[treetypes.DefaultHeight-1]))
}

func TestPrepareMatchesBridgeServiceL1ClaimProofFields(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(12)
	localProof := testProof("0xf", "0xd", "0xc", "0xb")
	rollupProof := testProof("0x1", "0x2")
	bridge := &fakeL1BridgeSyncer{
		proof: localProof,
	}
	l1InfoTree := &fakeL1InfoTreeSyncer{
		rollupProof: rollupProof,
	}
	configureSuccessfulIndexLookup(t, depositCount, bridge, l1InfoTree)

	preparer := NewPreparer(bridge, l1InfoTree, nil)
	result, err := preparer.Prepare(ctx, testRequest(depositCount))
	require.NoError(t, err)
	require.True(t, result.Ready)

	expectedBridgeServiceProof := bridgetypes.ClaimProof{
		ProofLocalExitRoot:  bridgetypes.ConvertToProofResponse(localProof),
		ProofRollupExitRoot: bridgetypes.ConvertToProofResponse(rollupProof),
		L1InfoTreeLeaf:      *bridgeservice.NewL1InfoTreeLeafResponse(l1InfoTree.infoByIndex[depositCount]),
	}
	actualBridgeServiceProof := bridgetypes.ClaimProof{
		ProofLocalExitRoot:  bridgetypes.ConvertToProofResponse(result.Proof.ProofLocalExitRoot),
		ProofRollupExitRoot: bridgetypes.ConvertToProofResponse(result.Proof.ProofRollupExitRoot),
		L1InfoTreeLeaf:      *bridgeservice.NewL1InfoTreeLeafResponse(result.Proof.L1InfoTreeLeaf),
	}

	require.Equal(t, expectedBridgeServiceProof, actualBridgeServiceProof)
}

func testRequest(depositCount uint32) autoclaimtypes.AutoClaimRequest {
	return autoclaimtypes.AutoClaimRequest{
		Key: autoclaimtypes.DeriveRequestKey(
			autoclaimtypes.L1OriginNetwork,
			1101,
			depositCount,
		),
		Bridge: autoclaimtypes.BridgeExit{
			BlockNum:           20,
			OriginNetwork:      autoclaimtypes.L1OriginNetwork,
			DestinationNetwork: 1101,
			DepositCount:       depositCount,
			GlobalIndex:        autoclaimtypes.DeriveL1GlobalIndex(depositCount),
		},
		GlobalIndex: autoclaimtypes.DeriveL1GlobalIndex(depositCount),
	}
}

func configureSuccessfulIndexLookup(
	t *testing.T,
	depositCount uint32,
	bridge *fakeL1BridgeSyncer,
	l1InfoTree *fakeL1InfoTreeSyncer,
) {
	t.Helper()

	firstInfo := testL1InfoTreeLeaf(10, 10, "0x10")
	targetInfo := testL1InfoTreeLeaf(20, depositCount, "0x20")
	lastInfo := testL1InfoTreeLeaf(30, 30, "0x30")

	bridge.rootsByLER = map[common.Hash]*treetypes.Root{
		firstInfo.MainnetExitRoot:  {Index: 10},
		targetInfo.MainnetExitRoot: {Index: depositCount},
		lastInfo.MainnetExitRoot:   {Index: 30},
	}
	if bridge.proof == (treetypes.Proof{}) && bridge.proofErr == nil {
		bridge.proof = testProof("0xaa")
	}

	l1InfoTree.firstInfo = firstInfo
	l1InfoTree.lastInfo = lastInfo
	l1InfoTree.infoAfterBlock = map[uint64]*l1infotreesync.L1InfoTreeLeaf{
		20: targetInfo,
	}
	l1InfoTree.infoByIndex = map[uint32]*l1infotreesync.L1InfoTreeLeaf{
		depositCount: targetInfo,
	}
}

func testL1InfoTreeLeaf(blockNumber uint64, index uint32, root string) *l1infotreesync.L1InfoTreeLeaf {
	return &l1infotreesync.L1InfoTreeLeaf{
		BlockNumber:     blockNumber,
		L1InfoTreeIndex: index,
		MainnetExitRoot: common.HexToHash(root),
		RollupExitRoot:  common.HexToHash(fmt.Sprintf("%s01", root)),
		GlobalExitRoot:  common.HexToHash(fmt.Sprintf("%s02", root)),
		Hash:            common.HexToHash(fmt.Sprintf("%s03", root)),
	}
}

func testProof(values ...string) treetypes.Proof {
	var proof treetypes.Proof
	for i, value := range values {
		proof[i] = common.HexToHash(value)
	}
	return proof
}

// fakeL2GERSyncer implements L2GERSyncer for tests.
type fakeL2GERSyncer struct {
	index uint32
	err   error
}

func (f *fakeL2GERSyncer) GetFirstGERAfterL1InfoTreeIndex(
	_ context.Context, _ uint32,
) (uint32, error) {
	return f.index, f.err
}

// Compile-time assertion: fakeL2GERSyncer implements L2GERSyncer.
var _ L2GERSyncer = (*fakeL2GERSyncer)(nil)

type fakeL1BridgeSyncer struct {
	rootsByLER map[common.Hash]*treetypes.Root
	rootErrs   map[common.Hash]error
	lastRoot   *treetypes.Root
	lastErr    error
	proof      treetypes.Proof
	proofErr   error
	proofCalls []getProofCall
}

func (f *fakeL1BridgeSyncer) GetProof(
	_ context.Context,
	depositCount uint32,
	localExitRoot common.Hash,
) (treetypes.Proof, error) {
	f.proofCalls = append(f.proofCalls, getProofCall{
		depositCount:  depositCount,
		localExitRoot: localExitRoot,
	})
	return f.proof, f.proofErr
}

func (f *fakeL1BridgeSyncer) GetRootByLER(_ context.Context, ler common.Hash) (*treetypes.Root, error) {
	if err := f.rootErrs[ler]; err != nil {
		return nil, err
	}
	root, ok := f.rootsByLER[ler]
	if !ok {
		return nil, errors.New("root not found")
	}
	return root, nil
}

func (f *fakeL1BridgeSyncer) GetLastRoot(_ context.Context) (*treetypes.Root, error) {
	return f.lastRoot, f.lastErr
}

type fakeL1InfoTreeSyncer struct {
	infoByIndex          map[uint32]*l1infotreesync.L1InfoTreeLeaf
	infoByIndexErr       error
	latestInfoUntilBlock *l1infotreesync.L1InfoTreeLeaf
	latestInfoUntilErr   error
	latestInfoUntilCalls []uint64
	rollupProof          treetypes.Proof
	rollupProofErr       error
	lastInfo             *l1infotreesync.L1InfoTreeLeaf
	firstInfo            *l1infotreesync.L1InfoTreeLeaf
	infoAfterBlock       map[uint64]*l1infotreesync.L1InfoTreeLeaf
	rollupProofCalls     []rollupProofCall
}

func (f *fakeL1InfoTreeSyncer) GetInfoByIndex(
	_ context.Context,
	index uint32,
) (*l1infotreesync.L1InfoTreeLeaf, error) {
	if f.infoByIndexErr != nil {
		return nil, f.infoByIndexErr
	}
	info, ok := f.infoByIndex[index]
	if !ok {
		return nil, errors.New("info not found")
	}
	return info, nil
}

func (f *fakeL1InfoTreeSyncer) GetLatestL1InfoLeafUntilBlock(
	_ context.Context,
	blockNum uint64,
) (*l1infotreesync.L1InfoTreeLeaf, error) {
	f.latestInfoUntilCalls = append(f.latestInfoUntilCalls, blockNum)
	if f.latestInfoUntilErr != nil {
		return nil, f.latestInfoUntilErr
	}
	return f.latestInfoUntilBlock, nil
}

func (f *fakeL1InfoTreeSyncer) GetRollupExitTreeMerkleProof(
	_ context.Context,
	networkID uint32,
	root common.Hash,
) (treetypes.Proof, error) {
	f.rollupProofCalls = append(f.rollupProofCalls, rollupProofCall{
		networkID: networkID,
		root:      root,
	})
	return f.rollupProof, f.rollupProofErr
}

func (f *fakeL1InfoTreeSyncer) GetLastInfo() (*l1infotreesync.L1InfoTreeLeaf, error) {
	return f.lastInfo, nil
}

func (f *fakeL1InfoTreeSyncer) GetFirstInfo() (*l1infotreesync.L1InfoTreeLeaf, error) {
	return f.firstInfo, nil
}

func (f *fakeL1InfoTreeSyncer) GetFirstInfoAfterBlock(blockNum uint64) (*l1infotreesync.L1InfoTreeLeaf, error) {
	info, ok := f.infoAfterBlock[blockNum]
	if !ok {
		return nil, fmt.Errorf("info after block %d not found", blockNum)
	}
	return info, nil
}

// Compile-time assertion: fakeL1InfoTreeSyncer implements L1InfoTreeSyncer.
var _ L1InfoTreeSyncer = (*fakeL1InfoTreeSyncer)(nil)

// Compile-time assertion: fakeL1BridgeSyncer implements L1BridgeSyncer.
var _ L1BridgeSyncer = (*fakeL1BridgeSyncer)(nil)

type getProofCall struct {
	depositCount  uint32
	localExitRoot common.Hash
}

type rollupProofCall struct {
	networkID uint32
	root      common.Hash
}

func TestPreparePrepareProofReturnsProof(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(12)
	bridge := &fakeL1BridgeSyncer{proof: testProof("0xaa")}
	l1InfoTree := &fakeL1InfoTreeSyncer{rollupProof: testProof("0xbb")}
	configureSuccessfulIndexLookup(t, depositCount, bridge, l1InfoTree)
	bridge.proof = testProof("0xaa")
	l1InfoTree.rollupProof = testProof("0xbb")

	preparer := NewPreparer(bridge, l1InfoTree, nil)
	proof, err := preparer.PrepareProof(ctx, testRequest(depositCount))
	require.NoError(t, err)
	require.NotNil(t, proof)
	require.Equal(t, depositCount, proof.L1InfoTreeIndex)
}

func TestPreparePendingWhenInfoBlockBeforeBridgeBlock(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(12)
	selectedIndex := uint32(44)
	// selectedInfo has BlockNumber=10; bridge.BlockNum defaults to 20 in testRequest.
	selectedInfo := testL1InfoTreeLeaf(10, selectedIndex, "0xAA")

	bridge := &fakeL1BridgeSyncer{proof: testProof("0x1")}
	l1InfoTree := &fakeL1InfoTreeSyncer{
		infoByIndex: map[uint32]*l1infotreesync.L1InfoTreeLeaf{
			selectedIndex: selectedInfo,
		},
		rollupProof: testProof("0x2"),
	}

	request := testRequest(depositCount)
	request.Bridge.L1InfoTreeIndex = &selectedIndex
	// Bridge.BlockNum=20 > selectedInfo.BlockNumber=10 → Ready: false.

	preparer := NewPreparer(bridge, l1InfoTree, nil)
	result, err := preparer.Prepare(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Ready)
}

func TestBinarySearchGetFirstInfoAfterBlockError(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(5)
	firstInfo := testL1InfoTreeLeaf(10, 0, "0x10")
	lastInfo := testL1InfoTreeLeaf(30, 5, "0x30")

	preparer := NewPreparer(
		&fakeL1BridgeSyncer{
			rootsByLER: map[common.Hash]*treetypes.Root{
				lastInfo.MainnetExitRoot:  {Index: depositCount},
				firstInfo.MainnetExitRoot: {Index: 0},
			},
		},
		&fakeL1InfoTreeSyncer{
			firstInfo:      firstInfo,
			lastInfo:       lastInfo,
			infoAfterBlock: map[uint64]*l1infotreesync.L1InfoTreeLeaf{},
		},
		nil,
	)

	result, err := preparer.Prepare(ctx, testRequest(depositCount))
	require.Nil(t, result)
	require.Error(t, err)
	require.ErrorContains(t, err, "info after block")
}

func TestBinarySearchLowerAndUpperLimitBranches(t *testing.T) {
	ctx := context.Background()
	depositCount := uint32(5)

	firstInfo := testL1InfoTreeLeaf(10, 0, "0x10")
	lastInfo := testL1InfoTreeLeaf(30, 99, "0x30")
	info20 := testL1InfoTreeLeaf(20, 88, "0x20") // root.Index=7 > 5 → upperLimit--
	info14 := testL1InfoTreeLeaf(14, 33, "0x14") // root.Index=3 < 5 → lowerLimit++
	info17 := testL1InfoTreeLeaf(17, 50, "0x17") // root.Index=5 == depositCount → return
	// infoByIndex[50] is the leaf returned after binary search selects index=50.
	resultInfo := testL1InfoTreeLeaf(25, 50, "0x50")

	bridge := &fakeL1BridgeSyncer{
		rootsByLER: map[common.Hash]*treetypes.Root{
			firstInfo.MainnetExitRoot:  {Index: 0},
			lastInfo.MainnetExitRoot:   {Index: 5},
			info20.MainnetExitRoot:     {Index: 7},
			info14.MainnetExitRoot:     {Index: 3},
			info17.MainnetExitRoot:     {Index: 5},
			resultInfo.MainnetExitRoot: {Index: 5},
		},
		proof: testProof("0xbb"),
	}
	l1InfoTree := &fakeL1InfoTreeSyncer{
		firstInfo: firstInfo,
		lastInfo:  lastInfo,
		infoAfterBlock: map[uint64]*l1infotreesync.L1InfoTreeLeaf{
			20: info20,
			14: info14,
			17: info17,
		},
		infoByIndex: map[uint32]*l1infotreesync.L1InfoTreeLeaf{
			50: resultInfo,
		},
		rollupProof: testProof("0xcc"),
	}

	preparer := NewPreparer(bridge, l1InfoTree, nil)
	result, err := preparer.Prepare(ctx, testRequest(depositCount))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Ready)
	require.Equal(t, uint32(50), result.Proof.L1InfoTreeIndex)
}

// INC-129 regression fixtures shared by the skew tests below. bridgesync L1 trails l1infotreesync,
// so GetRootByLER cannot resolve the MER of the newest info tree leaf and the fallback fires. The
// old fallback fed GetLastRoot's Index -- a position in the L1 bridge exit tree, i.e. a deposit
// count of ~1.1M -- into l1InfoTree.GetInfoByIndex, which expects an L1 info tree index. No such
// row could exist, so the preparer failed with "sql: no rows in result set".
const (
	// the deposit count the caller asks about, and the deposit count bridgesync L1 has settled
	skewDepositCount     = uint32(1_146_000)
	skewLastRootIndex    = uint32(1_146_035)
	skewLastRootBlockNum = uint64(8_412)
	// the L1 info tree syncer is ahead: its newest leaf sits at a later L1 block
	skewTipInfoBlockNum     = uint64(8_501)
	skewClampedInfoBlockNum = uint64(8_400)
	skewFirstInfoBlockNum   = uint64(4_000)
	// midpoint of [skewFirstInfoBlockNum, skewClampedInfoBlockNum]; with the unclamped tip block as
	// the upper limit the binary search would probe 6_250 instead and find no fixture
	skewProbedBlock   = uint64(6_200)
	skewExpectedIndex = uint32(74_099)
)

// newSkewedFakes builds a bridgesync-L1-trails-l1infotreesync pair of fakes. lastRootBlockNum is
// the BlockNum carried by the root returned from the GetLastRoot fallback.
func newSkewedFakes(lastRootBlockNum uint64) (*fakeL1BridgeSyncer, *fakeL1InfoTreeSyncer) {
	tipInfo := testL1InfoTreeLeaf(skewTipInfoBlockNum, 74_390, "0xdead")
	clampedInfo := testL1InfoTreeLeaf(skewClampedInfoBlockNum, 74_301, "0xcafe")
	firstInfo := testL1InfoTreeLeaf(skewFirstInfoBlockNum, 1, "0xfeed")
	targetInfo := testL1InfoTreeLeaf(6_250, skewExpectedIndex, "0xbeef")

	bridge := &fakeL1BridgeSyncer{
		// tipInfo.MainnetExitRoot is deliberately absent: bridgesync L1 has never seen that MER,
		// which is what pushes the code into the GetLastRoot fallback.
		rootsByLER: map[common.Hash]*treetypes.Root{
			targetInfo.MainnetExitRoot: {Index: skewDepositCount, BlockNum: 6_260},
		},
		lastRoot: &treetypes.Root{Index: skewLastRootIndex, BlockNum: lastRootBlockNum},
		proof:    testProof("0xaa"),
	}
	l1InfoTree := &fakeL1InfoTreeSyncer{
		lastInfo:  tipInfo,
		firstInfo: firstInfo,
		// the clamp must be by L1 block number, never by the deposit count
		latestInfoUntilBlock: clampedInfo,
		infoAfterBlock: map[uint64]*l1infotreesync.L1InfoTreeLeaf{
			skewProbedBlock: targetInfo,
		},
		infoByIndex: map[uint32]*l1infotreesync.L1InfoTreeLeaf{
			skewExpectedIndex: targetInfo,
		},
		rollupProof: testProof("0xbb"),
	}
	return bridge, l1InfoTree
}

func TestFirstL1InfoTreeIndexForL1BridgeIndexSpaceSkew(t *testing.T) {
	// guard the fixture itself: if the two index spaces ever converge, this test stops proving
	// anything, because a block number would also be a plausible L1 info tree index
	require.Greater(t, uint64(skewLastRootIndex), skewLastRootBlockNum*100,
		"the deposit count and the L1 block number must stay orders of magnitude apart")

	ctx := context.Background()

	t.Run("returns a valid l1 info tree index instead of an error", func(t *testing.T) {
		bridge, l1InfoTree := newSkewedFakes(skewLastRootBlockNum)
		preparer := NewPreparer(bridge, l1InfoTree, nil)

		index, err := preparer.firstL1InfoTreeIndexForL1Bridge(ctx, skewDepositCount, 20)
		require.NoError(t, err)
		require.Equal(t, skewExpectedIndex, index)
		// the fallback clamped on the bridge syncer's block, not on its deposit count
		require.Equal(t, []uint64{skewLastRootBlockNum}, l1InfoTree.latestInfoUntilCalls)
	})

	t.Run("prepares a proof while the L1 bridge syncer trails", func(t *testing.T) {
		bridge, l1InfoTree := newSkewedFakes(skewLastRootBlockNum)
		preparer := NewPreparer(bridge, l1InfoTree, nil)

		result, err := preparer.Prepare(ctx, testRequest(skewDepositCount))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.True(t, result.Ready)
		require.Equal(t, skewExpectedIndex, result.Proof.L1InfoTreeIndex)
		require.Equal(t, []uint64{skewLastRootBlockNum}, l1InfoTree.latestInfoUntilCalls)
	})
}

func TestFirstL1InfoTreeIndexForL1BridgeLastRootBlockZero(t *testing.T) {
	ctx := context.Background()

	t.Run("reports not-on-l1-info instead of clamping by block 0", func(t *testing.T) {
		bridge, l1InfoTree := newSkewedFakes(0)
		preparer := NewPreparer(bridge, l1InfoTree, nil)

		_, err := preparer.firstL1InfoTreeIndexForL1Bridge(ctx, skewDepositCount, 20)
		require.ErrorIs(t, err, bridgeservice.ErrNotOnL1Info)
		require.ErrorContains(t, err, "bridgesync L1 has not indexed any block yet")
		require.Empty(t, l1InfoTree.latestInfoUntilCalls)
	})

	t.Run("keeps the request pending", func(t *testing.T) {
		bridge, l1InfoTree := newSkewedFakes(0)
		preparer := NewPreparer(bridge, l1InfoTree, nil)

		result, err := preparer.Prepare(ctx, testRequest(skewDepositCount))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.False(t, result.Ready)
		require.Nil(t, result.Proof)
		require.Empty(t, l1InfoTree.latestInfoUntilCalls)
	})
}

func TestFirstL1InfoTreeIndexForL1BridgeClampLookupError(t *testing.T) {
	ctx := context.Background()
	clampErr := errors.New("sql: no rows in result set")

	bridge, l1InfoTree := newSkewedFakes(skewLastRootBlockNum)
	l1InfoTree.latestInfoUntilErr = clampErr
	preparer := NewPreparer(bridge, l1InfoTree, nil)

	_, err := preparer.firstL1InfoTreeIndexForL1Bridge(ctx, skewDepositCount, 20)
	require.ErrorIs(t, err, clampErr)
	require.ErrorContains(t, err, "l1infotreesync has no L1 info tree leaf at or before L1 block 8412")
}
