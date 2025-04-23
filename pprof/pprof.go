package pprof

import (
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/prometheus"
)

// StartProfilingHTTPServer starts an HTTP server for profiling using the provided configuration.
// It sets up endpoints for pprof profiling and listens on the specified host and port.
// The server includes handlers for various pprof endpoints such as index, profile, cmdline,
// symbol, and trace. The server's read and header timeouts are set to two minutes.
//
// Parameters:
//   - c (Config): The configuration object containing the profiling host and port.
//
// Behavior:
//   - Logs an error and returns if the TCP listener cannot be created.
//   - Logs the port on which the profiling server is listening.
//   - Handles server closure gracefully, logging a warning if the server is stopped
//     or an error if the connection is closed unexpectedly.
func StartProfilingHTTPServer(c Config) {
	const two = 2
	mux := http.NewServeMux()
	address := fmt.Sprintf("%s:%d", c.ProfilingHost, c.ProfilingPort)
	lis, err := net.Listen("tcp", address)
	if err != nil {
		log.Errorf("failed to create tcp listener for profiling: %v", err)
		return
	}
	mux.HandleFunc(prometheus.ProfilingIndexEndpoint, pprof.Index)
	mux.HandleFunc(prometheus.ProfileEndpoint, pprof.Profile)
	mux.HandleFunc(prometheus.ProfilingCmdEndpoint, pprof.Cmdline)
	mux.HandleFunc(prometheus.ProfilingSymbolEndpoint, pprof.Symbol)
	mux.HandleFunc(prometheus.ProfilingTraceEndpoint, pprof.Trace)
	profilingServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: two * time.Minute,
		ReadTimeout:       two * time.Minute,
	}
	log.Infof("profiling server listening on port %d", c.ProfilingPort)
	if err := profilingServer.Serve(lis); err != nil {
		if err == http.ErrServerClosed {
			log.Warnf("http server for profiling stopped")
			return
		}
		log.Errorf("closed http connection for profiling server: %v", err)
		return
	}
}
