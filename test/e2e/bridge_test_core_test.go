package e2e

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/contracts/mintableerc20"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// Timeout budgets for TestBridgeCore subtests. L1->L2 legs settle within a few minutes (an L1 Info
// Tree leaf injected on L2). The L2->L1 leg is fundamentally slower: an L2->L1 bridge becomes
// claimable only once a PP certificate covering the exit settles and the resulting rollup exit root
// propagates into a new GER / L1 Info Tree leaf, which spans several agglayer epochs. The
// context-driven wait in BridgeL2ToL1 needs an adequate deadline. Cert settlement latency is highly
// variable and has exceeded 25 min in low-activity runs, so the L2->L1 subtest gets a generous ~40
// min budget while the cheaper L1->L2 subtests get a few minutes each.
const (
	bridgeCoreL1ToL2Timeout = 8 * time.Minute
	bridgeCoreL2ToL1Timeout = 40 * time.Minute
)

// bridgeCoreMessageAmount is the small ETH value bridged with the "Transfer message" case.
var bridgeCoreMessageAmount = big.NewInt(1e14) // 0.0001 ETH

// bridgeCoreNativeAmount is the small ETH value bridged with the "Native token transfer" case.
var bridgeCoreNativeAmount = big.NewInt(1e14) // 0.0001 ETH

// bridgeCoreERC20Amount is the ERC20 amount bridged with the "ERC20 token deposit" cases.
var bridgeCoreERC20Amount = big.NewInt(1e17) // 0.1 token (18 decimals)

// TestBridgeCore ports the core happy-path bridge cases from the legacy bridge-e2e.bats: a message
// transfer L1->L2, an ERC20 deposit L1->L2 (with double-claim rejection), an ERC20 deposit L2->L1,
// and a native (ETH) transfer L1->L2 (with double-claim rejection). It runs each case as a subtest
// against the shared op-pp env, reusing the P1 helpers and bridge_utils.go bridge/claim machinery
// rather than duplicating it. It leaves the env healthy (pooled keys are returned and an
// assertNetworkHealthy check runs at the end) so later tests and the TestMain post-suite check pass.
func TestBridgeCore(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	t.Run("TransferMessageL1ToL2", testBridgeCoreTransferMessage)
	t.Run("ERC20DepositL1ToL2", testBridgeCoreERC20L1ToL2)
	t.Run("ERC20DepositL2ToL1", testBridgeCoreERC20L2ToL1)
	t.Run("NativeTransferL1ToL2", testBridgeCoreNativeL1ToL2)

	// After all subtests, assert the shared env is still healthy so a leak surfaces here rather than
	// only in the TestMain post-suite check.
	healthCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	assertNetworkHealthy(healthCtx, t, env)
}

// testBridgeCoreTransferMessage ports the "Transfer message" bats case: a bridgeMessage L1->L2
// followed by a successful claimMessage on L2. It asserts the L2 recipient's native balance increased
// by exactly the bridged amount. The message is bridged to a DISTINCT, freshly generated recipient
// (not the pooled L2 account that submits the claim) so the recipient pays no gas and its balance
// increases by exactly +amount; the claim itself is still submitted by the pooled L2 transactor,
// which pays the claim gas. The bats case additionally submitted a message L2->L1 bridge without
// claiming; that leg is intentionally omitted here because the load-bearing assertion is the
// successful L1->L2 message claim, and submitting an unclaimed L2->L1 message would leak state into
// the shared env.
func testBridgeCoreTransferMessage(t *testing.T) {
	env := testEnv
	ctx, cancel := context.WithTimeout(context.Background(), bridgeCoreL1ToL2Timeout)
	defer cancel()

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	// Bridge the message to a distinct, freshly generated recipient that pays no gas, so its balance
	// increases by exactly the bridged amount. The pooled L2 transactor (l2Opts) still submits and
	// pays for the claim.
	recipientKey, err := crypto.GenerateKey()
	require.NoError(t, err, "generate fresh L2 recipient key")
	destination := crypto.PubkeyToAddress(recipientKey.PublicKey)

	initialBalance, err := env.Clients.L2.BalanceAt(ctx, destination, nil)
	require.NoError(t, err, "read initial L2 balance")

	result := bridgeMessageL1ToL2AndClaim(ctx, t, env, l1Opts, l2Opts, destination, bridgeCoreMessageAmount, nil)
	log.Infof("[TestBridgeCore/TransferMessage] claimed message: recipient=%s deposit_count=%d global_index=%s",
		destination.Hex(), result.DepositCount, result.GlobalIndex.String())

	finalBalance, err := env.Clients.L2.BalanceAt(ctx, destination, nil)
	require.NoError(t, err, "read final L2 balance")
	increase := new(big.Int).Sub(finalBalance, initialBalance)
	require.Equal(t, 0, increase.Cmp(bridgeCoreMessageAmount),
		"L2 recipient balance must increase by the bridged message amount: got %s want %s",
		increase.String(), bridgeCoreMessageAmount.String())
}

