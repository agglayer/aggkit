package multidownloader

import (
	"context"
	"math/big"
	"path/filepath"
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mockethermantypes "github.com/agglayer/aggkit/etherman/types/mocks"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	mockmdrtypes "github.com/agglayer/aggkit/multidownloader/types/mocks"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocktypes "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// restartReopen creates a brand new multidownloader rooted on the SAME storage
// file (path). Nothing is carried over in memory: the storage connection, the
// RPC fake and the Ethereum mock are all fresh, so every bit of recovered state
// must come from what was actually persisted (and committed to WAL) by the
// previous phase. There is deliberately no Close() on the storage, so a fresh
// connection is the real acceptance test that the previous phase committed.
func restartReopen(t *testing.T, path string, enabled bool, txFrom uint64,
	topics []common.Hash) (*EVMMultidownloader, *storage.MultidownloaderStorage,
	*fakeTransactionRPC, *mocktypes.BaseEthereumClienter) {
	t.Helper()
	logger := log.WithFields("test", "restart_regression")
	cfg := NewConfigDefault("restart", filepath.Dir(path))
	cfg.StoragePath = path

	realStorage, err := storage.NewMultidownloaderStorage(logger,
		storage.MultidownloaderStorageConfig{DBPath: path})
	require.NoError(t, err)

	ethClient := mocktypes.NewBaseEthereumClienter(t)
	notifier := mockethermantypes.NewBlockNotifierManager(t)
	reorgProc := mockmdrtypes.NewReorgProcessor(t)
	fake := newFakeTransactionRPC()

	ethClient.EXPECT().ChainID(mock.Anything).Return(big.NewInt(1337), nil).Maybe()
	// A constant ToBlock resolves to its own specific number; any other tag is
	// irrelevant for this test (the syncer pins ToBlock to block 20).
	notifier.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, f aggkittypes.BlockNumberFinality) (uint64, error) {
			return f.Specific, nil
		}).Maybe()

	md, err := NewEVMMultidownloader(logger, cfg, "restart", ethClient, fake,
		realStorage, notifier, reorgProc)
	require.NoError(t, err)

	require.NoError(t, md.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID:          "bridge",
		ContractAddresses: []common.Address{txFixtureAddr},
		FromBlock:         1,
		ToBlock:           *aggkittypes.NewBlockNumber(20),
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   enabled,
			FromBlock: txFrom,
			Topic0:    topics,
		},
	}))
	require.NoError(t, md.Initialize(t.Context()))
	return md, realStorage, fake, ethClient
}

// restartSeedLogs persists the canonical block headers + logs for the given
// logs, marks [1,end] as covered for txFixtureAddr (via the real storage, so the
// coverage is persisted and survives a reopen), then rebuilds the in-memory
// state from storage so it reflects exactly what was persisted.
func restartSeedLogs(t *testing.T, md *EVMMultidownloader, s *storage.MultidownloaderStorage,
	end uint64, logs ...ethtypes.Log) {
	t.Helper()
	for _, lg := range logs {
		headers := aggkittypes.ListBlockHeaders{
			aggkittypes.NewBlockHeader(lg.BlockNumber, lg.BlockHash, lg.BlockTimestamp, nil),
		}
		require.NoError(t, s.SaveEthLogsWithHeaders(nil, headers, []ethtypes.Log{lg}, true))
	}
	require.NoError(t, s.UpdateSyncedStatus(nil, []mdrtypes.SyncSegment{{
		ContractAddr:  txFixtureAddr,
		BlockRange:    aggkitcommon.NewBlockRange(1, end),
		TargetToBlock: *aggkittypes.NewBlockNumber(20),
	}}))
	var err error
	md.state, err = md.newStateFromStorage(t.Context())
	require.NoError(t, err)
}

// restartStored asserts whether the canonical transaction for lg is persisted.
func restartStored(t *testing.T, s *storage.MultidownloaderStorage, lg ethtypes.Log, want bool) {
	t.Helper()
	got, err := s.GetTransaction(nil, lg.TxHash, lg.BlockHash)
	require.NoError(t, err)
	if want {
		require.NotNil(t, got, "expected transaction %s to be stored", lg.TxHash.Hex())
	} else {
		require.Nil(t, got, "expected transaction %s NOT to be stored", lg.TxHash.Hex())
	}
}

