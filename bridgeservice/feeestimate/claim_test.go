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

// Test fixture addresses (valid hex, distinct).
var (
	bridgeAddr   = common.HexToAddress("0x9999999999999999999999999999999999999999")
	l2Network    = uint32(1)
	l1Origin     = uint32(0)
	wrappedToken = common.HexToAddress("0x1111111111111111111111111111111111111111")
	otherToken   = common.HexToAddress("0x2222222222222222222222222222222222222222")
	wrapperAddr  = common.HexToAddress("0x3333333333333333333333333333333333333333")
	zeroToken    = common.Address{}
)

// firstClaimFixture puts one fully eligible first claim (the tx deployed the wrapper) of
// wrappedToken into both fakes.
func firstClaimFixture(
	chain *fakeChain, reader *fakeClaimsReader, txHash common.Hash, originNetwork uint32,
	originToken common.Address, gasUsed uint64,
) {
	chain.txs[txHash] = claimTx(bridgeAddr)
	chain.receipts[txHash] = receiptOf(ethtypes.ReceiptStatusSuccessful, gasUsed,
		bridgeLog(bridgeAddr, claimEventTopic),
		bridgeLog(bridgeAddr, newWrappedTokenTopic))
	reader.claims = append(reader.claims, assetClaim(txHash, originNetwork, originToken))
}

// wrapperLookup equips the fake chain with the eth_call answer of getTokenWrappedAddress:
// wrapped when non-nil, the zero address (no wrapper yet) when nil.
func wrapperLookup(chain *fakeChain, wrapped *common.Address) {
	chain.callFn = func(ethereum.CallMsg) ([]byte, error) {
		addr := common.Address{}
		if wrapped != nil {
			addr = *wrapped
		}
		return addressReturn(addr), nil
	}
}

func TestEstimateClaimFee_NotConfiguredFailsClosed(t *testing.T) {
	chain := eip1559Chain()
	estimator, err := New(Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, nil, nil)
	require.NoError(t, err)

	_, err = estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
	require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
	require.ErrorContains(t, err, "not configured")
}

func TestEstimateClaimFee_FirstVsRepeatClassification(t *testing.T) {
	chain := eip1559Chain()
	reader := &fakeClaimsReader{}
	// One first claim (200k gas, wrapper deployed during the claim) and one repeat claim
	// (100k gas) of the same wrapped token.
	firstClaimFixture(chain, reader, testHashes[0], l1Origin, wrappedToken, 200000)
	eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[1], l1Origin, wrappedToken, 100000)
	wrapper := wrapperAddr

	t.Run("wrapper exists: repeat claim applies, first claim is null with its reason", func(t *testing.T) {
		wrapperLookup(chain, &wrapper)
		estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)

		resp, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
		require.NoError(t, err)
		require.Equal(t, l2Network, resp.NetworkID)
		require.Equal(t, l1Origin, resp.OriginNetwork)
		require.Equal(t, WrappedERC20, resp.TokenClass)
		require.NotNil(t, resp.WrapperExists)
		require.True(t, *resp.WrapperExists)
		require.Equal(t, RepeatClaim, resp.Applicable)

		require.Nil(t, resp.FirstClaim)
		require.NotNil(t, resp.RepeatClaim)
		require.Equal(t, uint64(100000), resp.RepeatClaim.GasUnits)
		require.Equal(t, 1, resp.RepeatClaim.SampleCount)
		require.Equal(t, sourceClass, resp.RepeatClaim.Source)
		require.Len(t, resp.Unavailable, 1)
		require.Equal(t, firstClaimWrapperDeployedReason, resp.Unavailable[FirstClaim])
		requireCostMatches(t, "repeat claim", resp.RepeatClaim, &resp.GasPrice)
		require.Greater(t, resp.ComputedAt, uint64(0))
	})

	t.Run("wrapper missing: first claim applies, repeat claim is null with its reason", func(t *testing.T) {
		wrapperLookup(chain, nil)
		estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)

		resp, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
		require.NoError(t, err)
		require.NotNil(t, resp.WrapperExists)
		require.False(t, *resp.WrapperExists)
		require.Equal(t, FirstClaim, resp.Applicable)

		require.Nil(t, resp.RepeatClaim)
		require.NotNil(t, resp.FirstClaim)
		require.Equal(t, uint64(200000), resp.FirstClaim.GasUnits)
		require.Equal(t, 1, resp.FirstClaim.SampleCount)
		require.Equal(t, sourceClass, resp.FirstClaim.Source)
		require.Len(t, resp.Unavailable, 1)
		require.Equal(t, repeatClaimWrapperMissingReason, resp.Unavailable[RepeatClaim])
		requireCostMatches(t, "first claim", resp.FirstClaim, &resp.GasPrice)
	})

	t.Run("first claim estimates are never per-token, even with many samples", func(t *testing.T) {
		// Two more first claims of the same token would still quote source class.
		firstClaimFixture(chain, reader, testHashes[2], l1Origin, wrappedToken, 300000)
		firstClaimFixture(chain, reader, testHashes[3], l1Origin, wrappedToken, 400000)
		wrapperLookup(chain, nil)
		estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)

		resp, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
		require.NoError(t, err)
		require.Equal(t, sourceClass, resp.FirstClaim.Source)
		require.Equal(t, 3, resp.FirstClaim.SampleCount)
		// Three samples are below the small-bucket threshold: the maximum is quoted.
		require.Equal(t, uint64(400000), resp.FirstClaim.GasUnits)
	})
}

