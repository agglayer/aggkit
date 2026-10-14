package feeestimate

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// fakeBridgesReader serves GetBridgesPaged over a fixed, newest-first set of bridges, with
// the real storage's semantics: 1-based pages keyed to the call's page size, the compacted
// total count, an empty page when there are no rows, and an error for a page past the end.
type fakeBridgesReader struct {
	mu       sync.Mutex
	bridges  []*bridgesync.Bridge
	err      error
	getCalls atomic.Int64
}

func (f *fakeBridgesReader) GetBridgesPaged(
	_ context.Context, page, pageSize uint32, _ *uint64, _ []uint32, _ string,
) ([]*bridgesync.Bridge, int, error) {
	f.getCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, 0, f.err
	}
	// The real storage answers an empty page rather than an error when there are no rows
	// at all, and an error for a page past the end of a non-empty table.
	if len(f.bridges) == 0 {
		return []*bridgesync.Bridge{}, 0, nil
	}
	offset := (int(page) - 1) * int(pageSize)
	if offset >= len(f.bridges) {
		return nil, 0, fmt.Errorf(
			"invalid page number (page=%d, size=%d, total=%d)", page, pageSize, len(f.bridges))
	}
	end := min(offset+int(pageSize), len(f.bridges))
	return f.bridges[offset:end], len(f.bridges), nil
}

// newBridgeEstimator builds an estimator over the fakes with a controllable clock, with
// only the bridges reader configured (the claims reader is nil). The TTLs default to an
// hour so tests drive expiry through the clock, not through real waiting.
func newBridgeEstimator(
	t *testing.T, cfg Config, chain *fakeChain, bridges *fakeBridgesReader,
) (*Estimator, *fakeClock) {
	t.Helper()
	if cfg.GasPriceTTL == 0 {
		cfg.GasPriceTTL = time.Hour
	}
	if cfg.StatsTTL == 0 {
		cfg.StatsTTL = time.Hour
	}
	require.NotNil(t, bridges, "a nil bridges reader would reach New as a typed-nil interface")
	estimator, err := New(cfg, chain, nil, bridges)
	require.NoError(t, err)
	clock := newFakeClock()
	estimator.setClock(clock.now)
	return estimator, clock
}

// bridgeTx builds a legacy transaction sent to to carrying value wei. The fakes map
// transactions by the hash the test chose, so the tx's own hash is irrelevant.
func bridgeTx(to common.Address, value int64) *ethtypes.Transaction {
	return ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: 1, To: &to, Value: big.NewInt(value), Gas: 300000})
}

// assetBridgeRow builds a Bridge row of an asset bridge of originToken (native to
// originNetwork) in tx hash.
func assetBridgeRow(txHash common.Hash, originNetwork uint32, originToken common.Address) *bridgesync.Bridge {
	return &bridgesync.Bridge{
		TxHash:        txHash,
		LeafType:      bridgeLeafTypeAsset,
		OriginNetwork: originNetwork,
		OriginAddress: originToken,
	}
}

// eligibleBridgeFixture puts one fully eligible bridge sample into the chain and reader
// fakes: a successful tx sent to bridgeAddr whose receipt holds exactly one bridge
// BridgeEvent, of originToken (native to originNetwork) and carrying value wei of msg.value
// (non-zero makes it a native bridge).
func eligibleBridgeFixture(
	chain *fakeChain, reader *fakeBridgesReader, bridgeAddr common.Address,
	txHash common.Hash, originNetwork uint32, originToken common.Address, gasUsed uint64, value int64,
) {
	chain.txs[txHash] = bridgeTx(bridgeAddr, value)
	chain.receipts[txHash] = receiptOf(
		ethtypes.ReceiptStatusSuccessful, gasUsed, bridgeLog(bridgeAddr, bridgeEventTopic))
	reader.bridges = append(reader.bridges, assetBridgeRow(txHash, originNetwork, originToken))
}

// wrappedTokenInfo is the (origin network, origin token) pair the bridge maps a wrapped
// token back to.
type wrappedTokenInfo struct {
	originNetwork uint32
	originToken   common.Address
}

