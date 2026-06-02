package e2e

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/contracts/bridgemessagereceivermock"
	"github.com/agglayer/aggkit/test/contracts/mintableerc20"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// Timeout budget for the reentrancy subtests. Every leg here is an L1->L2 message bridge+claim (an L1
// Info Tree leaf injected on L2), which settles within a few minutes; there is no slow L2->L1 leg.
// The first subtest bridges 2 legs and the second bridges 3 legs plus an internal L2->L2 bridgeAsset,
// so each subtest gets a generous L1->L2-style budget.
const claimReentrancyTimeout = 12 * time.Minute

// Fixed receiver EOAs from the legacy claim-reetrancy.bats. These are arbitrary externally-owned
// addresses that hold no key in the suite; they exist only so a claim credits a non-gas-paying
// account, letting the test assert an exact +amount balance delta.
var (
	reentrancyReceiver1 = common.HexToAddress("0x15E13226E42ebB16fAD9E9A42B149954c5bD00e0")
	reentrancyReceiver2 = common.HexToAddress("0xBA002167c3a9Ee959EF4c2A62f7Fb026326479DD")
	// internalBridgeAssetReceiver / Network mirror the bats testClaim internal bridgeAsset target
	// (destinationNetwork=2, a fixed EOA). The 0.0004 ETH internal bridgeAsset is sent with the call.
	internalBridgeAssetReceiver = common.HexToAddress("0xa9bAE041CE268C90c54F588db794ab9f18686BBD")
	internalBridgeAssetNetwork  = uint32(2)
	internalBridgeAssetAmount   = big.NewInt(4e14) // 0.0004 ETH
)

// claimParams is the Go equivalent of the bats extract_claim_parameters_json blob: everything needed
// to (re)issue a ClaimMessage on L2 or to ABI-encode the claimData tuple consumed by the mock's
// testClaim. It is produced from the bridge-service record + GetClaimProof for a single L1->L2 leg.
type claimParams struct {
	proofLocal      [32][32]byte
	proofRollup     [32][32]byte
	globalIndex     *big.Int
	mainnetExitRoot common.Hash
	rollupExitRoot  common.Hash
	originNetwork   uint32
	originAddress   common.Address
	destNetwork     uint32
	destination     common.Address
	amount          *big.Int
	metadata        []byte
	depositCount    uint32
}

// TestClaimReentrancy ports the legacy e2e/tests/aggkit/claim-reetrancy.bats. It deploys the custom
// BridgeMessageReceiverMock once on L2 (constructor arg = L2 bridge address), then runs two subtests
// against the shared op-pp env:
//
//   - PreventDoubleClaim: a reentrant/duplicate ClaimMessage of an already-claimed asset must be
//     rejected by the bridge's already-claimed guard, with correct balance deltas.
//   - TestClaimInternalReentrancyAndBridgeAsset: the mock's testClaim, which performs two valid
//     claimMessage calls, an internal invalid claimMessage (destinationNetwork=1000) it expects to
//     revert, and one bridgeAsset, must succeed with correct balance deltas.
//
// It reuses the P1 helpers and bridge_utils.go machinery, returns all pooled keys, and asserts the env
// is healthy at the end. The deployed mock leaves no shared-state leak (it is a fresh contract on L2),
// so no teardown beyond returning keys is required.
func TestClaimReentrancy(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	ctx, cancel := context.WithTimeout(context.Background(), claimReentrancyTimeout)
	defer cancel()

	// Deploy the reentrancy mock once for all subtests (mirrors the bats setup() that deploys once).
	deployOpts, deployKey, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key for mock deploy")
	defer env.Keys.L2Keys.Return(deployKey)

	mockAddr, deployTx, mock, err := bridgemessagereceivermock.DeployBridgemessagereceivermock(
		deployOpts, env.Clients.L2, env.L2.Contracts.L2BridgeAddress)
	require.NoError(t, err, "deploy BridgeMessageReceiverMock on L2")
	deployReceipt, err := bind.WaitMined(ctx, env.Clients.L2, deployTx)
	require.NoError(t, err, "wait for mock deploy")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, deployReceipt.Status, "mock deploy failed")
	log.Infof("[TestClaimReentrancy] deployed BridgeMessageReceiverMock at %s", mockAddr.Hex())

	// Resolve the L2 WETH token (the wrapped representation of bridged native ETH). All balance
	// assertions below read this ERC20, matching the bats weth_token_addr = L2Bridge.WETHToken().
	wethAddr, err := env.L2.Contracts.L2Bridge.WETHToken(&bind.CallOpts{Context: ctx})
	require.NoError(t, err, "read L2 WETH token address")
	require.NotEqual(t, common.Address{}, wethAddr, "WETH token address must not be zero")
	weth, err := mintableerc20.NewMintableerc20(wethAddr, env.Clients.L2)
	require.NoError(t, err, "bind L2 WETH token")

	t.Run("PreventDoubleClaim", func(t *testing.T) {
		testClaimReentrancyPreventDoubleClaim(ctx, t, env, mockAddr, mock, weth)
	})
	t.Run("TestClaimInternalReentrancyAndBridgeAsset", func(t *testing.T) {
		testClaimReentrancyInternalAndBridgeAsset(ctx, t, env, mockAddr, mock, weth)
	})

	// After both subtests, assert the shared env is still healthy so a leak surfaces here rather than
	// only in the TestMain post-suite check.
	healthCtx, hcancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer hcancel()
	assertNetworkHealthy(healthCtx, t, env)
}

