package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	autoclaimconfig "github.com/agglayer/aggkit/autoclaim/config"
	autoclaimpolicy "github.com/agglayer/aggkit/autoclaim/policy"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/contracts/mintableerc20"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	autoClaimAPIBaseURL      = "http://127.0.0.1:11579"
	bridgeServiceBaseURL     = "http://127.0.0.1:11577"
	autoClaimKeystorePass    = "pSnv6Dh5s9ahuzGzH9RoCDrKAMddaX3m"
	autoClaimBridgeAddr      = "0xC8cbEBf950B9Df44d987c8619f092beA980fF038"
	autoClaimL2RPC           = "http://op-geth-001:8545"
	autoClaimL2ChainID       = 2151908
	autoClaimRequestWait     = 8 * time.Minute
	autoClaimRestartWait     = 2 * time.Minute
	autoClaimRestoreWait     = 2 * time.Minute
	autoClaimBridgeAmountWei = 100000000000000

	// autoClaimL1BridgeAddr is the op-pp env's L1 bridge contract address (from
	// test/e2e/envs/op-pp/summary.json: networks.l1.contracts.bridge). It happens to equal
	// autoClaimBridgeAddr (the L2 bridge address) because this env deploys the bridge at the same
	// deterministic address on every network; kept as a separate constant for clarity at L2ToLx
	// claimer call sites.
	autoClaimL1BridgeAddr = autoClaimBridgeAddr
	// autoClaimL1RPC is the in-network URL of the op-pp env's L1 geth node (docker-compose.yml's
	// "geth" service, summary.json: networks.l1.services.geth.http_rpc.internal).
	autoClaimL1RPC = "http://geth:8545"
	// autoClaimL1ChainID is the op-pp env's L1 chain ID (summary.json: networks.l1.chain_id).
	autoClaimL1ChainID = 271828
	// autoClaimSourceBridgeServiceURL is the in-network URL of the op-pp env's (only) L2 bridge
	// service, used as a static AutoClaim.BridgeServiceFinder.URLs override for the L2ToLx detector.
	// The on-chain fallback (trusted sequencer URL + port 5577) would resolve to the wrong host in
	// this docker-compose env, so a static override is required.
	autoClaimSourceBridgeServiceURL = "http://aggkit-001:5577"

	// The following constants describe the 2-chain env (EnvOpPP2Chains) used by
	// TestAutoClaimL2ToL2AllowAll. In that env Auto Claim runs on the aggkit-002 (network 2 / L2B)
	// node: it detects the L2-001 -> L2-002 bridge via the L2ToLx detector, resolves the source
	// (network 1) bridge service statically, and claims on network 2 through a claimer with its own
	// l2gersync.

	// autoClaimL2BRPC is the in-network RPC URL of the 2-chain env's second L2 execution client
	// (op-reth-002). It backs the network-2 claimer's tx sender and its l2gersync.
	autoClaimL2BRPC = "http://op-reth-002:8545"
	// autoClaimL2BChainID is the 2-chain env's second L2 chain ID (summary.json: l2_networks.002.chain_id).
	autoClaimL2BChainID = 20202
	// autoClaimNet1BridgeServiceURL / autoClaimNet2BridgeServiceURL are the in-network URLs of the two
	// L2 bridge services, used as static AutoClaim.BridgeServiceFinder.URLs overrides (the on-chain
	// fallback resolves the wrong host in this docker-compose env).
	autoClaimNet1BridgeServiceURL = "http://aggkit-001:5577"
	autoClaimNet2BridgeServiceURL = "http://aggkit-002:5577"
	// autoClaimL1RollupManagerAddr is the 2-chain env's L1 rollup manager address (summary.json:
	// networks.l1.contracts.rollup_manager). Required by AutoClaim.BridgeServiceFinder when the
	// L2ToLx detector is enabled.
	autoClaimL1RollupManagerAddr = "0x6c6c009cC348976dB4A908c92B24433d4F6edA43"
	// autoClaimL2BBridgeServiceBaseURL is the external (host) URL of the network-2 bridge service,
	// which also serves the public Auto Claim request-status API (/autoclaim/v1/bridges/<key>).
	autoClaimL2BBridgeServiceBaseURL = "http://127.0.0.1:12577"
	// l2NetworkKeyB mirrors envs.l2NetworkKeyB (unexported): the summary.json key / config dir of the
	// secondary L2 network (L2B) whose aggkit node runs Auto Claim in the L2->L2 test.
	l2NetworkKeyB = "002"
)

type autoClaimRequestResponse struct {
	ID                 string                     `json:"id"`
	Status             string                     `json:"status"`
	OriginNetwork      uint32                     `json:"origin_network"`
	DestinationNetwork uint32                     `json:"destination_network"`
	DepositCount       uint32                     `json:"deposit_count"`
	BridgeTxHash       string                     `json:"bridge_tx_hash"`
	ClaimTxHash        *string                    `json:"claim_tx_hash"`
	PolicyStatus       string                     `json:"policy_status"`
	PolicyDecision     *autoClaimDecisionResponse `json:"policy_decision"`
	LastError          string                     `json:"last_error"`
}

type autoClaimDecisionResponse struct {
	PolicyName string            `json:"policy_name"`
	Result     string            `json:"result"`
	Reason     string            `json:"reason"`
	Metadata   map[string]string `json:"metadata"`
}

func TestAutoClaimL1ToL2AllowAll(t *testing.T) {
	testAutoClaimL1ToL2(t, "allow-all", false)
}

func TestAutoClaimL1ToL2APIApprove(t *testing.T) {
	testAutoClaimL1ToL2(t, "api-approve", true)
}