// tokenInfoLookup equips the fake chain with the eth_call answer of
// wrappedTokenToTokenInfo: forToken maps back to info, every other address returns the
// zero pair (not a wrapped token the bridge knows). A nil info maps no token at all.
func tokenInfoLookup(chain *fakeChain, forToken common.Address, info *wrappedTokenInfo) {
	chain.callFn = func(msg ethereum.CallMsg) ([]byte, error) {
		out := make([]byte, tokenInfoWords*abiWordLen)
		if info != nil && len(msg.Data) == 4+abiWordLen &&
			common.BytesToAddress(msg.Data[4:]) == forToken {
			// The uint32 word is right-aligned on its last 4 bytes, the address word on
			// its last 20, exactly as the ABI encoder packs them.
			binary.BigEndian.PutUint32(out[abiWordLen-4:], info.originNetwork)
			copy(out[tokenInfoWords*abiWordLen-common.AddressLength:], info.originToken.Bytes())
		}
		return out, nil
	}
}

// TestEstimateBridgeFee_NotConfiguredFailsClosed pins the nil bridges reader path: the
// estimate fails closed instead of answering anything.
func TestEstimateBridgeFee_NotConfiguredFailsClosed(t *testing.T) {
	chain := eip1559Chain()
	estimator, err := New(Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, nil, nil)
	require.NoError(t, err)

	_, err = estimator.EstimateBridgeFee(context.Background(), wrappedToken)
	require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
	require.ErrorContains(t, err, "not configured")
}

// TestEstimateBridgeFee_EachMechanism walks one request per mechanism over one sample of
// each: the samples are bucketed by their tx and row (classification), and the requests are
// resolved by the token address and the bridge's wrapped-token mapping (resolution).
func TestEstimateBridgeFee_EachMechanism(t *testing.T) {
	chain := eip1559Chain()
	reader := &fakeBridgesReader{}
	// One sample of each mechanism on an L2: a native bridge carrying msg.value, an
	// origin-native ERC20 lock, and a wrapped-token burn.
	eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, zeroToken, 50000, 1)
	eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[1], l2Network, otherToken, 80000, 0)
	eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[2], l1Origin, wrappedToken, 70000, 0)
	// The bridge knows wrappedToken as a wrapped token; any other address it does not know.
	tokenInfoLookup(chain, wrappedToken, &wrappedTokenInfo{originNetwork: l1Origin, originToken: otherToken})

	estimator, _ := newBridgeEstimator(
		t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)

	t.Run("token 0x0 resolves the native mechanism without any eth_call", func(t *testing.T) {
		callsBefore := chain.callCalls.Load()
		resp, err := estimator.EstimateBridgeFee(context.Background(), zeroToken)
		require.NoError(t, err)
		require.Equal(t, l2Network, resp.NetworkID)
		require.Equal(t, types.Address(zeroToken.Hex()), resp.TokenAddress)
		require.Equal(t, Native, resp.Mechanism)
		require.Equal(t, uint64(50000), resp.Estimate.GasUnits)
		require.Equal(t, 1, resp.Estimate.SampleCount)
		require.Equal(t, sourceMechanism, resp.Estimate.Source)
		requireCostMatches(t, "native bridge", &resp.Estimate, &resp.GasPrice)
		require.Greater(t, resp.ComputedAt, uint64(0))
		require.Equal(t, callsBefore, chain.callCalls.Load(), "the gas token needs no lookup")
	})

	t.Run("a token unknown to the mapping resolves the erc20 lock mechanism", func(t *testing.T) {
		resp, err := estimator.EstimateBridgeFee(context.Background(), otherToken)
		require.NoError(t, err)
		require.Equal(t, ERC20Lock, resp.Mechanism)
		require.Equal(t, uint64(80000), resp.Estimate.GasUnits)
		require.Equal(t, 1, resp.Estimate.SampleCount)
		require.Equal(t, sourceMechanism, resp.Estimate.Source)
		requireCostMatches(t, "erc20 lock", &resp.Estimate, &resp.GasPrice)
	})

	t.Run("a token the bridge maps back resolves the wrapped burn mechanism", func(t *testing.T) {
		resp, err := estimator.EstimateBridgeFee(context.Background(), wrappedToken)
		require.NoError(t, err)
		require.Equal(t, WrappedBurn, resp.Mechanism)
		require.Equal(t, uint64(70000), resp.Estimate.GasUnits)
		require.Equal(t, 1, resp.Estimate.SampleCount)
		requireCostMatches(t, "wrapped burn", &resp.Estimate, &resp.GasPrice)
	})
}