// testClaimReentrancyPreventDoubleClaim ports bats @test 1
// ("Test reentrancy protection for bridge claims - should prevent double claiming").
//
// Mapping to bats steps:
//   - STEP 1-2  -> bridge asset #1 L1->L2 to a fixed EOA (reentrancyReceiver1) and capture params #1.
//   - STEP 3-4  -> bridge asset #2 L1->L2 to the mock contract and capture params #2.
//   - STEP 5    -> updateParameters(asset #1) arms the contract to reentrantly claim asset #1.
//   - STEP 6    -> record initial WETH balances of receiver and contract.
//   - STEP 7    -> claim asset #2 (destination=contract); succeeds and fires onMessageReceived, which
//     reentrantly claims the (still-unclaimed) asset #1, crediting the EOA receiver.
//   - STEP 8    -> LOAD-BEARING: a direct duplicate ClaimMessage of asset #1 (now settled via the
//     reentrant path) must be rejected by the already-claimed guard (bats check_claim_revert_code
//     AlreadyClaimed).
//   - STEP 9    -> both deposits processed (asserted implicitly by the successful claims).
//   - STEP 10   -> balance deltas: contract += amount_2, receiver += amount_1.
//   - STEP 11   -> IsClaimed(depositCount, originNetwork) == true for both deposits.
func testClaimReentrancyPreventDoubleClaim(
	ctx context.Context, t *testing.T, env *envs.Env, mockAddr common.Address,
	mock *bridgemessagereceivermock.Bridgemessagereceivermock, weth *mintableerc20.Mintableerc20,
) {
	t.Helper()
	amount1 := big.NewInt(1e14) // 0.0001 ETH
	amount2 := big.NewInt(1e14) // 0.0001 ETH

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	callOpts := &bind.CallOpts{Context: ctx}

	// STEP 1-2: bridge asset #1 L1->L2 to a fixed EOA and capture its claim params (do NOT claim yet).
	params1 := bridgeMessageL1ToL2GetParams(ctx, t, env, l1Opts, reentrancyReceiver1, amount1)
	// STEP 3-4: bridge asset #2 L1->L2 to the mock contract and capture its claim params.
	params2 := bridgeMessageL1ToL2GetParams(ctx, t, env, l1Opts, mockAddr, amount2)

	// STEP 5: arm the contract with asset #1's params so onMessageReceived reentrantly re-claims it.
	armTx, err := mock.UpdateParameters(
		l2Opts, params1.proofLocal, params1.proofRollup, params1.globalIndex,
		params1.mainnetExitRoot, params1.rollupExitRoot, params1.originNetwork, params1.originAddress,
		params1.destNetwork, params1.destination, params1.amount, params1.metadata)
	require.NoError(t, err, "updateParameters on mock")
	armReceipt, err := bind.WaitMined(ctx, env.Clients.L2, armTx)
	require.NoError(t, err, "wait for updateParameters")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, armReceipt.Status, "updateParameters failed")

	// STEP 6: record initial WETH balances.
	initialReceiverBal, err := weth.BalanceOf(callOpts, reentrancyReceiver1)
	require.NoError(t, err, "initial receiver WETH balance")
	initialContractBal, err := weth.BalanceOf(callOpts, mockAddr)
	require.NoError(t, err, "initial contract WETH balance")

	// STEP 7: claim asset #2 (destination=contract). The bridge delivers the message to the mock, which
	// fires onMessageReceived; that hook reentrantly calls claimMessage with the armed asset #1 params.
	// Because asset #1 has NOT been claimed yet, this reentrant inner claim SUCCEEDS, crediting the EOA
	// receiver with amount_1 — exactly mirroring the bats, which never claims #1 directly and relies on
	// the reentrant path to settle it. The outer claim of #2 then completes, crediting the contract.
	claimMessage(ctx, t, env, l2Opts, params2)

	// STEP 8 (LOAD-BEARING): now that asset #1 has been settled via the reentrant path, a direct
	// duplicate ClaimMessage of asset #1 must be rejected by the bridge's already-claimed guard. This is
	// the reentrancy/double-claim protection under test (bats check_claim_revert_code AlreadyClaimed).
	assertDuplicateClaimMessageRejected(ctx, t, env, l2Opts, params1)

	// STEP 10: balance deltas.
	finalReceiverBal, err := weth.BalanceOf(callOpts, reentrancyReceiver1)
	require.NoError(t, err, "final receiver WETH balance")
	finalContractBal, err := weth.BalanceOf(callOpts, mockAddr)
	require.NoError(t, err, "final contract WETH balance")

	receiverDelta := new(big.Int).Sub(finalReceiverBal, initialReceiverBal)
	require.Equal(t, 0, receiverDelta.Cmp(amount1),
		"receiver WETH balance must increase by exactly amount_1: got %s want %s",
		receiverDelta.String(), amount1.String())
	contractDelta := new(big.Int).Sub(finalContractBal, initialContractBal)
	require.Equal(t, 0, contractDelta.Cmp(amount2),
		"contract WETH balance must increase by exactly amount_2: got %s want %s",
		contractDelta.String(), amount2.String())

	// STEP 11: IsClaimed must be true for both deposits.
	assertClaimed(ctx, t, env, params1)
	assertClaimed(ctx, t, env, params2)
}

