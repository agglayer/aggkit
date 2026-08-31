package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/agglayer/aggkit/tools/remove_ger"
	"github.com/agglayer/aggkit/tree"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

const (
	keystorePassword = "pSnv6Dh5s9ahuzGzH9RoCDrKAMddaX3m"
	backoffInitial   = 500 * time.Millisecond
	backoffMax       = 10 * time.Second

	// l2GERStallObserveWindow bounds how long assertL2GERSyncStalledAt watches /sync-status to
	// confirm l2gersync has stopped advancing while the invalid GER is present. The Anvil L2
	// produces blocks frequently, so this window comfortably covers many L2 blocks and gives a clear
	// signal that the chain moves while l2gersync does not.
	l2GERStallObserveWindow = 20 * time.Second
	// l2GERCatchUpTimeout bounds how long waitForL2GERSyncCaughtUp waits for l2gersync to resume and
	// process past the removal block after ExecuteRecovery removes the invalid GER on-chain.
	l2GERCatchUpTimeout = 3 * time.Minute
)

// TestRemoveGER_NoProblematicClaims runs the No Problematic Claims
func TestRemoveGER_NoProblematicClaims(t *testing.T) {
	testRemoveGER_NoProblematicClaims(t)
}

// TestRemoveGER_CategoryA runs the Category A
func TestRemoveGER_CategoryA(t *testing.T) {
	testRemoveGER_CategoryA(t)
}

// TestRemoveGER_CategoryB1 runs the Category B.1
func TestRemoveGER_CategoryB1(t *testing.T) {
	testRemoveGER_CategoryB1(t)
}

// TestRemoveGER_CategoryB2 runs the Category B.2
func TestRemoveGER_CategoryB2(t *testing.T) {
	testRemoveGER_CategoryB2(t)
}

// TestGenerateInvalidGER tests the generate subcommand end-to-end:
// builds the CLI binary, runs generate, parses cast commands, executes them, and asserts results.
func TestGenerateInvalidGER(t *testing.T) {
	testGenerateInvalidGER(t)
}

// pollWithBackoff runs fn until it returns (true, nil) or ctx is done. Uses exponential backoff between attempts.
// If logLabel is non-empty, logs progress every 10 attempts so long-running waits are visible.
func pollWithBackoff(ctx context.Context, timeout time.Duration, initialInterval, maxInterval time.Duration, logLabel string, fn func() (done bool, err error)) error {
	deadline := time.Now().Add(timeout)
	start := time.Now()
	interval := initialInterval
	attempt := 0
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %v", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		attempt++
		done, err := fn()
		if err != nil {
			return err
		}
		if done {
			if logLabel != "" {
				log.Infof("[poll] done label=%s, attempt=%d", logLabel, attempt)
			}
			return nil
		}
		if logLabel != "" && attempt%10 == 0 {
			log.Infof("[poll] still waiting label=%s, attempt=%d, elapsed=%s", logLabel, attempt, time.Since(start))
		}
		time.Sleep(interval)
		if interval < maxInterval {
			interval *= 2
			if interval > maxInterval {
				interval = maxInterval
			}
		}
	}
}

// injectInvalidGER injects a fake/invalid GER into the L2 GER Manager using the aggoracle private key.
func injectInvalidGER(ctx context.Context, t *testing.T, env *envs.Env, gerHash common.Hash) *ethtypes.Receipt {
	t.Helper()
	opts, err := bind.NewKeyedTransactorWithChainID(env.Keys.AggOracle, env.L2.ChainID)
	require.NoError(t, err, "build aggoracle transactor")

	tx, err := env.L2.Contracts.GlobalExitRoot.InsertGlobalExitRoot(opts, gerHash)
	require.NoError(t, err, "InsertGlobalExitRoot")

	receipt, err := bind.WaitMined(ctx, env.Clients.L2, tx)
	require.NoError(t, err, "wait for InsertGlobalExitRoot receipt")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "InsertGlobalExitRoot tx failed")
	return receipt
}

// assertGERExistsOnL2 asserts that the GER is present in the L2 GER Manager (timestamp > 0).
func assertGERExistsOnL2(ctx context.Context, t *testing.T, env *envs.Env, gerHash common.Hash) {
	t.Helper()
	callOpts := &bind.CallOpts{Context: ctx}
	timestamp, err := env.L2.Contracts.GlobalExitRoot.GlobalExitRootMap(callOpts, gerHash)
	require.NoError(t, err, "GlobalExitRootMap")
	require.NotNil(t, timestamp, "GER timestamp should not be nil")
	require.True(t, timestamp.Sign() > 0, "GER should exist on L2 (timestamp > 0)")
}

// assertGERRemovedFromL2 asserts that the GER is no longer in the L2 GER Manager (timestamp == 0).
func assertGERRemovedFromL2(ctx context.Context, t *testing.T, env *envs.Env, gerHash common.Hash) {
	t.Helper()
	callOpts := &bind.CallOpts{Context: ctx}
	timestamp, err := env.L2.Contracts.GlobalExitRoot.GlobalExitRootMap(callOpts, gerHash)
	require.NoError(t, err, "GlobalExitRootMap")
	if timestamp == nil || timestamp.Sign() == 0 {
		return
	}
	require.Fail(t, "GER should be removed from L2", "timestamp=%s", timestamp.String())
}

// globalIndexToDepositCountAndOrigin decodes globalIndex into depositCount and originNetwork for L2 bridge IsClaimed(depositCount, originNetwork).
func globalIndexToDepositCountAndOrigin(globalIndex *big.Int) (depositCount, originNetwork uint32, err error) {
	mainnetFlag, rollupIndex, localExitRootIndex, err := bridgesync.DecodeGlobalIndex(globalIndex)
	if err != nil {
		return 0, 0, err
	}
	depositCount = localExitRootIndex
	if mainnetFlag {
		originNetwork = 0
	} else {
		originNetwork = rollupIndex + 1
	}
	return depositCount, originNetwork, nil
}

// assertClaimedOnL2 asserts that the L2 bridge has the claim for the given global index marked as claimed.
func assertClaimedOnL2(ctx context.Context, t *testing.T, env *envs.Env, globalIndex *big.Int) {
	t.Helper()
	depositCount, originNetwork, err := globalIndexToDepositCountAndOrigin(globalIndex)
	require.NoError(t, err, "decode global index")
	callOpts := &bind.CallOpts{Context: ctx}
	claimed, err := env.L2.Contracts.L2Bridge.IsClaimed(callOpts, depositCount, originNetwork)
	require.NoError(t, err, "IsClaimed")
	require.True(t, claimed, "claim should be marked claimed on L2 (global_index=%s)", globalIndex.String())
}

// assertClaimUnsetOnL2 asserts that the L2 bridge does not have the claim for the given global index marked as claimed.
func assertClaimUnsetOnL2(ctx context.Context, t *testing.T, env *envs.Env, globalIndex *big.Int) {
	t.Helper()
	depositCount, originNetwork, err := globalIndexToDepositCountAndOrigin(globalIndex)
	require.NoError(t, err, "decode global index")
	callOpts := &bind.CallOpts{Context: ctx}
	claimed, err := env.L2.Contracts.L2Bridge.IsClaimed(callOpts, depositCount, originNetwork)
	require.NoError(t, err, "IsClaimed")
	require.False(t, claimed, "claim should be unset on L2 (global_index=%s)", globalIndex.String())
}

// dummyClaimParams holds parameters for executing a dummy claim (Category A style).
// MainnetExitRoot and RollupExitRoot must hash to the injected GER (use l1infotreesync.CalculateGER to verify).
// ProofLocalExitRoot and ProofRollupExitRoot are the merkle proofs; if zero value, zero proofs are used.
type dummyClaimParams struct {
	GlobalIndex         *big.Int
	MainnetExitRoot     common.Hash
	RollupExitRoot      common.Hash
	OriginNetwork       uint32
	DestinationNetwork  uint32
	OriginAddress       common.Address
	DestinationAddress  common.Address
	Amount              *big.Int
	Metadata            []byte
	ProofLocalExitRoot  [32][32]byte // optional; zero value = use all-zero proof
	ProofRollupExitRoot [32][32]byte
}

// executeDummyClaimWithOpts runs the claim with the given transact opts. If opts is nil, uses pool L2 key (same as executeDummyClaim).
// Used by Category A test with AggOracle key to match bats test (latest-n-injected-ger.bats uses aggoracle_private_key for the claim).
func executeDummyClaimWithOpts(ctx context.Context, t *testing.T, env *envs.Env, params dummyClaimParams, opts *bind.TransactOpts) *ethtypes.Receipt {
	t.Helper()
	if opts == nil {
		l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
		require.NoError(t, err)
		defer env.Keys.L2Keys.Return(l2Key)
		opts = l2Opts
	}

	proofLocal := params.ProofLocalExitRoot
	proofRollup := params.ProofRollupExitRoot

	tx, err := env.L2.Contracts.L2Bridge.ClaimAsset(
		opts,
		proofLocal,
		proofRollup,
		params.GlobalIndex,
		params.MainnetExitRoot,
		params.RollupExitRoot,
		params.OriginNetwork,
		params.OriginAddress,
		params.DestinationNetwork,
		params.DestinationAddress,
		params.Amount,
		params.Metadata,
	)
	require.NoError(t, err, "ClaimAsset")

	receipt, err := bind.WaitMined(ctx, env.Clients.L2, tx)
	require.NoError(t, err, "wait for claim receipt")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "claim tx failed")
	return receipt
}

