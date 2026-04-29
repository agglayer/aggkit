package proofbuilder_test

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/dvnworker/proofbuilder"
	"github.com/agglayer/aggkit/l1infotreesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
)

// ---- mock implementations ----

type mockBridgeSyncer struct {
	bridge *bridgesync.Bridge
	proof  treetypes.Proof
	root   treetypes.Root
	errGet error
	errPrf error
}

func (m *mockBridgeSyncer) GetBridgeByDepositCount(_ context.Context, _ uint32) (*bridgesync.Bridge, error) {
	return m.bridge, m.errGet
}

func (m *mockBridgeSyncer) GetProof(_ context.Context, _ uint32, _ common.Hash) (treetypes.Proof, error) {
	return m.proof, m.errPrf
}

func (m *mockBridgeSyncer) GetExitRootByIndex(_ context.Context, _ uint32) (treetypes.Root, error) {
	return m.root, nil
}

type mockL1InfoTree struct {
	info        *l1infotreesync.L1InfoTreeLeaf
	ler         common.Hash
	rollupProof treetypes.Proof
	errInfo     error
	errLER      error
	errProof    error
}

func (m *mockL1InfoTree) GetLastInfo() (*l1infotreesync.L1InfoTreeLeaf, error) {
	return m.info, m.errInfo
}

func (m *mockL1InfoTree) GetLocalExitRoot(_ context.Context, _ uint32, _ common.Hash) (common.Hash, error) {
	return m.ler, m.errLER
}

func (m *mockL1InfoTree) GetRollupExitTreeMerkleProof(
	_ context.Context, _ uint32, _ common.Hash,
) (treetypes.Proof, error) {
	return m.rollupProof, m.errProof
}

// ---- helpers ----

// buildGlobalIndex for a rollup: bit layout = rollupIndex<<32 | depositCount
func rollupGlobalIndex(rollupIndex uint32, depositCount uint32) *big.Int {
	upper := new(big.Int).Lsh(big.NewInt(int64(rollupIndex)), 32)
	lower := big.NewInt(int64(depositCount))
	return new(big.Int).Or(upper, lower)
}

// buildGlobalIndex for mainnet: bit 64 set | depositCount
func mainnetGlobalIndex(depositCount uint32) *big.Int {
	flag := new(big.Int).Lsh(big.NewInt(1), 64)
	lower := big.NewInt(int64(depositCount))
	return new(big.Int).Or(flag, lower)
}

func syntheticProof(seed byte) treetypes.Proof {
	var p treetypes.Proof
	for i := range p {
		p[i] = common.Hash{seed + byte(i)}
	}
	return p
}

// ---- tests ----

// TestBuild_HappyPath_Rollup verifies all fields are populated correctly for a rollup origin.
func TestBuild_HappyPath_Rollup(t *testing.T) {
	t.Parallel()

	const (
		rollupIndex  uint32 = 2  // originNetwork = rollupIndex+1 = 3
		depositCount uint32 = 7
	)
	globalIndex := rollupGlobalIndex(rollupIndex, depositCount)

	originNetwork := rollupIndex + 1
	destNetwork := uint32(5)
	token := common.HexToAddress("0x1111111111111111111111111111111111111111")
	destAddr := common.HexToAddress("0x2222222222222222222222222222222222222222")
	amount := big.NewInt(1_000_000)
	metadata := []byte("hello")

	mainnetExitRoot := common.HexToHash("0xaaaa")
	rollupExitRoot := common.HexToHash("0xbbbb")
	localExitRoot := common.HexToHash("0xcccc")

	localProof := syntheticProof(0x10)
	rollupProof := syntheticProof(0x20)

	bridge := &bridgesync.Bridge{
		OriginNetwork:      originNetwork,
		OriginAddress:      token,
		DestinationNetwork: destNetwork,
		DestinationAddress: destAddr,
		Amount:             amount,
		Metadata:           metadata,
		DepositCount:       depositCount,
	}
	info := &l1infotreesync.L1InfoTreeLeaf{
		MainnetExitRoot: mainnetExitRoot,
		RollupExitRoot:  rollupExitRoot,
	}

	bs := &mockBridgeSyncer{bridge: bridge, proof: localProof}
	l1t := &mockL1InfoTree{info: info, ler: localExitRoot, rollupProof: rollupProof}

	pb := proofbuilder.New(bs, l1t, originNetwork, nil)

	claim, err := pb.Build(context.Background(), globalIndex)
	require.NoError(t, err)

	require.Equal(t, globalIndex, claim.GlobalIndex)
	require.Equal(t, [32]byte(mainnetExitRoot), claim.MainnetExitRoot)
	require.Equal(t, [32]byte(rollupExitRoot), claim.RollupExitRoot)
	require.Equal(t, originNetwork, claim.OriginNetwork)
	require.Equal(t, token, claim.OriginTokenAddress)
	require.Equal(t, destNetwork, claim.DestinationNetwork)
	require.Equal(t, destAddr, claim.DestinationAddress)
	require.Equal(t, amount, claim.Amount)
	require.Equal(t, metadata, claim.Metadata)

	// Check proofs are set correctly.
	for i, h := range localProof {
		require.Equal(t, [32]byte(h), claim.SMTProofLocalExitRoot[i])
	}
	for i, h := range rollupProof {
		require.Equal(t, [32]byte(h), claim.SMTProofRollupExitRoot[i])
	}
}

