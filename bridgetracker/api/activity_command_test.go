package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakeActivityRegistry is a hand-rolled domain.ActivityRegistry for tests: importing the
// generated bridgetracker/mocks package here would create an import cycle (mocks -> bridgetracker
// -> bridgetracker/api), so this package follows the same hand-rolled-fake convention the
// bridgetracker package itself uses for its own driven ports (see e.g. fakeActivityScanner).
// Every call is recorded in order into calls, so a test can assert relative call ordering
// (e.g. FlushActivity before RegisterAndAwait) without needing a full mocking framework.
type fakeActivityRegistry struct {
	calls []string

	// registerAndAwaitReady is RegisterAndAwait's ready return value; defaults to true (most
	// tests exercise the already-ready path), set to false to exercise the not-ready-yet 503
	registerAndAwaitReady bool
	registerAndAwaitErr   error
	getActivityEntries    []*domain.ActivityEntry
	getActivityWarnings   []domain.ActivityWarning
	getActivityErr        error

	lastRegisterAddress         common.Address
	lastRegisterIncludeTracking bool
	lastRegisterTimeout         time.Duration
	lastFlushAddress            common.Address
	lastGetIncludeTrack         bool
	lastGetFilter               types.ActivityFilter
}

// newFakeActivityRegistry returns a fakeActivityRegistry whose RegisterAndAwait reports ready,
// so a test only needs to override the fields it actually cares about
func newFakeActivityRegistry() *fakeActivityRegistry {
	return &fakeActivityRegistry{registerAndAwaitReady: true}
}

func (f *fakeActivityRegistry) RegisterAndAwait(
	fromAddress common.Address, includeTracking bool, timeout time.Duration,
) (bool, error) {
	f.calls = append(f.calls, "RegisterAndAwait")
	f.lastRegisterAddress = fromAddress
	f.lastRegisterIncludeTracking = includeTracking
	f.lastRegisterTimeout = timeout
	return f.registerAndAwaitReady, f.registerAndAwaitErr
}

func (f *fakeActivityRegistry) GetActiveAddresses() ([]common.Address, error) { return nil, nil }

func (f *fakeActivityRegistry) RefreshAddress(context.Context, common.Address) error { return nil }

func (f *fakeActivityRegistry) PruneIdle(time.Time) (int, error) { return 0, nil }

func (f *fakeActivityRegistry) GetActivity(
	_ context.Context, _ common.Address, includeTracking bool, filter types.ActivityFilter,
) ([]*domain.ActivityEntry, []domain.ActivityWarning, error) {
	f.calls = append(f.calls, "GetActivity")
	f.lastGetIncludeTrack = includeTracking
	f.lastGetFilter = filter
	return f.getActivityEntries, f.getActivityWarnings, f.getActivityErr
}

func (f *fakeActivityRegistry) FlushActivity(fromAddress common.Address) {
	f.calls = append(f.calls, "FlushActivity")
	f.lastFlushAddress = fromAddress
}

var testActivityFromAddress = common.HexToAddress("0x1111111111111111111111111111111111111111")

// newActivityTestContext builds a *gin.Context for GET /activity/from/{from_address}, with
// rawQuery as-is (e.g. "includeTracking=true&flush_cache=true"), backed by a real
// httptest.ResponseRecorder so a test can inspect any response header Execute sets (e.g.
// Retry-After) — gin.CreateTestContext(nil) would panic on the first c.Header call
func newActivityTestContext(rawQuery string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = &http.Request{URL: &url.URL{RawQuery: rawQuery}}
	c.Params = gin.Params{{Key: fromAddressParam, Value: testActivityFromAddress.Hex()}}
	return c
}

// TestActivityCommandExecute_RegistersBeforeReading verifies Execute registers from_address
// (see domain.ActivitySupervisedStore.RegisterAndAwait) before reading the cache (GetActivity),
// mirroring getTxStatusCommand's GetAndAwait-then-read ordering, and that the configured
// resolveTimeout is threaded through unchanged.
func TestActivityCommandExecute_RegistersBeforeReading(t *testing.T) {
	registry := newFakeActivityRegistry()
	cmd := &activityCommand{registry: registry, resolveTimeout: 5 * time.Second}

	code, obj, errData := cmd.Execute(newActivityTestContext(""))
	require.Nil(t, errData)
	require.Equal(t, http.StatusOK, code)
	body, ok := obj.(ActivityResponse)
	require.True(t, ok)
	require.Equal(t, testActivityFromAddress, body.FromAddress)
	require.Empty(t, body.Bridges)

	require.Equal(t, []string{"RegisterAndAwait", "GetActivity"}, registry.calls)
	require.Equal(t, testActivityFromAddress, registry.lastRegisterAddress)
	require.Equal(t, 5*time.Second, registry.lastRegisterTimeout)
}

