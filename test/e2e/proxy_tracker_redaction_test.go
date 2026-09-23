package e2e

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

const (
	// proxyRedactionUnreachableRPCURL/BridgeURL are the network-2 RPCURLs/BridgeURLs entries
	// substituted into aggkit-proxy's config. They use two
	// distinct, DNS-unresolvable ".invalid" hostnames -- a fast NXDOMAIN rather than the
	// connect-timeout stall a blackholed IP would cause -- so both the JSON-RPC and the
	// bridge-service failure paths are exercised and can be asserted on separately. Ports match
	// the entries they replace so only the host changes.
	proxyRedactionUnreachableRPCURL    = "http://unreachable-2.invalid:8545"
	proxyRedactionUnreachableBridgeURL = "http://unreachable-bridge-2.invalid:5577"

	// proxyRedactionRPCConfigEntry/BridgeConfigEntry are the exact, unedited lines in
	// aggkit-proxy.toml for network 2 that get rewritten (see the file's
	// [BridgeServiceFinder.RPCURLs]/[BridgeServiceFinder.BridgeURLs] tables).
	proxyRedactionRPCConfigEntry    = `2 = "http://l2-anvil-002:8545"`
	proxyRedactionBridgeConfigEntry = `2 = "http://aggkit-002:5577"`

	// proxyRedactionRestartWait/RestoreWait bound the aggkit-proxy restart used to pick up the
	// edited (and later restored) config; RestartAggkitProxyWithConfig itself does not wait for
	// readiness, so these only bound the docker compose restart call.
	proxyRedactionRestartWait = 2 * time.Minute
	proxyRedactionRestoreWait = 2 * time.Minute

	// proxyRedactionErrorWait bounds how long the test polls the tracker for the injected
	// network-2 failure to surface as a step- or tx-level error.
	proxyRedactionErrorWait = 3 * time.Minute
)

// collectErrorDescriptions gathers every ErrorStep.Description string carried by data: the
// tx-level error (if any) first, then every all_steps[*] step-level error, in response order.
func collectErrorDescriptions(data *trackerTrackingData) []string {
	var out []string
	if data.Error != nil {
		out = append(out, data.Error.Description...)
	}
	for _, step := range data.AllSteps {
		if step.Error != nil {
			out = append(out, step.Error.Description...)
		}
	}
	return out
}

