package claimsync

import (
	"errors"
	"math/big"
	"testing"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	claimstoragemocks "github.com/agglayer/aggkit/claimsync/types/mocks"
	"github.com/agglayer/aggkit/db"
	dbmocks "github.com/agglayer/aggkit/db/mocks"
	logger "github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var errEmbeddedUnittest = errors.New("embedded unittest error")

func newTestEmbeddedProcessor(t *testing.T) (*claimEmbeddedProcessor, *claimstoragemocks.ClaimStorager) {
	t.Helper()
	storageMock := claimstoragemocks.NewClaimStorager(t)
	lg := logger.WithFields("module", "test")
	return newEmbeddedProcessor(lg, storageMock), storageMock
}

// --- Event.String ---

func TestEvent_String_AllNil(t *testing.T) {
	t.Parallel()
	e := Event{}
	require.Equal(t, "claimsync.Event{}", e.String())
}

func TestEvent_String_ClaimOnly(t *testing.T) {
	t.Parallel()
	e := Event{Claim: &Claim{BlockNum: 1, GlobalIndex: big.NewInt(10)}}
	s := e.String()
	require.Contains(t, s, "claimsync.Event{")
	require.Contains(t, s, "Claim{")
}

func TestEvent_String_UnsetClaimOnly(t *testing.T) {
	t.Parallel()
	e := Event{UnsetClaim: &UnsetClaim{BlockNum: 2, GlobalIndex: big.NewInt(20)}}
	s := e.String()
	require.Contains(t, s, "claimsync.Event{")
	require.Contains(t, s, "UnsetClaim{")
}

func TestEvent_String_SetClaimOnly(t *testing.T) {
	t.Parallel()
	e := Event{SetClaim: &SetClaim{BlockNum: 3, GlobalIndex: big.NewInt(30)}}
	s := e.String()
	require.Contains(t, s, "claimsync.Event{")
	require.Contains(t, s, "SetClaim{")
}

func TestEvent_String_AllThree(t *testing.T) {
	t.Parallel()
	e := Event{
		Claim:      &Claim{BlockNum: 1, GlobalIndex: big.NewInt(10)},
		UnsetClaim: &UnsetClaim{BlockNum: 1, GlobalIndex: big.NewInt(20)},
		SetClaim:   &SetClaim{BlockNum: 1, GlobalIndex: big.NewInt(30)},
	}
	s := e.String()
	require.Contains(t, s, "Claim{")
	require.Contains(t, s, "UnsetClaim{")
	require.Contains(t, s, "SetClaim{")
}

// --- ProcessBlockWithTx ---

func TestProcessBlockWithTx_WrongEventType(t *testing.T) {
	t.Parallel()
	proc, _ := newTestEmbeddedProcessor(t)
	block := sync.Block{Num: 1}

	err := proc.ProcessBlockWithTx(t.Context(), nil, block, "not-an-event")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected event type")
}

func TestProcessBlockWithTx_EmptyEvent(t *testing.T) {
	t.Parallel()
	proc, _ := newTestEmbeddedProcessor(t)
	block := sync.Block{Num: 1}

	err := proc.ProcessBlockWithTx(t.Context(), nil, block, Event{})
	require.NoError(t, err)
}

func TestProcessBlockWithTx_ClaimOnly(t *testing.T) {
	t.Parallel()
	proc, storageMock := newTestEmbeddedProcessor(t)
	tx := dbmocks.NewQuerier(t)
	claim := Claim{BlockNum: 5, TxHash: common.HexToHash("0x1"), GlobalIndex: big.NewInt(100)}
	block := sync.Block{Num: 5}

	storageMock.EXPECT().InsertClaim(mock.Anything, tx, claim).Return(nil)

	err := proc.ProcessBlockWithTx(t.Context(), tx, block, Event{Claim: &claim})
	require.NoError(t, err)
}

func TestProcessBlockWithTx_UnsetClaimOnly(t *testing.T) {
	t.Parallel()
	proc, storageMock := newTestEmbeddedProcessor(t)
	tx := dbmocks.NewQuerier(t)
	unset := UnsetClaim{BlockNum: 5, TxHash: common.HexToHash("0x2"), GlobalIndex: big.NewInt(200)}
	block := sync.Block{Num: 5}

	storageMock.EXPECT().InsertUnsetClaim(mock.Anything, tx, unset).Return(nil)

	err := proc.ProcessBlockWithTx(t.Context(), tx, block, Event{UnsetClaim: &unset})
	require.NoError(t, err)
}

func TestProcessBlockWithTx_SetClaimOnly(t *testing.T) {
	t.Parallel()
	proc, storageMock := newTestEmbeddedProcessor(t)
	tx := dbmocks.NewQuerier(t)
	set := SetClaim{BlockNum: 5, TxHash: common.HexToHash("0x3"), GlobalIndex: big.NewInt(300)}
	block := sync.Block{Num: 5}

	storageMock.EXPECT().InsertSetClaim(mock.Anything, tx, set).Return(nil)

	err := proc.ProcessBlockWithTx(t.Context(), tx, block, Event{SetClaim: &set})
	require.NoError(t, err)
}

func TestProcessBlockWithTx_AllThreeEvents(t *testing.T) {
	t.Parallel()
	proc, storageMock := newTestEmbeddedProcessor(t)
	tx := dbmocks.NewQuerier(t)
	claim := Claim{BlockNum: 7, TxHash: common.HexToHash("0xA"), GlobalIndex: big.NewInt(1)}
	unset := UnsetClaim{BlockNum: 7, TxHash: common.HexToHash("0xB"), GlobalIndex: big.NewInt(2)}
	set := SetClaim{BlockNum: 7, TxHash: common.HexToHash("0xC"), GlobalIndex: big.NewInt(3)}
	block := sync.Block{Num: 7}

	storageMock.EXPECT().InsertClaim(mock.Anything, tx, claim).Return(nil)
	storageMock.EXPECT().InsertUnsetClaim(mock.Anything, tx, unset).Return(nil)
	storageMock.EXPECT().InsertSetClaim(mock.Anything, tx, set).Return(nil)

	err := proc.ProcessBlockWithTx(t.Context(), tx, block, Event{
		Claim: &claim, UnsetClaim: &unset, SetClaim: &set,
	})
	require.NoError(t, err)
}

func TestProcessBlockWithTx_InsertClaimError(t *testing.T) {
	t.Parallel()
	proc, storageMock := newTestEmbeddedProcessor(t)
	tx := dbmocks.NewQuerier(t)
	claim := Claim{BlockNum: 1, GlobalIndex: big.NewInt(1)}
	block := sync.Block{Num: 1}

	storageMock.EXPECT().InsertClaim(mock.Anything, tx, claim).Return(errEmbeddedUnittest)

	err := proc.ProcessBlockWithTx(t.Context(), tx, block, Event{Claim: &claim})
	require.ErrorIs(t, err, errEmbeddedUnittest)
}

func TestProcessBlockWithTx_InsertUnsetClaimError(t *testing.T) {
	t.Parallel()
	proc, storageMock := newTestEmbeddedProcessor(t)
	tx := dbmocks.NewQuerier(t)
	unset := UnsetClaim{BlockNum: 1, GlobalIndex: big.NewInt(1)}
	block := sync.Block{Num: 1}

	storageMock.EXPECT().InsertUnsetClaim(mock.Anything, tx, unset).Return(errEmbeddedUnittest)

	err := proc.ProcessBlockWithTx(t.Context(), tx, block, Event{UnsetClaim: &unset})
	require.ErrorIs(t, err, errEmbeddedUnittest)
}

func TestProcessBlockWithTx_InsertSetClaimError(t *testing.T) {
	t.Parallel()
	proc, storageMock := newTestEmbeddedProcessor(t)
	tx := dbmocks.NewQuerier(t)
	set := SetClaim{BlockNum: 1, GlobalIndex: big.NewInt(1)}
	block := sync.Block{Num: 1}

	storageMock.EXPECT().InsertSetClaim(mock.Anything, tx, set).Return(errEmbeddedUnittest)

	err := proc.ProcessBlockWithTx(t.Context(), tx, block, Event{SetClaim: &set})
	require.ErrorIs(t, err, errEmbeddedUnittest)
}

// InsertClaim fails: InsertUnsetClaim and InsertSetClaim must not be called
func TestProcessBlockWithTx_ClaimErrorAborts(t *testing.T) {
	t.Parallel()
	proc, storageMock := newTestEmbeddedProcessor(t)
	tx := dbmocks.NewQuerier(t)
	claim := Claim{BlockNum: 1, GlobalIndex: big.NewInt(1)}
	unset := UnsetClaim{BlockNum: 1, GlobalIndex: big.NewInt(2)}
	block := sync.Block{Num: 1}

	storageMock.EXPECT().InsertClaim(mock.Anything, tx, claim).Return(errEmbeddedUnittest)
	// InsertUnsetClaim must NOT be called after InsertClaim fails

	err := proc.ProcessBlockWithTx(t.Context(), tx, block, Event{Claim: &claim, UnsetClaim: &unset})
	require.ErrorIs(t, err, errEmbeddedUnittest)
}

// --- ReorgWithTx ---

func TestReorgWithTx_HappyPath(t *testing.T) {
	t.Parallel()
	proc, storageMock := newTestEmbeddedProcessor(t)
	tx := dbmocks.NewQuerier(t)

	storageMock.EXPECT().DeleteBlocksFrom(mock.Anything, tx, uint64(10)).Return(int64(3), nil)

	rows, err := proc.ReorgWithTx(t.Context(), tx, 10)
	require.NoError(t, err)
	require.Equal(t, int64(3), rows)
}

func TestReorgWithTx_Error(t *testing.T) {
	t.Parallel()
	proc, storageMock := newTestEmbeddedProcessor(t)
	tx := dbmocks.NewQuerier(t)

	storageMock.EXPECT().DeleteBlocksFrom(mock.Anything, tx, uint64(5)).Return(int64(0), errEmbeddedUnittest)

	rows, err := proc.ReorgWithTx(t.Context(), tx, 5)
	require.ErrorIs(t, err, errEmbeddedUnittest)
	require.Equal(t, int64(0), rows)
}

// --- NewClaimStorage ---

func TestNewClaimStorage_OK(t *testing.T) {
	t.Parallel()
	lg := logger.WithFields("module", "test")
	sqlDB, err := db.NewSQLiteDB(t.TempDir() + "/test.db")
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })

	storage, err := NewClaimStorage(sqlDB, lg, claimsynctypes.L1ClaimSyncer, 0)
	require.NoError(t, err)
	require.NotNil(t, storage)
}
