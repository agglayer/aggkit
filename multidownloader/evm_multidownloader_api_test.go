package multidownloader

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agglayer/aggkit/log"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newMultidownloaderAPITestEngine(t *testing.T, downloader *EVMMultidownloader) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	api := NewEVMMultidownloaderAPI(log.WithFields("module", "test"), downloader)
	engine := gin.New()
	api.RegisterPublicRoutes(engine)
	api.RegisterAdminRoutes(engine)
	return engine
}

func doMultidownloaderAPIRequest(engine *gin.Engine, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func TestEVMMultidownloaderAPI_Status(t *testing.T) {
	t.Run("returns the multidownloader status", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		testData.mdr.state = NewEmptyState()
		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(100), nil)

		engine := newMultidownloaderAPITestEngine(t, testData.mdr)
		rec := doMultidownloaderAPIRequest(engine, http.MethodGet,
			MultidownloaderV1Prefix+"/test/status")
		require.Equal(t, http.StatusOK, rec.Code)

		var status map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))
		require.Equal(t, "running", status["status"])
		require.Equal(t, float64(100), status["finalizedBlockNumber"])
		require.Equal(t, float64(100), status["latestBlockNumber"])
	})

	t.Run("not initialized is a 500", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)

		engine := newMultidownloaderAPITestEngine(t, testData.mdr)
		rec := doMultidownloaderAPIRequest(engine, http.MethodGet,
			MultidownloaderV1Prefix+"/test/status")
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		require.Contains(t, rec.Body.String(), "multidownloader not initialized")
	})

	t.Run("the route carries the downloader name", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)

		engine := newMultidownloaderAPITestEngine(t, testData.mdr)
		rec := doMultidownloaderAPIRequest(engine, http.MethodGet,
			MultidownloaderV1Prefix+"/other/status")
		require.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestEVMMultidownloaderAPI_Reorg(t *testing.T) {
	t.Run("route is absent without developer mode", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		require.Nil(t, testData.mdr.debug)

		engine := newMultidownloaderAPITestEngine(t, testData.mdr)
		rec := doMultidownloaderAPIRequest(engine, http.MethodPost,
			MultidownloaderV1Prefix+"/test/reorg?block=123")
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("without developer mode only the public routes are registered", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		engine := gin.New()
		api := NewEVMMultidownloaderAPI(log.WithFields("module", "test"), testData.mdr)
		api.RegisterPublicRoutes(engine)
		api.RegisterAdminRoutes(engine)

		registered := make(map[string]bool, len(engine.Routes()))
		for _, route := range engine.Routes() {
			registered[route.Method+" "+route.Path] = true
		}
		require.True(t, registered[http.MethodGet+" "+MultidownloaderV1Prefix+"/test/status"])
		require.False(t, registered[http.MethodPost+" "+MultidownloaderV1Prefix+"/test/reorg"])
	})

	t.Run("developer mode forces the reorg", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		testData.mdr.debug = NewEVMMultidownloaderDebug()

		engine := newMultidownloaderAPITestEngine(t, testData.mdr)
		rec := doMultidownloaderAPIRequest(engine, http.MethodPost,
			MultidownloaderV1Prefix+"/test/reorg?block=123")
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), "Reorg forced successfully")

		err := testData.mdr.debug.GetInjectedStartStepError()
		require.Error(t, err)
		require.Contains(t, err.Error(), "forced reorg at block number 123")
	})

	t.Run("a missing or malformed block is a 400", func(t *testing.T) {
		for _, target := range []string{
			MultidownloaderV1Prefix + "/test/reorg",
			MultidownloaderV1Prefix + "/test/reorg?block=",
			MultidownloaderV1Prefix + "/test/reorg?block=not-a-number",
		} {
			testData := newEVMMultidownloaderTestData(t, false)
			testData.mdr.debug = NewEVMMultidownloaderDebug()

			engine := newMultidownloaderAPITestEngine(t, testData.mdr)
			rec := doMultidownloaderAPIRequest(engine, http.MethodPost, target)
			require.Equal(t, http.StatusBadRequest, rec.Code, target)
			require.Contains(t, rec.Body.String(), "block")

			require.NoError(t, testData.mdr.debug.GetInjectedStartStepError(),
				"no reorg must be forced on invalid input")
		}
	})
}
