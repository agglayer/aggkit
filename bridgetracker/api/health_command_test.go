package api

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
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

// fakeCacheStatsRegistry embeds fakeSupervisedRegistry (see get_tx_status_command_test.go) and
// additionally implements domain.CacheStatsProvider, standing in for the SQLite-backed adapter
// (see bridgetracker/db.sqliteRegistry) so healthCommand's memory-vs-disk detection can be
// exercised without a real database
type fakeCacheStatsRegistry struct {
	fakeSupervisedRegistry
	sizeBytes int64
	statsErr  error
}

func (f *fakeCacheStatsRegistry) CacheStats() (domain.CacheStats, error) {
	return domain.CacheStats{SizeBytes: f.sizeBytes}, f.statsErr
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

// TestHealthCommandExecute_StartDateIsServedAsRFC3339 verifies that the instance's start date is
// reported verbatim on every response of one execution (it is captured once, at construction
// time), serialized as an RFC3339 UTC instant - the reference point pending_networks entries'
// first_seen values are relative to.
func TestHealthCommandExecute_StartDateIsServedAsRFC3339(t *testing.T) {
	startDate := time.Date(2026, 9, 22, 10, 30, 0, 0, time.UTC)
	cmd := &healthCommand{instanceID: "instance-1", startDate: startDate, configSHA1: "sha1"}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, obj, errData := cmd.Execute(c)
	require.Nil(t, errData)

	resp, ok := obj.(types.HealthResponse)
	require.True(t, ok)
	require.Equal(t, startDate, resp.StartDate)

	data, err := json.Marshal(obj)
	require.NoError(t, err)
	require.Contains(t, string(data), `"start_date":"2026-09-22T10:30:00Z"`)

	// A second call reports the identical instant: the field describes the execution, not "now".
	_, again, errData := cmd.Execute(c)
	require.Nil(t, errData)
	respAgain, ok := again.(types.HealthResponse)
	require.True(t, ok)
	require.Equal(t, resp.StartDate, respAgain.StartDate)
}

// TestNewAPIHealthStartDateIsSetAtConstruction verifies the wiring NewAPI does: the health
// command's start date is stamped when the API is built (not left zero, which would serialize as
// "0001-01-01T00:00:00Z"), in UTC, alongside the instance id it belongs to.
func TestNewAPIHealthStartDateIsSetAtConstruction(t *testing.T) {
	before := time.Now().UTC()
	api := NewAPI(nil, "sha1", nil, nil, nil, 0, 0, 0, aggkitcommon.CORSConfig{}, nil)
	after := time.Now().UTC()

	require.NotNil(t, api.healthCmd)
	require.False(t, api.healthCmd.startDate.IsZero(), "start date must be stamped at construction")
	require.Equal(t, time.UTC, api.healthCmd.startDate.Location())
	require.False(t, api.healthCmd.startDate.Before(before))
	require.False(t, api.healthCmd.startDate.After(after))
}

// TestNewAPIHealthWiresSupervisedAndActivity verifies that NewAPI threads the same supervised
// registry and activity registry it was given into the health command, so GET /health can report
// their alive counts/cache size instead of only instance identity.
func TestNewAPIHealthWiresSupervisedAndActivity(t *testing.T) {
	supervised := &fakeSupervisedRegistry{}
	activity := newFakeActivityRegistry()
	api := NewAPI(nil, "sha1", supervised, activity, nil, 0, 0, 0, aggkitcommon.CORSConfig{}, nil)

	require.NotNil(t, api.healthCmd)
	gotSupervised, ok := api.healthCmd.supervised.(*fakeSupervisedRegistry)
	require.True(t, ok)
	require.Same(t, supervised, gotSupervised)

	gotActivity, ok := api.healthCmd.activity.(*fakeActivityRegistry)
	require.True(t, ok)
	require.Same(t, activity, gotActivity)
}

// TestHealthCommandExecute_NoSupervisedOrActivityOmitsCounts verifies healthCommand.Execute
// never panics on a nil supervised/activity (the wiring test above always sets supervised, but
// some earlier tests in this file construct a bare healthCommand to isolate pending_networks
// behavior): Cache reports memory and both alive counts stay at their zero value.
func TestHealthCommandExecute_NoSupervisedOrActivityOmitsCounts(t *testing.T) {
	cmd := &healthCommand{instanceID: "instance-1", configSHA1: "sha1"}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, obj, errData := cmd.Execute(c)
	require.Nil(t, errData)

	resp, ok := obj.(types.HealthResponse)
	require.True(t, ok)
	require.Equal(t, types.CacheInfo{Kind: types.CacheKindMemory}, resp.Cache)
	require.Equal(t, 0, resp.AliveTrackers)
	require.Nil(t, resp.AliveActivities)
}

// TestHealthCommandExecute_MemoryRegistryReportsMemoryKindAndAliveTrackers verifies that a
// supervised registry not implementing domain.CacheStatsProvider (the in-memory adapter) is
// reported as CacheKindMemory with no size, while AliveTrackers still reflects
// GetTrackerActives's length.
func TestHealthCommandExecute_MemoryRegistryReportsMemoryKindAndAliveTrackers(t *testing.T) {
	supervised := &fakeSupervisedRegistry{
		getTrackerActives: []*domain.TrackingData{{}, {}, {}},
	}
	cmd := &healthCommand{instanceID: "instance-1", configSHA1: "sha1", supervised: supervised}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, obj, errData := cmd.Execute(c)
	require.Nil(t, errData)

	resp, ok := obj.(types.HealthResponse)
	require.True(t, ok)
	require.Equal(t, types.CacheInfo{Kind: types.CacheKindMemory}, resp.Cache)
	require.Equal(t, 3, resp.AliveTrackers)

	data, err := json.Marshal(obj)
	require.NoError(t, err)
	require.Contains(t, string(data), `"kind":"memory"`)
	require.NotContains(t, string(data), "size_bytes")
}

// TestHealthCommandExecute_SQLiteRegistryReportsDiskKindAndSize verifies that a supervised
// registry implementing domain.CacheStatsProvider (the SQLite-backed adapter) is reported as
// CacheKindDisk with its SizeBytes serialized.
func TestHealthCommandExecute_SQLiteRegistryReportsDiskKindAndSize(t *testing.T) {
	supervised := &fakeCacheStatsRegistry{sizeBytes: 4096}
	cmd := &healthCommand{instanceID: "instance-1", configSHA1: "sha1", supervised: supervised}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, obj, errData := cmd.Execute(c)
	require.Nil(t, errData)

	resp, ok := obj.(types.HealthResponse)
	require.True(t, ok)
	require.Equal(t, types.CacheInfo{Kind: types.CacheKindDisk, SizeBytes: 4096}, resp.Cache)

	data, err := json.Marshal(obj)
	require.NoError(t, err)
	require.Contains(t, string(data), `"kind":"disk"`)
	require.Contains(t, string(data), `"size_bytes":4096`)
}

// TestHealthCommandExecute_CacheStatsErrorFallsBackToDiskKindNoSize verifies that a
// CacheStatsProvider error still reports CacheKindDisk (the adapter is disk-backed regardless)
// but omits SizeBytes, instead of failing the whole health check.
func TestHealthCommandExecute_CacheStatsErrorFallsBackToDiskKindNoSize(t *testing.T) {
	supervised := &fakeCacheStatsRegistry{statsErr: errors.New("disk i/o error")}
	cmd := &healthCommand{instanceID: "instance-1", configSHA1: "sha1", supervised: supervised}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	code, obj, errData := cmd.Execute(c)
	require.Nil(t, errData)
	require.Equal(t, 200, code)

	resp, ok := obj.(types.HealthResponse)
	require.True(t, ok)
	require.Equal(t, types.CacheInfo{Kind: types.CacheKindDisk}, resp.Cache)
}

// TestHealthCommandExecute_NoActivityOmitsAliveActivities verifies that a nil activity registry
// (the activity endpoint not configured, see NewAPI's doc) omits alive_activities entirely
// rather than serializing it as zero — mirroring pending_networks' own omission convention.
func TestHealthCommandExecute_NoActivityOmitsAliveActivities(t *testing.T) {
	cmd := &healthCommand{instanceID: "instance-1", configSHA1: "sha1"}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, obj, errData := cmd.Execute(c)
	require.Nil(t, errData)

	resp, ok := obj.(types.HealthResponse)
	require.True(t, ok)
	require.Nil(t, resp.AliveActivities)

	data, err := json.Marshal(obj)
	require.NoError(t, err)
	require.NotContains(t, string(data), "alive_activities")
}

// TestHealthCommandExecute_ActivityReportsAliveAddressCount verifies that a configured activity
// registry's currently supervised addresses are counted into alive_activities.
func TestHealthCommandExecute_ActivityReportsAliveAddressCount(t *testing.T) {
	activity := newFakeActivityRegistry()
	activity.activeAddresses = []common.Address{
		common.HexToAddress("0x1111111111111111111111111111111111111111"),
		common.HexToAddress("0x2222222222222222222222222222222222222222"),
	}
	cmd := &healthCommand{instanceID: "instance-1", configSHA1: "sha1", activity: activity}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, obj, errData := cmd.Execute(c)
	require.Nil(t, errData)

	resp, ok := obj.(types.HealthResponse)
	require.True(t, ok)
	require.NotNil(t, resp.AliveActivities)
	require.Equal(t, 2, *resp.AliveActivities)

	data, err := json.Marshal(obj)
	require.NoError(t, err)
	require.Contains(t, string(data), `"alive_activities":2`)
}
