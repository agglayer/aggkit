package e2e

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/contracts/mintableerc20"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// Timeout budgets for TestBridgeNightly subtests. All six ordering combos are L1->L2 only (the legacy
// bridge-e2e-nightly.bats has no L2->L1 claim), so each leg settles within a few minutes (an L1 Info
// Tree leaf injected on L2). Combos that bridge two assets before any claim (5/6) do two indexing
// rounds, so they get a larger budget. These are local consts and intentionally do NOT reuse the P3
// bridgeCoreL1ToL2Timeout constant.
const (
	bridgeNightlyL1ToL2Timeout      = 12 * time.Minute
	bridgeNightlyL1ToL2DeferTimeout = 15 * time.Minute
	bridgeNightlyHealthCheckTimeout = 2 * time.Minute
)

// bridgeNightlyERC20Amount is the ERC20 asset amount bridged in every combo: 0.1 token (18 decimals),
// matching the bats common "0.1ether" tokens_amount.
var bridgeNightlyERC20Amount = big.NewInt(1e17) // 0.1 token (18 decimals)

// indexedERC20Bridge captures everything needed to issue a VALID ClaimAsset later, after one or more
// other bridges have been submitted. It is the local "bridge-without-claim" result used to honor the
// ordering combos (bridge A, bridge B, then claim in a chosen order). The bridge_utils.go
// BridgeL1NoClaim helper only supports native ETH (token hardcoded to common.Address{}), so this local
// type/flow is required to defer an ERC20 asset claim.
type indexedERC20Bridge struct {
	l1TokenAddr  common.Address
	destination  common.Address
	amount       *big.Int
	l2NetworkID  uint32
	depositCount uint32
	l1InfoIndex  uint32
}

// indexedMessageBridge captures everything needed to issue a VALID ClaimMessage later. Mirrors
// indexedERC20Bridge for the message leg (combos 1/2 defer the message claim relative to the asset).
type indexedMessageBridge struct {
	destination  common.Address
	amount       *big.Int
	l2NetworkID  uint32
	depositCount uint32
	l1InfoIndex  uint32
}

// TestBridgeNightly ports the six bridge/claim ordering combinations from the legacy
// e2e/tests/aggkit/bridge-e2e-nightly.bats (all L1->L2 flows). Each combo is a subtest that submits
// the bridges and claims in the SAME order the bats does, then asserts the final wrapped-token
// balances / claim states. Claims are deferred where the bats defers them (combos 1/2/5/6) and
// reversed where the bats reverses them (combo 6). It reuses the P1/P3 helpers and the bridge_utils.go
// primitives for the wait loops, deploys L1-origin ERC20s via the mintableerc20 binding, bridges each
// asset to a distinct gas-free recipient (so an exact wrapped-token balance == bridged amount is
// assertable), returns all pooled keys, and asserts the env is healthy at the end.
func TestBridgeNightly(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	t.Run("MessageA_AssetB_ClaimAssetA_ClaimMessageB", testBridgeNightryMessageAssetClaimAssetMessage)
	t.Run("MessageA_AssetB_ClaimMessageA_ClaimAssetB", testBridgeNightryMessageAssetClaimMessageAsset)
	t.Run("MessageA_ClaimMessageA_AssetB_ClaimAssetB", testBridgeNightryMessageClaimMessageAssetClaimAsset)
	t.Run("AssetA_ClaimAssetA_AssetB_ClaimAssetB", testBridgeNightryAssetClaimAssetAssetClaimAsset)
	t.Run("AssetA_AssetB_ClaimA_ClaimB", testBridgeNightryAssetAssetClaimAClaimB)
	t.Run("AssetA_AssetB_ClaimB_ClaimA", testBridgeNightryAssetAssetClaimBClaimA)

	// After all subtests, assert the shared env is still healthy so a leak surfaces here rather than
	// only in the TestMain post-suite check.
	healthCtx, cancel := context.WithTimeout(context.Background(), bridgeNightlyHealthCheckTimeout)
	defer cancel()
	assertNetworkHealthy(healthCtx, t, env)
}

