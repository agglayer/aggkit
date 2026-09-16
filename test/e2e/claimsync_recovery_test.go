package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	claimsyncstorage "github.com/agglayer/aggkit/claimsync/storage"
	configtypes "github.com/agglayer/aggkit/config/types"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/stretchr/testify/require"
)

const (
	// claimSyncerLogPollTimeout bounds how long waitForSetNextRequiredBlockLog waits for the
	// "Set next required block for claim syncer to N" log line to appear after StartAggkit.
	claimSyncerLogPollTimeout = 3 * time.Minute

	// claimSyncerSettleTimeout bounds how long waitForSettledIBEHeight waits for a claim's
	// certificate to settle on the AggLayer. Mirrors bflCertSettleTimeout's reasoning
	// (backwardforwardlet_test.go): a cert may take up to one full epoch to settle.
	claimSyncerSettleTimeout = 5 * time.Minute

	// claimSyncerMaxRetryFailures bounds how many "fails execution of Setting next required
	// block..." retry-loop log lines are tolerated after the DB wipe + restart before we
	// conclude the retry loop is spinning instead of making progress. A production incident
	// (bali-82-op, see issue #1842) saw this legitimately retry ~33 times while bridgesync
	// caught up, so the bound is set comfortably above that rather than at zero.
	claimSyncerMaxRetryFailures = 40

	// claimSyncerRestartClockSkewMargin is subtracted from the recorded restart time before
	// using it as docker compose logs' --since bound, to tolerate host/container clock skew
	// without cutting off the first post-restart log lines.
	claimSyncerRestartClockSkewMargin = 2 * time.Second

	// claimSyncerRecoveryDBOwnerName is the ownerName passed to claimsyncstorage.New for the
	// read-only verification query in assertClaimSyncerHasClaimAtBlock. It only namespaces the
	// compatibility-data key/value rows and has no effect on the block/claim tables this test
	// reads.
	claimSyncerRecoveryDBOwnerName = "e2e-claimsync-recovery-test"
)

// setNextRequiredBlockLogRegex matches aggsender/query/initial_block_to_claimsync_setter.go's
// "Set next required block for claim syncer to %d" log line, emitted by
// SetInitialBlockToClaimSyncer.SetClaimSyncerNextRequiredBlock once it has successfully derived
// and set the claim syncer's starting block. The captured group is the block number N.
var setNextRequiredBlockLogRegex = regexp.MustCompile(`Set next required block for claim syncer to (\d+)`)

// claimSyncerFallbackWarnSubstr is the exact prefix of the WARN log line emitted by
// SetInitialBlockToClaimSyncer.fallbackSettledIBEBlock (aggsender/query/initial_block_to_claimsync_setter.go)
// when the settled imported bridge exit's block cannot be resolved via the local claim DB nor the
// RPC log-scan fallback. Its absence proves the hardened RPC lookup (claimsync.go's
// GetLatestBlockNumByGlobalIndexFromRPC) found the claim directly.
const claimSyncerFallbackWarnSubstr = "falling back claim syncer start block for settled imported bridge exit: "

// claimSyncerRetryFailureSubstr matches common/retry_handler_delays.go's Execute() failure log
// line ("fails execution of %s try %s. delay %s.  due to error: %v") for the "Setting next
// required block for claim syncer based on agglayer's latest settled certificate" operation name
// used by SetClaimSyncerNextRequiredBlock's retry loop.
const claimSyncerRetryFailureSubstr = "fails execution of Setting next required block for claim syncer"

