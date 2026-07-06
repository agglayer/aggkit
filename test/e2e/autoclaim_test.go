package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	autoclaimconfig "github.com/agglayer/aggkit/autoclaim/config"
	autoclaimpolicy "github.com/agglayer/aggkit/autoclaim/policy"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/stretchr/testify/require"
)

const (
	autoClaimAPIBaseURL      = "http://127.0.0.1:11579"
	bridgeServiceBaseURL     = "http://127.0.0.1:11577"
	autoClaimKeystorePass    = "pSnv6Dh5s9ahuzGzH9RoCDrKAMddaX3m"
	autoClaimBridgeAddr      = "0xC8cbEBf950B9Df44d987c8619f092beA980fF038"
	autoClaimL2RPC           = "http://op-geth-001:8545"
	autoClaimL2ChainID       = 2151908
	autoClaimRequestWait     = 8 * time.Minute
	autoClaimRestartWait     = 2 * time.Minute
	autoClaimRestoreWait     = 2 * time.Minute
	autoClaimBridgeAmountWei = 100000000000000
)

type autoClaimRequestResponse struct {
	ID                 string                     `json:"id"`
	Status             string                     `json:"status"`
	OriginNetwork      uint32                     `json:"origin_network"`
	DestinationNetwork uint32                     `json:"destination_network"`
	DepositCount       uint32                     `json:"deposit_count"`
	BridgeTxHash       string                     `json:"bridge_tx_hash"`
	ClaimTxHash        *string                    `json:"claim_tx_hash"`
	PolicyStatus       string                     `json:"policy_status"`
	PolicyDecision     *autoClaimDecisionResponse `json:"policy_decision"`
	LastError          string                     `json:"last_error"`
}

type autoClaimDecisionResponse struct {
	PolicyName string            `json:"policy_name"`
	Result     string            `json:"result"`
	Reason     string            `json:"reason"`
	Metadata   map[string]string `json:"metadata"`
}

func TestAutoClaimL1ToL2AllowAll(t *testing.T) {
	testAutoClaimL1ToL2(t, "allow-all", false)
}

func TestAutoClaimL1ToL2APIApprove(t *testing.T) {
	testAutoClaimL1ToL2(t, "api-approve", true)
}

func TestAutoClaimL1ToL2BasicFilter(t *testing.T) {
	confirmed := testAutoClaimL1ToL2(t, string(autoclaimconfig.PolicyNameBasicFilter), false)

	require.NotNil(t, confirmed.PolicyDecision)
	require.Equal(t, string(autoclaimconfig.PolicyNameBasicFilter), confirmed.PolicyDecision.PolicyName)
	require.Equal(t, autoclaimtypes.PolicyResultApproved.String(), confirmed.PolicyDecision.Result)
	require.Equal(t, autoclaimpolicy.ReasonBasicFilterApproved, confirmed.PolicyDecision.Reason)
	require.NotEmpty(t, confirmed.PolicyDecision.Metadata["gas_used"])
	require.Equal(t, "500000", confirmed.PolicyDecision.Metadata["max_gas"])
	require.Equal(t, "skipped", confirmed.PolicyDecision.Metadata["nested_bridge_detection"])
	require.Equal(
		t,
		string(autoclaimpolicy.NestedBridgeCallNotDetected),
		confirmed.PolicyDecision.Metadata["nested_bridge_call"],
	)
}