// TestActivityCommandExecute_FlushCacheRunsBeforeRegister verifies ?flush_cache=true calls
// FlushActivity before RegisterAndAwait, mirroring getTxStatusCommand's flush-then-register
// ordering for the tracker endpoint.
func TestActivityCommandExecute_FlushCacheRunsBeforeRegister(t *testing.T) {
	registry := newFakeActivityRegistry()
	cmd := &activityCommand{registry: registry}

	code, _, errData := cmd.Execute(newActivityTestContext("flush_cache=true"))
	require.Nil(t, errData)
	require.Equal(t, http.StatusOK, code)

	require.Equal(t, []string{"FlushActivity", "RegisterAndAwait", "GetActivity"}, registry.calls)
	require.Equal(t, testActivityFromAddress, registry.lastFlushAddress)
}

// TestActivityCommandExecute_InvalidFilterRejectedBeforeFlush verifies an invalid filterBridges
// value 400s before FlushActivity runs: flush_cache=true must not discard the cache for a
// request that is about to be rejected anyway (previously flush ran first and validation only
// rejected the request afterward, so a client-side typo in filterBridges paid for a full cache
// flush and re-registration for nothing).
func TestActivityCommandExecute_InvalidFilterRejectedBeforeFlush(t *testing.T) {
	registry := newFakeActivityRegistry()
	cmd := &activityCommand{registry: registry}

	code, obj, errData := cmd.Execute(newActivityTestContext("flush_cache=true&filterBridges=bogus"))
	require.Zero(t, code)
	require.Nil(t, obj)
	require.NotNil(t, errData)
	require.Equal(t, http.StatusBadRequest, errData.Code)
	require.Empty(t, registry.calls, "an invalid filter must reject before touching the registry at all")
}

// TestActivityCommandExecute_IncludeTrackingPassedToRegisterAndAwait verifies includeTracking is
// threaded into RegisterAndAwait itself, not only the later GetActivity call: the sticky flag
// must be set before the engine's triggered refresh runs, or a client polling with both
// includeTracking=true and flush_cache=true could never observe Tracking (the flush resets the
// flag, and GetActivity would only re-set it after that refresh already ran without it).
func TestActivityCommandExecute_IncludeTrackingPassedToRegisterAndAwait(t *testing.T) {
	registry := newFakeActivityRegistry()
	cmd := &activityCommand{registry: registry}

	code, _, errData := cmd.Execute(newActivityTestContext("includeTracking=true"))
	require.Nil(t, errData)
	require.Equal(t, http.StatusOK, code)

	require.True(t, registry.lastRegisterIncludeTracking,
		"RegisterAndAwait must receive includeTracking=true, not just GetActivity")
}

// TestActivityCommandExecute_RegistryFullMapsTo503 verifies ErrActivityRegistryFull maps to a
// 503 response instead of a generic 500, and never reaches GetActivity.
func TestActivityCommandExecute_RegistryFullMapsTo503(t *testing.T) {
	registry := &fakeActivityRegistry{registerAndAwaitErr: domain.ErrActivityRegistryFull}
	cmd := &activityCommand{registry: registry}

	code, obj, errData := cmd.Execute(newActivityTestContext(""))
	require.Zero(t, code)
	require.Nil(t, obj)
	require.NotNil(t, errData)
	require.Equal(t, http.StatusServiceUnavailable, errData.Code)
	require.Equal(t, []string{"RegisterAndAwait"}, registry.calls, "GetActivity must not run after a failed registration")
}

// TestActivityCommandExecute_RegisterFailureMapsTo500 verifies a plain RegisterAndAwait failure
// (anything other than ErrActivityRegistryFull) maps to a 500, and never reaches GetActivity.
func TestActivityCommandExecute_RegisterFailureMapsTo500(t *testing.T) {
	registry := &fakeActivityRegistry{registerAndAwaitErr: errBoom}
	cmd := &activityCommand{registry: registry}

	code, obj, errData := cmd.Execute(newActivityTestContext(""))
	require.Zero(t, code)
	require.Nil(t, obj)
	require.NotNil(t, errData)
	require.Equal(t, http.StatusInternalServerError, errData.Code)
	require.Equal(t, []string{"RegisterAndAwait"}, registry.calls)
}