// TestClassifyBridgeObservation is the sample-classification half of the mechanism rule, as a
// table over the pure classifier: msg.value decides native before the origin network is
// consulted, then the origin network separates an ERC20 lock from a wrapped burn.
func TestClassifyBridgeObservation(t *testing.T) {
	tests := []struct {
		name      string
		value     int64
		origin    uint32
		networkID uint32
		want      Mechanism
	}{
		{
			name:  "msg.value bridges the native gas token",
			value: 1, origin: l2Network, networkID: l2Network,
			want: Native,
		},
		{
			name:  "msg.value wins over a foreign origin: custom gas token L2",
			value: 1, origin: l1Origin, networkID: l2Network,
			want: Native,
		},
		{
			name:  "msg.value wins over a non-zero foreign origin",
			value: 1, origin: 5, networkID: l2Network,
			want: Native,
		},
		{
			name:  "no value, token native to this network: an ERC20 lock",
			value: 0, origin: l2Network, networkID: l2Network,
			want: ERC20Lock,
		},
		{
			name:  "no value, token from another network: a wrapped burn",
			value: 0, origin: l1Origin, networkID: l2Network,
			want: WrappedBurn,
		},
		{
			name:  "L1: no value, L1-native token: an ERC20 lock",
			value: 0, origin: 0, networkID: 0,
			want: ERC20Lock,
		},
		{
			name:  "L1: no value, L2-native token: a wrapped burn",
			value: 0, origin: l2Network, networkID: 0,
			want: WrappedBurn,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := &txObservation{
				tx: bridgeTx(bridgeAddr, tt.value),
				receipt: receiptOf(ethtypes.ReceiptStatusSuccessful, 123000,
					bridgeLog(bridgeAddr, bridgeEventTopic)),
			}
			row := assetBridgeRow(testHashes[0], tt.origin, otherToken)
			gasUsed, mechanism, ok := classifyBridgeObservation(obs, bridgeAddr, tt.networkID, row)
			require.True(t, ok)
			require.Equal(t, tt.want, mechanism)
			require.Equal(t, uint64(123000), gasUsed)
		})
	}
}

// TestClassifyBridgeObservation_IneligibleObservations is the bridge-side half of the
// sample eligibility filter: only successful txs sent directly to the bridge, holding
// exactly one bridge-emitted BridgeEvent, count as samples.
func TestClassifyBridgeObservation_IneligibleObservations(t *testing.T) {
	router := common.HexToAddress("0x7777777777777777777777777777777777777777")
	tests := []struct {
		name string
		obs  *txObservation
	}{
		{
			name: "reverted tx is not a sample",
			obs: &txObservation{
				tx: bridgeTx(bridgeAddr, 1),
				receipt: receiptOf(ethtypes.ReceiptStatusFailed, 120000,
					bridgeLog(bridgeAddr, bridgeEventTopic)),
			},
		},
		{
			name: "tx sent elsewhere than the bridge",
			obs: &txObservation{
				tx: bridgeTx(router, 1),
				receipt: receiptOf(ethtypes.ReceiptStatusSuccessful, 120000,
					bridgeLog(bridgeAddr, bridgeEventTopic)),
			},
		},
		{
			name: "no BridgeEvent in the receipt",
			obs: &txObservation{
				tx:      bridgeTx(bridgeAddr, 1),
				receipt: receiptOf(ethtypes.ReceiptStatusSuccessful, 120000),
			},
		},
		{
			name: "two BridgeEvents in one receipt: a multi-bridge tx",
			obs: &txObservation{
				tx: bridgeTx(bridgeAddr, 1),
				receipt: receiptOf(ethtypes.ReceiptStatusSuccessful, 120000,
					bridgeLog(bridgeAddr, bridgeEventTopic),
					bridgeLog(bridgeAddr, bridgeEventTopic)),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := assetBridgeRow(testHashes[0], l2Network, otherToken)
			_, _, ok := classifyBridgeObservation(tt.obs, bridgeAddr, l2Network, row)
			require.False(t, ok)
		})
	}
}

// TestEstimateBridgeFee_CustomGasTokenL2 covers the custom-gas-token case of the mechanism
// rule: the native token's BridgeEvent carries the gas-token origin network rather than the
// network the bridge runs on, so only msg.value marks the bridge native, and WETH (a
// wrapped token) is burned.
func TestEstimateBridgeFee_CustomGasTokenL2(t *testing.T) {
	chain := eip1559Chain()
	reader := &fakeBridgesReader{}
	// A native bridge of the gas token: msg.value > 0 with an origin network that is not
	// this L2.
	eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, zeroToken, 50000, 1)
	// A WETH bridge: no value, a wrapped token whose origin is not this L2.
	weth := common.HexToAddress("0x4444444444444444444444444444444444444444")
	eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[1], l1Origin, weth, 70000, 0)
	tokenInfoLookup(chain, weth, &wrappedTokenInfo{originNetwork: l1Origin, originToken: otherToken})

	estimator, _ := newBridgeEstimator(
		t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)

	resp, err := estimator.EstimateBridgeFee(context.Background(), zeroToken)
	require.NoError(t, err)
	require.Equal(t, Native, resp.Mechanism)
	require.Equal(t, uint64(50000), resp.Estimate.GasUnits)
	require.Equal(t, 1, resp.Estimate.SampleCount)

	resp, err = estimator.EstimateBridgeFee(context.Background(), weth)
	require.NoError(t, err)
	require.Equal(t, WrappedBurn, resp.Mechanism)
	require.Equal(t, uint64(70000), resp.Estimate.GasUnits)
	requireCostMatches(t, "weth burn", &resp.Estimate, &resp.GasPrice)
}