// Bats-style dummy claim data for Category A (from agglayer/e2e latest-n-injected-ger.bats).
// GER1 and exit roots + local merkle proof are hardcoded so the L2 bridge accepts the claim.
var (
	// batsGER1 is the injected invalid GER used in the bats test (hashes from mainnetExitRootBats + rollupExitRootBats).
	batsGER1 = common.HexToHash("0xeddc1e373486f80fe4ee28eecdb1cc92f0ec309c931712d546041817599e0bea")
	// mainnetExitRootBats and rollupExitRootBats are the exit roots that hash to batsGER1; contract verifies proofs against these.
	mainnetExitRootBats = common.HexToHash("0xb13e35a3b4655ae13db68adab3c173d468bfd60da795045be46809691cb6de1b")
	rollupExitRootBats  = common.Hash{} // all zeros
	// batsLocalExitRootProof is the 32-element merkle proof for the local exit root (from bats in_merkle_proof_ger1).
	batsLocalExitRootProof = mustParseBatsLocalProof()
	// batsGlobalIndexCategoryA is global index for dummy claim (mainnet, deposit_count=2); from bats in_global_index_ger1.
	batsGlobalIndexCategoryA = mustSetBigInt("18446744073709551618")
	batsAmountCategoryA      = new(big.Int).SetUint64(30000005400000000)
)

func mustSetBigInt(s string) *big.Int {
	z := new(big.Int)
	_, ok := z.SetString(s, 10)
	if !ok {
		panic("invalid big.Int string: " + s)
	}
	return z
}

func mustParseBatsLocalProof() (p [32][32]byte) {
	hexHashes := []string{
		"0x0000000000000000000000000000000000000000000000000000000000000000",
		"0x62c61f81d725c13627a7916a8091bb259a539b5117262fceef227b1d72b8d5df",
		"0xb4c11951957c6f8f642c4af61cd6b24640fec6dc7fc607ee8206a99e92410d30",
		"0x21ddb9a356815c3fac1026b6dec5df3124afbadb485c9ba5a3e3398a04b7ba85",
		"0xe58769b32a1beaf1ea27375a44095a0d1fb664ce2dd358e7fcbfb78c26a19344",
		"0x0eb01ebfc9ed27500cd4dfc979272d1f0913cc9f66540d7e8005811109e1cf2d",
		"0x887c22bd8750d34016ac3c66b5ff102dacdd73f6b014e710b51e8022af9a1968",
		"0xffd70157e48063fc33c97a050f7f640233bf646cc98d9524c6b92bcf3ab56f83",
		"0x9867cc5f7f196b93bae1e27e6320742445d290f2263827498b54fec539f756af",
		"0xcefad4e508c098b9a7e1d8feb19955fb02ba9675585078710969d3440f5054e0",
		"0xf9dc3e7fe016e050eff260334f18a5d4fe391d82092319f5964f2e2eb7c1c3a5",
		"0xf8b13a49e282f609c317a833fb8d976d11517c571d1221a265d25af778ecf892",
		"0x3490c6ceeb450aecdc82e28293031d10c7d73bf85e57bf041a97360aa2c5d99c",
		"0xc1df82d9c4b87413eae2ef048f94b4d3554cea73d92b0f7af96e0271c691e2bb",
		"0x5c67add7c6caf302256adedf7ab114da0acfe870d449a3a489f781d659e8becc",
		"0xda7bce9f4e8618b6bd2f4132ce798cdc7a60e7e1460a7299e3c6342a579626d2",
		"0x2733e50f526ec2fa19a22b31e8ed50f23cd1fdf94c9154ed3a7609a2f1ff981f",
		"0xe1d3b5c807b281e4683cc6d6315cf95b9ade8641defcb32372f1c126e398ef7a",
		"0x5a2dce0a8a7f68bb74560f8f71837c2c2ebbcbf7fffb42ae1896f13f7c7479a0",
		"0xb46a28b6f55540f89444f63de0378e3d121be09e06cc9ded1c20e65876d36aa0",
		"0xc65e9645644786b620e2dd2ad648ddfcbf4a7e5b1a3a4ecfe7f64667a3f0b7e2",
		"0xf4418588ed35a2458cffeb39b93d26f18d2ab13bdce6aee58e7b99359ec2dfd9",
		"0x5a9c16dc00d6ef18b7933a6f8dc65ccb55667138776f7dea101070dc8796e377",
		"0x4df84f40ae0c8229d0d6069e5c8f39a7c299677a09d367fc7b05e3bc380ee652",
		"0xcdc72595f74c7b1043d0e1ffbab734648c838dfb0527d971b602bc216c9619ef",
		"0x0abf5ac974a1ed57f4050aa510dd9c74f508277b39d7973bb2dfccc5eeb0618d",
		"0xb8cd74046ff337f0a7bf2c8e03e10f642c1886798d71806ab1e888d9e5ee87d0",
		"0x838c5655cb21c6cb83313b5a631175dff4963772cce9108188b34ac87c81c41e",
		"0x662ee4dd2dd7b2bc707961b1e646c4047669dcb6584f0d8d770daf5d7e7deb2e",
		"0x388ab20e2573d171a88108e79d820e98f26c0b84aa8b2f4aa4968dbb818ea322",
		"0x93237c50ba75ee485f4c22adf2f741400bdf8d6a9cc7df7ecae576221665d735",
		"0x8448818bb4ae4562849e949e17ac16e0be16688e156b5cf15e098c627c0056a9",
	}
	for i, h := range hexHashes {
		p[i] = common.HexToHash(h)
	}
	return p
}

// Bats exact destination for the dummy claim (in_dest_net_ger1=1, in_dest_addr_ger1). The contract
// hashes claim params to compute the leaf; we must match these exactly or the merkle proof fails.
const batsDestinationNetworkCategoryA = 1

var batsDestinationAddressCategoryA = common.HexToAddress("0x85dA99c8a7C2C95964c8EfD687E95E632Fc533D6")

// batsCategoryADummyClaimParams returns dummy claim params with exact bats values so the contract
// computes the same leaf and the hardcoded merkle proof verifies. Uses fixed destination network 1
// and bats destination address (not env-dependent).
func batsCategoryADummyClaimParams() dummyClaimParams {
	var proofRollup [32][32]byte
	return dummyClaimParams{
		GlobalIndex:         new(big.Int).Set(batsGlobalIndexCategoryA),
		MainnetExitRoot:     mainnetExitRootBats,
		RollupExitRoot:      rollupExitRootBats,
		OriginNetwork:       0,
		DestinationNetwork:  batsDestinationNetworkCategoryA,
		OriginAddress:       common.Address{},
		DestinationAddress:  batsDestinationAddressCategoryA,
		Amount:              new(big.Int).Set(batsAmountCategoryA),
		Metadata:            []byte{}, // 0x in bats; use empty slice so ABI encodes as 0x
		ProofLocalExitRoot:  batsLocalExitRootProof,
		ProofRollupExitRoot: proofRollup,
	}
}

// performBridgeL1NoClaim performs a real L1->L2 bridge with the given amount and waits for it to be
// indexed by the bridge service, but does NOT claim on L2.
func performBridgeL1NoClaim(ctx context.Context, t *testing.T, env *envs.Env, bridgeAmount *big.Int, label string) *bridgeResult {
	t.Helper()
	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L2Keys.Return(l2Key)
	// Sanity check addr has enough balance
	balance, err := env.Clients.L1.BalanceAt(ctx, l1Opts.From, nil)
	require.NoError(t, err)
	require.Equal(t, balance.Cmp(bridgeAmount), 1, "addr does not have enough balance")
	result, err := BridgeL1NoClaim(ctx, env, l1Opts, l2Opts, bridgeAmount, label)
	require.NoError(t, err)
	return result
}

// generateZeroHashesForProof builds zero hashes for merkle proof (same logic as tree package for single-leaf tree).
// Index 0 = empty leaf, index i = hash(zeroHashes[i-1], zeroHashes[i-1]).
func generateZeroHashesForProof(height uint8) []common.Hash {
	zeroHashes := []common.Hash{{}}
	for i := 1; i <= int(height); i++ {
		next := crypto.Keccak256Hash(zeroHashes[i-1][:], zeroHashes[i-1][:])
		zeroHashes = append(zeroHashes, next)
	}
	return zeroHashes
}

// b1ClaimProof holds the outputs of buildB1ClaimProof for use in executeB1Claim.
type b1ClaimProof struct {
	InvalidGER      common.Hash
	MainnetExitRoot common.Hash
	RollupExitRoot  common.Hash
	ProofLocal      [32][32]byte
	ProofRollup     [32][32]byte
}

