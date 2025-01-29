package grpc

import (
	"context"
	"time"

	"github.com/agglayer/aggkit/aggsender/types"
)

const TIMEOUT = 2

// AggchainProofClientInterface defines an interface for aggchain proof client
type AggchainProofClientInterface interface {
	GenerateAggchainProof(startBlock uint64, maxEndBlock uint64) (*types.AggchainProof, error)
}

// AggchainProofClient provides an implementation for the AggchainProofClient interface
type AggchainProofClient struct {
	client types.AggchainProofServiceClient
}

// NewAggchainProofClient initializes a new AggchainProof instance
func NewAggchainProofClient(serverAddr string) (*AggchainProofClient, error) {
	grpcClient, err := NewClient(serverAddr)
	if err != nil {
		return nil, err
	}
	return &AggchainProofClient{
		client: types.NewAggchainProofServiceClient(grpcClient.conn),
	}, nil
}

func (c *AggchainProofClient) GenerateAggchainProof(startBlock uint64,
	maxEndBlock uint64) (*types.AggchainProof, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*TIMEOUT)
	defer cancel()

	resp, err := c.client.GenerateAggchainProof(ctx, &types.GenerateAggchainProofRequest{
		StartBlock:  startBlock,
		MaxEndBlock: maxEndBlock,
	})
	if err != nil {
		return nil, err
	}
	return &types.AggchainProof{
		Proof:      string(resp.AggchainProof),
		StartBlock: resp.StartBlock,
		EndBlock:   resp.EndBlock,
	}, nil
}
