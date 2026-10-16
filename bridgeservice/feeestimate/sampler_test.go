package feeestimate

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// hashes builds n distinct transaction hashes.
func hashes(n int) []common.Hash {
	out := make([]common.Hash, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, common.BigToHash(big.NewInt(int64(1000+i))))
	}
	return out
}

func TestTxStore_CachesObservationsByHash(t *testing.T) {
	chain := newFakeChain()
	bridge := common.HexToAddress("0x9999999999999999999999999999999999999999")
	store := newTxStore(chain, 64, 4)

	txs := hashes(3)
	for i, hash := range txs {
		chain.txs[hash] = claimTx(bridge)
		chain.receipts[hash] = receiptOf(ethtypes.ReceiptStatusSuccessful, uint64(100000+i))
	}

	observations, err := store.fetch(context.Background(), txs)
	require.NoError(t, err)
	require.Len(t, observations, 3)
	require.Equal(t, int64(3), chain.receiptCalls.Load())
	require.Equal(t, int64(3), chain.txCalls.Load())

	// The second fetch of the same hashes is fully cached.
	observations, err = store.fetch(context.Background(), txs)
	require.NoError(t, err)
	require.Len(t, observations, 3)
	require.Equal(t, int64(3), chain.receiptCalls.Load())
	require.Equal(t, int64(3), chain.txCalls.Load())
	require.Equal(t, uint64(100001), observations[txs[1]].receipt.GasUsed)
}

func TestTxStore_SkipsUnknownAndPendingTransactions(t *testing.T) {
	chain := newFakeChain()
	bridge := common.HexToAddress("0x9999999999999999999999999999999999999999")
	store := newTxStore(chain, 64, 4)

	unknown, pending, mined := hashes(3)[0], hashes(3)[1], hashes(3)[2]
	chain.txs[pending] = claimTx(bridge)
	chain.receipts[pending] = receiptOf(ethtypes.ReceiptStatusSuccessful, 1)
	chain.pending[pending] = true
	chain.txs[mined] = claimTx(bridge)
	chain.receipts[mined] = receiptOf(ethtypes.ReceiptStatusSuccessful, 2)

	observations, err := store.fetch(context.Background(), []common.Hash{unknown, pending, mined})
	require.NoError(t, err)
	require.Len(t, observations, 1)
	require.Contains(t, observations, mined)

	// Unknown and pending transactions are not cached: they are retried next recompute.
	require.Equal(t, int64(3), chain.receiptCalls.Load())
}

func TestTxStore_FailsClosedOnRPCErrors(t *testing.T) {
	chain := newFakeChain()
	bridge := common.HexToAddress("0x9999999999999999999999999999999999999999")
	store := newTxStore(chain, 64, 4)

	hash := hashes(1)[0]
	chain.txs[hash] = claimTx(bridge)
	chain.receiptErr[hash] = errRPC("eth_getTransactionReceipt")

	_, err := store.fetch(context.Background(), []common.Hash{hash})
	require.ErrorContains(t, err, "fetching receipt of tx")
}

func TestTxStore_EvictsLeastRecentlyUsed(t *testing.T) {
	chain := newFakeChain()
	bridge := common.HexToAddress("0x9999999999999999999999999999999999999999")
	store := newTxStore(chain, 2, 4)

	txs := hashes(3)
	for _, hash := range txs {
		chain.txs[hash] = claimTx(bridge)
		chain.receipts[hash] = receiptOf(ethtypes.ReceiptStatusSuccessful, 1)
	}

	// Touch hashes 0 and 1, then touch hash 0 again so hash 1 is the least recently used.
	_, err := store.fetch(context.Background(), txs[:2])
	require.NoError(t, err)
	_, err = store.fetch(context.Background(), txs[:1])
	require.NoError(t, err)
	require.Equal(t, int64(2), chain.receiptCalls.Load())

	// Inserting hash 2 evicts hash 1; hash 0 stays.
	_, err = store.fetch(context.Background(), txs[2:])
	require.NoError(t, err)
	require.Equal(t, int64(3), chain.receiptCalls.Load())
	_, err = store.fetch(context.Background(), txs[:1])
	require.NoError(t, err)
	require.Equal(t, int64(3), chain.receiptCalls.Load(), "hash 0 is still cached")
	_, err = store.fetch(context.Background(), txs[1:2])
	require.NoError(t, err)
	require.Equal(t, int64(4), chain.receiptCalls.Load(), "hash 1 was evicted and refetched")
}