func TestEstimateClaimFee_PerTokenVersusClassFallback(t *testing.T) {
	chain := eip1559Chain()
	reader := &fakeClaimsReader{}
	wrapper := wrapperAddr
	wrapperLookup(chain, &wrapper)

	// Three repeat claims of wrappedToken and one of otherToken, same class.
	for i := 0; i < 3; i++ {
		eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[i], l1Origin, wrappedToken,
			uint64(90000+1000*i))
	}
	eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[3], l1Origin, otherToken, 150000)

	t.Run("token with enough samples quotes its own bucket", func(t *testing.T) {
		estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
		resp, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
		require.NoError(t, err)
		require.Equal(t, RepeatClaim, resp.Applicable)
		require.Equal(t, sourceToken, resp.RepeatClaim.Source)
		require.Equal(t, 3, resp.RepeatClaim.SampleCount)
		// Three samples are below the small-bucket threshold: the maximum of the bucket.
		require.Equal(t, uint64(92000), resp.RepeatClaim.GasUnits)
	})

	t.Run("token without enough samples falls back to the class bucket", func(t *testing.T) {
		estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
		resp, err := estimator.EstimateClaimFee(context.Background(), l1Origin, otherToken)
		require.NoError(t, err)
		require.Equal(t, sourceClass, resp.RepeatClaim.Source)
		// The class bucket holds all four wrapped_erc20 repeat claims.
		require.Equal(t, 4, resp.RepeatClaim.SampleCount)
		require.Equal(t, uint64(150000), resp.RepeatClaim.GasUnits)
	})

	t.Run("a higher per-token threshold pushes the token back to the class fallback", func(t *testing.T) {
		estimator, _ := newEstimator(t, Config{
			NetworkID: l2Network, BridgeAddr: bridgeAddr, PerTokenMinSamples: 4,
		}, chain, reader)
		resp, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
		require.NoError(t, err)
		require.Equal(t, sourceClass, resp.RepeatClaim.Source)
		require.Equal(t, 4, resp.RepeatClaim.SampleCount)
	})
}