func TestAutoClaimL1ToL2BasicFilter(t *testing.T) {
	confirmed := testAutoClaimL1ToL2(t, string(autoclaimconfig.PolicyNameBasicFilter), false)

	require.NotNil(t, confirmed.PolicyDecision)
	require.Equal(t, string(autoclaimconfig.PolicyNameBasicFilter), confirmed.PolicyDecision.PolicyName)
	require.Equal(t, autoclaimtypes.PolicyResultApproved.String(), confirmed.PolicyDecision.Result)
	require.Equal(t, autoclaimpolicy.ReasonBasicFilterApproved, confirmed.PolicyDecision.Reason)
	require.NotEmpty(t, confirmed.PolicyDecision.Metadata["gas_used"])
	require.Equal(t, "500000", confirmed.PolicyDecision.Metadata["max_gas"])
	require.Equal(t, "skipped", confirmed.PolicyDecision.Metadata["nested_bridge_detection"])
	require.Equal(
		t,
		string(autoclaimpolicy.NestedBridgeCallNotDetected),
		confirmed.PolicyDecision.Metadata["nested_bridge_call"],
	)
}

func testAutoClaimL1ToL2(t *testing.T, policyName string, approveThroughAPI bool) autoClaimRequestResponse {
	t.Helper()
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	env := loadAutoClaimTestEnv(t, ctx)
	enableAutoClaimForTest(t, ctx, env, policyName, nil)
	waitForBridgeServiceSynced(ctx, t)

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L2Keys.Return(l2Key)

	bridgeAmount := big.NewInt(autoClaimBridgeAmountWei)
	initialBalance, err := env.Clients.L2.BalanceAt(ctx, l2Opts.From, nil)
	require.NoError(t, err)

	label := fmt.Sprintf("autoclaim-%s", policyName)
	result, err := BridgeL1NoClaim(ctx, env, l1Opts, l2Opts, bridgeAmount, label)
	require.NoError(t, err)
	require.Empty(t, result.ClaimTxHash, "test helper must not manually claim on L2")

	requestKey := autoclaimtypes.DeriveRequestKey(
		result.Bridge.OriginNetwork,
		result.Bridge.DestinationNetwork,
		result.DepositCount,
	)
	if approveThroughAPI {
		waitForAutoClaimStatus(ctx, t, requestKey, autoclaimtypes.RequestStatusManualApprovalRequired)
		approveAutoClaimRequest(ctx, t, requestKey)
	}
	confirmed := waitForAutoClaimStatus(ctx, t, requestKey, autoclaimtypes.RequestStatusConfirmed)
	require.NotNil(t, confirmed.ClaimTxHash, "confirmed Auto Claim request should expose claim tx hash")
	require.Equal(t, string(result.Bridge.TxHash), confirmed.BridgeTxHash)

	assertClaimedOnL2(ctx, t, env, result.GlobalIndex)
	finalBalance, err := env.Clients.L2.BalanceAt(ctx, result.DestinationAddr, nil)
	require.NoError(t, err)
	require.GreaterOrEqual(t, new(big.Int).Sub(finalBalance, initialBalance).Cmp(bridgeAmount), 0)
	return confirmed
}

func TestAutoClaimL2ToL1AllowAll(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Minute)
	defer cancel()

	env := loadAutoClaimTestEnv(t, ctx)

	// The L1-destination claimer's EthTxManager needs a funded L1 signer; checked out here (before
	// enabling Auto Claim) because newAutoClaimL2ToLxConfig needs the key to provision the claimer's
	// keystore before the patched config is written. l1Opts itself is not needed by this test (the
	// bridge destination is env.L2Keys' address, not env.L1Keys'), only the underlying private key.
	_, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err)
	t.Cleanup(func() { env.Keys.L1Keys.Return(l1Key) })

	l2ToLxCfg, err := newAutoClaimL2ToLxConfig(env, l1Key)
	require.NoError(t, err)

	enableAutoClaimForTest(t, ctx, env, "allow-all", l2ToLxCfg)
	waitForBridgeServiceSynced(ctx, t)

	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err)
	t.Cleanup(func() { env.Keys.L2Keys.Return(l2Key) })

	// This op-pp docker-compose env runs an OP-stack sequencer with NO op-batcher/op-proposer, so the
	// L2 chain's safe/finalized heads never advance past genesis. The aggsender only certifies L2
	// blocks up to min(lastBridgeBlock, lastClaimBlock) (aggsender/query/bridge_query.go), and its L2
	// claim syncer -- whose reorg-safe boundary is the (stuck-at-0) finalized head -- only advances
	// when it observes an L2 claim event. A pure L2->L1 bridge produces no L2 claim, so without
	// additional activity the claim syncer stays at block 0, the aggsender never builds a pessimistic
	// certificate covering the bridge, the bridge's local exit root is never settled to L1, and the
	// Auto Claim L2->Lx detector has nothing to detect. In a real network L2 finality provides this
	// for free; TestMain's post-test health check gets it from a parallel L1->L2 claim. Reproduce that
	// precondition here by driving L1->L2 Auto Claim in the background: each auto-claimed L1->L2 bridge
	// lands a claim on L2 at a block after this L2->L1 bridge, advancing the claim syncer past it so
	// the aggsender can certify and settle the L2->L1 bridge's LER. This is an environment precondition
	// only -- it neither claims the L2->L1 bridge under test nor relaxes any assertion below.
	stopPrime := make(chan struct{})
	primeDone := make(chan struct{})
	go func() {
		defer close(primeDone)
		primeL2ClaimSyncer(ctx, t, env, stopPrime)
	}()
	t.Cleanup(func() {
		close(stopPrime)
		<-primeDone
	})

	bridgeAmount := big.NewInt(autoClaimBridgeAmountWei)
	result, err := BridgeL2ToL1NoClaim(ctx, env, l2Opts, bridgeAmount, "autoclaim-l2-to-l1-allow-all")
	require.NoError(t, err)
	require.Empty(t, result.ClaimTxHash, "test helper must not manually claim on L1")

	callOpts := &bind.CallOpts{Context: ctx}
	originAddr := common.HexToAddress(string(result.Bridge.OriginAddress))
	// The L1 wrapped-token proxy for this L2-native token is deployed lazily on its first-ever claim
	// on L1. Before that, GetTokenWrappedAddress (a tokenInfoToWrappedToken mapping lookup) returns
	// the zero address, so the wrapped token's initial balance is definitionally zero. The concrete
	// wrapped-token address is only known after the claim lands, so it is re-fetched below.
	initialBalance := big.NewInt(0)

	// Request key = source:destination:deposit_count. Use the bridge's literal source network
	// (env.L2.NetworkID -- this env's only L2, network 1) rather than result.Bridge.OriginNetwork,
	// which is the bridged token's *origin* network, not its source network, and would derive the
	// wrong key here. This is the same latent bug S07 already fixed at l1_to_l2.go:289; the S11
	// outcome note for this step flags that this file's own L1->L2 test helper (testAutoClaimL1ToL2)
	// still has the equivalent bug -- harmless there only because origin==source==0 for its token --
	// and this new test must not repeat it.
	requestKey := autoclaimtypes.DeriveRequestKey(env.L2.NetworkID, 0, result.DepositCount)

	confirmed := waitForAutoClaimStatus(ctx, t, requestKey, autoclaimtypes.RequestStatusConfirmed)
	require.NotNil(t, confirmed.ClaimTxHash, "confirmed Auto Claim request should expose claim tx hash")
	require.Equal(t, string(result.Bridge.TxHash), confirmed.BridgeTxHash)

	assertClaimedOnL1(ctx, t, env, result.DepositCount, env.L2.NetworkID)

	// Re-fetch the wrapped-token address now that the claim has deployed it and populated the
	// tokenInfoToWrappedToken mapping (it was the zero address before the claim).
	wrappedTokenAddr, err := env.L1.Contracts.Bridge.GetTokenWrappedAddress(
		callOpts, result.Bridge.OriginNetwork, originAddr,
	)
	require.NoError(t, err, "get L1 wrapped token address")
	require.NotEqual(t, common.Address{}, wrappedTokenAddr, "wrapped token must be deployed on L1 after the claim")

	wrappedToken, err := mintableerc20.NewMintableerc20(wrappedTokenAddr, env.Clients.L1)
	require.NoError(t, err, "bind L1 wrapped token contract")
	finalBalance, err := wrappedToken.BalanceOf(callOpts, result.DestinationAddr)
	require.NoError(t, err, "get L1 wrapped token balance")
	require.GreaterOrEqual(t, new(big.Int).Sub(finalBalance, initialBalance).Cmp(bridgeAmount), 0)
}

