// Instantiate a gRPC server with the given options.
package grpcserver

import (
	"context"
	"log"
	"net"

	pb "github.com/agglayer/aggkit/aggsender/types"

	"google.golang.org/grpc"
)

// server is used to implement aggsender.AggSenderServer.
type server struct {
	pb.UnimplementedAggSenderServer
}

func NewGRPCServer(opts ...grpc.ServerOption) *grpc.Server {
	s := grpc.NewServer(opts...)
	pb.RegisterAggSenderServer(s, &server{})
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

// And register the AggsenderServiceServer with the server.
func (s *server) ReceiveProof(ctx context.Context, req *pb.ProofRequest) (*pb.ProofResponse, error) {
	// Implement your logic here
	return &pb.ProofResponse{}, nil
}
