// Package e2e — TestForceGERUpdateE2E exercises the force_ger_update CLI tool (tools/force_ger_update)
// as a real subprocess against the docker-compose op-pp environment.
//
// Run with (docker-compose env brought up automatically by TestMain/envs.LoadEnv):
//
//	go test -v -timeout 30m -run TestForceGERUpdateE2E ./test/e2e/...
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/require"
)

// bridgeMessageSelector is the 4-byte selector of bridgeMessage(uint32,address,bool,bytes)
// (0x240ff378), the function the force_ger_update tool calls to force a GER update.
var bridgeMessageSelector = crypto.Keccak256([]byte("bridgeMessage(uint32,address,bool,bytes)"))[:4]

// updateL1InfoTreeTopic is the topic0 hash of the V1 UpdateL1InfoTree(bytes32,bytes32) event,
// emitted by the L1 GlobalExitRootManager whenever the L1 info tree (and therefore the GER) is
// updated. Mirrors tools/force_ger_update/monitor.go's updateL1InfoTreeSignature.
var updateL1InfoTreeTopic = crypto.Keccak256Hash([]byte("UpdateL1InfoTree(bytes32,bytes32)"))

// forceGERUpdateThreshold is the tool's MaxTimeWithoutGERUpdate for this test: short enough that
// the test doesn't wait long, but long enough (well above CheckInterval/EventPollInterval) that a
// stale-on-boot forced update is unambiguous.
const forceGERUpdateThreshold = 30 * time.Second

// summaryForForceGERUpdateConfig is a minimal struct for reading summary.json to build the
// force_ger_update tool config (mirrors summaryForToolConfig in removeger_test.go / the analogous
// struct in backwardforwardlet_test.go — each tool test reads only the fields it needs).
type summaryForForceGERUpdateConfig struct {
	Networks struct {
		L1 struct {
			Contracts struct {
				Bridge string `json:"bridge"`
			} `json:"contracts"`
			Services struct {
				Geth struct {
					HTTPRpc struct {
						External string `json:"external"`
					} `json:"http_rpc"`
				} `json:"geth"`
			} `json:"services"`
		} `json:"l1"`
	} `json:"networks"`
}

// syncBuffer is a concurrency-safe io.Writer used to capture the force_ger_update subprocess's
// combined stdout/stderr while the test polls for on-chain effects in parallel.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// latestUpdateL1InfoTreeLog performs a bounded backward-chunked scan (mirroring
// tools/force_ger_update/monitor.go's boot scan) for the most recent UpdateL1InfoTree log emitted
// by gerAddr, so the test can record a "before" baseline block. Returns nil (no error) if none is
// found within lookback blocks of the current head.
func latestUpdateL1InfoTreeLog(
	ctx context.Context, client *ethclient.Client, gerAddr common.Address, lookback uint64,
) (*ethtypes.Log, error) {
	head, err := client.BlockNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch L1 block number: %w", err)
	}

	const chunk = uint64(5000)
	hi := head
	var scanned uint64
	lookbackFloor := uint64(0)
	if head > lookback {
		lookbackFloor = head - lookback
	}

	for {
		lo := lookbackFloor
		if hi-lookbackFloor+1 > chunk {
			lo = hi - chunk + 1
		}

		logs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
			FromBlock: new(big.Int).SetUint64(lo),
			ToBlock:   new(big.Int).SetUint64(hi),
			Addresses: []common.Address{gerAddr},
			Topics:    [][]common.Hash{{updateL1InfoTreeTopic}},
		})
		if err != nil {
			return nil, fmt.Errorf("filter UpdateL1InfoTree logs [%d,%d]: %w", lo, hi, err)
		}
		if len(logs) > 0 {
			latest := logs[len(logs)-1]
			return &latest, nil
		}

		scanned += hi - lo + 1
		if lo <= lookbackFloor || scanned >= lookback {
			return nil, nil
		}
		hi = lo - 1
	}
}

