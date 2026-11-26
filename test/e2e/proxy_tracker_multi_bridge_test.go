package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/test/contracts/multibridgerouter"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// multiBridgeTracking is the part of api.TrackingData TestBridgeTrackerMultiBridgeTx reads:
// trackerTrackingData plus the log_index the tracker echoes for a request selecting one.
type multiBridgeTracking struct {
	trackerTrackingData
	LogIndex *uint32 `json:"log_index"`
}

// multiBridgeActivity is the part of api.ActivityResponse (with includeTracking=true) that
// TestBridgeTrackerMultiBridgeTx reads.
type multiBridgeActivity struct {
	Bridges []struct {
		Bridge *struct {
			TxHash       string `json:"tx_hash"`
			DepositCount uint32 `json:"deposit_count"`
			BlockPos     uint64 `json:"block_pos"`
		} `json:"bridge"`
		BridgeNetworkID uint32               `json:"bridge_network_id"`
		Tracking        *multiBridgeTracking `json:"tracking"`
	} `json:"bridges"`
}

// receiptBridgeEvent is one canonical BridgeEvent of a receipt.
type receiptBridgeEvent struct {
	logIndex           uint32
	depositCount       uint32
	destinationNetwork uint32
	amount             *big.Int
}

// getJSON GETs url from the proxy and decodes its 200 OK JSON body into out.
func getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

// fetchMultiBridgeTracking is fetchTrackingData with an optional log_index selector (nil: a
// tx-only request).
func fetchMultiBridgeTracking(
	ctx context.Context, networkID uint32, txHash common.Hash, logIndex *uint32,
) (*multiBridgeTracking, error) {
	url := fmt.Sprintf("%s/tracker/v1/network/%d/tx/%s", proxyTrackerBaseURL, networkID, txHash.Hex())
	if logIndex != nil {
		url += fmt.Sprintf("?log_index=%d", *logIndex)
	}
	var tracking multiBridgeTracking
	if err := getJSON(ctx, url, &tracking); err != nil {
		return nil, err
	}
	return &tracking, nil
}