// TestActivityCommandExecute_RegistryFullMapsTo503RedactsSensitiveTokens covers the
// ErrActivityRegistryFull branch of Execute: any URL/host/IP baked into the underlying
// RegisterAndAwait error must be redacted before it is stored in errData.Message. Asserted
// directly on the returned *types.ErrorData rather than only on the marshalled JSON, because the
// WebSocket close frame carries Message as a bare string that never reaches
// types.ErrorData.MarshalJSON - which is why every ErrorData built from an error inside this
// package redacts at the literal.
func TestActivityCommandExecute_RegistryFullMapsTo503RedactsSensitiveTokens(t *testing.T) {
	wrapped := fmt.Errorf("%w: dial tcp 10.0.0.5:8545: connect: connection refused", domain.ErrActivityRegistryFull)
	registry := &fakeActivityRegistry{registerAndAwaitErr: wrapped}
	cmd := &activityCommand{registry: registry}

	code, obj, errData := cmd.Execute(newActivityTestContext(""))
	require.Zero(t, code)
	require.Nil(t, obj)
	require.NotNil(t, errData)
	require.Equal(t, http.StatusServiceUnavailable, errData.Code)
	require.Equal(t, "activity registry is full: dial tcp <redacted-host>: connect: connection refused", errData.Message)
	require.NotContains(t, errData.Message, "10.0.0.5")
	require.NotContains(t, errData.Message, "://")
}

// TestActivityCommandExecute_RegisterFailureMapsTo500RedactsSensitiveTokens is the sibling of
// TestActivityCommandExecute_RegistryFullMapsTo503RedactsSensitiveTokens for Execute's generic
// (non-ErrActivityRegistryFull) RegisterAndAwait failure branch.
func TestActivityCommandExecute_RegisterFailureMapsTo500RedactsSensitiveTokens(t *testing.T) {
	rawErr := errors.New(`registering bridge tx with the tracker: dial tcp 10.0.0.7:8546: i/o timeout`)
	registry := &fakeActivityRegistry{registerAndAwaitErr: rawErr}
	cmd := &activityCommand{registry: registry}

	code, obj, errData := cmd.Execute(newActivityTestContext(""))
	require.Zero(t, code)
	require.Nil(t, obj)
	require.NotNil(t, errData)
	require.Equal(t, http.StatusInternalServerError, errData.Code)
	require.Equal(t, "registering bridge tx with the tracker: dial tcp <redacted-host>: i/o timeout", errData.Message)
	require.NotContains(t, errData.Message, "10.0.0.7")
	require.NotContains(t, errData.Message, "://")
}

// TestActivityCommandExecute_NotReadyMapsTo503WithRetryAfter verifies that a from_address whose
// first background refresh has not completed yet (RegisterAndAwait's ready=false) answers 503
// with a Retry-After header set to the configured pollInterval, and never reaches GetActivity —
// answering 200 with an empty result here would be indistinguishable from "no activity at all"
func TestActivityCommandExecute_NotReadyMapsTo503WithRetryAfter(t *testing.T) {
	registry := &fakeActivityRegistry{registerAndAwaitReady: false}
	cmd := &activityCommand{registry: registry, pollInterval: 30 * time.Second}

	c := newActivityTestContext("")
	code, obj, errData := cmd.Execute(c)
	require.Zero(t, code)
	require.Nil(t, obj)
	require.NotNil(t, errData)
	require.Equal(t, http.StatusServiceUnavailable, errData.Code)
	require.Equal(t, "30", c.Writer.Header().Get(retryAfterHeader))
	require.Equal(t, []string{"RegisterAndAwait"}, registry.calls, "GetActivity must not run while not ready")
}

type boomError struct{}

func (boomError) Error() string { return "boom" }

var errBoom error = boomError{}