// buildB1ClaimProof builds (invalid GER, mainnet/rollup exit roots, proofs) so that a claim with the real bridge's
// leaf data will verify on L2 under the invalid GER. Uses bridgesync.Bridge.Hash() for the leaf and a single-leaf
// merkle tree with zero-hash siblings.
func buildB1ClaimProof(t *testing.T, bridge *types.BridgeResponse, depositCount uint32) *b1ClaimProof {
	t.Helper()
	b := &bridgesync.Bridge{
		LeafType:           bridge.LeafType,
		OriginNetwork:      bridge.OriginNetwork,
		OriginAddress:      common.HexToAddress(string(bridge.OriginAddress)),
		DestinationNetwork: bridge.DestinationNetwork,
		DestinationAddress: common.HexToAddress(string(bridge.DestinationAddress)),
		Amount:             bridge.Amount.ToBigInt(),
		Metadata:           common.Hex2Bytes(bridge.Metadata),
	}
	leafHash := b.Hash()
	zeroHashes := generateZeroHashesForProof(treetypes.DefaultHeight)
	var proof treetypes.Proof
	for h := uint8(0); h < treetypes.DefaultHeight; h++ {
		proof[h] = zeroHashes[h]
	}
	mainnetExitRoot := tree.CalculateRoot(leafHash, proof, depositCount)
	rollupExitRoot := common.Hash{}
	invalidGER := l1infotreesync.CalculateGER(mainnetExitRoot, rollupExitRoot)
	var proofLocal, proofRollup [32][32]byte
	for i := 0; i < 32; i++ {
		proofLocal[i] = proof[i]
		proofRollup[i] = zeroHashes[i]
	}
	return &b1ClaimProof{
		InvalidGER:      invalidGER,
		MainnetExitRoot: mainnetExitRoot,
		RollupExitRoot:  rollupExitRoot,
		ProofLocal:      proofLocal,
		ProofRollup:     proofRollup,
	}
}

// executeB1Claim runs ClaimAsset on the L2 bridge with the real bridge data but the given (invalid) exit roots and proofs.
func executeB1Claim(ctx context.Context, t *testing.T, env *envs.Env, result *bridgeResult, proof *b1ClaimProof) *ethtypes.Receipt {
	t.Helper()
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L2Keys.Return(l2Key)

	bridge := result.Bridge
	originTokenAddress := common.HexToAddress(string(bridge.OriginAddress))
	metadata := common.Hex2Bytes(bridge.Metadata)
	amount := bridge.Amount.ToBigInt()

	tx, err := env.L2.Contracts.L2Bridge.ClaimAsset(
		l2Opts,
		proof.ProofLocal,
		proof.ProofRollup,
		result.GlobalIndex,
		proof.MainnetExitRoot,
		proof.RollupExitRoot,
		bridge.OriginNetwork,
		originTokenAddress,
		bridge.DestinationNetwork,
		result.DestinationAddr,
		amount,
		metadata,
	)
	require.NoError(t, err, "ClaimAsset (B.1)")
	receipt, err := bind.WaitMined(ctx, env.Clients.L2, tx)
	require.NoError(t, err, "wait for B.1 claim receipt")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "B.1 claim tx failed")
	return receipt
}

// waitForGEROnBridgeService polls until the bridge service reports a remove-GER event for the given GER (e.g. after recovery).
func waitForGEROnBridgeService(ctx context.Context, t *testing.T, env *envs.Env, gerHash common.Hash, timeout time.Duration) {
	t.Helper()
	gerHex := gerHash.Hex()
	limit := 50
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := pollWithBackoff(pollCtx, timeout, backoffInitial, backoffMax, "GER remove event on bridge service", func() (bool, error) {
		resp, err := env.Clients.BridgeService.GetRemoveGEREvents(pollCtx, client.GetRemoveGEREventsParams{
			GlobalExitRoot: &gerHex,
			Limit:          &limit,
		})
		if err != nil {
			return false, err
		}
		return resp != nil && len(resp.RemoveGEREvents) > 0, nil
	})
	require.NoError(t, err, "wait for GER remove event on bridge service")
}

// waitForClaimInBridgeL2DBByGER polls the bridge service until at least one claim exists for the given GER,
// using the same GetClaimsByGER as the remove_ger tool so the test and tool cannot diverge on query/visibility.
func waitForClaimInBridgeL2DBByGER(ctx context.Context, t *testing.T, bsc *client.Client, l2NetworkID uint32, gerHash common.Hash, timeout time.Duration) {
	t.Helper()
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := pollWithBackoff(pollCtx, timeout, backoffInitial, backoffMax, "B1: claim by GER on bridge service", func() (bool, error) {
		claims, err := remove_ger.GetClaimsByGER(pollCtx, bsc, l2NetworkID, gerHash)
		if err != nil {
			return false, err
		}
		return len(claims) >= 1, nil
	})
	require.NoError(t, err, "wait for claim by GER %s on bridge service", gerHash.Hex())
}

// waitForClaimOnBridgeService polls until the bridge service returns at least one claim for the given global index.
func waitForClaimOnBridgeService(ctx context.Context, t *testing.T, env *envs.Env, globalIndex *big.Int, timeout time.Duration) {
	t.Helper()
	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	require.NoError(t, err)
	pageSize := uint32(100)
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err = pollWithBackoff(pollCtx, timeout, backoffInitial, backoffMax, "B1: claim on bridge service", func() (bool, error) {
		resp, err := env.Clients.BridgeService.GetClaims(pollCtx, client.GetClaimsParams{
			NetworkID:   l2NetworkID,
			PageSize:    &pageSize,
			GlobalIndex: globalIndex,
		})
		if err != nil {
			return false, err
		}
		return resp != nil && len(resp.Claims) > 0, nil
	})
	require.NoError(t, err, "wait for claim on bridge service")
}

// assertL2GERSyncStalledAt polls the bridgeservice /sync-status endpoint over observeWindow and
// asserts that l2gersync's LastProcessedBlock (in L2GERInfo) never reaches insertBlock -- the block
// containing the invalid GER insert event -- while the L2 chain head keeps advancing over the same
// window. This proves l2gersync is genuinely stuck on the invalid GER (per S2 design §7), as opposed
// to merely running slow: the chain moves but the syncer's last-processed-block does not.
func assertL2GERSyncStalledAt(ctx context.Context, t *testing.T, env *envs.Env, insertBlock uint64, observeWindow time.Duration) {
	t.Helper()

	startL2Block, err := env.Clients.L2.BlockNumber(ctx)
	require.NoError(t, err, "read initial L2 chain head")

	pollCtx, cancel := context.WithTimeout(ctx, observeWindow)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastSeenProcessed uint64
	sawL2GERInfo := false
	for {
		select {
		case <-pollCtx.Done():
			require.True(t, sawL2GERInfo, "/sync-status never returned l2_ger_info during the stall observation window")
			endL2Block, err := env.Clients.L2.BlockNumber(ctx)
			require.NoError(t, err, "read final L2 chain head")
			require.Greater(t, endL2Block, startL2Block,
				"L2 chain head must keep advancing while l2gersync is stalled (start=%d, end=%d)", startL2Block, endL2Block)
			log.Infof("[assertL2GERSyncStalledAt] confirmed stalled: lastProcessed=%d insertBlock=%d l2Head=%d->%d",
				lastSeenProcessed, insertBlock, startL2Block, endL2Block)
			return
		case <-ticker.C:
			resp, err := env.Clients.BridgeService.GetSyncStatus(pollCtx)
			if err != nil {
				// Transient polling error (e.g. brief connection hiccup); keep observing rather than
				// failing the whole assertion on a single blip.
				log.Infof("[assertL2GERSyncStalledAt] transient /sync-status error, retrying: %v", err)
				continue
			}
			require.NotNil(t, resp.L2GERInfo, "/sync-status must expose l2_ger_info for an L2 chain running l2gersync")
			sawL2GERInfo = true
			lastSeenProcessed = resp.L2GERInfo.LastProcessedBlock
			require.Less(t, lastSeenProcessed, insertBlock,
				"l2gersync must stay stalled below the invalid-GER insert block (insertBlock=%d, lastProcessed=%d)",
				insertBlock, lastSeenProcessed)
		}
	}
}

// waitForL2GERSyncCaughtUp polls /sync-status until l2gersync's LastProcessedBlock (in L2GERInfo)
// reaches at least removalBlock, within timeout. This is the core proof that l2gersync resumed and
// processed past the removal event after ExecuteRecovery removed the invalid GER on-chain (S2 design
// §7 "unstuck" sequence, step 1).
func waitForL2GERSyncCaughtUp(ctx context.Context, t *testing.T, env *envs.Env, removalBlock uint64, timeout time.Duration) {
	t.Helper()
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastSeen uint64
	err := pollWithBackoff(pollCtx, timeout, backoffInitial, backoffMax, "l2gersync caught up past removal block",
		func() (bool, error) {
			resp, err := env.Clients.BridgeService.GetSyncStatus(pollCtx)
			if err != nil {
				// Transient polling error; keep retrying until timeout rather than failing immediately.
				return false, nil
			}
			if resp.L2GERInfo == nil {
				return false, nil
			}
			lastSeen = resp.L2GERInfo.LastProcessedBlock
			return lastSeen >= removalBlock, nil
		})
	require.NoError(t, err, "l2gersync did not catch up past removal block %d (last seen last_processed_block=%d)",
		removalBlock, lastSeen)
}

// assertL2GERSyncStillAlive exercises a fresh, valid L1->L2 bridge end-to-end -- including GER
// injection served via bridgeservice /injected-l1-info-leaf, asserted inside BridgeL1ToL2 -- after
// remove-GER recovery. This proves l2gersync truly returned to normal operation past the removal
// (not merely unblocked once), per S2 design §7 "unstuck" sequence, step 4.
func assertL2GERSyncStillAlive(ctx context.Context, t *testing.T, env *envs.Env) {
	t.Helper()
	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L2Keys.Return(l2Key)
	require.NoError(t, BridgeL1ToL2(ctx, env, l1Opts, l2Opts),
		"still-alive check: valid L1->L2 bridge (incl. GER injection served via /injected-l1-info-leaf) must succeed after recovery")
}

// gerHashInLogsRegex matches a 32-byte hex hash (0x + 64 hex chars) as in runbook error messages.
var gerHashInLogsRegex = regexp.MustCompile(`0x[0-9a-fA-F]{64}`)