// testClaimReentrancyInternalAndBridgeAsset ports bats @test 2
// ("Test execute multiple claimMessages via testClaim with internal reentrancy and bridgeAsset call").
//
// Mapping to bats steps:
//   - STEP 1     -> bridge asset #1 L1->L2 to the mock contract (0.03 ETH), capture params #1.
//   - STEP 2     -> bridge asset #2 L1->L2 to a fixed EOA (reentrancyReceiver2, 0.02 ETH), params #2.
//   - STEP 3     -> bridge asset #3 L1->L2 to the same EOA (0.03 ETH), params #3.
//   - STEP 4     -> updateParameters(asset #2) (arms the contract; the bats does this though @test 2's
//     success path does not rely on onMessageReceived firing for #2 — it is faithfully replicated).
//   - STEP 5     -> record initial WETH balances.
//   - STEP 6     -> ABI-encode claimData1 (#1 tuple), bridgeAsset tuple, claimData2 (#3 tuple).
//   - STEP 7     -> contract.testClaim(claimData1, bridgeAsset, claimData2) with value=0.0004 ETH must
//     SUCCEED (two valid claimMessage calls + internal invalid destinationNetwork=1000 call that the
//     contract requires to revert + one bridgeAsset).
//   - STEP 8-9   -> all three claims processed; balance deltas: contract += amount_1,
//     receiver += amount_2 + amount_3.
//   - STEP 12    -> IsClaimed true for all three deposits.
//   - STEP 13    -> internal bridgeAsset observed (see deviation note below).
func testClaimReentrancyInternalAndBridgeAsset(
	ctx context.Context, t *testing.T, env *envs.Env, mockAddr common.Address,
	mock *bridgemessagereceivermock.Bridgemessagereceivermock, weth *mintableerc20.Mintableerc20,
) {
	t.Helper()
	amount1 := big.NewInt(3e16) // 0.03 ETH
	amount2 := big.NewInt(2e16) // 0.02 ETH
	amount3 := big.NewInt(3e16) // 0.03 ETH

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	callOpts := &bind.CallOpts{Context: ctx}

	// STEP 1-3: bridge the three assets and capture their claim params (none claimed yet — testClaim
	// performs the claims itself).
	params1 := bridgeMessageL1ToL2GetParams(ctx, t, env, l1Opts, mockAddr, amount1)
	params2 := bridgeMessageL1ToL2GetParams(ctx, t, env, l1Opts, reentrancyReceiver2, amount2)
	params3 := bridgeMessageL1ToL2GetParams(ctx, t, env, l1Opts, reentrancyReceiver2, amount3)

	// STEP 4: arm the contract with asset #2's params (faithful to the bats, which calls
	// updateParameters with params #2 before testClaim).
	armTx, err := mock.UpdateParameters(
		l2Opts, params2.proofLocal, params2.proofRollup, params2.globalIndex,
		params2.mainnetExitRoot, params2.rollupExitRoot, params2.originNetwork, params2.originAddress,
		params2.destNetwork, params2.destination, params2.amount, params2.metadata)
	require.NoError(t, err, "updateParameters on mock")
	armReceipt, err := bind.WaitMined(ctx, env.Clients.L2, armTx)
	require.NoError(t, err, "wait for updateParameters")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, armReceipt.Status, "updateParameters failed")

	// STEP 5: record initial WETH balances.
	initialReceiverBal, err := weth.BalanceOf(callOpts, reentrancyReceiver2)
	require.NoError(t, err, "initial receiver WETH balance")
	initialContractBal, err := weth.BalanceOf(callOpts, mockAddr)
	require.NoError(t, err, "initial contract WETH balance")

	// STEP 6: ABI-encode the three arguments to testClaim.
	claimData1 := encodeClaimDataTuple(t, params1)
	claimData2 := encodeClaimDataTuple(t, params3)
	// The internal bridgeAsset uses the L2 WETH/native token. On L2 the native token is bridged with
	// token=zero-address (native), matching the bats native_token_addr for the L2->L2 bridgeAsset.
	bridgeAssetData := encodeBridgeAssetTuple(
		t, internalBridgeAssetNetwork, internalBridgeAssetReceiver, internalBridgeAssetAmount,
		common.Address{}, true, []byte{})

	// STEP 7: call testClaim with value = the internal bridgeAsset amount. Must SUCCEED.
	l2Opts.Value = internalBridgeAssetAmount
	testClaimTx, err := mock.TestClaim(l2Opts, claimData1, bridgeAssetData, claimData2)
	l2Opts.Value = nil
	require.NoError(t, err, "testClaim on mock")
	testClaimReceipt, err := bind.WaitMined(ctx, env.Clients.L2, testClaimTx)
	require.NoError(t, err, "wait for testClaim")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, testClaimReceipt.Status,
		"testClaim must succeed (two claimMessage + internal revert + bridgeAsset): tx=%s",
		testClaimTx.Hash().Hex())
	log.Infof("[TestClaimReentrancy/Internal] testClaim succeeded: tx=%s", testClaimTx.Hash().Hex())

	// STEP 8-9: balance deltas. contract += amount_1; receiver += amount_2 + amount_3.
	finalReceiverBal, err := weth.BalanceOf(callOpts, reentrancyReceiver2)
	require.NoError(t, err, "final receiver WETH balance")
	finalContractBal, err := weth.BalanceOf(callOpts, mockAddr)
	require.NoError(t, err, "final contract WETH balance")

	contractDelta := new(big.Int).Sub(finalContractBal, initialContractBal)
	require.Equal(t, 0, contractDelta.Cmp(amount1),
		"contract WETH balance must increase by exactly amount_1: got %s want %s",
		contractDelta.String(), amount1.String())
	expectedReceiverDelta := new(big.Int).Add(amount2, amount3)
	receiverDelta := new(big.Int).Sub(finalReceiverBal, initialReceiverBal)
	require.Equal(t, 0, receiverDelta.Cmp(expectedReceiverDelta),
		"receiver WETH balance must increase by amount_2 + amount_3: got %s want %s",
		receiverDelta.String(), expectedReceiverDelta.String())

	// STEP 12: IsClaimed must be true for all three deposits.
	assertClaimed(ctx, t, env, params1)
	assertClaimed(ctx, t, env, params2)
	assertClaimed(ctx, t, env, params3)

	// STEP 13 (DEVIATION): the bats queries the bridge service get_bridge for the testClaim tx hash and
	// asserts the internal bridgeAsset's amount and destination address. Reusing the bridge-service
	// get_bridge by tx hash here would require waiting for the L2-originated bridge to be indexed and
	// is awkward to assert without modifying helpers (out of scope). Instead, the internal bridgeAsset
	// is asserted directly from on-chain evidence: the L2 bridge emits a BridgeEvent log from the mock's
	// bridgeAsset call inside the SAME testClaim receipt. We verify exactly one such bridge event was
	// emitted by the L2 bridge with destinationNetwork=2, destinationAddress=internalBridgeAssetReceiver,
	// amount=internalBridgeAssetAmount, originating from the mock contract.
	assertInternalBridgeAssetEvent(ctx, t, env, testClaimReceipt, mockAddr)
}