// primeL2ClaimSyncer repeatedly performs L1->L2 bridges and lets the (already-enabled) L1->L2 Auto
// Claim path claim them on L2, until stop is closed or ctx is done. Each L2 claim advances the
// aggsender's L2 claim syncer, which -- in this batcher-less env where the L2 finalized head is stuck
// at genesis -- is the only way the aggsender's certifiable block range grows past a freshly-bridged
// L2->L1 deposit. See the long comment in TestAutoClaimL2ToL1AllowAll for why this precondition is
// required. It does not touch the L2->L1 bridge under test; it only produces unrelated L1->L2 claim
// activity so the pessimistic certificate covering the L2->L1 bridge can be built and settled.
func primeL2ClaimSyncer(ctx context.Context, t *testing.T, env *envs.Env, stop <-chan struct{}) {
	t.Helper()
	amount := big.NewInt(autoClaimBridgeAmountWei)
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		default:
		}

		l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
		if err != nil {
			if !sleepOrStop(ctx, stop, 5*time.Second) {
				return
			}
			continue
		}
		l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
		if err != nil {
			env.Keys.L1Keys.Return(l1Key)
			if !sleepOrStop(ctx, stop, 5*time.Second) {
				return
			}
			continue
		}

		res, bridgeErr := BridgeL1NoClaim(ctx, env, l1Opts, l2Opts, amount, "prime-l2-claim")
		if bridgeErr == nil {
			// Wait for the L1->L2 Auto Claim path to claim it on L2 (this is what advances the
			// aggsender's L2 claim syncer). A failure here is non-fatal to the priming loop: log and
			// move on so a transient hiccup does not abort the precondition activity.
			key := autoclaimtypes.DeriveRequestKey(0, env.L2.NetworkID, res.DepositCount)
			if _, waitErr := waitForAutoClaimStatusResult(ctx, key, autoclaimtypes.RequestStatusConfirmed); waitErr != nil {
				log.Infof("prime-l2-claim: L1->L2 auto claim %s not confirmed: %v", key, waitErr)
			}
		} else {
			log.Infof("prime-l2-claim: L1->L2 bridge failed: %v", bridgeErr)
		}

		env.Keys.L2Keys.Return(l2Key)
		env.Keys.L1Keys.Return(l1Key)

		if !sleepOrStop(ctx, stop, 10*time.Second) {
			return
		}
	}
}

// sleepOrStop waits for d, returning false if ctx is done or stop is closed first.
func sleepOrStop(ctx context.Context, stop <-chan struct{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-stop:
		return false
	case <-timer.C:
		return true
	}
}

// assertClaimedOnL1 asserts that the L1 bridge marks the claim for the given deposit count and
// source network as claimed. Mirrors assertClaimedOnL2 (test/e2e/removeger_test.go) but takes the
// source network directly instead of decoding it from a global index, since the L2ToLx test above
// already knows the bridge's literal source network (and must not derive it from a global index or
// from result.Bridge.OriginNetwork -- see the request-key comment in
// TestAutoClaimL2ToL1AllowAll).
func assertClaimedOnL1(ctx context.Context, t *testing.T, env *envs.Env, depositCount, sourceNetwork uint32) {
	t.Helper()
	callOpts := &bind.CallOpts{Context: ctx}
	claimed, err := env.L1.Contracts.Bridge.IsClaimed(callOpts, depositCount, sourceNetwork)
	require.NoError(t, err, "IsClaimed")
	require.True(
		t, claimed,
		"claim should be marked claimed on L1 (deposit_count=%d source_network=%d)", depositCount, sourceNetwork,
	)
}

