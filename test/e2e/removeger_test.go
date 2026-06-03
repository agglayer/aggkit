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
	opPPEnvName      = "op-pp"
	keystorePassword = "pSnv6Dh5s9ahuzGzH9RoCDrKAMddaX3m"
	backoffInitial   = 500 * time.Millisecond
	backoffMax       = 10 * time.Second
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
// batsAmountCategoryA is the fabricated dummy-claim amount for the Category A test (kept from the
// legacy bats fixture; the value itself is arbitrary for a fabricated claim).
var batsAmountCategoryA = new(big.Int).SetUint64(30000005400000000)

// The CategoryA dummy claim's destination is the L2 network so the claim targets it. (The legacy
// bats fixture hardcoded a fixed GER + merkle proof at deposit_count=2; that collided with prior
// tests' sequential claims, so CategoryA now builds its proof dynamically at a fresh index — see
// buildDynamicClaimProof / dummyCategoryADepositCount.)
const batsDestinationNetworkCategoryA = 1

var batsDestinationAddressCategoryA = common.HexToAddress("0x85dA99c8a7C2C95964c8EfD687E95E632Fc533D6")

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
	return buildDynamicClaimProof(b, depositCount)
}

// buildDynamicClaimProof builds a single-leaf (otherwise-empty tree) claim proof and the resulting
// invalid GER for the given bridge-exit leaf placed at depositCount. The leaf sits in an otherwise
// empty exit tree, so the local proof is just the precomputed zero hashes. Used to craft
// order-independent problematic claims at a FRESH, never-claimed deposit count (the legacy bats
// fixtures hardcoded low deposit counts that collide with prior tests' sequential claims).
func buildDynamicClaimProof(leaf *bridgesync.Bridge, depositCount uint32) *b1ClaimProof {
	leafHash := leaf.Hash()
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

// mainnetGlobalIndex encodes a mainnet (L1->L2) global index for the given deposit count: the
// mainnet flag is bit 64 and the deposit count occupies the low bits (e.g. count 2 -> 2^64 + 2).
// depositCount < 2^32 never overlaps the flag bit.
func mainnetGlobalIndex(depositCount uint32) *big.Int {
	return new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(int64(depositCount)))
}

// dummyCategoryADepositCount is a high, never-reached deposit count for CategoryA's fabricated claim
// so it cannot collide with the sequential deposit counts (0,1,2,...) that real L1->L2 claims in
// other tests consume. It is well below the 2^32 exit-tree capacity.
const dummyCategoryADepositCount uint32 = 0x7F000000

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
	sovereignAdminKeyPath := filepath.Join(testEnv.EnvDir, "config", "001", "sovereignadmin.keystore")

	originalCfg := filepath.Join(testEnv.EnvDir, "config", "001", "aggkit-config.toml")
	content, err := os.ReadFile(originalCfg)
	require.NoError(t, err)

	// Patch internal URLs so the tool (running on host) can reach L1/L2.
	content = []byte(strings.ReplaceAll(string(content), "http://geth:8545", l1URL))
	content = []byte(strings.ReplaceAll(string(content), "http://op-geth-001:8545", l2URL))

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

	require.NoError(t, env.StopAggkit(ctx))
	injectInvalidGER(ctx, t, env, injectedGER)
	require.NoError(t, env.StartAggkit(ctx))

	assertGERExistsOnL2(ctx, t, env, injectedGER)

	// --- GER detection (runbook-aligned): obtain GER only from logs ---
	// Pass &injectedGER so we only accept this test's GER; when run in suite, logs contain
	// GERs from earlier tests (CategoryA, CategoryB1) and nil would return the first one found.
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