// bridgeMessageL1ToL2GetParams bridges a native-value message L1->L2 to the given destination and
// returns its claim params (proofs + bridge fields) WITHOUT claiming. It mirrors the bats
// bridge_message + extract_claim_parameters_json pair, reusing the P1 wait helpers. The bridged value
// is sent as the message value (the L2 recipient receives WETH on claim). This deliberately does not
// claim, leaving the caller in control of when/whether the claim happens (the reentrancy flows arm the
// contract and/or claim in a specific order).
func bridgeMessageL1ToL2GetParams(
	ctx context.Context, t *testing.T, env *envs.Env, l1Opts *bind.TransactOpts,
	destination common.Address, amount *big.Int,
) claimParams {
	t.Helper()
	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	require.NoError(t, err, "get L2 network ID")

	l1Opts.Value = amount
	defer func() { l1Opts.Value = nil }()
	tx, err := env.L1.Contracts.Bridge.BridgeMessage(l1Opts, l2NetworkID, destination, true, nil)
	require.NoError(t, err, "BridgeMessage on L1")
	receipt, err := bind.WaitMined(ctx, env.Clients.L1, tx)
	require.NoError(t, err, "wait for BridgeMessage tx")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "BridgeMessage tx failed")

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

// claimMessage issues a successful ClaimMessage on L2 for the given params, asserting the claim is
// mined successfully.
func claimMessage(ctx context.Context, t *testing.T, env *envs.Env, l2Opts *bind.TransactOpts, p claimParams) {
	t.Helper()
	tx, err := env.L2.Contracts.L2Bridge.ClaimMessage(
		l2Opts, p.proofLocal, p.proofRollup, p.globalIndex, p.mainnetExitRoot, p.rollupExitRoot,
		p.originNetwork, p.originAddress, p.destNetwork, p.destination, p.amount, p.metadata)
	require.NoError(t, err, "ClaimMessage on L2")
	receipt, err := bind.WaitMined(ctx, env.Clients.L2, tx)
	require.NoError(t, err, "wait for ClaimMessage tx")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status,
		"ClaimMessage must succeed: tx=%s", tx.Hash().Hex())
}

