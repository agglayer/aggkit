package multidownloader

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// This file contains the regression tests for the transaction-validation fix:
//
//   - A transaction whose RPC-reported mined transaction index does not match
//     the expected source log must be rejected as an inclusion mismatch BEFORE
//     it is persisted, so a wrong row can never poison the cache or make the
//     availability row look complete.
//   - Once the RPC is corrected the very next request must succeed and a later
//     request must be served from the cache without any further RPC.
//   - Initialize must capture the lifetime context immediately so an early
//     provider call can never start an unbounded context.Background RPC after
//     the lifetime is canceled.
//
// The helpers are prefixed with "regr" so they never collide with the shared
// fixtures in evm_multidownloader_transactions_test.go.

// regrLog builds a canonical source log with an explicit transaction index so a
// wrong RPC transaction index can be distinguished from the expected one.
func regrLog(blockNumber uint64, blockHash, txHash common.Hash, txIndex uint) ethtypes.Log {
	return ethtypes.Log{
		Address:        txFixtureAddr,
		BlockNumber:    blockNumber,
		BlockHash:      blockHash,
		BlockTimestamp: 1630000000,
		Topics:         []common.Hash{txFixtureT0},
		TxHash:         txHash,
		TxIndex:        txIndex,
		Index:          0,
	}
}

// regrRPCJSON builds an eth_getTransactionByHash payload with an explicit
// transaction index.
func regrRPCJSON(blockNumber uint64, blockHash, txHash common.Hash, txIndex uint64) json.RawMessage {
	obj := map[string]any{
		"hash":             txHash.Hex(),
		"from":             txFixtureFrom.Hex(),
		"to":               txFixtureTo.Hex(),
		"blockHash":        blockHash.Hex(),
		"blockNumber":      fmt.Sprintf("0x%x", blockNumber),
		"transactionIndex": fmt.Sprintf("0x%x", txIndex),
		"input":            "0x",
	}
	raw, _ := json.Marshal(obj)
	return raw
}

// regrPersistCoverage persists the syncer log coverage for an address so the
// enriched transaction availability reports LogCoverage=true independently of
// whether the transaction row exists.
func regrPersistCoverage(t *testing.T, td *testDataEVMMultidownloader,
	addr common.Address, from, to uint64) {
	t.Helper()
	require.NoError(t, td.realStorage.UpsertSyncerConfigs(nil, []mdrtypes.ContractConfig{{
		Address:   addr,
		FromBlock: from,
		ToBlock:   aggkittypes.LatestBlock,
		Syncers:   []mdrtypes.SyncerID{"test"},
	}}))
	require.NoError(t, td.realStorage.UpdateSyncedStatus(nil, []mdrtypes.SyncSegment{{
		ContractAddr:  addr,
		TargetToBlock: aggkittypes.LatestBlock,
		BlockRange:    aggkitcommon.NewBlockRange(from, to),
	}}))
}

// regrAvailability returns the transaction availability for the fixture address
// at the given block.
func regrAvailability(t *testing.T, td *testDataEVMMultidownloader,
	blockNumber uint64) mdrtypes.TransactionAvailability {
	t.Helper()
	av, err := td.mdr.transactionStorage.TransactionAvailabilityForLogQuery(nil,
		mdrtypes.NewLogQuery(blockNumber, blockNumber, []common.Address{txFixtureAddr}),
		[]common.Hash{txFixtureT0})
	require.NoError(t, err)
	return av
}

// TestTransactionValidation_WrongIndexRejectedThenCorrected covers the
// foreground provider path: a wrong mined transaction index is rejected and
// never cached, the corrected RPC succeeds, and the stored row is then served
// from the cache without any further RPC.
func TestTransactionValidation_WrongIndexRejectedThenCorrected(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	const wantIndex = 3
	lg := regrLog(10, blockHash, txHash, wantIndex)
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
	regrPersistCoverage(t, td, txFixtureAddr, 1, 20)

	// First request: the RPC reports a WRONG mined transaction index.
	fake.setTx(txHash, regrRPCJSON(10, blockHash, txHash, 7))

	got, err := td.mdr.TransactionByLog(t.Context(), lg)
	require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
	require.Nil(t, got)
	require.Equal(t, 1, fake.callCount())

	// The wrong row must never be persisted.
	stored, err := td.mdr.transactionStorage.GetTransaction(nil, txHash, blockHash)
	require.NoError(t, err)
	require.Nil(t, stored, "a wrong mined transaction index must never poison the cache")

	// Availability must not be reported complete while the tx is unresolved.
	av := regrAvailability(t, td, 10)
	require.True(t, av.LogCoverage, "coverage is persisted independently of the tx row")
	require.False(t, av.Complete, "a missing tx must not be reported as complete")
	require.NotEmpty(t, av.MissingTxHashes)

	// Second request: the RPC is corrected to the expected index.
	fake.setTx(txHash, regrRPCJSON(10, blockHash, txHash, wantIndex))

	got, err = td.mdr.TransactionByLog(t.Context(), lg)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.EqualValues(t, wantIndex, mustTxIndex(t, got))
	require.Equal(t, 2, fake.callCount())

	stored, err = td.mdr.transactionStorage.GetTransaction(nil, txHash, blockHash)
	require.NoError(t, err)
	require.NotNil(t, stored, "the corrected transaction must be persisted")

	av = regrAvailability(t, td, 10)
	require.True(t, av.Complete, "once the correct tx is stored the query is complete")
	require.Empty(t, av.MissingTxHashes)

	// Third request: a cache hit must serve the stored row with no extra RPC.
	got, err = td.mdr.TransactionByLog(t.Context(), lg)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, 2, fake.callCount(), "a cache hit must not perform another RPC")
}

