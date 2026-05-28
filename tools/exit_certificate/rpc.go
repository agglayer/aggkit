package exit_certificate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/agglayer/aggkit/log"
)

const (
	defaultRetries      = 3
	maxBackoffMs        = 10000
	baseBackoffMs       = 1000
	backoffExponent     = 2
	idleConnTimeoutSec  = 90
	httpTimeoutSec      = 120
	maxIdleConnsPerHost = 100
)

// httpClient keeps a large per-host idle connection pool to avoid throttling
// parallel RPC traffic on Go's default MaxIdleConnsPerHost=2.
var httpClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        0,
		MaxIdleConnsPerHost: maxIdleConnsPerHost,
		MaxConnsPerHost:     0,
		IdleConnTimeout:     idleConnTimeoutSec * time.Second,
	},
	Timeout: httpTimeoutSec * time.Second,
}

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
	ID      int    `json:"id"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error"`
	ID      int             `json:"id"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

// RPCExecutionError is returned by singleRPC when the node returns an RPC-level error.
// Data holds the raw hex-encoded revert payload (e.g. ABI-encoded custom error).
type RPCExecutionError struct {
	Code    int
	Message string
	Data    string
}

func (e *RPCExecutionError) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("RPC error: %s (data: %s)", e.Message, e.Data)
	}
	return fmt.Sprintf("RPC error: %s", e.Message)
}

// RPCCall represents a single JSON-RPC method call.
type RPCCall struct {
	Method string
	Params []any
}

// batchRPC sends a batch of JSON-RPC calls in a single HTTP POST.
// Returns ordered results matching the input calls slice. Per-item RPC errors are retried
// up to retries times. Returns an error if any call still fails after all retries.
func batchRPC(ctx context.Context, url string, calls []RPCCall, retries int) ([]json.RawMessage, error) {
	if retries <= 0 {
		retries = defaultRetries
	}

	results := make([]json.RawMessage, len(calls))
	pendingIdxs := make([]int, len(calls))
	for i := range pendingIdxs {
		pendingIdxs[i] = i
	}

	for attempt := 1; attempt <= retries && len(pendingIdxs) > 0; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt > 1 {
			log.Warnf("batchRPC: retrying %d/%d failed calls (attempt %d/%d)",
				len(pendingIdxs), len(calls), attempt, retries)
			sleepWithBackoff(ctx, attempt-1)
		}

		subCalls := make([]RPCCall, len(pendingIdxs))
		for i, origIdx := range pendingIdxs {
			subCalls[i] = calls[origIdx]
		}

		requests := make([]jsonRPCRequest, len(subCalls))
		for i, c := range subCalls {
			requests[i] = jsonRPCRequest{JSONRPC: "2.0", Method: c.Method, Params: c.Params, ID: i + 1}
		}

		body, err := json.Marshal(requests)
		if err != nil {
			return nil, fmt.Errorf("marshal batch request: %w", err)
		}

		responses, err := doRPCWithRetry(ctx, url, body, 1, "")
		if err != nil {
			if attempt == retries {
				return nil, err
			}
			log.Warnf("batchRPC attempt %d/%d HTTP error: %v", attempt, retries, err)
			continue
		}

		// Whole-batch rejection: node returned a single error object for multiple pending calls.
		if len(responses) == 1 && responses[0].Error != nil && len(subCalls) > 1 {
			e := responses[0].Error
			if attempt == retries {
				return nil, &RPCExecutionError{Code: e.Code, Message: e.Message, Data: e.Data}
			}
			log.Warnf("batchRPC attempt %d/%d: node rejected batch of %d calls [%d] %s — retrying",
				attempt, retries, len(subCalls), e.Code, e.Message)
			continue
		}

		if len(responses) != len(subCalls) {
			return nil, fmt.Errorf("RPC response count %d does not match request count %d",
				len(responses), len(subCalls))
		}

		var nextPending []int
		for _, r := range responses {
			localIdx := r.ID - 1
			if localIdx < 0 || localIdx >= len(pendingIdxs) {
				continue
			}
			origIdx := pendingIdxs[localIdx]
			if r.Error != nil {
				log.Warnf("RPC error for %s id=%d (attempt %d/%d): [%d] %s",
					calls[origIdx].Method, origIdx+1, attempt, retries, r.Error.Code, r.Error.Message)
				nextPending = append(nextPending, origIdx)
				continue
			}
			results[origIdx] = r.Result
		}
		pendingIdxs = nextPending
	}

	if len(pendingIdxs) > 0 {
		return nil, fmt.Errorf("batchRPC: %d/%d calls still failing after %d attempts",
			len(pendingIdxs), len(calls), retries)
	}

	return results, nil
}

// singleRPC sends one JSON-RPC call. Uses the same HTTP transport as batchRPC
// but propagates RPC-level errors as Go errors.
func singleRPC(ctx context.Context, url, method string, params []any, retries int) (json.RawMessage, error) {
	return singleRPCAuth(ctx, url, method, params, retries, "")
}

// singleRPCAuth is like singleRPC but adds an Authorization: Bearer header when bearerToken is non-empty.
// Use this for endpoints protected by Google Cloud IAP or similar token-based auth.
func singleRPCAuth(
	ctx context.Context, url, method string, params []any, retries int, bearerToken string,
) (json.RawMessage, error) {
	if retries <= 0 {
		retries = defaultRetries
	}

	body, err := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", Method: method, Params: params, ID: 1})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	responses, err := doRPCWithRetry(ctx, url, body, retries, bearerToken)
	if err != nil {
		return nil, err
	}
	if len(responses) == 0 {
		return nil, fmt.Errorf("RPC call %s returned empty response", method)
	}
	if responses[0].Error != nil {
		rpcErr := responses[0].Error
		return nil, &RPCExecutionError{Code: rpcErr.Code, Message: rpcErr.Message, Data: rpcErr.Data}
	}
	return responses[0].Result, nil
}

func doRPCAttempt(ctx context.Context, url string, body []byte, bearerToken string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// httpGetJSON performs a GET request to the given URL and returns the response body.
func httpGetJSON(ctx context.Context, reqURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create GET request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func parseRPCResponse(data []byte) ([]jsonRPCResponse, error) {
	var responses []jsonRPCResponse
	if err := json.Unmarshal(data, &responses); err != nil {
		var single jsonRPCResponse
		if err2 := json.Unmarshal(data, &single); err2 == nil {
			return []jsonRPCResponse{single}, nil
		}
		return nil, fmt.Errorf("parse RPC response: %w", err)
	}
	return responses, nil
}

// maskRPCURL returns only scheme://host to avoid exposing API keys in path segments.
func maskRPCURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	return u.Scheme + "://" + u.Host
}

// doRPCWithRetry handles the HTTP POST + retry loop.
func doRPCWithRetry(
	ctx context.Context, rpcURL string, body []byte, retries int, bearerToken string,
) ([]jsonRPCResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		respBody, err := doRPCAttempt(ctx, rpcURL, body, bearerToken)
		if err != nil {
			lastErr = err
			if attempt < retries {
				sleepWithBackoff(ctx, attempt)
				continue
			}
			return nil, fmt.Errorf("RPC failed after %d attempts on %s: %w", retries, maskRPCURL(rpcURL), lastErr)
		}
		return parseRPCResponse(respBody)
	}
	return nil, fmt.Errorf("RPC failed after %d attempts on %s", retries, maskRPCURL(rpcURL))
}

func sleepWithBackoff(ctx context.Context, attempt int) {
	ms := math.Min(
		float64(baseBackoffMs*int(math.Pow(backoffExponent, float64(attempt)))),
		float64(maxBackoffMs),
	)
	select {
	case <-time.After(time.Duration(ms) * time.Millisecond):
	case <-ctx.Done():
	}
}

// indexedBatchResult pairs batch RPC results with their offset in the global slice.
type indexedBatchResult struct {
	offset  int
	results []json.RawMessage
}

// concurrentBatchRPC splits calls into batchSize chunks and processes them
// through a worker pool. Workers immediately pick up the next batch when done.
func concurrentBatchRPC(
	ctx context.Context, url string, allCalls []RPCCall,
	batchSize, concurrency int, label string,
) ([]json.RawMessage, error) {
	if len(allCalls) == 0 {
		return nil, nil
	}

	type batchJob struct {
		offset int
		calls  []RPCCall
	}

	var jobs []batchJob
	for i := 0; i < len(allCalls); i += batchSize {
		end := min(i+batchSize, len(allCalls))
		jobs = append(jobs, batchJob{offset: i, calls: allCalls[i:end]})
	}

	allResults := make([]json.RawMessage, len(allCalls))

	err := runWorkerPool(
		ctx, jobs, concurrency,
		func(j batchJob) (indexedBatchResult, error) {
			res, err := batchRPC(ctx, url, j.calls, defaultRetries)
			return indexedBatchResult{offset: j.offset, results: res}, err
		},
		func(ir indexedBatchResult) {
			copy(allResults[ir.offset:ir.offset+len(ir.results)], ir.results)
		},
		label,
	)
	if err != nil {
		return nil, err
	}

	return allResults, nil
}