// TestBridgeTrackerMultiBridgeTx is per-event tracking on a real chain: one L1 tx, through a
// router contract (test/contracts/multibridgerouter), bridges twice through the canonical bridge
// — to L2 and to L2B — so its receipt carries two canonical BridgeEvents with consecutive deposit
// counts. The proxy's tracker must:
//   - track each bridge by its log_index, each with its own facts (deposit count, destination,
//     amount), never the other one's;
//   - fail a tx-only request on that tx permanently, as ambiguous, listing both log indexes,
//     instead of reporting the first bridge for both;
//   - give each bridge of the tx its own tracking in the router's activity (includeTracking).
//
// Needs the multi-chain anvil env (anvil-2chains): L2B must be non-nil.
func TestBridgeTrackerMultiBridgeTx(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")
	if testEnv.L2B == nil {
		t.Skip("bridge tracker test requires a multi-chain env (L2B must be non-nil)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	require.NoError(t, waitForTrackerReady(ctx), "tracker health check never succeeded")

	l1Opts := *testEnv.L1.Transactor
	routerAddr, deployTx, router, err := multibridgerouter.DeployMultibridgerouter(&l1Opts, testEnv.Clients.L1)
	require.NoError(t, err, "deploy MultiBridgeRouter failed")
	_, err = bind.WaitMined(ctx, testEnv.Clients.L1, deployTx)
	require.NoError(t, err, "wait for MultiBridgeRouter deployment failed")
	t.Logf("router contract: %s", routerAddr.Hex())

	amountA, amountB := big.NewInt(1_000_000_000_000_000), big.NewInt(2_000_000_000_000_000)
	l1Opts.Value = new(big.Int).Add(amountA, amountB)
	bridgeTx, err := router.BridgeTwice(&l1Opts, testEnv.L1.Contracts.BridgeAddress,
		testEnv.L2.NetworkID, testEnv.L2.Transactor.From, amountA,
		testEnv.L2B.NetworkID, testEnv.L2B.Transactor.From, amountB)
	l1Opts.Value = nil
	require.NoError(t, err, "router bridgeTwice failed")
	receipt, err := bind.WaitMined(ctx, testEnv.Clients.L1, bridgeTx)
	require.NoError(t, err, "wait for bridgeTwice tx failed")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status)
	txHash := bridgeTx.Hash()
	t.Logf("multi-bridge tx: %s (block %d)", txHash.Hex(), receipt.BlockNumber.Uint64())

	// the receipt really carries two canonical BridgeEvents, with consecutive deposit counts
	var events []receiptBridgeEvent
	for _, l := range receipt.Logs {
		if l.Address != testEnv.L1.Contracts.BridgeAddress {
			continue
		}
		ev, perr := testEnv.L1.Contracts.Bridge.ParseBridgeEvent(*l)
		if perr != nil {
			continue // another event of the bridge contract
		}
		events = append(events, receiptBridgeEvent{
			logIndex: uint32(l.Index), depositCount: ev.DepositCount,
			destinationNetwork: ev.DestinationNetwork, amount: ev.Amount,
		})
	}
	require.Len(t, events, 2, "the router's tx must emit two canonical BridgeEvents")
	require.Equal(t, events[0].depositCount+1, events[1].depositCount)
	require.Equal(t, testEnv.L2.NetworkID, events[0].destinationNetwork)
	require.Equal(t, testEnv.L2B.NetworkID, events[1].destinationNetwork)
	logIndexes := []uint32{events[0].logIndex, events[1].logIndex}
	slices.Sort(logIndexes)
	t.Logf("BridgeEvents: %+v", events)

	const l1NetworkID = 0
	for _, ev := range events {
		t.Run(fmt.Sprintf("log_index=%d", ev.logIndex), func(t *testing.T) {
			var tracking *multiBridgeTracking
			err := pollWithBackoff(ctx, 3*time.Minute, backoffInitial, backoffMax,
				fmt.Sprintf("tracker resolves log %d", ev.logIndex), func() (bool, error) {
					data, ferr := fetchMultiBridgeTracking(ctx, l1NetworkID, txHash, &ev.logIndex)
					if ferr != nil {
						return false, nil //nolint:nilerr // registration/resolution still in progress
					}
					tracking = data
					return tracking.BridgeStatus != nil || tracking.TrackingStatus == "error", nil
				})
			require.NoError(t, err, "tracker never resolved the bridge of log %d", ev.logIndex)
			require.NotNil(t, tracking.BridgeStatus, "log %d failed: %+v", ev.logIndex, tracking.Error)
			require.Equal(t, ev.depositCount, tracking.BridgeStatus.Event.DepositCount,
				"log %d reports another bridge of the tx", ev.logIndex)
			require.Equal(t, ev.destinationNetwork, tracking.BridgeStatus.Event.DestinationNetwork)
			require.Equal(t, ev.amount.String(), tracking.BridgeStatus.Event.Amount)
			require.Equal(t, ev.logIndex, tracking.BridgeStatus.LogIndex)
			require.NotNil(t, tracking.LogIndex)
			require.Equal(t, ev.logIndex, *tracking.LogIndex)
		})
	}

	t.Run("tx-only is ambiguous", func(t *testing.T) {
		var tracking *multiBridgeTracking
		err := pollWithBackoff(ctx, 3*time.Minute, backoffInitial, backoffMax, "tracker settles the tx-only request",
			func() (bool, error) {
				data, ferr := fetchMultiBridgeTracking(ctx, l1NetworkID, txHash, nil)
				if ferr != nil {
					return false, nil //nolint:nilerr // registration/resolution still in progress
				}
				tracking = data
				return tracking.BridgeStatus != nil || tracking.TrackingStatus == "error", nil
			})
		require.NoError(t, err, "tracker never settled the tx-only request")
		require.Nil(t, tracking.BridgeStatus, "a tx-only request on a multi-bridge tx must not resolve to one bridge")
		require.Equal(t, "error", tracking.TrackingStatus)
		require.Equal(t, "error", tracking.ClaimStatus)
		require.Nil(t, tracking.LogIndex)
		require.NotNil(t, tracking.Error)
		require.Equal(t, "permanent", tracking.Error.ErrorTypeString)
		description := strings.Join(tracking.Error.Description, " ")
		require.Contains(t, description, "select one by log_index")
		require.Contains(t, description, fmt.Sprintf("log_index %v", logIndexes))
	})

	// the bridges' from address is the router (the caller of bridgeAsset), not the tx sender
	t.Run("activity tracks each bridge on its own", func(t *testing.T) {
		url := fmt.Sprintf("%s/tracker/v1/activity/from/%s?includeTracking=true", proxyTrackerBaseURL, routerAddr.Hex())
		byDeposit := map[uint32]*multiBridgeTracking{}
		err := pollWithBackoff(ctx, 3*time.Minute, backoffInitial, backoffMax, "router activity with tracking",
			func() (bool, error) {
				var activity multiBridgeActivity
				if ferr := getJSON(ctx, url, &activity); ferr != nil {
					return false, nil //nolint:nilerr // 503 while the address is registered, keep polling
				}
				clear(byDeposit)
				for _, item := range activity.Bridges {
					if item.Bridge == nil || item.BridgeNetworkID != l1NetworkID ||
						!strings.EqualFold(item.Bridge.TxHash, txHash.Hex()) ||
						item.Tracking == nil || item.Tracking.BridgeStatus == nil {
						continue
					}
					require.Equal(t, item.Bridge.DepositCount, item.Tracking.BridgeStatus.Event.DepositCount,
						"activity item of deposit %d carries another bridge's tracking", item.Bridge.DepositCount)
					require.NotNil(t, item.Tracking.LogIndex)
					require.Equal(t, uint64(*item.Tracking.LogIndex), item.Bridge.BlockPos)
					byDeposit[item.Bridge.DepositCount] = item.Tracking
				}
				return len(byDeposit) == len(events), nil
			})
		require.NoError(t, err, "the router's activity never listed both bridges with their tracking")
		for _, ev := range events {
			require.Contains(t, byDeposit, ev.depositCount)
			require.Equal(t, ev.logIndex, byDeposit[ev.depositCount].BridgeStatus.LogIndex)
		}
	})
}
