package e2e

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgeservice/feeestimate"
	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/contracts/mintableerc20"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// TestClaimFeeEstimateViaProxy exercises GET /bridge/v1/claim-fee-estimate end to end through
// the aggkit-proxy in the anvil-2chains env: it bridges and claims real tokens, then requires
// the proxied endpoint to serve the documented estimate shape (first claim versus repeat claim,
// cost == gas_units x gas_price, no server-side margin) and to fail closed with HTTP 503 and a
// readable {"error": ...} body whenever the applicable estimate cannot be computed. Unsupported
// networks and missing/invalid parameters must answer 4xx (404 through the proxy for a network
// the finder cannot resolve, 400 from the instance's own parameter validation, 400 for the
// non-claimable origin_network == network_id with origin_token_address 0x0 pair, 400 from the
// proxy itself for a missing or non-numeric network_id).
//
// Success-shape scenarios run against both destinations: claims on this env's L2 are
// sovereign-chain claims whose receipts carry both ClaimEvent and DetailedClaimEvent, and the
// estimator counts that pair as ONE claim (dedup mirrors claimsync), so its "exactly one claim
// event" eligibility rule finds samples there and L2-destination estimates are served --
// required as the served contract below, after real L2 claims have been indexed. On L1 the
// bridge emits a single ClaimEvent per claim, so L1-destination estimates are served and the
// first-vs-repeat comparison runs there as well. The test is self-sufficient: it deploys its
// own tokens and performs its own bridges and claims, in any test order.
func TestClaimFeeEstimateViaProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")
	if testEnv.Clients.BridgeServiceProxy == nil {
		t.Skip("this test exercises the fee estimate endpoints through the aggkit-proxy, " +
			"which this env does not publish")
	}

	// Two ERC20 L1->L2 bridge+claim rounds and two L2->L1 bridge+claim rounds (each settling a
	// GER) are the slow parts; allow a generous budget.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	proxy := testEnv.Clients.BridgeServiceProxy
	// The instance behind the proxy for L1 and L2A (used to assert the service's own 400 for an
	// unsupported network, which the proxy turns into a 404 because it cannot route there).
	direct := client.New(client.Config{BaseURL: bridgeServiceBaseURL})
	l2NetworkID := testEnv.L2.NetworkID
	l2NativeToken := testEnv.L2.Contracts.MintableERC20Address
	someToken := common.HexToAddress(feeEstimateSomeTokenAddr)
	otherL2Token := common.HexToAddress(feeEstimateOtherL2TokenAddr)

	// 1. Fail closed on buckets that structurally cannot have samples: a claim executed on a
	// network always bridges a token from ANOTHER network, so the origin_native_erc20 class has
	// no possible sample on either destination, whatever the env's history is.
	_, err := proxy.GetClaimFeeEstimate(ctx, l2NetworkID, l2NetworkID, l2NativeToken)
	requireFeeEstimateStatus(t, err, http.StatusServiceUnavailable,
		"origin_native_erc20 estimate on the L2 destination (structurally unsampleable)")
	_, err = proxy.GetClaimFeeEstimate(ctx, 0, 0, someToken)
	requireFeeEstimateStatus(t, err, http.StatusServiceUnavailable,
		"origin_native_erc20 estimate on the L1 destination (structurally unsampleable)")

	// 2. A fresh L1 ERC20, queried before anything claims it: no wrapper exists on L2 yet, so
	// the request resolves to first_claim. Whether it is served depends on whether the
	// wrapped_erc20 first-claim bucket on L2 holds samples yet: L2-destination estimates are
	// served on this env (the paired ClaimEvent+DetailedClaimEvent of a sovereign L2 claim
	// counts as one claim -- see the file-top comment), so any wrapper-deploying L2 claim
	// indexed before this point fills the bucket -- both outcomes are asserted for their
	// documented shape.
	freshToken := deployL1MintableERC20(ctx, t, "ClaimFeeToken", "CFT")
	resp, err := proxy.GetClaimFeeEstimate(ctx, l2NetworkID, 0, freshToken)
	if err != nil {
		requireFeeEstimateStatus(t, err, http.StatusServiceUnavailable,
			"pre-claim estimate for a fresh L1 token on the L2 destination")
	} else {
		require.False(t, *resp.WrapperExists, "nothing has claimed the fresh token yet")
		require.Equal(t, feeestimate.FirstClaim, resp.Applicable)
		require.Equal(t, feeestimate.WrappedERC20, resp.TokenClass)
		requireFeeEstimateCost(t, "first claim on the L2 destination", resp.GasPrice, resp.FirstClaim)
		require.Nil(t, resp.RepeatClaim)
		require.NotEmpty(t, resp.Unavailable[feeestimate.RepeatClaim])
	}

	// 3. Bridge the fresh token L1->L2 twice and claim both deposits: the first claim deploys
	// the token's wrapper on L2 (a first claim), the second is a repeat claim.
	bridgeERC20L1ToL2AndClaimOnL2(ctx, t, freshToken, feeEstimateBridgeAmount, "claim-fee-A-1")
	bridgeERC20L1ToL2AndClaimOnL2(ctx, t, freshToken, feeEstimateBridgeAmount, "claim-fee-A-2")

	// 4. The L2-destination repeat-claim estimate must be served: the paired
	// ClaimEvent+DetailedClaimEvent each sovereign L2 claim emits is counted as ONE claim (the
	// dedup mirrors claimsync -- see the file-top comment), so the estimator's eligibility rule
	// finds the two claims above. Poll for a bounded while (a serving estimator needs claimsync
	// to index the claims plus one statistics TTL to pick them up) and require the served
	// estimate within that window: a fail-closed 503 here is a regression of the event pairing
	// and must fail the test.
	var l2RepeatResp *feeestimate.ClaimFeeEstimateResponse
	err = pollWithBackoff(ctx, feeEstimateL2ClaimPollTimeout, backoffInitial, backoffMax,
		"repeat_claim estimate on the L2 destination", func() (bool, error) {
			resp, err := proxy.GetClaimFeeEstimate(ctx, l2NetworkID, 0, freshToken)
			if err != nil || resp.RepeatClaim == nil {
				return false, nil //nolint:nilerr // fail closed until the claim is indexed
			}
			l2RepeatResp = resp
			return true, nil
		})
	require.NoError(t, err, "repeat_claim estimate on the L2 destination was never served")
	require.True(t, *l2RepeatResp.WrapperExists, "the first claim deployed the wrapper on L2")
	require.Equal(t, feeestimate.RepeatClaim, l2RepeatResp.Applicable)
	require.Nil(t, l2RepeatResp.FirstClaim)
	require.NotEmpty(t, l2RepeatResp.Unavailable[feeestimate.FirstClaim])
	requireFeeEstimateCost(t, "repeat claim on the L2 destination", l2RepeatResp.GasPrice, l2RepeatResp.RepeatClaim)

	// 5. L1 destination: bridge the env's L2-native ERC20 to L1 and claim it there. This first
	// L1 claim deploys the token's wrapper on L1, so the request for the token itself resolves
	// to repeat_claim while its only L1 claim is a first-claim sample -- the repeat bucket is
	// empty and the endpoint must fail closed. Both rounds draw on one mint: each
	// BridgeL2ToL1 round bridges exactly feeEstimateBridgeAmount, and the helper neither mints
	// nor approves (it expects the caller to fund the token, like TestMain's post-test check).
	l1Opts, l2Opts := *testEnv.L1.Transactor, *testEnv.L2.Transactor
	mintAndApproveL2NativeToken(ctx, t, l2NativeToken, new(big.Int).Mul(feeEstimateBridgeAmount, big.NewInt(2)))
	require.NoError(t, BridgeL2ToL1(ctx, testEnv, &l1Opts, &l2Opts, l2NativeToken),
		"first L2->L1 bridge and claim flow")
	_, err = proxy.GetClaimFeeEstimate(ctx, 0, l2NetworkID, l2NativeToken)
	requireFeeEstimateStatus(t, err, http.StatusServiceUnavailable,
		"repeat-claim estimate on the L1 destination right after the wrapper-deploying claim")

	// 6. The first-claim estimate on the L1 destination, observed through a second L2-native
	// token that stays unclaimed: it resolves to first_claim, served from the wrapped_erc20
	// class bucket that now holds the wrapper-deploying claim above.
	var firstResp *feeestimate.ClaimFeeEstimateResponse
	err = pollWithBackoff(ctx, feeEstimatePollTimeout, backoffInitial, backoffMax,
		"first_claim estimate on the L1 destination", func() (bool, error) {
			resp, err := proxy.GetClaimFeeEstimate(ctx, 0, l2NetworkID, otherL2Token)
			if err != nil || resp.FirstClaim == nil || resp.FirstClaim.SampleCount < 1 {
				return false, nil //nolint:nilerr // fail closed until the claim is indexed
			}
			firstResp = resp
			return true, nil
		})
	require.NoError(t, err, "first_claim estimate on the L1 destination was never served")
	require.False(t, *firstResp.WrapperExists, "the second L2 token was never claimed on L1")
	require.Equal(t, feeestimate.FirstClaim, firstResp.Applicable)
	require.Equal(t, feeestimate.WrappedERC20, firstResp.TokenClass)
	require.Equal(t, uint32(0), firstResp.NetworkID)
	require.Equal(t, feeEstimateAddress(otherL2Token), firstResp.OriginTokenAddress)
	require.Equal(t, "class", firstResp.FirstClaim.Source)
	requireFeeEstimateCost(t, "first claim on the L1 destination", firstResp.GasPrice, firstResp.FirstClaim)
	require.Nil(t, firstResp.RepeatClaim)
	require.NotEmpty(t, firstResp.Unavailable[feeestimate.RepeatClaim])

	// 7. Bridge and claim the L2-native token a second time: this claim is a repeat claim, so
	// the token's own request resolves to repeat_claim with a served estimate.
	l1Opts, l2Opts = *testEnv.L1.Transactor, *testEnv.L2.Transactor
	require.NoError(t, BridgeL2ToL1(ctx, testEnv, &l1Opts, &l2Opts, l2NativeToken),
		"second L2->L1 bridge and claim flow")
	var repeatResp *feeestimate.ClaimFeeEstimateResponse
	err = pollWithBackoff(ctx, feeEstimatePollTimeout, backoffInitial, backoffMax,
		"repeat_claim estimate on the L1 destination", func() (bool, error) {
			resp, err := proxy.GetClaimFeeEstimate(ctx, 0, l2NetworkID, l2NativeToken)
			if err != nil || resp.RepeatClaim == nil || resp.RepeatClaim.SampleCount < 1 {
				return false, nil //nolint:nilerr // fail closed until the claim is indexed
			}
			repeatResp = resp
			return true, nil
		})
	require.NoError(t, err, "repeat_claim estimate on the L1 destination was never served")
	require.True(t, *repeatResp.WrapperExists, "the first claim deployed the wrapper on L1")
	require.Equal(t, feeestimate.RepeatClaim, repeatResp.Applicable)
	requireFeeEstimateCost(t, "repeat claim on the L1 destination", repeatResp.GasPrice, repeatResp.RepeatClaim)

	// 8. The wrapper deployment (first claim) must cost more gas than a repeat claim: the
	// first claim deploys the token's wrapper contract inside the claim transaction.
	require.Greater(t, firstResp.FirstClaim.GasUnits, repeatResp.RepeatClaim.GasUnits,
		"the wrapper-deployment claim must cost more gas than a repeat claim")

	// 9. Unsupported network and invalid input must fail closed with 4xx statuses:
	// - through the proxy, a network the finder cannot resolve answers 404;
	// - the instance's own parameter validation answers 400 for an unsupported network
	//   (observable by addressing the instance directly, since the proxy would route any
	//   network it resolves to a service that accepts it);
	// - the claim endpoint answers 400 for the non-claimable pair origin_network ==
	//   network_id with origin_token_address 0x0: that claims the destination's own native
	//   gas token, which no bridge deposit can deliver;
	// - the proxy itself answers 400 for a missing or non-numeric network_id, which the typed
	//   client cannot send (it always serializes a valid uint32), so those go as raw requests.
	_, err = proxy.GetClaimFeeEstimate(ctx, feeEstimateUnroutableNetworkID, 0, someToken)
	requireFeeEstimateStatus(t, err, http.StatusNotFound,
		"unroutable network id through the proxy")
	_, err = direct.GetClaimFeeEstimate(ctx, feeEstimateUnroutableNetworkID, 0, someToken)
	requireFeeEstimateStatus(t, err, http.StatusBadRequest,
		"unsupported network id answered by the instance itself")
	_, err = proxy.GetClaimFeeEstimate(ctx, l2NetworkID, l2NetworkID, common.Address{})
	requireFeeEstimateStatus(t, err, http.StatusBadRequest,
		"claim of the destination network's own gas token through the proxy")
	_, err = direct.GetClaimFeeEstimate(ctx, 0, 0, common.Address{})
	requireFeeEstimateStatus(t, err, http.StatusBadRequest,
		"claim of the destination network's own gas token on L1, instance direct")
	requireProxyRaw400(t, "/bridge/v1/claim-fee-estimate"+
		"?origin_network=0&origin_token_address="+feeEstimateSomeTokenAddr)
	requireProxyRaw400(t, "/bridge/v1/claim-fee-estimate"+
		"?network_id=abc&origin_network=0&origin_token_address="+feeEstimateSomeTokenAddr)
}

