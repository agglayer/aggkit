package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-rpc/rpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	ethrpc "github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/require"
)

const (
	finalityKnobsAggkitService = "aggkit-001"
	finalityKnobsNetworkKey    = "001"
	// finalityKnobsWindowAfterClaim is the wall-clock window the claim must stay unprovable against the
	// aggsender's selected root, so that at least one certificate build lands inside it.
	finalityKnobsWindowAfterClaim = 120 * time.Second
	// finalityKnobsMaxTestBudget bounds n*blockTime; if the measured L1 makes the offset slower than
	// this, the env snapshot changed and the test must be re-sized rather than silently waiting.
	finalityKnobsMaxTestBudget   = 12 * time.Minute
	finalityKnobsRestartWait     = 6 * time.Minute // > envs.serviceReadyTimeout (4m)
	finalityKnobsRestoreWait     = 6 * time.Minute
	finalityKnobsRPCReadyWait    = 2 * time.Minute
	finalityKnobsLogPollInterval = 5 * time.Second
	// finalityKnobsBlockTimeSampleBlocks is how many new L1 blocks are timed against the wall clock to
	// estimate the block time. Header timestamps are deliberately not used: the anvil snapshot's
	// pre-mined blocks carry the timestamps of the day the snapshot was taken, so any sample that
	// straddles the snapshot boundary yields a block time of weeks.
	finalityKnobsBlockTimeSampleBlocks = 5
	// finalityKnobsBlockTimeMeasureWait bounds the measurement; an L1 that cannot mine the sample
	// within it is far too slow for this test and must fail loudly.
	finalityKnobsBlockTimeMeasureWait  = 3 * time.Minute
	finalityKnobsBlockTimePollInterval = 500 * time.Millisecond
	// finalityKnobsMinL1Headroom is the minimum L1 block number that latest-N must stay above so the
	// root at latest-N exists: the syncer only indexes leaves from L1InfoTreeSync.InitialBlock = "60"
	// (test/e2e/envs/anvil-2chains/config/001/aggkit-config.toml) and the first leaves are emitted by
	// the deployment transactions in the anvil snapshot's pre-mined blocks (snapshot boundary: 236).
	// 100 clears InitialBlock with room for that deployment; measured on the snapshot env:
	// latest=254, N=127, so latest-N=127 passed with 27 blocks to spare. A snapshot shorter than that,
	// or a larger N, is what this guard catches before the aggsender fails to find the root.
	finalityKnobsMinL1Headroom = 100
	finalityKnobsBridgeCount   = 2

	// Log fragments the test couples to (see aggsender/flows/adjust_block_range.go and
	// aggsender/query/l1info_tree_data_query.go).
	finalityKnobsTrimLogFragment      = "is not yet under the selected L1 info root"
	finalityKnobsHardErrorLogFragment = "exists on L1 but cannot be proved against selected root"
	finalityKnobsGuardLogFragment     = "block finality misconfiguration"
)