// TestAggsenderClaimSyncerRecoveryAfterDBWipe reproduces the issue #1842 production scenario
// (namespace bali-82-op): the L2 claim syncer's SQLite DB is wiped (simulating data loss / a
// fresh volume), so on the next aggkit startup SetInitialBlockToClaimSyncer must derive the claim
// syncer's starting block from the AggLayer's latest settled certificate rather than from local
// history. Because the local claim DB is empty, this exercises the hardened RPC lookup
// (GetLatestBlockNumByGlobalIndexFromRPC) for real, end-to-end.
//
// It proves two properties:
//  1. aggsender is not stuck: the retry loop around SetClaimSyncerNextRequiredBlock converges
//     (few "fails execution of..." lines) and reaches a "Set next required block..." log line,
//     and aggsender keeps functioning afterwards (a fresh bridge+claim settles).
//  2. the derived starting block N reflects a real settled-certificate reference rather than a
//     fallback to InitialBlockNum=0: N is > 0 and <= the block of a claim already known to be
//     settled. In this env the only settled reference that is ever non-zero is the settled IBE
//     (SettledBlocks.LastImportedBridgeExitBlock): the env never bridges L2->L1, so
//     LastBridgeExitBlock stays at the initial-LER sentinel 0, and network 001 runs in
//     PessimisticProof mode, so LastSettledL2BlockNum is always 0 too. SettledBlocks.EarliestBlock()
//     (aggsender/types/types.go) excludes a zero source from the minimum rather than letting it
//     dominate, so N > 0 here is a direct consequence of that guard plus the IBE having actually
//     settled. This does NOT prove the claim syncer avoids backfilling from block 0: this env runs
//     with AutoStart=true (the production shape #1842 is about), under which
//     claimsync.(*ClaimSync).Start seeds the underlying EVMDriver with InitialBlockNum regardless
//     of what the setter computes (claimsync/claimsync.go), so the syncer's own AutoStart goroutine
//     backfills from block 0 no matter what N is. N > 0 demonstrates only that the setter itself
//     resolved a real settled reference instead of falling back to 0 - it says nothing about how
//     much the syncer backfills.
func TestAggsenderClaimSyncerRecoveryAfterDBWipe(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	agglayerClient := newTestAgglayerClient(t, env)

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	bridgeAmount := big.NewInt(1e14) // 0.0001 ETH, mirrors BridgeL1ToL2's default.

	// Step 1: bridge L1->L2 and claim on L2. Record the claim's L2 block C, and wait until the
	// AggLayer's latest settled certificate for this network reports this claim as its settled
	// imported bridge exit, recording the settled height H.
	log.Infof("[TestAggsenderClaimSyncerRecoveryAfterDBWipe] sending first L1->L2 bridge+claim")
	firstResult, err := BridgeL1ToL2WithResult(ctx, env, l1Opts, l2Opts, bridgeAmount)
	require.NoError(t, err, "first L1->L2 bridge+claim")
	assertClaimedOnL2(ctx, t, env, firstResult.GlobalIndex)

	firstClaimReceipt, err := env.Clients.L2.TransactionReceipt(ctx, firstResult.ClaimTxHash)
	require.NoError(t, err, "get first claim tx receipt")
	claimBlockC := firstClaimReceipt.BlockNumber.Uint64()
	log.Infof("[TestAggsenderClaimSyncerRecoveryAfterDBWipe] first claim global_index=%s claimed at L2 block=%d",
		firstResult.GlobalIndex.String(), claimBlockC)

	settledHeightH := waitForSettledIBEHeight(
		ctx, t, agglayerClient, env.L2.NetworkID, firstResult.GlobalIndex, claimSyncerSettleTimeout)
	log.Infof("[TestAggsenderClaimSyncerRecoveryAfterDBWipe] first claim settled at agglayer height=%d", settledHeightH)

	// Step 2: stop aggkit, wipe the claim syncer's local DB, and restart.
	require.NoError(t, env.StopAggkit(ctx), "stop aggkit")
	dataDir := env.GetAggkitDataDir()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		p := filepath.Join(dataDir, "claiml2sync.sqlite"+suffix)
		if rmErr := os.Remove(p); rmErr != nil && !os.IsNotExist(rmErr) {
			require.NoError(t, rmErr, "remove %s", p)
		}
	}
	restartTime := time.Now()
	require.NoError(t, env.StartAggkit(ctx), "restart aggkit after claim syncer DB wipe")
	sinceFlag := restartTime.Add(-claimSyncerRestartClockSkewMargin).UTC().Format(time.RFC3339)

	// Step 3: poll for the "Set next required block for claim syncer to N" line and assert
	// 0 < N <= C (proves the settled-certificate path was used, not InitialBlockNum=0), and that
	// no fallback WARN appears (proves the RPC lookup itself succeeded).
	nextBlockN := waitForSetNextRequiredBlockLog(ctx, t, env, sinceFlag, claimSyncerLogPollTimeout)
	require.Greater(t, nextBlockN, uint64(0),
		"claim syncer start block must be > 0: the settled IBE for global_index=%s resolved to a "+
			"real, non-zero L2 block, so SettledBlocks.EarliestBlock() must reflect that reference "+
			"instead of falling back to InitialBlockNum=0", firstResult.GlobalIndex.String())
	require.LessOrEqual(t, nextBlockN, claimBlockC,
		"claim syncer start block (%d) must be <= the already-settled claim's own block (%d)", nextBlockN, claimBlockC)

	recoveryLogs, err := env.DockerComposeLogs(ctx, "--no-log-prefix", "--since", sinceFlag, "aggkit-001")
	require.NoError(t, err, "docker compose logs aggkit-001 since restart")
	recoveryLogsText := string(recoveryLogs)
	require.NotContains(t, recoveryLogsText, claimSyncerFallbackWarnSubstr,
		"fallback WARN must not appear: the hardened RPC lookup should have found the settled IBE directly")

	// Step 4: the retry loop must not spin.
	failureCount := strings.Count(recoveryLogsText, claimSyncerRetryFailureSubstr)
	require.LessOrEqual(t, failureCount, claimSyncerMaxRetryFailures,
		"claim syncer start-block retry loop must not spin: saw %d '%s' lines since restart",
		failureCount, claimSyncerRetryFailureSubstr)

	// Step 5 (storage-level cross-check): the claim syncer's own DB should have durably persisted
	// the claim at N, the block the setter bootstrapped. This does NOT check MIN(block.num) == N:
	// see assertClaimSyncerHasClaimAtBlock's doc comment for why that would not be meaningful here.
	assertClaimSyncerHasClaimAtBlock(t, dataDir, nextBlockN)

	// Step 6: prove aggsender is alive afterwards by bridging + claiming again and waiting for a
	// settled certificate whose height exceeds H and whose settled IBE is this new claim.
	log.Infof("[TestAggsenderClaimSyncerRecoveryAfterDBWipe] sending second L1->L2 bridge+claim (post-recovery)")
	secondResult, err := BridgeL1ToL2WithResult(ctx, env, l1Opts, l2Opts, bridgeAmount)
	require.NoError(t, err, "second L1->L2 bridge+claim after recovery")
	assertClaimedOnL2(ctx, t, env, secondResult.GlobalIndex)

	settledHeight2 := waitForSettledIBEHeight(
		ctx, t, agglayerClient, env.L2.NetworkID, secondResult.GlobalIndex, claimSyncerSettleTimeout)
	require.Greater(t, settledHeight2, settledHeightH,
		"post-recovery claim's settlement height (%d) must exceed the pre-wipe settlement height (%d)",
		settledHeight2, settledHeightH)
	log.Infof("[TestAggsenderClaimSyncerRecoveryAfterDBWipe] post-recovery claim settled at agglayer height=%d",
		settledHeight2)
}