// testBridgeCoreERC20L1ToL2 ports the "ERC20 token deposit L1 -> L2" bats case. It deploys a fresh
// ERC20 on L1 (pure-Go, via the mintableerc20 binding), mints and approves it for the L1 bridge,
// bridges it L1->L2 as an asset, claims it on L2, and asserts the destination's wrapped-token
// balance on L2 increased by the deposited amount. It then attempts a SECOND claim of the same
// deposit and asserts the duplicate claim is rejected (the L2 bridge reverts with AlreadyClaimed)
// and that the wrapped-token balance did not change. The double-claim rejection is the load-bearing
// assertion of this case.
func testBridgeCoreERC20L1ToL2(t *testing.T) {
	env := testEnv
	ctx, cancel := context.WithTimeout(context.Background(), bridgeCoreL1ToL2Timeout)
	defer cancel()

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	// Deploy a fresh ERC20 on L1 so this case bridges an L1-origin token (matching the bats case,
	// which deployed a fresh contract). The env only deploys an L2-native MintableERC20, so an
	// L1-origin token must be deployed here.
	l1TokenAddr, deployTx, l1Token, err := mintableerc20.DeployMintableerc20(
		l1Opts, env.Clients.L1, "L1TestToken", "L1TEST")
	require.NoError(t, err, "deploy L1 ERC20")
	deployReceipt, err := bind.WaitMined(ctx, env.Clients.L1, deployTx)
	require.NoError(t, err, "wait for L1 ERC20 deploy")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, deployReceipt.Status, "L1 ERC20 deploy failed")
	log.Infof("[TestBridgeCore/ERC20L1ToL2] deployed L1 ERC20 at %s", l1TokenAddr.Hex())

	// Mint the deposit amount to the sender and approve the L1 bridge to spend it.
	mintTx, err := l1Token.Mint(l1Opts, l1Opts.From, bridgeCoreERC20Amount)
	require.NoError(t, err, "mint L1 ERC20")
	mintReceipt, err := bind.WaitMined(ctx, env.Clients.L1, mintTx)
	require.NoError(t, err, "wait for L1 ERC20 mint")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, mintReceipt.Status, "L1 ERC20 mint failed")

	l1BridgeAddr := l1BridgeAddress(t, env)
	approveTx, err := l1Token.Approve(l1Opts, l1BridgeAddr, bridgeCoreERC20Amount)
	require.NoError(t, err, "approve L1 bridge for L1 ERC20")
	approveReceipt, err := bind.WaitMined(ctx, env.Clients.L1, approveTx)
	require.NoError(t, err, "wait for L1 ERC20 approve")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, approveReceipt.Status, "L1 ERC20 approve failed")

	// Bridge the ERC20 L1->L2 (asset bridge with a non-native token).
	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	require.NoError(t, err, "get L2 network ID")
	destination := l2Opts.From

	bridgeTx, err := env.L1.Contracts.Bridge.BridgeAsset(
		l1Opts, l2NetworkID, destination, bridgeCoreERC20Amount, l1TokenAddr, true, nil)
	require.NoError(t, err, "BridgeAsset L1->L2 (ERC20)")
	bridgeReceipt, err := bind.WaitMined(ctx, env.Clients.L1, bridgeTx)
	require.NoError(t, err, "wait for ERC20 bridge tx")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, bridgeReceipt.Status, "ERC20 bridge tx failed")

	// Wait for the bridge to be indexed, included in the L1 Info Tree, and injected on L2 (reuses the
	// P1 polling helpers rather than re-implementing the wait loops).
	bridge := waitForBridgeByTxHash(ctx, t, env, 0, bridgeTx.Hash())
	depositCount := bridge.DepositCount
	l1InfoTreeIndex := waitForL1InfoTreeIndex(ctx, t, env, 0, depositCount)
	waitForInjectedL1InfoLeaf(ctx, t, env, l2NetworkID, l1InfoTreeIndex)

	claimProof, err := env.Clients.BridgeService.GetClaimProof(ctx, 0, l1InfoTreeIndex, depositCount)
	require.NoError(t, err, "get claim proof")
	require.NotNil(t, claimProof, "claim proof must not be nil")
	proofLocal, proofRollup := claimProofToContractProofs(claimProof)
	mainnetExitRoot := common.HexToHash(string(claimProof.L1InfoTreeLeaf.MainnetExitRoot))
	rollupExitRoot := common.HexToHash(string(claimProof.L1InfoTreeLeaf.RollupExitRoot))
	originTokenAddress := common.HexToAddress(string(bridge.OriginAddress))
	metadata := common.FromHex(bridge.Metadata)

	// First claim on L2 — must succeed and deploy/mint the wrapped token.
	claimTx, err := env.L2.Contracts.L2Bridge.ClaimAsset(
		l2Opts, proofLocal, proofRollup, bridge.GlobalIndex, mainnetExitRoot, rollupExitRoot,
		bridge.OriginNetwork, originTokenAddress, bridge.DestinationNetwork,
		destination, bridgeCoreERC20Amount, metadata)
	require.NoError(t, err, "ClaimAsset on L2 (ERC20)")
	claimReceipt, err := bind.WaitMined(ctx, env.Clients.L2, claimTx)
	require.NoError(t, err, "wait for ClaimAsset tx")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, claimReceipt.Status, "ClaimAsset tx failed")

	// Resolve the wrapped-token address on L2 from the bridge-service token mappings and assert the
	// destination's wrapped-token balance increased by the deposited amount.
	wrappedTokenAddr := waitForWrappedTokenAddress(ctx, t, env, int(l2NetworkID), l1TokenAddr)
	wrappedToken, err := mintableerc20.NewMintableerc20(wrappedTokenAddr, env.Clients.L2)
	require.NoError(t, err, "bind wrapped token on L2")

	balanceAfterClaim, err := wrappedToken.BalanceOf(callOpts, destination)
	require.NoError(t, err, "read wrapped-token balance after claim")
	require.Equal(t, 0, balanceAfterClaim.Cmp(bridgeCoreERC20Amount),
		"wrapped-token balance must equal the deposited amount after claim: got %s want %s",
		balanceAfterClaim.String(), bridgeCoreERC20Amount.String())

	// Attempt a SECOND claim of the same deposit — it must be rejected (already claimed) and the
	// wrapped-token balance must be unchanged. The returned gas is irrelevant here because the
	// assertion below measures the ERC20 wrapped-token balance, not the claimer's native balance.
	_ = assertDuplicateClaimAssetRejected(ctx, t, env, l2Opts, claimAssetParams{
		proofLocal:      proofLocal,
		proofRollup:     proofRollup,
		globalIndex:     bridge.GlobalIndex,
		mainnetExitRoot: mainnetExitRoot,
		rollupExitRoot:  rollupExitRoot,
		originNetwork:   bridge.OriginNetwork,
		originToken:     originTokenAddress,
		destNetwork:     bridge.DestinationNetwork,
		destination:     destination,
		amount:          bridgeCoreERC20Amount,
		metadata:        metadata,
	})

	balanceAfterDuplicate, err := wrappedToken.BalanceOf(callOpts, destination)
	require.NoError(t, err, "read wrapped-token balance after duplicate claim")
	require.Equal(t, 0, balanceAfterDuplicate.Cmp(balanceAfterClaim),
		"wrapped-token balance must be unchanged after a rejected duplicate claim: got %s want %s",
		balanceAfterDuplicate.String(), balanceAfterClaim.String())
}