// runbookErrorSubstrings are substrings that identify runbook-aligned invalid GER error lines (AggSender or L2 GER Sync).
var runbookErrorSubstrings = []string{
	"error getting proof for GER:",
	"error getting L1 Info tree merkle proof for GER:",
	"error getting info by global exit root",
	"error sending certificate",
	"certificate validation failed",
	"failed to fetch l1 info tree for global exit root",
	"not found in L1 contract globalExitRootMap",
	"GER lookup for",
	"failed in L1 contract",
}

// detectInvalidGERFromAggkitLogs polls aggkit container logs for runbook error patterns that include a GER hash.
// When expectedGER is non-nil, it returns only when that specific GER is found in a runbook error line (so later
// tests that run in the same env don't pick up a GER from an earlier test). When expectedGER is nil, returns the
// first GER found. Used so the test obtains the GER only via log detection (runbook-aligned).
func detectInvalidGERFromAggkitLogs(ctx context.Context, t *testing.T, timeout time.Duration, expectedGER *common.Hash) (common.Hash, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	start := time.Now()
	interval := 3 * time.Second
	pollCount := 0
	for {
		if time.Now().After(deadline) {
			return common.Hash{}, fmt.Errorf("timeout after %v waiting for invalid GER in aggkit logs", timeout)
		}
		select {
		case <-ctx.Done():
			return common.Hash{}, ctx.Err()
		default:
		}
		pollCount++
		if pollCount%10 == 0 {
			log.Info("polling aggkit logs for invalid GER", "attempt", pollCount, "elapsed", time.Since(start))
		}
		out, err := testEnv.DockerComposeLogs(ctx, "--no-log-prefix", "aggkit-001")
		if err != nil {
			return common.Hash{}, err
		}
		logText := string(out)
		for _, line := range strings.Split(logText, "\n") {
			hasError := false
			for _, sub := range runbookErrorSubstrings {
				if strings.Contains(line, sub) {
					hasError = true
					break
				}
			}
			if !hasError {
				continue
			}
			for _, match := range gerHashInLogsRegex.FindAllString(line, -1) {
				h := common.HexToHash(match)
				if h != (common.Hash{}) {
					if expectedGER != nil && h != *expectedGER {
						continue
					}
					log.Info("invalid GER detected in logs", "ger", h.Hex(), "attempt", pollCount)
					return h, nil
				}
			}
		}
		time.Sleep(interval)
	}
}

// summaryForToolConfig is a minimal struct for reading summary.json to build the remove_ger tool config.
type summaryForToolConfig struct {
	Networks struct {
		L1 struct {
			Services struct {
				Geth struct {
					HTTPRpc struct {
						Internal string `json:"internal"`
						External string `json:"external"`
					} `json:"http_rpc"`
				} `json:"geth"`
			} `json:"services"`
		} `json:"l1"`
		L2Networks map[string]struct {
			Contracts struct {
				L2Bridge       string `json:"l2_bridge"`
				GlobalExitRoot string `json:"global_exit_root"`
			} `json:"contracts"`
			Services struct {
				Aggkit struct {
					BridgeService struct {
						External string `json:"external"`
					} `json:"rest_api"`
				} `json:"aggkit"`
				OpGeth struct {
					HTTPRpc struct {
						Internal string `json:"internal"`
						External string `json:"external"`
					} `json:"http_rpc"`
				} `json:"op-geth"`
			} `json:"services"`
		} `json:"l2_networks"`
	} `json:"networks"`
}

// prepareToolConfig creates a temp config file with the base aggkit config plus [RemoveGER] section for the tool.
// If pathRWDataOverride is non-empty, it is used as PathRWData (e.g. envs.AggkitE2EHostDataDir for the host-mounted E2E data dir); otherwise envDir/aggkit_data_001 is used.
// If configDir is non-empty, the config file is written there (so it persists across tests); otherwise t.TempDir() is used.
func prepareToolConfig(t *testing.T, configDir string) string {
	t.Helper()
	summaryPath := filepath.Join(testEnv.EnvDir, "summary.json")
	summaryData, err := os.ReadFile(summaryPath)
	require.NoError(t, err)

	var summary summaryForToolConfig
	require.NoError(t, json.Unmarshal(summaryData, &summary))

	l2Network, ok := summary.Networks.L2Networks["001"]
	require.True(t, ok, "L2 network 001 not found")

	bridgeServiceURL := l2Network.Services.Aggkit.BridgeService.External
	l1URL := summary.Networks.L1.Services.Geth.HTTPRpc.External
	l2URL := l2Network.Services.OpGeth.HTTPRpc.External
	require.NotEmpty(t, summary.Networks.L1.Services.Geth.HTTPRpc.Internal, "L1 internal RPC URL must be present")
	require.NotEmpty(t, l2Network.Services.OpGeth.HTTPRpc.Internal, "L2 internal RPC URL must be present")
	sovereignAdminKeyPath := filepath.Join(testEnv.EnvDir, "config", "001", "sovereignadmin.keystore")

	originalCfg := filepath.Join(testEnv.EnvDir, "config", "001", "aggkit-config.toml")
	content, err := os.ReadFile(originalCfg)
	require.NoError(t, err)

	// Patch the environment's internal URLs so the tool (running on the host) can reach L1/L2.
	content = []byte(strings.ReplaceAll(string(content), summary.Networks.L1.Services.Geth.HTTPRpc.Internal, l1URL))
	content = []byte(strings.ReplaceAll(string(content), l2Network.Services.OpGeth.HTTPRpc.Internal, l2URL))

	appendSection := fmt.Sprintf(`

[RemoveGER]
BridgeServiceURL = "%s"
L2NetworkID = %d

SovereignAdminKey = { Method = "local", Path = "%s", Password = "%s" }
`,
		bridgeServiceURL,
		testEnv.L2.NetworkID,
		sovereignAdminKeyPath,
		keystorePassword,
	)

	var baseDir string
	if configDir != "" {
		baseDir = configDir
	} else {
		baseDir = t.TempDir()
	}
	tmpFile := filepath.Join(baseDir, "aggkit-config-test.toml")
	err = os.WriteFile(tmpFile, append(content, []byte(appendSection)...), 0o600)
	require.NoError(t, err)
	return tmpFile
}

// buildToolCLIContext creates a *cli.Context with --cfg set to configPath so remove_ger.LoadConfig can be used from tests.
func buildToolCLIContext(t *testing.T, configPath string) *cli.Context {
	t.Helper()
	app := cli.NewApp()
	app.Flags = []cli.Flag{
		&cli.StringSliceFlag{Name: "cfg", Aliases: []string{"c"}},
		&cli.StringFlag{Name: "ger"},
		&cli.BoolFlag{Name: "yes"},
		&cli.BoolFlag{Name: "force"},
	}
	set := flag.NewFlagSet("", flag.ContinueOnError)
	for _, f := range app.Flags {
		require.NoError(t, f.Apply(set))
	}
	require.NoError(t, set.Parse([]string{"--cfg", configPath}))
	return cli.NewContext(app, set, nil)
}

var (
	toolConfigPath     string
	toolConfigPathOnce sync.Once
)

// getPreparedToolConfigPath returns the path to the remove_ger tool config file, building it once per test run.
// Uses a process-scoped temp dir so the file survives when the first test's t.TempDir() is cleaned up.
func getPreparedToolConfigPath(t *testing.T) string {
	t.Helper()
	toolConfigPathOnce.Do(func() {
		toolConfigDir, err := os.MkdirTemp("", "aggkit-e2e-removeger-config-")
		require.NoError(t, err)
		toolConfigPath = prepareToolConfig(t, toolConfigDir)
	})
	return toolConfigPath
}

// loadToolConfig loads the remove_ger config using the shared prepared config path.
func loadToolConfig(t *testing.T) *remove_ger.Config {
	t.Helper()
	configPath := getPreparedToolConfigPath(t)
	cliCtx := buildToolCLIContext(t, configPath)
	cfg, err := remove_ger.LoadConfig(cliCtx)
	require.NoError(t, err)
	return cfg
}