// ---------------------------------------------------------------------------------------------------
// Combo 1 — "Bridge message A → Bridge asset B → Claim asset A → Claim message B"
//
// IMPORTANT (prose-vs-actual): the bats step-3 prose says "Claim the bridged asset" labeled "asset A",
// but the actual process_bridge_claim call (bats line 46) claims $bridge_asset_tx_hash, i.e. the ASSET
// is claimed first, then the MESSAGE (bats line 67, $bridge_message_tx_hash). This port follows the
// ACTUAL tx-hash argument, not the loosely worded label: order is bridge-message, bridge-asset, then
// claim-asset, then claim-message.
func testBridgeNightryMessageAssetClaimAssetMessage(t *testing.T) {
	env := testEnv
	ctx, cancel := context.WithTimeout(context.Background(), bridgeNightlyL1ToL2Timeout)
	defer cancel()

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	// Step 1: bridge message A (dest = sender / pooled L1 account, amount 0) — no claim yet.
	msgDest := l1Opts.From
	msgBridge := bridgeMessageL1ToL2NoClaim(ctx, t, env, l1Opts, msgDest, big.NewInt(0), nil)

	// Step 2: deploy ERC20 B on L1, mint+approve, bridge asset B to a gas-free recipient — no claim yet.
	assetDest := freshRecipient(t)
	assetBridge := bridgeERC20L1ToL2NoClaim(ctx, t, env, l1Opts, assetDest, bridgeNightlyERC20Amount, "B")

	// Step 3 (actual): claim ASSET B first, assert wrapped-token balance == bridged amount.
	claimERC20L1ToL2(ctx, t, env, l2Opts, assetBridge)

	// Step 4: claim MESSAGE A — load-bearing assertion is a successful ClaimMessage receipt.
	claimMessageL1ToL2(ctx, t, env, l2Opts, msgBridge)

	log.Infof("[TestBridgeNightly/Combo1] message+asset bridged, asset-then-message claimed")
}

// ---------------------------------------------------------------------------------------------------
// Combo 2 — "Bridge message A → Bridge asset B → Claim message A → Claim asset B"
func testBridgeNightryMessageAssetClaimMessageAsset(t *testing.T) {
	env := testEnv
	ctx, cancel := context.WithTimeout(context.Background(), bridgeNightlyL1ToL2Timeout)
	defer cancel()

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	// Step 1: bridge message A (dest = sender, amount 0) — no claim yet.
	msgDest := l1Opts.From
	msgBridge := bridgeMessageL1ToL2NoClaim(ctx, t, env, l1Opts, msgDest, big.NewInt(0), nil)

	// Step 2: deploy ERC20 B, bridge asset B to a gas-free recipient — no claim yet.
	assetDest := freshRecipient(t)
	assetBridge := bridgeERC20L1ToL2NoClaim(ctx, t, env, l1Opts, assetDest, bridgeNightlyERC20Amount, "B")

	// Step 3: claim MESSAGE A first.
	claimMessageL1ToL2(ctx, t, env, l2Opts, msgBridge)

	// Step 4: claim ASSET B second; assert wrapped-token balance == bridged amount.
	claimERC20L1ToL2(ctx, t, env, l2Opts, assetBridge)

	log.Infof("[TestBridgeNightly/Combo2] message+asset bridged, message-then-asset claimed")
}

// ---------------------------------------------------------------------------------------------------
// Combo 3 — "Bridge message A → Claim message A → Bridge asset B → Claim asset B" (fully sequential).
func testBridgeNightryMessageClaimMessageAssetClaimAsset(t *testing.T) {
	env := testEnv
	ctx, cancel := context.WithTimeout(context.Background(), bridgeNightlyL1ToL2Timeout)
	defer cancel()

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	// Step 1+2: bridge message A then immediately claim it (the all-in-one P1 helper matches the
	// sequential bats order exactly here, so no deferral is needed).
	msgDest := l1Opts.From
	_ = bridgeMessageL1ToL2AndClaim(ctx, t, env, l1Opts, l2Opts, msgDest, big.NewInt(0), nil)

	// Step 3+4: deploy ERC20 B, bridge it, then claim it; assert wrapped-token balance == amount.
	assetDest := freshRecipient(t)
	assetBridge := bridgeERC20L1ToL2NoClaim(ctx, t, env, l1Opts, assetDest, bridgeNightlyERC20Amount, "B")
	claimERC20L1ToL2(ctx, t, env, l2Opts, assetBridge)

	log.Infof("[TestBridgeNightly/Combo3] message claimed, then asset bridged+claimed")
}

