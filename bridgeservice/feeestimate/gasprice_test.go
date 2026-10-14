package feeestimate

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// newEstimator builds an estimator over the fakes with a controllable clock. The TTLs
// default to an hour so tests drive expiry through the clock, not through real waiting.
func newEstimator(
	t *testing.T, cfg Config, chain *fakeChain, claims *fakeClaimsReader,
) (*Estimator, *fakeClock) {
	t.Helper()
	if cfg.GasPriceTTL == 0 {
		cfg.GasPriceTTL = time.Hour
	}
	if cfg.StatsTTL == 0 {
		cfg.StatsTTL = time.Hour
	}
	estimator, err := New(cfg, chain, claims, nil)
	require.NoError(t, err)
	clock := newFakeClock()
	estimator.setClock(clock.now)
	return estimator, clock
}

// eip1559Chain equips the fake chain with an EIP-1559 header, tip and (unused) gas price.
func eip1559Chain() *fakeChain {
	chain := newFakeChain()
	chain.header = eip1559Header(123, big.NewInt(15000000000))
	chain.tipCap = big.NewInt(2000000000)
	chain.gasPrice = big.NewInt(17000000000)
	return chain
}

// legacyHeader builds a latest header without a base fee: a chain without EIP-1559.
func legacyHeader(blockNumber uint64) *ethtypes.Header {
	return &ethtypes.Header{Number: new(big.Int).SetUint64(blockNumber)}
}

func TestReadGasPrice_EIP1559Chain(t *testing.T) {
	chain := eip1559Chain()
	estimator, _ := newEstimator(t, Config{NetworkID: 1}, chain, &fakeClaimsReader{})

	snapshot, err := estimator.readGasPrice(context.Background())
	require.NoError(t, err)

	require.NotNil(t, snapshot.info.BaseFee)
	require.Equal(t, "15000000000", string(*snapshot.info.BaseFee))
	require.NotNil(t, snapshot.info.PriorityFee)
	require.Equal(t, "2000000000", string(*snapshot.info.PriorityFee))
	require.Equal(t, "17000000000", string(snapshot.info.GasPrice))
	require.Equal(t, uint64(123), snapshot.info.BlockNumber)
	require.Equal(t, 0, new(big.Int).SetUint64(17000000000).Cmp(snapshot.price()))
}

func TestReadGasPrice_ChainWithoutEIP1559(t *testing.T) {
	chain := newFakeChain()
	chain.header = legacyHeader(55)
	chain.gasPrice = big.NewInt(2000000000)
	estimator, _ := newEstimator(t, Config{NetworkID: 1}, chain, &fakeClaimsReader{})

	snapshot, err := estimator.readGasPrice(context.Background())
	require.NoError(t, err)

	require.Nil(t, snapshot.info.BaseFee)
	require.Nil(t, snapshot.info.PriorityFee)
	require.Equal(t, "2000000000", string(snapshot.info.GasPrice))
	require.Equal(t, uint64(55), snapshot.info.BlockNumber)
}

func TestReadGasPrice_RPCFailures(t *testing.T) {
	t.Run("header read fails", func(t *testing.T) {
		chain := newFakeChain()
		chain.header = nil
		estimator, _ := newEstimator(t, Config{NetworkID: 1}, chain, &fakeClaimsReader{})
		_, err := estimator.readGasPrice(context.Background())
		require.ErrorContains(t, err, "reading latest header")
	})

	t.Run("tip suggestion fails on an EIP-1559 chain", func(t *testing.T) {
		chain := eip1559Chain()
		chain.tipCap = nil
		estimator, _ := newEstimator(t, Config{NetworkID: 1}, chain, &fakeClaimsReader{})
		_, err := estimator.readGasPrice(context.Background())
		require.ErrorContains(t, err, "suggesting priority fee")
	})

	t.Run("gas price suggestion fails on a legacy chain", func(t *testing.T) {
		chain := newFakeChain()
		chain.header = legacyHeader(55)
		chain.gasPrice = nil
		estimator, _ := newEstimator(t, Config{NetworkID: 1}, chain, &fakeClaimsReader{})
		_, err := estimator.readGasPrice(context.Background())
		require.ErrorContains(t, err, "suggesting gas price")
	})
}

