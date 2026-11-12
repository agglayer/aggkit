package multidownloader

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// blockingResponseWriter blocks in Write until release is closed, so a test can inspect
// what the handler holds while the response is being written.
type blockingResponseWriter struct {
	header     http.Header
	entered    chan struct{}
	release    chan struct{}
	enterOnce  sync.Once
	statusCode int
	body       bytes.Buffer
}

func newBlockingResponseWriter() *blockingResponseWriter {
	return &blockingResponseWriter{
		header:  make(http.Header),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (w *blockingResponseWriter) Header() http.Header { return w.header }

func (w *blockingResponseWriter) WriteHeader(statusCode int) { w.statusCode = statusCode }

func (w *blockingResponseWriter) Write(data []byte) (int, error) {
	w.enterOnce.Do(func() { close(w.entered) })
	<-w.release
	return w.body.Write(data)
}

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

func TestEVMMultidownloaderAPI_StatusDoesNotHoldMutexWhileWriting(t *testing.T) {
	testData := newEVMMultidownloaderTestData(t, false)
	testData.mdr.state = NewEmptyState()
	testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
		Return(uint64(100), nil)

	engine := newMultidownloaderAPITestEngine(t, testData.mdr)
	writer := newBlockingResponseWriter()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		engine.ServeHTTP(writer,
			httptest.NewRequest(http.MethodGet, MultidownloaderV1Prefix+"/test/status", nil))
	}()

	select {
	case <-writer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the status handler did not write a response")
	}

	// While the response is being written the downloader's lock must be free: its own
	// loop takes it, and a slow client must not stall the downloader.
	locked := make(chan struct{})
	go func() {
		testData.mdr.mutex.Lock()
		close(locked)
		testData.mdr.mutex.Unlock()
	}()

	select {
	case <-locked:
	case <-time.After(5 * time.Second):
		close(writer.release)
		<-requestDone
		t.Fatal("the status handler holds downloader.mutex while writing the response")
	}

	close(writer.release)
	<-requestDone

	require.Equal(t, http.StatusOK, writer.statusCode)
	require.Contains(t, writer.body.String(), `"status":"running"`)
}