// newTestAgglayerClient builds a minimal AgglayerClientInterface connected to the env's AggLayer
// gRPC endpoint, read from summary.json the same way prepareAgglayerOnlyConfigPath
// (backwardforwardlet_test.go) does for the backward_forward_let tool. This test only needs
// GetNetworkInfo, so it builds the client directly instead of standing up the heavier
// tools/backward_forward_let.Env (L2 bridge bindings, key management, etc.), which it does not
// otherwise need. summaryForBFLToolConfig is reused from backwardforwardlet_test.go (same
// package) rather than redeclared.
func newTestAgglayerClient(t *testing.T, env *envs.Env) agglayer.AgglayerClientInterface {
	t.Helper()
	summaryPath := filepath.Join(env.EnvDir, "summary.json")
	summaryData, err := os.ReadFile(summaryPath)
	require.NoError(t, err, "read summary.json")
	var summary summaryForBFLToolConfig
	require.NoError(t, json.Unmarshal(summaryData, &summary), "unmarshal summary.json")
	grpcURL := summary.Networks.Agglayer.Services.GrpcRPC.External
	require.NotEmpty(t, grpcURL, "agglayer gRPC URL not found in summary.json")

	cfg := agglayer.ClientConfig{
		GRPC: &aggkitgrpc.ClientConfig{
			URL:               grpcURL,
			MinConnectTimeout: configtypes.Duration{Duration: 5 * time.Second},
			RequestTimeout:    configtypes.Duration{Duration: 300 * time.Second},
			UseTLS:            false,
		},
	}
	client, err := agglayer.NewAgglayerClient(cfg, log.GetDefaultLogger())
	require.NoError(t, err, "create agglayer client")
	return client
}