// TestEstimateBridgeFee_SampleEligibilityFiltering proves every row-level exclusion at the
// estimator level: all ineligible rows would land in the same erc20_lock bucket as the one
// clean sample, so only the clean one may be counted.
func TestEstimateBridgeFee_SampleEligibilityFiltering(t *testing.T) {
	chain := eip1559Chain()
	reader := &fakeBridgesReader{}
	tokenInfoLookup(chain, common.Address{}, nil) // no token is known as wrapped

	txs := hashes(8)
	// The one eligible sample.
	eligibleBridgeFixture(chain, reader, bridgeAddr, txs[0], l2Network, otherToken, 100000, 0)
	// A failed (reverted) bridge tx.
	chain.txs[txs[1]] = bridgeTx(bridgeAddr, 0)
	chain.receipts[txs[1]] = receiptOf(ethtypes.ReceiptStatusFailed, 120000,
		bridgeLog(bridgeAddr, bridgeEventTopic))
	reader.bridges = append(reader.bridges, assetBridgeRow(txs[1], l2Network, otherToken))
	// A tx sent to a router contract, not the bridge.
	chain.txs[txs[2]] = bridgeTx(common.HexToAddress("0x7777777777777777777777777777777777777777"), 0)
	chain.receipts[txs[2]] = receiptOf(ethtypes.ReceiptStatusSuccessful, 130000,
		bridgeLog(bridgeAddr, bridgeEventTopic))
	reader.bridges = append(reader.bridges, assetBridgeRow(txs[2], l2Network, otherToken))
	// A multi-bridge tx: two BridgeEvents in one receipt.
	chain.txs[txs[3]] = bridgeTx(bridgeAddr, 0)
	chain.receipts[txs[3]] = receiptOf(ethtypes.ReceiptStatusSuccessful, 140000,
		bridgeLog(bridgeAddr, bridgeEventTopic),
		bridgeLog(bridgeAddr, bridgeEventTopic))
	reader.bridges = append(reader.bridges, assetBridgeRow(txs[3], l2Network, otherToken))
	// A message leave: excluded even though its tx is eligible.
	chain.txs[txs[4]] = bridgeTx(bridgeAddr, 0)
	chain.receipts[txs[4]] = receiptOf(ethtypes.ReceiptStatusSuccessful, 150000,
		bridgeLog(bridgeAddr, bridgeEventTopic))
	messageRow := assetBridgeRow(txs[4], l2Network, otherToken)
	messageRow.LeafType = uint8(bridgesynctypes.LeafTypeMessage)
	reader.bridges = append(reader.bridges, messageRow)
	// A backward-LET row: a synthetic event row, not a user bridge.
	chain.txs[txs[5]] = bridgeTx(bridgeAddr, 0)
	chain.receipts[txs[5]] = receiptOf(ethtypes.ReceiptStatusSuccessful, 160000,
		bridgeLog(bridgeAddr, bridgeEventTopic))
	backward := assetBridgeRow(txs[5], l2Network, otherToken)
	backward.Source = bridgesync.BridgeSourceBackwardLET
	reader.bridges = append(reader.bridges, backward)
	// A forward-LET row: same.
	chain.txs[txs[6]] = bridgeTx(bridgeAddr, 0)
	chain.receipts[txs[6]] = receiptOf(ethtypes.ReceiptStatusSuccessful, 170000,
		bridgeLog(bridgeAddr, bridgeEventTopic))
	forward := assetBridgeRow(txs[6], l2Network, otherToken)
	forward.Source = bridgesync.BridgeSourceForwardLET
	reader.bridges = append(reader.bridges, forward)
	// A bridge whose receipt the RPC no longer knows (reorged away): skipped, not an error.
	reader.bridges = append(reader.bridges, assetBridgeRow(txs[7], l2Network, otherToken))
	chain.notFound[txs[7]] = true

	estimator, _ := newBridgeEstimator(
		t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
	resp, err := estimator.EstimateBridgeFee(context.Background(), otherToken)
	require.NoError(t, err)

	// The bucket counts exactly the one clean sample.
	require.Equal(t, ERC20Lock, resp.Mechanism)
	require.Equal(t, 1, resp.Estimate.SampleCount)
	require.Equal(t, uint64(100000), resp.Estimate.GasUnits, "small bucket quotes the maximum")
}

// TestEstimateBridgeFee_FailClosedPaths walks every path that must answer an error instead
// of an estimate: no samples at all, an empty bucket for the requested mechanism, and
// each failing RPC call.
func TestEstimateBridgeFee_FailClosedPaths(t *testing.T) {
	t.Run("no bridges at all", func(t *testing.T) {
		chain := eip1559Chain()
		estimator, _ := newBridgeEstimator(
			t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, &fakeBridgesReader{})

		_, err := estimator.EstimateBridgeFee(context.Background(), zeroToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
		require.ErrorContains(t, err, "no native samples")
	})

	t.Run("requested mechanism bucket empty even though another mechanism has samples", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeBridgesReader{}
		// Only an erc20 lock sample exists; the request is for a wrapped burn.
		eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[0], l2Network, otherToken, 100000, 0)
		tokenInfoLookup(chain, wrappedToken, &wrappedTokenInfo{
			originNetwork: l1Origin, originToken: otherToken})
		estimator, _ := newBridgeEstimator(
			t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)

		_, err := estimator.EstimateBridgeFee(context.Background(), wrappedToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
		require.ErrorContains(t, err, "no wrapped_burn samples")
	})

	t.Run("bridges reader failure", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeBridgesReader{err: errRPC("db")}
		estimator, _ := newBridgeEstimator(
			t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)

		_, err := estimator.EstimateBridgeFee(context.Background(), zeroToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
		require.ErrorContains(t, err, "computing the bridge statistics")
	})

	t.Run("receipt RPC failure", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeBridgesReader{}
		tokenInfoLookup(chain, common.Address{}, nil)
		hash := testHashes[0]
		eligibleBridgeFixture(chain, reader, bridgeAddr, hash, l2Network, otherToken, 100000, 0)
		chain.receiptErr[hash] = errRPC("eth_getTransactionReceipt")

		estimator, _ := newBridgeEstimator(
			t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
		_, err := estimator.EstimateBridgeFee(context.Background(), otherToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
		require.ErrorContains(t, err, "computing the bridge statistics")
	})

	t.Run("gas price RPC failure", func(t *testing.T) {
		chain := newFakeChain() // no header configured
		reader := &fakeBridgesReader{}
		tokenInfoLookup(chain, common.Address{}, nil)
		eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[0], l2Network, otherToken, 100000, 0)

		estimator, _ := newBridgeEstimator(
			t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
		_, err := estimator.EstimateBridgeFee(context.Background(), otherToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
		require.ErrorContains(t, err, "computing the gas price")
	})

	t.Run("wrapped token lookup eth_call failure", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeBridgesReader{}
		eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[0], l2Network, otherToken, 100000, 0)
		chain.callFn = func(ethereum.CallMsg) ([]byte, error) { return nil, errRPC("eth_call") }

		estimator, _ := newBridgeEstimator(
			t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
		_, err := estimator.EstimateBridgeFee(context.Background(), wrappedToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
		require.ErrorContains(t, err, "wrappedTokenToTokenInfo")
	})
}

// TestEstimateBridgeFee_L1Network runs the whole bridge estimate with the estimator on L1
// (network 0): native ETH, an L1-native ERC20 lock and a wrapped L2 token burn.
func TestEstimateBridgeFee_L1Network(t *testing.T) {
	chain := eip1559Chain()
	reader := &fakeBridgesReader{}
	eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, zeroToken, 60000, 1)
	eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[1], l1Origin, otherToken, 90000, 0)
	eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[2], l2Network, wrappedToken, 75000, 0)
	tokenInfoLookup(chain, wrappedToken, &wrappedTokenInfo{
		originNetwork: l2Network, originToken: otherToken})

	estimator, _ := newBridgeEstimator(t, Config{NetworkID: 0, BridgeAddr: bridgeAddr}, chain, reader)

	resp, err := estimator.EstimateBridgeFee(context.Background(), zeroToken)
	require.NoError(t, err)
	require.Equal(t, uint32(0), resp.NetworkID)
	require.Equal(t, Native, resp.Mechanism)
	require.Equal(t, uint64(60000), resp.Estimate.GasUnits)

	resp, err = estimator.EstimateBridgeFee(context.Background(), otherToken)
	require.NoError(t, err)
	require.Equal(t, ERC20Lock, resp.Mechanism)
	require.Equal(t, uint64(90000), resp.Estimate.GasUnits)

	resp, err = estimator.EstimateBridgeFee(context.Background(), wrappedToken)
	require.NoError(t, err)
	require.Equal(t, WrappedBurn, resp.Mechanism)
	require.Equal(t, uint64(75000), resp.Estimate.GasUnits)
}

