// Package client is a client of the REST API of a GER validator signer, to call a validator by hand: see the `client`
// command of ger_validator_signer. The AggOracle has its own client (aggoracle/gervalidator), this one is not used by
// it: it is for testing a validator (does it answer, what does it say of a GER, is its signature right).
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/api"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/signer"
	"github.com/ethereum/go-ethereum/common"
)

const (
	pathSign   = "/v1/ger/sign"
	pathInfo   = "/v1/info"
	pathHealth = "/health"

	// DefaultTimeout is longer than the timeout of the requests of a validator (15 seconds by default), so the
	// validator is the one that gives up
	DefaultTimeout = 20 * time.Second

	// maxResponseBytes limits how much of the answer of a validator is read
	maxResponseBytes = 1 << 20
	// maxMessageLength limits how much of a message of the validator is kept in an error
	maxMessageLength = 200
)

var (
	// ErrInvalidResponse is returned when the validator answers something that is not its API
	ErrInvalidResponse = errors.New("invalid response of the validator")
	// ErrBadSignature is returned when the signature of a validator is not what the contract accepts
	ErrBadSignature = errors.New("bad signature")
)

// Config is the configuration of the client
type Config struct {
	// BaseURL is where the validator is, like http://localhost:8080
	BaseURL string
	// Timeout of each request. 0 is DefaultTimeout
	Timeout time.Duration
	// CACertFile is an optional PEM file with the CA that signed the certificate of the validator, instead of the
	// system trust store
	CACertFile string
}

// Client calls the REST API of one validator. It is safe for concurrent use
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// Error is an error response of the validator: it answered, and said no
type Error struct {
	// Status is the HTTP status
	Status int
	// Code is the code of the body (api.Code*). Empty if the body is not the error of the API (a wrong URL, a proxy)
	Code    string
	Message string
	// RetryAfter is what the validator asks to wait before trying again, if it says so
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("%s (HTTP %d): %s", e.Code, e.Status, e.Message)
}

// Retryable says if asking again later can have another answer, as the API defines it: the code decides, not the
// HTTP status
func (e *Error) Retryable() bool {
	switch e.Code {
	case api.CodeGERNotFinal, api.CodeRateLimited, api.CodeInternal:
		return true
	default:
		return false
	}
}

// New creates the client of the validator
func New(cfg Config) (*Client, error) {
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("invalid URL of the validator: it must be an absolute http or https URL")
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CACertFile != "" {
		caPEM, err := os.ReadFile(cfg.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read the CA certificate file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("the CA certificate file %s has no valid PEM certificates", cfg.CACertFile)
		}
		tlsConfig.RootCAs = pool
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("unexpected default HTTP transport")
	}
	transport = transport.Clone()
	transport.TLSClientConfig = tlsConfig

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		baseURL: cfg.BaseURL,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			// A validator answers by itself: a redirect is never followed
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// Health asks if the validator is running. It doesn't query L1 nor sign
func (c *Client) Health(ctx context.Context) (*api.HealthResponse, error) {
	var health api.HealthResponse
	if err := c.do(ctx, http.MethodGet, pathHealth, nil, &health); err != nil {
		return nil, err
	}
	return &health, nil
}

// Info asks for the version, the address that signs, the finality and the L1 of the validator
func (c *Client) Info(ctx context.Context) (*api.InfoResponse, error) {
	var info api.InfoResponse
	if err := c.do(ctx, http.MethodGet, pathInfo, nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// SignGER asks the validator to sign the GER. If the validator says no, the error is an *Error. The signature is not
// verified: see Verify
func (c *Client) SignGER(ctx context.Context, ger common.Hash) (*api.SignResponse, error) {
	body, err := json.Marshal(api.SignRequest{GER: ger.Hex()})
	if err != nil {
		return nil, fmt.Errorf("failed to encode the request: %w", err)
	}
	var signed api.SignResponse
	if err := c.do(ctx, http.MethodPost, pathSign, body, &signed); err != nil {
		return nil, err
	}
	if signed.GER != ger {
		return nil, fmt.Errorf("%w: it signed the GER %s, not the %s", ErrInvalidResponse, signed.GER, ger)
	}
	return &signed, nil
}

// Verify checks the response of a validator to a request to sign the GER: the signature must be what
// AggOracleCommittee.injectGER accepts, and it must be from the signer that the response says. If expected is not
// nil, the signer must be that address too (the one registered in the committee).
func Verify(ger common.Hash, response *api.SignResponse, expected *common.Address) error {
	if err := signer.VerifyGERSignature(ger, response.Signature, response.Signer); err != nil {
		return fmt.Errorf("%w: %w", ErrBadSignature, err)
	}
	if expected != nil && *expected != response.Signer {
		return fmt.Errorf("%w: signed by %s, expected %s", ErrBadSignature, response.Signer, *expected)
	}
	return nil
}

// do makes the request and decodes the answer of status 200 in out. Any other status is an *Error
func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	endpoint, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		return fmt.Errorf("invalid URL of the validator: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create the request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request to the validator failed: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("failed to read the response of the validator: %w", err)
	}
	if len(payload) > maxResponseBytes {
		return fmt.Errorf("%w: bigger than %d bytes", ErrInvalidResponse, maxResponseBytes)
	}

	if resp.StatusCode != http.StatusOK {
		return errorFromResponse(resp, payload)
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("%w: it is not what the API answers: %w", ErrInvalidResponse, err)
	}
	return nil
}

// errorFromResponse builds the Error of a response that is not a 200
func errorFromResponse(resp *http.Response, payload []byte) *Error {
	apiErr := &Error{Status: resp.StatusCode}
	var body api.ErrorResponse
	if err := json.Unmarshal(payload, &body); err == nil && body.Code != "" {
		apiErr.Code = body.Code
		apiErr.Message = printable(body.Message)
	} else {
		// Not the error of the API: a wrong URL, a proxy, a validator of another version...
		apiErr.Message = printable(string(payload))
	}
	if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
		apiErr.RetryAfter = time.Duration(seconds) * time.Second
	}
	return apiErr
}

// printable keeps what a validator says short and without control characters, as it is printed in a terminal
func printable(message string) string {
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(message))
	if len(message) > maxMessageLength {
		message = strings.ToValidUTF8(message[:maxMessageLength], "") + "..."
	}
	return message
}