func TestTxStore_BoundsConcurrentRPCCalls(t *testing.T) {
	chain := newFakeChain()
	bridge := common.HexToAddress("0x9999999999999999999999999999999999999999")
	store := newTxStore(chain, 64, 2)
	chain.sleep = 10 * time.Millisecond

	txs := hashes(8)
	for _, hash := range txs {
		chain.txs[hash] = claimTx(bridge)
		chain.receipts[hash] = receiptOf(ethtypes.ReceiptStatusSuccessful, 1)
	}

	_, err := store.fetch(context.Background(), txs)
	require.NoError(t, err)
	require.LessOrEqual(t, chain.maxInflight.Load(), int64(2),
		"the store must fan out over at most RPCConcurrency concurrent calls")
	require.Equal(t, int64(8), chain.receiptCalls.Load())
}

func TestIsSuccessfulBridgeTx(t *testing.T) {
	bridge := common.HexToAddress("0x9999999999999999999999999999999999999999")
	other := common.HexToAddress("0x8888888888888888888888888888888888888888")
	goodTx := claimTx(bridge)
	otherTo := claimTx(other)

	tests := []struct {
		name string
		obs  *txObservation
		want bool
	}{
		{
			name: "successful tx sent to the bridge",
			obs:  &txObservation{tx: goodTx, receipt: receiptOf(ethtypes.ReceiptStatusSuccessful, 1)},
			want: true,
		},
		{
			name: "reverted tx is not a sample",
			obs:  &txObservation{tx: goodTx, receipt: receiptOf(ethtypes.ReceiptStatusFailed, 1)},
			want: false,
		},
		{
			name: "tx sent elsewhere than the bridge",
			obs:  &txObservation{tx: otherTo, receipt: receiptOf(ethtypes.ReceiptStatusSuccessful, 1)},
			want: false,
		},
		{
			name: "missing receipt",
			obs:  &txObservation{tx: goodTx},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isSuccessfulBridgeTx(tt.obs, bridge))
		})
	}
}

func TestCountBridgeLogs(t *testing.T) {
	bridge := common.HexToAddress("0x9999999999999999999999999999999999999999")
	other := common.HexToAddress("0x8888888888888888888888888888888888888888")
	obs := &txObservation{receipt: receiptOf(ethtypes.ReceiptStatusSuccessful, 1,
		bridgeLog(bridge, claimEventTopic),
		bridgeLog(bridge, claimEventPreEtrogTopic),
		bridgeLog(other, claimEventTopic),                       // not emitted by the bridge
		&ethtypes.Log{Address: bridge, Topics: []common.Hash{}}, // no topics at all
		bridgeLog(bridge, newWrappedTokenTopic),
	)}

	require.Equal(t, 2, countBridgeLogs(obs, bridge,
		[]common.Hash{claimEventTopic, claimEventPreEtrogTopic}))
	require.Equal(t, 1, countBridgeLogs(obs, bridge, []common.Hash{newWrappedTokenTopic}))
	require.Equal(t, 0, countBridgeLogs(obs, bridge, []common.Hash{detailedClaimEventTopic}))
}

