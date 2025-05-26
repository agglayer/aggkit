package common

import (
	"fmt"
	"strings"
	"time"

	"github.com/agglayer/aggkit/config/types"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	defaultMaxRequestRetries = 3
	defaultInitialDelay      = 1 * time.Second
	defaultMinConnectTimeout = 5 * time.Second
)

// Config is the configuration for the gRPC client
type Config struct {
	// URL is the URL of the gRPC server
	URL string `mapstructure:"URL"`

	// MinConnectTimeout is the minimum time to wait for a connection to be established
	// This is used to prevent the client from hanging indefinitely if the server is unreachable.
	MinConnectTimeout types.Duration `mapstructure:"MinConnectTimeout"`

	// MaxRequestRetries is the maximum number of retries for a request
	MaxRequestRetries uint `mapstructure:"MaxRequestRetries"`

	// InitialDelay is the initial delay before retrying a request
	InitialDelay types.Duration `mapstructure:"InitialDelay"`
}

// DefaultConfig returns a default configuration for the gRPC client
func DefaultConfig() *Config {
	return &Config{
		URL:               "",
		MinConnectTimeout: types.NewDuration(defaultMinConnectTimeout),
		MaxRequestRetries: defaultMaxRequestRetries,
		InitialDelay:      types.NewDuration(defaultInitialDelay),
	}
}

// String returns a string representation of the gRPC client configuration
func (c *Config) String() string {
	if c == nil {
		return "none"
	}

	return fmt.Sprintf("GRPC Client Config: URL=%s, MinConnectTimeout=%s, MaxRequestRetries=%d, InitialDelay=%s",
		c.URL, c.MinConnectTimeout.String(), c.MaxRequestRetries, c.InitialDelay.String())
}

// Client holds the gRPC connection and services
type Client struct {
	conn *grpc.ClientConn
}

// NewClient initializes and returns a new gRPC client
func NewClient(cfg *Config) (*Client, error) {
	connectParams := grpc.ConnectParams{
		Backoff:           backoff.DefaultConfig,
		MinConnectTimeout: cfg.MinConnectTimeout.Duration,
	}

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(connectParams),
	}

	// trim the http:// and https:// prefixes from the URL because the go-grpc client expects it without it
	serverAddr := strings.TrimPrefix(cfg.URL, "http://")
	serverAddr = strings.TrimPrefix(serverAddr, "https://")
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