// ---------------------------------------------------------------------------
// Shared helpers of the fee estimate tests (claim and bridge files live in the same package).
// ---------------------------------------------------------------------------

const (
	// feeEstimateUnroutableNetworkID is a network id no bridge service is registered for in
	// this env: the proxy cannot route it (404) and the instance rejects it (400).
	feeEstimateUnroutableNetworkID = uint32(9999)

	// feeEstimateSomeTokenAddr is an arbitrary address no token lives behind, used where the
	// estimate only depends on the address's class (its zero-ness or wrapper mapping), not on
	// real token state.
	feeEstimateSomeTokenAddr = "0x0000000000000000000000000000000000000001"

	// feeEstimateOtherL2TokenAddr is a second arbitrary address: a second L2-native token that
	// stays unclaimed on L1, so its L1-destination request resolves to first_claim and is
	// served from the token-class bucket.
	feeEstimateOtherL2TokenAddr = "0x0000000000000000000000000000000000000042"

	// feeEstimatePollTimeout bounds how long a fee estimate test waits for an estimate to
	// become served after its sample transaction is indexed: the syncers follow the chains
	// within seconds, but the estimator caches its statistics for a short TTL.
	feeEstimatePollTimeout = 6 * time.Minute

	// feeEstimateL2ClaimPollTimeout bounds how long the L2-destination repeat-claim estimate must
	// take to be served (see step 4 and the file-top comment: the paired
	// ClaimEvent+DetailedClaimEvent of a sovereign L2 claim counts as one claim, so the
	// estimator serves L2-destination estimates on this env; failing closed within this window
	// is a regression).
	feeEstimateL2ClaimPollTimeout = 2 * time.Minute
)