// TestActivityItemMarshalJSON_EmbeddedBridgeGlobalIndexIsQuotedString verifies that
// GET /activity/from/{from_address}'s payload -- which embeds the bridge service's own
// BridgeResponse in ActivityItem.Bridge unmodified -- marshals an L1-origin deposit's
// global_index (2^64, past JavaScript's Number.MAX_SAFE_INTEGER) as a quoted JSON string rather
// than a bare number. This is the path the SDK actually consumes, so the embedding must not
// regress even if BridgeResponse's own marshalling is correct in isolation.
func TestActivityItemMarshalJSON_EmbeddedBridgeGlobalIndexIsQuotedString(t *testing.T) {
	// networkID=0 (mainnet), depositCount=0 -> the same L1-origin encoding NewBridgeResponse
	// uses internally, yielding 2^64 == 18446744073709551616, matching this step's live capture.
	globalIndex := bridgesync.GenerateGlobalIndexForNetworkID(0, 0)
	require.Equal(t, "18446744073709551616", globalIndex.String(), "test fixture must equal 2^64")

	item := ActivityItem{
		Bridge: &bridgeservicetypes.BridgeResponse{
			GlobalIndex: bridgeservicetypes.BigIntString(globalIndex.String()),
		},
		BridgeNetworkID: 0,
		ClaimStatus:     "pending",
	}

	data, err := json.Marshal(item)
	require.NoError(t, err)

	require.Contains(t, string(data), `"global_index":"18446744073709551616"`,
		"embedded BridgeResponse.GlobalIndex must marshal as a quoted string")
	require.NotContains(t, string(data), `"global_index":18446744073709551616`,
		"embedded BridgeResponse.GlobalIndex must never marshal as a bare JSON number")
}

// TestActivityItemMarshalJSON_ErrorsAreRedacted pins that ActivityItem.Errors (surfaced by
// GET /activity/from/{from_address} as "errors") never leaks a backend URL or host:port to an API
// client. The producers store the raw error on purpose (it is what they also log), so this
// marshaler is the layer that has to redact it.
func TestActivityItemMarshalJSON_ErrorsAreRedacted(t *testing.T) {
	item := ActivityItem{
		BridgeNetworkID: 2,
		ClaimStatus:     "error",
		Errors: map[string]string{
			// Simulates a construction site that forgot to redact (defensive coverage).
			"claim": `resolving JSON-RPC client for network 2: dialing JSON-RPC client of network 2 at ` +
				`http://l2-anvil-002.internal:8545: Post "http://l2-anvil-002.internal:8545": ` +
				`dial tcp 10.0.0.5:8545: connect: connection refused`,
		},
	}

	data, err := json.Marshal(item)
	require.NoError(t, err)

	require.NotContains(t, string(data), "://")
	require.NotContains(t, string(data), "l2-anvil-002.internal")
	require.NotContains(t, string(data), "10.0.0.5")

	var decoded ActivityItem
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "resolving JSON-RPC client for network 2: "+
		"dialing JSON-RPC client of network 2 at <redacted-url>: Post <redacted-url>: "+
		"dial tcp <redacted-host>: connect: connection refused", decoded.Errors["claim"])
}

// TestActivityWarningItemMarshalJSON_MessageIsRedacted pins that ActivityWarningItem.Message
// (surfaced by GET /activity/from/{from_address} as "warnings[].message") never leaks a backend
// URL or host:port to an API client. sources.ActivitySource.warnf keeps the raw message on purpose
// (it logs the identical string), so this marshaler is the layer that has to redact it.
func TestActivityWarningItemMarshalJSON_MessageIsRedacted(t *testing.T) {
	item := ActivityWarningItem{
		NetworkID: 2,
		Message: `fetching bridges from 0xabc on network 2: do request: ` +
			`Get "http://aggkit-002.internal:5577/bridge/v1/bridges?from_address=0xabc": ` +
			`dial tcp 10.0.0.6:5577: connection refused`,
	}

	data, err := json.Marshal(item)
	require.NoError(t, err)

	require.NotContains(t, string(data), "://")
	require.NotContains(t, string(data), "aggkit-002.internal")
	require.NotContains(t, string(data), "10.0.0.6")

	var decoded ActivityWarningItem
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "fetching bridges from 0xabc on network 2: "+
		"do request: Get <redacted-url>: dial tcp <redacted-host>: connection refused", decoded.Message)
}