// TestAutoClaimL2ToL2AllowAll proves the L2->L2 Auto Claim direction end to end on the 2-chain env
// (EnvOpPP2Chains, selected via AGGKIT_E2E_ENV=op-pp-2chains). Auto Claim runs on the network-2
// (L2B) node: the L2ToLx detector observes network 1's local exit root settle to L1, fetches the
// L2-001 -> L2-002 bridge from network 1's bridge service (static finder URL), and a network-2
// claimer with its own l2gersync waits for the GER covering that LER to be injected on network 2
// (GER-injection gating) before claiming. The bridge under test is performed without claiming; the
// test asserts Auto Claim reaches Confirmed, IsClaimed(depositCount, sourceNetwork=1) on network 2's
// bridge, and that the destination wrapped-token balance increased.
func TestAutoClaimL2ToL2AllowAll(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	env := loadAutoClaimL2ToL2TestEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Minute)
	defer cancel()

	enableAutoClaimL2ToL2(t, ctx, env, "allow-all")
	waitForBridgeServiceSynced(ctx, t)

	// Origin (L2A / network 1) transactor funds+bridges the L2-native token; destination
	// (L2B / network 2) transactor's From address is the claim destination.
	originOpts, originKey, err := env.L2.Keys.Checkout()
	require.NoError(t, err)
	t.Cleanup(func() { env.L2.Keys.Return(originKey) })
	destOpts, destKey, err := env.L2B.Keys.Checkout()
	require.NoError(t, err)
	t.Cleanup(func() { env.L2B.Keys.Return(destKey) })

	// The 2-chain env has no op-batcher/op-proposer for either L2, so the source L2's safe/finalized
	// heads never advance on their own and its aggsender only certifies blocks up to
	// min(lastBridgeBlock, lastClaimBlock). A pure L2->L2 bridge produces no claim on the source L2,
	// so without extra activity the source aggsender never certifies past the bridge and the origin
	// local exit root is never settled to L1 -- leaving the L2ToLx detector nothing to detect. Drive
	// background L1->L2 bridge+claim activity on network 1 so an unrelated claim lands after this
	// bridge, advancing network 1's claim syncer so its aggsender can certify and settle the L2->L2
	// bridge's LER. This is an environment precondition only; it neither claims the bridge under test
	// nor relaxes any assertion below. (Same trick as TestAutoClaimL2ToL1AllowAll, applied to the
	// source network. Network 2 needs no priming: its l2gersync reads at LatestBlock finality, so it
	// observes aggoracle's GER injection without requiring network-2 finality to advance.)
	stopPrime := make(chan struct{})
	primeDone := make(chan struct{})
	go func() {
		defer close(primeDone)
		primeL2ToL2SourceSettlement(ctx, t, env, stopPrime)
	}()
	t.Cleanup(func() {
		close(stopPrime)
		<-primeDone
	})

	bridgeAmount := big.NewInt(autoClaimBridgeAmountWei)
	result, err := BridgeL2ToL2NoClaim(ctx, env, originOpts, destOpts, bridgeAmount, "autoclaim-l2-to-l2-allow-all")
	require.NoError(t, err)
	require.Empty(t, result.ClaimTxHash, "test helper must not manually claim on L2B")

	callOpts := &bind.CallOpts{Context: ctx}
	originAddr := common.HexToAddress(string(result.Bridge.OriginAddress))
	// The L2B wrapped-token proxy for this L2A-native token is deployed lazily on its first-ever
	// claim on L2B; before that its balance is definitionally zero.
	initialBalance := big.NewInt(0)

	// Request key = source:destination:deposit_count. Source is the bridge's literal source network
	// (env.L2.NetworkID == 1), NOT result.Bridge.OriginNetwork (the token origin network); for an
	// L2A-native token these are both 1, but keying off the source network is the correct, general
	// rule -- see the same note in TestAutoClaimL2ToL1AllowAll.
	requestKey := autoclaimtypes.DeriveRequestKey(env.L2.NetworkID, env.L2B.NetworkID, result.DepositCount)

	confirmed := waitForAutoClaimStatusAt(
		ctx, t, autoClaimL2BBridgeServiceBaseURL, requestKey, autoclaimtypes.RequestStatusConfirmed,
	)
	require.NotNil(t, confirmed.ClaimTxHash, "confirmed Auto Claim request should expose claim tx hash")
	require.Equal(t, string(result.Bridge.TxHash), confirmed.BridgeTxHash)

	assertClaimedOnL2B(ctx, t, env, result.DepositCount, env.L2.NetworkID)

	// The wrapped token on L2B is deployed by the claim; compute its address and assert the
	// destination balance increased by the bridged amount.
	wrappedTokenAddr, err := env.L2B.Contracts.L2Bridge.ComputeTokenProxyAddress(
		callOpts, result.Bridge.OriginNetwork, originAddr,
	)
	require.NoError(t, err, "compute L2B wrapped token address")
	wrappedToken, err := mintableerc20.NewMintableerc20(wrappedTokenAddr, env.L2B.Client)
	require.NoError(t, err, "bind L2B wrapped token contract")
	finalBalance, err := wrappedToken.BalanceOf(callOpts, result.DestinationAddr)
	require.NoError(t, err, "get L2B wrapped token balance")
	require.GreaterOrEqual(t, new(big.Int).Sub(finalBalance, initialBalance).Cmp(bridgeAmount), 0)
}

// primeL2ToL2SourceSettlement repeatedly performs full L1->L2 bridge+claim flows on the source L2
// (network 1) until stop is closed or ctx is done. Each manual claim advances network 1's aggsender
// L2 claim syncer, which -- in this batcher-less env where the L2 finalized head is stuck at genesis
// -- is the only way the source aggsender's certifiable block range grows past a freshly-bridged
// L2->L2 deposit so its LER can settle to L1. It never touches the L2->L2 bridge under test. Unlike
// primeL2ClaimSyncer (which relies on an already-enabled L1->L2 Auto Claim path), this claims
// manually via BridgeL1ToL2 because the source node (aggkit-001) does not run Auto Claim in this test.
func primeL2ToL2SourceSettlement(ctx context.Context, t *testing.T, env *envs.Env, stop <-chan struct{}) {
	t.Helper()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		default:
		}

		l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
		if err != nil {
			if !sleepOrStop(ctx, stop, 5*time.Second) {
				return
			}
			continue
		}
		l2Opts, l2Key, err := env.L2.Keys.Checkout()
		if err != nil {
			env.Keys.L1Keys.Return(l1Key)
			if !sleepOrStop(ctx, stop, 5*time.Second) {
				return
			}
			continue
		}

		if bridgeErr := BridgeL1ToL2(ctx, env, l1Opts, l2Opts); bridgeErr != nil {
			log.Infof("prime-l2-to-l2: L1->L2 bridge+claim on network 1 failed: %v", bridgeErr)
		}

		env.L2.Keys.Return(l2Key)
		env.Keys.L1Keys.Return(l1Key)

		if !sleepOrStop(ctx, stop, 5*time.Second) {
			return
		}
	}
}

