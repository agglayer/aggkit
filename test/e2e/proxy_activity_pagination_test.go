package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const (
	// activityPaginationBridges is how many bridges the pagination scenario sends: more than two
	// pages of activityPaginationPageSize, the last one short
	activityPaginationBridges = 12
	// activityPaginationPageSize is the page size the scenario pages with
	activityPaginationPageSize = 5
	// activityPaginationAttempts bounds how many times a traversal is restarted because the
	// revision changed while it paged (a bridge moving to readyToClaim when its GER is injected)
	activityPaginationAttempts = 5
)

// activityPaginationKey identifies an activity item: the network that emitted the bridge and its
// deposit count there
type activityPaginationKey struct {
	network, depositCount uint32
}

// getActivityQuery calls GET /tracker/v1/activity/from/{from}?query on the proxy at baseURL, with
// an If-None-Match header when ifNoneMatch is not empty. It returns the status, the body (200
// only) and the ETag
func getActivityQuery(
	ctx context.Context, baseURL string, from common.Address, query, ifNoneMatch string,
) (int, *proxyActivityResponse, string, error) {
	url := fmt.Sprintf("%s/tracker/v1/activity/from/%s?%s", baseURL, from.Hex(), query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, "", err
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil, resp.Header.Get("ETag"), nil
	}
	var activity proxyActivityResponse
	if err := json.Unmarshal(body, &activity); err != nil {
		return 0, nil, "", fmt.Errorf("decode activity response: %w", err)
	}
	return resp.StatusCode, &activity, resp.Header.Get("ETag"), nil
}

// activityTraversal is every page of one traversal, in order
type activityTraversal struct {
	pages []*proxyActivityResponse
}

// items is every item of the traversal, in the order served
func (tr *activityTraversal) items() []proxyActivityItem {
	var out []proxyActivityItem
	for _, page := range tr.pages {
		out = append(out, page.Bridges...)
	}
	return out
}

// traverseActivity pages through the activity of from (filter: a filterBridges value, or empty)
// with pageSize, up to the first empty page, the documented client flow: if the revision of the
// last page differs from the first one's, something changed while paging and the traversal starts
// again from page 1. It fails the test if no traversal sees a single revision
func traverseActivity(
	ctx context.Context, t *testing.T, baseURL string, from common.Address, filter string, pageSize int,
) *activityTraversal {
	t.Helper()
	query := ""
	if filter != "" {
		query = "filterBridges=" + filter + "&"
	}
	for range activityPaginationAttempts {
		tr := &activityTraversal{}
		for page := 1; ; page++ {
			code, body, _, err := getActivityQuery(ctx, baseURL, from,
				fmt.Sprintf("%spage_number=%d&page_size=%d", query, page, pageSize), "")
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, code)
			requireActivityWellFormed(t, body)
			tr.pages = append(tr.pages, body)
			if len(body.Bridges) == 0 {
				break
			}
			require.LessOrEqual(t, page, activityPaginationBridges+1, "the traversal never ends")
		}
		if tr.pages[0].Revision == tr.pages[len(tr.pages)-1].Revision {
			return tr
		}
		t.Logf("the revision changed during the traversal (filter %q, page size %d): starting again", filter, pageSize)
	}
	require.FailNow(t, "no traversal saw a single revision")
	return nil
}

// requireDocumentedOrder requires items in the documented order: creation_timestamp descending,
// then global index descending, compared as numbers
func requireDocumentedOrder(t *testing.T, items []proxyActivityItem) {
	t.Helper()
	require.True(t, slices.IsSortedFunc(items, func(a, b proxyActivityItem) int {
		if a.CreationTimestamp != b.CreationTimestamp {
			if a.CreationTimestamp > b.CreationTimestamp {
				return -1
			}
			return 1
		}
		return b.Bridge.GlobalIndex.ToBigInt().Cmp(a.Bridge.GlobalIndex.ToBigInt())
	}), "items out of the documented order")
}

func keysOfItems(items []proxyActivityItem) []activityPaginationKey {
	keys := make([]activityPaginationKey, 0, len(items))
	for _, item := range items {
		keys = append(keys, activityPaginationKey{network: item.BridgeNetworkID, depositCount: item.Bridge.DepositCount})
	}
	return keys
}

