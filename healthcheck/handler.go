package healthcheck

import (
	"net/http"

	"github.com/agglayer/aggkit/log"
)

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

	response := `{"is_healthy": true}`
	if _, err := w.Write([]byte(response)); err != nil {
		h.logger.Errorf("failed to write health indicator: %v", err)
	}
}