// TestAggsenderIndependentL1FinalityKnobs restarts aggkit-001 with asymmetric L1 finality knobs
// (L1Multidownloader at its FinalizedBlock default, L1InfoTreeSync at LatestBlock and the aggsender
// proving against LatestBlock/-N), bridges and claims inside the resulting window, and asserts that:
//   - the aggsender starts (no "block finality misconfiguration" guard error, aggkit#1846);
//   - a claim whose GER is not yet under the selected root trims the certificate instead of failing
//     hard (aggkit#1847);
//   - a certificate covering the claims still settles.
//
// N and every timeout are derived from the L1 latest-to-finalized lag and block time measured at
// runtime, because the anvil L1 timing is not knowable statically.
func TestAggsenderIndependentL1FinalityKnobs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")

	// Per-phase timeouts below are the real bounds; this is a hard ceiling below the CI job timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Step 1: measure the L1 to derive N and every timeout.
	latest, err := testEnv.Clients.L1.HeaderByNumber(ctx, nil)
	require.NoError(t, err, "L1 latest header")
	finalized, err := testEnv.Clients.L1.HeaderByNumber(ctx, big.NewInt(int64(ethrpc.FinalizedBlockNumber)))
	require.NoError(t, err, "L1 finalized header")
	require.GreaterOrEqual(t, latest.Number.Uint64(), finalized.Number.Uint64())
	lag := latest.Number.Uint64() - finalized.Number.Uint64()

	blockTime := measureL1BlockTime(ctx, t, latest.Number.Uint64())
	windowBlocks := uint64(finalityKnobsWindowAfterClaim/blockTime) + 1
	// 3*lag guarantees the root at latest-N is already finalized; windowBlocks guarantees the claim
	// stays not-yet-provable for at least finalityKnobsWindowAfterClaim regardless of the injection delay.
	n := 3*lag + windowBlocks

	require.LessOrEqual(t, time.Duration(n)*blockTime, finalityKnobsMaxTestBudget,
		"L1 lag %d blocks x block time %s makes N=%d too slow; re-size the test", lag, blockTime, n)
	require.Greater(t, latest.Number.Uint64(), n+finalityKnobsMinL1Headroom,
		"snapshot L1 too short for offset -%d", n)

	trimWait := time.Duration(n+lag)*blockTime + 3*time.Minute
	settleWait := time.Duration(2*n+2*lag)*blockTime + 5*time.Minute
	log.Infof("finality knobs: L1 latest=%d finalized=%d lag=%d blockTime=%s windowBlocks=%d N=%d "+
		"trimWait=%s settleWait=%s",
		latest.Number.Uint64(), finalized.Number.Uint64(), lag, blockTime, windowBlocks, n, trimWait, settleWait)

	// Step 2: restart aggkit-001 with the asymmetric knobs, registering the restore first.
	configPath := testEnv.GetAggkitConfigPathForNetwork(finalityKnobsNetworkKey)
	originalConfig, err := os.ReadFile(configPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), finalityKnobsRestoreWait)
		defer cancel()
		if err := testEnv.RestartAggkitServiceWithConfig(restoreCtx, finalityKnobsNetworkKey,
			func(p string) error {
				return os.WriteFile(p, originalConfig, 0o600)
			}); err != nil {
			t.Logf("failed to restore %s config after finality knobs test: %v", finalityKnobsAggkitService, err)
		}
	})

	// Single-line insertion right after each bare section header. "[AggSender]\n" cannot match the
	// "[AggSender.*]\n" subsections, and [L1Multidownloader] is deliberately left at its default.
	patched := string(originalConfig)
	require.Contains(t, patched, "[L1InfoTreeSync]\n")
	require.Contains(t, patched, "[AggSender]\n")
	patched = strings.Replace(patched, "[L1InfoTreeSync]\n",
		"[L1InfoTreeSync]\nBlockFinality = \"LatestBlock\"\n", 1)
	patched = strings.Replace(patched, "[AggSender]\n",
		fmt.Sprintf("[AggSender]\nBlockFinalityForL1InfoTree = \"LatestBlock/-%d\"\n", n), 1)

	restartedAt := time.Now().UTC().Add(-5 * time.Second)
	restartCtx, cancelRestart := context.WithTimeout(ctx, finalityKnobsRestartWait)
	defer cancelRestart()
	restartErr := testEnv.RestartAggkitServiceWithConfig(restartCtx, finalityKnobsNetworkKey,
		func(p string) error {
			return os.WriteFile(p, []byte(patched), 0o600)
		})

	// Check the guard first so a startup failure is attributed to aggkit#1846 rather than a timeout.
	logs := aggkitLogsSince(ctx, t, restartedAt)
	require.NotContains(t, logs, finalityKnobsGuardLogFragment,
		"aggsender rejected {L1Multidownloader=FinalizedBlock, L1InfoTreeSync=LatestBlock, "+
			"AggSender=LatestBlock/-%d} (aggkit#1846)", n)
	require.NoError(t, restartErr, "restart %s with asymmetric finality knobs", finalityKnobsAggkitService)

	// Step 3: the aggsender RPC is up.
	require.NoError(t, pollWithBackoff(ctx, finalityKnobsRPCReadyWait, backoffInitial, backoffMax, "aggsender-rpc",
		func() (bool, error) {
			resp, err := rpc.JSONRPCCall(testEnv.AggsenderRPCURL, "aggsender_status")
			return err == nil && resp.Error == nil, nil
		}), "aggsender RPC did not come up within %s", finalityKnobsRPCReadyWait)

	// Step 4: inject claims inside the window.
	l1Opts, l1Key, err := testEnv.Keys.L1Keys.Checkout()
	require.NoError(t, err)
	defer testEnv.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := testEnv.Keys.L2Keys.Checkout()
	require.NoError(t, err)
	defer testEnv.Keys.L2Keys.Return(l2Key)

	for i := 1; i <= finalityKnobsBridgeCount; i++ {
		require.NoError(t, BridgeL1ToL2(ctx, testEnv, l1Opts, l2Opts), "L1->L2 bridge+claim #%d", i)
	}
	// BridgeL1ToL2 waits for the claim receipt, so the L2 head read here is >= the last claim's block.
	lastClaimL2Block, err := testEnv.Clients.L2.BlockNumber(ctx)
	require.NoError(t, err)
	log.Infof("finality knobs: %d L1->L2 bridges claimed, last claim L2 block <= %d",
		finalityKnobsBridgeCount, lastClaimL2Block)

	// Step 5: positive trim signal, negative hard-error signal.
	require.NoError(t, pollWithBackoff(ctx, trimWait, finalityKnobsLogPollInterval, finalityKnobsLogPollInterval,
		"trim-log", func() (bool, error) {
			logs := aggkitLogsSince(ctx, t, restartedAt)
			if strings.Contains(logs, finalityKnobsHardErrorLogFragment) {
				return false, fmt.Errorf("aggsender still hard-errors on a not-yet-provable claim (aggkit#1847)")
			}
			return strings.Contains(logs, finalityKnobsTrimLogFragment), nil
		}), "aggsender never trimmed the not-yet-provable claim within %s", trimWait)

	// Step 6: a certificate covering the claims settles.
	require.NoError(t, pollWithBackoff(ctx, settleWait, backoffInitial, backoffMax, "settled-cert",
		func() (bool, error) {
			last, ok := aggsenderCertificate(t, testEnv.AggsenderRPCURL, nil)
			if !ok {
				return false, nil
			}
			if certificateSettledCovering(last, lastClaimL2Block) {
				return true, nil
			}
			// ASAP mode may already have a newer pending certificate on top of the settled one.
			if last.Header.Height > 0 {
				prevHeight := last.Header.Height - 1
				prev, ok := aggsenderCertificate(t, testEnv.AggsenderRPCURL, &prevHeight)
				if ok && certificateSettledCovering(prev, lastClaimL2Block) {
					return true, nil
				}
			}
			return false, nil
		}), "no settled certificate with ToBlock >= %d within %s", lastClaimL2Block, settleWait)

	// Step 7: final log assertions over the whole patched run.
	logs = aggkitLogsSince(ctx, t, restartedAt)
	require.NotContains(t, logs, finalityKnobsHardErrorLogFragment)
	require.NotContains(t, logs, finalityKnobsGuardLogFragment)
	require.Contains(t, logs, finalityKnobsTrimLogFragment)
}