// feeEstimateBridgeAmount is the token amount every in-test fee estimate bridge moves.
var feeEstimateBridgeAmount = big.NewInt(1e14)

// feeEstimateErrorBody is the {"error": "..."} shape every fee estimate error answer carries,
// on the bridge service and on the aggkit proxy alike.
type feeEstimateErrorBody struct {
	Error string `json:"error"`
}

// feeEstimateAddress converts an address to the API's address type for response comparisons.
func feeEstimateAddress(a common.Address) types.Address {
	return types.Address(a.Hex())
}

// requireFeeEstimateErrorBody decodes a fee estimate error answer's body and requires it to be
// the documented {"error": "..."} JSON with a non-empty reason. The reason is asserted
// structurally, never by matching its text; callers may log it for diagnostics.
func requireFeeEstimateErrorBody(t *testing.T, body string) string {
	t.Helper()
	var parsed feeEstimateErrorBody
	require.NoError(t, json.Unmarshal([]byte(body), &parsed),
		"fee estimate error body must be the documented {\"error\": ...} JSON, got %q", body)
	require.NotEmpty(t, parsed.Error,
		"fee estimate error body must carry a readable reason, got %q", body)
	return parsed.Error
}

// requireFeeEstimateStatus requires err to be the client's typed HTTP status error carrying the
// wanted status and a readable error body: the fail-closed contract of the fee estimate
// endpoints (and of the proxy's own rejections), asserted structurally via errors.As -- never
// by matching the error message text.
func requireFeeEstimateStatus(t *testing.T, err error, wantStatus int, what string) {
	t.Helper()
	require.Error(t, err, "%s: the endpoint must fail closed, never serve a default value", what)
	var statusErr *client.HTTPStatusError
	require.ErrorAs(t, err, &statusErr,
		"%s: the client must surface the HTTP status as *client.HTTPStatusError", what)
	require.Equal(t, wantStatus, statusErr.StatusCode,
		"%s: unexpected HTTP status (body: %s)", what, statusErr.Body)
	log.Infof("[fee-estimate] %s: fail-closed body: %s", what, requireFeeEstimateErrorBody(t, statusErr.Body))
}

