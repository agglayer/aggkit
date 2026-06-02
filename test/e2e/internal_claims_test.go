package e2e

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/contracts/internalclaims"
	"github.com/agglayer/aggkit/test/contracts/mintableerc20"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// Timeout budget per internal-claims subtest. Every subtest bridges FOUR native-asset legs L1->L2
// (each an L1 Info Tree leaf injected on L2, settling within a few minutes), arms the InternalClaims
// contract once, then fires a single onMessageReceived. There is no slow L2->L1 leg, so a budget
// comparable to the reentrancy subtests (claimReentrancyTimeout = 12m) is generous; the four-leg
// bridging dominates, so each subtest gets its own 15m budget.
const internalClaimsSubtestTimeout = 15 * time.Minute

// internalClaimsReceiver is a fixed externally-owned address that holds no key in the suite. All four
// bridged legs in every subtest are sent to it as their L2 destination so the test can assert an exact
// WETH balance delta on a single account (the bridged native token is credited as WETH on L2). It
// pays no gas, so its WETH balance only moves via successful claims.
var internalClaimsReceiver = common.HexToAddress("0x1c3A1f1Ea0C0d6dB6E0a47b0C0CF0f0E0a0B0C0d")

// Junk values the legacy internal-claims.bats uses to corrupt a slot so its internal claimAsset reverts
// (and is swallowed by the contract's try/catch). The bats replaces the 2nd bytes32 entry of the
// 32-entry local-exit-root proof and overrides mainnetExitRoot; the precise junk is not load-bearing as
// long as proof verification fails. These mirror the bats verbatim for fidelity.
var (
	malformedProofEntrySlot1  = common.HexToHash("0xf077e0d22fd6721989347f053c33595697372ec8c0d0678b934bba193679e088")
	malformedMainnetRootSlot1 = common.HexToHash("0x787bc577d07da1b6ca15c9b2c6d869e08a29663f498b65752604c75efee2cfe0")
	malformedProofEntrySlot3  = common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	malformedMainnetRootSlot3 = common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
)

// TestInternalClaims ports the legacy e2e/tests/aggkit/internal-claims.bats. It deploys the custom
// InternalClaims contract once on L2 (constructor arg = L2 bridge address, matching the bats
// `cast abi-encode "constructor(address)" "$l2_bridge_addr"`), then runs the four triple-internal-claim
// scenarios as subtests against the shared op-pp env.
//
// In every scenario the contract is armed with FOUR full asset-claim parameter slots via
// updateParameters, then onMessageReceived is fired once. onMessageReceived attempts all four internal
// claimAsset calls, each wrapped in try/catch, so a malformed slot fails silently without reverting the
// transaction. A slot succeeds iff its stored params are valid (claim gets recorded + IsClaimed); a
// malformed slot (corrupted local-exit-root proof + junk mainnetExitRoot) reverts inside try/catch and
// is NOT claimed. Per-claim success/failure is asserted via L2Bridge.IsClaimed plus the exact WETH
// balance delta of the shared receiver.
//
// It reuses the P1/bridge_utils helpers, returns all pooled keys, and asserts the env is healthy at the
// end. The deployed contract is a fresh L2 contract (no shared-state leak), so no teardown beyond
// returning keys is required.
func TestInternalClaims(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	// Deploy the InternalClaims contract once for all subtests (mirrors the bats setup() that deploys
	// once). Use a short-lived context for the deploy + WETH resolution; each subtest manages its own.
	deployCtx, deployCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer deployCancel()

	deployOpts, deployKey, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key for InternalClaims deploy")
	defer env.Keys.L2Keys.Return(deployKey)

	contractAddr, deployTx, contract, err := internalclaims.DeployInternalclaims(
		deployOpts, env.Clients.L2, env.L2.Contracts.L2BridgeAddress)
	require.NoError(t, err, "deploy InternalClaims on L2")
	deployReceipt, err := bind.WaitMined(deployCtx, env.Clients.L2, deployTx)
	require.NoError(t, err, "wait for InternalClaims deploy")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, deployReceipt.Status, "InternalClaims deploy failed")
	log.Infof("[TestInternalClaims] deployed InternalClaims at %s", contractAddr.Hex())

	// Resolve the L2 WETH token (the wrapped representation of bridged native ETH). All balance
	// assertions read this ERC20, matching the bats weth_token_addr = L2Bridge.WETHToken().
	wethAddr, err := env.L2.Contracts.L2Bridge.WETHToken(&bind.CallOpts{Context: deployCtx})
	require.NoError(t, err, "read L2 WETH token address")
	require.NotEqual(t, common.Address{}, wethAddr, "WETH token address must not be zero")
	weth, err := mintableerc20.NewMintableerc20(wethAddr, env.Clients.L2)
	require.NoError(t, err, "bind L2 WETH token")

	// Scenario 1: all four slots valid -> claims 1, 2, 3 all succeed (slot 4 valid filler, not asserted).
	t.Run("ThreeSuccess", func(t *testing.T) {
		testInternalClaimsThreeSuccess(t, env, contract, weth)
	})
	// Scenario 2: slot 1 valid, slot 2 malformed, slot 3 valid -> claims 1 and 3 succeed, claim 2 fails.
	t.Run("SuccessFailSuccess", func(t *testing.T) {
		testInternalClaimsSuccessFailSuccess(t, env, contract, weth)
	})
	// Scenario 3: slot 1 malformed, slot 2 valid, slot 3 malformed -> only claim 2 succeeds.
	t.Run("FailSuccessFail", func(t *testing.T) {
		testInternalClaimsFailSuccessFail(t, env, contract, weth)
	})
	// Scenario 4: same shape as 3, but slot 1 stores slot 2's global index (still malformed) -> a
	// malformed claim sharing a global index with a successful one must not corrupt the successful one.
	t.Run("SameGlobalIndexFailSuccessFail", func(t *testing.T) {
		testInternalClaimsSameGlobalIndexFailSuccessFail(t, env, contract, weth)
	})

	// After all subtests, assert the shared env is still healthy so a leak surfaces here rather than only
	// in the TestMain post-suite check.
	healthCtx, hcancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer hcancel()
	assertNetworkHealthy(healthCtx, t, env)
}

