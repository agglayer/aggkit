package e2e

import (
	"context"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgeservice/feeestimate"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestBridgeFeeEstimateViaProxy exercises GET /bridge/v1/bridge-fee-estimate end to end through
// the aggkit-proxy in the anvil-2chains env: it sends the three bridging mechanisms the endpoint
// quotes (native gas token via msg.value, lock of an origin-native ERC20, burn of a wrapped
// token), then requires the proxied endpoint to serve each mechanism's estimate with the
// documented shape (mechanism, sample_count >= 1, cost == gas_units x gas_price, no margin) and
// to fail closed with HTTP 503 and a readable {"error": ...} body while a mechanism has no
// samples. Unsupported networks and missing/invalid parameters must answer 4xx (404 through the
// proxy for a network the finder cannot resolve, 400 from the instance's own parameter
// validation, 400 from the proxy itself for a missing or non-numeric network_id).
//
// The test is self-sufficient (it does not depend on other tests having run): the native and
// erc20_lock samples come from its own L1->L2 bridges, and the wrapped token it burns is the
// wrapper its own L1->L2 ERC20 claim deploys on L2.
func TestBridgeFeeEstimateViaProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")
	if testEnv.Clients.BridgeServiceProxy == nil {
		t.Skip("this test exercises the fee estimate endpoints through the aggkit-proxy, " +
			"which this env does not publish")
	}

	// Native L1->L2 bridging plus the full ERC20 bridge+claim round (whose claim settles the
	// GER on L2) are the slow parts; allow a generous budget.
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	proxy := testEnv.Clients.BridgeServiceProxy
	// The instance behind the proxy for L1 and L2A (used to assert the service's own 400 for an
	// unsupported network, which the proxy turns into a 404 because it cannot route there).
	direct := client.New(client.Config{BaseURL: bridgeServiceBaseURL})
	l2NetworkID := testEnv.L2.NetworkID
	zeroAddr := common.Address{}

	// A fresh L1-native ERC20: its L1->L2 bridge is the erc20_lock sample, and claiming it on
	// L2 deploys the wrapper the test burns back to L1.
	token := deployL1MintableERC20(ctx, t, "BridgeFeeToken", "BFT")

	// 1. Native mechanism sample: bridge native ETH L1->L2 (fully indexed by the helper, no
	// claim needed -- the bridge tx itself is the sample).
	l1Opts, l2Opts := *testEnv.L1.Transactor, *testEnv.L2.Transactor
	_, err := BridgeL1NoClaim(ctx, testEnv, &l1Opts, &l2Opts, feeEstimateBridgeAmount, "bridge-fee-native")
	require.NoError(t, err, "native ETH L1->L2 bridge flow failed")

	// 2. erc20_lock mechanism sample and wrapped token: one full ERC20 round. The bridge tx
	// locks the fresh L1 ERC20 (erc20_lock on L1); the claim on L2 deploys its wrapper.
	bridgeERC20L1ToL2AndClaimOnL2(ctx, t, token, feeEstimateBridgeAmount, "bridge-fee-lock")
	wrapped, err := testEnv.L2.Contracts.L2Bridge.ComputeTokenProxyAddress(
		&bind.CallOpts{Context: ctx}, 0, token)
	require.NoError(t, err, "compute the wrapped token address on L2")
	log.Infof("[bridge-fee] wrapper for %s on L2: %s", token.Hex(), wrapped.Hex())

	// 3. Native estimate on L1, polled until the bridge is indexed and the estimator's window
	// picks it up (statistics are cached for a short TTL, so the first answers may still be
	// fail-closed 503s).
	var native *feeestimate.BridgeFeeEstimateResponse
	err = pollWithBackoff(ctx, feeEstimatePollTimeout, backoffInitial, backoffMax,
		"native bridge fee estimate on L1", func() (bool, error) {
			resp, err := proxy.GetBridgeFeeEstimate(ctx, 0, zeroAddr)
			if err != nil {
				return false, nil //nolint:nilerr // fail closed until the sample is indexed
			}
			native = resp
			return true, nil
		})
	require.NoError(t, err, "native bridge fee estimate on L1 was never served")
	require.Equal(t, feeestimate.Native, native.Mechanism, "token 0x0 must resolve to the native mechanism")
	require.Equal(t, uint32(0), native.NetworkID)
	require.Equal(t, feeEstimateAddress(zeroAddr), native.TokenAddress)
	require.Equal(t, "mechanism", native.Estimate.Source)
	requireFeeEstimateCost(t, "native on L1", native.GasPrice, &native.Estimate)

	// 4. erc20_lock estimate on L1 for the fresh ERC20.
	var lock *feeestimate.BridgeFeeEstimateResponse
	err = pollWithBackoff(ctx, feeEstimatePollTimeout, backoffInitial, backoffMax,
		"erc20_lock bridge fee estimate on L1", func() (bool, error) {
			resp, err := proxy.GetBridgeFeeEstimate(ctx, 0, token)
			if err != nil {
				return false, nil //nolint:nilerr // fail closed until the sample is indexed
			}
			lock = resp
			return true, nil
		})
	require.NoError(t, err, "erc20_lock bridge fee estimate on L1 was never served")
	require.Equal(t, feeestimate.ERC20Lock, lock.Mechanism,
		"an L1-native ERC20 that is not a wrapped token must resolve to erc20_lock")
	require.Equal(t, feeEstimateAddress(token), lock.TokenAddress)
	requireFeeEstimateCost(t, "erc20_lock on L1", lock.GasPrice, &lock.Estimate)

	// 5. Fail closed while the wrapped_burn bucket has no samples: nothing has bridged a
	// wrapped token out of L2 yet, so the estimate for the fresh wrapper must answer 503 with
	// the reason in the body -- never a default value.
	_, err = proxy.GetBridgeFeeEstimate(ctx, l2NetworkID, wrapped)
	requireFeeEstimateStatus(t, err, http.StatusServiceUnavailable,
		"wrapped_burn estimate on L2 before any burn")

	// 6. wrapped_burn mechanism sample: bridge the wrapper back to L1 (the burn), without
	// claiming on L1 -- the burn tx itself is the sample.
	burnAmount := new(big.Int).Div(feeEstimateBridgeAmount, big.NewInt(2))
	burnWrappedTokenL2ToL1NoClaim(ctx, t, wrapped, burnAmount, "bridge-fee-burn")

	// 7. wrapped_burn estimate on L2, polled until the burn is indexed.
	var burn *feeestimate.BridgeFeeEstimateResponse
	err = pollWithBackoff(ctx, feeEstimatePollTimeout, backoffInitial, backoffMax,
		"wrapped_burn bridge fee estimate on L2", func() (bool, error) {
			resp, err := proxy.GetBridgeFeeEstimate(ctx, l2NetworkID, wrapped)
			if err != nil {
				return false, nil //nolint:nilerr // fail closed until the sample is indexed
			}
			burn = resp
			return true, nil
		})
	require.NoError(t, err, "wrapped_burn bridge fee estimate on L2 was never served")
	require.Equal(t, feeestimate.WrappedBurn, burn.Mechanism,
		"a wrapped token bridged back to its origin must resolve to wrapped_burn")
	require.Equal(t, l2NetworkID, burn.NetworkID)
	require.Equal(t, feeEstimateAddress(wrapped), burn.TokenAddress)
	requireFeeEstimateCost(t, "wrapped_burn on L2", burn.GasPrice, &burn.Estimate)

	// 8. Unsupported network and invalid input must fail closed with 4xx statuses:
	// - through the proxy, a network the finder cannot resolve answers 404;
	// - the instance's own parameter validation answers 400 for an unsupported network
	//   (observable by addressing the instance directly, since the proxy would route any
	//   network it resolves to a service that accepts it);
	// - the proxy itself answers 400 for a missing or non-numeric network_id, which the typed
	//   client cannot send (it always serializes a valid uint32), so those go as raw requests.
	_, err = proxy.GetBridgeFeeEstimate(ctx, feeEstimateUnroutableNetworkID, zeroAddr)
	requireFeeEstimateStatus(t, err, http.StatusNotFound,
		"unroutable network id through the proxy")
	_, err = direct.GetBridgeFeeEstimate(ctx, feeEstimateUnroutableNetworkID, zeroAddr)
	requireFeeEstimateStatus(t, err, http.StatusBadRequest,
		"unsupported network id answered by the instance itself")
	requireProxyRaw400(t, "/bridge/v1/bridge-fee-estimate?token_address="+zeroAddr.Hex())
	requireProxyRaw400(t, "/bridge/v1/bridge-fee-estimate?network_id=abc&token_address="+zeroAddr.Hex())
}
