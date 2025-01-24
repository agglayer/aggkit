package grpc

import (
	"context"
	"time"

	"google.golang.org/grpc"

	"github.com/agglayer/aggkit/aggsender/types"
)

const TIMEOUT = 60

// Client struct holds the gRPC client connection
type Client struct {
	conn   *grpc.ClientConn
	client types.AuthProofServiceClient
}

// NewClient initializes and returns a new gRPC client
func NewClient(serverAddr string) (*Client, error) {
	conn, err := grpc.NewClient(serverAddr)
	if err != nil {
		return nil, err
	}

	client := types.NewAuthProofServiceClient(conn)
	return &Client{conn: conn, client: client}, nil
}

func (c *Client) FetchAuthProof(startBlock uint64, maxEndBlock uint64) (*types.AuthProof, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*TIMEOUT)
	defer cancel()

	resp, err := c.client.FetchAuthProof(ctx, &types.FetchAuthProofRequest{
		StartBlock:  startBlock,
		MaxEndBlock: maxEndBlock,
	})
	if err != nil {
		return nil, err
	}
	return &types.AuthProof{
		Proof:      string(resp.AuthProof),
		StartBlock: resp.StartBlock,
		EndBlock:   resp.EndBlock,
	}, nil
}

// Close closes the gRPC connection
func (c *Client) Close() error {
	return c.conn.Close()
}