// ---------------------------------------------------------------------------------------------------
// Combo 4 — "Bridge asset A -> Claim asset A -> Bridge asset B -> Claim asset B" (two ERC20s, fully
// sequential: each asset is claimed before the next is bridged).
func testBridgeNightryAssetClaimAssetAssetClaimAsset(t *testing.T) {
	env := testEnv
	ctx, cancel := context.WithTimeout(context.Background(), bridgeNightlyL1ToL2DeferTimeout)
	defer cancel()

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	// Asset A: deploy + bridge + claim; assert wrapped balance == amount (mapping origin == A).
	destA := freshRecipient(t)
	bridgeA := bridgeERC20L1ToL2NoClaim(ctx, t, env, l1Opts, destA, bridgeNightlyERC20Amount, "A")
	claimERC20L1ToL2(ctx, t, env, l2Opts, bridgeA)

	// Asset B: deploy + bridge + claim; assert wrapped balance == amount (mapping origin == B).
	destB := freshRecipient(t)
	bridgeB := bridgeERC20L1ToL2NoClaim(ctx, t, env, l1Opts, destB, bridgeNightlyERC20Amount, "B")
	claimERC20L1ToL2(ctx, t, env, l2Opts, bridgeB)

	log.Infof("[TestBridgeNightly/Combo4] asset A claimed, then asset B bridged+claimed")
}

// ---------------------------------------------------------------------------------------------------
// Combo 5 — "Bridge A -> Bridge B -> Claim A -> Claim B" (two ERC20s, BOTH bridged before ANY claim;
// claimed in A,B order — DEFERRED claims). The bridge phase is fully separated from the claim phase.
func testBridgeNightryAssetAssetClaimAClaimB(t *testing.T) {
	env := testEnv
	ctx, cancel := context.WithTimeout(context.Background(), bridgeNightlyL1ToL2DeferTimeout)
	defer cancel()

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	// Bridge phase: bridge A then B (no claims yet).
	destA := freshRecipient(t)
	destB := freshRecipient(t)
	bridgeA := bridgeERC20L1ToL2NoClaim(ctx, t, env, l1Opts, destA, bridgeNightlyERC20Amount, "A")
	bridgeB := bridgeERC20L1ToL2NoClaim(ctx, t, env, l1Opts, destB, bridgeNightlyERC20Amount, "B")

	// Claim phase: claim A then B; assert each wrapped balance == amount (mappings origin == A / B).
	claimERC20L1ToL2(ctx, t, env, l2Opts, bridgeA)
	claimERC20L1ToL2(ctx, t, env, l2Opts, bridgeB)

	log.Infof("[TestBridgeNightly/Combo5] assets A+B bridged (deferred), claimed A then B")
}

// ---------------------------------------------------------------------------------------------------
// Combo 6 — "Bridge A -> Bridge B -> Claim B -> Claim A" (two ERC20s, BOTH bridged first, then claimed
// in REVERSE order — DEFERRED + reversed). The bridge phase is fully separated from the claim phase
// and the claim order is observably B-then-A.
func testBridgeNightryAssetAssetClaimBClaimA(t *testing.T) {
	env := testEnv
	ctx, cancel := context.WithTimeout(context.Background(), bridgeNightlyL1ToL2DeferTimeout)
	defer cancel()

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	// Bridge phase: bridge A then B (no claims yet).
	destA := freshRecipient(t)
	destB := freshRecipient(t)
	bridgeA := bridgeERC20L1ToL2NoClaim(ctx, t, env, l1Opts, destA, bridgeNightlyERC20Amount, "A")
	bridgeB := bridgeERC20L1ToL2NoClaim(ctx, t, env, l1Opts, destB, bridgeNightlyERC20Amount, "B")

	// Claim phase (REVERSED): claim B first, then A; assert each wrapped balance == amount.
	claimERC20L1ToL2(ctx, t, env, l2Opts, bridgeB)
	claimERC20L1ToL2(ctx, t, env, l2Opts, bridgeA)

	log.Infof("[TestBridgeNightly/Combo6] assets A+B bridged (deferred), claimed B then A (reversed)")
}

// ---------------------------------------------------------------------------------------------------
// Local composition helpers (UNEXPORTED, scoped to this file). These split the bridge phase from the
// claim phase using the public primitives + the P1/P3 wait helpers so the ordering combos can defer
// and reorder claims. They deliberately do NOT modify any shared helper.
// ---------------------------------------------------------------------------------------------------