// newUpdateL1InfoTreeFromSender polls (via pollWithBackoff, shared with removeger_test.go) for an
// UpdateL1InfoTree log at a block strictly after afterBlock whose transaction: (a) targets bridgeAddr,
// (b) starts with the bridgeMessage selector (0x240ff378), and (c) was sent by senderAddr — the
// causal proof that the force_ger_update tool (and not some other actor, e.g. the aggoracle or a
// bridge-driven organic update) produced this GER update.
func waitForForcedGERUpdate(
	ctx context.Context, t *testing.T, client *ethclient.Client,
	gerAddr, bridgeAddr, senderAddr common.Address, afterBlock uint64, l1ChainID *big.Int, timeout time.Duration,
) *ethtypes.Log {
	t.Helper()

	var found *ethtypes.Log
	signer := ethtypes.LatestSignerForChainID(l1ChainID)

	err := pollWithBackoff(ctx, timeout, 2*time.Second, 5*time.Second, "forced UpdateL1InfoTree from tool sender", func() (bool, error) {
		head, err := client.BlockNumber(ctx)
		if err != nil {
			return false, nil //nolint:nilerr // transient RPC hiccups shouldn't abort the poll
		}
		if head <= afterBlock {
			return false, nil
		}

		logs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
			FromBlock: new(big.Int).SetUint64(afterBlock + 1),
			ToBlock:   new(big.Int).SetUint64(head),
			Addresses: []common.Address{gerAddr},
			Topics:    [][]common.Hash{{updateL1InfoTreeTopic}},
		})
		if err != nil {
			return false, nil //nolint:nilerr // transient RPC hiccups shouldn't abort the poll
		}

		for i := range logs {
			l := logs[i]
			tx, _, err := client.TransactionByHash(ctx, l.TxHash)
			if err != nil || tx == nil {
				continue
			}
			if tx.To() == nil || *tx.To() != bridgeAddr {
				continue
			}
			data := tx.Data()
			if len(data) < 4 || !bytes.Equal(data[:4], bridgeMessageSelector) {
				continue
			}
			from, err := ethtypes.Sender(signer, tx)
			if err != nil || from != senderAddr {
				continue
			}
			found = &l
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err, "waiting for a forced UpdateL1InfoTree update from the tool's sender")
	require.NotNil(t, found, "expected a matching UpdateL1InfoTree log")
	return found
}

