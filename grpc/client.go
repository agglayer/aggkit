package grpc

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/agglayer/aggkit/config/types"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	defaultTimeout           = 5 * time.Second
	defaultInitialBackoff    = 100 * time.Millisecond
	defaultMaxAttempts       = 3
	defaultMaxBackoff        = 10 * time.Second
	defaultBackoffMultiplier = 2.0
)

// ClientConfig is the configuration for the gRPC client
type ClientConfig struct {
	// URL is the URL of the gRPC server
	URL string `mapstructure:"URL"`

	// MinConnectTimeout is the minimum time to wait for a connection to be established
	// This is used to prevent the client from hanging indefinitely if the server is unreachable.
	MinConnectTimeout types.Duration `mapstructure:"MinConnectTimeout"`

	// InitialBackoff is the initial delay before retrying a request
	InitialBackoff types.Duration `mapstructure:"InitialBackoff"`

	// MaxBackoff is the maximum backoff duration for retries
	MaxBackoff types.Duration `mapstructure:"MaxBackoff"`

	// BackoffMultiplier is the multiplier for the backoff duration
	BackoffMultiplier float64 `mapstructure:"BackoffMultiplier"`

	// MaxAttempts is the maximum number of retries for a request
	MaxAttempts int `mapstructure:"MaxAttempts"`

	// RequestTimeout is the timeout for individual requests
	RequestTimeout types.Duration `mapstructure:"RequestTimeout"`

	// UseTLS indicates whether to use TLS for the gRPC connection
	UseTLS bool `mapstructure:"UseTLS"`
}

// DefaultConfig returns a default configuration for the gRPC client
func DefaultConfig() *ClientConfig {
	return &ClientConfig{
		URL:               "localhost:50051",
		MinConnectTimeout: types.NewDuration(defaultTimeout),
		MaxAttempts:       defaultMaxAttempts,
		InitialBackoff:    types.NewDuration(defaultInitialBackoff),
		MaxBackoff:        types.NewDuration(defaultMaxBackoff),
		BackoffMultiplier: defaultBackoffMultiplier,
		RequestTimeout:    types.NewDuration(defaultTimeout),
		UseTLS:            false,
	}
}

// String returns a string representation of the gRPC client configuration
func (c *ClientConfig) String() string {
	if c == nil {
		return "none"
	}

	return fmt.Sprintf("GRPC Client Config: "+
		"URL=%s, MinConnectTimeout=%s, "+
		"InitialBackoff=%s, MaxBackoff=%s, "+
		"BackoffMultiplier=%f, MaxAttempts=%d, "+
		"UseTLS=%t",
		c.URL, c.MinConnectTimeout.String(),
		c.InitialBackoff.String(), c.MaxBackoff.String(),
		c.BackoffMultiplier, c.MaxAttempts,
		c.UseTLS)
}

// Validate checks if the gRPC client configuration is valid.
// It returns an error if any of the required fields are missing or invalid.
func (c *ClientConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("gRPC client configuration cannot be nil")
	}

	if c.URL == "" {
		return fmt.Errorf("gRPC client URL cannot be empty")
	}

	if c.MinConnectTimeout.Duration <= 0 {
		return fmt.Errorf("MinConnectTimeout must be greater than zero")
	}

	if c.InitialBackoff.Duration <= 0 {
		return fmt.Errorf("InitialBackoff must be greater than zero")
	}

	if c.MaxBackoff.Duration <= 0 {
		return fmt.Errorf("MaxBackoff must be greater than zero")
	}

	if c.InitialBackoff.Duration >= c.MaxBackoff.Duration {
		return fmt.Errorf("InitialBackoff must be less than MaxBackoff")
	}

	if c.BackoffMultiplier < 1.0 {
		return fmt.Errorf("BackoffMultiplier must be greater than 1.0")
	}

	if c.MaxAttempts < 1 {
		return fmt.Errorf("MaxAttempts must be at least 1")
	}

	return nil
}

// Client holds the gRPC connection and services
type Client struct {
	conn *grpc.ClientConn
}

// NewClient initializes and returns a new gRPC client
func NewClient(cfg *ClientConfig) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("gRPC client configuration cannot be nil")
	}

	connectBackoff := backoff.DefaultConfig
	connectBackoff.BaseDelay = cfg.InitialBackoff.Duration
	connectBackoff.MaxDelay = cfg.MaxBackoff.Duration
	connectBackoff.Multiplier = cfg.BackoffMultiplier
	connectParams := grpc.ConnectParams{
		Backoff:           connectBackoff,
		MinConnectTimeout: cfg.MinConnectTimeout.Duration,
	}

	serviceCfg := ServiceConfig{
		MethodConfig: []MethodConfig{
			{
				Name: []MethodName{{}}, // Empty name matches all methods
				RetryPolicy: &RetryPolicy{
					MaxAttempts:       cfg.MaxAttempts,
					InitialBackoff:    cfg.InitialBackoff.String(),
					MaxBackoff:        cfg.MaxBackoff.String(),
					BackoffMultiplier: cfg.BackoffMultiplier,
					RetryableStatusCodes: []string{
						GRPCCodeCanonicalString(codes.Unavailable),
						GRPCCodeCanonicalString(codes.DeadlineExceeded),
						GRPCCodeCanonicalString(codes.ResourceExhausted),
						GRPCCodeCanonicalString(codes.Aborted),
						GRPCCodeCanonicalString(codes.Unknown),
						GRPCCodeCanonicalString(codes.Internal),
					},
				},
			},
		},
	}

	serviceCfgJSON, err := json.Marshal(serviceCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal gRPC service config: %w", err)
	}

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(connectParams),
		grpc.WithDefaultServiceConfig(string(serviceCfgJSON)),
	}

	if cfg.UseTLS {
		creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: false, MinVersion: tls.VersionTLS12})
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

// GRPCCodeCanonicalString returns the canonical service config string for a gRPC status code.
func GRPCCodeCanonicalString(c codes.Code) string {
	if c == codes.OK {
		return "OK"
	}

	var b strings.Builder
	name := c.String()

	for i, r := range name {
		if i > 0 && unicode.IsUpper(r) &&
			(unicode.IsLower(rune(name[i-1])) || (i+1 < len(name) && unicode.IsLower(rune(name[i+1])))) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToUpper(r))
	}

	return b.String()
}
