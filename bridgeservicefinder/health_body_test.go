package bridgeservicefinder

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

// TestHTTPHealthChecker_IgnoresResponseBody checks that the default health checker only looks at the
// status code, so new fields in the bridge service health response (e.g. api_revision) can't break it,
// and neither does an older instance that doesn't return them.
func TestHTTPHealthChecker_IgnoresResponseBody(t *testing.T) {
	testCases := []struct {
		name string
		body string
	}{
		{
			name: "instance with api_revision",
			body: `{"status":"ok","time":"2025-06-05T07:30:00Z","version":"v0.11.0","api_revision":1,` +
				`"sync_status":"done","details":{}}`,
		},
		{
			name: "instance without api_revision",
			body: `{"status":"ok","time":"2025-06-05T07:30:00Z","version":"v0.4.0","sync_status":"done","details":{}}`,
		},
		{
			name: "unknown future fields",
			body: `{"status":"ok","api_revision":99,"something_new":{"a":1}}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/health", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			checker := newHTTPHealthChecker(nil, "/health", time.Second, log.WithFields("test", "health"))
			require.True(t, checker.IsHealthy(t.Context(), server.URL))
		})
	}
}
