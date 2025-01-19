package grpc

import (
	"context"
	"log"

	"github.com/agglayer/aggkit/aggsender/types"
)

func (s *server) ReceiveAuthProof(ctx context.Context, req *types.ProofRequest) (*types.ProofResponse, error) {
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
