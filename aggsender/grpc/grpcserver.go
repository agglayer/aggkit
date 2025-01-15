// Instantiate a gRPC server with the given options.
package grpc

import (
	"context"
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
	types.RegisterAggSenderServer(s, &server{})
	// @temaniarpit27 - Add initialization for aggsender storage
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

// Dummy implementation of the Proof method.
// And register the AggsenderServiceServer with the server.
func (s *server) ReceiveProof(ctx context.Context, req *types.ProofRequest) (*types.ProofResponse, error) {
	// Implement your logic here
	log.Printf("Received proof: %v", req)
	valid, err := s.aggsenderStorage.ValidateProof(req)
	if err != nil {
		log.Printf("Error validating proof: %v", err)
		return &types.ProofResponse{}, err
	}

	if !valid {
		log.Printf("Proof is invalid")
		return &types.ProofResponse{}, err
	}

	log.Printf("Proof is valid")
	err = s.aggsenderStorage.AddAuthProof(context.Background(), types.AuthProof{
		Proof:      req.Proof,
		Identifier: req.Identifier,
	})
	if err != nil {
		log.Printf("Error adding proof: %v", err)
		return &types.ProofResponse{}, err
	}
	log.Printf("Proof added successfully")

	return &types.ProofResponse{}, nil
}