// TestTransactionValidation_BackgroundWrongIndexRejectedThenCorrected covers the
// background worker path: it must not cache a wrong index, must backfill once the
// RPC is corrected, and must not refetch an already stored transaction.
func TestTransactionValidation_BackgroundWrongIndexRejectedThenCorrected(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	const wantIndex = 2
	lg := regrLog(10, blockHash, txHash, wantIndex)
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)

	td.mdr.transactionDemands = []transactionDemand{{
		SyncerID:  "s1",
		Address:   txFixtureAddr,
		FromBlock: 1,
		ToBlock:   aggkittypes.LatestBlock,
		Topic0:    []common.Hash{txFixtureT0},
	}}
	state := NewEmptyState()
	state.Synced.Add(mdrtypes.SyncSegment{
		ContractAddr: txFixtureAddr,
		BlockRange:   aggkitcommon.NewBlockRange(1, 20),
	})
	td.mdr.state = state
	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(20), nil).Maybe()

	// First scan: the RPC reports a WRONG index.
	fake.setTx(txHash, regrRPCJSON(10, blockHash, txHash, 9))
	td.mdr.scanMissingTransactions(t.Context())
	require.Equal(t, 1, fake.callCount())
	stored, err := td.mdr.transactionStorage.GetTransaction(nil, txHash, blockHash)
	require.NoError(t, err)
	require.Nil(t, stored, "the background worker must not cache a wrong index")

	// Second scan: the corrected index is backfilled.
	fake.setTx(txHash, regrRPCJSON(10, blockHash, txHash, wantIndex))
	td.mdr.scanMissingTransactions(t.Context())
	require.Equal(t, 2, fake.callCount())
	stored, err = td.mdr.transactionStorage.GetTransaction(nil, txHash, blockHash)
	require.NoError(t, err)
	require.NotNil(t, stored, "the corrected transaction must be backfilled")

	// Third scan: the row now exists, so no further RPC is issued.
	td.mdr.scanMissingTransactions(t.Context())
	require.Equal(t, 2, fake.callCount(), "a stored tx must not be re-fetched")
}

// TestInitialize_SetsInflightBaseContextFromLifetime verifies that Initialize
// captures the lifetime context immediately, so a provider call issued under a
// separate live context after the lifetime is canceled cannot start an
// unbounded context.Background RPC.
func TestInitialize_SetsInflightBaseContextFromLifetime(t *testing.T) {
	td := newEVMMultidownloaderTestData(t, false)
	td.MockInitialize(t, 1)
	require.NoError(t, td.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txFixtureAddr},
		FromBlock:         1,
		ToBlock:           aggkittypes.LatestBlock,
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 1,
		},
	}))

	// Initialize captures the lifetime context.
	lifetime, cancelLifetime := context.WithCancel(context.Background())
	require.NoError(t, td.mdr.Initialize(lifetime))
	require.True(t, td.mdr.requiresTransactions)

	fake := newFakeTransactionRPC()
	td.mdr.rpcClient = fake

	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	lg := regrLog(10, blockHash, txHash, 0)
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
	fake.setTx(txHash, regrRPCJSON(10, blockHash, txHash, 0))

	// Cancel the lifetime AFTER Initialize: the shared flight base context must
	// already be the lifetime context, so a provider call under a separate live
	// context must not start an unbounded Background RPC.
	cancelLifetime()

	live, cancelLive := context.WithCancel(context.Background())
	defer cancelLive()
	got, err := td.mdr.TransactionByLog(live, lg)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, got, "a canceled lifetime must not be reported as a nil success")
	require.Zero(t, fake.callCount(), "no RPC may be issued once the lifetime is canceled")
}

// mustTxIndex returns the transaction index of tx, failing the test when it is
// absent or malformed.
func mustTxIndex(t *testing.T, tx *aggkittypes.RPCTransaction) uint64 {
	t.Helper()
	idx, ok := tx.TransactionIndex()
	require.True(t, ok, "transaction %s must carry a well-formed index", tx.HashRaw)
	return idx
}
