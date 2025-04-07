package common

import (
	"fmt"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
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

// Conn returns the gRPC connection
func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}

// Close closes the gRPC connection
func (c *Client) Close() error {
	return c.conn.Close()
}

// RepackGRPCErrorWithDetails extracts *status.Status and formats ErrorInfo details into a single error
func RepackGRPCErrorWithDetails(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return err // Not a gRPC status error
	}

	var detailStrs []string
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			var detail string
			if len(info.Metadata) > 0 {
				detail += ", Metadata: {"
				for k, v := range info.Metadata {
					detail += fmt.Sprintf("%s: %s, ", k, v)
				}
				detail = strings.TrimSuffix(detail, ", ") + "}"
			}

			detailStr := fmt.Sprintf("Reason: %s, Domain: %s. %s", info.Reason, info.Domain, detail)
			detailStrs = append(detailStrs, detailStr)
		} else {
			detailStrs = append(detailStrs, fmt.Sprintf("%+v", d))
		}
	}

	return fmt.Errorf("%s - Details: %s", st.Message(), joinDetails(detailStrs))
}

// joinDetails joins detail strings with a separator
func joinDetails(details []string) string {
	if len(details) == 0 {
		return "none"
	}
	return fmt.Sprintf("[%s]", strings.Join(details, ";"))
}
