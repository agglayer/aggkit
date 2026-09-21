package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakePendingNetworksLister is a minimal PendingNetworksLister double: it returns whatever
// networks was set, unconditionally.
type fakePendingNetworksLister struct {
	networks []bridgeservicefinder.PendingNetwork
}

func (f *fakePendingNetworksLister) PendingNetworks() []bridgeservicefinder.PendingNetwork {
	return f.networks
}

// TestHealthCommandExecute_NoPendingListerOmitsKey verifies that a nil PendingNetworksLister
// (the default when the tracker is embedded without a bridge service finder) leaves
// pending_networks entirely absent from the JSON response, rather than serializing an empty list.
func TestHealthCommandExecute_NoPendingListerOmitsKey(t *testing.T) {
	cmd := &healthCommand{instanceID: "instance-1", configSHA1: "sha1"}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	code, obj, errData := cmd.Execute(c)
	require.Nil(t, errData)
	require.Equal(t, 200, code)

	data, err := json.Marshal(obj)
	require.NoError(t, err)
	require.NotContains(t, string(data), "pending_networks")
}

// TestHealthCommandExecute_EmptyPendingListerOmitsKey verifies that a lister returning no pending
// networks (the common case: the finder is wired but nothing is pending) also omits the key,
// rather than serializing it as an empty array.
func TestHealthCommandExecute_EmptyPendingListerOmitsKey(t *testing.T) {
	cmd := &healthCommand{
		instanceID:    "instance-1",
		configSHA1:    "sha1",
		pendingLister: &fakePendingNetworksLister{networks: nil},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, obj, errData := cmd.Execute(c)
	require.Nil(t, errData)

	data, err := json.Marshal(obj)
	require.NoError(t, err)
	require.NotContains(t, string(data), "pending_networks")
}

// TestHealthCommandExecute_TwoPendingNetworksSortedByID verifies that two pending networks are
// both listed, in the order the lister returns them (the finder is documented to already sort by
// ascending network id), and that RollupAddress renders as a hex string.
func TestHealthCommandExecute_TwoPendingNetworksSortedByID(t *testing.T) {
	firstSeen3 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	firstSeen5 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	cmd := &healthCommand{
		instanceID: "instance-1",
		configSHA1: "sha1",
		pendingLister: &fakePendingNetworksLister{networks: []bridgeservicefinder.PendingNetwork{
			{
				NetworkID:     3,
				RollupAddress: common.HexToAddress("0x0000000000000000000000000000000000000003"),
				BlockNumber:   100,
				FirstSeen:     firstSeen3,
				Reason:        bridgeservicefinder.PendingReasonRollupAttached,
			},
			{
				NetworkID:     5,
				RollupAddress: common.HexToAddress("0x0000000000000000000000000000000000000005"),
				BlockNumber:   200,
				FirstSeen:     firstSeen5,
				Reason:        bridgeservicefinder.PendingReasonFirstURLEvent,
			},
		}},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, obj, errData := cmd.Execute(c)
	require.Nil(t, errData)

	resp, ok := obj.(types.HealthResponse)
	require.True(t, ok)
	require.Len(t, resp.PendingNetworks, 2)

	require.Equal(t, uint32(3), resp.PendingNetworks[0].NetworkID)
	require.Equal(t, common.HexToAddress("0x0000000000000000000000000000000000000003").Hex(),
		resp.PendingNetworks[0].RollupAddress)
	require.Equal(t, uint64(100), resp.PendingNetworks[0].BlockNumber)
	require.Equal(t, firstSeen3, resp.PendingNetworks[0].FirstSeen)
	require.Equal(t, bridgeservicefinder.PendingReasonRollupAttached, resp.PendingNetworks[0].Reason)

	require.Equal(t, uint32(5), resp.PendingNetworks[1].NetworkID)
	require.Equal(t, common.HexToAddress("0x0000000000000000000000000000000000000005").Hex(),
		resp.PendingNetworks[1].RollupAddress)
	require.Equal(t, uint64(200), resp.PendingNetworks[1].BlockNumber)
	require.Equal(t, firstSeen5, resp.PendingNetworks[1].FirstSeen)
	require.Equal(t, bridgeservicefinder.PendingReasonFirstURLEvent, resp.PendingNetworks[1].Reason)

	data, err := json.Marshal(obj)
	require.NoError(t, err)
	require.Contains(t, string(data), `"pending_networks"`)
}