// waitForSettledIBEHeight polls the AggLayer (via GetNetworkInfo) until it reports a settled
// certificate whose SettledImportedBridgeExit.GlobalIndex matches targetGlobalIndex, and returns
// that certificate's settled height. GetNetworkInfo is used (rather than
// GetLatestSettledCertificateHeader) because only NetworkInfo carries SettledImportedBridgeExit;
// CertificateHeader does not (confirmed by reading agglayer/types/types.go).
func waitForSettledIBEHeight(
	ctx context.Context, t *testing.T,
	agglayerClient agglayer.AgglayerClientInterface,
	l2NetworkID uint32,
	targetGlobalIndex *big.Int,
	timeout time.Duration,
) uint64 {
	t.Helper()
	var settledHeight uint64
	err := pollWithBackoff(ctx, timeout, backoffInitial, backoffMax,
		fmt.Sprintf("settled-ibe-%s", targetGlobalIndex.String()),
		func() (bool, error) {
			info, infoErr := agglayerClient.GetNetworkInfo(ctx, l2NetworkID)
			if infoErr != nil {
				log.Debugf("[waitForSettledIBEHeight] GetNetworkInfo error (retrying): %v", infoErr)
				return false, nil
			}
			if info.SettledHeight == nil || info.SettledImportedBridgeExit == nil ||
				info.SettledImportedBridgeExit.GlobalIndex == nil {
				return false, nil
			}
			if info.SettledImportedBridgeExit.GlobalIndex.Cmp(targetGlobalIndex) != 0 {
				return false, nil
			}
			settledHeight = *info.SettledHeight
			return true, nil
		})
	require.NoError(t, err, "wait for settled IBE global_index=%s on agglayer", targetGlobalIndex.String())
	return settledHeight
}

// waitForSetNextRequiredBlockLog polls aggkit-001's docker compose logs (restricted to lines since
// sinceFlag, an RFC3339 timestamp) for the "Set next required block for claim syncer to N" line
// and returns N. The last match is used defensively, though since sinceFlag scopes the log window
// to just after our own StartAggkit call there should only ever be one.
func waitForSetNextRequiredBlockLog(
	ctx context.Context, t *testing.T, env *envs.Env, sinceFlag string, timeout time.Duration,
) uint64 {
	t.Helper()
	var nextBlock uint64
	err := pollWithBackoff(ctx, timeout, backoffInitial, backoffMax, "claim-syncer-next-required-block",
		func() (bool, error) {
			out, logsErr := env.DockerComposeLogs(ctx, "--no-log-prefix", "--since", sinceFlag, "aggkit-001")
			if logsErr != nil {
				log.Debugf("[waitForSetNextRequiredBlockLog] docker compose logs error (retrying): %v", logsErr)
				return false, nil
			}
			matches := setNextRequiredBlockLogRegex.FindAllStringSubmatch(string(out), -1)
			if len(matches) == 0 {
				return false, nil
			}
			last := matches[len(matches)-1]
			n, parseErr := strconv.ParseUint(last[1], 10, 64)
			if parseErr != nil {
				return false, fmt.Errorf("parse block number from log match %q: %w", last[0], parseErr)
			}
			nextBlock = n
			return true, nil
		})
	require.NoError(t, err, "wait for 'Set next required block for claim syncer' log line since %s", sinceFlag)
	return nextBlock
}

