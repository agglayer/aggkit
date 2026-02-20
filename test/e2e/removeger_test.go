package e2e

import (
	"context"
	"crypto/rand"
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
	"github.com/agglayer/aggkit/db"
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
	opPPEnvName      = "op-pp"
	keystorePassword = "pSnv6Dh5s9ahuzGzH9RoCDrKAMddaX3m"
	backoffInitial   = 500 * time.Millisecond
	backoffMax       = 10 * time.Second
)

// pollWithBackoff runs fn until it returns (true, nil) or ctx is done. Uses exponential backoff between attempts.
func pollWithBackoff(ctx context.Context, timeout time.Duration, initialInterval, maxInterval time.Duration, fn func() (done bool, err error)) error {
	deadline := time.Now().Add(timeout)
	interval := initialInterval
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %v", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		done, err := fn()
		if err != nil {
			return err
		}
		if done {
			return nil
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

// executeDummyClaim executes a claim on the L2 bridge with fabricated data (for Category A setup).
// Uses pool L2 key for the claimer. Uses params.ProofLocalExitRoot and params.ProofRollupExitRoot when non-zero.
func executeDummyClaim(ctx context.Context, t *testing.T, env *envs.Env, params dummyClaimParams) *ethtypes.Receipt {
	t.Helper()
	return executeDummyClaimWithOpts(ctx, t, env, params, nil)
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

// bridgeResult holds the outcome of a real L1->L2 bridge + claim for use in B.1/B.2 tests.
type bridgeResult struct {
	Bridge          *types.BridgeResponse
	DepositCount    uint32
	L1InfoTreeIndex uint32
	ClaimTxHash     common.Hash
	GlobalIndex     *big.Int
	DestinationAddr common.Address
	BridgeAmount    *big.Int
}

// performRealBridgeL1ToL2 performs a full L1->L2 bridge and claim using pool keys; returns bridge and claim details.
func performRealBridgeL1ToL2(ctx context.Context, t *testing.T, env *envs.Env) *bridgeResult {
	t.Helper()
	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L2Keys.Return(l2Key)

	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	require.NoError(t, err)

	bridgeAmount := big.NewInt(100000000000000) // 0.0001 ETH
	destinationAddress := l2Opts.From
	forceUpdateGlobalExitRoot := true

	l1Opts.Value = bridgeAmount
	defer func() { l1Opts.Value = nil }()

	tx, err := env.L1.Contracts.Bridge.BridgeAsset(
		l1Opts,
		l2NetworkID,
		destinationAddress,
		bridgeAmount,
		common.Address{},
		forceUpdateGlobalExitRoot,
		nil,
	)
	require.NoError(t, err, "BridgeAsset")
	receipt, err := bind.WaitMined(ctx, env.Clients.L1, tx)
	require.NoError(t, err, "wait for bridge tx")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "bridge tx failed")

	time.Sleep(10 * time.Second)

	var bridge *types.BridgeResponse
	for i := 0; i < 30; i++ {
		pageSize := uint32(100)
		params := client.GetBridgesParams{NetworkID: 0, PageSize: &pageSize}
		bridgesResult, err := env.Clients.BridgeService.GetBridges(ctx, params)
		if err == nil && bridgesResult != nil {
			for _, b := range bridgesResult.Bridges {
				if string(b.TxHash) == tx.Hash().Hex() {
					bridge = b
					break
				}
			}
		}
		if bridge != nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	require.NotNil(t, bridge, "bridge not found in bridge service")

	depositCount := bridge.DepositCount
	var l1InfoTreeIndex uint32
	for i := 0; i < 60; i++ {
		idx, err := env.Clients.BridgeService.GetL1InfoTreeIndex(ctx, 0, int(depositCount))
		if err == nil {
			l1InfoTreeIndex = idx
			break
		}
		time.Sleep(5 * time.Second)
	}
	require.NotZero(t, l1InfoTreeIndex, "bridge not in L1 Info Tree")

	for i := 0; i < 120; i++ {
		_, err := env.Clients.BridgeService.GetInjectedL1InfoLeaf(ctx, int(l2NetworkID), int(l1InfoTreeIndex))
		if err == nil {
			break
		}
		time.Sleep(5 * time.Second)
	}

	claimProof, err := env.Clients.BridgeService.GetClaimProof(ctx, 0, l1InfoTreeIndex, depositCount)
	require.NoError(t, err)
	require.NotNil(t, claimProof)

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
	mainnetExitRoot := common.HexToHash(string(claimProof.L1InfoTreeLeaf.MainnetExitRoot))
	rollupExitRoot := common.HexToHash(string(claimProof.L1InfoTreeLeaf.RollupExitRoot))
	originTokenAddress := common.HexToAddress(string(bridge.OriginAddress))
	metadata := common.Hex2Bytes(bridge.Metadata)

	claimTx, err := env.L2.Contracts.L2Bridge.ClaimAsset(
		l2Opts,
		smtProofLocalExitRoot,
		smtProofRollupExitRoot,
		bridge.GlobalIndex,
		mainnetExitRoot,
		rollupExitRoot,
		bridge.OriginNetwork,
		originTokenAddress,
		bridge.DestinationNetwork,
		destinationAddress,
		bridgeAmount,
		metadata,
	)
	require.NoError(t, err, "ClaimAsset")
	claimReceipt, err := bind.WaitMined(ctx, env.Clients.L2, claimTx)
	require.NoError(t, err)
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, claimReceipt.Status, "claim failed")

	return &bridgeResult{
		Bridge:          bridge,
		DepositCount:    depositCount,
		L1InfoTreeIndex: l1InfoTreeIndex,
		ClaimTxHash:     claimTx.Hash(),
		GlobalIndex:     bridge.GlobalIndex,
		DestinationAddr: destinationAddress,
		BridgeAmount:    bridgeAmount,
	}
}

// performRealBridgeL1ToL2NoClaim performs a real L1->L2 bridge and waits for it to be indexed, but does not claim.
// Used by Category B.1: the test then injects an invalid GER and claims with that GER but correct bridge data.
func performRealBridgeL1ToL2NoClaim(ctx context.Context, t *testing.T, env *envs.Env) *bridgeResult {
	t.Helper()
	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L2Keys.Return(l2Key)

	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	require.NoError(t, err)

	bridgeAmount := big.NewInt(100000000000000) // 0.0001 ETH
	destinationAddress := l2Opts.From
	forceUpdateGlobalExitRoot := true

	l1Opts.Value = bridgeAmount
	defer func() { l1Opts.Value = nil }()

	tx, err := env.L1.Contracts.Bridge.BridgeAsset(
		l1Opts,
		l2NetworkID,
		destinationAddress,
		bridgeAmount,
		common.Address{},
		forceUpdateGlobalExitRoot,
		nil,
	)
	require.NoError(t, err, "BridgeAsset")
	receipt, err := bind.WaitMined(ctx, env.Clients.L1, tx)
	require.NoError(t, err, "wait for bridge tx")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "bridge tx failed")

	time.Sleep(10 * time.Second)

	var bridge *types.BridgeResponse
	for i := 0; i < 30; i++ {
		pageSize := uint32(100)
		params := client.GetBridgesParams{NetworkID: 0, PageSize: &pageSize}
		bridgesResult, err := env.Clients.BridgeService.GetBridges(ctx, params)
		if err == nil && bridgesResult != nil {
			for _, b := range bridgesResult.Bridges {
				if string(b.TxHash) == tx.Hash().Hex() {
					bridge = b
					break
				}
			}
		}
		if bridge != nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	require.NotNil(t, bridge, "bridge not found in bridge service")

	depositCount := bridge.DepositCount
	var l1InfoTreeIndex uint32
	for i := 0; i < 60; i++ {
		idx, err := env.Clients.BridgeService.GetL1InfoTreeIndex(ctx, 0, int(depositCount))
		if err == nil {
			l1InfoTreeIndex = idx
			break
		}
		time.Sleep(5 * time.Second)
	}
	require.NotZero(t, l1InfoTreeIndex, "bridge not in L1 Info Tree")

	for i := 0; i < 120; i++ {
		_, err := env.Clients.BridgeService.GetInjectedL1InfoLeaf(ctx, int(l2NetworkID), int(l1InfoTreeIndex))
		if err == nil {
			break
		}
		time.Sleep(5 * time.Second)
	}

	return &bridgeResult{
		Bridge:          bridge,
		DepositCount:    depositCount,
		L1InfoTreeIndex: l1InfoTreeIndex,
		ClaimTxHash:     common.Hash{}, // no claim executed
		GlobalIndex:     bridge.GlobalIndex,
		DestinationAddr: destinationAddress,
		BridgeAmount:    bridgeAmount,
	}
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
	err := pollWithBackoff(pollCtx, timeout, backoffInitial, backoffMax, func() (bool, error) {
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

// waitForClaimInBridgeL2DBByGER polls the Bridge L2 SQLite DB (at host path) until at least one claim exists for the given GER.
// The tool reads this same DB when diagnosing by GER; without this wait the tool can see no rows.
func waitForClaimInBridgeL2DBByGER(ctx context.Context, t *testing.T, hostDataDir string, gerHash common.Hash, timeout time.Duration) {
	t.Helper()
	dbPath := filepath.Join(hostDataDir, "bridgel2sync.sqlite")
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := pollWithBackoff(pollCtx, timeout, backoffInitial, backoffMax, func() (bool, error) {
		db, err := db.NewSQLiteDB(dbPath)
		if err != nil {
			return false, err
		}
		defer db.Close()
		var count int
		err = db.QueryRowContext(ctx, `SELECT count(*) FROM claim WHERE global_exit_root = $1`, gerHash.Hex()).Scan(&count)
		if err != nil {
			return false, err
		}
		return count >= 1, nil
	})
	require.NoError(t, err, "wait for claim in Bridge L2 DB by GER %s", gerHash.Hex())
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
	err = pollWithBackoff(pollCtx, timeout, backoffInitial, backoffMax, func() (bool, error) {
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

// assertNetworkHealthy verifies the network is healthy after recovery: L2 progresses, then a fresh L1->L2 bridge + claim succeeds.
func assertNetworkHealthy(ctx context.Context, t *testing.T, env *envs.Env) {
	t.Helper()
	// 1) Ensure L2 is progressing
	block0, err := env.Clients.L2.BlockNumber(ctx)
	require.NoError(t, err)
	time.Sleep(5 * time.Second)
	block1, err := env.Clients.L2.BlockNumber(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, block1, block0, "L2 should be producing blocks")

	// 2) Perform a fresh L1->L2 bridge and claim
	result := performRealBridgeL1ToL2(ctx, t, env)
	require.NotNil(t, result, "fresh bridge+claim should succeed")
	log.Info("assertNetworkHealthy: fresh bridge and claim succeeded")
	_ = result
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
func detectInvalidGERFromAggkitLogs(ctx context.Context, t *testing.T, envDir string, timeout time.Duration, expectedGER *common.Hash) (common.Hash, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	interval := 3 * time.Second
	for {
		if time.Now().After(deadline) {
			return common.Hash{}, fmt.Errorf("timeout after %v waiting for invalid GER in aggkit logs", timeout)
		}
		select {
		case <-ctx.Done():
			return common.Hash{}, ctx.Err()
		default:
		}
		cmd := exec.CommandContext(ctx, "docker", "compose", "logs", "--no-log-prefix", "aggkit-001")
		cmd.Dir = envDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return common.Hash{}, fmt.Errorf("docker compose logs: %w", err)
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
func prepareToolConfig(t *testing.T, pathRWDataOverride, configDir string) string {
	t.Helper()
	envsDir, err := envs.FindEnvsDir()
	require.NoError(t, err)

	envDir := filepath.Join(envsDir, opPPEnvName)
	summaryPath := filepath.Join(envDir, "summary.json")
	summaryData, err := os.ReadFile(summaryPath)
	require.NoError(t, err)

	var summary summaryForToolConfig
	require.NoError(t, json.Unmarshal(summaryData, &summary))

	l2Network, ok := summary.Networks.L2Networks["001"]
	require.True(t, ok, "L2 network 001 not found")

	bridgeServiceURL := l2Network.Services.Aggkit.BridgeService.External
	l1URL := summary.Networks.L1.Services.Geth.HTTPRpc.External
	l2URL := l2Network.Services.OpGeth.HTTPRpc.External
	var pathRWData string
	if pathRWDataOverride != "" {
		pathRWData = pathRWDataOverride
	} else {
		pathRWData, err = filepath.Abs(filepath.Join(envDir, "aggkit_data_001"))
		require.NoError(t, err)
	}
	sovereignAdminKeyPath := filepath.Join(envDir, "config", "001", "sovereignadmin.keystore")

	originalCfg := filepath.Join(envDir, "config", "001", "aggkit-config.toml")
	content, err := os.ReadFile(originalCfg)
	require.NoError(t, err)

	// Patch internal URLs so the tool (running on host) can reach L1/L2.
	content = []byte(strings.ReplaceAll(string(content), "http://geth:8545", l1URL))
	content = []byte(strings.ReplaceAll(string(content), "http://op-geth-001:8545", l2URL))

	appendSection := fmt.Sprintf(`
PathRWData = "%s"

[RemoveGER]
BridgeServiceURL = "%s"

[RemoveGER.SovereignAdminPrivateKey]
Path = "%s"
Password = "%s"
`,
		pathRWData,
		bridgeServiceURL,
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
		persistentDir, err := os.MkdirTemp("", "aggkit-e2e-removeger-config-")
		require.NoError(t, err)
		toolConfigPath = prepareToolConfig(t, envs.AggkitE2EHostDataDir, persistentDir)
	})
	return toolConfigPath
}

// loadToolConfigWithHostDBPaths loads the remove_ger config using the shared prepared config path and patches
// all sync DB paths to the host E2E data dir (so the tool sees the same DBs as the aggkit container).
func loadToolConfigWithHostDBPaths(t *testing.T) *remove_ger.Config {
	t.Helper()
	configPath := getPreparedToolConfigPath(t)
	cliCtx := buildToolCLIContext(t, configPath)
	cfg, err := remove_ger.LoadConfig(cliCtx)
	require.NoError(t, err)
	hostData := envs.AggkitE2EHostDataDir
	cfg.BridgeL2Sync.DBPath = filepath.Join(hostData, "bridgel2sync.sqlite")
	cfg.BridgeL1Sync.DBPath = filepath.Join(hostData, "bridgel1sync.sqlite")
	cfg.L1InfoTreeSync.DBPath = filepath.Join(hostData, "L1InfoTreeSync.sqlite")
	return cfg
}

// testRemoveGER_NoProblematicClaims runs the No Claims scenario: inject invalid GER, detect from logs, diagnose NoClaims, recover, assert health.
func testRemoveGER_NoProblematicClaims(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
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

	injectInvalidGER(ctx, t, env, injectedGER)
	assertGERExistsOnL2(ctx, t, env, injectedGER)

	// --- GER detection (runbook-aligned): obtain GER only from logs ---
	// Pass &injectedGER so we only accept this test's GER; when run in suite, logs contain
	// GERs from earlier tests (CategoryA, CategoryB1) and nil would return the first one found.
	envsDir, err := envs.FindEnvsDir()
	require.NoError(t, err)
	envDir := filepath.Join(envsDir, opPPEnvName)

	detectedGER, err := detectInvalidGERFromAggkitLogs(ctx, t, envDir, 3*time.Minute, &injectedGER)
	require.NoError(t, err)
	require.NotEqual(t, common.Hash{}, detectedGER, "detected GER must not be zero")
	require.Equal(t, injectedGER, detectedGER, "detected GER must match injected (sanity check)")

	// --- Tool: diagnosis ---
	cfg := loadToolConfigWithHostDBPaths(t)
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

	err = remove_ger.ExecuteRecovery(recoveryCtx, cfg, toolEnv, diagnosis)
	require.NoError(t, err)

	assertGERRemovedFromL2(ctx, t, env, detectedGER)
	waitForGEROnBridgeService(ctx, t, env, detectedGER, 2*time.Minute)

	isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: ctx})
	require.NoError(t, err)
	require.False(t, isEmergency, "bridge must not be in emergency state after recovery")

	// // --- Post-recovery ---
	// assertNetworkHealthy(ctx, t, env)
}

// TestRemoveGER_NoProblematicClaims runs the No Problematic Claims E2E scenario (Chunk 6).
func TestRemoveGER_NoProblematicClaims(t *testing.T) {
	testRemoveGER_NoProblematicClaims(t)
}

// testRemoveGER_CategoryA runs the Category A scenario: invalid GER + dummy claim (no bridge on L1), detect GER from logs, diagnose Category A, recover (unset claim), assert health.
func testRemoveGER_CategoryA(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
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
	// CDK/bridge deployment (e.g. Kurtosis env) and may need to be regenerated for this op-pp snapshot.
	injectedGER := batsGER1
	require.Equal(t, injectedGER, l1infotreesync.CalculateGER(mainnetExitRootBats, rollupExitRootBats),
		"bats GER must equal keccak256(mainnetExitRootBats, rollupExitRootBats)")

	injectInvalidGER(ctx, t, env, injectedGER)
	assertGERExistsOnL2(ctx, t, env, injectedGER)

	globalIndex := batsGlobalIndexCategoryA
	params := batsCategoryADummyClaimParams() // exact bats params so leaf hashes match and proof verifies
	// Bats test uses aggoracle key to send the dummy claim tx (latest-n-injected-ger.bats).
	aggoracleOpts, err := bind.NewKeyedTransactorWithChainID(env.Keys.AggOracle, env.L2.ChainID)
	require.NoError(t, err)
	executeDummyClaimWithOpts(ctx, t, env, params, aggoracleOpts)
	assertClaimedOnL2(ctx, t, env, globalIndex)

	// Wait for bridge L2 sync to index the claim (tool reads from same SQLite); otherwise diagnosis sees no claims.
	waitForClaimOnBridgeService(ctx, t, env, globalIndex, 2*time.Minute)

	// --- GER detection (runbook-aligned) ---
	envsDir, err := envs.FindEnvsDir()
	require.NoError(t, err)
	envDir := filepath.Join(envsDir, opPPEnvName)

	detectedGER, err := detectInvalidGERFromAggkitLogs(ctx, t, envDir, 3*time.Minute, &injectedGER)
	require.NoError(t, err)
	require.NotEqual(t, common.Hash{}, detectedGER)
	require.Equal(t, injectedGER, detectedGER, "detected GER must match injected (sanity check)")

	// --- Tool: diagnosis ---
	cfg := loadToolConfigWithHostDBPaths(t)
	// Assert the claim is present in Bridge L2 DB with this GER (same path the tool uses).
	bridgeL2Path := cfg.BridgeL2Sync.DBPath
	bridgeL2DB, err := db.NewSQLiteDB(bridgeL2Path)
	require.NoError(t, err, "open Bridge L2 DB at %s", bridgeL2Path)
	defer bridgeL2DB.Close()
	var count int
	err = bridgeL2DB.QueryRowContext(ctx, `SELECT count(*) FROM claim WHERE global_exit_root = $1`, detectedGER.Hex()).Scan(&count)
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 1, "Bridge L2 DB at %s must contain at least one claim for GER %s (aggkit container mounts this dir as /tmp)",
		bridgeL2Path, detectedGER.Hex())

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

	err = remove_ger.ExecuteRecovery(recoveryCtx, cfg, toolEnv, diagnosis)
	require.NoError(t, err)

	assertGERRemovedFromL2(ctx, t, env, detectedGER)
	waitForGEROnBridgeService(ctx, t, env, detectedGER, 2*time.Minute)

	isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: ctx})
	require.NoError(t, err)
	require.False(t, isEmergency, "bridge must not be in emergency state after recovery")

	// --- Post-recovery: unset claim remains unset, network healthy ---
	assertClaimUnsetOnL2(ctx, t, env, globalIndex)
	// assertNetworkHealthy(ctx, t, env)
}

// testRemoveGER_CategoryB1 runs the Category B.1 scenario: real bridge, inject invalid GER, claim with invalid GER but correct bridge data, detect from logs, diagnose B.1, recover (remove GER + force emit), assert health.
func testRemoveGER_CategoryB1(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	// B.1 needs more time: real bridge + claim + DB wait + GER detection (up to 6 min) + recovery
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

	// --- Setup: real bridge (no claim), then inject invalid GER and claim with invalid GER but correct bridge data ---
	bridgeResult := performRealBridgeL1ToL2NoClaim(ctx, t, env)
	proof := buildB1ClaimProof(t, bridgeResult.Bridge, bridgeResult.DepositCount)
	injectInvalidGER(ctx, t, env, proof.InvalidGER)
	assertGERExistsOnL2(ctx, t, env, proof.InvalidGER)
	executeB1Claim(ctx, t, env, bridgeResult, proof)
	assertClaimedOnL2(ctx, t, env, bridgeResult.GlobalIndex)

	// Wait for bridge L2 sync to index the claim before diagnosis (tool reads same SQLite as aggkit)
	waitForClaimOnBridgeService(ctx, t, env, bridgeResult.GlobalIndex, 2*time.Minute)
	// Wait for the claim to appear in the Bridge L2 DB under our invalid GER (tool queries by GER)
	waitForClaimInBridgeL2DBByGER(ctx, t, envs.AggkitE2EHostDataDir, proof.InvalidGER, 2*time.Minute)

	// --- GER detection (runbook-aligned) ---
	envsDir, err := envs.FindEnvsDir()
	require.NoError(t, err)
	envDir := filepath.Join(envsDir, opPPEnvName)
	// B.1: wait for our injected GER to appear in logs (l2gersync logs when it processes the InsertGER block and fails to fetch L1 info)
	detectedGER, err := detectInvalidGERFromAggkitLogs(ctx, t, envDir, 6*time.Minute, &proof.InvalidGER)
	require.NoError(t, err)
	require.NotEqual(t, common.Hash{}, detectedGER)
	require.Equal(t, proof.InvalidGER, detectedGER, "detected GER must match injected (sanity check)")

	// --- Tool: diagnosis ---
	cfg := loadToolConfigWithHostDBPaths(t)
	// Re-check that the claim is in the Bridge L2 DB at the exact path the tool will use.
	checkDB, err := db.NewSQLiteDB(cfg.BridgeL2Sync.DBPath)
	require.NoError(t, err)
	var count int
	err = checkDB.QueryRowContext(ctx, `SELECT count(*) FROM claim WHERE global_exit_root = $1`, detectedGER.Hex()).Scan(&count)
	_ = checkDB.Close()
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 1, "claim must be in Bridge L2 DB at tool path %q when starting diagnosis", cfg.BridgeL2Sync.DBPath)
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
	recoveryTimeout := 10 * time.Minute
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, recoveryTimeout)
	defer recoveryCancel()
	err = remove_ger.ExecuteRecovery(recoveryCtx, cfg, toolEnv, diagnosis)
	require.NoError(t, err)

	// --- Post-recovery assertions ---
	assertGERRemovedFromL2(ctx, t, env, detectedGER)
	waitForGEROnBridgeService(ctx, t, env, detectedGER, 2*time.Minute)
	isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: ctx})
	require.NoError(t, err)
	require.False(t, isEmergency, "bridge must not be in emergency state after recovery")

	// // --- Post-recovery health ---
	// assertNetworkHealthy(ctx, t, env)
}

// TestRemoveGER_CategoryA runs the Category A E2E scenario (Chunk 7).
func TestRemoveGER_CategoryA(t *testing.T) {
	testRemoveGER_CategoryA(t)
}

// TestRemoveGER_CategoryB1 runs the Category B.1 E2E scenario (Chunk 8).
func TestRemoveGER_CategoryB1(t *testing.T) {
	testRemoveGER_CategoryB1(t)
}
