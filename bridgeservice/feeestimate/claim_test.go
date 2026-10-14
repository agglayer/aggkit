package feeestimate

import (
	"bytes"
	"context"
	"encoding/binary"
	"math/big"
	"sync/atomic"
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
// wrapped when non-nil, the zero address (no wrapper yet) when nil. The no-argument
// construction-time gas-token reads revert (as on an older bridge), which the estimator
// tolerates as no custom gas token.
func wrapperLookup(chain *fakeChain, wrapped *common.Address) {
	chain.callFn = func(msg ethereum.CallMsg) ([]byte, error) {
		if len(msg.Data) != 4+2*abiWordLen {
			return nil, errRPC("eth_call")
		}
		addr := common.Address{}
		if wrapped != nil {
			addr = *wrapped
		}
		return addressReturn(addr), nil
	}
}

// Selectors of the no-argument gas-token view methods, for the fake chain's dispatch: the
// estimator packs them from bridgeViewABIJSON; these mirror the first four keccak bytes of
// the signatures.
var (
	gasTokenAddressSelector = common.Hex2Bytes("3c351e10")
	gasTokenNetworkSelector = common.Hex2Bytes("3cbc795b")
)

// uint32Return packs v as the 32-byte ABI return of gasTokenNetwork.
func uint32Return(v uint32) []byte {
	out := make([]byte, abiWordLen)
	binary.BigEndian.PutUint32(out[abiWordLen-4:], v)
	return out
}

// gasTokenLookup equips the fake chain with the answers of the construction-time gas-token
// view calls (the given pair; the zero pair answers both as zero) and of
// getTokenWrappedAddress (wrapped when non-nil, no wrapper when nil), dispatching on the
// packed selector. Anything else reverts.
func gasTokenLookup(chain *fakeChain, pair tokenKey, wrapped *common.Address) {
	chain.callFn = func(msg ethereum.CallMsg) ([]byte, error) {
		if len(msg.Data) >= 4 {
			switch {
			case bytes.Equal(msg.Data[:4], gasTokenAddressSelector):
				return addressReturn(pair.originToken), nil
			case bytes.Equal(msg.Data[:4], gasTokenNetworkSelector):
				return uint32Return(pair.originNetwork), nil
			}
		}
		if len(msg.Data) == 4+2*abiWordLen {
			addr := common.Address{}
			if wrapped != nil {
				addr = *wrapped
			}
			return addressReturn(addr), nil
		}
		return nil, errRPC("eth_call")
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
	chain.callFn = func(msg ethereum.CallMsg) ([]byte, error) {
		if len(msg.Data) != 4 {
			t.Fatal("no wrapper lookup happens for classes that never deploy a wrapper")
		}
		// The construction-time gas-token reads revert: no custom gas token.
		return nil, errRPC("eth_call")
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

// TestEstimateClaimFee_DestinationGasTokenPairRejectedAsBadRequest pins the rejection of the
// one parameter combination that cannot describe a claimable deposit: origin_network equal
// to the destination with origin_token_address 0x0 would claim the destination's own native
// gas token. It answers ErrFeeEstimateBadRequest (400 via the handler mapping) before any
// estimator input is read.
func TestEstimateClaimFee_DestinationGasTokenPairRejectedAsBadRequest(t *testing.T) {
	chain := eip1559Chain()
	reader := &fakeClaimsReader{}
	wrapper := wrapperAddr
	wrapperLookup(chain, &wrapper)
	eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, wrappedToken, 100000)

	l2Estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
	l1Estimator, _ := newEstimator(t, Config{NetworkID: 0, BridgeAddr: bridgeAddr}, chain, reader)

	t.Run("L2 destination", func(t *testing.T) {
		_, err := l2Estimator.EstimateClaimFee(context.Background(), l2Network, zeroToken)
		require.ErrorIs(t, err, ErrFeeEstimateBadRequest)
		require.NotErrorIs(t, err, ErrFeeEstimateUnavailable)
		require.ErrorContains(t, err, "no bridge deposit can deliver")
	})

	t.Run("L1 destination", func(t *testing.T) {
		_, err := l1Estimator.EstimateClaimFee(context.Background(), l1Origin, zeroToken)
		require.ErrorIs(t, err, ErrFeeEstimateBadRequest)
		require.NotErrorIs(t, err, ErrFeeEstimateUnavailable)
	})

	// The rejection happens before any estimator input is read: no claims page, no gas
	// price header, and no wrapper eth_call. The only eth_call is the L2 construction's
	// gas-token identity read (it stops at the first failure); the L1 construction makes
	// none (network 0 is plain native ETH).
	require.Zero(t, reader.getCalls.Load())
	require.Zero(t, chain.headerCalls.Load())
	require.Equal(t, int64(1), chain.callCalls.Load(),
		"only the L2 construction's gas-token identity read")
}

// TestEstimateClaimFee_CustomGasTokenClassifiedFromTheBridge pins the on-chain gas-token
// detection: on an L2 whose bridge configures a custom gas token, claims of that token —
// identified by the bridge's (gasTokenNetwork, gasTokenAddress) pair rather than the 0x0
// marker — classify as gas_token on both the request path and the sampled-claim path, while
// wrapped tokens keep their classification. The L1 estimator never reads the pair, and a
// bridge without the two view methods (or a failing read) falls back to no custom gas
// token without failing construction.
func TestEstimateClaimFee_CustomGasTokenClassifiedFromTheBridge(t *testing.T) {
	ctx := context.Background()
	// The custom gas token of the L2 under test: a token native to L1, bridged to the L2
	// as its native currency.
	gasToken := tokenKey{originNetwork: l1Origin, originToken: otherToken}

	t.Run("requests and samples of the custom gas token classify as gas_token", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeClaimsReader{}
		gasTokenLookup(chain, gasToken, nil) // no wrapper ever: the gas token deploys none
		// Two repeat claims of the gas token and one wrapper-deploying first claim of an
		// ordinary wrapped token.
		eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[0],
			gasToken.originNetwork, gasToken.originToken, 80000)
		eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[1],
			gasToken.originNetwork, gasToken.originToken, 82000)
		firstClaimFixture(chain, reader, testHashes[2], l1Origin, wrappedToken, 200000)
		estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)

		// Request path: the gas token's non-zero origin pair resolves to gas_token, with
		// no wrapper lookup (the gas token never deploys one) and the repeat claim as the
		// applicable estimate.
		resp, err := estimator.EstimateClaimFee(ctx, gasToken.originNetwork, gasToken.originToken)
		require.NoError(t, err)
		require.Equal(t, GasToken, resp.TokenClass)
		require.Nil(t, resp.WrapperExists)
		require.Equal(t, RepeatClaim, resp.Applicable)
		// Sampled path: both gas-token claims landed in the gas_token class bucket, not
		// the wrapped one.
		require.NotNil(t, resp.RepeatClaim)
		require.Equal(t, 2, resp.RepeatClaim.SampleCount)
		requireCostMatches(t, "custom gas token", resp.RepeatClaim, &resp.GasPrice)
		require.Equal(t, int64(2), chain.callCalls.Load(),
			"only the construction's gas-token reads: no wrapper lookup for the gas_token class")

		// A wrapped token on the same chain keeps its classification and its wrapper
		// lookup.
		resp, err = estimator.EstimateClaimFee(ctx, l1Origin, wrappedToken)
		require.NoError(t, err)
		require.Equal(t, WrappedERC20, resp.TokenClass)
		require.NotNil(t, resp.WrapperExists)
		require.False(t, *resp.WrapperExists)
		require.Equal(t, FirstClaim, resp.Applicable)
	})

	t.Run("L1 estimator never reads the gas-token pair", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeClaimsReader{}
		eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, wrappedToken, 100000)
		wrapper := wrapperAddr
		wrapperLookup(chain, &wrapper)
		_, err := New(Config{NetworkID: 0, BridgeAddr: bridgeAddr}, chain, reader, nil)
		require.NoError(t, err)
		require.Zero(t, chain.callCalls.Load(), "network 0 is L1: plain native ETH, no read")
	})

	t.Run("a bridge without the view methods falls back to no custom gas token", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeClaimsReader{}
		// One repeat claim of the gas token's origin pair and one of an ordinary wrapped
		// token: with no pair, both land in the wrapped_erc20 bucket.
		eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[0],
			gasToken.originNetwork, gasToken.originToken, 80000)
		eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[1], l1Origin, wrappedToken, 100000)
		var calls atomic.Int64
		wrapper := wrapperAddr
		chain.callFn = func(ethereum.CallMsg) ([]byte, error) {
			if calls.Add(1) == 1 {
				// The construction-time gas-token read reverts, as on an older bridge; the
				// read stops at the first failure, so construction consumes one call.
				return nil, errRPC("eth_call")
			}
			return addressReturn(wrapper), nil
		}
		estimator, err := New(Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader, nil)
		require.NoError(t, err, "a failing gas-token read must never fail construction")
		require.Equal(t, int64(1), calls.Load(), "the pair read stops at the first failure")

		// Fail-soft: the gas token's origin pair is classified as wrapped_erc20 (the
		// pre-detection behaviour), and its sampled claim skews the wrapped bucket.
		resp, err := estimator.EstimateClaimFee(ctx, gasToken.originNetwork, gasToken.originToken)
		require.NoError(t, err)
		require.Equal(t, WrappedERC20, resp.TokenClass)
		require.Equal(t, 2, resp.RepeatClaim.SampleCount,
			"without a pair, the gas-token claim samples into the wrapped bucket")
	})
}