// TestActivityResponseMarshalJSON_NoRemainingURLs is the end-to-end pass over the full wire shape
// GET /activity/from/{from_address} returns: neither a bridge's "errors" nor a top-level
// "warnings[].message" may contain a raw backend URL or host:port once marshalled, however the
// values got there.
func TestActivityResponseMarshalJSON_NoRemainingURLs(t *testing.T) {
	resp := ActivityResponse{
		Bridges: []ActivityItem{{
			BridgeNetworkID: 2,
			ClaimStatus:     "error",
			Errors: map[string]string{
				"readiness": `fetching l1 info tree index for network 2 deposit 7: do request: ` +
					`Get "http://aggkit-002.internal:5577/bridge/v1/l1-info-tree-index?network_id=2": ` +
					`dial tcp i/o timeout`,
			},
		}},
		Warnings: []ActivityWarningItem{{
			NetworkID: 2,
			Message: `fetching bridges from 0xabc on network 2: do request: ` +
				`Get "http://aggkit-002.internal:5577/bridge/v1/bridges?from_address=0xabc": ` +
				`dial tcp connection refused`,
		}},
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	require.NotContains(t, string(data), "://")
	require.NotContains(t, string(data), "aggkit-002.internal")
}

// activityEntryAt builds a bare domain.ActivityEntry for pagination tests: only CreatedAt (the
// sort key, see paginateActivityEntries) and TrackerClaimStatus (read by newActivityItems) need
// to be set, globalIndex only distinguishes entries in assertions
func activityEntryAt(globalIndex string, createdAt time.Time) *domain.ActivityEntry {
	return &domain.ActivityEntry{
		Bridge:             &bridgeservicetypes.BridgeResponse{GlobalIndex: bridgeservicetypes.BigIntString(globalIndex)},
		TrackerClaimStatus: types.TrackerClaimStatusPending,
		CreatedAt:          createdAt,
	}
}

// bridgeGlobalIndexes extracts, in order, the global_index of every ActivityItem in bridges
func bridgeGlobalIndexes(bridges []ActivityItem) []string {
	out := make([]string, 0, len(bridges))
	for _, b := range bridges {
		out = append(out, string(b.Bridge.GlobalIndex))
	}
	return out
}

// TestActivityCommandExecute_SortsMostRecentFirst verifies GetActivity's result is sorted by
// CreatedAt descending before being returned, regardless of the order the registry reports it
// in — the activity engine's map-backed cache (ActivityCache) and the SQLite store's row order
// are both otherwise unspecified.
func TestActivityCommandExecute_SortsMostRecentFirst(t *testing.T) {
	now := time.Now()
	registry := newFakeActivityRegistry()
	registry.getActivityEntries = []*domain.ActivityEntry{
		activityEntryAt("1", now.Add(-time.Hour)),
		activityEntryAt("2", now),
		activityEntryAt("3", now.Add(-2*time.Hour)),
	}
	cmd := &activityCommand{registry: registry}

	code, obj, errData := cmd.Execute(newActivityTestContext(""))
	require.Nil(t, errData)
	require.Equal(t, http.StatusOK, code)
	body, ok := obj.(ActivityResponse)
	require.True(t, ok)

	require.Equal(t, []string{"2", "1", "3"}, bridgeGlobalIndexes(body.Bridges))
	require.Equal(t, 3, body.Count)
}

// TestActivityCommandExecute_TiedCreatedAtOrderedByGlobalIndex verifies entries sharing a
// CreatedAt are ordered by global index descending (numerically, not as text) whatever order the
// registry reports them in, so paging never moves a tied entry across a page boundary.
func TestActivityCommandExecute_TiedCreatedAtOrderedByGlobalIndex(t *testing.T) {
	now := time.Now()
	orders := [][]string{{"9", "10", "2"}, {"2", "10", "9"}, {"10", "2", "9"}}
	for _, order := range orders {
		entries := make([]*domain.ActivityEntry, 0, len(order))
		for _, gi := range order {
			entries = append(entries, activityEntryAt(gi, now))
		}
		registry := newFakeActivityRegistry()
		registry.getActivityEntries = entries
		cmd := &activityCommand{registry: registry}

		_, obj, errData := cmd.Execute(newActivityTestContext(""))
		require.Nil(t, errData)
		body, ok := obj.(ActivityResponse)
		require.True(t, ok)
		require.Equal(t, []string{"10", "9", "2"}, bridgeGlobalIndexes(body.Bridges))
	}
}

// TestActivityCommandExecute_DefaultPagination verifies that with no page_number/page_size
// given, Execute answers page 1 of size 20 (bridgeservice's own defaults, see
// bridgeservice.DefaultPage/DefaultPageSize) and Count reports the true total rather than
// len(Bridges).
func TestActivityCommandExecute_DefaultPagination(t *testing.T) {
	now := time.Now()
	entries := make([]*domain.ActivityEntry, 0, 25)
	for i := range 25 {
		entries = append(entries, activityEntryAt(strconv.Itoa(i), now.Add(-time.Duration(i)*time.Minute)))
	}
	registry := newFakeActivityRegistry()
	registry.getActivityEntries = entries
	cmd := &activityCommand{registry: registry}

	code, obj, errData := cmd.Execute(newActivityTestContext(""))
	require.Nil(t, errData)
	require.Equal(t, http.StatusOK, code)
	body, ok := obj.(ActivityResponse)
	require.True(t, ok)

	require.Len(t, body.Bridges, 20, "default page_size must be 20")
	require.Equal(t, 25, body.Count)
	require.Equal(t, "0", string(body.Bridges[0].Bridge.GlobalIndex), "most recent entry must lead page 1")
}

// TestActivityCommandExecute_ExplicitPageNumberAndSize verifies page_number/page_size slice the
// sorted (most-recent-first) result exactly like the bridge service's own paginated endpoints.
func TestActivityCommandExecute_ExplicitPageNumberAndSize(t *testing.T) {
	now := time.Now()
	registry := newFakeActivityRegistry()
	registry.getActivityEntries = []*domain.ActivityEntry{
		activityEntryAt("0", now),
		activityEntryAt("1", now.Add(-time.Minute)),
		activityEntryAt("2", now.Add(-2*time.Minute)),
		activityEntryAt("3", now.Add(-3*time.Minute)),
		activityEntryAt("4", now.Add(-4*time.Minute)),
	}
	cmd := &activityCommand{registry: registry}

	code, obj, errData := cmd.Execute(newActivityTestContext("page_number=2&page_size=2"))
	require.Nil(t, errData)
	require.Equal(t, http.StatusOK, code)
	body, ok := obj.(ActivityResponse)
	require.True(t, ok)

	require.Equal(t, []string{"2", "3"}, bridgeGlobalIndexes(body.Bridges))
	require.Equal(t, 5, body.Count)
}

// TestActivityCommandExecute_PageBeyondRangeReturnsEmptyBridgesWithCount verifies a page past the
// last one answers 200 with an empty (not nil-vs-empty-ambiguous) Bridges slice and the true
// Count, rather than an error — the caller already knows from Count that there is nothing more
// to page through.
func TestActivityCommandExecute_PageBeyondRangeReturnsEmptyBridgesWithCount(t *testing.T) {
	registry := newFakeActivityRegistry()
	registry.getActivityEntries = []*domain.ActivityEntry{activityEntryAt("0", time.Now())}
	cmd := &activityCommand{registry: registry}

	code, obj, errData := cmd.Execute(newActivityTestContext("page_number=5&page_size=20"))
	require.Nil(t, errData)
	require.Equal(t, http.StatusOK, code)
	body, ok := obj.(ActivityResponse)
	require.True(t, ok)

	require.Empty(t, body.Bridges)
	require.Equal(t, 1, body.Count)
}

// TestActivityCommandExecute_MaxPageSizeAccepted verifies page_size=200 (the maximum) is accepted
// and page_size=201 is not.
func TestActivityCommandExecute_MaxPageSizeAccepted(t *testing.T) {
	registry := newFakeActivityRegistry()
	cmd := &activityCommand{registry: registry}

	code, _, errData := cmd.Execute(newActivityTestContext("page_size=200"))
	require.Nil(t, errData)
	require.Equal(t, http.StatusOK, code)

	_, _, errData = cmd.Execute(newActivityTestContext("page_size=201"))
	require.NotNil(t, errData)
	require.Equal(t, http.StatusBadRequest, errData.Code)
}

// TestActivityCommandExecute_InvalidPaginationParamsRejectedBeforeFlush verifies invalid
// page_number/page_size values 400 before FlushActivity runs, mirroring
// TestActivityCommandExecute_InvalidFilterRejectedBeforeFlush's reasoning for filterBridges.
func TestActivityCommandExecute_InvalidPaginationParamsRejectedBeforeFlush(t *testing.T) {
	tests := map[string]string{
		"page_number zero":         "page_number=0",
		"page_number not a number": "page_number=abc",
		"page_size zero":           "page_size=0",
		"page_size over max":       "page_size=201",
	}
	for name, query := range tests {
		t.Run(name, func(t *testing.T) {
			registry := newFakeActivityRegistry()
			cmd := &activityCommand{registry: registry}

			code, obj, errData := cmd.Execute(newActivityTestContext("flush_cache=true&" + query))
			require.Zero(t, code)
			require.Nil(t, obj)
			require.NotNil(t, errData)
			require.Equal(t, http.StatusBadRequest, errData.Code)
			require.Empty(t, registry.calls, "invalid pagination params must reject before touching the registry at all")
		})
	}
}