// prepareForceGERUpdateConfig builds the force_ger_update tool config (TOML) into a temp dir:
// L1 RPC + bridge address from summary.json, the L1 GER manager address read on-chain from the
// bridge contract (authoritative, avoids trusting an unlisted summary.json field), a fresh
// keystore for privKey, and a sqlite storage path — all inside dir.
func prepareForceGERUpdateConfig(
	ctx context.Context, t *testing.T, env *envs.Env, dir string, destinationNetwork uint32,
) (configPath string, senderAddr common.Address) {
	t.Helper()

	summaryPath := filepath.Join(env.EnvDir, "summary.json")
	summaryData, err := os.ReadFile(summaryPath)
	require.NoError(t, err)

	var summary summaryForForceGERUpdateConfig
	require.NoError(t, json.Unmarshal(summaryData, &summary))

	l1URL := summary.Networks.L1.Services.Geth.HTTPRpc.External
	require.NotEmpty(t, l1URL, "L1 RPC URL must be present in summary.json")
	bridgeAddr := common.HexToAddress(summary.Networks.L1.Contracts.Bridge)
	require.NotEqual(t, common.Address{}, bridgeAddr, "L1 bridge address must be present in summary.json")

	gerAddr, err := env.L1.Contracts.Bridge.GlobalExitRootManager(&bind.CallOpts{Context: ctx})
	require.NoError(t, err, "read GlobalExitRootManager address from the L1 bridge contract")
	require.NotEqual(t, common.Address{}, gerAddr)

	// Check out a pool key (not a shared special key) and fund it as the tool's sender.
	_, privKey, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err)
	t.Cleanup(func() { env.Keys.L1Keys.Return(privKey) })
	senderAddr = crypto.PubkeyToAddress(privKey.PublicKey)

	const keystorePassword = "force-ger-update-e2e-test-only"
	ks := keystore.NewKeyStore(dir, keystore.LightScryptN, keystore.LightScryptP)
	acc, err := ks.ImportECDSA(privKey, keystorePassword)
	require.NoError(t, err, "import tool sender key into ephemeral keystore")

	storagePath := filepath.Join(dir, "ethtxmanager-force_ger_update.sqlite")

	configContent := fmt.Sprintf(`[ForceGERUpdate]
L1URL = %q
L1WSURL = ""
GlobalExitRootManagerAddr = %q
BridgeAddr = %q
MaxTimeWithoutGERUpdate = %q
CheckInterval = "3s"
EventPollInterval = "3s"
InitialLookbackBlocks = 20000
FilterLogsChunkSize = 5000
DestinationNetwork = %d
DestinationAddress = "0x0000000000000000000000000000000000000000"
DryRun = false

	[ForceGERUpdate.EthTxManager]
	FrequencyToMonitorTxs = "1s"
	WaitTxToBeMined = "2s"
	GetReceiptMaxTime = "250ms"
	GetReceiptWaitInterval = "1s"
	PrivateKeys = [
		{Method = "local", Path = %q, Password = %q},
	]
	ForcedGas = 0
	GasPriceMarginFactor = 1
	MaxGasPriceLimit = 0
	StoragePath = %q
	ReadPendingL1Txs = false
	SafeStatusL1NumberOfBlocks = 0
	FinalizedStatusL1NumberOfBlocks = 0
	EstimateGasMaxRetries = 1

		[ForceGERUpdate.EthTxManager.Etherman]
		URL = %q
		MultiGasProvider = false
		L1ChainID = %s
		HTTPHeaders = []
`,
		l1URL,
		gerAddr.Hex(),
		bridgeAddr.Hex(),
		forceGERUpdateThreshold.String(),
		destinationNetwork,
		acc.URL.Path,
		keystorePassword,
		storagePath,
		l1URL,
		env.L1.ChainID.String(),
	)

	configPath = filepath.Join(dir, "force-ger-update-config-test.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))
	return configPath, senderAddr
}

// buildForceGERUpdateBinary builds the force_ger_update tool via "make build-force_ger_update" and
// returns the path to the resulting binary (target/force_ger_update, per the Makefile's GOBIN rule).
func buildForceGERUpdateBinary(ctx context.Context, t *testing.T) string {
	t.Helper()

	envsDir, err := envs.FindEnvsDir()
	require.NoError(t, err)
	repoRoot := filepath.Join(envsDir, "..", "..", "..") // envs dir = <repo>/test/e2e/envs

	buildCmd := exec.CommandContext(ctx, "make", "build-force_ger_update")
	buildCmd.Dir = repoRoot
	out, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "make build-force_ger_update: %s", string(out))

	binaryPath := filepath.Join(repoRoot, "target", "force_ger_update")
	_, err = os.Stat(binaryPath)
	require.NoError(t, err, "expected binary at %s", binaryPath)
	return binaryPath
}

// TestForceGERUpdateE2E runs the force_ger_update tool as a real subprocess against the op-pp
// docker-compose env and proves it forces an on-chain L1 GER update when none happens organically
// within MaxTimeWithoutGERUpdate.
//
// Run with:
//
//	go test -v -timeout 30m -run TestForceGERUpdateE2E ./test/e2e/...
func TestForceGERUpdateE2E(t *testing.T) {
	// Skipped by default (like the remove_ger e2e tests): forcing a GER update perturbs the shared
	// op-pp env's post-test L1<->L2 bridge health check that TestMain runs, which can then time out
	// even though this test's own assertions pass. Run it explicitly with the command in the doc
	// comment above when validating the tool against a live environment.
	t.Skip("Skipping known flaky e2e: forcing a GER update can leave the post-test bridge health check unhealthy")
	testForceGERUpdateE2E(t)
}