func TestEstimateClaimFee_SampleEligibilityFiltering(t *testing.T) {
	// One clean repeat claim per class-relevant bucket baseline, plus ineligible rows that
	// must not inflate any bucket.
	chain := eip1559Chain()
	reader := &fakeClaimsReader{}
	wrapper := wrapperAddr
	wrapperLookup(chain, &wrapper)

	txs := hashes(12)
	// The one eligible sample.
	eligibleClaimFixture(chain, reader, bridgeAddr, txs[0], l1Origin, wrappedToken, 100000)
	// A failed (reverted) claim tx.
	chain.txs[txs[1]] = claimTx(bridgeAddr)
	chain.receipts[txs[1]] = receiptOf(ethtypes.ReceiptStatusFailed, 120000,
		bridgeLog(bridgeAddr, claimEventTopic))
	reader.claims = append(reader.claims, assetClaim(txs[1], l1Origin, wrappedToken))
	// A tx sent to a router contract, not the bridge.
	chain.txs[txs[2]] = claimTx(common.HexToAddress("0x7777777777777777777777777777777777777777"))
	chain.receipts[txs[2]] = receiptOf(ethtypes.ReceiptStatusSuccessful, 130000,
		bridgeLog(bridgeAddr, claimEventTopic))
	reader.claims = append(reader.claims, assetClaim(txs[2], l1Origin, wrappedToken))
	// A multi-claim tx: two ClaimEvents in one receipt.
	chain.txs[txs[3]] = claimTx(bridgeAddr)
	chain.receipts[txs[3]] = receiptOf(ethtypes.ReceiptStatusSuccessful, 140000,
		bridgeLog(bridgeAddr, claimEventTopic),
		bridgeLog(bridgeAddr, claimEventTopic))
	reader.claims = append(reader.claims, assetClaim(txs[3], l1Origin, wrappedToken))
	// A message claim: excluded even though its tx is eligible.
	chain.txs[txs[4]] = claimTx(bridgeAddr)
	chain.receipts[txs[4]] = receiptOf(ethtypes.ReceiptStatusSuccessful, 150000,
		bridgeLog(bridgeAddr, claimEventTopic))
	messageClaim := assetClaim(txs[4], l1Origin, wrappedToken)
	messageClaim.IsMessage = true
	reader.claims = append(reader.claims, messageClaim)
	// A claim whose tx emitted the pre-Etrog ClaimEvent topic: still a valid sample.
	chain.txs[txs[5]] = claimTx(bridgeAddr)
	chain.receipts[txs[5]] = receiptOf(ethtypes.ReceiptStatusSuccessful, 160000,
		bridgeLog(bridgeAddr, claimEventPreEtrogTopic))
	reader.claims = append(reader.claims, assetClaim(txs[5], l1Origin, wrappedToken))
	// A claim whose tx emitted the sovereign DetailedClaimEvent topic: still a sample.
	chain.txs[txs[6]] = claimTx(bridgeAddr)
	chain.receipts[txs[6]] = receiptOf(ethtypes.ReceiptStatusSuccessful, 170000,
		bridgeLog(bridgeAddr, detailedClaimEventTopic))
	reader.claims = append(reader.claims, assetClaim(txs[6], l1Origin, wrappedToken))
	// A claim whose receipt the RPC no longer knows (reorged away): skipped, not an error.
	reader.claims = append(reader.claims, assetClaim(txs[7], l1Origin, wrappedToken))
	chain.notFound[txs[7]] = true
	// A sovereign-chain claim: AgglayerBridgeL2 emits ClaimEvent AND DetailedClaimEvent for
	// one claim, so the receipt holds two claim logs. Counted the way claimsync counts rows,
	// the pair is one claim: an eligible sample (the L2 destination's everyday claim).
	chain.txs[txs[8]] = claimTx(bridgeAddr)
	chain.receipts[txs[8]] = receiptOf(ethtypes.ReceiptStatusSuccessful, 180000,
		bridgeLog(bridgeAddr, claimEventTopic),
		bridgeLog(bridgeAddr, detailedClaimEventTopic))
	reader.claims = append(reader.claims, assetClaim(txs[8], l1Origin, wrappedToken))
	// A tx performing two sovereign claims, one pair each: two claims in one receipt, still
	// excluded by the exactly-one-claim rule.
	chain.txs[txs[9]] = claimTx(bridgeAddr)
	chain.receipts[txs[9]] = receiptOf(ethtypes.ReceiptStatusSuccessful, 190000,
		bridgeLog(bridgeAddr, claimEventTopic),
		bridgeLog(bridgeAddr, detailedClaimEventTopic),
		bridgeLog(bridgeAddr, claimEventTopic),
		bridgeLog(bridgeAddr, detailedClaimEventTopic))
	reader.claims = append(reader.claims, assetClaim(txs[9], l1Origin, wrappedToken))
	// A ClaimEvent from the bridge paired with a DetailedClaimEvent another contract emitted:
	// not a pair (claimsync only ever queries the bridge's logs), so the bridge's ClaimEvent
	// stands alone and the claim stays eligible.
	chain.txs[txs[10]] = claimTx(bridgeAddr)
	chain.receipts[txs[10]] = receiptOf(ethtypes.ReceiptStatusSuccessful, 200000,
		bridgeLog(bridgeAddr, claimEventTopic),
		bridgeLog(common.HexToAddress("0x8888888888888888888888888888888888888888"),
			detailedClaimEventTopic))
	reader.claims = append(reader.claims, assetClaim(txs[10], l1Origin, wrappedToken))
	// The mirrored split: the bridge's DetailedClaimEvent plus another contract's ClaimEvent.
	chain.txs[txs[11]] = claimTx(bridgeAddr)
	chain.receipts[txs[11]] = receiptOf(ethtypes.ReceiptStatusSuccessful, 210000,
		bridgeLog(common.HexToAddress("0x8888888888888888888888888888888888888888"),
			claimEventTopic),
		bridgeLog(bridgeAddr, detailedClaimEventTopic))
	reader.claims = append(reader.claims, assetClaim(txs[11], l1Origin, wrappedToken))

	estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
	resp, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
	require.NoError(t, err)
	require.NotNil(t, resp.RepeatClaim)

	// The class bucket counts exactly the six eligible samples: the clean one, the pre-Etrog
	// one, the DetailedClaimEvent one, the paired-emission one and the two split-emitter
	// ones. Everything else is filtered out.
	require.Equal(t, 6, resp.RepeatClaim.SampleCount)
	require.Equal(t, uint64(210000), resp.RepeatClaim.GasUnits, "small bucket quotes the maximum")
}

