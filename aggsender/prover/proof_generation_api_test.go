package prover

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newProofGenTestRouter(t *testing.T, tool AggchainProofGeneration) *gin.Engine {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewAggchainProofGenAPI(tool).RegisterAdminRoutes(router)
	return router
}

func newLimitedProofGenTestRouter(t *testing.T, tool AggchainProofGeneration, limit int64) *gin.Engine {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(aggkitcommon.RequestBodyLimitHandler(limit))
	NewAggchainProofGenAPI(tool).RegisterAdminRoutes(router)
	return router
}

// unknownLengthBody hides the reader's type so httptest leaves the request's
// Content-Length unset, like a chunked request.
type unknownLengthBody struct{ r io.Reader }

func (u unknownLengthBody) Read(p []byte) (int, error) { return u.r.Read(p) }

func performProofRequest(t *testing.T, router *gin.Engine, body []byte) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, AggchainProofGenV1Prefix+"/aggchain-proof", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestGenerateAggchainProofRoute(t *testing.T) {
	t.Parallel()

	const (
		fromBlock = uint64(1)
		toBlock   = uint64(10)
	)

	t.Run("Success", func(t *testing.T) {
		t.Parallel()

		aggchainProofGen := mocks.NewAggchainProofGeneration(t)
		router := newProofGenTestRouter(t, aggchainProofGen)
		expectedProof := &types.SP1StarkProof{Version: "v1", Proof: []byte("proof")}

		aggchainProofGen.EXPECT().GenerateAggchainProof(mock.Anything, fromBlock, toBlock).
			Return(expectedProof, nil).Once()

		body := []byte(`{"last_proven_block":1,"requested_end_block":10}`)
		rec := performProofRequest(t, router, body)
		require.Equal(t, http.StatusOK, rec.Code)

		var actual types.SP1StarkProof
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &actual))
		require.Equal(t, expectedProof.Version, actual.Version)
		require.Equal(t, expectedProof.Proof, actual.Proof)
	})

	t.Run("Invalid body", func(t *testing.T) {
		t.Parallel()

		aggchainProofGen := mocks.NewAggchainProofGeneration(t)
		router := newProofGenTestRouter(t, aggchainProofGen)

		rec := performProofRequest(t, router, []byte(`{"last_proven_block":"not-a-number"}`))
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Contains(t, rec.Body.String(), "invalid request body")
	})

	t.Run("Body over the declared limit", func(t *testing.T) {
		t.Parallel()

		const limit = 32
		aggchainProofGen := mocks.NewAggchainProofGeneration(t)
		router := newLimitedProofGenTestRouter(t, aggchainProofGen, limit)

		body := []byte(`{"last_proven_block":1,"requested_end_block":10,"padding":"` + strings.Repeat("a", 64) + `"}`)
		rec := performProofRequest(t, router, body)
		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		require.Contains(t, rec.Body.String(), "32-byte limit")
	})

	t.Run("Body of unknown length over the limit", func(t *testing.T) {
		t.Parallel()

		const limit = 32
		aggchainProofGen := mocks.NewAggchainProofGeneration(t)
		router := newLimitedProofGenTestRouter(t, aggchainProofGen, limit)

		body := []byte(`{"last_proven_block":1,"requested_end_block":10,"padding":"` + strings.Repeat("a", 64) + `"}`)
		req := httptest.NewRequest(http.MethodPost, AggchainProofGenV1Prefix+"/aggchain-proof",
			unknownLengthBody{r: bytes.NewReader(body)})
		req.Header.Set("Content-Type", "application/json")
		require.Equal(t, int64(-1), req.ContentLength)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		require.Contains(t, rec.Body.String(), "32-byte limit")
	})

	t.Run("Generator error", func(t *testing.T) {
		t.Parallel()

		aggchainProofGen := mocks.NewAggchainProofGeneration(t)
		router := newProofGenTestRouter(t, aggchainProofGen)

		aggchainProofGen.EXPECT().GenerateAggchainProof(mock.Anything, fromBlock, toBlock).
			Return(nil, errors.New("mock error")).Once()

		body := []byte(`{"last_proven_block":1,"requested_end_block":10}`)
		rec := performProofRequest(t, router, body)
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		require.Contains(t, rec.Body.String(), "mock error")
	})
}
