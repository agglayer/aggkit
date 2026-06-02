package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/stretchr/testify/require"
)

// certSettlementTestTimeout bounds the overall test. It is comfortably below the suite's
// -timeout 30m while leaving room for an optional bridge trigger plus the settlement wait.
const certSettlementTestTimeout = 20 * time.Minute

// certSettlementWaitTimeout bounds the wait for a settled certificate on the agglayer. It mirrors
// the original e2e-pp.bats budget of 600s (10 min) for "agglayer_certificates_monitor.sh 1 600"
// but is given extra slack (15 min) since a fresh op-pp may need to mine genesis/bridge activity
// before the aggsender produces and the agglayer settles its first certificate.
const certSettlementWaitTimeout = 15 * time.Minute

// certSettlementTarget is the number of settled certificates the test waits for, matching the bats
// invocation "agglayer_certificates_monitor.sh 1 600 <l2_network_id>" (target = 1).
const certSettlementTarget = uint64(1)

// certSettlementStatusSettled is the agglayer certificate status string that indicates a
// certificate has settled on L1. The agglayer read RPC returns the status as a string enum
// (e.g. "Pending", "Proven", "Settled") in the certificate header JSON.
const certSettlementStatusSettled = "Settled"

// certSettlementBridgeAmount is the small ETH amount bridged L1->L2 to keep the network warm so the
// aggsender has activity to certify. Kept minimal.
var certSettlementBridgeAmount = big.NewInt(1000000000000000) // 0.001 ETH

// agglayerCertificateHeader is the subset of the agglayer read RPC
// interop_getLatestKnownCertificateHeader response that the settlement check needs. The agglayer
// returns a certificate header object with a numeric "height" and a string "status"; this mirrors
// what the legacy agglayer_certificates_monitor.sh extracts via jq (.height and .status).
type agglayerCertificateHeader struct {
	Height uint64 `json:"height"`
	Status string `json:"status"`
}

// TestCertificateSettlement ports the "Verify certificate settlement" case from e2e-pp.bats: it
// waits until the agglayer settles at least one PP certificate for the L2 network. The bats test ran
// agglayer_certificates_monitor.sh with target=1, which polls the agglayer read RPC method
// interop_getLatestKnownCertificateHeader and succeeds as soon as a certificate at height 0 reaches
// status "Settled" (or any height >= 1 exists). This test replicates that condition directly against
// the agglayer read RPC (the authoritative settlement signal), rather than reading the aggsender
// SQLite DB. Like the bats test, it does not itself force a certificate: the running aggsender
// produces and settles PP certificates from network activity. It drives a single light L1->L2
// bridge-and-claim to keep the network warm, then waits for settlement, and leaves the env healthy
// for later tests.
func TestCertificateSettlement(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	ctx, cancel := context.WithTimeout(context.Background(), certSettlementTestTimeout)
	defer cancel()

	// Resolve the agglayer read RPC URL (compose port 4444) from the env's summary.json. This is the
	// same endpoint the legacy bats monitor used (networks.agglayer.read_rpc.external).
	readRPCURL := agglayerReadRPCURL(t, env)
	log.Infof("[TestCertificateSettlement] using agglayer read RPC %s for L2 network id %d",
		readRPCURL, env.L2.NetworkID)

	// Drive a minimal L1->L2 bridge-and-claim so both directions exercise the network. A fresh
	// op-pp may otherwise have little activity; this keeps the network warm.
	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err, "checkout L1 key")
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer env.Keys.L2Keys.Return(l2Key)

	result := bridgeETHL1ToL2AndClaim(ctx, t, env, l1Opts, l2Opts, certSettlementBridgeAmount)
	log.Infof("[TestCertificateSettlement] L1->L2 bridge complete: deposit_count=%d global_index=%s",
		result.DepositCount, result.GlobalIndex.String())

	// Assert the agglayer settles at least one certificate within the bounded timeout.
	settledHeight := waitForAgglayerSettledCertificate(ctx, t, readRPCURL, env.L2.NetworkID,
		certSettlementTarget, certSettlementWaitTimeout)
	log.Infof("[TestCertificateSettlement] observed settled certificate at height %d", settledHeight)

	// Leave the env healthy so later tests and the post-suite TestMain check pass.
	assertNetworkHealthy(ctx, t, env)
}