// assertDuplicateClaimMessageRejected re-issues a ClaimMessage on L2 with already-claimed params and
// asserts it is rejected by the bridge's already-claimed guard. As with ClaimAsset, go-ethereum
// surfaces the revert either at send time (gas estimation reverts) or as a failed receipt. Both count
// as a rejection; only an accepted duplicate (no send error AND a successful receipt) fails the test.
// This is the ClaimMessage analogue of bridge_test_core_test.go's assertDuplicateClaimAssetRejected.
func assertDuplicateClaimMessageRejected(
	ctx context.Context, t *testing.T, env *envs.Env, l2Opts *bind.TransactOpts, p claimParams,
) {
	t.Helper()
	tx, err := env.L2.Contracts.L2Bridge.ClaimMessage(
		l2Opts, p.proofLocal, p.proofRollup, p.globalIndex, p.mainnetExitRoot, p.rollupExitRoot,
		p.originNetwork, p.originAddress, p.destNetwork, p.destination, p.amount, p.metadata)
	if err != nil {
		log.Infof("[assertDuplicateClaimMessageRejected] duplicate claim rejected at send: %v", err)
		return
	}
	receipt, err := bind.WaitMined(ctx, env.Clients.L2, tx)
	require.NoError(t, err, "wait for duplicate ClaimMessage tx")
	require.Equal(t, ethtypes.ReceiptStatusFailed, receipt.Status,
		"duplicate ClaimMessage must be rejected (already claimed), but it succeeded: tx=%s",
		tx.Hash().Hex())
	log.Infof("[assertDuplicateClaimMessageRejected] duplicate claim mined with failed status: tx=%s",
		tx.Hash().Hex())
}