// TestEstimateBridgeFee_ChainWithoutEIP1559 prices the estimate with eth_gasPrice on a
// chain without a base fee; base_fee and priority_fee are null.
func TestEstimateBridgeFee_ChainWithoutEIP1559(t *testing.T) {
	chain := newFakeChain()
	chain.header = legacyHeader(55)
	chain.gasPrice = big.NewInt(2000000000)
	reader := &fakeBridgesReader{}
	eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, zeroToken, 100000, 1)

	estimator, _ := newBridgeEstimator(
		t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
	resp, err := estimator.EstimateBridgeFee(context.Background(), zeroToken)
	require.NoError(t, err)

	require.Nil(t, resp.GasPrice.BaseFee)
	require.Nil(t, resp.GasPrice.PriorityFee)
	require.Equal(t, "2000000000", string(resp.GasPrice.GasPrice))
	require.Equal(t, uint64(55), resp.GasPrice.BlockNumber)
	requireCostMatches(t, "legacy chain", &resp.Estimate, &resp.GasPrice)
}

// TestEstimateBridgeFee_PercentileOnLargeBuckets quotes the p90 rather than the maximum
// once a mechanism bucket grows past the small-bucket threshold.
func TestEstimateBridgeFee_PercentileOnLargeBuckets(t *testing.T) {
	chain := eip1559Chain()
	reader := &fakeBridgesReader{}
	// Twelve native bridges with increasing gas: above the small-bucket threshold.
	txs := hashes(12)
	for i, hash := range txs {
		eligibleBridgeFixture(chain, reader, bridgeAddr, hash, l1Origin, zeroToken,
			uint64(50000+10000*i), 1)
	}

	estimator, _ := newBridgeEstimator(
		t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
	resp, err := estimator.EstimateBridgeFee(context.Background(), zeroToken)
	require.NoError(t, err)
	require.Equal(t, 12, resp.Estimate.SampleCount)
	// Nearest-rank p90 of 12 samples: the ceil(0.9*12) = 11th smallest = 150000, not the
	// maximum 160000.
	require.Equal(t, uint64(150000), resp.Estimate.GasUnits)
	requireCostMatches(t, "p90 bucket", &resp.Estimate, &resp.GasPrice)
}

// TestEstimateBridgeFee_StatsAndGasPriceCaching pins the TTL behaviour the bridge estimate
// shares with the claim one: statistics, gas price and transactions are each read once per
// TTL window, and the gas price refreshes independently of the statistics.
func TestEstimateBridgeFee_StatsAndGasPriceCaching(t *testing.T) {
	chain := eip1559Chain()
	reader := &fakeBridgesReader{}
	eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, zeroToken, 100000, 1)

	estimator, clock := newBridgeEstimator(
		t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr,
			GasPriceTTL: 5 * time.Second, StatsTTL: 60 * time.Second},
		chain, reader)

	ctx := context.Background()
	first, err := estimator.EstimateBridgeFee(ctx, zeroToken)
	require.NoError(t, err)

	// Within both TTLs: the bridges are read once and the header once.
	second, err := estimator.EstimateBridgeFee(ctx, zeroToken)
	require.NoError(t, err)
	require.Equal(t, int64(1), reader.getCalls.Load(), "statistics are cached")
	require.Equal(t, int64(1), chain.headerCalls.Load(), "gas price is cached")
	require.Equal(t, int64(1), chain.receiptCalls.Load(), "transactions are cached")
	require.Equal(t, first.Estimate, second.Estimate)
	require.Equal(t, first.ComputedAt, second.ComputedAt)

	// Past the gas price TTL only the header is re-read; the statistics stay cached.
	clock.advance(5 * time.Second)
	_, err = estimator.EstimateBridgeFee(ctx, zeroToken)
	require.NoError(t, err)
	require.Equal(t, int64(2), chain.headerCalls.Load())
	require.Equal(t, int64(1), reader.getCalls.Load())

	// Past the statistics TTL the bridges are read again; the receipts stay cached in the
	// LRU.
	clock.advance(60 * time.Second)
	_, err = estimator.EstimateBridgeFee(ctx, zeroToken)
	require.NoError(t, err)
	require.Equal(t, int64(2), reader.getCalls.Load())
	require.Equal(t, int64(1), chain.receiptCalls.Load())
}