// requireFeeEstimateCost asserts the structural invariants the fee estimate API documents for a
// served estimate: at least one observed sample, positive gas units and cost, a gas price
// snapshot naming its block, and cost exactly equal to gas_units x gas_price (the endpoint
// adds no server-side margin).
func requireFeeEstimateCost(
	t *testing.T, what string, gasPrice feeestimate.GasPriceInfo, est *feeestimate.FeeEstimate,
) {
	t.Helper()
	require.NotNil(t, est, "%s: a served estimate must be present", what)
	require.GreaterOrEqual(t, est.SampleCount, 1, "%s: a served estimate needs at least one sample", what)
	require.Greater(t, est.GasUnits, uint64(0), "%s: gas_units must be positive", what)
	require.Greater(t, gasPrice.BlockNumber, uint64(0),
		"%s: the gas price snapshot must name its block", what)
	price, ok := new(big.Int).SetString(string(gasPrice.GasPrice), 10)
	require.True(t, ok, "%s: gas_price %q must be a decimal string", what, gasPrice.GasPrice)
	require.Greater(t, price.Sign(), 0, "%s: gas_price must be positive", what)
	cost, ok := new(big.Int).SetString(string(est.Cost), 10)
	require.True(t, ok, "%s: cost %q must be a decimal string", what, est.Cost)
	require.Greater(t, cost.Sign(), 0, "%s: cost must be positive", what)
	expected := new(big.Int).Mul(price, new(big.Int).SetUint64(est.GasUnits))
	require.Equal(t, expected, cost, "%s: cost must equal gas_units x gas_price (no server-side margin)", what)
}

