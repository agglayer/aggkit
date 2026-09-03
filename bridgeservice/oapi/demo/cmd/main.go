// Command demo serves the current bridge service and the spec-first generated
// server side by side over one set of canned bridge rows.
//
//	go run ./bridgeservice/oapi/demo/cmd
//	curl -s 'http://127.0.0.1:8099/bridge/v1/bridges?network_id=0'
//	curl -s 'http://127.0.0.1:8099/specfirst/bridge/v1/bridges?network_id=0'
//
// The first response carries global_index as a bare JSON number above 2^53; the
// second carries it as a quoted decimal string. Same rows, same computation,
// different wire format.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/oapi/demo"
)

const (
	defaultPort       = "8099"
	serverReadTimeout = 15 * time.Second
)

func main() {
	port := flag.String("port", envOr("PORT", defaultPort), "port to listen on (127.0.0.1 only)")
	flag.Parse()

	addr := fmt.Sprintf("127.0.0.1:%s", *port)
	server := &http.Server{
		Addr:              addr,
		Handler:           demo.NewRouter(demo.CannedBridges()),
		ReadHeaderTimeout: serverReadTimeout,
	}

	fmt.Printf("listening on http://%s\n", addr)
	fmt.Printf("  current:    http://%s/bridge/v1/bridges?network_id=0\n", addr)
	fmt.Printf("  spec-first: http://%s%s/bridge/v1/bridges?network_id=0\n", addr, demo.SpecFirstPrefix)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "server stopped: %v\n", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
