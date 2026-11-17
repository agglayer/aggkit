package grpc

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/agglayer/aggkit/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

const (
	// keepaliveMinTime is the minimum time a client must wait between keepalive pings.
	keepaliveMinTime = 10 * time.Second
	// keepaliveTime is how often the server pings an idle client.
	keepaliveTime = 2 * time.Hour
	// keepaliveTimeout is how long the server waits for a keepalive ack.
	keepaliveTimeout = 20 * time.Second
)

type ServerConfig struct {
	// Host is the address to bind the gRPC server
	Host string `mapstructure:"Host"`
	// Port is the port to bind the gRPC server
	Port int `mapstructure:"Port"`
	// EnableReflection indicates whether gRPC server reflection is enabled
	// This allows clients to introspect the server's services and methods.
	EnableReflection bool `mapstructure:"EnableReflection"`
	// MaxDecodingMessageSize is the maximum size of a message that the server can receive.
	MaxDecodingMessageSize int `mapstructure:"MaxDecodingMessageSize"`
	// MaxConcurrentRequests is the maximum number of RPCs the server executes at the same time.
	// Further requests are rejected with ResourceExhausted. 0 means DefaultMaxConcurrentRequests.
	MaxConcurrentRequests int `mapstructure:"MaxConcurrentRequests"`
	// MaxConcurrentStreams is the maximum number of concurrent streams per HTTP/2 connection.
	// 0 means DefaultMaxConcurrentStreams.
	MaxConcurrentStreams uint32 `mapstructure:"MaxConcurrentStreams"`
	// MaxRequestsPerSecondPerPeer limits the request rate of a single peer address.
	// 0 means DefaultMaxRequestsPerSecondPerPeer.
	MaxRequestsPerSecondPerPeer float64 `mapstructure:"MaxRequestsPerSecondPerPeer"`
	// MaxRequestsBurstPerPeer is the burst of the per-peer rate limit.
	// 0 means four times MaxRequestsPerSecondPerPeer.
	MaxRequestsBurstPerPeer int `mapstructure:"MaxRequestsBurstPerPeer"`
}

// Server encapsulates a gRPC server instance, its network listener, and the address it listens on.
// It provides the necessary fields to manage the lifecycle and configuration of a gRPC server.
type Server struct {
	grpcServer *grpc.Server
	listener   net.Listener
	addr       string

	cfg ServerConfig
}

// NewServer creates and returns a new gRPC Server instance listening on the specified address.
// Every server recovers from handler panics and limits concurrent requests, the request rate
// per peer and the concurrent streams per connection. TLS is not configured here: when the
// server listens on a non-loopback address, the connection must be protected by the
// infrastructure that exposes it.
// Returns a pointer to the Server and an error if the listener cannot be created.
func NewServer(cfg ServerConfig, opts ...grpc.ServerOption) (*Server, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	serverAddr := trimGRPCAddress(addr)
	if cfg.MaxDecodingMessageSize > 0 {
		opts = append(opts, grpc.MaxRecvMsgSize(cfg.MaxDecodingMessageSize))
	}

	maxConcurrentRequests := cfg.MaxConcurrentRequests
	if maxConcurrentRequests <= 0 {
		maxConcurrentRequests = DefaultMaxConcurrentRequests
	}

	maxConcurrentStreams := cfg.MaxConcurrentStreams
	if maxConcurrentStreams == 0 {
		maxConcurrentStreams = DefaultMaxConcurrentStreams
	}

	requestsPerSecond := cfg.MaxRequestsPerSecondPerPeer
	if requestsPerSecond <= 0 {
		requestsPerSecond = DefaultMaxRequestsPerSecondPerPeer
	}

	limiter := newPerPeerRateLimiter(requestsPerSecond, cfg.MaxRequestsBurstPerPeer)
	opts = append(opts,
		grpc.MaxConcurrentStreams(maxConcurrentStreams),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             keepaliveMinTime,
			PermitWithoutStream: false,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    keepaliveTime,
			Timeout: keepaliveTimeout,
		}),
		grpc.ChainUnaryInterceptor(
			recoveryUnaryInterceptor(),
			rateLimitUnaryInterceptor(limiter),
			concurrencyLimitUnaryInterceptor(make(chan struct{}, maxConcurrentRequests)),
		),
		grpc.ChainStreamInterceptor(recoveryStreamInterceptor()),
	)

	listener, err := net.Listen("tcp", serverAddr)
	if err != nil {
		return nil, err
	}

	server := grpc.NewServer(opts...)

	if cfg.EnableReflection {
		reflection.Register(server) // Register reflection service on gRPC server
	}

	if !isLoopback(cfg.Host) {
		log.Warnf("gRPC server on %s is reachable from the network: put it behind an "+
			"infrastructure that terminates TLS and restrict who can reach it", serverAddr)
	}

	return &Server{
		grpcServer: server,
		listener:   listener,
		addr:       serverAddr,
		cfg:        cfg,
	}, nil
}

// Start launches the gRPC server and begins serving incoming connections.
// It blocks until the provided context is cancelled, at which point it gracefully stops the server.
// If the server fails to start, an error is logged.
func (s *Server) Start(ctx context.Context) {
	go func() {
		<-ctx.Done()
		s.stop()
	}()

	// TODO: Set starting block for claimsyncer

	if err := s.grpcServer.Serve(s.listener); err != nil {
		log.Errorf("failed to start gRPC server: %v", err)
	}
}

// stop gracefully shuts down the gRPC server, ensuring that all ongoing RPCs are completed before stopping.
// It also logs an informational message indicating that the server has stopped and specifies the server address.
func (s *Server) stop() {
	s.grpcServer.GracefulStop()
	log.Infof("gRPC server on %s stopped", s.addr)
}

// Addr returns the address on which the server is listening.
func (s *Server) Addr() string {
	return s.addr
}

// GRPC returns the underlying gRPC server instance.
func (s *Server) GRPC() *grpc.Server {
	return s.grpcServer
}

// isLoopback reports whether a bind address only accepts connections from the local machine.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}
