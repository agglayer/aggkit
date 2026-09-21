package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	"github.com/agglayer/aggkit/test/contracts/aggchainrollupmock"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/require"
)

const (
	// proxyAutoregProxy002BaseURL is the external (host) URL of aggkit-proxy-002's REST API. That
	// instance runs with [BridgeServiceFinder] AutoRegisterNewNetworks = false (see
	// config/aggkit-proxy/aggkit-proxy-noautoreg.toml) and exists solely for this test.
	proxyAutoregProxy002BaseURL = "http://127.0.0.1:15602"

	// proxyAutoregProxy002Service is the docker-compose service name of aggkit-proxy-002.
	proxyAutoregProxy002Service = "aggkit-proxy-002"

	// proxyAutoregAdminAddr holds DEFAULT_ADMIN_ROLE on the rollup manager in this env (proven in
	// the plan's "S3 findings" (b)). Its private key is unavailable, so every on-chain write below
	// is sent as an unsigned eth_sendTransaction from an anvil-impersonated account, built from the
	// generated ABI, rather than through bind.TransactOpts (which needs a signer). Reads use the
	// generated *Caller bindings instead, which need no signer.
	proxyAutoregAdminAddr = "0xE34aaF64b29273B7D567FCFc40544c014EEe9970"

	// proxyAutoregRollupManagerAddr is the L1 rollup manager address; it must match
	// [BridgeServiceFinder] RollupManagerAddr in both aggkit-proxy TOML configs in this env.
	proxyAutoregRollupManagerAddr = "0x6c6c009cC348976dB4A908c92B24433d4F6edA43"

	// proxyAutoregAdminFundingWei is 100 ETH in wei, hex-encoded: enough for the impersonated admin
	// to pay gas for the writes below.
	proxyAutoregAdminFundingWei = "0x56bc75e2d63100000"

	// proxyAutoregBridgeServiceURL is the bridge-service URL the deployed mock rollup advertises,
	// pointed at network 1's real, already-healthy bridge REST rather than a synthetic/unreachable
	// one: a genuinely reachable backend is required so aggkit-proxy-001 answers 200 (not 502) once
	// it discovers and starts serving network 3 -- otherwise the positive assertion below would only
	// prove "forwarded", not "served".
	proxyAutoregBridgeServiceURL = "http://aggkit-001:5577"

	// proxyAutoregNewChainID is the chain id used for the newly attached rollup; 20203 is the next
	// unused id after this env's existing L2 chain ids (20201, 20202).
	proxyAutoregNewChainID = 20203

	// proxyAutoregNewNetworkID is the network (== rollup) id the attach below produces: rollupID
	// follows rollupCount, i.e. the existing 2 rollups become 3.
	proxyAutoregNewNetworkID = 3

	// proxyAutoregRollupVerifierType matches rollupVerifierType on the two pessimistic rollups
	// already attached in this env (rollupIDToRollupData(1)/(2), per the plan's "S3 findings").
	proxyAutoregRollupVerifierType = 2

	// proxyAutoregAttachBudget bounds the whole attach sequence (impersonate, fund, deploy the mock,
	// configure it, self-grant the role, addExistingRollup), each step awaited until mined.
	proxyAutoregAttachBudget = 2 * time.Minute

	// proxyAutoregPollInterval mirrors [BridgeServiceFinder] PollInterval in both aggkit-proxy
	// configs in this env.
	proxyAutoregPollInterval = 10 * time.Second

	// proxyAutoregDiscoveryBudget bounds how long this test waits for aggkit-proxy-001 to discover
	// and start serving network 3 -- 3x proxyAutoregPollInterval with margin.
	proxyAutoregDiscoveryBudget = 60 * time.Second
	// proxyAutoregDiscoveryPoll is the fixed interval this test polls aggkit-proxy-001 at while
	// waiting for that discovery.
	proxyAutoregDiscoveryPoll = 2 * time.Second

	// proxyAutoregRestartReadyBudget bounds how long this test waits for aggkit-proxy-002's tracker
	// health to come back after being restarted to pick up network 3.
	proxyAutoregRestartReadyBudget = 90 * time.Second

	// proxyAutoregInitialReadyBudget bounds how long this test waits for either proxy's tracker
	// health to answer before the test body starts, mirroring waitForTrackerReady's own budget.
	proxyAutoregInitialReadyBudget = 2 * time.Minute
)