func TestEstimateClaimFee_FailClosedPaths(t *testing.T) {
	t.Run("no claims at all", func(t *testing.T) {
		chain := eip1559Chain()
		wrapper := wrapperAddr
		wrapperLookup(chain, &wrapper)
		estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr},
			chain, &fakeClaimsReader{})

		_, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
		require.ErrorContains(t, err, "no repeat-claim samples")
	})

	t.Run("applicable first-claim bucket empty even though repeats exist", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeClaimsReader{}
		eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, wrappedToken, 100000)
		wrapperLookup(chain, nil) // no wrapper: the first claim is applicable
		estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)

		_, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
		require.ErrorContains(t, err, "no first-claim samples")
	})

	t.Run("receipt RPC failure", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeClaimsReader{}
		hash := testHashes[0]
		eligibleClaimFixture(chain, reader, bridgeAddr, hash, l1Origin, wrappedToken, 100000)
		chain.receiptErr[hash] = errRPC("eth_getTransactionReceipt")
		wrapper := wrapperAddr
		wrapperLookup(chain, &wrapper)

		estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
		_, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
		require.ErrorContains(t, err, "computing the claim statistics")
	})

	t.Run("claims reader failure", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeClaimsReader{err: errRPC("db")}
		wrapper := wrapperAddr
		wrapperLookup(chain, &wrapper)
		estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)

		_, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
		require.ErrorContains(t, err, "computing the claim statistics")
	})

	t.Run("gas price RPC failure", func(t *testing.T) {
		chain := newFakeChain() // no header configured
		reader := &fakeClaimsReader{}
		eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, wrappedToken, 100000)
		wrapper := wrapperAddr
		wrapperLookup(chain, &wrapper)

		estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
		_, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
		require.ErrorContains(t, err, "computing the gas price")
	})

	t.Run("wrapper lookup eth_call failure", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeClaimsReader{}
		eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, wrappedToken, 100000)
		chain.callFn = func(ethereum.CallMsg) ([]byte, error) { return nil, errRPC("eth_call") }

		estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
		_, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
		require.ErrorContains(t, err, "getTokenWrappedAddress")
	})
}

