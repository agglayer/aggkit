package multidownloader

// Stop/Start race regression tests for the shared transaction flight.
//
// The shared transaction flight (transactionInflight) admits a flight by
// inserting it into the calls map and registering it in its WaitGroup. If the
// map insert and the wg.Add are not serialized with the stop path, a provider
// request racing Stop can register a flight AFTER wait() has already observed an
// empty group: that is a WaitGroup misuse (Add concurrently with Wait at zero)
// and it lets the flight return after the stopped state has been published.
//
// These tests pin the fixed lifecycle with channel-based synchronization only
// (no sleeps):
//   - once wait() seals admission, a concurrent do() is rejected without ever
//     running its body and without registering a late wg.Add;
//   - wait() cancels every in-flight flight's OWN context, so a flight admitted
//     before Start (under the Initialize/Background base context) is drained by
//     Stop instead of lingering until its 30s request timeout;
//   - setBaseContext reopens admission for the next run only after the previous
//     run has been joined.

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestTransactionStopRace_SecondDoRejectedAfterWaitSealsAdmission pins that once
// wait() has sealed the group no new flight can be admitted: a do() racing the
// stop path is rejected, never runs its body and never performs a late wg.Add
// (which would be a WaitGroup misuse and could let it return after the stopped
// state). It also pins that setBaseContext reopens admission for the next run.
func TestTransactionStopRace_SecondDoRejectedAfterWaitSealsAdmission(t *testing.T) {
	g := newTransactionInflight(context.Background(), 1)

	leaderKey := txFlightKey{txHash: common.HexToHash("0x01"), blockHash: common.HexToHash("0x11")}
	secondKey := txFlightKey{txHash: common.HexToHash("0x02"), blockHash: common.HexToHash("0x12")}
	thirdKey := txFlightKey{txHash: common.HexToHash("0x03"), blockHash: common.HexToHash("0x13")}

	leaderEntered := make(chan struct{})
	leaderCanceled := make(chan struct{})
	releaseLeader := make(chan struct{})
	leaderDone := make(chan error, 1)
	go func() {
		_, err := g.do(context.Background(), leaderKey, func(ctx context.Context) (*aggkittypes.RPCTransaction, error) {
			close(leaderEntered)
			<-ctx.Done()
			close(leaderCanceled)
			// Hold the flight open so wait() is provably blocked in wg.Wait
			// while the concurrent second do() runs.
			<-releaseLeader
			return nil, ctx.Err()
		})
		leaderDone <- err
	}()

	select {
	case <-leaderEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("leader flight never entered")
	}

	waitDone := make(chan struct{})
	go func() {
		g.wait()
		close(waitDone)
	}()

	// wait() sets closing BEFORE it cancels the flights, so observing the
	// leader's own context canceled proves admission is already sealed and
	// wait() is now blocked joining the (still held-open) leader.
	select {
	case <-leaderCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("wait() did not cancel the in-flight leader")
	}

	// A do() racing wait() must be rejected without running its body and without
	// registering a flight (no wg.Add after wait() observed the group).
	secondRan := make(chan struct{})
	_, err := g.do(context.Background(), secondKey, func(context.Context) (*aggkittypes.RPCTransaction, error) {
		close(secondRan)
		return nil, nil
	})
	require.Error(t, err, "a do() racing wait() must be rejected")
	select {
	case <-secondRan:
		t.Fatal("a rejected do() must never run its body")
	default:
	}
	require.False(t, g.hasCall(secondKey), "a rejected do() must not register a flight")

	// Release the leader so wait() can join it and return.
	close(releaseLeader)
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("wait() never returned after the leader drained")
	}
	select {
	case err := <-leaderDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("leader flight never returned")
	}

	// Admission stays sealed until the next run reopens it.
	_, err = g.do(context.Background(), thirdKey, func(context.Context) (*aggkittypes.RPCTransaction, error) {
		return nil, nil
	})
	require.Error(t, err, "admission must stay sealed until setBaseContext reopens it")

	// setBaseContext reopens admission for the next run.
	g.setBaseContext(context.Background())
	got, err := g.do(context.Background(), thirdKey, func(context.Context) (*aggkittypes.RPCTransaction, error) {
		return nil, nil
	})
	require.NoError(t, err)
	require.Nil(t, got)
}