// TestBuild_HappyPath_Mainnet verifies mainnet (networkID=0) path uses MainnetExitRoot as localExitRoot.
func TestBuild_HappyPath_Mainnet(t *testing.T) {
	t.Parallel()

	const depositCount uint32 = 3
	globalIndex := mainnetGlobalIndex(depositCount)

	mainnetExitRoot := common.HexToHash("0xdead")
	rollupExitRoot := common.HexToHash("0xbeef")

	bridge := &bridgesync.Bridge{
		OriginNetwork:      0,
		OriginAddress:      common.HexToAddress("0xAAAA"),
		DestinationNetwork: 1,
		DestinationAddress: common.HexToAddress("0xBBBB"),
		Amount:             big.NewInt(42),
		DepositCount:       depositCount,
	}
	info := &l1infotreesync.L1InfoTreeLeaf{
		MainnetExitRoot: mainnetExitRoot,
		RollupExitRoot:  rollupExitRoot,
	}

	localProof := syntheticProof(0x01)
	// For mainnet (networkID==0), GetRollupExitTreeMerkleProof returns empty proof per bridgeservice convention.
	var emptyRollupProof treetypes.Proof

	bs := &mockBridgeSyncer{bridge: bridge, proof: localProof}
	l1t := &mockL1InfoTree{info: info, rollupProof: emptyRollupProof}

	pb := proofbuilder.New(bs, l1t, 0, nil)

	claim, err := pb.Build(context.Background(), globalIndex)
	require.NoError(t, err)

	require.Equal(t, uint32(0), claim.OriginNetwork)
	require.Equal(t, [32]byte(mainnetExitRoot), claim.MainnetExitRoot)
}

// TestBuild_MissingBridgeEvent verifies ErrNotReady is returned when bridge is not found.
func TestBuild_MissingBridgeEvent(t *testing.T) {
	t.Parallel()

	globalIndex := rollupGlobalIndex(0, 99)

	bs := &mockBridgeSyncer{errGet: db.ErrNotFound}
	l1t := &mockL1InfoTree{info: &l1infotreesync.L1InfoTreeLeaf{}}

	pb := proofbuilder.New(bs, l1t, 1, nil)

	_, err := pb.Build(context.Background(), globalIndex)
	require.Error(t, err)
	require.True(t, errors.Is(err, proofbuilder.ErrNotReady),
		"expected ErrNotReady, got: %v", err)
}

// TestBuild_L1InfoTreeNotSynced verifies ErrNotReady is returned when l1infotreesync has no data.
func TestBuild_L1InfoTreeNotSynced(t *testing.T) {
	t.Parallel()

	globalIndex := rollupGlobalIndex(0, 5)

	bridge := &bridgesync.Bridge{
		OriginNetwork: 1,
		Amount:        big.NewInt(10),
		DepositCount:  5,
	}

	bs := &mockBridgeSyncer{bridge: bridge}
	l1t := &mockL1InfoTree{errInfo: db.ErrNotFound}

	pb := proofbuilder.New(bs, l1t, 1, nil)

	_, err := pb.Build(context.Background(), globalIndex)
	require.Error(t, err)
	require.True(t, errors.Is(err, proofbuilder.ErrNotReady),
		"expected ErrNotReady, got: %v", err)
}

// TestBuild_L1InfoTreeNotSyncedNativeErr verifies ErrNotReady for l1infotreesync.ErrNotFound too.
func TestBuild_L1InfoTreeNotSyncedNativeErr(t *testing.T) {
	t.Parallel()

	globalIndex := rollupGlobalIndex(0, 5)

	bridge := &bridgesync.Bridge{
		OriginNetwork: 1,
		Amount:        big.NewInt(10),
		DepositCount:  5,
	}

	bs := &mockBridgeSyncer{bridge: bridge}
	l1t := &mockL1InfoTree{errInfo: l1infotreesync.ErrNotFound}

	pb := proofbuilder.New(bs, l1t, 1, nil)

	_, err := pb.Build(context.Background(), globalIndex)
	require.Error(t, err)
	require.True(t, errors.Is(err, proofbuilder.ErrNotReady),
		"expected ErrNotReady, got: %v", err)
}