func testAutoClaimL1ToL2(t *testing.T, policyName string, approveThroughAPI bool) autoClaimRequestResponse {
	t.Helper()
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	env := loadAutoClaimTestEnv(t, ctx)
	enableAutoClaimForTest(t, ctx, env, policyName)
	waitForBridgeServiceSynced(ctx, t)

	l1Opts, l1Key, err := env.Keys.L1Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := env.Keys.L2Keys.Checkout()
	require.NoError(t, err)
	defer env.Keys.L2Keys.Return(l2Key)

	bridgeAmount := big.NewInt(autoClaimBridgeAmountWei)
	initialBalance, err := env.Clients.L2.BalanceAt(ctx, l2Opts.From, nil)
	require.NoError(t, err)

	label := fmt.Sprintf("autoclaim-%s", policyName)
	result, err := BridgeL1NoClaim(ctx, env, l1Opts, l2Opts, bridgeAmount, label)
	require.NoError(t, err)
	require.Empty(t, result.ClaimTxHash, "test helper must not manually claim on L2")

	requestKey := autoclaimtypes.DeriveRequestKey(
		result.Bridge.OriginNetwork,
		result.Bridge.DestinationNetwork,
		result.DepositCount,
	)
	if approveThroughAPI {
		waitForAutoClaimStatus(ctx, t, requestKey, autoclaimtypes.RequestStatusManualApprovalRequired)
		approveAutoClaimRequest(ctx, t, requestKey)
	}
	confirmed := waitForAutoClaimStatus(ctx, t, requestKey, autoclaimtypes.RequestStatusConfirmed)
	require.NotNil(t, confirmed.ClaimTxHash, "confirmed Auto Claim request should expose claim tx hash")
	require.Equal(t, string(result.Bridge.TxHash), confirmed.BridgeTxHash)

	assertClaimedOnL2(ctx, t, env, result.GlobalIndex)
	finalBalance, err := env.Clients.L2.BalanceAt(ctx, result.DestinationAddr, nil)
	require.NoError(t, err)
	require.GreaterOrEqual(t, new(big.Int).Sub(finalBalance, initialBalance).Cmp(bridgeAmount), 0)
	return confirmed
}

func loadAutoClaimTestEnv(t *testing.T, ctx context.Context) *envs.Env {
	t.Helper()
	loadCtx, loadCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer loadCancel()
	env, err := envs.LoadEnv(loadCtx, envs.EnvOpPP)
	require.NoError(t, err)

	checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer checkCancel()
	require.NoError(t, env.CheckEnv(checkCtx))

	testEnv = env
	return env
}

func enableAutoClaimForTest(t *testing.T, ctx context.Context, env *envs.Env, policyName string) {
	t.Helper()
	originalConfig, err := os.ReadFile(env.GetAggkitConfigPath())
	require.NoError(t, err)

	t.Cleanup(func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), autoClaimRestoreWait)
		defer cancel()
		if err := env.RestartAggkitWithConfig(restoreCtx, func(configPath string) error {
			return os.WriteFile(configPath, originalConfig, 0o600)
		}); err != nil {
			t.Logf("failed to restore aggkit config after Auto Claim test: %v", err)
		}
	})

	restartCtx, cancel := context.WithTimeout(ctx, autoClaimRestartWait)
	defer cancel()
	err = env.RestartAggkitWithConfig(restartCtx, func(configPath string) error {
		patched := patchAutoClaimConfig(string(originalConfig), autoClaimConfig(policyName, env.L2.NetworkID))
		return os.WriteFile(configPath, []byte(patched), 0o600)
	})
	require.NoError(t, err, "restart aggkit with Auto Claim %s policy", policyName)
}

func waitForAutoClaimStatus(
	ctx context.Context,
	t *testing.T,
	key autoclaimtypes.RequestKey,
	expected autoclaimtypes.RequestStatus,
) autoClaimRequestResponse {
	t.Helper()
	latest, err := waitForAutoClaimStatusResult(ctx, key, expected)
	require.NoError(t, err, "wait for Auto Claim request %s status %s", key, expected)
	return latest
}

func waitForAutoClaimStatusResult(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	expected autoclaimtypes.RequestStatus,
) (autoClaimRequestResponse, error) {
	var latest autoClaimRequestResponse
	pollCtx, cancel := context.WithTimeout(ctx, autoClaimRequestWait)
	defer cancel()
	err := pollWithBackoff(
		pollCtx,
		autoClaimRequestWait,
		backoffInitial,
		backoffMax,
		fmt.Sprintf("autoclaim-%s-%s", key, expected),
		func() (bool, error) {
			request, found, err := getAutoClaimRequest(pollCtx, key)
			if err != nil {
				return false, err
			}
			if !found {
				return false, nil
			}
			latest = request
			switch request.Status {
			case expected.String():
				return true, nil
			case autoclaimtypes.RequestStatusFailed.String(), autoclaimtypes.RequestStatusPolicyRejected.String():
				return false, fmt.Errorf(
					"autoclaim request %s reached terminal status %s: last_error=%q claim_tx_hash=%v",
					key,
					request.Status,
					request.LastError,
					request.ClaimTxHash,
				)
			default:
				log.Infof(
					"waiting for Auto Claim request %s status %s, current=%s, last_error=%q, claim_tx_hash=%v",
					key,
					expected,
					request.Status,
					request.LastError,
					request.ClaimTxHash,
				)
				return false, nil
			}
		},
	)
	if err != nil {
		return latest, fmt.Errorf("wait for Auto Claim request %s status %s: %w", key, expected, err)
	}
	return latest, nil
}

