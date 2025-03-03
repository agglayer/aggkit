package healthcheck

import (
	"encoding/json"
	"net/http"

	"github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/log"
)

// HealthResponse represents the JSON response structure
type HealthResponse struct {
	IsHealthy bool               `json:"is_healthy"`
	Version   aggkit.FullVersion `json:"version_info"`
}

// HealthCheckHandler encapsulates logic that serves the HTTP request for health checks
type HealthCheckHandler struct {
	logger *log.Logger
}

var _ http.Handler = (*HealthCheckHandler)(nil)

// NewHealthCheckHandler creates a new healthcheck http handler
func NewHealthCheckHandler(logger *log.Logger) *HealthCheckHandler {
	return &HealthCheckHandler{logger: logger}
}

// HealthHandler is a health check handler
func (h *HealthCheckHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")

	resp := &HealthResponse{
		IsHealthy: true,
		Version:   aggkit.GetVersion(),
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Errorf("Failed to encode health check response: %v", err)

		http.Error(w, `{"error": "failed to encode health check response"}`, http.StatusInternalServerError)
	}
}