// trackerPendingNetwork mirrors types.PendingNetwork (bridgetracker/types/health.go); only the
// fields this test asserts on are declared.
type trackerPendingNetwork struct {
	NetworkID     uint32 `json:"network_id"`
	RollupAddress string `json:"rollup_address"`
	BlockNumber   uint64 `json:"block_number"`
	Reason        string `json:"reason"`
}

// trackerHealthResponse mirrors types.HealthResponse (bridgetracker/types/health.go); only the
// pending_networks field this test asserts on is declared.
type trackerHealthResponse struct {
	PendingNetworks []trackerPendingNetwork `json:"pending_networks"`
}

// waitForTrackerReadyAt polls baseURL's /tracker/v1/health until it answers 200 OK, generalizing
// waitForTrackerReady (proxy_tracker_test.go, hardcoded to aggkit-proxy-001's URL) to any
// aggkit-proxy instance -- needed here for aggkit-proxy-002, added solely for this test.
func waitForTrackerReadyAt(ctx context.Context, baseURL string, timeout time.Duration) error {
	return pollWithBackoff(ctx, timeout, backoffInitial, backoffMax, "tracker health ("+baseURL+")",
		func() (bool, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/tracker/v1/health", nil)
			if err != nil {
				return false, err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return false, nil //nolint:nilerr // proxy not reachable yet, keep polling
			}
			resp.Body.Close()
			return resp.StatusCode == http.StatusOK, nil
		})
}

// fetchBridgeStatusCode issues GET baseURL/bridge/v1/sync-status?network_id=networkID and returns
// only the HTTP status code: this test only needs to distinguish "not yet known" (404) from
// "proxied" (200), never the response body.
func fetchBridgeStatusCode(ctx context.Context, baseURL string, networkID uint32) (int, error) {
	url := fmt.Sprintf("%s/bridge/v1/sync-status?network_id=%d", baseURL, networkID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// fetchTrackerHealthRawBody calls GET baseURL/tracker/v1/health and returns the raw response body
// alongside its status code, so callers can also check for an absent "pending_networks" key --
// something a typed struct decode cannot distinguish from an empty one, since the field is
// omitempty on both sides.
func fetchTrackerHealthRawBody(ctx context.Context, baseURL string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/tracker/v1/health", nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", resp.StatusCode, err
	}
	return string(body), resp.StatusCode, nil
}

// fetchTrackerHealth calls GET baseURL/tracker/v1/health and decodes its body.
func fetchTrackerHealth(ctx context.Context, baseURL string) (*trackerHealthResponse, int, error) {
	body, code, err := fetchTrackerHealthRawBody(ctx, baseURL)
	if err != nil {
		return nil, code, err
	}
	var data trackerHealthResponse
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return nil, code, err
	}
	return &data, code, nil
}

// sendUnsignedTx sends an unsigned eth_sendTransaction from an anvil-impersonated account (which
// has no private key available, so bind.TransactOpts cannot sign for it) and returns the
// transaction hash. to is nil for a contract-creation transaction.
func sendUnsignedTx(ctx context.Context, rpcClient *rpc.Client, from common.Address, to *common.Address, data []byte) (common.Hash, error) {
	params := map[string]any{
		"from": from,
		"data": hexutil.Encode(data),
	}
	if to != nil {
		params["to"] = *to
	}
	var txHash common.Hash
	if err := rpcClient.CallContext(ctx, &txHash, "eth_sendTransaction", params); err != nil {
		return common.Hash{}, err
	}
	return txHash, nil
}