// assertClaimedOnL2B asserts that network 2's (L2B) bridge marks the claim for the given deposit
// count and source network as claimed. Mirrors assertClaimedOnL1 but targets env.L2B's bridge.
func assertClaimedOnL2B(ctx context.Context, t *testing.T, env *envs.Env, depositCount, sourceNetwork uint32) {
	t.Helper()
	require.NotNil(t, env.L2B, "assertClaimedOnL2B requires a multi-chain env (env.L2B is nil)")
	callOpts := &bind.CallOpts{Context: ctx}
	claimed, err := env.L2B.Contracts.L2Bridge.IsClaimed(callOpts, depositCount, sourceNetwork)
	require.NoError(t, err, "IsClaimed")
	require.True(
		t, claimed,
		"claim should be marked claimed on L2B (deposit_count=%d source_network=%d)", depositCount, sourceNetwork,
	)
}

// loadAutoClaimL2ToL2TestEnv returns the shared env loaded once by TestMain (selected via
// AGGKIT_E2E_ENV), skipping the test when it isn't the 2-chain env (EnvOpPP2Chains). Mirrors
// TestBridgeL2ToL2's pattern: a second, independent docker-compose stack must not be brought up
// alongside the singleton env TestMain already started, since both envs bind the same host ports.
func loadAutoClaimL2ToL2TestEnv(t *testing.T) *envs.Env {
	t.Helper()
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")
	if testEnv.L2B == nil {
		t.Skip("L2->L2 Auto Claim test requires EnvOpPP2Chains (L2B must be non-nil)")
	}
	return testEnv
}

// enableAutoClaimL2ToL2 restarts the network-2 (aggkit-002) node with a patched config that enables
// the L2ToLx bridge detector, a BridgeServiceFinder with static URLs for both L2 bridge services,
// and a single network-2 destination claimer (with its own l2gersync). The original config is
// restored on cleanup. Auto Claim is left disabled on the network-1 node; the source LER is settled
// via manual priming instead (see primeL2ToL2SourceSettlement).
func enableAutoClaimL2ToL2(t *testing.T, ctx context.Context, env *envs.Env, policyName string) {
	t.Helper()
	configPath := env.GetAggkitConfigPathForNetwork(l2NetworkKeyB)
	originalConfig, err := os.ReadFile(configPath)
	require.NoError(t, err)

	t.Cleanup(func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), autoClaimRestoreWait)
		defer cancel()
		if err := env.RestartAggkitServiceWithConfig(restoreCtx, l2NetworkKeyB, func(p string) error {
			return os.WriteFile(p, originalConfig, 0o600)
		}); err != nil {
			t.Logf("failed to restore aggkit-%s config after Auto Claim L2->L2 test: %v", l2NetworkKeyB, err)
		}
	})

	restartCtx, cancel := context.WithTimeout(ctx, autoClaimRestartWait)
	defer cancel()
	err = env.RestartAggkitServiceWithConfig(restartCtx, l2NetworkKeyB, func(p string) error {
		patched := patchAutoClaimConfig(string(originalConfig), autoClaimL2ToL2Config(policyName, env.L2B.NetworkID))
		return os.WriteFile(p, []byte(patched), 0o600)
	})
	require.NoError(t, err, "restart aggkit-%s with Auto Claim L2->L2 %s policy", l2NetworkKeyB, policyName)
}

// autoClaimL2ToL2Config renders the [AutoClaim] TOML section for the network-2 (L2B) node in the
// L2->L2 test: the L2ToLx detector enabled, a BridgeServiceFinder with static URLs for both L2
// bridge services, and one destination claimer for destNetworkID (network 2) funded from the
// node's mounted aggoracle keystore. The L1ToL2 detector is present-but-disabled (its PollInterval
// must still be > 0 to satisfy config validation).
func autoClaimL2ToL2Config(policyName string, destNetworkID uint32) string {
	suffix := strings.ReplaceAll(policyName, "-", "_")
	return fmt.Sprintf(`
[AutoClaim]
StoragePath = "/tmp/autoclaim-l2tol2-e2e-%s.sqlite"

[AutoClaim.API]
Enabled = true

[AutoClaim.L1ToL2BridgeDetector]
Enabled = false
PollInterval = "2s"
EtrogL1UpgradeBlock = 0

[AutoClaim.L2ToLxBridgeDetector]
Enabled = true
StartL1Block = 0
PollInterval = "3s"

[AutoClaim.BridgeServiceFinder]
RollupManagerAddr = %q
PollInterval = "3s"

[AutoClaim.BridgeServiceFinder.URLs]
1 = %q
2 = %q

[[AutoClaim.Claimers]]
Enabled = true
ID = "l2b-autoclaim-e2e"
NetworkType = "EVM"
NetworkID = %d
URLRPC = %q
BridgeAddr = %q
PolicyName = %q
GasOffset = 100000
WaitPeriod = "1s"
RetryAfter = "1s"
MaxRetries = 180

[AutoClaim.Claimers.Policy]
AllowMessageClaims = false
AllowedOrigins = [0, 1]
AllowedTokens = []
ManualFallback = false
MaxGas = 500000

[AutoClaim.Claimers.EthTxManager]
FrequencyToMonitorTxs = "1s"
WaitTxToBeMined = "2s"
WaitReceiptMaxTime = "250ms"
WaitReceiptCheckInterval = "1s"
PrivateKeys = [
	{Method = "local", Path = "/etc/aggkit/aggoracle.keystore", Password = %q},
]
ForcedGas = 0
GasPriceMarginFactor = 1
MaxGasPriceLimit = 0
StoragePath = "/tmp/ethtxmanager-autoclaim-l2b-e2e-%s.sqlite"
ReadPendingL1Txs = false
SafeStatusL1NumberOfBlocks = 0
FinalizedStatusL1NumberOfBlocks = 0
EstimateGasMaxRetries = 1

[AutoClaim.Claimers.EthTxManager.Etherman]
URL = %q
MultiGasProvider = false
L1ChainID = %d
HTTPHeaders = {}
`,
		suffix,
		autoClaimL1RollupManagerAddr,
		autoClaimNet1BridgeServiceURL, autoClaimNet2BridgeServiceURL,
		destNetworkID, autoClaimL2BRPC, autoClaimBridgeAddr, policyName,
		autoClaimKeystorePass, suffix,
		autoClaimL2BRPC, autoClaimL2BChainID,
	)
}