// testRemoveGER_CategoryA runs the Category A scenario: invalid GER + dummy claim (no bridge on L1), detect GER from logs, diagnose Category A, recover (unset claim), assert health.
func testRemoveGER_CategoryA(t *testing.T) {
	t.Helper()
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

	// --- Setup: inject an invalid GER and execute a fabricated "Category A" dummy claim ---
	// ORDER-INDEPENDENT: place the fabricated claim at a fresh, never-claimed deposit count and build
	// its proof + GER dynamically (single-leaf otherwise-empty tree), rather than the legacy bats
	// fixture's hardcoded mainnet deposit_count=2, which prior tests' sequential L1->L2 claims consume
	// (so the dummy ClaimAsset would revert "already claimed"). The leaf has no matching real bridge,
	// so the tool still classifies it as Category A. The destination is the L2 so the claim targets it.
	dummyLeaf := &bridgesync.Bridge{
		LeafType:           0,
		OriginNetwork:      0,
		OriginAddress:      common.Address{},
		DestinationNetwork: batsDestinationNetworkCategoryA,
		DestinationAddress: batsDestinationAddressCategoryA,
		Amount:             new(big.Int).Set(batsAmountCategoryA),
		Metadata:           []byte{},
	}
	claimProof := buildDynamicClaimProof(dummyLeaf, dummyCategoryADepositCount)
	injectedGER := claimProof.InvalidGER
	globalIndex := mainnetGlobalIndex(dummyCategoryADepositCount)
	params := dummyClaimParams{
		GlobalIndex:         globalIndex,
		MainnetExitRoot:     claimProof.MainnetExitRoot,
		RollupExitRoot:      claimProof.RollupExitRoot,
		OriginNetwork:       0,
		DestinationNetwork:  batsDestinationNetworkCategoryA,
		OriginAddress:       common.Address{},
		DestinationAddress:  batsDestinationAddressCategoryA,
		Amount:              new(big.Int).Set(batsAmountCategoryA),
		Metadata:            []byte{},
		ProofLocalExitRoot:  claimProof.ProofLocal,
		ProofRollupExitRoot: claimProof.ProofRollup,
	}

	require.NoError(t, env.StopAggkit(ctx))
	injectInvalidGER(ctx, t, env, injectedGER)
	assertGERExistsOnL2(ctx, t, env, injectedGER)
	// Bats test uses aggoracle key to send the dummy claim tx (latest-n-injected-ger.bats).
	aggoracleOpts, err := bind.NewKeyedTransactorWithChainID(env.Keys.AggOracle, env.L2.ChainID)
	require.NoError(t, err)
	executeDummyClaimWithOpts(ctx, t, env, params, aggoracleOpts)
	require.NoError(t, env.StartAggkit(ctx))

	assertClaimedOnL2(ctx, t, env, globalIndex)

	// Wait for bridge L2 sync to index the claim; otherwise diagnosis sees no claims.
	waitForClaimOnBridgeService(ctx, t, env, globalIndex, 2*time.Minute)

	// --- GER detection (runbook-aligned) ---
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
	t.Helper()
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
	log.Info("[B1] step: performBridgeL1NoClaim")
	bridgeResult := performBridgeL1NoClaim(ctx, t, env, big.NewInt(100000000000000), "B1")
	log.Info("[B1] step: buildB1ClaimProof, injectInvalidGER, executeB1Claim")
	proof := buildB1ClaimProof(t, bridgeResult.Bridge, bridgeResult.DepositCount)
	require.NoError(t, env.StopAggkit(ctx))
	injectInvalidGER(ctx, t, env, proof.InvalidGER)
	require.NoError(t, env.StartAggkit(ctx))
	assertGERExistsOnL2(ctx, t, env, proof.InvalidGER)
	executeB1Claim(ctx, t, env, bridgeResult, proof)
	assertClaimedOnL2(ctx, t, env, bridgeResult.GlobalIndex)

	// Wait for bridge L2 sync to index the claim before diagnosis (tool reads same SQLite as aggkit)
	log.Info("[B1] step: waitForClaimOnBridgeService (up to 2m)")
	waitForClaimOnBridgeService(ctx, t, env, bridgeResult.GlobalIndex, 2*time.Minute)

	// Wait for the claim to appear via bridge service under our invalid GER (same query as tool).
	log.Info("[B1] step: waitForClaimInBridgeL2DBByGER (up to 2m)", "ger", proof.InvalidGER.Hex())
	waitForClaimInBridgeL2DBByGER(ctx, t, env.Clients.BridgeService, env.L2.NetworkID, proof.InvalidGER, 2*time.Minute)

	// --- GER detection (runbook-aligned) ---
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
	err = remove_ger.ExecuteRecovery(recoveryCtx, cfg, toolEnv, diagnosis)
	require.NoError(t, err)

	// --- Post-recovery assertions ---
	log.Info("[B1] step: post-recovery assertions")
	assertGERRemovedFromL2(ctx, t, env, detectedGER)
	waitForGEROnBridgeService(ctx, t, env, detectedGER, 2*time.Minute)
	isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: ctx})
	require.NoError(t, err)
	require.False(t, isEmergency, "bridge must not be in emergency state after recovery")

	// // --- Post-recovery health ---
	// assertNetworkHealthy(ctx, t, env)
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
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
	injectInvalidGER(ctx, t, env, fakeProof1.GER)
	require.NoError(t, env.StartAggkit(ctx))
	assertGERExistsOnL2(ctx, t, env, fakeProof1.GER)

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

	// --- Step 6: GER detection (runbook-aligned) ---
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
	err = remove_ger.ExecuteRecovery(recoveryCtx1, cfg, toolEnv, diagnosis1)
	require.NoError(t, err)

	// --- Post-recovery assertions ---
	log.Info("[B2] step: post-recovery assertions")
	assertGERRemovedFromL2(ctx, t, env, detectedGER1)
	waitForGEROnBridgeService(ctx, t, env, detectedGER1, 2*time.Minute)
	assertClaimUnsetOnL2(ctx, t, env, wrongGlobalIndex1)
	assertClaimedOnL2(ctx, t, env, bridge1.GlobalIndex) // correct claim should now be set

	isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: ctx})
	require.NoError(t, err)
	require.False(t, isEmergency, "bridge must not be in emergency state after recovery")
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

	// --- Step 4: Stop aggkit, inject GER, claim, start aggkit ---
	// Stop aggkit first to avoid nonce conflicts with the aggoracle key (consistent with other tests).
	require.NoError(t, env.StopAggkit(ctx))

	log.Info("[GenerateInvalidGER] executing inject GER cast command")
	injectExec := exec.CommandContext(ctx, "bash", "-c", injectCmd)
	injectOut, err := injectExec.CombinedOutput()
	require.NoError(t, err, "cast inject GER: %s", string(injectOut))
	t.Logf("inject output: %s", string(injectOut))

	log.Info("[GenerateInvalidGER] executing claim cast command")
	claimExec := exec.CommandContext(ctx, "bash", "-c", claimCmd)
	claimOut, err := claimExec.CombinedOutput()
	require.NoError(t, err, "cast claim: %s", string(claimOut))
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