// TestRestartRegression_DisableEnableGapsAndTopicWidening is a single phased
// table-style regression: every phase reopens the SAME database and asserts that
// transaction backfill recovers exactly the persisted coverage/frontier/topics,
// never refetching cached rows and never treating an incomplete range as done.
//
// The three canonical logs are deliberately spread over three blocks:
//
//	block 10 (hash10) txA topic T0
//	block 12 (hash12) txB topic T1
//	block 15 (hash15) txC topic T0
//
// Every phase seeds the fake RPC with ALL three transaction payloads, including
// the rows it must NOT fetch, so an erroneous fetch would be observable as an
// extra RPC call and a stored row that should not exist.
func TestRestartRegression_DisableEnableGapsAndTopicWidening(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "restart_regression.sqlite")

	hash10 := common.HexToHash("0x10")
	hash12 := common.HexToHash("0x12")
	hash15 := common.HexToHash("0x15")
	txA := common.HexToHash("0xaa")
	txB := common.HexToHash("0xbb")
	txC := common.HexToHash("0xcc")

	lg10 := txFixtureLog(10, hash10, txA, 0, []common.Hash{txFixtureT0})
	lg12 := txFixtureLog(12, hash12, txB, 0, []common.Hash{txFixtureT1})
	lg15 := txFixtureLog(15, hash15, txC, 0, []common.Hash{txFixtureT0})

	seedFake := func(f *fakeTransactionRPC) {
		f.setTx(txA, txFixtureRPCJSON(10, hash10, txA))
		f.setTx(txB, txFixtureRPCJSON(12, hash12, txB))
		f.setTx(txC, txFixtureRPCJSON(15, hash15, txC))
	}

	// --- Phase A: transactions DISABLED, coverage 1..10 persisted ------------
	mdA, stA, fakeA, ethA := restartReopen(t, dbPath, false, 1, []common.Hash{txFixtureT0})
	seedFake(fakeA)
	restartSeedLogs(t, mdA, stA, 10, lg10)
	mdA.scanMissingTransactions(t.Context())
	require.Zero(t, fakeA.callCount(), "phase A: a disabled registration must never fetch a transaction")
	restartStored(t, stA, lg10, false)
	ethA.AssertNotCalled(t, "FilterLogs")

	// --- Phase B: reopen ENABLED, persisted coverage must be reconstructed ---
	mdB, stB, fakeB, ethB := restartReopen(t, dbPath, true, 1, []common.Hash{txFixtureT0})
	seedFake(fakeB)
	require.True(t, mdB.IsAvailable(mdrtypes.NewLogQuery(1, 10, []common.Address{txFixtureAddr})),
		"phase B: coverage 1..10 must be reconstructed from storage before scanning")
	mdB.scanMissingTransactions(t.Context())
	require.Equal(t, 1, fakeB.callCount(), "phase B: exactly txA must be fetched")
	restartStored(t, stB, lg10, true)
	restartStored(t, stB, lg12, false)
	restartStored(t, stB, lg15, false)
	ethB.AssertNotCalled(t, "FilterLogs")

	// --- Phase C: reopen DISABLED, seed gap logs + widen coverage to 1..20 ---
	mdC, stC, fakeC, ethC := restartReopen(t, dbPath, false, 1, []common.Hash{txFixtureT0})
	seedFake(fakeC)
	restartSeedLogs(t, mdC, stC, 20, lg12, lg15)
	mdC.scanMissingTransactions(t.Context())
	require.Zero(t, fakeC.callCount(), "phase C: disabled reopen must not fetch")
	restartStored(t, stC, lg10, true) // resolved in B, must be retained
	restartStored(t, stC, lg12, false)
	restartStored(t, stC, lg15, false)
	ethC.AssertNotCalled(t, "FilterLogs")

	// --- Phase D: reopen ENABLED with a FUTURE frontier (13) and topic T0 ----
	mdD, stD, fakeD, ethD := restartReopen(t, dbPath, true, 13, []common.Hash{txFixtureT0})
	seedFake(fakeD)
	mdD.scanMissingTransactions(t.Context())
	require.Equal(t, 1, fakeD.callCount(),
		"phase D: only txC (block 15 >= frontier 13, topic T0) may be fetched")
	restartStored(t, stD, lg10, true)  // cached txA reused, never refetched
	restartStored(t, stD, lg12, false) // below the frontier, stays missing
	restartStored(t, stD, lg15, true)
	ethD.AssertNotCalled(t, "FilterLogs")

	availD, err := stD.TransactionAvailabilityForLogQuery(nil,
		mdrtypes.NewLogQuery(1, 20, []common.Address{txFixtureAddr}),
		[]common.Hash{txFixtureT0, txFixtureT1})
	require.NoError(t, err)
	require.False(t, availD.Complete, "phase D: txB is still missing, the range is not complete")
	require.Contains(t, availD.MissingTxHashes, txB)

	// --- Phase E: reopen ENABLED HISTORIC (1) with widened topics T0+T1 ------
	mdE, stE, fakeE, ethE := restartReopen(t, dbPath, true, 1,
		[]common.Hash{txFixtureT0, txFixtureT1})
	seedFake(fakeE)
	mdE.scanMissingTransactions(t.Context())
	require.Equal(t, 1, fakeE.callCount(),
		"phase E: widening the topic selector must fetch exactly the missing txB")
	restartStored(t, stE, lg10, true) // cached txA unchanged
	restartStored(t, stE, lg12, true)
	restartStored(t, stE, lg15, true) // cached txC unchanged
	ethE.AssertNotCalled(t, "FilterLogs")

	availE, err := stE.TransactionAvailabilityForLogQuery(nil,
		mdrtypes.NewLogQuery(1, 20, []common.Address{txFixtureAddr}),
		[]common.Hash{txFixtureT0, txFixtureT1})
	require.NoError(t, err)
	require.True(t, availE.Complete,
		"phase E: with coverage 1..20 and all transactions stored the range is complete")
	require.Empty(t, availE.MissingTxHashes)

	// --- Phase F: reopen ENABLED again, everything must already be ready -----
	mdF, stF, fakeF, ethF := restartReopen(t, dbPath, true, 1,
		[]common.Hash{txFixtureT0, txFixtureT1})
	seedFake(fakeF)
	mdF.scanMissingTransactions(t.Context())
	require.Zero(t, fakeF.callCount(), "phase F: nothing left to resolve, no RPC may be issued")
	restartStored(t, stF, lg10, true)
	restartStored(t, stF, lg12, true)
	restartStored(t, stF, lg15, true)
	ethF.AssertNotCalled(t, "FilterLogs")
}