// sendAndWaitMined sends an unsigned transaction via sendUnsignedTx and waits for it to be mined,
// via bind.WaitMinedHash (the hash-based variant of bind.WaitMined -- needed because these
// transactions are never locally signed into a *types.Transaction, only submitted by hash).
func sendAndWaitMined(
	ctx context.Context, l1Client *ethclient.Client, from common.Address, to *common.Address, data []byte,
) (*ethtypes.Receipt, error) {
	txHash, err := sendUnsignedTx(ctx, l1Client.Client(), from, to, data)
	if err != nil {
		return nil, fmt.Errorf("eth_sendTransaction: %w", err)
	}
	receipt, err := bind.WaitMinedHash(ctx, l1Client, txHash)
	if err != nil {
		return nil, fmt.Errorf("wait mined %s: %w", txHash.Hex(), err)
	}
	return receipt, nil
}

// TestProxyAutoRegisterNewNetworks exercises #1855: a network discovered after aggkit-proxy starts
// is only auto-activated when [BridgeServiceFinder] AutoRegisterNewNetworks is true. It attaches a
// third rollup (network 3) to the L1 rollup manager using the recipe proven in the plan's "S3
// findings" (b) -- an anvil-impersonated admin (its private key is unavailable, so every on-chain
// write is an unsigned eth_sendTransaction built from the generated ABI, never bind.TransactOpts;
// reads use the generated *Caller bindings instead, which need no signer) -- and asserts that
// aggkit-proxy-001 (AutoRegisterNewNetworks = true, the default) starts proxying network 3 within
// 3x PollInterval, while aggkit-proxy-002 (AutoRegisterNewNetworks = false, this test's own config)
// keeps 404ing it and instead reports it under GET /tracker/v1/health's pending_networks, and that
// restarting aggkit-proxy-002 -- the only activation path while the flag is off -- makes it serve
// network 3 too, with pending_networks then gone.
func TestProxyAutoRegisterNewNetworks(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")
	if testEnv.L2B == nil {
		t.Skip("proxy autoregister test requires a multi-chain env (L2B must be non-nil)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	require.NoError(t, waitForTrackerReady(ctx), "aggkit-proxy-001 tracker health check never succeeded before test start")
	require.NoError(t, waitForTrackerReadyAt(ctx, proxyAutoregProxy002BaseURL, proxyAutoregInitialReadyBudget),
		"aggkit-proxy-002 tracker health check never succeeded before test start")

	// Sanity: neither proxy knows about network 3 yet.
	status, err := fetchBridgeStatusCode(ctx, proxyTrackerBaseURL, proxyAutoregNewNetworkID)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, status, "aggkit-proxy-001 already serves network 3 before attach")
	status, err = fetchBridgeStatusCode(ctx, proxyAutoregProxy002BaseURL, proxyAutoregNewNetworkID)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, status, "aggkit-proxy-002 already serves network 3 before attach")

	// Attach rollup 3, per the plan's "S3 findings" (b): impersonate+fund the admin, deploy the
	// mock rollup contract, point it at a real, healthy bridge service, self-grant
	// _ADD_EXISTING_ROLLUP_ROLE, then addExistingRollup.
	attachCtx, cancelAttach := context.WithTimeout(ctx, proxyAutoregAttachBudget)
	defer cancelAttach()

	adminAddr := common.HexToAddress(proxyAutoregAdminAddr)
	rollupManagerAddr := common.HexToAddress(proxyAutoregRollupManagerAddr)
	l1Client := testEnv.Clients.L1
	rpcClient := l1Client.Client()

	require.NoError(t, rpcClient.CallContext(attachCtx, nil, "anvil_impersonateAccount", adminAddr),
		"anvil_impersonateAccount")
	require.NoError(t, rpcClient.CallContext(attachCtx, nil, "anvil_setBalance", adminAddr, proxyAutoregAdminFundingWei),
		"anvil_setBalance")

	callOpts := &bind.CallOpts{Context: attachCtx}
	rollupCountBefore, err := testEnv.L1.Contracts.RollupManager.RollupCount(callOpts)
	require.NoError(t, err, "rollupCount before attach")

	// Deploy the mock rollup contract (no constructor args).
	deployData := common.FromHex(aggchainrollupmock.AggchainrollupmockMetaData.Bin)
	deployReceipt, err := sendAndWaitMined(attachCtx, l1Client, adminAddr, nil, deployData)
	require.NoError(t, err, "deploy aggchainrollupmock")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, deployReceipt.Status, "deploy aggchainrollupmock tx failed")
	mockAddr := deployReceipt.ContractAddress
	require.NotEqual(t, common.Address{}, mockAddr, "deployed mock contract address is zero")

	mockABI, err := aggchainrollupmock.AggchainrollupmockMetaData.GetAbi()
	require.NoError(t, err, "parse aggchainrollupmock ABI")

	setURLData, err := mockABI.Pack("setTrustedSequencerURL", proxyAutoregBridgeServiceURL)
	require.NoError(t, err, "pack setTrustedSequencerURL")
	setURLReceipt, err := sendAndWaitMined(attachCtx, l1Client, adminAddr, &mockAddr, setURLData)
	require.NoError(t, err, "setTrustedSequencerURL")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, setURLReceipt.Status, "setTrustedSequencerURL tx failed")

	setMetadataData, err := mockABI.Pack("setAggchainMetadata", "BRIDGE_SERVICE_URL", proxyAutoregBridgeServiceURL)
	require.NoError(t, err, "pack setAggchainMetadata")
	setMetadataReceipt, err := sendAndWaitMined(attachCtx, l1Client, adminAddr, &mockAddr, setMetadataData)
	require.NoError(t, err, "setAggchainMetadata")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, setMetadataReceipt.Status, "setAggchainMetadata tx failed")

	mockCaller, err := aggchainrollupmock.NewAggchainrollupmockCaller(mockAddr, l1Client)
	require.NoError(t, err, "build aggchainrollupmock caller")
	gotURL, err := mockCaller.TrustedSequencerURL(callOpts)
	require.NoError(t, err, "read back trustedSequencerURL")
	require.Equal(t, proxyAutoregBridgeServiceURL, gotURL, "trustedSequencerURL mismatch after set")

	managerABI, err := agglayermanager.AgglayermanagerMetaData.GetAbi()
	require.NoError(t, err, "parse agglayermanager ABI")

	addExistingRollupRole := crypto.Keccak256Hash([]byte("_ADD_EXISTING_ROLLUP_ROLE"))
	grantRoleData, err := managerABI.Pack("grantRole", [32]byte(addExistingRollupRole), adminAddr)
	require.NoError(t, err, "pack grantRole")
	grantRoleReceipt, err := sendAndWaitMined(attachCtx, l1Client, adminAddr, &rollupManagerAddr, grantRoleData)
	require.NoError(t, err, "grantRole(_ADD_EXISTING_ROLLUP_ROLE)")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, grantRoleReceipt.Status, "grantRole tx failed")

	addExistingRollupData, err := managerABI.Pack("addExistingRollup",
		mockAddr, common.Address{}, uint64(0), uint64(proxyAutoregNewChainID),
		[32]byte{}, uint8(proxyAutoregRollupVerifierType), [32]byte{}, [32]byte{})
	require.NoError(t, err, "pack addExistingRollup")
	addExistingRollupReceipt, err := sendAndWaitMined(attachCtx, l1Client, adminAddr, &rollupManagerAddr, addExistingRollupData)
	require.NoError(t, err, "addExistingRollup")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, addExistingRollupReceipt.Status, "addExistingRollup tx failed")

	rollupCountAfter, err := testEnv.L1.Contracts.RollupManager.RollupCount(callOpts)
	require.NoError(t, err, "rollupCount after attach")
	require.Equal(t, rollupCountBefore+1, rollupCountAfter, "rollupCount did not increase by exactly one")

	t.Logf("attached rollup %d (mock=%s, chainID=%d) to the rollup manager",
		rollupCountAfter, mockAddr.Hex(), proxyAutoregNewChainID)

	// aggkit-proxy-001 (AutoRegisterNewNetworks = true) must discover and start serving network 3
	// within 3x PollInterval.
	err = pollWithBackoff(ctx, proxyAutoregDiscoveryBudget, proxyAutoregDiscoveryPoll, proxyAutoregDiscoveryPoll,
		"aggkit-proxy-001 discovers network 3", func() (bool, error) {
			code, ferr := fetchBridgeStatusCode(ctx, proxyTrackerBaseURL, proxyAutoregNewNetworkID)
			if ferr != nil {
				return false, nil //nolint:nilerr // proxy transiently unreachable, keep polling
			}
			return code == http.StatusOK, nil
		})
	require.NoError(t, err, "aggkit-proxy-001 never started serving network 3")

	// aggkit-proxy-002 (AutoRegisterNewNetworks = false) must keep 404ing it -- re-checked after a
	// further PollInterval to prove it is not merely late.
	status, err = fetchBridgeStatusCode(ctx, proxyAutoregProxy002BaseURL, proxyAutoregNewNetworkID)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, status, "aggkit-proxy-002 unexpectedly serves network 3 before restart")

	time.Sleep(proxyAutoregPollInterval)
	status, err = fetchBridgeStatusCode(ctx, proxyAutoregProxy002BaseURL, proxyAutoregNewNetworkID)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, status,
		"aggkit-proxy-002 unexpectedly serves network 3 after a further PollInterval (still before restart)")

	// aggkit-proxy-002's health must report network 3 as pending; aggkit-proxy-001's health must
	// carry no pending_networks key at all.
	proxy001Body, code, err := fetchTrackerHealthRawBody(ctx, proxyTrackerBaseURL)
	require.NoError(t, err, "fetch aggkit-proxy-001 health")
	require.Equal(t, http.StatusOK, code)
	require.NotContains(t, proxy001Body, "pending_networks",
		"aggkit-proxy-001 health unexpectedly carries a pending_networks key")

	proxy002Health, code, err := fetchTrackerHealth(ctx, proxyAutoregProxy002BaseURL)
	require.NoError(t, err, "fetch aggkit-proxy-002 health")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, proxy002Health.PendingNetworks, 1, "aggkit-proxy-002 pending_networks: %+v", proxy002Health.PendingNetworks)
	pending := proxy002Health.PendingNetworks[0]
	require.EqualValues(t, proxyAutoregNewNetworkID, pending.NetworkID)
	require.Equal(t, mockAddr, common.HexToAddress(pending.RollupAddress),
		"pending network's rollup_address does not match the deployed mock")
	require.NotEmpty(t, pending.Reason, "pending network's reason is empty")
	require.Positive(t, pending.BlockNumber, "pending network's block_number is not > 0")

	// Restart aggkit-proxy-002 -- the only activation path while AutoRegisterNewNetworks is false --
	// and confirm it now serves network 3, with pending_networks gone.
	require.NoError(t, testEnv.RestartService(ctx, proxyAutoregProxy002Service), "restart aggkit-proxy-002")
	require.NoError(t, waitForTrackerReadyAt(ctx, proxyAutoregProxy002BaseURL, proxyAutoregRestartReadyBudget),
		"aggkit-proxy-002 tracker health check never recovered after restart")

	status, err = fetchBridgeStatusCode(ctx, proxyAutoregProxy002BaseURL, proxyAutoregNewNetworkID)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, "aggkit-proxy-002 does not serve network 3 after restart")

	proxy002BodyAfter, code, err := fetchTrackerHealthRawBody(ctx, proxyAutoregProxy002BaseURL)
	require.NoError(t, err, "fetch aggkit-proxy-002 health after restart")
	require.Equal(t, http.StatusOK, code)
	require.NotContains(t, proxy002BodyAfter, "pending_networks",
		"aggkit-proxy-002 health still carries a pending_networks key after restart")
}