// TestBuild_LocalExitRootNotFound verifies ErrNotReady when local exit root is missing for a rollup.
func TestBuild_LocalExitRootNotFound(t *testing.T) {
	t.Parallel()

	globalIndex := rollupGlobalIndex(2, 5) // originNetwork = 3

	bridge := &bridgesync.Bridge{
		OriginNetwork: 3,
		Amount:        big.NewInt(10),
		DepositCount:  5,
	}
	info := &l1infotreesync.L1InfoTreeLeaf{
		MainnetExitRoot: common.HexToHash("0x1"),
		RollupExitRoot:  common.HexToHash("0x2"),
	}

	bs := &mockBridgeSyncer{bridge: bridge}
	l1t := &mockL1InfoTree{info: info, errLER: db.ErrNotFound}

	pb := proofbuilder.New(bs, l1t, 3, nil)

	_, err := pb.Build(context.Background(), globalIndex)
	require.Error(t, err)
	require.True(t, errors.Is(err, proofbuilder.ErrNotReady),
		"expected ErrNotReady, got: %v", err)
}

// TestBuild_LocalProofNotFound verifies ErrNotReady when the local SMT proof is missing.
func TestBuild_LocalProofNotFound(t *testing.T) {
	t.Parallel()

	globalIndex := rollupGlobalIndex(2, 5)

	bridge := &bridgesync.Bridge{
		OriginNetwork: 3,
		Amount:        big.NewInt(10),
		DepositCount:  5,
	}
	info := &l1infotreesync.L1InfoTreeLeaf{
		MainnetExitRoot: common.HexToHash("0x1"),
		RollupExitRoot:  common.HexToHash("0x2"),
	}

	bs := &mockBridgeSyncer{bridge: bridge, errPrf: db.ErrNotFound}
	l1t := &mockL1InfoTree{info: info, ler: common.HexToHash("0xc")}

	pb := proofbuilder.New(bs, l1t, 3, nil)

	_, err := pb.Build(context.Background(), globalIndex)
	require.Error(t, err)
	require.True(t, errors.Is(err, proofbuilder.ErrNotReady),
		"expected ErrNotReady, got: %v", err)
}

// TestBuild_RollupProofNotFound verifies ErrNotReady when the rollup exit SMT proof is missing.
func TestBuild_RollupProofNotFound(t *testing.T) {
	t.Parallel()

	globalIndex := rollupGlobalIndex(2, 5)

	bridge := &bridgesync.Bridge{
		OriginNetwork: 3,
		Amount:        big.NewInt(10),
		DepositCount:  5,
	}
	info := &l1infotreesync.L1InfoTreeLeaf{
		MainnetExitRoot: common.HexToHash("0x1"),
		RollupExitRoot:  common.HexToHash("0x2"),
	}

	bs := &mockBridgeSyncer{bridge: bridge, proof: syntheticProof(0x01)}
	l1t := &mockL1InfoTree{info: info, ler: common.HexToHash("0xc"), errProof: db.ErrNotFound}

	pb := proofbuilder.New(bs, l1t, 3, nil)

	_, err := pb.Build(context.Background(), globalIndex)
	require.Error(t, err)
	require.True(t, errors.Is(err, proofbuilder.ErrNotReady),
		"expected ErrNotReady, got: %v", err)
}

// TestBuild_NilGlobalIndex verifies ErrNotReady is returned for a nil globalIndex.
func TestBuild_NilGlobalIndex(t *testing.T) {
	t.Parallel()

	bs := &mockBridgeSyncer{}
	l1t := &mockL1InfoTree{}

	pb := proofbuilder.New(bs, l1t, 1, nil)

	_, err := pb.Build(context.Background(), nil)
	require.Error(t, err)
	require.True(t, errors.Is(err, proofbuilder.ErrNotReady),
		"expected ErrNotReady, got: %v", err)
}

// TestBuild_NilAmountBecomesZero verifies bridge.Amount==nil results in Amount=0 in the claim.
func TestBuild_NilAmountBecomesZero(t *testing.T) {
	t.Parallel()

	globalIndex := mainnetGlobalIndex(1)

	bridge := &bridgesync.Bridge{
		OriginNetwork: 0,
		Amount:        nil, // nil amount
		DepositCount:  1,
	}
	info := &l1infotreesync.L1InfoTreeLeaf{
		MainnetExitRoot: common.HexToHash("0x11"),
		RollupExitRoot:  common.HexToHash("0x22"),
	}

	bs := &mockBridgeSyncer{bridge: bridge, proof: syntheticProof(0x05)}
	l1t := &mockL1InfoTree{info: info}

	pb := proofbuilder.New(bs, l1t, 0, nil)

	claim, err := pb.Build(context.Background(), globalIndex)
	require.NoError(t, err)
	require.NotNil(t, claim.Amount)
	require.Equal(t, int64(0), claim.Amount.Int64())
}