// freshRecipient generates a fresh, gas-free recipient address (it never pays gas, so its wrapped-token
// balance increases by exactly the bridged amount). Mirrors the bats "$receiver" (a recipient distinct
// from the sender) and the testBridgeCoreTransferMessage fresh-key pattern.
func freshRecipient(t *testing.T) common.Address {
	t.Helper()
	key, err := crypto.GenerateKey()
	require.NoError(t, err, "generate fresh recipient key")
	return crypto.PubkeyToAddress(key.PublicKey)
}

// bridgeERC20L1ToL2NoClaim deploys a fresh L1-origin ERC20 (named per label, e.g. "A"/"B"), mints and
// approves the amount for the L1 bridge, bridges it as an asset L1->L2 to destination, and fully
// indexes it (bridge record + L1 Info Tree index + injected L2 leaf) WITHOUT claiming. The returned
// indexedERC20Bridge carries everything claimERC20L1ToL2 needs to issue a valid claim later. This is
// the deferred-ERC20 "bridge-now, valid-claim-later" composition that no shared helper provides
// (BridgeL1NoClaim is native-ETH only).
func bridgeERC20L1ToL2NoClaim(
	ctx context.Context, t *testing.T, env *envs.Env, l1Opts *bind.TransactOpts,
	destination common.Address, amount *big.Int, label string,
) indexedERC20Bridge {
	t.Helper()

	// Deploy a fresh L1-origin ERC20 (distinct name/symbol per label so each has its own wrapped token
	// and mapping).
	name := "L1NightlyToken" + label
	symbol := "L1NGT" + label
	l1TokenAddr, deployTx, l1Token, err := mintableerc20.DeployMintableerc20(l1Opts, env.Clients.L1, name, symbol)
	require.NoError(t, err, "deploy L1 ERC20 (%s)", label)
	deployReceipt, err := bind.WaitMined(ctx, env.Clients.L1, deployTx)
	require.NoError(t, err, "wait for L1 ERC20 deploy (%s)", label)
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, deployReceipt.Status, "L1 ERC20 deploy failed (%s)", label)
	log.Infof("[TestBridgeNightly] deployed L1 ERC20 %s at %s", label, l1TokenAddr.Hex())

	// Mint to sender and approve the L1 bridge.
	mintTx, err := l1Token.Mint(l1Opts, l1Opts.From, amount)
	require.NoError(t, err, "mint L1 ERC20 (%s)", label)
	mintReceipt, err := bind.WaitMined(ctx, env.Clients.L1, mintTx)
	require.NoError(t, err, "wait for L1 ERC20 mint (%s)", label)
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, mintReceipt.Status, "L1 ERC20 mint failed (%s)", label)

	l1BridgeAddr := l1BridgeAddress(t, env)
	approveTx, err := l1Token.Approve(l1Opts, l1BridgeAddr, amount)
	require.NoError(t, err, "approve L1 bridge for L1 ERC20 (%s)", label)
	approveReceipt, err := bind.WaitMined(ctx, env.Clients.L1, approveTx)
	require.NoError(t, err, "wait for L1 ERC20 approve (%s)", label)
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, approveReceipt.Status, "L1 ERC20 approve failed (%s)", label)

	// Bridge the asset L1->L2 to the gas-free recipient (NO claim here).
	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	require.NoError(t, err, "get L2 network ID")
	bridgeTx, err := env.L1.Contracts.Bridge.BridgeAsset(l1Opts, l2NetworkID, destination, amount, l1TokenAddr, true, nil)
	require.NoError(t, err, "BridgeAsset L1->L2 (%s)", label)
	bridgeReceipt, err := bind.WaitMined(ctx, env.Clients.L1, bridgeTx)
	require.NoError(t, err, "wait for ERC20 bridge tx (%s)", label)
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, bridgeReceipt.Status, "ERC20 bridge tx failed (%s)", label)

	// Fully index the deposit (reuses the P1 polling helpers; no claim yet).
	bridge := waitForBridgeByTxHash(ctx, t, env, 0, bridgeTx.Hash())
	depositCount := bridge.DepositCount
	l1InfoTreeIndex := waitForL1InfoTreeIndex(ctx, t, env, 0, depositCount)
	waitForInjectedL1InfoLeaf(ctx, t, env, l2NetworkID, l1InfoTreeIndex)

	return indexedERC20Bridge{
		l1TokenAddr:  l1TokenAddr,
		destination:  destination,
		amount:       amount,
		l2NetworkID:  l2NetworkID,
		depositCount: depositCount,
		l1InfoIndex:  l1InfoTreeIndex,
	}
}

