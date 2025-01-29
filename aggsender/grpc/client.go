package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client holds the gRPC connection and services
type Client struct {
	conn *grpc.ClientConn
}

// NewClient initializes and returns a new gRPC client
func NewClient(serverAddr string) (*Client, error) {
	// TODO - Check if we need to use this
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	conn, err := grpc.NewClient(serverAddr, opts...)
	if err != nil {
		return nil, err
	}

	return &Client{conn: conn}, nil
}

// Close closes the gRPC connection
func (c *Client) Close() error {
	return c.conn.Close()
}