// testInternalClaimsThreeSuccess ports bats @test 1 ("Test triple claim internal calls -> 3 success").
//
// All four slots are armed with valid params. After onMessageReceived, claims 1, 2 and 3 all succeed
// (each IsClaimed == true) and the receiver's WETH balance increases by amount_1 + amount_2 + amount_3.
// Slot 4 is a valid filler and is not asserted (the bats does not assert on it either).
func testInternalClaimsThreeSuccess(
	t *testing.T, env *envs.Env, contract *internalclaims.Internalclaims, weth *mintableerc20.Mintableerc20,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), internalClaimsSubtestTimeout)
	defer cancel()

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	callOpts := &bind.CallOpts{Context: ctx}

	amount1 := big.NewInt(1e14)
	amount2 := big.NewInt(1e14)
	amount3 := big.NewInt(1e14)
	amount4 := big.NewInt(1e14)

	// STEP 1-4: bridge four native-asset legs L1->L2 to the shared receiver and capture claim params.
	p1 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount1)
	p2 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount2)
	p3 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount3)
	p4 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount4)

	initialBal, err := weth.BalanceOf(callOpts, internalClaimsReceiver)
	require.NoError(t, err, "initial receiver WETH balance")

	// STEP 5: arm the contract with all four valid slots.
	armInternalClaims(ctx, t, env, l2Opts, contract, p1, p2, p3, p4)

	// STEP 6: fire onMessageReceived once. originAddress/originNetwork are taken from leg 1 (all legs
	// bridge the same native token from the same origin, so the contract's override is consistent).
	fireOnMessageReceived(ctx, t, env, l2Opts, contract, p1)

	// All three asserted legs succeeded.
	assertClaimed(ctx, t, env, p1)
	assertClaimed(ctx, t, env, p2)
	assertClaimed(ctx, t, env, p3)

	// Receiver WETH increased by amount_1 + amount_2 + amount_3 (slot 4 is filler, also valid; it would
	// add amount_4 too, but the bats only asserts on the first three, so assert at least those three
	// settled by checking the exact sum including slot 4 since all four are valid here).
	expectedDelta := new(big.Int).Add(amount1, amount2)
	expectedDelta.Add(expectedDelta, amount3)
	expectedDelta.Add(expectedDelta, amount4)
	assertWETHDelta(ctx, t, weth, internalClaimsReceiver, initialBal, expectedDelta)
}

