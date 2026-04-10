package claimsync

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"path"
	"testing"
	"time"

	claimsyncStorage "github.com/agglayer/aggkit/claimsync/storage"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	claimstoragemocks "github.com/agglayer/aggkit/claimsync/types/mocks"
	dbmocks "github.com/agglayer/aggkit/db/mocks"
	logger "github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const processorTestDBQueryTimeout = 30 * time.Second

func newTestProcessor(t *testing.T) *processor {
	t.Helper()
	lg := logger.WithFields("module", "test-processor")
	store, err := claimsyncStorage.NewStandalone(lg,
		path.Join(t.TempDir(), "processor.db"),
		"test-syncer",
		processorTestDBQueryTimeout,
	)
	require.NoError(t, err)
	return newProcessor(lg, store, processorTestDBQueryTimeout)
}

// --- Test data ---

var (
	procBlock1 = sync.Block{
		Num:  1,
		Hash: common.HexToHash("0x01"),
		Events: []any{
			Event{Claim: &Claim{
				BlockNum:    1,
				BlockPos:    0,
				TxHash:      common.HexToHash("0xa1"),
				GlobalIndex: big.NewInt(10),
			}},
			Event{UnsetClaim: &UnsetClaim{
				BlockNum:                  1,
				BlockPos:                  1,
				TxHash:                    common.HexToHash("0xa2"),
				GlobalIndex:               big.NewInt(20),
				UnsetGlobalIndexHashChain: common.HexToHash("0xff"),
			}},
		},
	}
	procBlock3 = sync.Block{
		Num:  3,
		Hash: common.HexToHash("0x03"),
		Events: []any{
			Event{Claim: &Claim{
				BlockNum:    3,
				BlockPos:    0,
				TxHash:      common.HexToHash("0xb1"),
				GlobalIndex: big.NewInt(30),
			}},
			Event{SetClaim: &SetClaim{
				BlockNum:    3,
				BlockPos:    1,
				TxHash:      common.HexToHash("0xb2"),
				GlobalIndex: big.NewInt(40),
			}},
		},
	}
	procBlock4 = sync.Block{Num: 4, Hash: common.HexToHash("0x04"), Events: []any{}}
	procBlock5 = sync.Block{
		Num:  5,
		Hash: common.HexToHash("0x05"),
		Events: []any{
			Event{Claim: &Claim{
				BlockNum:    5,
				BlockPos:    0,
				TxHash:      common.HexToHash("0xc1"),
				GlobalIndex: big.NewInt(50),
			}},
		},
	}
)

// --- Action interfaces (mirroring bridgesync/processor_test.go pattern) ---

type procTestAction interface {
	method() string
	desc() string
	execute(t *testing.T)
}

// processBlockAction

type procProcessBlockAction struct {
	p           *processor
	description string
	block       sync.Block
	expectedErr error
}

func (a *procProcessBlockAction) method() string { return "ProcessBlock" }
func (a *procProcessBlockAction) desc() string   { return a.description }
func (a *procProcessBlockAction) execute(t *testing.T) {
	t.Helper()
	err := a.p.ProcessBlock(context.Background(), a.block)
	require.Equal(t, a.expectedErr, err)
}

// reorgAction

type procReorgAction struct {
	p                 *processor
	description       string
	firstReorgedBlock uint64
	expectedErr       error
}

func (a *procReorgAction) method() string { return "Reorg" }
func (a *procReorgAction) desc() string   { return a.description }
func (a *procReorgAction) execute(t *testing.T) {
	t.Helper()
	err := a.p.Reorg(context.Background(), a.firstReorgedBlock)
	require.Equal(t, a.expectedErr, err)
}

// getLastProcessedBlockAction

type procGetLastAction struct {
	p             *processor
	description   string
	expectedBlock uint64
	expectedFound bool
}

func (a *procGetLastAction) method() string { return "GetLastProcessedBlock" }
func (a *procGetLastAction) desc() string   { return a.description }
func (a *procGetLastAction) execute(t *testing.T) {
	t.Helper()
	block, found, err := a.p.GetLastProcessedBlock(context.Background())
	require.NoError(t, err)
	require.Equal(t, a.expectedFound, found)
	if found {
		require.Equal(t, a.expectedBlock, block)
	}
}

// getFirstProcessedBlockAction

type procGetFirstAction struct {
	p             *processor
	description   string
	expectedBlock uint64
	expectedFound bool
}

func (a *procGetFirstAction) method() string { return "GetFirstProcessedBlock" }
func (a *procGetFirstAction) desc() string   { return a.description }
func (a *procGetFirstAction) execute(t *testing.T) {
	t.Helper()
	block, found, err := a.p.GetFirstProcessedBlock(context.Background())
	require.NoError(t, err)
	require.Equal(t, a.expectedFound, found)
	if found {
		require.Equal(t, a.expectedBlock, block)
	}
}

// --- Integration tests ---

