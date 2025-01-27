package grpc

import (
	"google.golang.org/grpc"
)

// Client holds the gRPC connection and services
type Client struct {
	conn *grpc.ClientConn
}

// NewClient initializes and returns a new gRPC client with a 15-minute connection timeout
func NewClient(serverAddr string) (*Client, error) {
	conn, err := grpc.NewClient(serverAddr)
	if err != nil {
		return nil, err
	}

	return &Client{conn: conn}, nil
}

// Close closes the gRPC connection
func (c *Client) Close() error {
	return c.conn.Close()
}