func TestEstimateClaimFee_TokenClassesWithoutWrapper(t *testing.T) {
	// Gas token and origin-native token claims: no wrapper applies, so wrapper_exists is
	// null and the repeat claim is the applicable estimate, without any eth_call.
	chain := eip1559Chain()
	reader := &fakeClaimsReader{}
	// A gas token repeat claim (origin token 0x0 from another network) and an origin-native
	// ERC20 repeat claim (origin network is this network).
	eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, zeroToken, 80000)
	eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[1], l2Network, otherToken, 60000)
	chain.callFn = func(ethereum.CallMsg) ([]byte, error) {
		t.Fatal("no wrapper lookup happens for classes that never deploy a wrapper")
		return nil, nil
	}

	estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)

	t.Run("gas token", func(t *testing.T) {
		resp, err := estimator.EstimateClaimFee(context.Background(), l1Origin, zeroToken)
		require.NoError(t, err)
		require.Equal(t, GasToken, resp.TokenClass)
		require.Nil(t, resp.WrapperExists)
		require.Equal(t, RepeatClaim, resp.Applicable)
		require.Nil(t, resp.FirstClaim)
		require.NotNil(t, resp.RepeatClaim)
		require.Equal(t, uint64(80000), resp.RepeatClaim.GasUnits)
		require.Len(t, resp.Unavailable, 1)
		require.NotEmpty(t, resp.Unavailable[FirstClaim])
		requireCostMatches(t, "gas token", resp.RepeatClaim, &resp.GasPrice)
	})

	t.Run("origin native erc20", func(t *testing.T) {
		resp, err := estimator.EstimateClaimFee(context.Background(), l2Network, otherToken)
		require.NoError(t, err)
		require.Equal(t, OriginNativeERC20, resp.TokenClass)
		require.Nil(t, resp.WrapperExists)
		require.Equal(t, RepeatClaim, resp.Applicable)
		require.Equal(t, uint64(60000), resp.RepeatClaim.GasUnits)
		require.Len(t, resp.Unavailable, 1)
	})
}

func TestEstimateClaimFee_ChainWithoutEIP1559(t *testing.T) {
	// The whole claim estimate on a chain without a base fee: the gas price comes from
	// eth_gasPrice and base_fee and priority_fee are null.
	chain := newFakeChain()
	chain.header = legacyHeader(55)
	chain.gasPrice = big.NewInt(2000000000)
	reader := &fakeClaimsReader{}
	wrapperLookup(chain, &wrapperAddr)
	eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, wrappedToken, 100000)

	estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
	resp, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
	require.NoError(t, err)

	require.Nil(t, resp.GasPrice.BaseFee)
	require.Nil(t, resp.GasPrice.PriorityFee)
	require.Equal(t, "2000000000", string(resp.GasPrice.GasPrice))
	require.Equal(t, uint64(55), resp.GasPrice.BlockNumber)
	requireCostMatches(t, "legacy chain", resp.RepeatClaim, &resp.GasPrice)
}

func TestEstimateClaimFee_L1Network(t *testing.T) {
	// The same machinery with the estimator on L1 (network 0): a wrapped token originating
	// on L2 and a gas token claim of L2's native token.
	chain := eip1559Chain()
	reader := &fakeClaimsReader{}
	wrapper := wrapperAddr
	wrapperLookup(chain, &wrapper)
	eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[0], l2Network, wrappedToken, 110000)
	eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[1], l2Network, zeroToken, 70000)

	estimator, _ := newEstimator(t, Config{NetworkID: 0, BridgeAddr: bridgeAddr}, chain, reader)

	resp, err := estimator.EstimateClaimFee(context.Background(), l2Network, wrappedToken)
	require.NoError(t, err)
	require.Equal(t, uint32(0), resp.NetworkID)
	require.Equal(t, WrappedERC20, resp.TokenClass)
	require.Equal(t, RepeatClaim, resp.Applicable)
	require.Equal(t, uint64(110000), resp.RepeatClaim.GasUnits)

	resp, err = estimator.EstimateClaimFee(context.Background(), l2Network, zeroToken)
	require.NoError(t, err)
	require.Equal(t, GasToken, resp.TokenClass)
	require.Equal(t, uint64(70000), resp.RepeatClaim.GasUnits)
}