// assertClaimSyncerHasClaimAtBlock opens the claim syncer's SQLite DB directly, read-only (host-side,
// via the aggkit container's bind-mounted /tmp, same access pattern GetAggsenderDBPath documents for
// aggsender.sqlite) and asserts that a claim was durably persisted at expectedBlock, the block the
// setter chose to bootstrap.
//
// This deliberately does NOT assert MIN(block.num) == expectedBlock (an earlier version of this
// check did). That assertion is not meaningful in this env: AutoStart=true (the production shape
// #1842 is about) makes claimsync.(*ClaimSync).Start seed its own EVMDriver with InitialBlockNum
// independently of whatever the setter computes, and sync/evmdownloader.go force-reports
// InitialBlockNum (0 here) as a block row on its very first iteration regardless of whether it has
// events - so MIN(block.num) is expected to be 0 (or whatever InitialBlockNum is), not
// expectedBlock, as soon as that independent backfill has made any progress at all. It only
// happened to equal expectedBlock in an earlier run because the whole (short) test chain fit in a
// single downloader chunk.
//
// What the fix this test exists for (S12c: idempotent InsertClaim/InsertUnsetClaim/InsertSetClaim,
// see claimsync/storage/storage.go) actually guarantees is that expectedBlock's claim event
// survives being bootstrapped by both the AutoStart goroutine and the setter without one write
// clobbering the other with a UNIQUE constraint error. expectedBlock is, by construction, the
// settled imported bridge exit's block, so it necessarily carries a claim event - checking
// GetClaims([expectedBlock, expectedBlock]) is non-empty verifies exactly that, directly, instead
// of via an unrelated and timing-dependent property of the block table.
//
// This is safe to run while aggkit keeps writing to the same file because the connection below is
// opened genuinely read-only (mode=ro), which SQLite allows alongside a concurrent WAL writer,
// rather than through claimsyncstorage.NewStandalone, which would run migrations and open with
// _txlock=immediate -- appropriate for a writer, not a verification-only reader. The handle is
// closed via defer once this assertion is done.
func assertClaimSyncerHasClaimAtBlock(t *testing.T, dataDir string, expectedBlock uint64) {
	t.Helper()
	dbPath := filepath.Join(dataDir, "claiml2sync.sqlite")
	database, err := sql.Open("sqlite3", fmt.Sprintf(
		"file:%s?mode=ro&_journal_mode=WAL&_busy_timeout=30000", dbPath))
	require.NoError(t, err, "open claim syncer DB read-only at %s", dbPath)
	defer database.Close()
	store, err := claimsyncstorage.New(log.GetDefaultLogger(), database, claimSyncerRecoveryDBOwnerName, 5*time.Second)
	require.NoError(t, err, "wrap claim syncer DB at %s", dbPath)
	claims, err := store.GetClaims(context.Background(), nil, expectedBlock, expectedBlock)
	require.NoError(t, err, "GetClaims at block %d", expectedBlock)
	require.NotEmpty(t, claims,
		"claim syncer DB must have persisted a claim at the bootstrapped block %d (the settled IBE "+
			"block); an empty result here would mean the two-writer claim-insert bootstrap race "+
			"silently dropped it", expectedBlock)
}