// testInternalClaimsSuccessFailSuccess ports bats @test 2
// ("Test triple claim internal calls -> 1 success, 1 fail and 1 success").
//
// Slot 1 valid, slot 2 malformed, slot 3 valid (slot 4 valid filler). After onMessageReceived: claims
// 1 and 3 succeed (IsClaimed == true), claim 2 fails (NOT IsClaimed). Receiver WETH delta =
// amount_1 + amount_3 + amount_4 (NOT amount_2).
func testInternalClaimsSuccessFailSuccess(
	t *testing.T, env *envs.Env, contract *internalclaims.Internalclaims, weth *mintableerc20.Mintableerc20,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), internalClaimsSubtestTimeout)
	defer cancel()

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	callOpts := &bind.CallOpts{Context: ctx}

	amount1 := big.NewInt(1e14)
	amount2 := big.NewInt(1e14)
	amount3 := big.NewInt(1e14)
	amount4 := big.NewInt(1e14)

	p1 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount1)
	p2 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount2)
	p3 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount3)
	p4 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount4)

	// Corrupt slot 2 so its internal claimAsset reverts (swallowed by try/catch).
	p2mal := withMalformedProof(p2, malformedProofEntrySlot1, malformedMainnetRootSlot1)

	initialBal, err := weth.BalanceOf(callOpts, internalClaimsReceiver)
	require.NoError(t, err, "initial receiver WETH balance")

	armInternalClaims(ctx, t, env, l2Opts, contract, p1, p2mal, p3, p4)
	fireOnMessageReceived(ctx, t, env, l2Opts, contract, p1)

	// Claims 1 and 3 succeed; claim 2 fails.
	assertClaimed(ctx, t, env, p1)
	assertNotClaimed(ctx, t, env, p2)
	assertClaimed(ctx, t, env, p3)

	// Receiver WETH increased by amount_1 + amount_3 + amount_4 (slot 4 valid filler), NOT amount_2.
	expectedDelta := new(big.Int).Add(amount1, amount3)
	expectedDelta.Add(expectedDelta, amount4)
	assertWETHDelta(ctx, t, weth, internalClaimsReceiver, initialBal, expectedDelta)
}

// testInternalClaimsFailSuccessFail ports bats @test 3
// ("Test triple claim internal calls -> 1 fail, 1 success and 1 fail").
//
// Slot 1 malformed, slot 2 valid, slot 3 malformed (slot 4 valid filler). After onMessageReceived:
// only claim 2 succeeds (IsClaimed == true); claims 1 and 3 fail (NOT IsClaimed). Receiver WETH delta
// = amount_2 + amount_4 (slot 4 valid filler).
func testInternalClaimsFailSuccessFail(
	t *testing.T, env *envs.Env, contract *internalclaims.Internalclaims, weth *mintableerc20.Mintableerc20,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), internalClaimsSubtestTimeout)
	defer cancel()

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	callOpts := &bind.CallOpts{Context: ctx}

	amount1 := big.NewInt(1e14)
	amount2 := big.NewInt(1e14)
	amount3 := big.NewInt(1e14)
	amount4 := big.NewInt(1e14)

	p1 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount1)
	p2 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount2)
	p3 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount3)
	p4 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount4)

	// Corrupt slots 1 and 3.
	p1mal := withMalformedProof(p1, malformedProofEntrySlot1, malformedMainnetRootSlot1)
	p3mal := withMalformedProof(p3, malformedProofEntrySlot3, malformedMainnetRootSlot3)

	initialBal, err := weth.BalanceOf(callOpts, internalClaimsReceiver)
	require.NoError(t, err, "initial receiver WETH balance")

	armInternalClaims(ctx, t, env, l2Opts, contract, p1mal, p2, p3mal, p4)
	// onMessageReceived must still take origin from leg 1's (unmalformed) origin token/network.
	fireOnMessageReceived(ctx, t, env, l2Opts, contract, p1)

	// Only claim 2 succeeds; claims 1 and 3 fail.
	assertNotClaimed(ctx, t, env, p1)
	assertClaimed(ctx, t, env, p2)
	assertNotClaimed(ctx, t, env, p3)

	// Receiver WETH increased by amount_2 + amount_4 (slot 4 valid filler) only.
	expectedDelta := new(big.Int).Add(amount2, amount4)
	assertWETHDelta(ctx, t, weth, internalClaimsReceiver, initialBal, expectedDelta)
}