// testRemoveGER_NoProblematicClaims runs the No Claims scenario: inject invalid GER, detect from logs, diagnose NoClaims, recover, assert health.
func testRemoveGER_NoProblematicClaims(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	// 20min: base 15min budget + time for the end-of-scenario still-alive check (a full L1->L2 bridge).
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// Best-effort: restore bridge if left in emergency state (e.g. on test failure)
	defer func() {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer restoreCancel()
		isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: restoreCtx})
		if err != nil || !isEmergency {
			return
		}
		// Attempt deactivateEmergencyState using sovereign admin (best-effort, ignore errors)
		opts, err := bind.NewKeyedTransactorWithChainID(env.Keys.SovereignAdmin, env.L2.ChainID)
		if err != nil {
			return
		}
		_, _ = env.L2.Contracts.L2Bridge.DeactivateEmergencyState(opts)
	}()

	// --- Setup: inject invalid GER ---
	var gerBytes [32]byte
	_, err := rand.Read(gerBytes[:])
	require.NoError(t, err)
	injectedGER := common.Hash(gerBytes)

	require.NoError(t, env.StopAggkit(ctx))
	injectReceipt := injectInvalidGER(ctx, t, env, injectedGER)
	require.NoError(t, env.StartAggkit(ctx))

	assertGERExistsOnL2(ctx, t, env, injectedGER)

	// --- Assert l2gersync is stalled and noisy while the invalid GER is present (S2 design §7) ---
	assertL2GERSyncStalledAt(ctx, t, env, injectReceipt.BlockNumber.Uint64(), l2GERStallObserveWindow)

	// --- GER detection (runbook-aligned): obtain GER only from logs ---
	// Pass &injectedGER so we only accept this test's GER; when run in suite, logs contain
	// GERs from earlier tests (CategoryA, CategoryB1) and nil would return the first one found.
	// This is a complementary signal to the sync-status stall assertion above (also discovers the GER hash).
	detectedGER, err := detectInvalidGERFromAggkitLogs(ctx, t, 3*time.Minute, &injectedGER)
	require.NoError(t, err)
	require.NotEqual(t, common.Hash{}, detectedGER, "detected GER must not be zero")
	require.Equal(t, injectedGER, detectedGER, "detected GER must match injected (sanity check)")

	// --- Tool: diagnosis ---
	cfg := loadToolConfig(t)
	envCtx, envCancel := context.WithTimeout(ctx, 30*time.Second)
	defer envCancel()
	toolEnv, err := remove_ger.SetupEnv(envCtx, cfg)
	require.NoError(t, err)
	defer toolEnv.Close()

	diagnosis, err := remove_ger.Diagnose(ctx, toolEnv, detectedGER, false)
	require.NoError(t, err)
	require.Equal(t, remove_ger.ScenarioNoClaims, diagnosis.Scenario)
	require.False(t, diagnosis.GERExistsOnL1, "GER must be confirmed invalid on L1")

	// --- Tool: recovery ---
	recoveryTimeout := 10 * time.Minute
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, recoveryTimeout)
	defer recoveryCancel()

	recovery, err := remove_ger.ExecuteRecovery(recoveryCtx, cfg, toolEnv, diagnosis)
	require.NoError(t, err)

	// removalBlock is the exact block of the removeGlobalExitRoots tx (from ExecuteRecovery's receipt).
	// l2gersync only needs to process this fixed past block to observe the removal and unstick; targeting
	// the live L2 head instead would chase a moving target several blocks ahead.
	removalBlock := recovery.RemovalBlock
	require.NotZero(t, removalBlock, "recovery must report the removeGlobalExitRoots block")
	waitForL2GERSyncCaughtUp(ctx, t, env, removalBlock, l2GERCatchUpTimeout)

	assertGERRemovedFromL2(ctx, t, env, detectedGER)
	waitForGEROnBridgeService(ctx, t, env, detectedGER, 2*time.Minute)

	isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: ctx})
	require.NoError(t, err)
	require.False(t, isEmergency, "bridge must not be in emergency state after recovery")

	// // --- Post-recovery ---
	// assertNetworkHealthy(ctx, t, env)

	// --- End-of-scenario still-alive check: l2gersync must serve fresh state past the removal ---
	assertL2GERSyncStillAlive(ctx, t, env)
}

// testRemoveGER_CategoryA runs the Category A scenario: invalid GER + dummy claim (no bridge on L1), detect GER from logs, diagnose Category A, recover (unset claim), assert health.
func testRemoveGER_CategoryA(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	// 20min: base 15min budget + time for the end-of-scenario still-alive check (a full L1->L2 bridge).
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	defer func() {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer restoreCancel()
		isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: restoreCtx})
		if err != nil || !isEmergency {
			return
		}
		opts, err := bind.NewKeyedTransactorWithChainID(env.Keys.SovereignAdmin, env.L2.ChainID)
		if err != nil {
			return
		}
		_, _ = env.L2.Contracts.L2Bridge.DeactivateEmergencyState(opts)
	}()

	// --- Setup: inject bats-style GER and execute bats-style dummy claim ---
	// Crafted like agglayer/e2e latest-n-injected-ger.bats: fixed GER (batsGER1), exit roots that hash to it,
	// hardcoded local merkle proof (batsLocalExitRootProof), and all-zero rollup proof. The claim is sent with
	// AggOracle key to match the bats test. If ClaimAsset reverts, the bats proof may be for a different
	// CDK/bridge deployment and may need to be regenerated for a newer snapshot.
	injectedGER := batsGER1
	require.Equal(t, injectedGER, l1infotreesync.CalculateGER(mainnetExitRootBats, rollupExitRootBats),
		"bats GER must equal keccak256(mainnetExitRootBats, rollupExitRootBats)")

	require.NoError(t, env.StopAggkit(ctx))
	injectReceipt := injectInvalidGER(ctx, t, env, injectedGER)
	assertGERExistsOnL2(ctx, t, env, injectedGER)
	globalIndex := batsGlobalIndexCategoryA
	params := batsCategoryADummyClaimParams() // exact bats params so leaf hashes match and proof verifies
	// Bats test uses aggoracle key to send the dummy claim tx (latest-n-injected-ger.bats).
	aggoracleOpts, err := bind.NewKeyedTransactorWithChainID(env.Keys.AggOracle, env.L2.ChainID)
	require.NoError(t, err)
	executeDummyClaimWithOpts(ctx, t, env, params, aggoracleOpts)
	require.NoError(t, env.StartAggkit(ctx))

	assertClaimedOnL2(ctx, t, env, globalIndex)

	// --- Assert l2gersync is stalled and noisy while the invalid GER is present (S2 design §7) ---
	assertL2GERSyncStalledAt(ctx, t, env, injectReceipt.BlockNumber.Uint64(), l2GERStallObserveWindow)

	// Wait for bridge L2 sync to index the claim; otherwise diagnosis sees no claims.
	waitForClaimOnBridgeService(ctx, t, env, globalIndex, 2*time.Minute)

	// --- GER detection (runbook-aligned); complementary signal to the sync-status stall assertion above ---
	detectedGER, err := detectInvalidGERFromAggkitLogs(ctx, t, 3*time.Minute, &injectedGER)
	require.NoError(t, err)
	require.NotEqual(t, common.Hash{}, detectedGER)
	require.Equal(t, injectedGER, detectedGER, "detected GER must match injected (sanity check)")

	// --- Tool: diagnosis ---
	cfg := loadToolConfig(t)
	envCtx, envCancel := context.WithTimeout(ctx, 30*time.Second)
	defer envCancel()
	toolEnv, err := remove_ger.SetupEnv(envCtx, cfg)
	require.NoError(t, err)
	defer toolEnv.Close()

	diagnosis, err := remove_ger.Diagnose(ctx, toolEnv, detectedGER, false)
	require.NoError(t, err)
	require.Equal(t, remove_ger.ScenarioCategoryA, diagnosis.Scenario)
	require.Len(t, diagnosis.Claims, 1)
	require.Equal(t, remove_ger.ScenarioCategoryA, diagnosis.Claims[0].Category)
	require.Equal(t, 0, globalIndex.Cmp(diagnosis.Claims[0].GlobalIndex), "diagnosis claim global_index must match dummy claim")

	// --- Tool: recovery ---
	recoveryTimeout := 10 * time.Minute
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, recoveryTimeout)
	defer recoveryCancel()

	recovery, err := remove_ger.ExecuteRecovery(recoveryCtx, cfg, toolEnv, diagnosis)
	require.NoError(t, err)

	// removalBlock is the exact block of the removeGlobalExitRoots tx (from ExecuteRecovery's receipt).
	// l2gersync only needs to process this fixed past block to observe the removal and unstick; targeting
	// the live L2 head instead would chase a moving target several blocks ahead.
	removalBlock := recovery.RemovalBlock
	require.NotZero(t, removalBlock, "recovery must report the removeGlobalExitRoots block")
	waitForL2GERSyncCaughtUp(ctx, t, env, removalBlock, l2GERCatchUpTimeout)

	assertGERRemovedFromL2(ctx, t, env, detectedGER)
	waitForGEROnBridgeService(ctx, t, env, detectedGER, 2*time.Minute)

	isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: ctx})
	require.NoError(t, err)
	require.False(t, isEmergency, "bridge must not be in emergency state after recovery")

	// --- Post-recovery: unset claim remains unset, network healthy ---
	assertClaimUnsetOnL2(ctx, t, env, globalIndex)
	// assertNetworkHealthy(ctx, t, env)

	// --- End-of-scenario still-alive check: l2gersync must serve fresh state past the removal ---
	assertL2GERSyncStillAlive(ctx, t, env)
}