// requireProxyRaw400 GETs the given path against the env's aggkit-proxy and requires the
// proxy's own 400 answer. The proxy rejects a missing or non-numeric network_id before any
// forwarding; the typed client cannot send either (it always serializes a valid uint32), so
// these invalid-input cases need a raw request.
func requireProxyRaw400(t *testing.T, pathAndQuery string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		testEnv.Clients.BridgeServiceProxyURL+pathAndQuery, nil)
	require.NoError(t, err, "build the raw request for %s", pathAndQuery)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "raw GET %s", pathAndQuery)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read the body of %s", pathAndQuery)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "the proxy must answer 400 for %s", pathAndQuery)
	requireFeeEstimateErrorBody(t, string(body))
}

// feeEstimateSummary is a minimal summary.json reader for the L1 bridge address, which the env
// loader keeps only inside the L1 bridge binding (test/e2e/forcegerupdate_test.go reads it the
// same way).
type feeEstimateSummary struct {
	Networks struct {
		L1 struct {
			Contracts struct {
				Bridge string `json:"bridge"`
			} `json:"contracts"`
		} `json:"l1"`
	} `json:"networks"`
}

// feeEstimateL1BridgeAddr returns the L1 bridge contract address from the env's summary.json.
func feeEstimateL1BridgeAddr(t *testing.T) common.Address {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testEnv.EnvDir, "summary.json"))
	require.NoError(t, err, "read summary.json")
	var summary feeEstimateSummary
	require.NoError(t, json.Unmarshal(data, &summary), "parse summary.json")
	addr := common.HexToAddress(summary.Networks.L1.Contracts.Bridge)
	require.NotEqual(t, common.Address{}, addr, "the L1 bridge address must be present in summary.json")
	return addr
}