func loadAutoClaimTestEnv(t *testing.T, ctx context.Context) *envs.Env {
	t.Helper()
	loadCtx, loadCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer loadCancel()
	env, err := envs.LoadEnv(loadCtx, envs.EnvOpPP)
	require.NoError(t, err)

	checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer checkCancel()
	require.NoError(t, env.CheckEnv(checkCtx))

	testEnv = env
	return env
}

// enableAutoClaimForTest restarts aggkit with a patched Auto Claim config for the given policy.
// l2ToLx is optional: pass nil for the existing L1->L2-only tests, or the result of
// newAutoClaimL2ToLxConfig to additionally enable the L2ToLx bridge detector, the
// BridgeServiceFinder static URL override, and an L1-destination claimer (see autoClaimConfig).
func enableAutoClaimForTest(
	t *testing.T, ctx context.Context, env *envs.Env, policyName string, l2ToLx *autoClaimL2ToLxConfig,
) {
	t.Helper()
	originalConfig, err := os.ReadFile(env.GetAggkitConfigPath())
	require.NoError(t, err)

	t.Cleanup(func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), autoClaimRestoreWait)
		defer cancel()
		if err := env.RestartAggkitWithConfig(restoreCtx, func(configPath string) error {
			return os.WriteFile(configPath, originalConfig, 0o600)
		}); err != nil {
			t.Logf("failed to restore aggkit config after Auto Claim test: %v", err)
		}
	})

	restartCtx, cancel := context.WithTimeout(ctx, autoClaimRestartWait)
	defer cancel()
	err = env.RestartAggkitWithConfig(restartCtx, func(configPath string) error {
		patched := patchAutoClaimConfig(string(originalConfig), autoClaimConfig(policyName, env.L2.NetworkID, l2ToLx))
		return os.WriteFile(configPath, []byte(patched), 0o600)
	})
	require.NoError(t, err, "restart aggkit with Auto Claim %s policy", policyName)
}

func waitForAutoClaimStatus(
	ctx context.Context,
	t *testing.T,
	key autoclaimtypes.RequestKey,
	expected autoclaimtypes.RequestStatus,
) autoClaimRequestResponse {
	t.Helper()
	return waitForAutoClaimStatusAt(ctx, t, bridgeServiceBaseURL, key, expected)
}

// waitForAutoClaimStatusAt is waitForAutoClaimStatus against a specific bridge-service base URL, so
// tests can poll a non-default node's public Auto Claim API (e.g. the network-2 node at
// autoClaimL2BBridgeServiceBaseURL in the L2->L2 test).
func waitForAutoClaimStatusAt(
	ctx context.Context,
	t *testing.T,
	baseURL string,
	key autoclaimtypes.RequestKey,
	expected autoclaimtypes.RequestStatus,
) autoClaimRequestResponse {
	t.Helper()
	latest, err := waitForAutoClaimStatusResultAt(ctx, baseURL, key, expected)
	require.NoError(t, err, "wait for Auto Claim request %s status %s (%s)", key, expected, baseURL)
	return latest
}

func waitForAutoClaimStatusResult(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	expected autoclaimtypes.RequestStatus,
) (autoClaimRequestResponse, error) {
	return waitForAutoClaimStatusResultAt(ctx, bridgeServiceBaseURL, key, expected)
}

func waitForAutoClaimStatusResultAt(
	ctx context.Context,
	baseURL string,
	key autoclaimtypes.RequestKey,
	expected autoclaimtypes.RequestStatus,
) (autoClaimRequestResponse, error) {
	var latest autoClaimRequestResponse
	pollCtx, cancel := context.WithTimeout(ctx, autoClaimRequestWait)
	defer cancel()
	err := pollWithBackoff(
		pollCtx,
		autoClaimRequestWait,
		backoffInitial,
		backoffMax,
		fmt.Sprintf("autoclaim-%s-%s", key, expected),
		func() (bool, error) {
			request, found, err := getAutoClaimRequestAt(pollCtx, baseURL, key)
			if err != nil {
				return false, err
			}
			if !found {
				return false, nil
			}
			latest = request
			switch request.Status {
			case expected.String():
				return true, nil
			case autoclaimtypes.RequestStatusFailed.String(), autoclaimtypes.RequestStatusPolicyRejected.String():
				return false, fmt.Errorf(
					"autoclaim request %s reached terminal status %s: last_error=%q claim_tx_hash=%v",
					key,
					request.Status,
					request.LastError,
					request.ClaimTxHash,
				)
			default:
				log.Infof(
					"waiting for Auto Claim request %s status %s, current=%s, last_error=%q, claim_tx_hash=%v",
					key,
					expected,
					request.Status,
					request.LastError,
					request.ClaimTxHash,
				)
				return false, nil
			}
		},
	)
	if err != nil {
		return latest, fmt.Errorf("wait for Auto Claim request %s status %s: %w", key, expected, err)
	}
	return latest, nil
}