// testBridgeCoreERC20L2ToL1 ports the "ERC20 token deposit L2 -> L1" bats case. It reuses the P1
// helper bridgeERC20L2ToL1AndClaim, which mints and approves the env L2-native MintableERC20 on L2,
// then bridges and claims it on L1 via BridgeL2ToL1. The L2->L1 leg is the long-running one (it
// depends on PP certificate settlement spanning several agglayer epochs), so it is given a context
// with a >= ~20 min deadline.
func testBridgeCoreERC20L2ToL1(t *testing.T) {
	env := testEnv
	ctx, cancel := context.WithTimeout(context.Background(), bridgeCoreL2ToL1Timeout)
	defer cancel()

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	bridgeERC20L2ToL1AndClaim(ctx, t, env, l1Opts, l2Opts, bridgeCoreERC20Amount)
	log.Infof("[TestBridgeCore/ERC20L2ToL1] L2->L1 ERC20 bridge+claim complete")
}

// testBridgeCoreNativeL1ToL2 ports the "Native token transfer L1 -> L2" bats case. It bridges native
// ETH L1->L2 and claims it on L2 (reusing the P1 helper bridgeETHL1ToL2AndClaim), asserts the L2
// recipient balance increased, then attempts a SECOND claim of the same deposit and asserts the
// duplicate claim is rejected and the recipient balance is unchanged. The double-claim rejection is
// the load-bearing assertion of this case.
//
// The underlying BridgeL1ToL2WithResult helper hardcodes the destination to l2Opts.From, so the
// recipient is also the account that pays the claim-tx gas. A naive +amount assertion would therefore
// be short by the claim fees. This env is an OP-Stack L2 (op-geth), which charges an L1 data fee on
// top of the L2 execution gas (GasUsed * EffectiveGasPrice), so an exact `amount - gasSpent`
// accounting is inherently incomplete (it was observed short by ~1013 wei of unaccounted L1 data fee).
// Rather than depend on exact OP-Stack fee accounting, this asserts the bats intent ("native transfer
// arrived"): the recipient balance strictly increased, the net increase does not exceed the bridged
// amount, and the shortfall (claim fees) is a tiny fraction of the amount (< amount/1000).
func testBridgeCoreNativeL1ToL2(t *testing.T) {
	env := testEnv
	ctx, cancel := context.WithTimeout(context.Background(), bridgeCoreL1ToL2Timeout)
	defer cancel()

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	destination := l2Opts.From
	initialBalance, err := env.Clients.L2.BalanceAt(ctx, destination, nil)
	require.NoError(t, err, "read initial L2 balance")

	result := bridgeETHL1ToL2AndClaim(ctx, t, env, l1Opts, l2Opts, bridgeCoreNativeAmount)
	log.Infof("[TestBridgeCore/NativeL1ToL2] claimed native deposit: deposit_count=%d global_index=%s",
		result.DepositCount, result.GlobalIndex.String())

	balanceAfterClaim, err := env.Clients.L2.BalanceAt(ctx, destination, nil)
	require.NoError(t, err, "read L2 balance after claim")
	// The recipient (== claimer == gas payer) nets +amount from the claim minus the claim-tx fees. On
	// this OP-Stack L2 those fees are L2 execution gas plus an L1 data fee that the go-ethereum receipt
	// does not surface, so an exact delta is not assertable. Instead require that the balance strictly
	// increased, the increase did not exceed the bridged amount, and the fee shortfall is a tiny
	// fraction of the amount (< amount/1000).
	increase := new(big.Int).Sub(balanceAfterClaim, initialBalance)
	require.Equal(t, 1, increase.Sign(),
		"L2 recipient balance must strictly increase after the native claim: got delta %s (amount=%s)",
		increase.String(), bridgeCoreNativeAmount.String())
	require.LessOrEqual(t, increase.Cmp(bridgeCoreNativeAmount), 0,
		"L2 recipient balance increase must not exceed the bridged amount: got delta %s want <= %s",
		increase.String(), bridgeCoreNativeAmount.String())
	feeShortfall := new(big.Int).Sub(bridgeCoreNativeAmount, increase)
	feeTolerance := new(big.Int).Div(bridgeCoreNativeAmount, big.NewInt(1000))
	require.Equal(t, -1, feeShortfall.Cmp(feeTolerance),
		"native claim fee shortfall must be a tiny fraction of the amount: shortfall %s want < %s "+
			"(amount=%s delta=%s)",
		feeShortfall.String(), feeTolerance.String(), bridgeCoreNativeAmount.String(), increase.String())

	// Re-fetch the claim proof for the same deposit and attempt a SECOND claim — it must be rejected
	// (already claimed). The bridgeResult exposes DepositCount and L1InfoTreeIndex, which is enough to
	// reconstruct the same ClaimAsset parameters.
	claimProof, err := env.Clients.BridgeService.GetClaimProof(ctx, 0, result.L1InfoTreeIndex, result.DepositCount)
	require.NoError(t, err, "get claim proof for duplicate claim")
	require.NotNil(t, claimProof, "claim proof must not be nil")
	proofLocal, proofRollup := claimProofToContractProofs(claimProof)

	bridge := result.Bridge
	// The returned gas is not used to compute an exact expected balance below (OP-Stack L1 data fees
	// are not surfaced on the receipt); the balance change is bounded by a tolerance instead.
	_ = assertDuplicateClaimAssetRejected(ctx, t, env, l2Opts, claimAssetParams{
		proofLocal:      proofLocal,
		proofRollup:     proofRollup,
		globalIndex:     result.GlobalIndex,
		mainnetExitRoot: common.HexToHash(string(claimProof.L1InfoTreeLeaf.MainnetExitRoot)),
		rollupExitRoot:  common.HexToHash(string(claimProof.L1InfoTreeLeaf.RollupExitRoot)),
		originNetwork:   bridge.OriginNetwork,
		originToken:     common.HexToAddress(string(bridge.OriginAddress)),
		destNetwork:     bridge.DestinationNetwork,
		destination:     destination,
		amount:          bridgeCoreNativeAmount,
		metadata:        common.FromHex(bridge.Metadata),
	})

	// The duplicate claimer is also the recipient (destination == l2Opts.From), so no additional amount
	// must have been credited: the balance must not increase. The only allowed change is fees consumed
	// by a mined-but-failed duplicate (zero if it was rejected at send time). As with the claim above,
	// OP-Stack fees are not exactly assertable (the L1 data fee is not surfaced on the receipt), so the
	// downward change is bounded by the same tiny tolerance rather than required to equal the gas used.
	balanceAfterDuplicate, err := env.Clients.L2.BalanceAt(ctx, destination, nil)
	require.NoError(t, err, "read L2 balance after duplicate claim")
	require.LessOrEqual(t, balanceAfterDuplicate.Cmp(balanceAfterClaim), 0,
		"L2 recipient balance must not increase after a rejected duplicate claim: got %s want <= %s",
		balanceAfterDuplicate.String(), balanceAfterClaim.String())
	dupShortfall := new(big.Int).Sub(balanceAfterClaim, balanceAfterDuplicate)
	dupTolerance := new(big.Int).Div(bridgeCoreNativeAmount, big.NewInt(1000))
	require.Equal(t, -1, dupShortfall.Cmp(dupTolerance),
		"rejected duplicate claim must only consume a tiny fraction in fees: shortfall %s want < %s "+
			"(balanceAfterClaim=%s balanceAfterDuplicate=%s)",
		dupShortfall.String(), dupTolerance.String(),
		balanceAfterClaim.String(), balanceAfterDuplicate.String())
}