// TestEstimateBridgeFee_WindowPaging pins the constant-page-size paging: a window larger
// than one page costs one reader call per page, a smaller window reads a single page and
// quotes only its samples.
func TestEstimateBridgeFee_WindowPaging(t *testing.T) {
	chain := eip1559Chain()

	// 250 native bridges with a window of 250: two reader pages (200 + 50).
	txs := hashes(250)
	reader := &fakeBridgesReader{}
	for _, hash := range txs {
		eligibleBridgeFixture(chain, reader, bridgeAddr, hash, l1Origin, zeroToken, 100000, 1)
	}
	estimator, _ := newBridgeEstimator(
		t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr, Window: 250}, chain, reader)

	resp, err := estimator.EstimateBridgeFee(context.Background(), zeroToken)
	require.NoError(t, err)
	require.Equal(t, 250, resp.Estimate.SampleCount)
	require.Equal(t, int64(2), reader.getCalls.Load())

	// A smaller window reads one page and quotes only its samples.
	smallReader := &fakeBridgesReader{}
	for _, hash := range txs {
		eligibleBridgeFixture(chain, smallReader, bridgeAddr, hash, l1Origin, zeroToken, 100000, 1)
	}
	// Give the small-window estimator fresh chain responses: the tx cache is shared through
	// the same chain fake, so the receipts already cached keep it cheap.
	estimator, _ = newBridgeEstimator(
		t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr, Window: 10}, chain, smallReader)
	resp, err = estimator.EstimateBridgeFee(context.Background(), zeroToken)
	require.NoError(t, err)
	require.Equal(t, 10, resp.Estimate.SampleCount)
	require.Equal(t, int64(1), smallReader.getCalls.Load())
}