// mintAndApproveL2NativeToken mints amount of an L2-native ERC20 to the L2 transactor and
// approves the L2 bridge to spend it, so a subsequent BridgeL2ToL1 round (which neither mints
// nor approves) can move the tokens out of the L2. Mirrors the funding TestMain's post-test
// check does before its own L2->L1 flow.
func mintAndApproveL2NativeToken(ctx context.Context, t *testing.T, token common.Address, amount *big.Int) {
	t.Helper()
	l2Opts := *testEnv.L2.Transactor
	tok, err := mintableerc20.NewMintableerc20(token, testEnv.Clients.L2)
	require.NoError(t, err, "bind the L2-native token %s", token.Hex())

	mintTx, err := tok.Mint(&l2Opts, l2Opts.From, amount)
	require.NoError(t, err, "mint %s of %s on L2", amount.String(), token.Hex())
	mintReceipt, err := bind.WaitMined(ctx, testEnv.Clients.L2, mintTx)
	require.NoError(t, err, "wait for the mint of %s", token.Hex())
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, mintReceipt.Status, "mint tx failed")

	approveTx, err := tok.Approve(&l2Opts, testEnv.L2.Contracts.L2BridgeAddress, amount)
	require.NoError(t, err, "approve the L2 bridge for %s", token.Hex())
	approveReceipt, err := bind.WaitMined(ctx, testEnv.Clients.L2, approveTx)
	require.NoError(t, err, "wait for the approval of %s", token.Hex())
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, approveReceipt.Status, "approve tx failed")
}

// deployL1MintableERC20 deploys a fresh MintableERC20 on L1 with the env's L1 transactor and
// returns its address. Each fee estimate test uses its own tokens so the first-claim (wrapper
// deployment) state stays under the test's control.
func deployL1MintableERC20(ctx context.Context, t *testing.T, name, symbol string) common.Address {
	t.Helper()
	opts := *testEnv.L1.Transactor
	addr, tx, _, err := mintableerc20.DeployMintableerc20(&opts, testEnv.Clients.L1, name, symbol)
	require.NoError(t, err, "deploy MintableERC20 on L1")
	_, err = bind.WaitMined(ctx, testEnv.Clients.L1, tx)
	require.NoError(t, err, "wait for the MintableERC20 deployment on L1")
	log.Infof("[fee-estimate] MintableERC20 %s (%s) deployed on L1 at %s", name, symbol, addr.Hex())
	return addr
}

