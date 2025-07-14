package grpc

import (
	"context"
	"fmt"
	"net"

	"github.com/agglayer/aggkit/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

type ServerConfig struct {
	// Host is the address to bind the gRPC server
	Host string `mapstructure:"Host"`
	// Port is the port to bind the gRPC server
	Port int `mapstructure:"Port"`
	// EnableReflection indicates whether gRPC server reflection is enabled
	// This allows clients to introspect the server's services and methods.
	EnableReflection bool `mapstructure:"EnableReflection"`
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
// It accepts an address string, a boolean to enable server reflection, and optional gRPC server options.
// If enableReflection is true, the server will register the reflection service for introspection.
// Returns a pointer to the Server and an error if the listener cannot be created.
func NewServer(cfg ServerConfig, opts ...grpc.ServerOption) (*Server, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	serverAddr := trimGRPCAddress(addr)

	listener, err := net.Listen("tcp", serverAddr)
	if err != nil {
		return nil, err
	}

	server := grpc.NewServer(opts...)

	if cfg.EnableReflection {
		reflection.Register(server) // Register reflection service on gRPC server
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