func TestEstimateClaimFee_PercentileOnLargeBuckets(t *testing.T) {
	chain := eip1559Chain()
	reader := &fakeClaimsReader{}
	wrapper := wrapperAddr
	wrapperLookup(chain, &wrapper)

	// Twelve repeat claims of the same token with increasing gas: above the small-bucket
	// threshold, so the p90 is quoted rather than the maximum.
	txs := hashes(12)
	for i, hash := range txs {
		eligibleClaimFixture(chain, reader, bridgeAddr, hash, l1Origin, wrappedToken,
			uint64(50000+10000*i))
	}

	estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
	resp, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
	require.NoError(t, err)
	require.Equal(t, 12, resp.RepeatClaim.SampleCount)
	// Nearest-rank p90 of 12 samples: the ceil(0.9*12) = 11th smallest = 150000, not the
	// maximum 160000.
	require.Equal(t, uint64(150000), resp.RepeatClaim.GasUnits)
	requireCostMatches(t, "p90 bucket", resp.RepeatClaim, &resp.GasPrice)
}

func TestEstimateClaimFee_StatsAndGasPriceCaching(t *testing.T) {
	chain := eip1559Chain()
	reader := &fakeClaimsReader{}
	wrapper := wrapperAddr
	wrapperLookup(chain, &wrapper)
	eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, wrappedToken, 100000)

	estimator, clock := newEstimator(
		t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr,
			GasPriceTTL: 5 * time.Second, StatsTTL: 60 * time.Second},
		chain, reader)

	ctx := context.Background()
	first, err := estimator.EstimateClaimFee(ctx, l1Origin, wrappedToken)
	require.NoError(t, err)

	// Within both TTLs: the claims are read once and the header once.
	second, err := estimator.EstimateClaimFee(ctx, l1Origin, wrappedToken)
	require.NoError(t, err)
	require.Equal(t, int64(1), reader.getCalls.Load(), "statistics are cached")
	require.Equal(t, int64(1), chain.headerCalls.Load(), "gas price is cached")
	require.Equal(t, int64(1), chain.receiptCalls.Load(), "transactions are cached")
	require.Equal(t, first.RepeatClaim, second.RepeatClaim)
	require.Equal(t, first.ComputedAt, second.ComputedAt)

	// Past the gas price TTL only the header is re-read; the statistics stay cached.
	clock.advance(5 * time.Second)
	_, err = estimator.EstimateClaimFee(ctx, l1Origin, wrappedToken)
	require.NoError(t, err)
	require.Equal(t, int64(2), chain.headerCalls.Load())
	require.Equal(t, int64(1), reader.getCalls.Load())

	// Past the statistics TTL the claims are read again; the receipts stay cached in the LRU.
	clock.advance(60 * time.Second)
	_, err = estimator.EstimateClaimFee(ctx, l1Origin, wrappedToken)
	require.NoError(t, err)
	require.Equal(t, int64(2), reader.getCalls.Load())
	require.Equal(t, int64(1), chain.receiptCalls.Load())
}