// bridgeERC20L1ToL2AndClaimOnL2 performs one full L1->L2 ERC20 bridge and claim round through
// the proxy-fronted bridge service: it mints amount of token to the L1 transactor, approves the
// L1 bridge, bridges the token to the env's L2, waits for the deposit to be indexed and its GER
// injected, then claims on L2 for the L2 transactor. The first claim of a token deploys its
// wrapper on L2 (a first-claim sample); every later claim is a repeat claim.
//
// Shared by TestClaimFeeEstimateViaProxy (two rounds for the same token) and
// TestBridgeFeeEstimateViaProxy (one round, to deploy the wrapped token it burns back to L1).
func bridgeERC20L1ToL2AndClaimOnL2(
	ctx context.Context, t *testing.T, token common.Address, amount *big.Int, label string,
) {
	t.Helper()
	bs := testEnv.ClaimBridgeService(testEnv.Clients.BridgeService)
	l2NetworkID := testEnv.L2.NetworkID
	destination := testEnv.L2.Transactor.From

	tok, err := mintableerc20.NewMintableerc20(token, testEnv.Clients.L1)
	require.NoError(t, err, "[%s] bind the L1 ERC20", label)
	l1Opts, l2Opts := *testEnv.L1.Transactor, *testEnv.L2.Transactor

	mintTx, err := tok.Mint(&l1Opts, l1Opts.From, amount)
	require.NoError(t, err, "[%s] mint", label)
	_, err = bind.WaitMined(ctx, testEnv.Clients.L1, mintTx)
	require.NoError(t, err, "[%s] wait for the mint", label)

	approveTx, err := tok.Approve(&l1Opts, feeEstimateL1BridgeAddr(t), amount)
	require.NoError(t, err, "[%s] approve the L1 bridge", label)
	_, err = bind.WaitMined(ctx, testEnv.Clients.L1, approveTx)
	require.NoError(t, err, "[%s] wait for the approval", label)

	bridgeTx, err := testEnv.L1.Contracts.Bridge.BridgeAsset(
		&l1Opts, l2NetworkID, destination, amount, token, true, nil)
	require.NoError(t, err, "[%s] send the L1->L2 bridge tx", label)
	receipt, err := waitMinedL1WithDiagnostics(ctx, testEnv.Clients.L1, bridgeTx, l1MineDiagnosticsWait)
	require.NoError(t, err, "[%s] wait for the L1->L2 bridge tx", label)
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "[%s] bridge tx failed", label)

	// The ERC20 transfer also emits a Transfer log, so scan all logs for the BridgeEvent.
	var depositCount uint32
	for _, lg := range receipt.Logs {
		bridgeEvent, parseErr := testEnv.L1.Contracts.Bridge.ParseBridgeEvent(*lg)
		if parseErr == nil {
			depositCount = bridgeEvent.DepositCount
			break
		}
	}
	require.NotZero(t, depositCount, "[%s] BridgeEvent not found in the bridge tx receipt", label)

	var bridge *types.BridgeResponse
	err = pollWithBackoff(ctx, 2*time.Minute, backoffInitial, backoffMax, label+" deposit indexed",
		func() (bool, error) {
			row, err := bs.GetBridgeByDepositCount(ctx, 0, depositCount)
			if err != nil || row == nil {
				return false, nil //nolint:nilerr // not indexed yet, keep polling
			}
			bridge = row
			return true, nil
		})
	require.NoError(t, err, "[%s] the deposit was never indexed by the bridge service", label)
	require.Equal(t, bridgeTx.Hash().Hex(), string(bridge.TxHash),
		"[%s] the indexed deposit must be the test's own", label)
	log.Debugf("[%s] deposit indexed: deposit_count=%d", label, bridge.DepositCount)

	var l1InfoTreeIndex uint32
	err = pollWithBackoff(ctx, 5*time.Minute, backoffInitial, backoffMax, label+" L1 info tree index",
		func() (bool, error) {
			idx, err := bs.GetL1InfoTreeIndex(ctx, 0, int(depositCount))
			if err != nil || idx == 0 {
				return false, nil //nolint:nilerr // not included yet, keep polling
			}
			l1InfoTreeIndex = idx
			return true, nil
		})
	require.NoError(t, err, "[%s] the deposit was never included in the L1 info tree", label)

	var claimL1InfoTreeIndex uint32
	err = pollWithBackoff(ctx, 8*time.Minute, backoffInitial, backoffMax, label+" injected L1 info leaf",
		func() (bool, error) {
			leaf, err := bs.GetInjectedL1InfoLeaf(ctx, int(l2NetworkID), int(l1InfoTreeIndex))
			if err != nil || leaf == nil {
				return false, nil //nolint:nilerr // not injected yet, keep polling
			}
			// The aggoracle may inject a leaf beyond the deposit's own index (a parallel bridge
			// can update the L1 info tree first); the claim must use the ACTUAL injected index
			// so its proofs match the GER stored in GlobalExitRootMap.
			claimL1InfoTreeIndex = leaf.L1InfoTreeIndex
			return true, nil
		})
	require.NoError(t, err, "[%s] the L1 info leaf was never injected on L2", label)

	claimProof, err := bs.GetClaimProof(ctx, 0, claimL1InfoTreeIndex, depositCount)
	require.NoError(t, err, "[%s] fetch the claim proof", label)
	require.NotNil(t, claimProof, "[%s] the claim proof must be present", label)

	// Verify the GER is confirmed on L2 before claiming (the claim consumes it).
	callOpts := &bind.CallOpts{Context: ctx}
	gerHash := crypto.Keccak256Hash(
		common.HexToHash(string(claimProof.L1InfoTreeLeaf.MainnetExitRoot)).Bytes(),
		common.HexToHash(string(claimProof.L1InfoTreeLeaf.RollupExitRoot)).Bytes())
	err = pollWithBackoff(ctx, 2*time.Minute, backoffInitial, backoffMax, label+" GER confirmed on L2",
		func() (bool, error) {
			ts, err := testEnv.L2.Contracts.GlobalExitRoot.GlobalExitRootMap(callOpts, gerHash)
			return err == nil && ts.Sign() > 0, nil //nolint:nilerr // not confirmed yet, keep polling
		})
	require.NoError(t, err, "[%s] the GER was never confirmed on L2", label)

	var smtProofLocalExitRoot [32][32]byte
	for i, proofHex := range claimProof.ProofLocalExitRoot {
		if i >= 32 {
			break
		}
		smtProofLocalExitRoot[i] = common.HexToHash(string(proofHex))
	}
	var smtProofRollupExitRoot [32][32]byte
	for i, proofHex := range claimProof.ProofRollupExitRoot {
		if i >= 32 {
			break
		}
		smtProofRollupExitRoot[i] = common.HexToHash(string(proofHex))
	}
	originTokenAddress := common.HexToAddress(string(bridge.OriginAddress))
	claimTx, err := testEnv.L2.Contracts.L2Bridge.ClaimAsset(
		&l2Opts, smtProofLocalExitRoot, smtProofRollupExitRoot,
		bridge.GlobalIndex.ToBigInt(),
		common.HexToHash(string(claimProof.L1InfoTreeLeaf.MainnetExitRoot)),
		common.HexToHash(string(claimProof.L1InfoTreeLeaf.RollupExitRoot)),
		bridge.OriginNetwork, originTokenAddress, bridge.DestinationNetwork,
		destination, amount, common.FromHex(bridge.Metadata),
	)
	require.NoError(t, err, "[%s] send the L2 claim tx", label)
	claimReceipt, err := bind.WaitMined(ctx, testEnv.Clients.L2, claimTx)
	require.NoError(t, err, "[%s] wait for the L2 claim tx", label)
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, claimReceipt.Status, "[%s] L2 claim tx failed", label)
	log.Infof("[%s] L2 claim mined at block %d", label, claimReceipt.BlockNumber.Uint64())
}

