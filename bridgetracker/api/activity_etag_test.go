package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const (
	etagTestRevision      = "v1:00000000000000000000000000000000000000000000000000000000000000ab"
	etagTestOtherRevision = "v1:00000000000000000000000000000000000000000000000000000000000000cd"
	etagTestETag          = `"` + etagTestRevision + `"`
)

// newETagTestRegistry returns a ready registry holding one bridge whose revision is revision
func newETagTestRegistry(revision string) *fakeRevisionActivityRegistry {
	registry := &fakeRevisionActivityRegistry{fakeActivityRegistry: newFakeActivityRegistry(), revision: revision}
	registry.getActivityEntries = []*domain.ActivityEntry{activityEntryAt("1", time.Now())}
	return registry
}

// executeActivity runs cmd for GET /activity/from/{from_address}?rawQuery with the given
// If-None-Match header lines (none: the header is not sent) and returns Execute's result plus
// the response headers it set
func executeActivity(
	cmd *activityCommand, rawQuery string, ifNoneMatch ...string,
) (code int, obj any, errCode int, header http.Header) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/?"+rawQuery, nil)
	for _, value := range ifNoneMatch {
		c.Request.Header.Add(ifNoneMatchHeader, value)
	}
	c.Params = gin.Params{{Key: fromAddressParam, Value: testActivityFromAddress.Hex()}}

	code, obj, errData := cmd.Execute(c)
	if errData != nil {
		errCode = errData.Code
	}
	return code, obj, errCode, recorder.Header()
}

// TestActivityCommandExecute_ETagIsTheRevision verifies a request with no If-None-Match is
// answered as before, plus the revision of its body as a strong ETag and Cache-Control: no-store
func TestActivityCommandExecute_ETagIsTheRevision(t *testing.T) {
	registry := newETagTestRegistry(etagTestRevision)
	cmd := &activityCommand{registry: registry}

	code, obj, errCode, header := executeActivity(cmd, "")
	require.Zero(t, errCode)
	require.Equal(t, http.StatusOK, code)
	body, ok := obj.(ActivityResponse)
	require.True(t, ok)
	require.Equal(t, etagTestRevision, body.Revision)
	require.Len(t, body.Bridges, 1)
	require.Equal(t, `"`+body.Revision+`"`, header.Get(etagHeader), "the ETag is the body's revision, quoted")
	require.Equal(t, cacheControlNoStore, header.Get(cacheControlHeader))
	require.Equal(t, []string{"RegisterAndAwait", "GetActivityRevision", "GetActivity"}, registry.calls)
}

// TestActivityCommandExecute_IfNoneMatch verifies which If-None-Match values are answered 304
// (no body, the entries not read) and which get the full 200
func TestActivityCommandExecute_IfNoneMatch(t *testing.T) {
	otherETag := `"` + etagTestOtherRevision + `"`
	tests := map[string]struct {
		ifNoneMatch []string
		notModified bool
	}{
		"the current ETag":                     {ifNoneMatch: []string{etagTestETag}, notModified: true},
		"the current ETag as a weak tag":       {ifNoneMatch: []string{"W/" + etagTestETag}, notModified: true},
		"any (*)":                              {ifNoneMatch: []string{"*"}, notModified: true},
		"any (*) with spaces around":           {ifNoneMatch: []string{" * "}, notModified: true},
		"a * inside a quoted tag with commas":  {ifNoneMatch: []string{`"other,*,revision"`}},
		"a * inside a list of tags":            {ifNoneMatch: []string{otherETag + ", *"}},
		"the revision inside another tag":      {ifNoneMatch: []string{`"other,` + etagTestRevision + `"`}},
		"a list holding the current ETag":      {ifNoneMatch: []string{otherETag + ` , ` + etagTestETag}, notModified: true},
		"several header lines, one current":    {ifNoneMatch: []string{otherETag, etagTestETag}, notModified: true},
		"another revision":                     {ifNoneMatch: []string{otherETag}},
		"a list without the current ETag":      {ifNoneMatch: []string{otherETag + `, "x"`}},
		"the revision without quotes":          {ifNoneMatch: []string{etagTestRevision}},
		"an empty header":                      {ifNoneMatch: []string{""}},
		"the current ETag with something else": {ifNoneMatch: []string{etagTestETag + "x"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			registry := newETagTestRegistry(etagTestRevision)
			cmd := &activityCommand{registry: registry}

			code, obj, errCode, header := executeActivity(cmd, "", tt.ifNoneMatch...)
			require.Zero(t, errCode)
			require.Equal(t, etagTestETag, header.Get(etagHeader), "the ETag is sent with a 304 too")
			require.Equal(t, cacheControlNoStore, header.Get(cacheControlHeader))
			if tt.notModified {
				require.Equal(t, http.StatusNotModified, code)
				require.Nil(t, obj, "a 304 has no body")
				require.Equal(t, []string{"RegisterAndAwait", "GetActivityRevision"}, registry.calls,
					"the entries are not read for a 304")
				return
			}
			require.Equal(t, http.StatusOK, code)
			body, ok := obj.(ActivityResponse)
			require.True(t, ok)
			require.Equal(t, etagTestRevision, body.Revision)
			require.Equal(t, []string{"RegisterAndAwait", "GetActivityRevision", "GetActivity"}, registry.calls)
		})
	}
}