// testRemoveGER_CategoryB1 runs the Category B.1 scenario: real bridge, inject invalid GER, claim with invalid GER but correct bridge data, detect from logs, diagnose B.1, recover (remove GER + force emit), assert health.
func testRemoveGER_CategoryB1(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	// B.1 needs more time: real bridge + claim + DB wait + GER detection (up to 6 min) + recovery +
	// end-of-scenario still-alive check (a full L1->L2 bridge).
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	defer func() {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer restoreCancel()
		isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: restoreCtx})
		if err != nil || !isEmergency {
			return
		}
		opts, err := bind.NewKeyedTransactorWithChainID(env.Keys.SovereignAdmin, env.L2.ChainID)
		if err != nil {
			return
		}
		_, _ = env.L2.Contracts.L2Bridge.DeactivateEmergencyState(opts)
	}()

	// --- Setup: real bridge (no claim), then inject invalid GER and claim with invalid GER but correct bridge data ---
	log.Info("[B1] step: performBridgeL1NoClaim")
	bridgeResult := performBridgeL1NoClaim(ctx, t, env, big.NewInt(100000000000000), "B1")
	log.Info("[B1] step: buildB1ClaimProof, injectInvalidGER, executeB1Claim")
	proof := buildB1ClaimProof(t, bridgeResult.Bridge, bridgeResult.DepositCount)
	require.NoError(t, env.StopAggkit(ctx))
	injectReceipt := injectInvalidGER(ctx, t, env, proof.InvalidGER)
	require.NoError(t, env.StartAggkit(ctx))
	assertGERExistsOnL2(ctx, t, env, proof.InvalidGER)

	// --- Assert l2gersync is stalled and noisy while the invalid GER is present (S2 design §7) ---
	assertL2GERSyncStalledAt(ctx, t, env, injectReceipt.BlockNumber.Uint64(), l2GERStallObserveWindow)

	executeB1Claim(ctx, t, env, bridgeResult, proof)
	assertClaimedOnL2(ctx, t, env, bridgeResult.GlobalIndex)

	// Wait for bridge L2 sync to index the claim before diagnosis (tool reads same SQLite as aggkit)
	log.Info("[B1] step: waitForClaimOnBridgeService (up to 2m)")
	waitForClaimOnBridgeService(ctx, t, env, bridgeResult.GlobalIndex, 2*time.Minute)

	// Wait for the claim to appear via bridge service under our invalid GER (same query as tool).
	log.Info("[B1] step: waitForClaimInBridgeL2DBByGER (up to 2m)", "ger", proof.InvalidGER.Hex())
	waitForClaimInBridgeL2DBByGER(ctx, t, env.Clients.BridgeService, env.L2.NetworkID, proof.InvalidGER, 2*time.Minute)

	// --- GER detection (runbook-aligned); complementary signal to the sync-status stall assertion above ---
	log.Info("[B1] step: detectInvalidGERFromAggkitLogs (up to 6m)")
	// B.1: wait for our injected GER to appear in logs (l2gersync logs when it processes the InsertGER block and fails to fetch L1 info)
	detectedGER, err := detectInvalidGERFromAggkitLogs(ctx, t, 6*time.Minute, &proof.InvalidGER)
	require.NoError(t, err)
	require.NotEqual(t, common.Hash{}, detectedGER)
	require.Equal(t, proof.InvalidGER, detectedGER, "detected GER must match injected (sanity check)")

	// Assert claim present via bridge service using the exact same query as the tool (GetClaimsByGER).
	log.Info("[B1] step: assert claim via bridge service, SetupEnv, Diagnose")
	cfg := loadToolConfig(t)
	claimsForGER, err := remove_ger.GetClaimsByGER(ctx, env.Clients.BridgeService, env.L2.NetworkID, detectedGER)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(claimsForGER), 1, "claim must be visible on bridge service when starting diagnosis")

	// --- Tool: diagnosis ---
	envCtx, envCancel := context.WithTimeout(ctx, 30*time.Second)
	defer envCancel()
	toolEnv, err := remove_ger.SetupEnv(envCtx, cfg)
	require.NoError(t, err)
	defer toolEnv.Close()
	diagnosis, err := remove_ger.Diagnose(ctx, toolEnv, detectedGER, false)
	require.NoError(t, err)
	require.Equal(t, remove_ger.ScenarioCategoryB1, diagnosis.Scenario)
	require.Len(t, diagnosis.Claims, 1)
	require.Equal(t, remove_ger.ScenarioCategoryB1, diagnosis.Claims[0].Category)
	require.NotNil(t, diagnosis.Claims[0].CorrectBridge, "B.1 claim must have CorrectBridge")

	// --- Tool: recovery ---
	log.Info("[B1] step: ExecuteRecovery (up to 10m)")
	recoveryTimeout := 10 * time.Minute
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, recoveryTimeout)
	defer recoveryCancel()
	recovery, err := remove_ger.ExecuteRecovery(recoveryCtx, cfg, toolEnv, diagnosis)
	require.NoError(t, err)

	// removalBlock is the exact block of the removeGlobalExitRoots tx (from ExecuteRecovery's receipt).
	// l2gersync only needs to process this fixed past block to observe the removal and unstick; targeting
	// the live L2 head instead would chase a moving target several blocks ahead.
	removalBlock := recovery.RemovalBlock
	require.NotZero(t, removalBlock, "recovery must report the removeGlobalExitRoots block")
	waitForL2GERSyncCaughtUp(ctx, t, env, removalBlock, l2GERCatchUpTimeout)

	// --- Post-recovery assertions ---
	log.Info("[B1] step: post-recovery assertions")
	assertGERRemovedFromL2(ctx, t, env, detectedGER)
	waitForGEROnBridgeService(ctx, t, env, detectedGER, 2*time.Minute)
	isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: ctx})
	require.NoError(t, err)
	require.False(t, isEmergency, "bridge must not be in emergency state after recovery")

	// // --- Post-recovery health ---
	// assertNetworkHealthy(ctx, t, env)

	// --- End-of-scenario still-alive check: l2gersync must serve fresh state past the removal ---
	assertL2GERSyncStillAlive(ctx, t, env)
}

// testRemoveGER_CategoryB2 runs the Category B.2 scenario:
// 1. Do a real L1 bridge (do NOT claim it on L2 directly)
// 2. Build a fake merkle proof at a WRONG deposit_count
// 3. Inject a fake GER that corresponds to that fake root
// 4. Claim using the fake proof (zero-hash siblings) at the wrong deposit_count
// 5. Let the tool diagnose B.2 and recover (unset wrong claim, set correct claim, force emit)
//
// Key: the real bridge is never claimed normally. Instead it is claimed at a wrong deposit_count
// via a fake GER, which is exactly the B.2 scenario the runbook describes.
func testRemoveGER_CategoryB2(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	// 35min: base 30min budget + time for the end-of-scenario still-alive check (a full L1->L2 bridge).
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()

	defer func() {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer restoreCancel()
		isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: restoreCtx})
		if err != nil || !isEmergency {
			return
		}
		opts, err := bind.NewKeyedTransactorWithChainID(env.Keys.SovereignAdmin, env.L2.ChainID)
		if err != nil {
			return
		}
		_, _ = env.L2.Contracts.L2Bridge.DeactivateEmergencyState(opts)
	}()

	// --- Step 1: Do a real L1 bridge (NO claim on L2) ---
	log.Info("[B2] step: perform real L1 bridge (no claim)")

	bridge1 := performBridgeL1NoClaim(ctx, t, env, big.NewInt(200000000000000), "B2-1") // 0.0002 ETH
	log.Infof("[B2] bridge done: deposit_count=%d, global_index=%s",
		bridge1.DepositCount, bridge1.GlobalIndex.String())

	// --- Step 2: Build fake merkle proof at wrong deposit count ---
	// This simulates what happens during an L1 reorg when a bridge moves to a different index.
	log.Info("[B2] step: build fake proof for wrong deposit_count")

	wrongDepositCount1 := uint32(42069)
	fakeProof1 := buildFakeMerkleProofForWrongDepositCount(t, bridge1, wrongDepositCount1)

	// --- Step 3: Stop aggkit, inject fake GER, start aggkit ---
	log.Info("[B2] step: inject fake GER")
	require.NoError(t, env.StopAggkit(ctx))
	injectReceipt := injectInvalidGER(ctx, t, env, fakeProof1.GER)
	require.NoError(t, env.StartAggkit(ctx))
	assertGERExistsOnL2(ctx, t, env, fakeProof1.GER)

	// --- Assert l2gersync is stalled and noisy while the invalid GER is present (S2 design §7) ---
	assertL2GERSyncStalledAt(ctx, t, env, injectReceipt.BlockNumber.Uint64(), l2GERStallObserveWindow)

	// --- Step 4: Claim using fake proof at wrong deposit count ---
	// The claim uses the real bridge leaf data but at a wrong deposit_count, verified against the fake GER.
	log.Info("[B2] step: claim bridge using fake proof (wrong deposit_count)")

	wrongGlobalIndex1 := bridgesync.GenerateGlobalIndexForNetworkID(0, wrongDepositCount1)

	l2Opts1, l2Key1, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L2Keys.Return(l2Key1)

	claimTx1, err := env.L2.Contracts.L2Bridge.ClaimAsset(
		l2Opts1,
		fakeProof1.ProofLocal,
		fakeProof1.ProofRollup,
		wrongGlobalIndex1,
		fakeProof1.MainnetExitRoot,
		fakeProof1.RollupExitRoot,
		bridge1.Bridge.OriginNetwork,
		common.HexToAddress(string(bridge1.Bridge.OriginAddress)),
		bridge1.Bridge.DestinationNetwork,
		bridge1.DestinationAddr,
		bridge1.BridgeAmount,
		common.Hex2Bytes(bridge1.Bridge.Metadata),
	)
	require.NoError(t, err, "ClaimAsset (B.2)")
	receipt1, err := bind.WaitMined(ctx, env.Clients.L2, claimTx1)
	require.NoError(t, err)
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt1.Status, "claim tx failed")
	log.Info("[B2] claim succeeded with fake proof")
	assertClaimedOnL2(ctx, t, env, wrongGlobalIndex1)

	// --- Step 5: Wait for bridge service to index claim ---
	log.Info("[B2] step: wait for claim on bridge service (up to 2m)")
	waitForClaimOnBridgeService(ctx, t, env, wrongGlobalIndex1, 2*time.Minute)

	log.Info("[B2] step: wait for claim by GER on bridge service (up to 2m)")
	waitForClaimInBridgeL2DBByGER(ctx, t, env.Clients.BridgeService, env.L2.NetworkID, fakeProof1.GER, 2*time.Minute)

	// --- Step 6: GER detection (runbook-aligned); complementary signal to the sync-status stall assertion above ---
	log.Info("[B2] step: detect invalid GER from aggkit logs (up to 6m)")
	detectedGER1, err := detectInvalidGERFromAggkitLogs(ctx, t, 6*time.Minute, &fakeProof1.GER)
	require.NoError(t, err)
	require.Equal(t, fakeProof1.GER, detectedGER1, "detected GER must match injected")

	// --- Step 7: Tool diagnosis ---
	log.Info("[B2] step: diagnose GER")
	cfg := loadToolConfig(t)
	envCtx, envCancel := context.WithTimeout(ctx, 30*time.Second)
	defer envCancel()
	toolEnv, err := remove_ger.SetupEnv(envCtx, cfg)
	require.NoError(t, err)
	defer toolEnv.Close()

	diagnosis1, err := remove_ger.Diagnose(ctx, toolEnv, detectedGER1, false)
	require.NoError(t, err)
	require.Equal(t, remove_ger.ScenarioCategoryB2, diagnosis1.Scenario)
	require.Len(t, diagnosis1.Claims, 1)
	require.Equal(t, remove_ger.ScenarioCategoryB2, diagnosis1.Claims[0].Category)
	require.NotNil(t, diagnosis1.Claims[0].CorrectBridge, "B.2 claim must have CorrectBridge")
	require.Equal(t, bridge1.DepositCount, diagnosis1.Claims[0].CorrectBridge.DepositCount,
		"CorrectBridge deposit_count must match real bridge")

	// --- Step 8: Tool recovery ---
	// B.2 recovery: freeze → remove GER → unset wrong claim → set correct claim → force emit → restore
	log.Info("[B2] step: ExecuteRecovery (up to 10m)")
	recoveryTimeout := 10 * time.Minute
	recoveryCtx1, recoveryCancel1 := context.WithTimeout(ctx, recoveryTimeout)
	defer recoveryCancel1()
	recovery, err := remove_ger.ExecuteRecovery(recoveryCtx1, cfg, toolEnv, diagnosis1)
	require.NoError(t, err)

	// removalBlock is the exact block of the removeGlobalExitRoots tx (from ExecuteRecovery's receipt).
	// l2gersync only needs to process this fixed past block to observe the removal and unstick; targeting
	// the live L2 head instead would chase a moving target several blocks ahead.
	removalBlock := recovery.RemovalBlock
	require.NotZero(t, removalBlock, "recovery must report the removeGlobalExitRoots block")
	waitForL2GERSyncCaughtUp(ctx, t, env, removalBlock, l2GERCatchUpTimeout)

	// --- Post-recovery assertions ---
	log.Info("[B2] step: post-recovery assertions")
	assertGERRemovedFromL2(ctx, t, env, detectedGER1)
	waitForGEROnBridgeService(ctx, t, env, detectedGER1, 2*time.Minute)
	assertClaimUnsetOnL2(ctx, t, env, wrongGlobalIndex1)
	assertClaimedOnL2(ctx, t, env, bridge1.GlobalIndex) // correct claim should now be set

	isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: ctx})
	require.NoError(t, err)
	require.False(t, isEmergency, "bridge must not be in emergency state after recovery")

	// --- End-of-scenario still-alive check: l2gersync must serve fresh state past the removal ---
	assertL2GERSyncStillAlive(ctx, t, env)
}

