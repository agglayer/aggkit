// Instantiate a gRPC server with the given options.
package grpc

import (
	"log"
	"net"

	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	"google.golang.org/grpc"
)

// server is used to implement aggsender.AggSenderServer.
type server struct {
	types.UnimplementedAggSenderServer
	aggsenderStorage db.AggSenderSQLStorage
}

func NewGRPCServer(opts ...grpc.ServerOption) *grpc.Server {
	s := grpc.NewServer(opts...)
	// TODO - Add aggsenderStorage initialization
	types.RegisterAggSenderServer(s, &server{})
	return s
}

func StartGRPCServer(address string, opts ...grpc.ServerOption) {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	s := NewGRPCServer(opts...)
	log.Printf("gRPC server listening on %s", address)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