// TestActivityCommandExecute_ETagIndependentOfFilterAndPage verifies every filter and page of
// an address carries the same ETag — the revision covers all its bridges — including a page past
// the end and a filter matching nothing, and that the ETag of one of them is a 304 on any other
func TestActivityCommandExecute_ETagIndependentOfFilterAndPage(t *testing.T) {
	registry := newETagTestRegistry(etagTestRevision)
	cmd := &activityCommand{registry: registry}

	queries := []string{
		"", "page_size=1", "page_number=2&page_size=1", "page_number=99",
		"filterBridges=claimed", "filterBridges=pending&page_size=200", "filterBridges=error&page_number=3",
		"includeTracking=true",
	}
	for _, rawQuery := range queries {
		code, obj, errCode, header := executeActivity(cmd, rawQuery)
		require.Zero(t, errCode, rawQuery)
		require.Equal(t, http.StatusOK, code, rawQuery)
		body, ok := obj.(ActivityResponse)
		require.True(t, ok)
		require.Equal(t, etagTestRevision, body.Revision, rawQuery)
		require.Equal(t, etagTestETag, header.Get(etagHeader), rawQuery)

		code, obj, errCode, header = executeActivity(cmd, rawQuery, etagTestETag)
		require.Zero(t, errCode, rawQuery)
		require.Equal(t, http.StatusNotModified, code, rawQuery)
		require.Nil(t, obj, rawQuery)
		require.Equal(t, etagTestETag, header.Get(etagHeader), rawQuery)
	}
}

// TestActivityCommandExecute_IfNoneMatchAfterTheRevisionChanged verifies the ETag a client holds
// stops matching once the revision changes: it gets the full response and the new ETag
func TestActivityCommandExecute_IfNoneMatchAfterTheRevisionChanged(t *testing.T) {
	registry := newETagTestRegistry(etagTestRevision)
	cmd := &activityCommand{registry: registry}

	code, obj, errCode, header := executeActivity(cmd, "page_size=1", etagTestETag)
	require.Zero(t, errCode)
	require.Equal(t, http.StatusNotModified, code)
	require.Nil(t, obj)
	require.Equal(t, etagTestETag, header.Get(etagHeader))

	registry.revision = etagTestOtherRevision
	code, obj, errCode, header = executeActivity(cmd, "page_size=1", etagTestETag)
	require.Zero(t, errCode)
	require.Equal(t, http.StatusOK, code)
	body, ok := obj.(ActivityResponse)
	require.True(t, ok)
	require.Equal(t, etagTestOtherRevision, body.Revision)
	require.Equal(t, `"`+etagTestOtherRevision+`"`, header.Get(etagHeader))
}

// TestActivityCommandExecute_IfNoneMatchKeepsRequestSideEffects verifies a 304 skips nothing but
// the read of the entries: flush_cache still flushes first and includeTracking still reaches
// RegisterAndAwait, which is what marks the address as wanting tracking
func TestActivityCommandExecute_IfNoneMatchKeepsRequestSideEffects(t *testing.T) {
	registry := newETagTestRegistry(etagTestRevision)
	cmd := &activityCommand{registry: registry, resolveTimeout: 5 * time.Second}

	code, obj, errCode, _ := executeActivity(cmd, "includeTracking=true&flush_cache=true", etagTestETag)
	require.Zero(t, errCode)
	require.Equal(t, http.StatusNotModified, code)
	require.Nil(t, obj)
	require.Equal(t, []string{"FlushActivity", "RegisterAndAwait", "GetActivityRevision"}, registry.calls)
	require.Equal(t, testActivityFromAddress, registry.lastFlushAddress)
	require.True(t, registry.lastRegisterIncludeTracking)
	require.Equal(t, 5*time.Second, registry.lastRegisterTimeout)
}

// TestActivityCommandExecute_IfNoneMatchNeverHidesAnError verifies If-None-Match (even "*") only
// applies where a 200 would be answered: every error keeps its status and carries no ETag
func TestActivityCommandExecute_IfNoneMatchNeverHidesAnError(t *testing.T) {
	tests := map[string]struct {
		rawQuery string
		prepare  func(registry *fakeRevisionActivityRegistry)
		code     int
		calls    []string
	}{
		"invalid filter": {rawQuery: "filterBridges=typo", code: http.StatusBadRequest},
		"invalid page":   {rawQuery: "page_size=0", code: http.StatusBadRequest},
		"address not ready yet": {
			prepare: func(registry *fakeRevisionActivityRegistry) { registry.registerAndAwaitReady = false },
			code:    http.StatusServiceUnavailable, calls: []string{"RegisterAndAwait"},
		},
		"registry full": {
			prepare: func(registry *fakeRevisionActivityRegistry) {
				registry.registerAndAwaitErr = domain.ErrActivityRegistryFull
			},
			code: http.StatusServiceUnavailable, calls: []string{"RegisterAndAwait"},
		},
		"reading the revision fails": {
			prepare: func(registry *fakeRevisionActivityRegistry) { registry.revisionErr = errBoom },
			code:    http.StatusInternalServerError, calls: []string{"RegisterAndAwait", "GetActivityRevision"},
		},
	}
	for name, tt := range tests {
		for _, ifNoneMatch := range []string{etagTestETag, "*"} {
			t.Run(name+"/"+ifNoneMatch, func(t *testing.T) {
				registry := newETagTestRegistry(etagTestRevision)
				if tt.prepare != nil {
					tt.prepare(registry)
				}
				cmd := &activityCommand{registry: registry, pollInterval: 10 * time.Second}

				code, obj, errCode, header := executeActivity(cmd, tt.rawQuery, ifNoneMatch)
				require.Equal(t, tt.code, errCode)
				require.Zero(t, code)
				require.Nil(t, obj)
				require.Empty(t, header.Values(etagHeader))
				require.Empty(t, header.Values(cacheControlHeader))
				require.Equal(t, tt.calls, registry.calls)
			})
		}
	}
}

