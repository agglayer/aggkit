package l1infotreesync

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var testHash = common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")

func newL1InfoTreeSyncAPITestEngine(t *testing.T) (*L1InfoTreeSyncerMock, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mockSyncer := NewL1InfoTreeSyncerMock(t)
	api := NewL1InfoTreeSyncAPI(log.WithFields("module", "test"), mockSyncer)
	engine := gin.New()
	api.RegisterRoutes(engine)
	return mockSyncer, engine
}

func doAPIRequest(engine *gin.Engine, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func TestL1InfoTreeSyncAPI_Status(t *testing.T) {
	mockSyncer, engine := newL1InfoTreeSyncAPITestEngine(t)

	mockSyncer.EXPECT().GetCompletionPercentage().Return(nil).Once()
	rec := doAPIRequest(engine, http.MethodGet, L1InfoTreeSyncV1Prefix+"/status")
	require.Equal(t, http.StatusOK, rec.Code)

	var statusInfo StatusInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &statusInfo))
	assert.Equal(t, "running", statusInfo.Status)
	require.Nil(t, statusInfo.CompletionPercentage)

	percent := 20.0
	mockSyncer.EXPECT().GetCompletionPercentage().Return(&percent).Once()
	rec = doAPIRequest(engine, http.MethodGet, L1InfoTreeSyncV1Prefix+"/status")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &statusInfo))
	require.NotNil(t, statusInfo.CompletionPercentage)
	assert.Equal(t, percent, *statusInfo.CompletionPercentage)
}

// testInfoByParam covers the subtests shared by the info-by-global-exit-root and
// the info-by-root routes: no parameter, the parameter in the query, malformed
// values, an unknown value and a storage error.
func testInfoByParam(
	t *testing.T,
	path, paramName string,
	expectLatest func(*L1InfoTreeSyncerMock),
	expectByParam func(*L1InfoTreeSyncerMock),
	expectUnknown func(*L1InfoTreeSyncerMock),
	expectError func(*L1InfoTreeSyncerMock),
) {
	t.Helper()
	target := L1InfoTreeSyncV1Prefix + "/" + path

	t.Run("no parameter returns the latest leaf", func(t *testing.T) {
		mockSyncer, engine := newL1InfoTreeSyncAPITestEngine(t)
		expectLatest(mockSyncer)

		rec := doAPIRequest(engine, http.MethodGet, target)
		require.Equal(t, http.StatusOK, rec.Code)

		var leaf L1InfoTreeLeaf
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &leaf))
		assert.Equal(t, testHash, leaf.Hash)
	})

	t.Run("parameter in the query returns its leaf", func(t *testing.T) {
		mockSyncer, engine := newL1InfoTreeSyncAPITestEngine(t)
		expectByParam(mockSyncer)

		rec := doAPIRequest(engine, http.MethodGet, target+"?"+paramName+"="+testHash.Hex())
		require.Equal(t, http.StatusOK, rec.Code)

		var leaf L1InfoTreeLeaf
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &leaf))
		assert.Equal(t, testHash, leaf.Hash)
	})

	for _, malformed := range []string{"0x1234", "", "not-a-hash", testHash.Hex() + "00"} {
		t.Run(fmt.Sprintf("malformed %s %q is a 400", paramName, malformed), func(t *testing.T) {
			_, engine := newL1InfoTreeSyncAPITestEngine(t)

			rec := doAPIRequest(engine, http.MethodGet, target+"?"+paramName+"="+malformed)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), paramName)
		})
	}

	t.Run("unknown value is a 404", func(t *testing.T) {
		mockSyncer, engine := newL1InfoTreeSyncAPITestEngine(t)
		expectUnknown(mockSyncer)

		rec := doAPIRequest(engine, http.MethodGet, target+"?"+paramName+"="+testHash.Hex())
		require.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Body.String(), "leaf not found")
	})

	t.Run("storage error is a 500", func(t *testing.T) {
		mockSyncer, engine := newL1InfoTreeSyncAPITestEngine(t)
		expectError(mockSyncer)

		rec := doAPIRequest(engine, http.MethodGet, target+"?"+paramName+"="+testHash.Hex())
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "storage failure")
	})
}

func TestL1InfoTreeSyncAPI_InfoByGlobalExitRoot(t *testing.T) {
	testInfoByParam(t, "info-by-global-exit-root", "global_exit_root",
		func(m *L1InfoTreeSyncerMock) {
			m.EXPECT().GetLatestL1InfoLeaf(mock.Anything).
				Return(&L1InfoTreeLeaf{Hash: testHash}, nil).Once()
		},
		func(m *L1InfoTreeSyncerMock) {
			m.EXPECT().GetInfoByGlobalExitRoot(testHash).
				Return(&L1InfoTreeLeaf{Hash: testHash}, nil).Once()
		},
		func(m *L1InfoTreeSyncerMock) {
			m.EXPECT().GetInfoByGlobalExitRoot(testHash).Return(nil, nil).Once()
		},
		func(m *L1InfoTreeSyncerMock) {
			m.EXPECT().GetInfoByGlobalExitRoot(testHash).
				Return(nil, fmt.Errorf("storage failure")).Once()
		},
	)
}

func TestL1InfoTreeSyncAPI_InfoByRoot(t *testing.T) {
	testInfoByParam(t, "info-by-root", "root",
		func(m *L1InfoTreeSyncerMock) {
			m.EXPECT().GetLatestL1InfoLeaf(mock.Anything).
				Return(&L1InfoTreeLeaf{Hash: testHash}, nil).Once()
		},
		func(m *L1InfoTreeSyncerMock) {
			m.EXPECT().GetInfoByRoot(testHash).
				Return(&L1InfoTreeLeaf{Hash: testHash}, nil).Once()
		},
		func(m *L1InfoTreeSyncerMock) {
			m.EXPECT().GetInfoByRoot(testHash).Return(nil, nil).Once()
		},
		func(m *L1InfoTreeSyncerMock) {
			m.EXPECT().GetInfoByRoot(testHash).
				Return(nil, fmt.Errorf("storage failure")).Once()
		},
	)
}