// agglayerReadRPCURL reads the agglayer read RPC external URL (compose port 4444) from the env's
// summary.json. This is the endpoint exposing interop_getLatestKnownCertificateHeader, which the
// legacy agglayer_certificates_monitor.sh polled.
func agglayerReadRPCURL(t *testing.T, env *envs.Env) string {
	t.Helper()
	summaryPath := filepath.Join(env.EnvDir, "summary.json")
	data, err := os.ReadFile(summaryPath)
	require.NoError(t, err, "read summary.json at %s", summaryPath)

	var summary struct {
		Networks struct {
			Agglayer struct {
				Services struct {
					ReadRPC struct {
						External string `json:"external"`
					} `json:"read_rpc"`
				} `json:"services"`
			} `json:"agglayer"`
		} `json:"networks"`
	}
	require.NoError(t, json.Unmarshal(data, &summary), "parse summary.json")

	url := summary.Networks.Agglayer.Services.ReadRPC.External
	require.NotEmpty(t, url, "agglayer read RPC external URL not found in summary.json")
	return url
}

// waitForAgglayerSettledCertificate polls the agglayer read RPC method
// interop_getLatestKnownCertificateHeader for the given L2 network id until at least `target`
// certificates are settled, then returns the latest known certificate height. It replicates the
// success condition of the legacy agglayer_certificates_monitor.sh: succeed when
// height > target-1, OR (height == target-1 AND status == "Settled"). For target=1 that means:
// succeed as soon as a certificate at height 0 reaches status "Settled", or any height >= 1 exists.
// It require.NoErrors on timeout, so reaching the return means the settlement condition was met.
func waitForAgglayerSettledCertificate(
	ctx context.Context, t *testing.T,
	readRPCURL string, l2NetworkID uint32, target uint64, timeout time.Duration,
) uint64 {
	t.Helper()
	log.Infof("[waitForAgglayerSettledCertificate] waiting for %d settled cert(s) on network %d",
		target, l2NetworkID)

	var lastHeight uint64
	err := pollWithBackoff(ctx, timeout, backoffInitial, backoffMax, "agglayer settled certificate",
		func() (bool, error) {
			header, pollErr := getLatestKnownCertificateHeader(ctx, readRPCURL, l2NetworkID)
			if pollErr != nil {
				// Non-fatal: the network may have no certificate yet (the agglayer returns an error
				// for an unknown network) or the RPC may be transiently unavailable. Keep polling.
				log.Debugf("[waitForAgglayerSettledCertificate] header error (retrying): %v", pollErr)
				return false, nil
			}
			if header == nil {
				// No certificate header yet.
				return false, nil
			}
			lastHeight = header.Height
			log.Debugf("[waitForAgglayerSettledCertificate] latest known cert height=%d status=%s",
				header.Height, header.Status)
			// height > target-1: at least `target` certificates exist (any later height is settled).
			if header.Height > target-1 {
				return true, nil
			}
			// height == target-1 AND status == "Settled": the target-th certificate has settled.
			if header.Height == target-1 && header.Status == certSettlementStatusSettled {
				return true, nil
			}
			return false, nil
		})
	require.NoError(t, err, "wait for %d settled certificate(s) on agglayer within %s", target, timeout)
	return lastHeight
}

// getLatestKnownCertificateHeader calls interop_getLatestKnownCertificateHeader on the agglayer read
// RPC for the given L2 network id and decodes the certificate header. It returns (nil, nil) when the
// JSON-RPC result is null (no certificate known yet for the network), and an error when the RPC call
// or the response itself reports a failure.
func getLatestKnownCertificateHeader(
	ctx context.Context, readRPCURL string, l2NetworkID uint32,
) (*agglayerCertificateHeader, error) {
	response, err := rpc.JSONRPCCallWithContext(
		ctx, readRPCURL, "interop_getLatestKnownCertificateHeader", l2NetworkID)
	if err != nil {
		return nil, fmt.Errorf("interop_getLatestKnownCertificateHeader RPC call: %w", err)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("interop_getLatestKnownCertificateHeader returned error: %v", response.Error)
	}
	// A null result means no certificate is known for this network yet.
	if len(response.Result) == 0 || string(response.Result) == "null" {
		return nil, nil //nolint:nilnil // (nil, nil) signals "no certificate yet" to the poller.
	}
	var header agglayerCertificateHeader
	if err := json.Unmarshal(response.Result, &header); err != nil {
		return nil, fmt.Errorf("unmarshal certificate header: %w", err)
	}
	return &header, nil
}