// burnWrappedTokenL2ToL1NoClaim bridges a wrapped L2 token back to L1 (the burn that samples
// the wrapped_burn mechanism) without claiming on L1: the burn tx itself is the sample.
// wrapped must map back to an origin token in the L2 bridge (wrappedTokenToTokenInfo non-zero).
func burnWrappedTokenL2ToL1NoClaim(
	ctx context.Context, t *testing.T, wrapped common.Address, amount *big.Int, label string,
) {
	t.Helper()
	wrappedToken, err := mintableerc20.NewMintableerc20(wrapped, testEnv.Clients.L2)
	require.NoError(t, err, "[%s] bind the wrapped token", label)
	l2Opts := *testEnv.L2.Transactor

	balance, err := wrappedToken.BalanceOf(&bind.CallOpts{Context: ctx}, l2Opts.From)
	require.NoError(t, err, "[%s] read the wrapped token balance", label)
	require.True(t, balance.Cmp(amount) >= 0,
		"[%s] balance %s is too small to bridge %s", label, balance.String(), amount.String())

	approveTx, err := wrappedToken.Approve(&l2Opts, testEnv.L2.Contracts.L2BridgeAddress, amount)
	require.NoError(t, err, "[%s] approve the L2 bridge", label)
	_, err = bind.WaitMined(ctx, testEnv.Clients.L2, approveTx)
	require.NoError(t, err, "[%s] wait for the approval", label)

	bridgeTx, err := testEnv.L2.Contracts.L2Bridge.BridgeAsset(
		&l2Opts, 0, l2Opts.From, amount, wrapped, true, nil)
	require.NoError(t, err, "[%s] send the L2->L1 burn tx", label)
	receipt, err := bind.WaitMined(ctx, testEnv.Clients.L2, bridgeTx)
	require.NoError(t, err, "[%s] wait for the L2->L1 burn tx", label)
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "[%s] burn tx failed", label)
	log.Infof("[%s] wrapped token burned on L2 at block %d", label, receipt.BlockNumber.Uint64())
}