// testInternalClaimsSameGlobalIndexFailSuccessFail ports bats @test 4
// ("... 1 fail (same global index), 1 success (same global index) and 1 fail (different global index)").
//
// Same shape as scenario 3 (slot 1 malformed, slot 2 valid, slot 3 malformed, slot 4 valid filler)
// EXCEPT slot 1 is armed with leg 2's global index (global_index_2) while keeping leg 1's malformed
// proof/mainnet-exit-root and the rest of leg 1's fields. This exercises that a malformed claim sharing
// a global index with a successful claim does not corrupt the successful claim's state.
//
// Assertions: claim 2 succeeds (IsClaimed via leg 2's depositCount/originNetwork); leg 1's ORIGINAL
// deposit (its own depositCount/originNetwork, which carries global_index_1) is NOT claimed; leg 3 is
// NOT claimed. Receiver WETH delta = amount_2 + amount_4.
func testInternalClaimsSameGlobalIndexFailSuccessFail(
	t *testing.T, env *envs.Env, contract *internalclaims.Internalclaims, weth *mintableerc20.Mintableerc20,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), internalClaimsSubtestTimeout)
	defer cancel()

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	callOpts := &bind.CallOpts{Context: ctx}

	amount1 := big.NewInt(1e14)
	amount2 := big.NewInt(1e14)
	amount3 := big.NewInt(1e14)
	amount4 := big.NewInt(1e14)

	p1 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount1)
	p2 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount2)
	p3 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount3)
	p4 := bridgeAssetL1ToL2GetParams(ctx, t, env, l1Opts, internalClaimsReceiver, amount4)

	// Slot 1: malformed proof + junk mainnet-exit-root AND override its global index to leg 2's
	// global index (the bats passes global_index_2 for slot 1 here).
	p1mal := withMalformedProof(p1, malformedProofEntrySlot1, malformedMainnetRootSlot1)
	p1mal.globalIndex = new(big.Int).Set(p2.globalIndex)
	// Slot 3: malformed proof + junk mainnet-exit-root, keeps its own (different) global index.
	p3mal := withMalformedProof(p3, malformedProofEntrySlot3, malformedMainnetRootSlot3)

	initialBal, err := weth.BalanceOf(callOpts, internalClaimsReceiver)
	require.NoError(t, err, "initial receiver WETH balance")

	armInternalClaims(ctx, t, env, l2Opts, contract, p1mal, p2, p3mal, p4)
	fireOnMessageReceived(ctx, t, env, l2Opts, contract, p1)

	// Claim 2 succeeds; leg 1 (original deposit, carrying global_index_1) and leg 3 are NOT claimed.
	// IsClaimed is keyed by (depositCount, originNetwork) of the originally-bridged leg, so asserting on
	// p1/p3 checks the original legs' state, exactly the bats "global_index_1 / global_index_3 absent".
	assertClaimed(ctx, t, env, p2)
	assertNotClaimed(ctx, t, env, p1)
	assertNotClaimed(ctx, t, env, p3)

	// Receiver WETH increased by amount_2 + amount_4 (slot 4 valid filler) only.
	expectedDelta := new(big.Int).Add(amount2, amount4)
	assertWETHDelta(ctx, t, weth, internalClaimsReceiver, initialBal, expectedDelta)
}