func TestGasPriceCacheTTL(t *testing.T) {
	chain := eip1559Chain()
	estimator, clock := newEstimator(
		t, Config{NetworkID: 1, GasPriceTTL: 5 * time.Second}, chain, &fakeClaimsReader{})

	ctx := context.Background()
	first, err := estimator.gasPrice.get(ctx, estimator.readGasPrice)
	require.NoError(t, err)
	second, err := estimator.gasPrice.get(ctx, estimator.readGasPrice)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, int64(1), chain.headerCalls.Load(), "cached within the TTL")

	clock.advance(5 * time.Second)
	_, err = estimator.gasPrice.get(ctx, estimator.readGasPrice)
	require.NoError(t, err)
	require.Equal(t, int64(2), chain.headerCalls.Load(), "re-read after the TTL")
}

func TestCostOf(t *testing.T) {
	t.Run("cost equals gas units times gas price", func(t *testing.T) {
		require.Equal(t, "1870000000000000", string(costOf(110000, big.NewInt(17000000000))))
	})

	t.Run("zero gas units and zero price", func(t *testing.T) {
		require.Equal(t, "0", string(costOf(0, big.NewInt(17000000000))))
		require.Equal(t, "0", string(costOf(110000, big.NewInt(0))))
	})

	t.Run("large values stay exact", func(t *testing.T) {
		price, _ := new(big.Int).SetString("20000000000000000000000", 10) // 20000 ETH in wei
		require.Equal(t, "22000000000000000000000000000", string(costOf(1100000, price)))
	})
}

func TestGetTokenWrappedAddress_PacksTheWellKnownSelector(t *testing.T) {
	chain := newFakeChain()
	var gotTo *common.Address
	var gotData []byte
	chain.callFn = func(msg ethereum.CallMsg) ([]byte, error) {
		if len(msg.Data) != 4+2*abiWordLen {
			// The construction-time gas-token reads revert: no custom gas token.
			return nil, errRPC("eth_call")
		}
		gotTo = msg.To
		gotData = msg.Data
		return addressReturn(wrapperAddr), nil
	}
	estimator, _ := newEstimator(t, Config{NetworkID: 1, BridgeAddr: bridgeAddr}, chain, &fakeClaimsReader{})

	originToken := wrappedToken
	wrapped, err := estimator.getTokenWrappedAddress(context.Background(), 11, originToken)
	require.NoError(t, err)
	require.Equal(t, wrapperAddr, wrapped)
	require.NotNil(t, gotTo)
	require.Equal(t, bridgeAddr, *gotTo)

	// The calldata is the well-known selector (0x22e95f2c, as in the generated binding)
	// followed by the ABI-encoded uint32 and address arguments.
	require.Len(t, gotData, 4+2*abiWordLen)
	require.Equal(t, common.Hex2Bytes("22e95f2c"), gotData[:4])
	require.Equal(t, uint64(11), new(big.Int).SetBytes(gotData[4:4+abiWordLen]).Uint64())
	require.Equal(t, originToken, common.BytesToAddress(gotData[4+abiWordLen:]))
}

func TestGetTokenWrappedAddress_FailsClosed(t *testing.T) {
	t.Run("eth_call error", func(t *testing.T) {
		chain := newFakeChain()
		chain.callFn = func(ethereum.CallMsg) ([]byte, error) { return nil, errRPC("eth_call") }
		estimator, _ := newEstimator(t, Config{NetworkID: 1, BridgeAddr: bridgeAddr},
			chain, &fakeClaimsReader{})
		_, err := estimator.getTokenWrappedAddress(context.Background(), 11, wrappedToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
	})

	t.Run("short return", func(t *testing.T) {
		chain := newFakeChain()
		chain.callFn = func(ethereum.CallMsg) ([]byte, error) { return nil, nil }
		estimator, _ := newEstimator(t, Config{NetworkID: 1, BridgeAddr: bridgeAddr},
			chain, &fakeClaimsReader{})
		_, err := estimator.getTokenWrappedAddress(context.Background(), 11, wrappedToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
	})
}
