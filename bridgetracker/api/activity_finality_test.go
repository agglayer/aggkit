package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// fakeRevisionActivityRegistry is a fakeActivityRegistry that also implements
// domain.ActivityRevisionReader, recording its call into calls so a test can assert it runs
// before GetActivity
type fakeRevisionActivityRegistry struct {
	*fakeActivityRegistry
	revision    uint64
	revisionErr error
}

func (f *fakeRevisionActivityRegistry) GetActivityRevision(context.Context, common.Address) (uint64, error) {
	f.calls = append(f.calls, "GetActivityRevision")
	return f.revision, f.revisionErr
}

// TestActivityItemMarshalJSON_FinalizedIsAlwaysSerialized verifies ActivityItem.finalized is on
// the wire whatever its value (no omitempty): false until the follow-up issues populate it, and
// true once they do
func TestActivityItemMarshalJSON_FinalizedIsAlwaysSerialized(t *testing.T) {
	for _, finalized := range []bool{false, true} {
		data, err := json.Marshal(ActivityItem{ClaimStatus: "pending", Finalized: finalized})
		require.NoError(t, err)

		var raw map[string]any
		require.NoError(t, json.Unmarshal(data, &raw))
		value, present := raw["finalized"]
		require.True(t, present, "finalized must always be serialized")
		require.Equal(t, finalized, value)
	}
}

// TestActivityItemJSONRoundTrip_ClaimedFinalized verifies the new claim_status value and the
// finalized flag survive a JSON round trip unchanged, and that the wire value is the
// types.TrackerClaimStatusClaimedFinalized name
func TestActivityItemJSONRoundTrip_ClaimedFinalized(t *testing.T) {
	item := ActivityItem{
		BridgeNetworkID: 1,
		Source:          string(domain.ActivitySourceBridgeService),
		ClaimStatus:     types.TrackerClaimStatusClaimedFinalized.String(),
		Finalized:       true,
	}

	data, err := json.Marshal(item)
	require.NoError(t, err)
	require.Contains(t, string(data), `"claim_status":"claimed_finalized"`)
	require.Contains(t, string(data), `"finalized":true`)

	var decoded ActivityItem
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, item, decoded)

	status, err := types.ParseTrackerClaimStatus(decoded.ClaimStatus)
	require.NoError(t, err)
	require.Equal(t, types.TrackerClaimStatusClaimedFinalized, status)
}

// TestNewActivityItems_CarriesFinalizedAndClaimedFinalized verifies the entry's Finalized flag
// and a claimed_finalized TrackerClaimStatus reach the wire item as they are
func TestNewActivityItems_CarriesFinalizedAndClaimedFinalized(t *testing.T) {
	bridge := &bridgeservicetypes.BridgeResponse{GlobalIndex: "1"}
	items := newActivityItems([]*domain.ActivityEntry{
		{Bridge: bridge, TrackerClaimStatus: types.TrackerClaimStatusClaimedFinalized, Finalized: true},
		{Bridge: bridge, TrackerClaimStatus: types.TrackerClaimStatusClaimed},
	})

	require.Len(t, items, 2)
	require.Equal(t, "claimed_finalized", items[0].ClaimStatus)
	require.True(t, items[0].Finalized)
	require.Equal(t, "claimed", items[1].ClaimStatus)
	require.False(t, items[1].Finalized, "an entry that does not set Finalized is not finalized")
}

// TestActivityResponseMarshalJSON_RevisionIsAlwaysSerialized verifies revision is on the wire
// even at its zero value (no omitempty)
func TestActivityResponseMarshalJSON_RevisionIsAlwaysSerialized(t *testing.T) {
	for _, revision := range []uint64{0, 42} {
		data, err := json.Marshal(ActivityResponse{Bridges: []ActivityItem{}, Revision: revision})
		require.NoError(t, err)

		var raw map[string]any
		require.NoError(t, json.Unmarshal(data, &raw))
		value, present := raw["revision"]
		require.True(t, present, "revision must always be serialized")
		require.EqualValues(t, revision, value)
	}
}

// TestActivityCommandExecute_RevisionReadBeforeEntries verifies Execute serves the address's
// revision in every response and reads it BEFORE the entries (see domain.ActivityRevisionReader:
// the opposite order could report a revision newer than the entries beside it and hide a change)
func TestActivityCommandExecute_RevisionReadBeforeEntries(t *testing.T) {
	registry := &fakeRevisionActivityRegistry{fakeActivityRegistry: newFakeActivityRegistry(), revision: 7}
	registry.getActivityEntries = []*domain.ActivityEntry{activityEntryAt("1", time.Now())}
	cmd := &activityCommand{registry: registry}

	// the cheap poll a client sends to watch the revision, and a request for another page: both
	// carry the same revision, which is independent of the page
	for _, rawQuery := range []string{"page_size=1", "page_number=2&page_size=1"} {
		registry.calls = nil
		code, obj, errData := cmd.Execute(newActivityTestContext(rawQuery))
		require.Nil(t, errData)
		require.Equal(t, http.StatusOK, code)
		body, ok := obj.(ActivityResponse)
		require.True(t, ok)
		require.EqualValues(t, 7, body.Revision)
		require.Equal(t, []string{"RegisterAndAwait", "GetActivityRevision", "GetActivity"}, registry.calls)
	}
}

// TestActivityCommandExecute_RevisionZeroWithoutReader verifies a registry that does not
// implement domain.ActivityRevisionReader is served as revision 0, not as an error
func TestActivityCommandExecute_RevisionZeroWithoutReader(t *testing.T) {
	cmd := &activityCommand{registry: newFakeActivityRegistry()}

	code, obj, errData := cmd.Execute(newActivityTestContext(""))
	require.Nil(t, errData)
	require.Equal(t, http.StatusOK, code)
	body, ok := obj.(ActivityResponse)
	require.True(t, ok)
	require.Zero(t, body.Revision)
}

// TestActivityCommandExecute_RevisionErrorIs500 verifies a failure reading the revision maps to
// a 500 and never reaches GetActivity
func TestActivityCommandExecute_RevisionErrorIs500(t *testing.T) {
	registry := &fakeRevisionActivityRegistry{fakeActivityRegistry: newFakeActivityRegistry(), revisionErr: errBoom}
	cmd := &activityCommand{registry: registry}

	code, _, errData := cmd.Execute(newActivityTestContext(""))
	require.NotNil(t, errData)
	require.Equal(t, http.StatusInternalServerError, errData.Code)
	require.Zero(t, code)
	require.Equal(t, []string{"RegisterAndAwait", "GetActivityRevision"}, registry.calls)
}