// fakeMerkleProof holds the components needed to make a fake B.2 claim.
type fakeMerkleProof struct {
	GER             common.Hash
	MainnetExitRoot common.Hash
	RollupExitRoot  common.Hash
	ProofLocal      [32][32]byte
	ProofRollup     [32][32]byte
}

// buildFakeMerkleProofForWrongDepositCount generates a fake merkle proof for a bridge at a wrong deposit_count.
// It creates a single-leaf tree with the bridge's leaf hash at the wrong deposit_count, computes the root,
// and generates a GER from that root. The zero-hash proof will verify against this fake root.
func buildFakeMerkleProofForWrongDepositCount(t *testing.T, bridge *bridgeResult, wrongDepositCount uint32) *fakeMerkleProof {
	t.Helper()

	// Get the bridge leaf hash
	b := &bridgesync.Bridge{
		LeafType:           bridge.Bridge.LeafType,
		OriginNetwork:      bridge.Bridge.OriginNetwork,
		OriginAddress:      common.HexToAddress(string(bridge.Bridge.OriginAddress)),
		DestinationNetwork: bridge.Bridge.DestinationNetwork,
		DestinationAddress: common.HexToAddress(string(bridge.Bridge.DestinationAddress)),
		Amount:             bridge.Bridge.Amount.ToBigInt(),
		Metadata:           common.Hex2Bytes(bridge.Bridge.Metadata),
	}
	leafHash := b.Hash()

	// Generate zero hashes for the merkle tree (same as in tree package)
	zeroHashes := generateZeroHashesForProof(treetypes.DefaultHeight)

	// Build proof: all zero-hash siblings (represents a single-leaf tree)
	var proof treetypes.Proof
	for h := uint8(0); h < treetypes.DefaultHeight; h++ {
		proof[h] = zeroHashes[h]
	}

	// Calculate the merkle root for this leaf at the wrong deposit_count
	mainnetExitRoot := tree.CalculateRoot(leafHash, proof, wrongDepositCount)

	// Rollup exit root is zero (no rollup bridges in this test)
	rollupExitRoot := common.Hash{}

	// Calculate GER from the fake mainnet exit root
	ger := l1infotreesync.CalculateGER(mainnetExitRoot, rollupExitRoot)

	// Convert proof to contract format
	var proofLocal, proofRollup [32][32]byte
	for i := 0; i < 32; i++ {
		proofLocal[i] = proof[i]
		proofRollup[i] = zeroHashes[i]
	}

	return &fakeMerkleProof{
		GER:             ger,
		MainnetExitRoot: mainnetExitRoot,
		RollupExitRoot:  rollupExitRoot,
		ProofLocal:      proofLocal,
		ProofRollup:     proofRollup,
	}
}

// forceIPv4Loopback rewrites a "localhost" RPC host in a shell command to the IPv4 loopback address.
// The L2 RPC is reachable on the IPv4 loopback; pinning avoids relying on "localhost" resolving to a
// working address (some hosts list ::1 first for localhost).
func forceIPv4Loopback(cmd string) string {
	return strings.ReplaceAll(cmd, "//localhost:", "//127.0.0.1:")
}

// castRPCURLRegex extracts the value passed to a cast "--rpc-url" flag.
var castRPCURLRegex = regexp.MustCompile(`--rpc-url\s+(\S+)`)

// extractCastRPCURL returns the --rpc-url value from a cast shell command, or "" if none is present.
func extractCastRPCURL(cmd string) string {
	m := castRPCURLRegex.FindStringSubmatch(cmd)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// castCanReachL2 reports whether the host's cast binary can reach the given L2 RPC URL. cast is a
// separate binary; on some hosts its outbound networking to the local Docker-published RPC port is
// broken (a foundry binary networking defect) even though the Go ethclient and curl reach the exact
// same endpoint. It returns true (proceed) unless a trivial cast call fails while the Go ethclient
// probe to the same node succeeds -- i.e. cast specifically cannot reach a reachable node -- in which
// case the caller should skip the cast-based flow. CI installs foundry fresh and is unaffected.
func castCanReachL2(ctx context.Context, t *testing.T, env *envs.Env, rpcURL string) bool {
	t.Helper()
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, castErr := exec.CommandContext(probeCtx, "cast", "block-number", "--rpc-url", rpcURL).CombinedOutput()
	if castErr == nil {
		return true
	}
	// cast failed: only treat it as a host-cast defect if the node itself is reachable via the Go client.
	if _, goErr := env.Clients.L2.BlockNumber(probeCtx); goErr != nil {
		// Node genuinely unreachable -- not a cast-specific problem; let the test proceed and fail loudly.
		return true
	}
	log.Infof("[GenerateInvalidGER] host cast cannot reach L2 RPC %s (%v: %s) while the Go ethclient can",
		rpcURL, castErr, strings.TrimSpace(string(out)))
	return false
}

// waitForL2RPCReady blocks until the L2 RPC endpoint answers a basic query (via the Go ethclient,
// which retries internally), or fails the test after timeout. Used before invoking the cast
// subprocess so a momentary post-StopAggkit connection blip on the shared compose network doesn't
// surface as a spurious "Connection refused".
func waitForL2RPCReady(ctx context.Context, t *testing.T, env *envs.Env, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := env.Clients.L2.BlockNumber(ctx); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("L2 RPC not ready within %s", timeout)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context done waiting for L2 RPC readiness: %v", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// castTransientErrSubstrings identify transient L2-RPC connection failures worth retrying a cast call on.
var castTransientErrSubstrings = []string{
	"Connection refused",
	"connect error",
	"error sending request",
	"tcp connect",
}

// runCastWithRetry runs a `cast` bash command, retrying briefly on transient L2-RPC connection
// failures. Unlike the Go ethclient used elsewhere in the harness, the cast subprocess has no
// built-in retry, so a momentary Docker bridge/NAT blip (e.g. right after StopAggkit reconfigures
// the shared compose network) can surface as a one-off "Connection refused". A connection-refused
// failure means the tx never reached the node, so retrying the cast send is safe. On persistent or
// non-transient failure the test fails via require.
func runCastWithRetry(ctx context.Context, t *testing.T, env *envs.Env, label, cmd string) []byte {
	t.Helper()
	const maxAttempts = 5
	backoff := backoffInitial
	var out []byte
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		out, err = exec.CommandContext(ctx, "bash", "-c", cmd).CombinedOutput()
		if err == nil {
			return out
		}
		transient := false
		for _, sub := range castTransientErrSubstrings {
			if strings.Contains(string(out), sub) {
				transient = true
				break
			}
		}
		if attempt == maxAttempts || !transient {
			break
		}
		log.Infof("[GenerateInvalidGER] cast %q attempt %d/%d failed transiently, retrying in %s: %s",
			label, attempt, maxAttempts, backoff, strings.TrimSpace(string(out)))
		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err(), "context done while retrying cast %q", label)
		case <-time.After(backoff):
		}
		// Re-probe the RPC so the next attempt only fires once the endpoint answers again.
		waitForL2RPCReady(ctx, t, env, 30*time.Second)
		backoff *= 2
		if backoff > backoffMax {
			backoff = backoffMax
		}
	}
	require.NoError(t, err, "cast %s: %s", label, string(out))
	return out
}