// TestEstimateBridgeFee_MultiBridgeTxRowsDeduplicate keeps the fetch bounded when rows
// share one tx hash: the tx is fetched and counted once (and would be excluded anyway by
// the exactly-one-bridge-event rule).
func TestEstimateBridgeFee_MultiBridgeTxRowsDeduplicate(t *testing.T) {
	chain := eip1559Chain()
	reader := &fakeBridgesReader{}
	tokenInfoLookup(chain, common.Address{}, nil)

	hash := testHashes[0]
	chain.txs[hash] = bridgeTx(bridgeAddr, 0)
	chain.receipts[hash] = receiptOf(ethtypes.ReceiptStatusSuccessful, 100000,
		bridgeLog(bridgeAddr, bridgeEventTopic))
	reader.bridges = append(reader.bridges, assetBridgeRow(hash, l2Network, otherToken))
	reader.bridges = append(reader.bridges, assetBridgeRow(hash, l2Network, wrappedToken))

	estimator, _ := newBridgeEstimator(
		t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
	resp, err := estimator.EstimateBridgeFee(context.Background(), otherToken)
	require.NoError(t, err)
	// Both rows would be erc20_lock, but the shared tx only counts once.
	require.Equal(t, 1, resp.Estimate.SampleCount)
	require.Equal(t, int64(1), chain.receiptCalls.Load())
}

// TestEstimateBridgeFee_TokenInfoLookupCachedPerToken pins the TTL cache behind the bridge
// side's wrapped-token lookup: an answer — the zero pair ("not a wrapped token") included —
// is eth_called once per token and reused until the token facts TTL expires.
func TestEstimateBridgeFee_TokenInfoLookupCachedPerToken(t *testing.T) {
	ctx := context.Background()

	t.Run("answers are reused within the TTL, the zero pair included", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeBridgesReader{}
		// One lock and one burn sample so both resolved mechanisms are served.
		eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[0], l2Network, otherToken, 80000, 0)
		eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[1], l1Origin, wrappedToken, 70000, 0)
		tokenInfoLookup(chain, wrappedToken, &wrappedTokenInfo{originNetwork: l1Origin, originToken: otherToken})
		estimator, _ := newBridgeEstimator(
			t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
		// The construction's gas-token identity read is counted too (its answer fails the
		// length check, so no custom gas token results); only the wrapped-token lookups
		// matter below.
		base := chain.callCalls.Load()

		resp, err := estimator.EstimateBridgeFee(ctx, wrappedToken)
		require.NoError(t, err)
		require.Equal(t, WrappedBurn, resp.Mechanism)
		resp, err = estimator.EstimateBridgeFee(ctx, wrappedToken)
		require.NoError(t, err)
		require.Equal(t, WrappedBurn, resp.Mechanism)
		require.Equal(t, base+1, chain.callCalls.Load(), "the wrapped-token lookup is cached per token")

		// A different token is looked up separately; its zero pair is cached too.
		resp, err = estimator.EstimateBridgeFee(ctx, otherToken)
		require.NoError(t, err)
		require.Equal(t, ERC20Lock, resp.Mechanism)
		require.Equal(t, base+2, chain.callCalls.Load())
		resp, err = estimator.EstimateBridgeFee(ctx, otherToken)
		require.NoError(t, err)
		require.Equal(t, ERC20Lock, resp.Mechanism)
		require.Equal(t, base+2, chain.callCalls.Load(), "the zero pair is cached, zero answer semantics kept")
	})

	t.Run("the cached answer expires after the token facts TTL", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeBridgesReader{}
		eligibleBridgeFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, wrappedToken, 70000, 0)
		tokenInfoLookup(chain, wrappedToken, &wrappedTokenInfo{originNetwork: l1Origin, originToken: otherToken})
		estimator, _ := newBridgeEstimator(t, Config{
			NetworkID: l2Network, BridgeAddr: bridgeAddr, TokenFactsTTL: 10 * time.Millisecond,
		}, chain, reader)
		base := chain.callCalls.Load()

		_, err := estimator.EstimateBridgeFee(ctx, wrappedToken)
		require.NoError(t, err)
		require.Equal(t, base+1, chain.callCalls.Load())
		time.Sleep(50 * time.Millisecond)
		_, err = estimator.EstimateBridgeFee(ctx, wrappedToken)
		require.NoError(t, err)
		require.Equal(t, base+2, chain.callCalls.Load(), "the cached token info expired")
	})
}