// bridgeAssetL1ToL2GetParams bridges a native-token asset L1->L2 to the given destination and returns
// its claim params (proofs + bridge fields) WITHOUT claiming. It is the ASSET analogue of
// bridgeMessageL1ToL2GetParams: it calls BridgeAsset with the native token (token=zero-address, matching
// the legacy internal-claims.bats `native_token_addr`), then mirrors the exact
// waitForBridgeByTxHash -> waitForL1InfoTreeIndex -> waitForInjectedL1InfoLeaf -> GetClaimProof ->
// claimProofToContractProofs sequence used by the message variant (those helpers already exist in this
// package and are reused as-is). The bridged native value is credited as WETH on the L2 recipient on
// claim. It deliberately does not claim, leaving the caller in control of arming the contract and firing
// onMessageReceived. originAddress here is the origin TOKEN address reported by the bridge service.
func bridgeAssetL1ToL2GetParams(
	ctx context.Context, t *testing.T, env *envs.Env, l1Opts *bind.TransactOpts,
	destination common.Address, amount *big.Int,
) claimParams {
	t.Helper()
	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	require.NoError(t, err, "get L2 network ID")

	l1Opts.Value = amount
	defer func() { l1Opts.Value = nil }()
	// token = zero-address => native token bridge (the legacy bats native_token_addr); forceUpdate=true;
	// no permit data.
	tx, err := env.L1.Contracts.Bridge.BridgeAsset(
		l1Opts, l2NetworkID, destination, amount, common.Address{}, true, nil)
	require.NoError(t, err, "BridgeAsset on L1")
	receipt, err := bind.WaitMined(ctx, env.Clients.L1, tx)
	require.NoError(t, err, "wait for BridgeAsset tx")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "BridgeAsset tx failed")

	bridge := waitForBridgeByTxHash(ctx, t, env, 0, tx.Hash())
	depositCount := bridge.DepositCount
	l1InfoTreeIndex := waitForL1InfoTreeIndex(ctx, t, env, 0, depositCount)
	waitForInjectedL1InfoLeaf(ctx, t, env, l2NetworkID, l1InfoTreeIndex)

	claimProof, err := env.Clients.BridgeService.GetClaimProof(ctx, 0, l1InfoTreeIndex, depositCount)
	require.NoError(t, err, "get claim proof")
	require.NotNil(t, claimProof, "claim proof must not be nil")
	proofLocal, proofRollup := claimProofToContractProofs(claimProof)

	return claimParams{
		proofLocal:      proofLocal,
		proofRollup:     proofRollup,
		globalIndex:     bridge.GlobalIndex,
		mainnetExitRoot: common.HexToHash(string(claimProof.L1InfoTreeLeaf.MainnetExitRoot)),
		rollupExitRoot:  common.HexToHash(string(claimProof.L1InfoTreeLeaf.RollupExitRoot)),
		originNetwork:   bridge.OriginNetwork,
		originAddress:   common.HexToAddress(string(bridge.OriginAddress)),
		destNetwork:     bridge.DestinationNetwork,
		destination:     destination,
		amount:          amount,
		metadata:        common.FromHex(bridge.Metadata),
		depositCount:    depositCount,
	}
}

// withMalformedProof returns a copy of p with the 2nd entry of its local-exit-root proof replaced by a
// junk hash and its mainnetExitRoot overridden, so the bridge's claimAsset proof verification reverts
// (swallowed by the contract's try/catch). All other fields (globalIndex, origin/destination, amount,
// metadata, depositCount, the rollup proof) are kept intact so the ONLY reason for failure is the
// proof/root mismatch — mirroring the legacy bats, which sed-replaces the 2nd bytes32 proof entry and
// sets a fixed junk mainnet_exit_root.
func withMalformedProof(p claimParams, junkEntry, junkMainnetRoot common.Hash) claimParams {
	out := p
	// Deep-copy the local-exit-root proof so the original p (used for IsClaimed assertions) is untouched.
	// [32][32]byte is a value type, so this is a full copy; mutating index 1 does not touch p.proofLocal.
	proofLocal := p.proofLocal
	proofLocal[1] = junkEntry
	out.proofLocal = proofLocal
	out.mainnetExitRoot = junkMainnetRoot
	return out
}