// TestTransactionStopRace_PreStartFlightDrainedByWait pins that wait() cancels a
// flight admitted BEFORE Start. Such a flight's context is derived from the
// Initialize/Background base, not from the run context Start installs, so
// canceling the run context alone would leave it waiting for its request
// timeout. wait() must cancel the flight's OWN context instead.
func TestTransactionStopRace_PreStartFlightDrainedByWait(t *testing.T) {
	// Initialize with a Background parent: the base context the flight is
	// admitted under.
	g := newTransactionInflight(context.Background(), 1)

	key := txFlightKey{txHash: common.HexToHash("0x04"), blockHash: common.HexToHash("0x14")}
	entered := make(chan struct{})
	cancelObserved := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := g.do(context.Background(), key, func(ctx context.Context) (*aggkittypes.RPCTransaction, error) {
			close(entered)
			<-ctx.Done()
			close(cancelObserved)
			return nil, ctx.Err()
		})
		done <- err
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("pre-Start flight never entered")
	}

	// Start replaces the base context with the run context. The already-admitted
	// flight's own context is NOT a child of the run context.
	runCtx, cancelRun := context.WithCancel(context.Background())
	g.setBaseContext(runCtx)
	cancelRun() // Stop cancels the run context

	waitDone := make(chan struct{})
	go func() {
		g.wait()
		close(waitDone)
	}()

	select {
	case <-cancelObserved:
	case <-time.After(5 * time.Second):
		t.Fatal("wait() must cancel a flight admitted before Start, not wait for its request timeout")
	}
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("wait() never returned")
	}
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("pre-Start flight never returned")
	}
}

// TestTransactionStopRace_StartStopDrainsPreStartFlight is the end-to-end
// counterpart: a real Start/Stop run must drain a transaction flight that was
// admitted before Start. The pre-Start flight performs a real transaction RPC
// bounded by the default 30s request timeout, so only wait() canceling the
// flight's own context can drain it promptly.
func TestTransactionStopRace_StartStopDrainsPreStartFlight(t *testing.T) {
	td := newEVMMultidownloaderTestData(t, true)
	td.FakeInitialized(t)

	realTS, err := storage.NewMultidownloaderStorage(
		log.WithFields("test", "tx_stop_race"),
		storage.MultidownloaderStorageConfig{DBPath: filepath.Join(t.TempDir(), "tx.sqlite")})
	require.NoError(t, err)
	ts, err := mdrtypes.AsTransactionStorage(realTS)
	require.NoError(t, err)
	td.mdr.transactionStorage = ts
	td.mdr.requiresTransactions = true
	td.mdr.cfg.PeriodToCheckReorgs = types.NewDuration(time.Hour)

	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})

	state := NewEmptyState()
	state.Synced.Add(mdrtypes.SyncSegment{
		ContractAddr: txFixtureAddr,
		BlockRange:   aggkitcommon.NewBlockRange(1, 20),
	})
	td.mdr.state = state
	td.mdr.transactionDemands = []transactionDemand{{
		SyncerID:  "s1",
		Address:   txFixtureAddr,
		FromBlock: 1,
		ToBlock:   aggkittypes.LatestBlock,
		Topic0:    []common.Hash{txFixtureT0},
	}}

	blocking := tlcNewBlockingRPC()
	td.mdr.rpcClient = blocking

	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
		Return(uint64(100), nil).Maybe()
	td.mockStorage.EXPECT().GetEthLogs(mock.Anything, mock.Anything).
		Return([]ethtypes.Log{lg}, nil).Maybe()
	td.mockStorage.EXPECT().NewTx(mock.Anything).Return(nil, fmt.Errorf("stop test")).Maybe()

	// A flight admitted BEFORE Start: its base context is the constructor
	// context.Background(), NOT the run context Start installs. Its RPC is
	// bounded by the default 30s request timeout, so only wait() canceling the
	// flight's own context can drain it promptly.
	preHash := common.HexToHash("0xabc")
	preDone := make(chan error, 1)
	go func() {
		_, err := td.mdr.txInflight.do(context.Background(),
			txFlightKey{txHash: preHash, blockHash: blockHash},
			func(runCtx context.Context) (*aggkittypes.RPCTransaction, error) {
				return td.mdr.rpcTransactionByHash(runCtx, preHash)
			})
		preDone <- err
	}()

	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("pre-Start transaction RPC never started")
	}

	var startErr error
	var startWG sync.WaitGroup
	startWG.Add(1)
	go func() {
		defer startWG.Done()
		startErr = td.mdr.Start(context.Background())
	}()

	// Start is up once the transaction worker issues its own transaction RPC.
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("transaction worker never issued a transaction RPC after Start")
	}

	require.NoError(t, td.mdr.Stop(context.Background()))
	startWG.Wait()

	require.ErrorIs(t, startErr, context.Canceled)

	// The pre-Start flight must have been drained by Stop, NOT left waiting for
	// its 30s request timeout.
	select {
	case err := <-preDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not drain a flight admitted before Start")
	}
	require.GreaterOrEqual(t, blocking.callCount(), 2,
		"the pre-Start flight and the worker flight must each have issued one RPC")
}
