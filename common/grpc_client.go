package common

import (
	"fmt"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
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

// GRPCError represents an error structure used in gRPC communication.
// It contains the error code, a descriptive message, and optional details
// providing additional context about the error.
type GRPCError struct {
	Code    codes.Code
	Message string
	Details []string
}

// Error returns a formatted string representation of the GRPCError,
// including the error code, message, and details. The details are
// joined into a single string for readability.
func (e GRPCError) Error() string {
	return fmt.Sprintf("Code: %s, Message: %s, Details: %s", e.Code.String(), e.Message, joinDetails(e.Details))
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

	return GRPCError{
		Code:    st.Code(),
		Message: st.Message(),
		Details: detailStrs,
	}
}

// joinDetails joins detail strings with a separator
func joinDetails(details []string) string {
	if len(details) == 0 {
		return "none"
	}
	return fmt.Sprintf("[%s]", strings.Join(details, ";"))
}
