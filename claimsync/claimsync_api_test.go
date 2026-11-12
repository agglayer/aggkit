package claimsync

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	claimmocks "github.com/agglayer/aggkit/claimsync/types/mocks"
	logger "github.com/agglayer/aggkit/log"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type fakeClaimsBackend struct {
	lastBlock    uint64
	foundLast    bool
	lastErr      error
	firstBlock   uint64
	foundFirst   bool
	firstErr     error
	claims       []claimsynctypes.Claim
	claimsErr    error
	byIndex      []claimsynctypes.Claim
	byIndexErr   error
	claimsCalls  int
	lastFrom     uint64
	lastTo       uint64
	lastLimit    uint64
	byIndexCalls []*big.Int
}

func (f *fakeClaimsBackend) GetLastProcessedBlock(context.Context) (uint64, bool, error) {
	return f.lastBlock, f.foundLast, f.lastErr
}

func (f *fakeClaimsBackend) GetFirstProcessedBlock(context.Context) (uint64, bool, error) {
	return f.firstBlock, f.foundFirst, f.firstErr
}

func (f *fakeClaimsBackend) GetClaimsWithLimit(
	_ context.Context, fromBlock, toBlock, limit uint64,
) ([]claimsynctypes.Claim, error) {
	f.claimsCalls++
	f.lastFrom, f.lastTo, f.lastLimit = fromBlock, toBlock, limit
	if f.claimsErr != nil {
		return nil, f.claimsErr
	}
	if uint64(len(f.claims)) > limit {
		return f.claims[:limit], nil
	}
	return f.claims, nil
}

func (f *fakeClaimsBackend) GetClaimsByGlobalIndex(
	_ context.Context, globalIndex *big.Int,
) ([]claimsynctypes.Claim, error) {
	f.byIndexCalls = append(f.byIndexCalls, globalIndex)
	return f.byIndex, f.byIndexErr
}

func newTestAPI(t *testing.T, backend *fakeClaimsBackend, maxRange, maxClaims uint64) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewClaimSyncAPI(logger.WithFields("module", "test"), backend, "l2claimsync", maxRange, maxClaims).
		RegisterRoutes(engine)
	return engine
}

func doGet(engine *gin.Engine, url string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	engine.ServeHTTP(rec, req)
	return rec
}

func makeClaims(n int) []claimsynctypes.Claim {
	claims := make([]claimsynctypes.Claim, n)
	for i := range claims {
		claims[i] = claimsynctypes.Claim{
			BlockNum:    uint64(i + 1),
			GlobalIndex: big.NewInt(int64(i + 1)),
			Amount:      big.NewInt(1),
		}
	}
	return claims
}

func TestClaimSyncAPI_Status(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		backend := &fakeClaimsBackend{lastBlock: 42, foundLast: true, firstBlock: 10, foundFirst: true}
		rec := doGet(newTestAPI(t, backend, 0, 0), "/l2claimsync/v1/status")
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{"firstProcessedBlock":10,"lastProcessedBlock":42}`, rec.Body.String())
	})
	t.Run("nothing processed", func(t *testing.T) {
		rec := doGet(newTestAPI(t, &fakeClaimsBackend{}, 0, 0), "/l2claimsync/v1/status")
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{"lastProcessedBlock":null}`, rec.Body.String())
	})
	t.Run("last block error", func(t *testing.T) {
		backend := &fakeClaimsBackend{lastErr: errors.New("db error")}
		rec := doGet(newTestAPI(t, backend, 0, 0), "/l2claimsync/v1/status")
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		require.Contains(t, rec.Body.String(), "getting last processed block")
	})
	t.Run("first block error", func(t *testing.T) {
		backend := &fakeClaimsBackend{foundLast: true, firstErr: errors.New("db error")}
		rec := doGet(newTestAPI(t, backend, 0, 0), "/l2claimsync/v1/status")
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		require.Contains(t, rec.Body.String(), "getting first processed block")
	})
}

func TestClaimSyncAPI_GetClaims_OK(t *testing.T) {
	backend := &fakeClaimsBackend{claims: makeClaims(3)}
	rec := doGet(newTestAPI(t, backend, 100, 10), "/l2claimsync/v1/claims?from_block=1&to_block=100")
	require.Equal(t, http.StatusOK, rec.Code)

	var got []claimsynctypes.Claim
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 3)
	require.Equal(t, uint64(2), got[1].BlockNum)
	require.Equal(t, 1, backend.claimsCalls)
	require.Equal(t, uint64(1), backend.lastFrom)
	require.Equal(t, uint64(100), backend.lastTo)
}

func TestClaimSyncAPI_GetClaims_UsesDefaultLimits(t *testing.T) {
	backend := &fakeClaimsBackend{}
	engine := newTestAPI(t, backend, 0, 0)

	rec := doGet(engine, "/l2claimsync/v1/claims?from_block=0&to_block=9999")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, DefaultAPIMaxClaims+1, backend.lastLimit)

	backend.claimsCalls = 0
	rec = doGet(engine, "/l2claimsync/v1/claims?from_block=0&to_block=10000")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Zero(t, backend.claimsCalls)
}

func TestClaimSyncAPI_GetClaims_RangeTooWide(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"one block over", "/l2claimsync/v1/claims?from_block=1&to_block=101"},
		{"far over", "/l2claimsync/v1/claims?from_block=0&to_block=1000000"},
		{"full uint64 span does not wrap", "/l2claimsync/v1/claims?from_block=0&to_block=18446744073709551615"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &fakeClaimsBackend{claims: makeClaims(1)}
			rec := doGet(newTestAPI(t, backend, 100, 10), tt.url)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), "block range too wide")
			require.Zero(t, backend.claimsCalls, "storage must not be called")
		})
	}
}

