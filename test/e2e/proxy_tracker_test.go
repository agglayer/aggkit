package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// proxyTrackerBaseURL is the external (host) URL of the aggkit-proxy REST API, which serves the
// bridge tracker. It only exists in the 2-chain env (test/e2e/envs/op-pp-2chains/docker-compose.yml,
// service aggkit-proxy-001, port 12601 -> 8080); the single-chain env (op-pp) has no proxy at all.
const proxyTrackerBaseURL = "http://127.0.0.1:12601"

// trackerBridgeEventData mirrors api.BridgeEventData (bridgetracker/api/bridge_status.go): the
// facts taken directly from the on-chain BridgeEvent log.
type trackerBridgeEventData struct {
	LeafType           string         `json:"leaf_type"`
	OriginNetwork      uint32         `json:"origin_network"`
	OriginAddress      common.Address `json:"origin_address"`
	DestinationNetwork uint32         `json:"destination_network"`
	DestinationAddress common.Address `json:"destination_address"`
	Amount             string         `json:"amount"`
	DepositCount       uint32         `json:"deposit_count"`
}

// trackerBridgeStatus mirrors api.BridgeStatus.
type trackerBridgeStatus struct {
	BridgeType     string                 `json:"bridge_type"`
	BlockNumber    uint64                 `json:"block_number"`
	LogIndex       uint32                 `json:"log_index"`
	BlockTimestamp uint64                 `json:"block_timestamp"`
	Event          trackerBridgeEventData `json:"event"`
}

// trackerBridgeStepPath mirrors api.BridgeStepPath (bridgetracker/api/bridge_step_path.go); only
// the fields this test asserts on are declared, the rest (start_date/end_date/expected_duration/
// result/error) are simply ignored by json.Unmarshal.
type trackerBridgeStepPath struct {
	StepIndex int    `json:"step_index"`
	StepName  string `json:"step_name"`
	Status    string `json:"status"`
}

// trackerTrackingData mirrors api.TrackingData, the body of GET /tracker/v1/network/{id}/tx/{hash}.
type trackerTrackingData struct {
	TrackingStatus string                  `json:"tracking_status"`
	BridgeStatus   *trackerBridgeStatus    `json:"bridge_status"`
	StepIndex      *int                    `json:"step_index"`
	AllSteps       []trackerBridgeStepPath `json:"all_steps"`
	// Error is only checked for nil-ness, so an empty struct is enough to decode "not null".
	Error *struct{} `json:"error"`
}

// fetchTrackingData calls the tracker's GET /tracker/v1/network/{networkID}/tx/{txHash}, which
// registers the bridge in the supervised list on first call and reports its current TrackingData
// from then on.
func fetchTrackingData(ctx context.Context, networkID uint32, txHash common.Hash) (*trackerTrackingData, error) {
	url := fmt.Sprintf("%s/tracker/v1/network/%d/tx/%s", proxyTrackerBaseURL, networkID, txHash.Hex())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET tracker status returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var data trackerTrackingData
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	return &data, nil
}

// waitForTrackerReady polls the tracker's health endpoint until it answers 200 OK. Nothing in
// TestMain/envs.LoadEnv waits for aggkit-proxy-001's own readiness, unlike the other services.
func waitForTrackerReady(ctx context.Context) error {
	return pollWithBackoff(ctx, 2*time.Minute, backoffInitial, backoffMax, "tracker health", func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyTrackerBaseURL+"/tracker/v1/health", nil)
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

// stepNames extracts the StepName of each entry, in order, for asserting the expected-path shape.
func stepNames(steps []trackerBridgeStepPath) []string {
	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.StepName
	}
	return names
}

// claimL1ToL2 claims, on L2, an L1->L2 bridge previously sent (and left unclaimed) via
// BridgeL1NoClaim. Mirrors the claim tail of BridgeL1ToL2 (bridge_utils.go).
func claimL1ToL2(ctx context.Context, env *envs.Env, l2Opts *bind.TransactOpts, result *bridgeResult) (common.Hash, error) {
	callOpts := &bind.CallOpts{Context: ctx}

	// The aggoracle may have injected a later leaf than our deposit's own L1InfoTreeIndex (index
	// >= our own); the claim proof must be built against the leaf actually on L2, not the
	// deposit's index (see BridgeL1ToL2's identical comment in bridge_utils.go).
	injectedLeaf, err := env.Clients.BridgeService.GetInjectedL1InfoLeaf(
		ctx, int(result.Bridge.DestinationNetwork), int(result.L1InfoTreeIndex),
	)
	if err != nil || injectedLeaf == nil {
		return common.Hash{}, fmt.Errorf("failed to get injected L1 info leaf: %w", err)
	}
	claimL1InfoTreeIndex := injectedLeaf.L1InfoTreeIndex

	claimProof, err := env.Clients.BridgeService.GetClaimProof(ctx, 0, claimL1InfoTreeIndex, result.DepositCount)
	if err != nil || claimProof == nil {
		return common.Hash{}, fmt.Errorf("failed to get claim proof: %w", err)
	}

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
	originTokenAddress := common.HexToAddress(string(result.Bridge.OriginAddress))
	metadata := common.FromHex(result.Bridge.Metadata)

	// Verify the GER is confirmed on-chain before claiming (mirrors BridgeL1ToL2).
	gerHash := crypto.Keccak256Hash(mainnetExitRoot[:], rollupExitRoot[:])
	for i := 0; i < 30; i++ {
		ts, gerErr := env.L2.Contracts.GlobalExitRoot.GlobalExitRootMap(callOpts, gerHash)
		if gerErr == nil && ts.Sign() > 0 {
			break
		}
		time.Sleep(time.Second)
	}

	claimTx, err := env.L2.Contracts.L2Bridge.ClaimAsset(
		l2Opts, smtProofLocalExitRoot, smtProofRollupExitRoot,
		result.Bridge.GlobalIndex, mainnetExitRoot, rollupExitRoot,
		result.Bridge.OriginNetwork, originTokenAddress, result.Bridge.DestinationNetwork,
		result.DestinationAddr, result.BridgeAmount, metadata,
	)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to send claim transaction: %w", err)
	}
	claimReceipt, err := bind.WaitMined(ctx, env.Clients.L2, claimTx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to wait for claim tx: %w", err)
	}
	if claimReceipt.Status != ethtypes.ReceiptStatusSuccessful {
		return common.Hash{}, errors.New("claim transaction failed")
	}
	return claimTx.Hash(), nil
}