// measureL1BlockTime times the next finalityKnobsBlockTimeSampleBlocks L1 blocks after startBlock
// against the wall clock and returns the per-block interval, floored at 1s. Only block counts and
// wall-clock time enter the estimate, so pre-mined snapshot history cannot poison it and the result
// does not depend on when the test starts. It fails loudly if the L1 cannot mine the sample within
// finalityKnobsBlockTimeMeasureWait.
func measureL1BlockTime(ctx context.Context, t *testing.T, startBlock uint64) time.Duration {
	t.Helper()
	start := time.Now()
	var advanced uint64
	err := pollWithBackoff(ctx, finalityKnobsBlockTimeMeasureWait,
		finalityKnobsBlockTimePollInterval, finalityKnobsBlockTimePollInterval, "l1-block-time",
		func() (bool, error) {
			current, err := testEnv.Clients.L1.BlockNumber(ctx)
			if err != nil {
				return false, nil
			}
			if current > startBlock {
				advanced = current - startBlock
			}
			return advanced >= finalityKnobsBlockTimeSampleBlocks, nil
		})
	require.NoError(t, err, "L1 mined %d/%d blocks in %s; too slow for this test, re-size it",
		advanced, finalityKnobsBlockTimeSampleBlocks, finalityKnobsBlockTimeMeasureWait)
	return max(time.Second, (time.Since(start) / time.Duration(advanced)).Round(time.Second))
}

// aggkitLogsSince returns aggkit-001's container logs emitted after `since`, so assertions are
// scoped to the run with the patched config.
func aggkitLogsSince(ctx context.Context, t *testing.T, since time.Time) string {
	t.Helper()
	out, err := testEnv.DockerComposeLogs(ctx, "--no-log-prefix", "--since", since.Format(time.RFC3339),
		finalityKnobsAggkitService)
	require.NoError(t, err)
	return string(out)
}

// aggsenderCertificate fetches the certificate at height (or the last sent one when height is nil)
// through aggsender_getCertificateHeaderPerHeight. It returns ok=false on transient RPC errors and on
// "certificate not found", so callers can keep polling.
func aggsenderCertificate(t *testing.T, url string, height *uint64) (*aggsendertypes.Certificate, bool) {
	t.Helper()
	var (
		resp rpc.Response
		err  error
	)
	if height == nil {
		resp, err = rpc.JSONRPCCall(url, "aggsender_getCertificateHeaderPerHeight")
	} else {
		resp, err = rpc.JSONRPCCall(url, "aggsender_getCertificateHeaderPerHeight", *height)
	}
	if err != nil || resp.Error != nil {
		return nil, false
	}
	var cert aggsendertypes.Certificate
	require.NoError(t, json.Unmarshal(resp.Result, &cert))
	if cert.Header == nil {
		return nil, false
	}
	return &cert, true
}

// certificateSettledCovering reports whether cert is settled and its range reaches toBlock.
func certificateSettledCovering(cert *aggsendertypes.Certificate, toBlock uint64) bool {
	log.Debugf("finality knobs: certificate height=%d status=%s toBlock=%d",
		cert.Header.Height, cert.Header.Status, cert.Header.ToBlock)
	return cert.Header.Status == agglayertypes.Settled && cert.Header.ToBlock >= toBlock
}