// TestProxyActivityPagination sends several L1 -> L2 bridges from a fresh sender and pages through
// its activity on the aggkit-proxy (the store applies page_number/page_size):
// every page reports the total, only the last page is short and the page after it is empty, the
// pages hold every bridge exactly once (no duplicate, no gap) in the documented order, whatever the
// page size, filterBridges combines with the pagination (a filtered traversal is exactly the
// matching items of the full one, in the same order, with the filtered total), and a client
// holding the current revision is answered 304
func TestProxyActivityPagination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")
	if testEnv.L2B == nil {
		t.Skip("the activity endpoint is served by aggkit-proxy, only present in the multi-chain envs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	require.NoError(t, waitForTrackerReady(ctx), "aggkit-proxy health check never succeeded")

	sender := newProxyReorgSender(t)
	funder := *testEnv.L1.Transactor
	sender.fundNative(ctx, t, testEnv.Clients.L1, &funder)
	opts := sender.opts(t, testEnv.L1.ChainID)
	opts.Context = ctx
	const l1NetworkID = 0

	sent := make([]activityPaginationKey, 0, activityPaginationBridges)
	for range activityPaginationBridges {
		tx := sendL1Bridge(t, opts, testEnv.L2.NetworkID, sender.addr, proxyReorgBridgeAmount)
		receipt := requireMined(ctx, t, testEnv.Clients.L1, tx, "L1 bridge")
		sent = append(sent, activityPaginationKey{network: l1NetworkID, depositCount: l1DepositCount(t, receipt)})
	}

	_, err := waitForActivity(ctx, proxyTrackerBaseURL, sender.addr, "every bridge is listed",
		proxyReorgConvergeTimeout, func(a *proxyActivityResponse) bool {
			if a.Count != activityPaginationBridges {
				return false
			}
			for _, k := range sent {
				if a.find(k.network, k.depositCount) == nil {
					return false
				}
			}
			return true
		})
	require.NoError(t, err)

	t.Run("pages hold every bridge once, in order, whatever the page size", func(t *testing.T) {
		full := traverseActivity(ctx, t, proxyTrackerBaseURL, sender.addr, "", activityPaginationPageSize)
		wantPages := (activityPaginationBridges + activityPaginationPageSize - 1) / activityPaginationPageSize
		require.Len(t, full.pages, wantPages+1, "the page after the last one is served, empty")
		for i, page := range full.pages {
			require.Equal(t, activityPaginationBridges, page.Count, "page %d reports the total", i+1)
			switch {
			case i < wantPages-1:
				require.Len(t, page.Bridges, activityPaginationPageSize, "page %d is full", i+1)
			case i == wantPages-1:
				require.Len(t, page.Bridges, activityPaginationBridges-(wantPages-1)*activityPaginationPageSize)
			default:
				require.Empty(t, page.Bridges, "past the end")
			}
		}
		items := full.items()
		keys := keysOfItems(items)
		require.ElementsMatch(t, sent, keys, "every bridge exactly once, no duplicate nor gap between pages")
		requireDocumentedOrder(t, items)

		for _, pageSize := range []int{1, 3, int(activityMaxPageSize)} {
			other := traverseActivity(ctx, t, proxyTrackerBaseURL, sender.addr, "", pageSize)
			require.Equal(t, keys, keysOfItems(other.items()), "page size %d serves another order", pageSize)
		}
	})

	t.Run("filterBridges combines with the pagination", func(t *testing.T) {
		statusesOf := map[string][]string{
			"pending":      {claimStatusPending},
			"readyToClaim": {claimStatusReadyToClaim},
			"claimed":      {claimStatusClaimed, claimStatusClaimedFinalized},
			"error":        {"error"},
		}
		for range activityPaginationAttempts {
			full := traverseActivity(ctx, t, proxyTrackerBaseURL, sender.addr, "", int(activityMaxPageSize))
			revision := full.pages[0].Revision
			consistent := true
			for filter, statuses := range statusesOf {
				var want []activityPaginationKey
				for _, item := range full.items() {
					if slices.Contains(statuses, item.ClaimStatus) {
						want = append(want, activityPaginationKey{item.BridgeNetworkID, item.Bridge.DepositCount})
					}
				}
				filtered := traverseActivity(ctx, t, proxyTrackerBaseURL, sender.addr, filter, 2)
				if filtered.pages[0].Revision != revision {
					consistent = false // a status changed between both traversals: compare again
					break
				}
				for _, page := range filtered.pages {
					require.Equal(t, len(want), page.Count, "filterBridges=%s total", filter)
					for _, item := range page.Bridges {
						require.Contains(t, statuses, item.ClaimStatus, "filterBridges=%s", filter)
					}
				}
				require.Equal(t, want, keysOfItems(filtered.items()), "filterBridges=%s", filter)
			}
			if consistent {
				return
			}
		}
		require.FailNow(t, "the statuses kept changing while comparing the filters")
	})

	t.Run("the current revision is answered 304", func(t *testing.T) {
		for range activityPaginationAttempts {
			code, body, etag, err := getActivityQuery(ctx, proxyTrackerBaseURL, sender.addr, "page_size=1", "")
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, code)
			require.Equal(t, `"`+body.Revision+`"`, etag)
			code, _, notModifiedETag, err := getActivityQuery(ctx, proxyTrackerBaseURL, sender.addr,
				"page_number=3&page_size=5", etag)
			require.NoError(t, err)
			if notModifiedETag != etag {
				continue // the activity changed in between: try again
			}
			require.Equal(t, http.StatusNotModified, code, "the same revision on another page must be a 304")
			return
		}
		require.FailNow(t, "the revision kept changing")
	})
}