// TestActivityCommandExecute_EntriesErrorCarriesNoETag verifies a request that is not answered 304
// still fails with 500 when the entries cannot be read, and that this error carries no ETag
func TestActivityCommandExecute_EntriesErrorCarriesNoETag(t *testing.T) {
	registry := newETagTestRegistry(etagTestRevision)
	registry.getActivityErr = errBoom
	cmd := &activityCommand{registry: registry}

	for _, ifNoneMatch := range [][]string{nil, {`"` + etagTestOtherRevision + `"`}} {
		code, obj, errCode, header := executeActivity(cmd, "", ifNoneMatch...)
		require.Equal(t, http.StatusInternalServerError, errCode)
		require.Zero(t, code)
		require.Nil(t, obj)
		require.Empty(t, header.Values(etagHeader), "no ETag for a body that was never sent")
		require.Empty(t, header.Values(cacheControlHeader))
	}
}

// TestActivityCommandExecute_NoETagWithoutRevisionReader verifies a registry that keeps no
// revision offers no validator: its body carries the constant empty revision, which says nothing
// about the activity, so no ETag is sent and If-None-Match is never answered 304
func TestActivityCommandExecute_NoETagWithoutRevisionReader(t *testing.T) {
	for _, ifNoneMatch := range []string{`"` + domain.EmptyActivityRevision + `"`, "*"} {
		registry := newFakeActivityRegistry()
		cmd := &activityCommand{registry: registry}

		code, obj, errCode, header := executeActivity(cmd, "", ifNoneMatch)
		require.Zero(t, errCode)
		require.Equal(t, http.StatusOK, code)
		body, ok := obj.(ActivityResponse)
		require.True(t, ok)
		require.Equal(t, domain.EmptyActivityRevision, body.Revision)
		require.Empty(t, header.Values(etagHeader))
		require.Empty(t, header.Values(cacheControlHeader))
		require.Equal(t, []string{"RegisterAndAwait", "GetActivity"}, registry.calls)
	}
}

// TestActivityRouteNotModifiedOnTheWire runs the flow a client follows through the real router:
// a first request, then a conditional one with the ETag received. The 304 has the ETag and an
// empty body with no Content-Type; an address with no bridges has the empty revision as its ETag
func TestActivityRouteNotModifiedOnTheWire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := newETagTestRegistry(etagTestRevision)
	router := gin.New()
	NewAPI(nil, "sha1", nil, registry, nil, 0, 0, 0, aggkitcommon.CORSConfig{}, nil).RegisterRoutes(router)
	target := TrackerV1Prefix + "/activity/from/" + testActivityFromAddress.Hex() + "?page_size=1"
	get := func(ifNoneMatch string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		if ifNoneMatch != "" {
			request.Header.Set(ifNoneMatchHeader, ifNoneMatch)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	first := get("")
	require.Equal(t, http.StatusOK, first.Code)
	var body ActivityResponse
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &body))
	etag := first.Header().Get(etagHeader)
	require.Equal(t, `"`+body.Revision+`"`, etag)
	require.Equal(t, cacheControlNoStore, first.Header().Get(cacheControlHeader))

	unchanged := get(etag)
	require.Equal(t, http.StatusNotModified, unchanged.Code)
	require.Zero(t, unchanged.Body.Len(), "a 304 has no body")
	require.Empty(t, unchanged.Header().Get("Content-Type"))
	require.Equal(t, etag, unchanged.Header().Get(etagHeader))
	require.Equal(t, cacheControlNoStore, unchanged.Header().Get(cacheControlHeader))

	registry.revision = domain.EmptyActivityRevision
	registry.getActivityEntries = nil
	changed := get(etag)
	require.Equal(t, http.StatusOK, changed.Code)
	require.NoError(t, json.Unmarshal(changed.Body.Bytes(), &body))
	require.Equal(t, domain.EmptyActivityRevision, body.Revision)
	require.Equal(t, `"`+domain.EmptyActivityRevision+`"`, changed.Header().Get(etagHeader))
}
