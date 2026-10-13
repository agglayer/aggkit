package feeestimate

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// fakeClock is a controllable clock for the estimator's TTL caches.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1760000000, 0)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeChain is a hand-written ChainReader fake. Transactions, receipts, the latest header
// and the gas price suggestions come from the maps and fields; CallContract (the eth_call
// of getTokenWrappedAddress) comes from callFn. RPC-level counters let tests assert caching
// and call counts.
type fakeChain struct {
	receipts   map[common.Hash]*ethtypes.Receipt
	txs        map[common.Hash]*ethtypes.Transaction
	pending    map[common.Hash]bool
	header     *ethtypes.Header
	tipCap     *big.Int
	gasPrice   *big.Int
	callFn     func(msg ethereum.CallMsg) ([]byte, error)
	notFound   map[common.Hash]bool
	receiptErr map[common.Hash]error

	receiptCalls atomic.Int64
	txCalls      atomic.Int64
	headerCalls  atomic.Int64
	tipCalls     atomic.Int64
	priceCalls   atomic.Int64
	callCalls    atomic.Int64

	// inflight and maxInflight track concurrent RPC calls, for the bounded-concurrency
	// assertion.
	inflight    atomic.Int64
	maxInflight atomic.Int64
	sleep       time.Duration
}

func newFakeChain() *fakeChain {
	return &fakeChain{
		receipts:   make(map[common.Hash]*ethtypes.Receipt),
		txs:        make(map[common.Hash]*ethtypes.Transaction),
		pending:    make(map[common.Hash]bool),
		notFound:   make(map[common.Hash]bool),
		receiptErr: make(map[common.Hash]error),
	}
}

func (f *fakeChain) enter() {
	cur := f.inflight.Add(1)
	for {
		curMax := f.maxInflight.Load()
		if cur <= curMax || f.maxInflight.CompareAndSwap(curMax, cur) {
			break
		}
	}
}

func (f *fakeChain) leave() {
	f.inflight.Add(-1)
}

func (f *fakeChain) TransactionReceipt(
	_ context.Context, hash common.Hash,
) (*ethtypes.Receipt, error) {
	f.enter()
	defer f.leave()
	time.Sleep(f.sleep)
	f.receiptCalls.Add(1)
	if err, ok := f.receiptErr[hash]; ok {
		return nil, err
	}
	if f.notFound[hash] {
		return nil, ethereum.NotFound
	}
	receipt, ok := f.receipts[hash]
	if !ok {
		return nil, ethereum.NotFound
	}
	return receipt, nil
}

func (f *fakeChain) TransactionByHash(
	_ context.Context, hash common.Hash,
) (*ethtypes.Transaction, bool, error) {
	f.enter()
	defer f.leave()
	time.Sleep(f.sleep)
	f.txCalls.Add(1)
	if f.notFound[hash] {
		return nil, false, ethereum.NotFound
	}
	tx, ok := f.txs[hash]
	if !ok {
		return nil, false, ethereum.NotFound
	}
	return tx, f.pending[hash], nil
}

func (f *fakeChain) HeaderByNumber(
	_ context.Context, _ *big.Int,
) (*ethtypes.Header, error) {
	f.enter()
	defer f.leave()
	f.headerCalls.Add(1)
	if f.header == nil {
		return nil, errRPC("eth_getBlockByNumber")
	}
	return f.header, nil
}

func (f *fakeChain) SuggestGasTipCap(_ context.Context) (*big.Int, error) {
	f.tipCalls.Add(1)
	if f.tipCap == nil {
		return nil, errRPC("eth_maxPriorityFeePerGas")
	}
	return f.tipCap, nil
}

func (f *fakeChain) SuggestGasPrice(_ context.Context) (*big.Int, error) {
	f.priceCalls.Add(1)
	if f.gasPrice == nil {
		return nil, errRPC("eth_gasPrice")
	}
	return f.gasPrice, nil
}

func (f *fakeChain) CallContract(
	_ context.Context, msg ethereum.CallMsg, _ *big.Int,
) ([]byte, error) {
	f.callCalls.Add(1)
	if f.callFn == nil {
		return nil, errRPC("eth_call")
	}
	return f.callFn(msg)
}

type rpcError struct{ method string }

func (e rpcError) Error() string { return "rpc error on " + e.method }

func errRPC(method string) error { return rpcError{method: method} }

// fakeClaimsReader serves GetClaimsPaged over a fixed, newest-first set of claims, with the
// real storage's semantics: 1-based pages, the compacted total count, and an error for a
// page past the end.
type fakeClaimsReader struct {
	mu       sync.Mutex
	claims   []*claimsynctypes.Claim
	err      error
	getCalls atomic.Int64
}