func getAutoClaimRequest(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
) (autoClaimRequestResponse, bool, error) {
	// Public request inspection is served by the bridge service, not the admin Auto Claim API.
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/autoclaim/v1/bridges/%s", bridgeServiceBaseURL, key),
		nil,
	)
	if err != nil {
		return autoClaimRequestResponse{}, false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return autoClaimRequestResponse{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return autoClaimRequestResponse{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return autoClaimRequestResponse{}, false, fmt.Errorf(
			"GET Auto Claim request %s returned %s: %s",
			key,
			resp.Status,
			strings.TrimSpace(string(body)),
		)
	}

	var request autoClaimRequestResponse
	if err := json.NewDecoder(resp.Body).Decode(&request); err != nil {
		return autoClaimRequestResponse{}, false, err
	}
	return request, true, nil
}

func approveAutoClaimRequest(ctx context.Context, t *testing.T, key autoclaimtypes.RequestKey) {
	t.Helper()
	body := bytes.NewBufferString(`{"reason":"e2e approved","decider":"e2e","decider_id":"autoclaim-test"}`)
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/autoclaim/v1/bridges/%s/approve", autoClaimAPIBaseURL, key),
		body,
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(
		t,
		http.StatusOK,
		resp.StatusCode,
		"approve Auto Claim request %s response body: %s",
		key,
		strings.TrimSpace(string(respBody)),
	)
}

func patchAutoClaimConfig(baseConfig, autoClaimSection string) string {
	trimmed := strings.TrimRight(baseConfig, "\n")
	if idx := strings.Index(trimmed, "\n[AutoClaim]"); idx >= 0 {
		trimmed = strings.TrimRight(trimmed[:idx], "\n")
	}
	if strings.HasPrefix(trimmed, "[AutoClaim]") {
		trimmed = ""
	}
	return strings.TrimRight(trimmed, "\n") + "\n\n" + strings.TrimSpace(autoClaimSection) + "\n"
}

func autoClaimConfig(policyName string, networkID uint32) string {
	suffix := strings.ReplaceAll(policyName, "-", "_")
	return fmt.Sprintf(`
[AutoClaim]
StoragePath = "/tmp/autoclaim-e2e-%s.sqlite"

[AutoClaim.API]
Enabled = true

[AutoClaim.L1ToL2BridgeDetector]
Enabled = true
PollInterval = "2s"
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
EtrogL1UpgradeBlock = 0

[AutoClaim.L2ToLxBridgeDetector]
Enabled = false

[[AutoClaim.Claimers]]
Enabled = true
ID = "l2-autoclaim-e2e"
NetworkType = "EVM"
NetworkID = %d
URLRPC = %q
BridgeAddr = %q
PolicyName = %q
GasOffset = 100000
WaitPeriod = "1s"
RetryAfter = "1s"
MaxRetries = 180

[AutoClaim.Claimers.Policy]
AllowMessageClaims = false
AllowedOrigins = [0]
AllowedTokens = []
ManualFallback = false
MaxGas = 500000

[AutoClaim.Claimers.EthTxManager]
FrequencyToMonitorTxs = "1s"
WaitTxToBeMined = "2s"
WaitReceiptMaxTime = "250ms"
WaitReceiptCheckInterval = "1s"
PrivateKeys = [
	{Method = "local", Path = "/etc/aggkit/aggoracle.keystore", Password = %q},
]
ForcedGas = 0
GasPriceMarginFactor = 1
MaxGasPriceLimit = 0
StoragePath = "/tmp/ethtxmanager-autoclaim-e2e-%s.sqlite"
ReadPendingL1Txs = false
SafeStatusL1NumberOfBlocks = 0
FinalizedStatusL1NumberOfBlocks = 0
EstimateGasMaxRetries = 1

[AutoClaim.Claimers.EthTxManager.Etherman]
URL = %q
MultiGasProvider = false
L1ChainID = %d
HTTPHeaders = {}
`, suffix, networkID, autoClaimL2RPC, autoClaimBridgeAddr, policyName, autoClaimKeystorePass, suffix,
		autoClaimL2RPC, autoClaimL2ChainID)
}
