package bridgeservicefinder

import (
	"context"
	"net/http"
	"strings"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
)

// httpHealthChecker is the default HealthChecker. It issues an HTTP GET against
// baseURL + healthPath and reports healthy iff the response status is in the 2xx range. The
// underlying http.Client and its per-request timeout are injectable so tests can stub behaviour.
type httpHealthChecker struct {
	client     *http.Client
	healthPath string
	timeout    time.Duration
	logger     aggkitcommon.Logger
}

// newHTTPHealthChecker builds the default HealthChecker. If client is nil a client with the given
// timeout is created. healthPath is the path appended to each probed base URL (e.g. "/health").
func newHTTPHealthChecker(
	client *http.Client, healthPath string, timeout time.Duration, logger aggkitcommon.Logger,
) *httpHealthChecker {
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	return &httpHealthChecker{
		client:     client,
		healthPath: healthPath,
		timeout:    timeout,
		logger:     logger,
	}
}

// IsHealthy reports whether the bridge service reachable at baseURL is healthy. It never blocks
// longer than its configured timeout and returns false (rather than an error) for any failure so
// callers can use the boolean directly in the health-gating rule.
func (h *httpHealthChecker) IsHealthy(ctx context.Context, baseURL string) bool {
	if baseURL == "" {
		return false
	}

	reqCtx := ctx
	if h.timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, h.timeout)
		defer cancel()
	}

	target := strings.TrimRight(baseURL, "/") + h.healthPath

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, http.NoBody)
	if err != nil {
		h.logger.Debugf("failed to build health check request for %s: %v", target, err)
		return false
	}

	resp, err := h.client.Do(req)
	if err != nil {
		h.logger.Debugf("health check request to %s failed: %v", target, err)
		return false
	}
	defer resp.Body.Close()

	healthy := resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
	if !healthy {
		h.logger.Debugf("health check to %s returned non-2xx status %d", target, resp.StatusCode)
	}

	return healthy
}