// claimAssetParams bundles the parameters for an L2 ClaimAsset call so the duplicate-claim helper can
// re-issue the exact same claim that was already settled.
type claimAssetParams struct {
	proofLocal      [32][32]byte
	proofRollup     [32][32]byte
	globalIndex     *big.Int
	mainnetExitRoot common.Hash
	rollupExitRoot  common.Hash
	originNetwork   uint32
	originToken     common.Address
	destNetwork     uint32
	destination     common.Address
	amount          *big.Int
	metadata        []byte
}

// assertDuplicateClaimAssetRejected re-issues a ClaimAsset on the L2 bridge with parameters that were
// already claimed and asserts the duplicate is rejected. The L2 bridge reverts with AlreadyClaimed,
// which go-ethereum surfaces either as an error at send time (gas estimation reverts) or, if the tx
// is mined, as a failed receipt status. Both outcomes count as a rejection; the helper fails only if
// the duplicate claim is accepted (no send error AND a successful receipt). It returns the gas spent
// by the rejected duplicate (zero when it was rejected at send time and no tx was mined; the gas of
// the failed tx when it was mined). Callers that measure the claimer's native balance use this to
// account for the gas a mined-but-failed duplicate consumes.
func assertDuplicateClaimAssetRejected(
	ctx context.Context, t *testing.T, env *envs.Env, l2Opts *bind.TransactOpts, p claimAssetParams,
) *big.Int {
	t.Helper()
	claimTx, err := env.L2.Contracts.L2Bridge.ClaimAsset(
		l2Opts, p.proofLocal, p.proofRollup, p.globalIndex, p.mainnetExitRoot, p.rollupExitRoot,
		p.originNetwork, p.originToken, p.destNetwork, p.destination, p.amount, p.metadata)
	if err != nil {
		// Most common path: gas estimation reverts because the deposit is already claimed. No tx is
		// mined, so no gas is spent.
		log.Infof("[assertDuplicateClaimAssetRejected] duplicate claim rejected at send: %v", err)
		return big.NewInt(0)
	}
	receipt, err := bind.WaitMined(ctx, env.Clients.L2, claimTx)
	require.NoError(t, err, "wait for duplicate ClaimAsset tx")
	require.Equal(t, ethtypes.ReceiptStatusFailed, receipt.Status,
		"duplicate ClaimAsset must be rejected (already claimed), but it succeeded: tx=%s",
		claimTx.Hash().Hex())
	gasSpent := receiptGasSpent(receipt)
	log.Infof("[assertDuplicateClaimAssetRejected] duplicate claim mined with failed status: tx=%s gasSpent=%s",
		claimTx.Hash().Hex(), gasSpent.String())
	return gasSpent
}