// armInternalClaims calls updateParameters on the contract with all four slots in order (slot 1..4),
// each slot being the 11 claim fields, and waits for the tx to be mined successfully. Mirrors the bats
// STEP "updateParameters(...)" with all four sets of claim data.
func armInternalClaims(
	ctx context.Context, t *testing.T, env *envs.Env, l2Opts *bind.TransactOpts,
	contract *internalclaims.Internalclaims, p1, p2, p3, p4 claimParams,
) {
	t.Helper()
	tx, err := contract.UpdateParameters(l2Opts,
		// Slot 1.
		p1.proofLocal, p1.proofRollup, p1.globalIndex, p1.mainnetExitRoot, p1.rollupExitRoot,
		p1.originNetwork, p1.originAddress, p1.destNetwork, p1.destination, p1.amount, p1.metadata,
		// Slot 2.
		p2.proofLocal, p2.proofRollup, p2.globalIndex, p2.mainnetExitRoot, p2.rollupExitRoot,
		p2.originNetwork, p2.originAddress, p2.destNetwork, p2.destination, p2.amount, p2.metadata,
		// Slot 3.
		p3.proofLocal, p3.proofRollup, p3.globalIndex, p3.mainnetExitRoot, p3.rollupExitRoot,
		p3.originNetwork, p3.originAddress, p3.destNetwork, p3.destination, p3.amount, p3.metadata,
		// Slot 4.
		p4.proofLocal, p4.proofRollup, p4.globalIndex, p4.mainnetExitRoot, p4.rollupExitRoot,
		p4.originNetwork, p4.originAddress, p4.destNetwork, p4.destination, p4.amount, p4.metadata,
	)
	require.NoError(t, err, "updateParameters on InternalClaims")
	receipt, err := bind.WaitMined(ctx, env.Clients.L2, tx)
	require.NoError(t, err, "wait for updateParameters")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "updateParameters failed")
}

// fireOnMessageReceived calls onMessageReceived(originAddress, originNetwork, "0x") once and asserts the
// transaction itself succeeds (it always returns normally in every scenario; per-claim outcome is
// determined by each slot's stored params). originAddress/originNetwork override the per-slot stored
// origin inside the contract; all four legs share the same native-token origin, so leg 1's values are
// passed (matching the bats, which always passes origin_address_1 / origin_network_1).
func fireOnMessageReceived(
	ctx context.Context, t *testing.T, env *envs.Env, l2Opts *bind.TransactOpts,
	contract *internalclaims.Internalclaims, originLeg claimParams,
) {
	t.Helper()
	tx, err := contract.OnMessageReceived(l2Opts, originLeg.originAddress, originLeg.originNetwork, []byte{})
	require.NoError(t, err, "onMessageReceived on InternalClaims")
	receipt, err := bind.WaitMined(ctx, env.Clients.L2, tx)
	require.NoError(t, err, "wait for onMessageReceived")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status,
		"onMessageReceived must succeed (per-claim failures are swallowed by try/catch): tx=%s",
		tx.Hash().Hex())
	log.Infof("[TestInternalClaims] onMessageReceived succeeded: tx=%s", tx.Hash().Hex())
}

// assertNotClaimed is the negative counterpart of assertClaimed: it asserts
// L2Bridge.IsClaimed(depositCount, originNetwork) == false for the given params, porting the bats checks
// that a failed leg's global index is absent from the claims API (equivalently, the leg is not claimed
// on-chain). It is added here (rather than editing assertClaimed) to keep the shared helper untouched.
func assertNotClaimed(ctx context.Context, t *testing.T, env *envs.Env, p claimParams) {
	t.Helper()
	claimed, err := env.L2.Contracts.L2Bridge.IsClaimed(&bind.CallOpts{Context: ctx}, p.depositCount, p.originNetwork)
	require.NoError(t, err, "IsClaimed(depositCount=%d, originNetwork=%d)", p.depositCount, p.originNetwork)
	require.False(t, claimed,
		"deposit must NOT be claimed (its internal claimAsset was expected to fail): "+
			"depositCount=%d originNetwork=%d", p.depositCount, p.originNetwork)
}

// assertWETHDelta asserts the account's WETH balance increased by exactly expectedDelta over initialBal.
func assertWETHDelta(
	ctx context.Context, t *testing.T, weth *mintableerc20.Mintableerc20, account common.Address,
	initialBal, expectedDelta *big.Int,
) {
	t.Helper()
	finalBal, err := weth.BalanceOf(&bind.CallOpts{Context: ctx}, account)
	require.NoError(t, err, "final receiver WETH balance")
	delta := new(big.Int).Sub(finalBal, initialBal)
	require.Equal(t, 0, delta.Cmp(expectedDelta),
		"receiver WETH balance must increase by exactly the sum of successful claims: got %s want %s",
		delta.String(), expectedDelta.String())
}