// TestWrappedTokenToTokenInfo_PacksTheWellKnownSelector pins the eth_call the mechanism
// resolution makes: the calldata is the well-known selector 0x318aee3d followed by the
// ABI-encoded address, and the two-word return decodes back to (network, token).
func TestWrappedTokenToTokenInfo_PacksTheWellKnownSelector(t *testing.T) {
	chain := newFakeChain()
	var gotTo *common.Address
	var gotData []byte
	chain.callFn = func(msg ethereum.CallMsg) ([]byte, error) {
		gotTo = msg.To
		gotData = msg.Data
		out := make([]byte, tokenInfoWords*abiWordLen)
		binary.BigEndian.PutUint32(out[abiWordLen-4:], 7)
		copy(out[tokenInfoWords*abiWordLen-common.AddressLength:], otherToken.Bytes())
		return out, nil
	}
	estimator, _ := newBridgeEstimator(
		t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, &fakeBridgesReader{})

	originNetwork, originToken, err := estimator.wrappedTokenToTokenInfo(
		context.Background(), wrappedToken)
	require.NoError(t, err)
	require.Equal(t, uint32(7), originNetwork)
	require.Equal(t, otherToken, originToken)
	require.NotNil(t, gotTo)
	require.Equal(t, bridgeAddr, *gotTo)
	require.Len(t, gotData, 4+abiWordLen)
	require.Equal(t, common.Hex2Bytes("318aee3d"), gotData[:4])
	require.Equal(t, wrappedToken, common.BytesToAddress(gotData[4:]))
}

// TestWrappedTokenToTokenInfo_FailsClosed covers the RPC failure and the malformed return.
func TestWrappedTokenToTokenInfo_FailsClosed(t *testing.T) {
	t.Run("eth_call error", func(t *testing.T) {
		chain := newFakeChain()
		chain.callFn = func(ethereum.CallMsg) ([]byte, error) { return nil, errRPC("eth_call") }
		estimator, _ := newBridgeEstimator(
			t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, &fakeBridgesReader{})
		_, _, err := estimator.wrappedTokenToTokenInfo(context.Background(), wrappedToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
	})

	t.Run("short return", func(t *testing.T) {
		chain := newFakeChain()
		chain.callFn = func(ethereum.CallMsg) ([]byte, error) { return nil, nil }
		estimator, _ := newBridgeEstimator(
			t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, &fakeBridgesReader{})
		_, _, err := estimator.wrappedTokenToTokenInfo(context.Background(), wrappedToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
	})
}

// Compile-time check that the fake satisfies the reader interface.
var _ BridgesReader = (*fakeBridgesReader)(nil)