// receiptGasSpent returns the total native value spent on gas for a mined transaction:
// GasUsed * EffectiveGasPrice. EffectiveGasPrice is populated by the node for mined receipts.
func receiptGasSpent(receipt *ethtypes.Receipt) *big.Int {
	gasPrice := receipt.EffectiveGasPrice
	if gasPrice == nil {
		gasPrice = big.NewInt(0)
	}
	return new(big.Int).Mul(new(big.Int).SetUint64(receipt.GasUsed), gasPrice)
}

// waitForWrappedTokenAddress polls the bridge service token mappings on the given destination network
// until the wrapped-token address for the given origin token is available, returning it. The wrapped
// token is created lazily by the first ClaimAsset of an origin token, so the mapping may take a short
// while to be indexed after the claim.
func waitForWrappedTokenAddress(
	ctx context.Context, t *testing.T, env *envs.Env, networkID int, originToken common.Address,
) common.Address {
	t.Helper()
	originHex := originToken.Hex()
	var wrapped common.Address
	pollCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	err := pollWithBackoff(pollCtx, 2*time.Minute, backoffInitial, backoffMax, "wrapped token mapping",
		func() (bool, error) {
			pageSize := 100
			res, err := env.Clients.BridgeService.GetTokenMappings(pollCtx, client.GetTokenMappingsParams{
				NetworkID:          networkID,
				PageSize:           &pageSize,
				OriginTokenAddress: &originHex,
			})
			if err != nil {
				return false, nil //nolint:nilerr // transient; keep polling until timeout
			}
			if res == nil {
				return false, nil
			}
			for _, m := range res.TokenMappings {
				if common.HexToAddress(string(m.OriginTokenAddress)) == originToken {
					addr := common.HexToAddress(string(m.WrappedTokenAddress))
					if addr != (common.Address{}) {
						wrapped = addr
						return true, nil
					}
				}
			}
			return false, nil
		})
	require.NoError(t, err, "wait for wrapped token mapping (origin=%s network=%d)", originHex, networkID)
	require.NotEqual(t, common.Address{}, wrapped, "wrapped token address must not be zero")
	return wrapped
}

// l1BridgeAddress reads the L1 bridge contract address from the env's summary.json. The env's
// L1 bridge binding does not expose its address, so it is resolved from summary.json (the same file
// the env loader parses), matching the way other E2E tests read endpoints from summary.json.
func l1BridgeAddress(t *testing.T, env *envs.Env) common.Address {
	t.Helper()
	summaryPath := filepath.Join(env.EnvDir, "summary.json")
	data, err := os.ReadFile(summaryPath)
	require.NoError(t, err, "read summary.json at %s", summaryPath)

	var summary struct {
		Networks struct {
			L1 struct {
				Contracts struct {
					Bridge string `json:"bridge"`
				} `json:"contracts"`
			} `json:"l1"`
		} `json:"networks"`
	}
	require.NoError(t, json.Unmarshal(data, &summary), "parse summary.json")
	require.NotEmpty(t, summary.Networks.L1.Contracts.Bridge, "L1 bridge address not found in summary.json")
	return common.HexToAddress(summary.Networks.L1.Contracts.Bridge)
}