// claimERC20L1ToL2 issues a VALID ClaimAsset on L2 for a previously indexed ERC20 bridge (submitted by
// the pooled L2 transactor, which pays the claim gas), then asserts the gas-free recipient's
// wrapped-token balance equals the bridged amount and that the token mapping origin == the deployed L1
// ERC20. This is the deferred claim half of the split flow.
func claimERC20L1ToL2(ctx context.Context, t *testing.T, env *envs.Env, l2Opts *bind.TransactOpts, b indexedERC20Bridge) {
	t.Helper()

	// Re-read the bridge record by deposit count to recover the exact claim parameters.
	bridge := waitForBridgeByDepositCount(ctx, t, env, 0, b.depositCount)

	claimProof, err := env.Clients.BridgeService.GetClaimProof(ctx, 0, b.l1InfoIndex, b.depositCount)
	require.NoError(t, err, "get claim proof (deposit=%d)", b.depositCount)
	require.NotNil(t, claimProof, "claim proof must not be nil")
	proofLocal, proofRollup := claimProofToContractProofs(claimProof)
	mainnetExitRoot := common.HexToHash(string(claimProof.L1InfoTreeLeaf.MainnetExitRoot))
	rollupExitRoot := common.HexToHash(string(claimProof.L1InfoTreeLeaf.RollupExitRoot))
	originTokenAddress := common.HexToAddress(string(bridge.OriginAddress))
	metadata := common.FromHex(bridge.Metadata)

	claimTx, err := env.L2.Contracts.L2Bridge.ClaimAsset(
		l2Opts, proofLocal, proofRollup, bridge.GlobalIndex, mainnetExitRoot, rollupExitRoot,
		bridge.OriginNetwork, originTokenAddress, bridge.DestinationNetwork, b.destination, b.amount, metadata)
	require.NoError(t, err, "ClaimAsset on L2 (token=%s)", b.l1TokenAddr.Hex())
	claimReceipt, err := bind.WaitMined(ctx, env.Clients.L2, claimTx)
	require.NoError(t, err, "wait for ClaimAsset tx (token=%s)", b.l1TokenAddr.Hex())
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, claimReceipt.Status, "ClaimAsset tx failed (token=%s)", b.l1TokenAddr.Hex())

	// Resolve the wrapped token (this also asserts the mapping origin == the deployed L1 ERC20, since
	// the lookup is keyed by origin token) and assert the gas-free recipient's balance == bridged amount.
	wrappedTokenAddr := waitForWrappedTokenAddress(ctx, t, env, int(b.l2NetworkID), b.l1TokenAddr)
	wrappedToken, err := mintableerc20.NewMintableerc20(wrappedTokenAddr, env.Clients.L2)
	require.NoError(t, err, "bind wrapped token on L2 (token=%s)", b.l1TokenAddr.Hex())
	bal, err := wrappedToken.BalanceOf(&bind.CallOpts{Context: ctx}, b.destination)
	require.NoError(t, err, "read wrapped-token balance (token=%s)", b.l1TokenAddr.Hex())
	require.Equal(t, 0, bal.Cmp(b.amount),
		"wrapped-token balance must equal the bridged amount: got %s want %s (origin=%s)",
		bal.String(), b.amount.String(), b.l1TokenAddr.Hex())
}