func TestCountClaimEvents(t *testing.T) {
	bridge := common.HexToAddress("0x9999999999999999999999999999999999999999")
	other := common.HexToAddress("0x8888888888888888888888888888888888888888")

	tests := []struct {
		name string
		logs []*ethtypes.Log
		want int
	}{
		{
			name: "L1 receipt: one Etrog ClaimEvent is one claim",
			logs: []*ethtypes.Log{bridgeLog(bridge, claimEventTopic)},
			want: 1,
		},
		{
			name: "one pre-Etrog ClaimEvent is one claim",
			logs: []*ethtypes.Log{bridgeLog(bridge, claimEventPreEtrogTopic)},
			want: 1,
		},
		{
			name: "one DetailedClaimEvent alone is one claim",
			logs: []*ethtypes.Log{bridgeLog(bridge, detailedClaimEventTopic)},
			want: 1,
		},
		{
			// The sovereign AgglayerBridgeL2 paired emission: one claim, two logs.
			name: "ClaimEvent + DetailedClaimEvent pair from the bridge is one claim",
			logs: []*ethtypes.Log{
				bridgeLog(bridge, claimEventTopic),
				bridgeLog(bridge, detailedClaimEventTopic),
			},
			want: 1,
		},
		{
			name: "pre-Etrog ClaimEvent + DetailedClaimEvent pair from the bridge is one claim",
			logs: []*ethtypes.Log{
				bridgeLog(bridge, claimEventPreEtrogTopic),
				bridgeLog(bridge, detailedClaimEventTopic),
			},
			want: 1,
		},
		{
			name: "both ClaimEvent topics + DetailedClaimEvent from the bridge is one claim",
			logs: []*ethtypes.Log{
				bridgeLog(bridge, claimEventTopic),
				bridgeLog(bridge, claimEventPreEtrogTopic),
				bridgeLog(bridge, detailedClaimEventTopic),
			},
			want: 1,
		},
		{
			name: "two separate claims in one receipt (two pairs) are two claims",
			logs: []*ethtypes.Log{
				bridgeLog(bridge, claimEventTopic),
				bridgeLog(bridge, detailedClaimEventTopic),
				bridgeLog(bridge, claimEventTopic),
				bridgeLog(bridge, detailedClaimEventTopic),
			},
			want: 2,
		},
		{
			name: "two separate L1 claims in one receipt (two ClaimEvents) are two claims",
			logs: []*ethtypes.Log{
				bridgeLog(bridge, claimEventTopic),
				bridgeLog(bridge, claimEventTopic),
			},
			want: 2,
		},
		{
			name: "two DetailedClaimEvents in one receipt are two claims",
			logs: []*ethtypes.Log{
				bridgeLog(bridge, detailedClaimEventTopic),
				bridgeLog(bridge, detailedClaimEventTopic),
			},
			want: 2,
		},
		{
			// A pair split across emitters is not a pair: the bridge's own logs stand alone.
			name: "bridge ClaimEvent + other emitter's DetailedClaimEvent is one claim, not zero",
			logs: []*ethtypes.Log{
				bridgeLog(bridge, claimEventTopic),
				bridgeLog(other, detailedClaimEventTopic),
			},
			want: 1,
		},
		{
			name: "bridge DetailedClaimEvent + other emitter's ClaimEvent is one claim",
			logs: []*ethtypes.Log{
				bridgeLog(other, claimEventTopic),
				bridgeLog(bridge, detailedClaimEventTopic),
			},
			want: 1,
		},
		{
			name: "logs other emitters produced never count",
			logs: []*ethtypes.Log{
				bridgeLog(other, claimEventTopic),
				bridgeLog(other, detailedClaimEventTopic),
			},
			want: 0,
		},
		{
			name: "no claim logs at all",
			logs: []*ethtypes.Log{
				bridgeLog(bridge, newWrappedTokenTopic),
				{Address: bridge, Topics: []common.Hash{}},
				nil,
			},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := &txObservation{receipt: receiptOf(ethtypes.ReceiptStatusSuccessful, 1, tt.logs...)}
			require.Equal(t, tt.want, countClaimEvents(obs, bridge))
		})
	}
}
