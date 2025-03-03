package healthcheck

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

func TestHealthCheckHandler_ServeHTTP_Healthy(t *testing.T) {
	handler := NewHealthCheckHandler(log.GetDefaultLogger())

	// Create a request and record the response
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	// Set the health status to healthy
	handler.ServeHTTP(rr, req)

	// Check the response code and body
	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), `{"is_healthy": true}`)
}