// TestBridgeTrackerL1ToL2 bridges an asset L1->L2 and follows it through the bridge tracker
// (aggkit-proxy's /tracker/v1 REST API) from resolution to WaitingClaim, then claims it manually
// (the tracker only reports status, it never builds/sends claim transactions itself) and asserts
// the tracker follows it through to its terminal Claimed/finished state.
//
// It requires the multi-chain env (EnvOpPP2Chains): aggkit-proxy is only wired there (see
// docker-compose.yml). When run against the single-chain env (testEnv.L2B == nil) it is skipped.
func TestBridgeTrackerL1ToL2(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")
	if testEnv.L2B == nil {
		t.Skip("bridge tracker test requires EnvOpPP2Chains (aggkit-proxy is only wired there)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	require.NoError(t, waitForTrackerReady(ctx), "tracker health check never succeeded")

	l1Opts := *testEnv.L1.Transactor
	l2Opts := *testEnv.L2.Transactor
	bridgeAmount := big.NewInt(1e14) // 0.0001 ETH

	result, err := BridgeL1NoClaim(ctx, testEnv, &l1Opts, &l2Opts, bridgeAmount, "tracker-l1-l2")
	require.NoError(t, err, "L1->L2 bridge (no claim) failed")

	const l1NetworkID = 0 // TrackingID.NetworkID is the network the creating tx was sent to
	txHash := common.HexToHash(string(result.Bridge.TxHash))
	t.Logf("bridge tx: %s deposit_count=%d", txHash.Hex(), result.DepositCount)

	// The tracker resolves the creating tx and walks WaitingGERUpdate -> WaitingGERInjection ->
	// WaitingClaim on its own, independently of the bridge-service polling BridgeL1NoClaim already
	// did above; since the bridge is already fully indexed/injected by now, the tracker should
	// reach WaitingClaim once it resolves rather than showing every intermediate step live.
	var tracking *trackerTrackingData
	err = pollWithBackoff(ctx, 3*time.Minute, backoffInitial, backoffMax, "tracker reaches WaitingClaim",
		func() (bool, error) {
			data, ferr := fetchTrackingData(ctx, l1NetworkID, txHash)
			if ferr != nil {
				return false, nil //nolint:nilerr // registration/resolution still in progress
			}
			tracking = data
			if tracking.StepIndex == nil || len(tracking.AllSteps) == 0 {
				return false, nil
			}
			return tracking.AllSteps[*tracking.StepIndex].StepName == "WaitingClaim", nil
		})
	require.NoError(t, err, "tracker never reached WaitingClaim")

	require.Equal(t, "running", tracking.TrackingStatus)
	require.NotNil(t, tracking.BridgeStatus, "bridge_status must be resolved by the time a step is reachable")
	require.Equal(t, "L1->L2", tracking.BridgeStatus.BridgeType)
	require.Equal(t, "Asset", tracking.BridgeStatus.Event.LeafType)
	require.EqualValues(t, l1NetworkID, tracking.BridgeStatus.Event.OriginNetwork)
	require.Equal(t, result.Bridge.DestinationNetwork, tracking.BridgeStatus.Event.DestinationNetwork)
	require.Equal(t, common.Address{}, tracking.BridgeStatus.Event.OriginAddress, "native ETH bridge has no origin token")
	require.Equal(t, result.DestinationAddr, tracking.BridgeStatus.Event.DestinationAddress)
	require.Equal(t, bridgeAmount.String(), tracking.BridgeStatus.Event.Amount)
	require.Equal(t,
		[]string{"WaitingGERUpdate", "WaitingGERInjection", "WaitingClaim", "Claimed"},
		stepNames(tracking.AllSteps),
		"L1->L2 path must match domain.ExpectedPath")

	claimTxHash, err := claimL1ToL2(ctx, testEnv, &l2Opts, result)
	require.NoError(t, err, "manual L1->L2 claim failed")
	t.Logf("claim tx: %s", claimTxHash.Hex())

	err = pollWithBackoff(ctx, 3*time.Minute, backoffInitial, backoffMax, "tracker reaches finished",
		func() (bool, error) {
			data, ferr := fetchTrackingData(ctx, l1NetworkID, txHash)
			if ferr != nil {
				return false, nil //nolint:nilerr // transient
			}
			tracking = data
			return tracking.TrackingStatus == "finished", nil
		})
	require.NoError(t, err, "tracker never reached finished")

	require.Nil(t, tracking.Error, "a successfully claimed bridge must carry no error")
	require.NotEmpty(t, tracking.AllSteps)
	last := tracking.AllSteps[len(tracking.AllSteps)-1]
	require.Equal(t, "Claimed", last.StepName)
	require.Equal(t, "done", last.Status)
}