// getAutoClaimRequestAt fetches an Auto Claim request's public status from the bridge service at
// baseURL (each node's bridge service serves its own autoclaim status; the default single-chain
// tests use bridgeServiceBaseURL, the L2->L2 test polls the network-2 node's own URL).
func getAutoClaimRequestAt(
	ctx context.Context,
	baseURL string,
	key autoclaimtypes.RequestKey,
) (autoClaimRequestResponse, bool, error) {
	// Public request inspection is served by the bridge service, not the admin Auto Claim API.
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/autoclaim/v1/bridges/%s", baseURL, key),
		nil,
	)
	if err != nil {
		return autoClaimRequestResponse{}, false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return autoClaimRequestResponse{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return autoClaimRequestResponse{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return autoClaimRequestResponse{}, false, fmt.Errorf(
			"GET Auto Claim request %s returned %s: %s",
			key,
			resp.Status,
			strings.TrimSpace(string(body)),
		)
	}

	var request autoClaimRequestResponse
	if err := json.NewDecoder(resp.Body).Decode(&request); err != nil {
		return autoClaimRequestResponse{}, false, err
	}
	return request, true, nil
}

func approveAutoClaimRequest(ctx context.Context, t *testing.T, key autoclaimtypes.RequestKey) {
	t.Helper()
	body := bytes.NewBufferString(`{"reason":"e2e approved","decider":"e2e","decider_id":"autoclaim-test"}`)
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/autoclaim/v1/bridges/%s/approve", autoClaimAPIBaseURL, key),
		body,
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(
		t,
		http.StatusOK,
		resp.StatusCode,
		"approve Auto Claim request %s response body: %s",
		key,
		strings.TrimSpace(string(respBody)),
	)
}

func patchAutoClaimConfig(baseConfig, autoClaimSection string) string {
	trimmed := strings.TrimRight(baseConfig, "\n")
	if idx := strings.Index(trimmed, "\n[AutoClaim]"); idx >= 0 {
		trimmed = strings.TrimRight(trimmed[:idx], "\n")
	}
	if strings.HasPrefix(trimmed, "[AutoClaim]") {
		trimmed = ""
	}
	return strings.TrimRight(trimmed, "\n") + "\n\n" + strings.TrimSpace(autoClaimSection) + "\n"
}

// autoClaimL2ToLxConfig holds the parameters needed by autoClaimConfig to additionally enable the
// L2ToLx bridge detector, the BridgeServiceFinder static URL override, and an L1-destination
// claimer. Build one via newAutoClaimL2ToLxConfig. A nil *autoClaimL2ToLxConfig passed to
// autoClaimConfig leaves L2ToLx disabled, matching the existing L1->L2-only tests' behavior.
type autoClaimL2ToLxConfig struct {
	// SourceNetworkID is the L2 (rollup) network whose bridge service URL is statically overridden
	// in [AutoClaim.BridgeServiceFinder.URLs]; the on-chain fallback would resolve the sequencer
	// URL host, which is wrong in this docker-compose env.
	SourceNetworkID uint32
	// SourceBridgeServiceURL is the in-network URL of SourceNetworkID's bridge service.
	SourceBridgeServiceURL string
	// L1KeystorePath is the in-container path to a keystore file funded with an L1 private key,
	// used by the L1-destination claimer's EthTxManager. See newAutoClaimL2ToLxConfig /
	// writeAutoClaimL1Keystore.
	L1KeystorePath string
}

// newAutoClaimL2ToLxConfig builds an autoClaimL2ToLxConfig for env's (single) L2 network, encrypting
// l1Key into a fresh keystore file inside the aggkit-001 container's bind-mounted data directory so
// an L1-destination claimer can be funded without any docker-compose/env changes. l1Key must be a
// funded L1 private key, e.g. checked out from env.Keys.L1Keys.
func newAutoClaimL2ToLxConfig(env *envs.Env, l1Key *ecdsa.PrivateKey) (*autoClaimL2ToLxConfig, error) {
	keystorePath, err := writeAutoClaimL1Keystore(env, l1Key)
	if err != nil {
		return nil, err
	}
	return &autoClaimL2ToLxConfig{
		SourceNetworkID:        env.L2.NetworkID,
		SourceBridgeServiceURL: autoClaimSourceBridgeServiceURL,
		L1KeystorePath:         keystorePath,
	}, nil
}

// writeAutoClaimL1Keystore encrypts priv into a keystore file inside the aggkit-001 container's
// bind-mounted data directory (host: env.GetAggkitDataDir(), container: /tmp — see
// docker-compose.yml's "./aggkit-001-data:/tmp" volume) and returns the keystore file path as seen
// from inside the container. This avoids needing any new docker-compose volume mount or committed
// keystore file: none of the env's existing keystores (aggoracle, sequencer, sovereignadmin) are
// funded on L1, so a fresh one is provisioned at test time from an L1-funded key (e.g. one of
// env.Keys.L1Keys). The file is overwritten on every call, so this is safe to call once per test run.
func writeAutoClaimL1Keystore(env *envs.Env, priv *ecdsa.PrivateKey) (string, error) {
	key := &keystore.Key{
		Id:         uuid.New(),
		Address:    crypto.PubkeyToAddress(priv.PublicKey),
		PrivateKey: priv,
	}
	keyJSON, err := keystore.EncryptKey(key, autoClaimKeystorePass, keystore.LightScryptN, keystore.LightScryptP)
	if err != nil {
		return "", fmt.Errorf("encrypt L1 Auto Claim key: %w", err)
	}
	hostDir := filepath.Join(env.GetAggkitDataDir(), "autoclaim-l1-keystore")
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return "", fmt.Errorf("create L1 Auto Claim keystore dir: %w", err)
	}
	const keystoreFileName = "l1-autoclaim.keystore"
	if err := os.WriteFile(filepath.Join(hostDir, keystoreFileName), keyJSON, 0o600); err != nil {
		return "", fmt.Errorf("write L1 Auto Claim keystore: %w", err)
	}
	return "/tmp/autoclaim-l1-keystore/" + keystoreFileName, nil
}