func testForceGERUpdateE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// --- Step 1: build the real binary ---
	binaryPath := buildForceGERUpdateBinary(ctx, t)

	// --- Step 2: render the tool config into a temp dir (funded key from the L1 pool) ---
	tmpDir := t.TempDir()
	configPath, senderAddr := prepareForceGERUpdateConfig(ctx, t, env, tmpDir, env.L2.NetworkID)
	log.Infof("[ForceGERUpdateE2E] tool sender address: %s", senderAddr.Hex())

	gerAddr, err := env.L1.Contracts.Bridge.GlobalExitRootManager(&bind.CallOpts{Context: ctx})
	require.NoError(t, err)
	bridgeAddr, err := func() (common.Address, error) {
		summaryData, err := os.ReadFile(filepath.Join(env.EnvDir, "summary.json"))
		if err != nil {
			return common.Address{}, err
		}
		var summary summaryForForceGERUpdateConfig
		if err := json.Unmarshal(summaryData, &summary); err != nil {
			return common.Address{}, err
		}
		return common.HexToAddress(summary.Networks.L1.Contracts.Bridge), nil
	}()
	require.NoError(t, err)

	// --- Step 3: record the latest UpdateL1InfoTree block before starting the tool ---
	var baselineBlock uint64
	baselineLog, err := latestUpdateL1InfoTreeLog(ctx, env.Clients.L1, gerAddr, 20000)
	require.NoError(t, err)
	if baselineLog != nil {
		baselineBlock = baselineLog.BlockNumber
	}
	log.Infof("[ForceGERUpdateE2E] baseline UpdateL1InfoTree block: %d", baselineBlock)

	// --- Step 4: exec the real binary as a subprocess ---
	runCmd := exec.CommandContext(ctx, binaryPath, "--cfg", configPath)
	var output syncBuffer
	runCmd.Stdout = &output
	runCmd.Stderr = &output

	require.NoError(t, runCmd.Start(), "start force_ger_update subprocess")

	// Ensure the process is always cleaned up, even if an assertion below fails first.
	processExited := make(chan struct{})
	go func() {
		_ = runCmd.Wait()
		close(processExited)
	}()
	defer func() {
		if runCmd.ProcessState == nil {
			_ = runCmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-processExited:
			case <-time.After(15 * time.Second):
				_ = runCmd.Process.Kill()
			}
		}
		t.Logf("force_ger_update output:\n%s", output.String())
	}()

	// --- Step 5: wait past the threshold and assert a new forced UpdateL1InfoTree appears ---
	forcedLog := waitForForcedGERUpdate(
		ctx, t, env.Clients.L1, gerAddr, bridgeAddr, senderAddr, baselineBlock, env.L1.ChainID,
		forceGERUpdateThreshold+2*time.Minute,
	)
	log.Infof("[ForceGERUpdateE2E] forced UpdateL1InfoTree observed at block %d tx %s",
		forcedLog.BlockNumber, forcedLog.TxHash.Hex())

	// --- Step 6: kill the process and assert a clean exit ---
	require.NoError(t, runCmd.Process.Signal(syscall.SIGTERM), "send SIGTERM to force_ger_update")
	select {
	case <-processExited:
	case <-time.After(15 * time.Second):
		t.Fatal("force_ger_update did not exit within 15s of SIGTERM")
	}
	require.NotNil(t, runCmd.ProcessState, "process must have a final state after Wait")
	require.True(t, runCmd.ProcessState.Success(),
		"force_ger_update should exit cleanly (code 0) on SIGTERM; output:\n%s", output.String())
}
