package gertracker_test

import (
	"context"
	"errors"
	"testing"
	"unsafe"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/agglayer/aggkit/autoclaim/gertracker"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/l1infotreesync"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// fakeSubscription is a minimal ethereum.Subscription for tests.
type fakeSubscription struct {
	errCh chan error
}

func (f *fakeSubscription) Unsubscribe()      {}
func (f *fakeSubscription) Err() <-chan error { return f.errCh }

// insertIteratorLayout mirrors the unexported layout of Agglayergerl2UpdateHashChainValueIterator.
// The field order MUST match the struct declaration in agglayergerl2.go.
type insertIteratorLayout struct {
	Event    *agglayergerl2.Agglayergerl2UpdateHashChainValue
	contract *bind.BoundContract
	event    string
	logs     chan ethtypes.Log
	sub      ethereum.Subscription
	done     bool
	fail     error
}

// removalIteratorLayout mirrors the unexported layout of Agglayergerl2UpdateRemovalHashChainValueIterator.
type removalIteratorLayout struct {
	Event    *agglayergerl2.Agglayergerl2UpdateRemovalHashChainValue
	contract *bind.BoundContract
	event    string
	logs     chan ethtypes.Log
	sub      ethereum.Subscription
	done     bool
	fail     error
}

// parsedABI is a shared parsed ABI for building fake log iterators.
var parsedABI, _ = agglayergerl2.Agglayergerl2MetaData.GetAbi()

// updateHashChainEventSig is the keccak256 hash of the UpdateHashChainValue event signature.
var updateHashChainEventSig = crypto.Keccak256Hash([]byte("UpdateHashChainValue(bytes32,bytes32)"))

// updateRemovalHashChainEventSig is the keccak256 hash of the UpdateRemovalHashChainValue event signature.
var updateRemovalHashChainEventSig = crypto.Keccak256Hash([]byte("UpdateRemovalHashChainValue(bytes32,bytes32)"))

// newInsertIterator builds a fake insert iterator pre-loaded with the given events.
func newInsertIterator(events []insertEvent) *agglayergerl2.Agglayergerl2UpdateHashChainValueIterator {
	bc := bind.NewBoundContract(common.Address{}, *parsedABI, nil, nil, nil)
	logsCh := make(chan ethtypes.Log, len(events))
	for _, e := range events {
		logsCh <- ethtypes.Log{
			Topics:      []common.Hash{updateHashChainEventSig, e.ger, {}},
			BlockNumber: e.blockNumber,
			Index:       e.logIndex,
		}
	}

	it := &agglayergerl2.Agglayergerl2UpdateHashChainValueIterator{}
	layout := (*insertIteratorLayout)(unsafe.Pointer(it))
	layout.contract = bc
	layout.event = "UpdateHashChainValue"
	layout.logs = logsCh
	layout.done = true
	layout.sub = &fakeSubscription{errCh: make(chan error)}
	return it
}

// newRemovalIterator builds a fake removal iterator pre-loaded with the given events.
func newRemovalIterator(events []removalEvent) *agglayergerl2.Agglayergerl2UpdateRemovalHashChainValueIterator {
	bc := bind.NewBoundContract(common.Address{}, *parsedABI, nil, nil, nil)
	logsCh := make(chan ethtypes.Log, len(events))
	for _, e := range events {
		logsCh <- ethtypes.Log{
			Topics:      []common.Hash{updateRemovalHashChainEventSig, e.ger, {}},
			BlockNumber: e.blockNumber,
			Index:       e.logIndex,
		}
	}

	it := &agglayergerl2.Agglayergerl2UpdateRemovalHashChainValueIterator{}
	layout := (*removalIteratorLayout)(unsafe.Pointer(it))
	layout.contract = bc
	layout.event = "UpdateRemovalHashChainValue"
	layout.logs = logsCh
	layout.done = true
	layout.sub = &fakeSubscription{errCh: make(chan error)}
	return it
}

type insertEvent struct {
	ger         common.Hash
	blockNumber uint64
	logIndex    uint
}

type removalEvent struct {
	ger         common.Hash
	blockNumber uint64
	logIndex    uint
}

// --- Fakes ---

// fakeL2GERManager is a hand-written fake for L2GERManagerContract.
type fakeL2GERManager struct {
	insertIterator  *agglayergerl2.Agglayergerl2UpdateHashChainValueIterator
	insertErr       error
	removalIterator *agglayergerl2.Agglayergerl2UpdateRemovalHashChainValueIterator
	removalErr      error
}

func (f *fakeL2GERManager) FilterUpdateHashChainValue(
	_ *bind.FilterOpts,
	_ [][32]byte,
	_ [][32]byte,
) (*agglayergerl2.Agglayergerl2UpdateHashChainValueIterator, error) {
	return f.insertIterator, f.insertErr
}

func (f *fakeL2GERManager) FilterUpdateRemovalHashChainValue(
	_ *bind.FilterOpts,
	_ [][32]byte,
	_ [][32]byte,
) (*agglayergerl2.Agglayergerl2UpdateRemovalHashChainValueIterator, error) {
	return f.removalIterator, f.removalErr
}

// fakeL1InfoTreeSyncer is a hand-written fake for L1InfoTreeSyncer.
type fakeL1InfoTreeSyncer struct {
	leaf *l1infotreesync.L1InfoTreeLeaf
	err  error
}

func (f *fakeL1InfoTreeSyncer) GetInfoByGlobalExitRoot(_ common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error) {
	return f.leaf, f.err
}

// newTestGERTracker is a convenience wrapper around gertracker.NewTestGERTracker.
func newTestGERTracker(l2GERManager gertracker.L2GERManagerContract, l1InfoTreeSync gertracker.L1InfoTreeSyncer) gertracker.GERTracker {
	return gertracker.NewTestGERTracker(l2GERManager, l1InfoTreeSync)
}

// --- Tests ---

func TestLatestInjectedGER_FoundAndResolved(t *testing.T) {
	t.Parallel()

	ger := common.HexToHash("0xaaaa")
	insert := newInsertIterator([]insertEvent{{ger: ger, blockNumber: 10, logIndex: 0}})
	removal := newRemovalIterator(nil)

	leaf := &l1infotreesync.L1InfoTreeLeaf{L1InfoTreeIndex: 7}
	tracker := newTestGERTracker(
		&fakeL2GERManager{insertIterator: insert, removalIterator: removal},
		&fakeL1InfoTreeSyncer{leaf: leaf},
	)

	gotHash, gotIndex, err := tracker.LatestInjectedGER(context.Background())
	require.NoError(t, err)
	require.NotNil(t, gotHash)
	require.Equal(t, ger, *gotHash)
	require.Equal(t, uint32(7), gotIndex)
}

func TestLatestInjectedGER_NoGERInjectedYet(t *testing.T) {
	t.Parallel()

	insert := newInsertIterator(nil)
	removal := newRemovalIterator(nil)

	tracker := newTestGERTracker(
		&fakeL2GERManager{insertIterator: insert, removalIterator: removal},
		&fakeL1InfoTreeSyncer{},
	)

	gotHash, gotIndex, err := tracker.LatestInjectedGER(context.Background())
	require.NoError(t, err)
	require.Nil(t, gotHash)
	require.Equal(t, uint32(0), gotIndex)
}

func TestLatestInjectedGER_GERRemoved(t *testing.T) {
	t.Parallel()

	ger := common.HexToHash("0xbbbb")
	insert := newInsertIterator([]insertEvent{{ger: ger, blockNumber: 5, logIndex: 0}})
	removal := newRemovalIterator([]removalEvent{{ger: ger, blockNumber: 6, logIndex: 0}})

	tracker := newTestGERTracker(
		&fakeL2GERManager{insertIterator: insert, removalIterator: removal},
		&fakeL1InfoTreeSyncer{},
	)

	gotHash, gotIndex, err := tracker.LatestInjectedGER(context.Background())
	require.NoError(t, err)
	require.Nil(t, gotHash)
	require.Equal(t, uint32(0), gotIndex)
}

func TestLatestInjectedGER_SyncerLagging(t *testing.T) {
	t.Parallel()

	ger := common.HexToHash("0xcccc")
	insert := newInsertIterator([]insertEvent{{ger: ger, blockNumber: 3, logIndex: 0}})
	removal := newRemovalIterator(nil)

	tracker := newTestGERTracker(
		&fakeL2GERManager{insertIterator: insert, removalIterator: removal},
		&fakeL1InfoTreeSyncer{err: db.ErrNotFound},
	)

	gotHash, gotIndex, err := tracker.LatestInjectedGER(context.Background())
	require.NoError(t, err)
	require.Nil(t, gotHash)
	require.Equal(t, uint32(0), gotIndex)
}

func TestLatestInjectedGER_RPCError(t *testing.T) {
	t.Parallel()

	rpcErr := errors.New("connection refused")
	tracker := newTestGERTracker(
		&fakeL2GERManager{insertErr: rpcErr},
		&fakeL1InfoTreeSyncer{},
	)

	gotHash, gotIndex, err := tracker.LatestInjectedGER(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, rpcErr)
	require.Nil(t, gotHash)
	require.Equal(t, uint32(0), gotIndex)
}

func TestLatestInjectedGER_MultipleGERsLatestWins(t *testing.T) {
	t.Parallel()

	gerOld := common.HexToHash("0x0001")
	gerNew := common.HexToHash("0x0002")
	insert := newInsertIterator([]insertEvent{
		{ger: gerOld, blockNumber: 5, logIndex: 0},
		{ger: gerNew, blockNumber: 10, logIndex: 0},
	})
	removal := newRemovalIterator(nil)

	leaf := &l1infotreesync.L1InfoTreeLeaf{L1InfoTreeIndex: 42}
	tracker := newTestGERTracker(
		&fakeL2GERManager{insertIterator: insert, removalIterator: removal},
		&fakeL1InfoTreeSyncer{leaf: leaf},
	)

	gotHash, gotIndex, err := tracker.LatestInjectedGER(context.Background())
	require.NoError(t, err)
	require.NotNil(t, gotHash)
	require.Equal(t, gerNew, *gotHash)
	require.Equal(t, uint32(42), gotIndex)
}
