package claimer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHealthCheckURL(t *testing.T) {
	t.Parallel()

	require.Equal(t, "http://127.0.0.1:8080/claimer/v1/health", HealthCheckURL("127.0.0.1", 8080))
	// IPv6 hosts get bracketed by net.JoinHostPort.
	require.Equal(t, "http://[::1]:9000/claimer/v1/health", HealthCheckURL("::1", 9000))
}

func TestHealthCheckOK(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, apiBasePath+"/health", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := HealthCheck(context.Background(), srv.URL+apiBasePath+"/health", time.Second)
	require.NoError(t, err)
}

func TestHealthCheckNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	err := HealthCheck(context.Background(), srv.URL, time.Second)
	require.ErrorContains(t, err, "unexpected status")
}

func TestHealthCheckUnreachable(t *testing.T) {
	t.Parallel()

	// Nothing listens here: httptest server closed before probing.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	err := HealthCheck(context.Background(), url, time.Second)
	require.Error(t, err)
}

func TestHealthCheckInvalidURL(t *testing.T) {
	t.Parallel()

	err := HealthCheck(context.Background(), "http://\x00invalid", time.Second)
	require.ErrorContains(t, err, "building health request")
}

// TestHealthCheckAgainstServer probes a real claimer Server end to end.
func TestHealthCheckAgainstServer(t *testing.T) {
	t.Parallel()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)
	claimer, _ := buildTestClaimer(t, cert.NewLocalExitRoot)
	cfg := &Config{Address: "127.0.0.1", Port: 0, ReadTimeoutSeconds: 1, WriteTimeoutSeconds: 1}
	srv := NewServer(cfg, claimer, claimer.logger)

	ts := httptest.NewServer(srv.router)
	defer ts.Close()

	err = HealthCheck(context.Background(), ts.URL+apiBasePath+"/health", time.Second)
	require.NoError(t, err)
}
