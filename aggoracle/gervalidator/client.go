package gervalidator

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
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// maxResponseBytes limits how much of the response of a validator is read
const maxResponseBytes = 1 << 20

// ClientConfig is the configuration of the HTTP client of the GER validators
type ClientConfig struct {
	// RequestTimeout is the timeout of each request. 0 means no timeout (the context rules)
	RequestTimeout time.Duration
	// CACertFile is an optional PEM file with the CA(s) used to verify the validators certificate,
	// instead of the system trust store
	CACertFile string
	// ClientCertFile and ClientKeyFile are optional PEM files to authenticate against the validators (mTLS)
	ClientCertFile string
	ClientKeyFile  string
}

// HTTPClient is the Client that talks to the REST API of the GER validators
type HTTPClient struct {
	httpClient *http.Client
}

var _ Client = (*HTTPClient)(nil)

// NewHTTPClient creates the HTTP client of the GER validators
func NewHTTPClient(cfg ClientConfig) (*HTTPClient, error) {
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

	if cfg.ClientCertFile != "" || cfg.ClientKeyFile != "" {
		if cfg.ClientCertFile == "" || cfg.ClientKeyFile == "" {
			return nil, errors.New("the client certificate and the client key must be set together")
		}
		cert, err := tls.LoadX509KeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load the client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("unexpected default HTTP transport")
	}
	transport = transport.Clone()
	transport.TLSClientConfig = tlsConfig

	return &HTTPClient{
		httpClient: &http.Client{
			Timeout:   cfg.RequestTimeout,
			Transport: transport,
			// A validator must answer by itself: never follow a redirect to another place
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// SignGER asks the validator at baseURL to sign the GER
func (c *HTTPClient) SignGER(ctx context.Context, baseURL string, ger common.Hash) (*SignResponse, error) {
	endpoint, err := signEndpoint(baseURL)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(SignRequest{GER: ger})
	if err != nil {
		return nil, fmt.Errorf("failed to encode the request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to the validator failed: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read the response of the validator: %w", err)
	}
	if len(payload) > maxResponseBytes {
		return nil, fmt.Errorf("%w: response bigger than %d bytes", ErrInvalidResponse, maxResponseBytes)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errorFromResponse(resp.StatusCode, payload)
	}

	var signResp SignResponse
	if err := json.Unmarshal(payload, &signResp); err != nil {
		return nil, fmt.Errorf("%w: cannot decode the response: %w", ErrInvalidResponse, err)
	}
	return &signResp, nil
}

// signEndpoint returns the URL of the sign endpoint of the validator at baseURL
func signEndpoint(baseURL string) (string, error) {
	if baseURL == "" {
		return "", ErrNoURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("%w: invalid validator URL, it must be an absolute http or https URL", ErrNoURL)
	}
	return url.JoinPath(baseURL, SignaturePath)
}

// errorFromResponse converts the error response of a validator into one of the typed errors of the package.
// The code of the body decides: the HTTP status alone doesn't (a 404 can be a wrong URL, not an unknown GER).
func errorFromResponse(statusCode int, payload []byte) error {
	var errResp ErrorResponse
	if err := json.Unmarshal(payload, &errResp); err != nil {
		errResp = ErrorResponse{}
	}

	switch errResp.Code {
	case CodeGERUnknown:
		return fmt.Errorf("%w: %s", ErrGERUnknown, truncate(errResp.Message))
	case CodeGERBlockNotCanon:
		return fmt.Errorf("%w: %s", ErrGERNotCanonical, truncate(errResp.Message))
	case CodeGERNotFinal:
		return fmt.Errorf("%w: %s", ErrGERNotFinal, truncate(errResp.Message))
	case CodeRateLimited:
		return fmt.Errorf("%w: %s", ErrRateLimited, truncate(errResp.Message))
	case CodeInvalidRequest:
		return fmt.Errorf("%w: %s", ErrBadRequest, truncate(errResp.Message))
	}

	switch statusCode {
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: HTTP 429", ErrRateLimited)
	case http.StatusBadRequest:
		return fmt.Errorf("%w: HTTP 400", ErrBadRequest)
	default:
		return &APIError{StatusCode: statusCode, Code: errResp.Code, Message: errResp.Message}
	}
}