func TestClaimSyncAPI_GetClaims_RangeAtLimit(t *testing.T) {
	backend := &fakeClaimsBackend{}
	rec := doGet(newTestAPI(t, backend, 100, 10), "/l2claimsync/v1/claims?from_block=1&to_block=100")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, backend.claimsCalls)
}

func TestClaimSyncAPI_GetClaims_InvalidParams(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"missing from", "/l2claimsync/v1/claims?to_block=10"},
		{"missing to", "/l2claimsync/v1/claims?from_block=10"},
		{"invalid from", "/l2claimsync/v1/claims?from_block=abc&to_block=10"},
		{"negative to", "/l2claimsync/v1/claims?from_block=1&to_block=-1"},
		{"to lower than from", "/l2claimsync/v1/claims?from_block=10&to_block=9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &fakeClaimsBackend{}
			rec := doGet(newTestAPI(t, backend, 100, 10), tt.url)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Zero(t, backend.claimsCalls)
		})
	}
}

func TestClaimSyncAPI_GetClaims_TooManyClaims(t *testing.T) {
	// the storage holds more claims than the maximum: it is asked for max+1 and the response is rejected
	backend := &fakeClaimsBackend{claims: makeClaims(50)}
	rec := doGet(newTestAPI(t, backend, 100, 10), "/l2claimsync/v1/claims?from_block=1&to_block=100")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "narrow the block range")
	require.Equal(t, 1, backend.claimsCalls)
	require.Equal(t, uint64(11), backend.lastLimit)
}

func TestClaimSyncAPI_GetClaims_ExactlyMaxClaims(t *testing.T) {
	backend := &fakeClaimsBackend{claims: makeClaims(10)}
	rec := doGet(newTestAPI(t, backend, 100, 10), "/l2claimsync/v1/claims?from_block=1&to_block=100")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(11), backend.lastLimit)

	var got []claimsynctypes.Claim
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 10)
}

func TestClaimSyncAPI_GetClaims_StorageError(t *testing.T) {
	backend := &fakeClaimsBackend{claimsErr: errors.New("storage error")}
	rec := doGet(newTestAPI(t, backend, 100, 10), "/l2claimsync/v1/claims?from_block=1&to_block=10")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, rec.Body.String(), "storage error")
}

func TestClaimSyncAPI_GetClaimsByGlobalIndex(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		backend := &fakeClaimsBackend{byIndex: makeClaims(1)}
		rec := doGet(newTestAPI(t, backend, 0, 0), "/l2claimsync/v1/claims-by-global-index?global_index=123")
		require.Equal(t, http.StatusOK, rec.Code)
		require.Len(t, backend.byIndexCalls, 1)
		require.Equal(t, "123", backend.byIndexCalls[0].String())
	})
	t.Run("not found", func(t *testing.T) {
		rec := doGet(newTestAPI(t, &fakeClaimsBackend{}, 0, 0), "/l2claimsync/v1/claims-by-global-index?global_index=123")
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Contains(t, rec.Body.String(), "no claims found")
	})
	t.Run("invalid", func(t *testing.T) {
		backend := &fakeClaimsBackend{}
		rec := doGet(newTestAPI(t, backend, 0, 0), "/l2claimsync/v1/claims-by-global-index?global_index=xyz")
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Empty(t, backend.byIndexCalls)
	})
	t.Run("missing", func(t *testing.T) {
		rec := doGet(newTestAPI(t, &fakeClaimsBackend{}, 0, 0), "/l2claimsync/v1/claims-by-global-index")
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("storage error", func(t *testing.T) {
		backend := &fakeClaimsBackend{byIndexErr: errors.New("storage error")}
		rec := doGet(newTestAPI(t, backend, 0, 0), "/l2claimsync/v1/claims-by-global-index?global_index=1")
		require.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestClaimSync_RegisterRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		syncerID claimsynctypes.ClaimSyncerID
		prefix   string
	}{
		{claimsynctypes.L1ClaimSyncer, "/l1claimsync/v1"},
		{claimsynctypes.L2ClaimSyncer, "/l2claimsync/v1"},
	} {
		engine := gin.New()
		cs := &ClaimSync{syncerID: tt.syncerID}
		cs.RegisterRoutes(engine)
		paths := map[string]bool{}
		for _, r := range engine.Routes() {
			paths[r.Method+" "+r.Path] = true
		}
		require.True(t, paths["GET "+tt.prefix+"/status"])
		require.True(t, paths["GET "+tt.prefix+"/claims"])
		require.True(t, paths["GET "+tt.prefix+"/claims-by-global-index"])
		require.Len(t, paths, 3)
	}
}

func TestClaimSync_GetClaimsWithLimit_DelegatesToReader(t *testing.T) {
	reader := claimmocks.NewClaimsReader(t)
	expected := makeClaims(2)
	reader.EXPECT().GetClaimsWithLimit(context.Background(), nil, uint64(5), uint64(50), uint64(7)).
		Return(expected, nil)

	cs := &ClaimSync{reader: reader}
	got, err := cs.GetClaimsWithLimit(context.Background(), 5, 50, 7)
	require.NoError(t, err)
	require.Equal(t, expected, got)
}