// assertClaimed asserts IsClaimed(depositCount, originNetwork) == true for the given params, porting
// the bats is_claimed checks.
func assertClaimed(ctx context.Context, t *testing.T, env *envs.Env, p claimParams) {
	t.Helper()
	claimed, err := env.L2.Contracts.L2Bridge.IsClaimed(&bind.CallOpts{Context: ctx}, p.depositCount, p.originNetwork)
	require.NoError(t, err, "IsClaimed(depositCount=%d, originNetwork=%d)", p.depositCount, p.originNetwork)
	require.True(t, claimed,
		"deposit must be claimed: depositCount=%d originNetwork=%d", p.depositCount, p.originNetwork)
}

// encodeClaimDataTuple ABI-encodes a claimParams into the
// tuple(bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes)
// expected by the mock's testClaim (the Go analogue of the bats `cast abi-encode "tuple(...)"`).
func encodeClaimDataTuple(t *testing.T, p claimParams) []byte {
	t.Helper()
	args := claimDataTupleArgs()
	encoded, err := args.Pack(
		p.proofLocal, p.proofRollup, p.globalIndex, p.mainnetExitRoot, p.rollupExitRoot,
		p.originNetwork, p.originAddress, p.destNetwork, p.destination, p.amount, p.metadata)
	require.NoError(t, err, "ABI-encode claimData tuple")
	return encoded
}

// encodeBridgeAssetTuple ABI-encodes the tuple(uint32,address,uint256,address,bool,bytes) expected by
// the mock's testClaim for the internal bridgeAsset call (the Go analogue of the bats
// `cast abi-encode "tuple(uint32,address,uint256,address,bool,bytes)"`).
func encodeBridgeAssetTuple(
	t *testing.T, destNetwork uint32, destination common.Address, amount *big.Int,
	token common.Address, forceUpdate bool, permitData []byte,
) []byte {
	t.Helper()
	args := bridgeAssetTupleArgs()
	encoded, err := args.Pack(destNetwork, destination, amount, token, forceUpdate, permitData)
	require.NoError(t, err, "ABI-encode bridgeAsset tuple")
	return encoded
}

