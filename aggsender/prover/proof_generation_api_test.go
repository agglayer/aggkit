package prover

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
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
