// Package api is the REST API of the GER validator signer.
//
//	@title			GER validator signer API
//	@version		1.0
//	@description	REST API of the validators of the AggOracleCommittee v2. The AggOracle asks a validator to sign
//	@description	a Global Exit Root (GER) and sends the signatures of enough validators to the contract.
//	@description	The validator signs a GER only if it is in the AgglayerGER contract of L1 and the L1 block that
//	@description	inserted it is canonical and final for the finality the validator is configured with.
//	@BasePath		/
package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/config"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/service"
	"github.com/ethereum/go-ethereum/common"
)

const (
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = time.Minute
	shutdownTimeout   = 10 * time.Second
	maxHeaderBytes    = 1 << 14
)

// Service is what the API needs from the service of the validator
type Service interface {
	// SignGER signs the GER if it is in L1 and its block is canonical and final
	SignGER(ctx context.Context, ger common.Hash) (*service.Result, error)
	// Info describes the validator
	Info() service.Info
}

// Server is the REST API
type Server struct {
	logger  *slog.Logger
	cfg     config.Config
	service Service
	limiter *clientLimiter
}

// New creates the REST API. The rate limit, the timeouts and the limits of the requests come from cfg
func New(logger *slog.Logger, cfg config.Config, svc Service) *Server {
	return &Server{
		logger:  logger.With("module", "api"),
		cfg:     cfg,
		service: svc,
		limiter: newClientLimiter(cfg.PublicREST.MaxRequestsPerIPAndSecond, cfg.PublicREST.MaxRequestsBurstPerIP),
	}
}

// Handler returns the handler of the API, with its routes and middleware
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /v1/ger/sign", s.limited(http.HandlerFunc(s.handleSign)))
	mux.Handle("GET /v1/info", s.limited(http.HandlerFunc(s.handleInfo)))
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /swagger/doc.json", s.handleSpec(specJSON, "application/json"))
	mux.HandleFunc("GET /swagger/doc.yaml", s.handleSpec(specYAML, "application/yaml"))
	return s.recoverer(s.accessLog(mux))
}

// Serve serves the API on the listener, with TLS if the certificate is configured, until the context is done
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	rest := s.cfg.PublicREST
	server := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: min(readHeaderTimeout, rest.ReadTimeout.Duration),
		ReadTimeout:       rest.ReadTimeout.Duration,
		WriteTimeout:      rest.WriteTimeout.Duration,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}

	serveErr := make(chan error, 1)
	go func() {
		if rest.TLSCertFile != "" {
			serveErr <- server.ServeTLS(listener, rest.TLSCertFile, rest.TLSKeyFile)
			return
		}
		serveErr <- server.Serve(listener)
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("failed to shut the server down: %w", err)
		}
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// limited applies the rate limit of each client to the handler
func (s *Server) limited(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(clientAddress(r)) {
			writeError(w, apiError{
				status: http.StatusTooManyRequests, code: CodeRateLimited,
				message: "too many requests", retryAfter: 1,
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder keeps the status of a response, to log it
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// accessLog logs each request: what was asked and the status of the answer, never the body
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.Debug("request",
			"method", r.Method, "path", r.URL.Path, "status", recorder.status,
			"duration", time.Since(start), "client", clientAddress(r))
	})
}

// recoverer turns a panic of a handler into an internal error, instead of closing the connection
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic serving a request", "path", r.URL.Path, "panic", recovered)
				writeError(w, apiError{status: http.StatusInternalServerError, code: CodeInternal, message: "internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
