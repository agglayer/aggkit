package metrics

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const serverTimeout = 10 * time.Second

// StartPrometheusHTTPServer serves the Prometheus metrics endpoint on c.Host:c.Port. It blocks
// until the server stops, so callers normally run it in its own goroutine
func StartPrometheusHTTPServer(c prometheus.Config) {
	mux := http.NewServeMux()
	address := fmt.Sprintf("%s:%d", c.Host, c.Port)
	lis, err := net.Listen("tcp", address)
	if err != nil {
		log.Errorf("failed to create tcp listener for metrics: %v", err)
		return
	}
	mux.Handle(prometheus.Endpoint, promhttp.Handler())

	metricsServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: serverTimeout,
		ReadTimeout:       serverTimeout,
	}
	log.Infof("prometheus server listening on port %d", c.Port)
	if err := metricsServer.Serve(lis); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			log.Warnf("prometheus http server stopped")
			return
		}
		log.Errorf("closed http connection for prometheus server: %v", err)
		return
	}
}