// TestEstimateClaimFee_WrapperLookupCachedPerToken pins the TTL cache behind the wrapper
// lookup: an answer — the zero address included — is eth_called once per token and reused
// until the token facts TTL expires, and a failed call is retried by the next request.
func TestEstimateClaimFee_WrapperLookupCachedPerToken(t *testing.T) {
	ctx := context.Background()

	t.Run("answers are reused within the TTL, the zero answer included", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeClaimsReader{}
		wrapperLookup(chain, nil) // no wrapper: the zero address is the answer
		// One first claim (so the applicable first-claim estimate is served) and one repeat
		// claim of the same token.
		firstClaimFixture(chain, reader, testHashes[0], l1Origin, wrappedToken, 200000)
		eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[1], l1Origin, wrappedToken, 120000)
		estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
		// The construction's gas-token identity read is counted too (it fails here, so no
		// custom gas token results); only the wrapper-lookup calls matter below.
		base := chain.callCalls.Load()

		resp, err := estimator.EstimateClaimFee(ctx, l1Origin, wrappedToken)
		require.NoError(t, err)
		require.False(t, *resp.WrapperExists)
		resp, err = estimator.EstimateClaimFee(ctx, l1Origin, wrappedToken)
		require.NoError(t, err)
		require.False(t, *resp.WrapperExists)
		require.Equal(t, base+1, chain.callCalls.Load(),
			"the wrapper lookup is cached per token, zero answer included")

		// A different token is looked up separately.
		_, err = estimator.EstimateClaimFee(ctx, l1Origin, otherToken)
		require.NoError(t, err)
		require.Equal(t, base+2, chain.callCalls.Load())
		_, err = estimator.EstimateClaimFee(ctx, l1Origin, otherToken)
		require.NoError(t, err)
		require.Equal(t, base+2, chain.callCalls.Load())
	})

	t.Run("the cached answer expires after the token facts TTL", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeClaimsReader{}
		wrapper := wrapperAddr
		wrapperLookup(chain, &wrapper)
		eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, wrappedToken, 100000)
		estimator, _ := newEstimator(t, Config{
			NetworkID: l2Network, BridgeAddr: bridgeAddr, TokenFactsTTL: 10 * time.Millisecond,
		}, chain, reader)
		base := chain.callCalls.Load()

		_, err := estimator.EstimateClaimFee(ctx, l1Origin, wrappedToken)
		require.NoError(t, err)
		require.Equal(t, base+1, chain.callCalls.Load())
		time.Sleep(50 * time.Millisecond)
		_, err = estimator.EstimateClaimFee(ctx, l1Origin, wrappedToken)
		require.NoError(t, err)
		require.Equal(t, base+2, chain.callCalls.Load(), "the cached wrapper answer expired")
	})

	t.Run("a failed lookup is not cached", func(t *testing.T) {
		chain := eip1559Chain()
		reader := &fakeClaimsReader{}
		eligibleClaimFixture(chain, reader, bridgeAddr, testHashes[0], l1Origin, wrappedToken, 100000)
		var failing atomic.Bool
		chain.callFn = func(msg ethereum.CallMsg) ([]byte, error) {
			if len(msg.Data) != 4+2*abiWordLen {
				return nil, errRPC("eth_call") // the construction-time gas-token reads revert
			}
			if failing.Load() {
				return nil, errRPC("eth_call")
			}
			return addressReturn(wrapperAddr), nil
		}
		estimator, _ := newEstimator(t, Config{NetworkID: l2Network, BridgeAddr: bridgeAddr}, chain, reader)
		base := chain.callCalls.Load()

		failing.Store(true)
		_, err := estimator.EstimateClaimFee(ctx, l1Origin, wrappedToken)
		require.ErrorIs(t, err, ErrFeeEstimateUnavailable)
		require.ErrorContains(t, err, "getTokenWrappedAddress")

		failing.Store(false)
		_, err = estimator.EstimateClaimFee(ctx, l1Origin, wrappedToken)
		require.NoError(t, err)
		require.Equal(t, base+2, chain.callCalls.Load(), "the failed lookup was retried")
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
	// The gas-token identity a custom-gas-token L2's bridge would answer: a token native
	// to L1 arriving as the L2's native currency.
	customGasToken := tokenKey{originNetwork: l1Origin, originToken: otherToken}

	tests := []struct {
		name          string
		destination   uint32
		originNetwork uint32
		originToken   common.Address
		gasTokenPair  tokenKey
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
		{
			name:          "custom gas token: the bridge-configured pair matches",
			destination:   l2Network,
			originNetwork: customGasToken.originNetwork,
			originToken:   customGasToken.originToken,
			gasTokenPair:  customGasToken,
			want:          GasToken,
		},
		{
			name:          "custom gas token configured, but the token's network differs",
			destination:   l2Network,
			originNetwork: 5,
			originToken:   customGasToken.originToken,
			gasTokenPair:  customGasToken,
			want:          WrappedERC20,
		},
		{
			name:          "custom gas token configured, but the token's address differs",
			destination:   l2Network,
			originNetwork: customGasToken.originNetwork,
			originToken:   wrappedToken,
			gasTokenPair:  customGasToken,
			want:          WrappedERC20,
		},
		{
			name:          "no custom gas token configured: the same pair stays wrapped",
			destination:   l2Network,
			originNetwork: customGasToken.originNetwork,
			originToken:   customGasToken.originToken,
			want:          WrappedERC20,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want,
				classifyToken(tt.destination, tt.originNetwork, tt.originToken, tt.gasTokenPair))
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