func TestEstimateClaimFee_WindowPaging(t *testing.T) {
	chain := eip1559Chain()
	reader := &fakeClaimsReader{}
	wrapper := wrapperAddr
	wrapperLookup(chain, &wrapper)

	// 250 repeat claims with a window of 250: two reader pages (200 + 50).
	txs := hashes(250)
	for _, hash := range txs {
		eligibleClaimFixture(chain, reader, bridgeAddr, hash, l1Origin, wrappedToken, 100000)
	}
	estimator, _ := newEstimator(
		t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr, Window: 250}, chain, reader)

	resp, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
	require.NoError(t, err)
	require.Equal(t, 250, resp.RepeatClaim.SampleCount)
	require.Equal(t, int64(2), reader.getCalls.Load())

	// A smaller window reads one page and quotes only its samples.
	smallReader := &fakeClaimsReader{}
	for _, hash := range txs {
		eligibleClaimFixture(chain, smallReader, bridgeAddr, hash, l1Origin, wrappedToken, 100000)
	}
	// Give the small-window estimator fresh chain responses: the tx cache is shared through
	// the same chain fake, so the receipts already cached keep it cheap.
	estimator, _ = newEstimator(
		t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr, Window: 10}, chain, smallReader)
	resp, err = estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
	require.NoError(t, err)
	require.Equal(t, 10, resp.RepeatClaim.SampleCount)
	require.Equal(t, int64(1), smallReader.getCalls.Load())
}

func TestEstimateClaimFee_MultiClaimTxRowsDeduplicate(t *testing.T) {
	// Two claim rows share one tx hash: the tx is fetched and counted once (and would be
	// excluded anyway by the exactly-one-claim-event rule, but the dedup keeps the fetch
	// bounded regardless).
	chain := eip1559Chain()
	reader := &fakeClaimsReader{}
	wrapper := wrapperAddr
	wrapperLookup(chain, &wrapper)

	hash := testHashes[0]
	chain.txs[hash] = claimTx(bridgeAddr)
	chain.receipts[hash] = receiptOf(ethtypes.ReceiptStatusSuccessful, 100000,
		bridgeLog(bridgeAddr, claimEventTopic))
	reader.claims = append(reader.claims, assetClaim(hash, l1Origin, wrappedToken))
	reader.claims = append(reader.claims, assetClaim(hash, l1Origin, otherToken))

	estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
	resp, err := estimator.EstimateClaimFee(context.Background(), l1Origin, wrappedToken)
	require.NoError(t, err)
	// Both rows are wrapped_erc20 class, but the shared tx only counts once.
	require.Equal(t, 1, resp.RepeatClaim.SampleCount)
	require.Equal(t, int64(1), chain.receiptCalls.Load())
}

func TestClassifyToken(t *testing.T) {
	tests := []struct {
		name          string
		destination   uint32
		originNetwork uint32
		originToken   common.Address
		want          TokenClass
	}{
		{
			name:          "token native to the destination network",
			destination:   l2Network,
			originNetwork: l2Network,
			originToken:   otherToken,
			want:          OriginNativeERC20,
		},
		{
			name:          "gas token of another network",
			destination:   l2Network,
			originNetwork: l1Origin,
			originToken:   zeroToken,
			want:          GasToken,
		},
		{
			name:          "token of another network",
			destination:   l2Network,
			originNetwork: l1Origin,
			originToken:   wrappedToken,
			want:          WrappedERC20,
		},
		{
			name:          "L1 destination, L2-native token",
			destination:   0,
			originNetwork: l2Network,
			originToken:   wrappedToken,
			want:          WrappedERC20,
		},
		{
			name:          "L1 destination, L1-native token claimed back",
			destination:   0,
			originNetwork: 0,
			originToken:   otherToken,
			want:          OriginNativeERC20,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, classifyToken(tt.destination, tt.originNetwork, tt.originToken))
		})
	}
}

func TestNew_RequiresChainReader(t *testing.T) {
	_, err := New(Config{NetworkID: l2Network}, nil, &fakeClaimsReader{}, nil)
	require.ErrorContains(t, err, "chain reader must not be nil")
}

// Compile-time check that the fakes satisfy the reader interfaces and the estimator
// implements the published one.
var (
	_ ChainReader  = (*fakeChain)(nil)
	_ ClaimsReader = (*fakeClaimsReader)(nil)
	_ FeeEstimator = (*Estimator)(nil)
)
