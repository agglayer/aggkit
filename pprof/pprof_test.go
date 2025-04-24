package pprof

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/agglayer/aggkit/prometheus"
	"github.com/stretchr/testify/require"
)

func TestStartProfilingHttpServer(t *testing.T) {
	// Mock configuration
	config := Config{
		ProfilingHost: "127.0.0.1",
		ProfilingPort: 6060,
	}

	context, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the profiling server in a separate goroutine
	go StartProfilingHTTPServer(context, Config{
		ProfilingHost: config.ProfilingHost,
		ProfilingPort: config.ProfilingPort,
	})

	// Allow some time for the server to start
	time.Sleep(1 * time.Second)

	// Test if the server is listening
	address := net.JoinHostPort(config.ProfilingHost, fmt.Sprintf("%d", config.ProfilingPort))
	conn, err := net.Dial("tcp", address)
	require.NoError(t, err, "failed to connect to profiling server")
	conn.Close()

	// Test if the endpoints are accessible
	endpoints := []string{
		prometheus.ProfilingIndexEndpoint,
		prometheus.ProfileEndpoint,
		prometheus.ProfilingCmdEndpoint,
		prometheus.ProfilingSymbolEndpoint,
		prometheus.ProfilingTraceEndpoint,
	}

	for _, endpoint := range endpoints {
		resp, err := http.Get("http://" + address + endpoint)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "unexpected status code for endpoint %s: got %d, want %d", endpoint, resp.StatusCode, http.StatusOK)
		resp.Body.Close()
	}
}
