package healthcheck

import (
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/log"
)

// HealthResponse represents the JSON response structure
type HealthResponse struct {
	IsHealthy bool               `json:"is_healthy"`
	Version   aggkit.FullVersion `json:"version"`
}

// HealthCheckHandler encapsulates logic that serves the HTTP request for health checks
type HealthCheckHandler struct {
	logger    *log.Logger
	isHealthy atomic.Bool
}

var _ http.Handler = (*HealthCheckHandler)(nil)

// NewHandler creates a new healthcheck http handler
func NewHandler(logger *log.Logger) *HealthCheckHandler {
	s := &HealthCheckHandler{logger: logger}
	s.isHealthy.Store(true)

	return s
}

// HealthHandler is a health check handler
func (h *HealthCheckHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.isHealthy.Load() {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
	version := aggkit.GetVersion()
	w.Header().Set("Content-Type", "application/json")

	resp := &HealthResponse{
		IsHealthy: h.isHealthy.Load(),
		Version:   version,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Errorf("Failed to encode health check response: %v", err)

		http.Error(w, `{"error": "failed to encode health check response"}`, http.StatusInternalServerError)
	}
}

// SetHealthStatus sets isHealthy indicator atomically.
func (h *HealthCheckHandler) SetHealthStatus(isHealthy bool) {
	h.isHealthy.Store(isHealthy)
}
