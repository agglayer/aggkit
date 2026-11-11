package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
)

const (
	defaultRequestTimeout = 10 * time.Second

	// maxResponseBytes caps how much of a response body the client reads, so a misconfigured
	// or wrong endpoint cannot make it read an unbounded stream.
	maxResponseBytes = 16 << 20

	statusPath                 = "/aggsender/v1/status"
	certificateHeaderPath      = "/aggsender/v1/certificate-header"
	certificateBridgeExitsPath = "/aggsender/v1/certificate-bridge-exits"

	heightQueryParam = "height"
)

// Client calls the aggkit PublicREST API. The base URL is the root of that
// server, for example http://localhost:5577.
type Client struct {
	baseURL        string
	requestTimeout time.Duration
	httpClient     *http.Client
}

// NewClient returns a client for the aggkit PublicREST API listening at baseURL.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:        strings.TrimRight(baseURL, "/"),
		requestTimeout: defaultRequestTimeout,
		httpClient:     &http.Client{},
	}
}

// GetStatus returns the current aggsender status.
func (c *Client) GetStatus() (*types.AggsenderInfo, error) {
	result := types.AggsenderInfo{}
	if err := c.get(statusPath, nil, &result); err != nil {
		return nil, fmt.Errorf("error calling %s: %w", statusPath, err)
	}
	return &result, nil
}

// GetCertificateHeaderPerHeight returns the certificate at the given height.
// When height is nil it returns the last sent certificate.
func (c *Client) GetCertificateHeaderPerHeight(height *uint64) (*types.Certificate, error) {
	result := types.Certificate{}
	if err := c.get(certificateHeaderPath, heightQuery(height), &result); err != nil {
		return nil, fmt.Errorf("error calling %s: %w", certificateHeaderPath, err)
	}
	return &result, nil
}

// GetCertificateBridgeExits returns the bridge exits of the certificate at the
// given height. When height is nil it returns the bridge exits of the last sent
// certificate.
func (c *Client) GetCertificateBridgeExits(height *uint64) ([]*agglayertypes.BridgeExit, error) {
	var result []*agglayertypes.BridgeExit
	if err := c.get(certificateBridgeExitsPath, heightQuery(height), &result); err != nil {
		return nil, fmt.Errorf("error calling %s: %w", certificateBridgeExitsPath, err)
	}
	return result, nil
}

// get performs a GET request against path, decodes the JSON body into out and
// maps a non-2xx status to an error that carries the response error message.
func (c *Client) get(path string, query url.Values, out any) error {
	timeout := c.requestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	requestURL := c.baseURL + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("response body exceeds the %d-byte limit", maxResponseBytes)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, apiErrorMessage(body))
	}

	return json.Unmarshal(body, out)
}

// apiErrorMessage extracts the message of an {"error": ...} body, falling back
// to the raw body when it cannot be decoded.
func apiErrorMessage(body []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error != "" {
		return payload.Error
	}
	return strings.TrimSpace(string(body))
}

// heightQuery builds the optional height query parameter.
func heightQuery(height *uint64) url.Values {
	if height == nil {
		return nil
	}
	return url.Values{heightQueryParam: []string{strconv.FormatUint(*height, 10)}}
}