// abi type singletons used to build the argument lists below. abi.NewType never fails for these
// well-formed static descriptors, so the errors are ignored.
var (
	abiBytes32Arr32, _ = abi.NewType("bytes32[32]", "", nil)
	abiUint256, _      = abi.NewType("uint256", "", nil)
	abiBytes32, _      = abi.NewType("bytes32", "", nil)
	abiUint32, _       = abi.NewType("uint32", "", nil)
	abiAddress, _      = abi.NewType("address", "", nil)
	abiBool, _         = abi.NewType("bool", "", nil)
	abiBytes, _        = abi.NewType("bytes", "", nil)
)

// claimDataTupleArgs returns the abi.Arguments matching the Solidity
// abi.decode(claimData, (bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes))
// in the mock's testClaim. A flat argument list (not a wrapped tuple) is used because Solidity's
// abi.decode of a type list expects the standard head/tail encoding of that sequence — which, for this
// shape, is byte-for-byte identical to the legacy bats `cast abi-encode "tuple(...)"` output (verified).
func claimDataTupleArgs() abi.Arguments {
	return abi.Arguments{
		{Type: abiBytes32Arr32}, // smtProofLocalExitRoot
		{Type: abiBytes32Arr32}, // smtProofRollupExitRoot
		{Type: abiUint256},      // globalIndex
		{Type: abiBytes32},      // mainnetExitRoot
		{Type: abiBytes32},      // rollupExitRoot
		{Type: abiUint32},       // originNetwork
		{Type: abiAddress},      // originAddress
		{Type: abiUint32},       // destinationNetwork
		{Type: abiAddress},      // destinationAddress
		{Type: abiUint256},      // amount
		{Type: abiBytes},        // metadata
	}
}

// bridgeAssetTupleArgs returns the abi.Arguments matching the Solidity
// abi.decode(bridgeAsset, (uint32,address,uint256,address,bool,bytes)) in the mock's testClaim.
func bridgeAssetTupleArgs() abi.Arguments {
	return abi.Arguments{
		{Type: abiUint32},  // destinationNetwork
		{Type: abiAddress}, // destinationAddress
		{Type: abiUint256}, // amount
		{Type: abiAddress}, // token
		{Type: abiBool},    // forceUpdateGlobalExitRoot
		{Type: abiBytes},   // permitData
	}
}

// assertInternalBridgeAssetEvent verifies the internal bridgeAsset performed inside the mock's
// testClaim by inspecting the testClaim transaction receipt for a BridgeEvent emitted by the L2 bridge.
// It requires that exactly one such event targets internalBridgeAssetReceiver on
// internalBridgeAssetNetwork for internalBridgeAssetAmount and originates from the mock contract
// (originAddress == mockAddr). This replaces the bats STEP 13 bridge-service get_bridge assertion with
// a direct on-chain check (see the deviation note in the calling subtest).
func assertInternalBridgeAssetEvent(
	ctx context.Context, t *testing.T, env *envs.Env, receipt *ethtypes.Receipt, mockAddr common.Address,
) {
	t.Helper()
	_ = ctx
	filterer, err := agglayerbridgel2.NewAgglayerbridgel2Filterer(env.L2.Contracts.L2BridgeAddress, env.Clients.L2)
	require.NoError(t, err, "build L2 bridge filterer")

	matches := 0
	for _, lg := range receipt.Logs {
		if lg == nil || lg.Address != env.L2.Contracts.L2BridgeAddress {
			continue
		}
		ev, err := filterer.ParseBridgeEvent(*lg)
		if err != nil {
			continue // not a BridgeEvent log
		}
		if ev.DestinationNetwork == internalBridgeAssetNetwork &&
			ev.DestinationAddress == internalBridgeAssetReceiver &&
			ev.Amount.Cmp(internalBridgeAssetAmount) == 0 &&
			ev.OriginAddress == mockAddr {
			matches++
		}
	}
	require.Equal(t, 1, matches,
		"testClaim receipt must contain exactly one internal bridgeAsset BridgeEvent "+
			"(destNetwork=%d destination=%s amount=%s origin=%s)",
		internalBridgeAssetNetwork, internalBridgeAssetReceiver.Hex(),
		internalBridgeAssetAmount.String(), mockAddr.Hex())
}