// fetchTrackingRawBody calls the same tracker endpoint as fetchTrackingData but returns the raw
// response body instead of decoding it, so TestProxyTrackerRedactsURLs can additionally check for
// a leaked "://" anywhere in the JSON -- covering any field the typed struct walk might miss.
func fetchTrackingRawBody(ctx context.Context, networkID uint32, txHash common.Hash) (string, error) {
	url := fmt.Sprintf("%s/tracker/v1/network/%d/tx/%s", proxyTrackerBaseURL, networkID, txHash.Hex())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET tracker status returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

// TestProxyTrackerRedactsURLs exercises #1845: the bridge tracker's client-facing error
// descriptions must never leak an internal URL, host or port, even though the underlying error
// legitimately contains one.
//
// Network 2's RPC and bridge-service URLs in aggkit-proxy's
// bind-mounted config are rewritten to point at two distinct, unresolvable ".invalid" hosts,
// aggkit-proxy-001 is restarted to pick up the change (a single restart is enough -- the config
// is read fresh at process start, no stop/start pair needed), and a plain L1->network2 bridge is
// sent. The tracker resolves the bridge via its own L1 BridgeEventSource independently of network
// 2's reachability, but a later resolution step needs to reach network 2 (proxied) -- confirmed by
// a manual probe of this env, which observed the proxy's own (unredacted) log:
// "forwarding GET ... to http://unreachable-2.invalid:8545 failed: dial tcp: lookup
// unreachable-2.invalid on 127.0.0.11:53: no such host" -- and that raw host must not reach the
// tracker API response.
//
// This cannot pass vacuously: besides the negative assertion (none of the sensitive tokens
// appear), it asserts POSITIVELY that the redaction placeholder
// (aggkitcommon.RedactedURLPlaceholder / RedactedHostPlaceholder) appears at least once across the
// collected error descriptions, proving a URL-bearing failure path actually ran. A build with
// redaction disabled would fail this test: the raw ".invalid" host and "://" would appear in the
// error description(s) instead of the placeholder, tripping the negative assertions, and (were
// those hypothetically skipped too) the placeholder would never appear at all, tripping the
// positive one.
func TestProxyTrackerRedactsURLs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")
	if testEnv.L2B == nil {
		t.Skip("proxy tracker redaction test requires a multi-chain env (L2B must be non-nil)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	require.NoError(t, waitForTrackerReady(ctx), "tracker health check never succeeded before test start")

	configPath := testEnv.GetAggkitProxyConfigPath()
	originalConfig, err := os.ReadFile(configPath)
	require.NoError(t, err, "read aggkit-proxy config")

	t.Cleanup(func() {
		restoreCtx, cancelRestore := context.WithTimeout(context.Background(), proxyRedactionRestoreWait)
		defer cancelRestore()
		if err := testEnv.RestartAggkitProxyWithConfig(restoreCtx, func(p string) error {
			return os.WriteFile(p, originalConfig, 0o600)
		}); err != nil {
			// A failed restore leaves the tracked aggkit-proxy.toml pointed at the
			// unreachable ".invalid" hosts, which a later local run would then fail on (the
			// require.Contains precondition above) for an unrelated reason. Surface it as a
			// test failure rather than silently logging it, so it cannot go unnoticed.
			t.Errorf("failed to restore aggkit-proxy config after redaction test: %v", err)
			return
		}
		if err := waitForTrackerReady(restoreCtx); err != nil {
			t.Errorf("tracker health check never recovered after restoring aggkit-proxy config: %v", err)
		}
	})

	// Rewrite network 2's RPC and bridge-service URLs by exact string replacement, then restart
	// aggkit-proxy-001 so it picks the change up.
	patched := string(originalConfig)
	require.Contains(t, patched, proxyRedactionRPCConfigEntry,
		"network 2 RPC URL entry not found in aggkit-proxy config")
	require.Contains(t, patched, proxyRedactionBridgeConfigEntry,
		"network 2 bridge URL entry not found in aggkit-proxy config")
	patched = strings.Replace(patched, proxyRedactionRPCConfigEntry,
		fmt.Sprintf(`2 = "%s"`, proxyRedactionUnreachableRPCURL), 1)
	patched = strings.Replace(patched, proxyRedactionBridgeConfigEntry,
		fmt.Sprintf(`2 = "%s"`, proxyRedactionUnreachableBridgeURL), 1)

	restartCtx, cancelRestart := context.WithTimeout(ctx, proxyRedactionRestartWait)
	defer cancelRestart()
	require.NoError(t, testEnv.RestartAggkitProxyWithConfig(restartCtx, func(p string) error {
		return os.WriteFile(p, []byte(patched), 0o600)
	}), "restart aggkit-proxy with unreachable network-2 URLs")
	require.NoError(t, waitForTrackerReady(ctx), "tracker health check never recovered after restart")

	// Bridge L1 -> network 2. Only the tx hash is needed (the tracker resolves it via its own L1
	// BridgeEventSource), so this sends and mines the bridge tx directly with the same primitives
	// BridgeL1NoClaim/BridgeL1ToL2WithResult (bridge_utils.go) use, rather than their post-mine
	// indexing waits, which target env.L2 (network 1) and are irrelevant here since this bridge is
	// never claimed.
	l1Opts := *testEnv.L1.Transactor
	l2BOpts := *testEnv.L2B.Transactor
	bridgeAmount := big.NewInt(1e14) // 0.0001 ETH

	callOpts := &bind.CallOpts{Context: ctx}
	destNetworkID, err := testEnv.L2B.Contracts.L2Bridge.NetworkID(callOpts)
	require.NoError(t, err, "get network 2 (L2B) network ID")

	l1Opts.Value = bridgeAmount
	bridgeTx, err := testEnv.L1.Contracts.Bridge.BridgeAsset(
		&l1Opts, destNetworkID, l2BOpts.From, bridgeAmount, common.Address{}, true, nil,
	)
	l1Opts.Value = nil
	require.NoError(t, err, "send L1->network2 bridge tx")

	receipt, err := waitMinedL1WithDiagnostics(ctx, testEnv.Clients.L1, bridgeTx, l1MineDiagnosticsWait)
	require.NoError(t, err, "wait for L1->network2 bridge tx to be mined")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "L1->network2 bridge tx failed")

	txHash := bridgeTx.Hash()
	t.Logf("bridge tx: %s", txHash.Hex())

	const l1NetworkID = 0
	var allDescriptions []string
	err = pollWithBackoff(ctx, proxyRedactionErrorWait, backoffInitial, backoffMax,
		"tracker surfaces the injected network-2 failure", func() (bool, error) {
			data, ferr := fetchTrackingData(ctx, l1NetworkID, txHash)
			if ferr != nil {
				return false, nil //nolint:nilerr // registration/resolution still in progress
			}
			allDescriptions = collectErrorDescriptions(data)
			return len(allDescriptions) > 0, nil
		})
	require.NoError(t, err, "tracker never surfaced an error for the L1->network2 bridge")
	require.NotEmpty(t, allDescriptions, "no error descriptions collected")

	sensitiveTokens := []string{
		"://",
		"unreachable-2.invalid",
		"unreachable-bridge-2.invalid",
		":8545",
		":5577",
	}
	foundPlaceholder := false
	for _, desc := range allDescriptions {
		for _, token := range sensitiveTokens {
			require.NotContains(t, desc, token, "error description leaked a sensitive token: %q", desc)
		}
		if strings.Contains(desc, aggkitcommon.RedactedURLPlaceholder) ||
			strings.Contains(desc, aggkitcommon.RedactedHostPlaceholder) {
			foundPlaceholder = true
		}
	}
	require.True(t, foundPlaceholder,
		"no error description contained the redaction placeholder; the URL-bearing failure path may not have run: %v",
		allDescriptions)

	// Also check the raw response body: covers any field the typed struct walk above might miss.
	body, err := fetchTrackingRawBody(ctx, l1NetworkID, txHash)
	require.NoError(t, err, "fetch raw tracking body")
	require.NotContains(t, body, "://", "raw tracker response body contains an unredacted URL")
}