// testGenerateInvalidGER tests the "generate" subcommand end-to-end:
// 1. Build CLI binary
// 2. Run "generate --network-id <N>" and capture output
// 3. Parse the two cast commands from stdout
// 4. Stop aggkit, execute both cast commands, start aggkit
// 5. Assert GER exists on L2 and claim is marked
func testGenerateInvalidGER(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	if _, err := exec.LookPath("cast"); err != nil {
		t.Skip("cast not found in PATH, skipping TestGenerateInvalidGER")
	}
	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// Best-effort: restore bridge if left in emergency state
	defer func() {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer restoreCancel()
		isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: restoreCtx})
		if err != nil || !isEmergency {
			return
		}
		opts, err := bind.NewKeyedTransactorWithChainID(env.Keys.SovereignAdmin, env.L2.ChainID)
		if err != nil {
			return
		}
		_, _ = env.L2.Contracts.L2Bridge.DeactivateEmergencyState(opts)
	}()

	// --- Step 1: Build the CLI binary ---
	envsDir, err := envs.FindEnvsDir()
	require.NoError(t, err)
	repoRoot := filepath.Join(envsDir, "..", "..", "..") // envs dir = <repo>/test/e2e/envs
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "remove-ger")
	buildCmd := exec.CommandContext(ctx, "go", "build", "-o", binaryPath, "./tools/remove_ger/cmd/")
	buildCmd.Dir = repoRoot
	buildOut, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "build remove-ger binary: %s", string(buildOut))

	// --- Step 2: Run "generate" subcommand ---
	// Use a random deposit count so the generated GER is unique per run (avoids GlobalExitRootAlreadySet
	// if the environment persists from a previous run).
	var depositCountBytes [2]byte
	_, err = rand.Read(depositCountBytes[:])
	require.NoError(t, err)
	randomDepositCount := uint32(40000) + uint32(depositCountBytes[0])<<8 + uint32(depositCountBytes[1])

	configPath := getPreparedToolConfigPath(t)
	networkID := fmt.Sprintf("%d", env.L2.NetworkID)
	generateCmd := exec.CommandContext(ctx, binaryPath,
		"--cfg", configPath,
		"generate",
		"--network-id", networkID,
		"--deposit-count", fmt.Sprintf("%d", randomDepositCount),
	)
	generateOut, err := generateCmd.CombinedOutput()
	require.NoError(t, err, "run generate subcommand: %s", string(generateOut))
	output := string(generateOut)
	t.Logf("generate output:\n%s", output)

	// --- Step 3: Parse output ---
	gerHash := parseGERFromGenerateOutput(t, output)
	globalIndex := parseGlobalIndexFromGenerateOutput(t, output)
	injectCmd, claimCmd := parseCastCommandsFromOutput(t, output)

	aggoracleKeyHex := hex.EncodeToString(crypto.FromECDSA(env.Keys.AggOracle))
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L2Keys.Return(l2Key)
	claimKeyHex := hex.EncodeToString(crypto.FromECDSA(l2Key))
	_ = l2Opts // only need the raw key for cast

	injectCmd = expandEnvVars(injectCmd, map[string]string{
		"AGGORACLE_PRIVATE_KEY": "0x" + aggoracleKeyHex,
	})
	claimCmd = expandEnvVars(claimCmd, map[string]string{
		"CLAIM_PRIVATE_KEY": "0x" + claimKeyHex,
	})

	// cast (foundry) resolves "localhost" to IPv6 ::1 first on hosts whose /etc/hosts lists ::1 for
	// localhost, but Docker publishes the L2 RPC port on IPv4 only -> deterministic "Connection refused"
	// (the Go ethclient used elsewhere sidesteps this via dual-stack Happy Eyeballs). Pin cast to the
	// IPv4 loopback so the RPC URL resolves to where the port is actually published.
	injectCmd = forceIPv4Loopback(injectCmd)
	claimCmd = forceIPv4Loopback(claimCmd)

	// Preflight: this test's whole point is to exercise the operator's cast-based inject/claim flow, so
	// it needs a working host cast. On some dev hosts the foundry cast binary cannot open outbound
	// connections to the local Docker-published L2 RPC port (connection refused) even though curl and
	// the Go ethclient reach the same endpoint -- a machine-local cast networking defect. Skip cleanly
	// in that case rather than red-failing; CI installs foundry fresh and runs the test normally.
	if rpcURL := extractCastRPCURL(injectCmd); rpcURL != "" && !castCanReachL2(ctx, t, env, rpcURL) {
		t.Skipf("host cast cannot reach the L2 RPC (%s) though the node is reachable via curl/Go; "+
			"skipping cast-based generate flow -- local foundry networking defect, unaffected in CI", rpcURL)
	}

	// --- Step 4: Stop aggkit, inject GER, claim, start aggkit ---
	// Stop aggkit first to avoid nonce conflicts with the aggoracle key (consistent with other tests).
	require.NoError(t, env.StopAggkit(ctx))

	// Stopping the aggkit sibling container can briefly reconfigure the shared compose network's
	// Docker bridge/NAT, so probe the L2 RPC (via the retrying Go ethclient) before invoking the
	// non-retrying cast subprocess, then run each cast with a short retry on transient RPC blips.
	waitForL2RPCReady(ctx, t, env, 30*time.Second)

	log.Info("[GenerateInvalidGER] executing inject GER cast command")
	injectOut := runCastWithRetry(ctx, t, env, "inject GER", injectCmd)
	t.Logf("inject output: %s", string(injectOut))

	log.Info("[GenerateInvalidGER] executing claim cast command")
	claimOut := runCastWithRetry(ctx, t, env, "claim", claimCmd)
	t.Logf("claim output: %s", string(claimOut))

	require.NoError(t, env.StartAggkit(ctx))

	// --- Step 5: Assert injection and claim succeeded ---
	assertGERExistsOnL2(ctx, t, env, gerHash)
	assertClaimedOnL2(ctx, t, env, globalIndex)

	// Wait for bridge L2 sync to index the claim; otherwise diagnosis sees no claims.
	waitForClaimOnBridgeService(ctx, t, env, globalIndex, 2*time.Minute)

	// --- Step 6: Recovery using the CLI binary (same binary, diagnose+recover mode) ---
	log.Info("[GenerateInvalidGER] running remove-ger tool to recover from invalid GER")
	recoverCmd := exec.CommandContext(ctx, binaryPath,
		"--cfg", configPath,
		"--ger", gerHash.Hex(),
		"--yes",
	)
	recoverOut, err := recoverCmd.CombinedOutput()
	require.NoError(t, err, "remove-ger recovery: %s", string(recoverOut))
	t.Logf("recovery output: %s", string(recoverOut))

	assertGERRemovedFromL2(ctx, t, env, gerHash)

	isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: ctx})
	require.NoError(t, err)
	require.False(t, isEmergency, "bridge must not be in emergency state after recovery")

	log.Info("[GenerateInvalidGER] test passed: generate, inject, claim, and recovery all succeeded")
}

// parseGERFromGenerateOutput extracts the GER hash from the "# GER: 0x..." line.
func parseGERFromGenerateOutput(t *testing.T, output string) common.Hash {
	t.Helper()
	re := regexp.MustCompile(`# GER: (0x[0-9a-fA-F]{64})`)
	matches := re.FindStringSubmatch(output)
	require.Len(t, matches, 2, "expected GER hash in generate output")
	return common.HexToHash(matches[1])
}

// parseGlobalIndexFromGenerateOutput extracts the global index from the "# Global Index: <decimal>" line.
func parseGlobalIndexFromGenerateOutput(t *testing.T, output string) *big.Int {
	t.Helper()
	re := regexp.MustCompile(`# Global Index: (\d+)`)
	matches := re.FindStringSubmatch(output)
	require.Len(t, matches, 2, "expected Global Index in generate output")
	gi := new(big.Int)
	_, ok := gi.SetString(matches[1], 10)
	require.True(t, ok, "invalid Global Index decimal: %s", matches[1])
	return gi
}

// parseCastCommandsFromOutput extracts the two "cast send" command lines from generate output.
func parseCastCommandsFromOutput(t *testing.T, output string) (injectCmd, claimCmd string) {
	t.Helper()
	var castLines []string
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "cast send") {
			castLines = append(castLines, trimmed)
		}
	}
	require.Len(t, castLines, 2, "expected exactly 2 cast send commands in generate output")
	return castLines[0], castLines[1]
}

// expandEnvVars replaces $VAR_NAME placeholders in cmd with their values from vars.
func expandEnvVars(cmd string, vars map[string]string) string {
	for k, v := range vars {
		cmd = strings.ReplaceAll(cmd, "$"+k, v)
	}
	return cmd
}