func TestProcessor(t *testing.T) {
	p := newTestProcessor(t)

	actions := []procTestAction{
		// empty state
		&procGetLastAction{p: p, description: "empty: no last block", expectedFound: false},
		&procGetFirstAction{p: p, description: "empty: no first block", expectedFound: false},
		&procReorgAction{p: p, description: "reorg on empty (block 0)", firstReorgedBlock: 0},
		&procReorgAction{p: p, description: "reorg on empty (block 1)", firstReorgedBlock: 1},

		// process block1
		&procProcessBlockAction{p: p, description: "process block1", block: procBlock1},
		&procGetLastAction{p: p, description: "after block1: last=1", expectedFound: true, expectedBlock: 1},
		&procGetFirstAction{p: p, description: "after block1: first=1", expectedFound: true, expectedBlock: 1},

		// reorg block1 away
		&procReorgAction{p: p, description: "reorg block1", firstReorgedBlock: 1},
		&procGetLastAction{p: p, description: "after reorg block1: no last", expectedFound: false},

		// process block1 again, then block3
		&procProcessBlockAction{p: p, description: "process block1 again", block: procBlock1},
		&procProcessBlockAction{p: p, description: "process block3", block: procBlock3},
		&procGetLastAction{p: p, description: "after block3: last=3", expectedFound: true, expectedBlock: 3},
		&procGetFirstAction{p: p, description: "after block3: first=1", expectedFound: true, expectedBlock: 1},

		// reorg from block3 → only block1 remains
		&procReorgAction{p: p, description: "reorg block3", firstReorgedBlock: 3},
		&procGetLastAction{p: p, description: "after reorg block3: last=1", expectedFound: true, expectedBlock: 1},

		// process block3, block4, block5
		&procProcessBlockAction{p: p, description: "process block3 again", block: procBlock3},
		&procProcessBlockAction{p: p, description: "process empty block4", block: procBlock4},
		&procProcessBlockAction{p: p, description: "process block5", block: procBlock5},
		&procGetLastAction{p: p, description: "after block5: last=5", expectedFound: true, expectedBlock: 5},
		&procGetFirstAction{p: p, description: "after block5: first=1", expectedFound: true, expectedBlock: 1},

		// reorg last block only
		&procReorgAction{p: p, description: "reorg block5", firstReorgedBlock: 5},
		&procGetLastAction{p: p, description: "after reorg block5: last=4", expectedFound: true, expectedBlock: 4},
	}

	for _, a := range actions {
		t.Logf("%s: %s", a.method(), a.desc())
		a.execute(t)
	}
}

// --- GetBoundaryBlockForClaimType ---

func TestProcessor_GetBoundaryBlockForClaimType(t *testing.T) {
	t.Parallel()
	p := newTestProcessor(t)
	ctx := context.Background()

	b1 := sync.Block{
		Num:  1,
		Hash: common.HexToHash("0x01"),
		Events: []any{
			Event{Claim: &Claim{BlockNum: 1, BlockPos: 0, TxHash: common.HexToHash("0x1"), GlobalIndex: big.NewInt(1), Type: claimsynctypes.ClaimEvent}},
		},
	}
	b3 := sync.Block{
		Num:  3,
		Hash: common.HexToHash("0x03"),
		Events: []any{
			Event{Claim: &Claim{BlockNum: 3, BlockPos: 0, TxHash: common.HexToHash("0x3"), GlobalIndex: big.NewInt(3), Type: claimsynctypes.ClaimEvent}},
		},
	}
	require.NoError(t, p.ProcessBlock(ctx, b1))
	require.NoError(t, p.ProcessBlock(ctx, b3))

	blockNum, err := p.GetBoundaryBlockForClaimType(ctx, nil, claimsynctypes.ClaimEvent)
	require.NoError(t, err)
	require.Equal(t, uint64(3), blockNum)
}

// --- ProcessBlock error paths (mocks) ---

func newMockProcessor(t *testing.T) (*processor, *claimstoragemocks.ClaimStorager) {
	t.Helper()
	lg := logger.WithFields("module", "test-mock-processor")
	storageMock := claimstoragemocks.NewClaimStorager(t)
	proc := newProcessor(lg, storageMock, processorTestDBQueryTimeout)
	return proc, storageMock
}

func TestProcessBlock_NewTxError(t *testing.T) {
	t.Parallel()
	proc, storageMock := newMockProcessor(t)
	storageMock.EXPECT().NewTx(mock.Anything).Return(nil, errors.New("connection failed"))

	err := proc.ProcessBlock(t.Context(), sync.Block{Num: 1})
	require.ErrorContains(t, err, "connection failed")
}

func TestProcessBlock_InsertBlockError(t *testing.T) {
	t.Parallel()
	proc, storageMock := newMockProcessor(t)
	tx := dbmocks.NewTxer(t)

	storageMock.EXPECT().NewTx(mock.Anything).Return(tx, nil)
	storageMock.EXPECT().InsertBlock(mock.Anything, tx, uint64(1), common.Hash{}).Return(errors.New("insert block failed"))
	tx.EXPECT().Rollback().Return(nil)

	err := proc.ProcessBlock(t.Context(), sync.Block{Num: 1})
	require.ErrorContains(t, err, "insert block failed")
}