func (f *fakeClaimsReader) GetClaimsPaged(
	_ context.Context, page, pageSize uint32, _ []uint32, _ *big.Int,
) ([]*claimsynctypes.Claim, int, error) {
	f.getCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, 0, f.err
	}
	// The real storage answers an empty page rather than an error when there are no rows
	// at all, and an error for a page past the end of a non-empty table.
	if len(f.claims) == 0 {
		return []*claimsynctypes.Claim{}, 0, nil
	}
	offset := (int(page) - 1) * int(pageSize)
	if offset >= len(f.claims) {
		return nil, 0, fmt.Errorf(
			"invalid page number (page=%d, size=%d, total=%d)", page, pageSize, len(f.claims))
	}
	end := min(offset+int(pageSize), len(f.claims))
	return f.claims[offset:end], len(f.claims), nil
}

// claimTx builds a legacy transaction that went to to. The fakes map transactions by the
// hash the test chose, so the tx's own hash is irrelevant.
func claimTx(to common.Address) *ethtypes.Transaction {
	return ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: 1, To: &to, Value: big.NewInt(0), Gas: 300000})
}

// receiptOf builds a receipt with the given status, gasUsed and logs.
func receiptOf(status uint64, gasUsed uint64, logs ...*ethtypes.Log) *ethtypes.Receipt {
	return &ethtypes.Receipt{Status: status, GasUsed: gasUsed, Logs: logs}
}

// bridgeLog builds a log emitted by addr with the given first topic.
func bridgeLog(addr common.Address, topic common.Hash) *ethtypes.Log {
	return &ethtypes.Log{Address: addr, Topics: []common.Hash{topic}}
}

// assetClaim builds a Claim row of a successful asset claim of originToken (native to
// originNetwork) in tx hash.
func assetClaim(txHash common.Hash, originNetwork uint32, originToken common.Address) *claimsynctypes.Claim {
	return &claimsynctypes.Claim{
		TxHash:        txHash,
		OriginNetwork: originNetwork,
		OriginAddress: originToken,
		GlobalIndex:   big.NewInt(1),
		Amount:        big.NewInt(1),
		Type:          claimsynctypes.ClaimEvent,
		IsMessage:     false,
	}
}

// eligibleClaimFixture puts one fully eligible repeat claim of token (originNetwork,
// originToken) into the chain and reader fakes: a successful tx sent to bridgeAddr whose
// receipt holds exactly one bridge ClaimEvent.
func eligibleClaimFixture(
	chain *fakeChain, reader *fakeClaimsReader, bridgeAddr common.Address,
	txHash common.Hash, originNetwork uint32, originToken common.Address, gasUsed uint64,
) {
	chain.txs[txHash] = claimTx(bridgeAddr)
	chain.receipts[txHash] = receiptOf(
		ethtypes.ReceiptStatusSuccessful, gasUsed, bridgeLog(bridgeAddr, claimEventTopic))
	reader.claims = append(reader.claims, assetClaim(txHash, originNetwork, originToken))
}

// eip1559Header builds a latest header of an EIP-1559 chain at blockNumber with baseFee.
func eip1559Header(blockNumber uint64, baseFee *big.Int) *ethtypes.Header {
	return &ethtypes.Header{Number: new(big.Int).SetUint64(blockNumber), BaseFee: baseFee}
}

// addressReturn packs a common.Address as the 32-byte ABI return of getTokenWrappedAddress.
func addressReturn(addr common.Address) []byte {
	out := make([]byte, abiWordLen)
	copy(out[abiWordLen-common.AddressLength:], addr.Bytes())
	return out
}

// testHashes are distinct transaction hashes shared by the claim tests.
var testHashes = hashes(12)

// requireCostMatches asserts that Cost equals GasUnits × the effective gas price.
func requireCostMatches(t *testing.T, what string, estimate *FeeEstimate, gas *GasPriceInfo) {
	t.Helper()
	require.NotNil(t, estimate, what)
	price, ok := new(big.Int).SetString(string(gas.GasPrice), 10)
	require.True(t, ok, "gas price must be a decimal string: %s", gas.GasPrice)
	want := new(big.Int).Mul(new(big.Int).SetUint64(estimate.GasUnits), price)
	got, ok := new(big.Int).SetString(string(estimate.Cost), 10)
	require.True(t, ok, "cost must be a decimal string: %s", estimate.Cost)
	require.Zero(t, want.Cmp(got), "%s: cost %s must equal gas_units %d x gas_price %s",
		what, estimate.Cost, estimate.GasUnits, gas.GasPrice)
}