// autoClaimConfig renders the [AutoClaim] TOML section used by e2e tests. It always emits a
// BridgeServiceFinder static URL override so the destination-bridge-service GER gate can resolve
// the L1->L2 claimer's destination (networkID) without falling back to on-chain trusted-sequencer
// discovery, which resolves to the wrong host in this docker-compose env. When l2ToLx is non-nil it
// additionally enables the L2ToLx bridge detector, repoints that same BridgeServiceFinder override
// at l2ToLx.SourceNetworkID, and adds an L1-destination (NetworkID=0) claimer funded from
// l2ToLx.L1KeystorePath.
func autoClaimConfig(policyName string, networkID uint32, l2ToLx *autoClaimL2ToLxConfig) string {
	suffix := strings.ReplaceAll(policyName, "-", "_")

	// bridgeServiceFinderNetworkID/-URL default to the L1->L2 claimer's own destination network
	// (networkID, the env's single L2 network) so the destination-bridge-service GER gate can
	// resolve it via a static override; the on-chain trusted-sequencer-URL fallback would resolve to
	// the wrong host in this docker-compose env (see autoClaimSourceBridgeServiceURL's doc comment).
	// When l2ToLx is non-nil, the L2ToLx detector's source network reuses this same override slot
	// (in this file's tests it is the same network/URL, since there is only one L2 network).
	bridgeServiceFinderNetworkID := networkID
	bridgeServiceFinderURL := autoClaimSourceBridgeServiceURL

	l2ToLxDetectorSection := `
[AutoClaim.L2ToLxBridgeDetector]
Enabled = false
`
	var l1ClaimerSection string
	if l2ToLx != nil {
		bridgeServiceFinderNetworkID = l2ToLx.SourceNetworkID
		bridgeServiceFinderURL = l2ToLx.SourceBridgeServiceURL

		l2ToLxDetectorSection = `
[AutoClaim.L2ToLxBridgeDetector]
Enabled = true
StartL1Block = 0
PollInterval = "3s"
`

		l1ClaimerSection = fmt.Sprintf(`
[[AutoClaim.Claimers]]
Enabled = true
ID = "l1-autoclaim-e2e"
NetworkType = "EVM"
NetworkID = 0
URLRPC = %q
BridgeAddr = %q
PolicyName = %q
GasOffset = 100000
WaitPeriod = "1s"
RetryAfter = "1s"
MaxRetries = 180

[AutoClaim.Claimers.Policy]
AllowMessageClaims = false
AllowedOrigins = [%d]
AllowedTokens = []
ManualFallback = false
MaxGas = 500000

[AutoClaim.Claimers.EthTxManager]
FrequencyToMonitorTxs = "1s"
WaitTxToBeMined = "2s"
WaitReceiptMaxTime = "250ms"
WaitReceiptCheckInterval = "1s"
PrivateKeys = [
	{Method = "local", Path = %q, Password = %q},
]
ForcedGas = 0
GasPriceMarginFactor = 1
MaxGasPriceLimit = 0
StoragePath = "/tmp/ethtxmanager-autoclaim-l1-e2e-%s.sqlite"
ReadPendingL1Txs = false
SafeStatusL1NumberOfBlocks = 0
FinalizedStatusL1NumberOfBlocks = 0
EstimateGasMaxRetries = 1

[AutoClaim.Claimers.EthTxManager.Etherman]
URL = %q
MultiGasProvider = false
L1ChainID = %d
HTTPHeaders = {}
`,
			autoClaimL1RPC, autoClaimL1BridgeAddr, policyName, l2ToLx.SourceNetworkID,
			l2ToLx.L1KeystorePath, autoClaimKeystorePass, suffix,
			autoClaimL1RPC, autoClaimL1ChainID,
		)
	}

	l2ToLxSection := l2ToLxDetectorSection + fmt.Sprintf(`
[AutoClaim.BridgeServiceFinder]
PollInterval = "3s"

[AutoClaim.BridgeServiceFinder.URLs]
%d = %q
`, bridgeServiceFinderNetworkID, bridgeServiceFinderURL)

	return fmt.Sprintf(`
[AutoClaim]
StoragePath = "/tmp/autoclaim-e2e-%s.sqlite"

[AutoClaim.API]
Enabled = true

[AutoClaim.L1ToL2BridgeDetector]
Enabled = true
PollInterval = "2s"
EtrogL1UpgradeBlock = 0
%s
[[AutoClaim.Claimers]]
Enabled = true
ID = "l2-autoclaim-e2e"
NetworkType = "EVM"
NetworkID = %d
URLRPC = %q
BridgeAddr = %q
PolicyName = %q
GasOffset = 100000
WaitPeriod = "1s"
RetryAfter = "1s"
MaxRetries = 180

[AutoClaim.Claimers.Policy]
AllowMessageClaims = false
AllowedOrigins = [0]
AllowedTokens = []
ManualFallback = false
MaxGas = 500000

[AutoClaim.Claimers.EthTxManager]
FrequencyToMonitorTxs = "1s"
WaitTxToBeMined = "2s"
WaitReceiptMaxTime = "250ms"
WaitReceiptCheckInterval = "1s"
PrivateKeys = [
	{Method = "local", Path = "/etc/aggkit/aggoracle.keystore", Password = %q},
]
ForcedGas = 0
GasPriceMarginFactor = 1
MaxGasPriceLimit = 0
StoragePath = "/tmp/ethtxmanager-autoclaim-e2e-%s.sqlite"
ReadPendingL1Txs = false
SafeStatusL1NumberOfBlocks = 0
FinalizedStatusL1NumberOfBlocks = 0
EstimateGasMaxRetries = 1

[AutoClaim.Claimers.EthTxManager.Etherman]
URL = %q
MultiGasProvider = false
L1ChainID = %d
HTTPHeaders = {}
%s`, suffix, l2ToLxSection, networkID, autoClaimL2RPC, autoClaimBridgeAddr, policyName, autoClaimKeystorePass, suffix,
		autoClaimL2RPC, autoClaimL2ChainID, l1ClaimerSection)
}