func TestProcessBlock_ProcessEventError(t *testing.T) {
	t.Parallel()
	proc, storageMock := newMockProcessor(t)
	tx := dbmocks.NewTxer(t)
	embMock := claimstoragemocks.NewEmbeddedProcessor(t)
	proc.embeddedProcessor = embMock

	block := sync.Block{Num: 2, Events: []any{Event{}}}
	storageMock.EXPECT().NewTx(mock.Anything).Return(tx, nil)
	storageMock.EXPECT().InsertBlock(mock.Anything, tx, block.Num, block.Hash).Return(nil)
	embMock.EXPECT().ProcessBlockWithTx(mock.Anything, tx, mock.Anything, Event{}).Return(errors.New("event error"))
	tx.EXPECT().Rollback().Return(nil)

	err := proc.ProcessBlock(t.Context(), block)
	require.ErrorContains(t, err, "event error")
}

func TestProcessBlock_CommitError(t *testing.T) {
	t.Parallel()
	proc, storageMock := newMockProcessor(t)
	tx := dbmocks.NewTxer(t)

	block := sync.Block{Num: 3}
	storageMock.EXPECT().NewTx(mock.Anything).Return(tx, nil)
	storageMock.EXPECT().InsertBlock(mock.Anything, tx, block.Num, block.Hash).Return(nil)
	tx.EXPECT().Commit().Return(errors.New("commit failed"))
	tx.EXPECT().Rollback().Return(nil)

	err := proc.ProcessBlock(t.Context(), block)
	require.ErrorContains(t, err, "commit failed")
}

func TestProcessBlock_RollbackErrTxDone(t *testing.T) {
	t.Parallel()
	// rollbackTx must not log an error when Rollback returns sql.ErrTxDone
	proc, storageMock := newMockProcessor(t)
	tx := dbmocks.NewTxer(t)

	storageMock.EXPECT().NewTx(mock.Anything).Return(tx, nil)
	storageMock.EXPECT().InsertBlock(mock.Anything, tx, uint64(1), common.Hash{}).Return(errors.New("trigger rollback"))
	tx.EXPECT().Rollback().Return(sql.ErrTxDone) // must be silenced

	err := proc.ProcessBlock(t.Context(), sync.Block{Num: 1})
	require.Error(t, err) // the InsertBlock error propagates
}

// --- Reorg error paths (mocks) ---

func TestReorg_NewTxError(t *testing.T) {
	t.Parallel()
	proc, storageMock := newMockProcessor(t)
	storageMock.EXPECT().NewTx(mock.Anything).Return(nil, errors.New("tx error"))

	err := proc.Reorg(t.Context(), 5)
	require.ErrorContains(t, err, "claimsync Reorg: start tx")
	require.ErrorContains(t, err, "tx error")
}

func TestReorg_EmbeddedProcessorError(t *testing.T) {
	t.Parallel()
	proc, storageMock := newMockProcessor(t)
	tx := dbmocks.NewTxer(t)
	embMock := claimstoragemocks.NewEmbeddedProcessor(t)
	proc.embeddedProcessor = embMock

	storageMock.EXPECT().NewTx(mock.Anything).Return(tx, nil)
	embMock.EXPECT().ReorgWithTx(mock.Anything, tx, uint64(5)).Return(int64(0), errors.New("delete failed"))
	tx.EXPECT().Rollback().Return(nil)

	err := proc.Reorg(t.Context(), 5)
	require.ErrorContains(t, err, "claimsync Reorg")
	require.ErrorContains(t, err, "delete failed")
}

func TestReorg_CommitError(t *testing.T) {
	t.Parallel()
	proc, storageMock := newMockProcessor(t)
	tx := dbmocks.NewTxer(t)
	embMock := claimstoragemocks.NewEmbeddedProcessor(t)
	proc.embeddedProcessor = embMock

	storageMock.EXPECT().NewTx(mock.Anything).Return(tx, nil)
	embMock.EXPECT().ReorgWithTx(mock.Anything, tx, uint64(5)).Return(int64(3), nil)
	tx.EXPECT().Commit().Return(errors.New("commit failed"))
	tx.EXPECT().Rollback().Return(nil)

	err := proc.Reorg(t.Context(), 5)
	require.ErrorContains(t, err, "claimsync Reorg: commit")
}

// --- GetLastProcessedBlock / GetFirstProcessedBlock via mock ---

func TestGetLastProcessedBlock_Delegates(t *testing.T) {
	t.Parallel()
	proc, storageMock := newMockProcessor(t)
	storageMock.EXPECT().GetLastProcessedBlock(mock.Anything, nil).Return(uint64(42), true, nil)

	block, found, err := proc.GetLastProcessedBlock(t.Context())
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(42), block)
}

func TestGetFirstProcessedBlock_Delegates(t *testing.T) {
	t.Parallel()
	proc, storageMock := newMockProcessor(t)
	storageMock.EXPECT().GetFirstProcessedBlock(mock.Anything, nil).Return(uint64(1), true, nil)

	block, found, err := proc.GetFirstProcessedBlock(t.Context())
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(1), block)
}