// bridgeMessageL1ToL2NoClaim bridges a message L1->L2 to destination (amount may be 0) and fully
// indexes it WITHOUT claiming, returning the data claimMessageL1ToL2 needs to claim it later. It
// mirrors the P1 bridgeMessageL1ToL2AndClaim body but splits the bridge from the claim so combos 1/2
// can defer the message claim relative to the asset.
func bridgeMessageL1ToL2NoClaim(
	ctx context.Context, t *testing.T, env *envs.Env, l1Opts *bind.TransactOpts,
	destination common.Address, amount *big.Int, metadata []byte,
) indexedMessageBridge {
	t.Helper()
	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	require.NoError(t, err, "get L2 network ID")

	l1Opts.Value = amount
	defer func() { l1Opts.Value = nil }()
	tx, err := env.L1.Contracts.Bridge.BridgeMessage(l1Opts, l2NetworkID, destination, true, metadata)
	require.NoError(t, err, "BridgeMessage on L1")
	receipt, err := bind.WaitMined(ctx, env.Clients.L1, tx)
	require.NoError(t, err, "wait for BridgeMessage tx")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "BridgeMessage tx failed")

	bridge := waitForBridgeByTxHash(ctx, t, env, 0, tx.Hash())
	depositCount := bridge.DepositCount
	l1InfoTreeIndex := waitForL1InfoTreeIndex(ctx, t, env, 0, depositCount)
	waitForInjectedL1InfoLeaf(ctx, t, env, l2NetworkID, l1InfoTreeIndex)

	return indexedMessageBridge{
		destination:  destination,
		amount:       amount,
		l2NetworkID:  l2NetworkID,
		depositCount: depositCount,
		l1InfoIndex:  l1InfoTreeIndex,
	}
}

// claimMessageL1ToL2 issues a VALID ClaimMessage on L2 for a previously indexed message bridge and
// asserts the claim receipt succeeded. The bats bridges amount=0, so a successful ClaimMessage receipt
// is the load-bearing assertion (if amount > 0 to a gas-free recipient, the caller could additionally
// assert a native +amount; that is not needed for these combos).
func claimMessageL1ToL2(ctx context.Context, t *testing.T, env *envs.Env, l2Opts *bind.TransactOpts, b indexedMessageBridge) {
	t.Helper()

	bridge := waitForBridgeByDepositCount(ctx, t, env, 0, b.depositCount)

	claimProof, err := env.Clients.BridgeService.GetClaimProof(ctx, 0, b.l1InfoIndex, b.depositCount)
	require.NoError(t, err, "get claim proof for message (deposit=%d)", b.depositCount)
	require.NotNil(t, claimProof, "claim proof must not be nil")
	proofLocal, proofRollup := claimProofToContractProofs(claimProof)

	claimTx, err := env.L2.Contracts.L2Bridge.ClaimMessage(
		l2Opts, proofLocal, proofRollup, bridge.GlobalIndex,
		common.HexToHash(string(claimProof.L1InfoTreeLeaf.MainnetExitRoot)),
		common.HexToHash(string(claimProof.L1InfoTreeLeaf.RollupExitRoot)),
		bridge.OriginNetwork, common.HexToAddress(string(bridge.OriginAddress)),
		bridge.DestinationNetwork, b.destination, b.amount, common.FromHex(bridge.Metadata))
	require.NoError(t, err, "ClaimMessage on L2")
	claimReceipt, err := bind.WaitMined(ctx, env.Clients.L2, claimTx)
	require.NoError(t, err, "wait for ClaimMessage tx")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, claimReceipt.Status, "ClaimMessage tx failed")
}

// waitForBridgeByDepositCount polls the bridge service until the bridge with the given deposit count on
// the given source networkID is indexed, returning its record. It is used by the deferred claim
// helpers to re-read the exact bridge record (global index, origin, metadata) at claim time. It reuses
// the same GetBridges polling pattern as waitForBridgeByTxHash but matches on deposit count, which is
// the stable key captured at bridge time.
func waitForBridgeByDepositCount(ctx context.Context, t *testing.T, env *envs.Env, networkID, depositCount uint32) *bridgetypes.BridgeResponse {
	t.Helper()
	var found *bridgetypes.BridgeResponse
	pollCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	err := pollWithBackoff(pollCtx, 2*time.Minute, backoffInitial, backoffMax, "bridge by deposit count", func() (bool, error) {
		pageSize := uint32(100)
		res, err := env.Clients.BridgeService.GetBridges(pollCtx, client.GetBridgesParams{NetworkID: networkID, PageSize: &pageSize})
		if err != nil {
			return false, nil //nolint:nilerr // transient; keep polling until timeout
		}
		if res == nil {
			return false, nil
		}
		for _, b := range res.Bridges {
			if b.DepositCount == depositCount {
				found = b
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err, "wait for bridge with deposit count %d in bridge service", depositCount)
	require.NotNil(t, found, "bridge with deposit count %d not found in bridge service", depositCount)
	return found
}
