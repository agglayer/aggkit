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
	require.Contains(t, rr.Body.String(), `"is_healthy":true`)
}

func TestHealthCheckHandler_ServeHTTP_Unhealthy(t *testing.T) {
	handler := NewHealthCheckHandler(log.GetDefaultLogger())

	// Set the health status to unhealthy
	handler.SetHealthStatus(false)

	// Create a request and record the response
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Check the response code and body
	require.Equal(t, http.StatusInternalServerError, rr.Code)
	require.Contains(t, rr.Body.String(), `"is_healthy":false`)
}

func TestHealthCheckHandler_SetHealthStatus(t *testing.T) {
	handler := NewHealthCheckHandler(log.GetDefaultLogger())

	// Check the initial health status
	require.True(t, handler.isHealthy.Load())

	// Set the health status to false
	handler.SetHealthStatus(false)
	require.False(t, handler.isHealthy.Load())

	// Set the health status to true
	handler.SetHealthStatus(true)
	require.True(t, handler.isHealthy.Load())
}

func TestNewHealthCheckHandler(t *testing.T) {
	handler := NewHealthCheckHandler(log.GetDefaultLogger())

	// Assert that the handler is not nil and the initial health status is true
	require.NotNil(t, handler)
	require.True(t, handler.isHealthy.Load())
}
