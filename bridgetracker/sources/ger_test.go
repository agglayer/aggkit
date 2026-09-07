package sources

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// startL1InfoTreeIndexServer starts an httptest server serving GET
// /bridge/v1/l1-info-tree-index: index if non-nil, a "not found" error otherwise (mirroring the
// real endpoint's 404 for "not indexed yet", see bridgeservice.L1InfoTreeIndexForBridgeHandler).
// The returned func reports the network_id the last request queried, so tests can assert which
// instance actually received the call
func startL1InfoTreeIndexServer(t *testing.T, index *uint32) (*httptest.Server, func() string) {
	t.Helper()

	var lastNetworkID string
	mux := http.NewServeMux()
	mux.HandleFunc("/bridge/v1/l1-info-tree-index", func(w http.ResponseWriter, r *http.Request) {
		lastNetworkID = r.URL.Query().Get("network_id")
		if index == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"l1 info tree index for deposit is not available yet: not found"}`)
			return
		}
		fmt.Fprintf(w, "%d", *index)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, func() string { return lastNetworkID }
}

// newTestGERSource returns a GERSource resolving bridge-service instances through urls; the
// other NewGERSource arguments are irrelevant to L1InfoTreeIndexForBridge
func newTestGERSource(urls NetworkURLResolver) *GERSource {
	return NewGERSource(urls, nil, common.Address{}, aggkittypes.BlockNumberFinality{}, nil, 0, log.NewLoggerNil())
}

// TestL1InfoTreeIndexForBridge pins the fix for the review finding on #1829: mainnet (network
// 0) has no bridge-service deployment of its own — bridgeservicefinder.GetURL(0) fails unless a
// network-0 URL was explicitly configured — so for an L1-originated bridge the request must go
// to the destination's own (resolvable) instance, with network_id=0 as a query parameter, never
// by resolving a URL for network 0 itself. For an L2-originated bridge, the origin's own
// instance is both the queried instance and the query's own network_id (a self-query)
func TestL1InfoTreeIndexForBridge(t *testing.T) {
	t.Parallel()

	t.Run("L1-originated bridge: destination's own instance, network 0 never resolved directly", func(t *testing.T) {
		t.Parallel()

		index := uint32(42)
		destServer, destQueriedNetworkID := startL1InfoTreeIndexServer(t, &index)

		// only network 1 (the destination) has a resolvable URL — network 0 deliberately does
		// not, matching a deployment that never configured Config.BridgeURLs[0]
		urls := staticURLs{1: bridgeservicefinder.NetworkURLs{BridgeURL: destServer.URL}}
		gerSource := newTestGERSource(urls)

		bridge := &bridgetracker.BridgeInfo{NetworkID: MainnetNetworkID, DestinationNetwork: 1, DepositCount: 7}
		got, err := gerSource.L1InfoTreeIndexForBridge(t.Context(), bridge)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, index, *got)
		require.Equal(t, "0", destQueriedNetworkID(),
			"the destination instance is asked about network_id=0 (mainnet) as a query param")
	})

	t.Run("L2-originated bridge: the origin's own instance, self-queried", func(t *testing.T) {
		t.Parallel()

		index := uint32(9)
		originServer, originQueriedNetworkID := startL1InfoTreeIndexServer(t, &index)

		// only network 1 (the origin) has a resolvable URL; the destination (network 2) is
		// deliberately absent — this must never be queried for this bridge
		urls := staticURLs{1: bridgeservicefinder.NetworkURLs{BridgeURL: originServer.URL}}
		gerSource := newTestGERSource(urls)

		bridge := &bridgetracker.BridgeInfo{NetworkID: 1, DestinationNetwork: 2, DepositCount: 3}
		got, err := gerSource.L1InfoTreeIndexForBridge(t.Context(), bridge)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, index, *got)
		require.Equal(t, "1", originQueriedNetworkID())
	})

	t.Run("not yet covered by the queried instance's own L1 info tree sync -> nil, not an error", func(t *testing.T) {
		t.Parallel()

		server, _ := startL1InfoTreeIndexServer(t, nil)
		urls := staticURLs{1: bridgeservicefinder.NetworkURLs{BridgeURL: server.URL}}
		gerSource := newTestGERSource(urls)

		bridge := &bridgetracker.BridgeInfo{NetworkID: 1, DestinationNetwork: 2, DepositCount: 1}
		got, err := gerSource.L1InfoTreeIndexForBridge(t.Context(), bridge)
		require.NoError(t, err)
		require.Nil(t, got)
	})

	t.Run("URL resolution failure is a transient error", func(t *testing.T) {
		t.Parallel()

		gerSource := newTestGERSource(staticURLs{}) // no network resolvable at all

		bridge := &bridgetracker.BridgeInfo{NetworkID: 1, DestinationNetwork: 2, DepositCount: 1}
		_, err := gerSource.L1InfoTreeIndexForBridge(t.Context(), bridge)
		require.ErrorIs(t, err, bridgeservicefinder.ErrURLNotFound)
	})
}
