package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
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

	registerAndAwaitErr error
	getActivityEntries  []*domain.ActivityEntry
	getActivityWarnings []domain.ActivityWarning
	getActivityErr      error

	lastRegisterAddress common.Address
	lastRegisterTimeout time.Duration
	lastFlushAddress    common.Address
	lastGetIncludeTrack bool
	lastGetFilter       types.ActivityFilter
}

func (f *fakeActivityRegistry) RegisterAndAwait(fromAddress common.Address, timeout time.Duration) error {
	f.calls = append(f.calls, "RegisterAndAwait")
	f.lastRegisterAddress = fromAddress
	f.lastRegisterTimeout = timeout
	return f.registerAndAwaitErr
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
// rawQuery as-is (e.g. "includeTracking=true&flush_cache=true")
func newActivityTestContext(rawQuery string) *gin.Context {
	c, _ := gin.CreateTestContext(nil)
	c.Request = &http.Request{URL: &url.URL{RawQuery: rawQuery}}
	c.Params = gin.Params{{Key: fromAddressParam, Value: testActivityFromAddress.Hex()}}
	return c
}

// TestActivityCommandExecute_RegistersBeforeReading verifies Execute registers from_address
// (see domain.ActivitySupervisedStore.RegisterAndAwait) before reading the cache (GetActivity),
// mirroring getTxStatusCommand's GetAndAwait-then-read ordering, and that the configured
// resolveTimeout is threaded through unchanged.
func TestActivityCommandExecute_RegistersBeforeReading(t *testing.T) {
	registry := &fakeActivityRegistry{}
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
	registry := &fakeActivityRegistry{}
	cmd := &activityCommand{registry: registry}

	code, _, errData := cmd.Execute(newActivityTestContext("flush_cache=true"))
	require.Nil(t, errData)
	require.Equal(t, http.StatusOK, code)

	require.Equal(t, []string{"FlushActivity", "RegisterAndAwait", "GetActivity"}, registry.calls)
	require.Equal(t, testActivityFromAddress, registry.lastFlushAddress)
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
