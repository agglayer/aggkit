// Package l1 is a minimal JSON-RPC client of L1. It only does the few queries the validator needs, which keeps the
// code that talks to the network small and easy to audit.
package l1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	// maxResponseBytes limits how much of a response of the RPC is read
	maxResponseBytes = 1 << 20

	// globalExitRootMapSignature is the function of AgglayerGER that returns the hash of the block previous
	// to the one that inserted a GER: mapping(bytes32 => uint256) public globalExitRootMap
	globalExitRootMapSignature = "globalExitRootMap(bytes32)"

	wordLength     = 32
	selectorLength = 4
)

// ErrBlockNotFound is returned when the node doesn't know the block
var ErrBlockNotFound = errors.New("block not found")

// Block is the part of a block header the validator uses
type Block struct {
	Number     uint64
	Hash       common.Hash
	ParentHash common.Hash
}

// RPCError is an error returned by the node in the JSON-RPC response
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// Client is the JSON-RPC client of L1
type Client struct {
	url        string
	httpClient *http.Client
	nextID     atomic.Uint64
}

// NewClient creates a client of the JSON-RPC endpoint. timeout limits each request
func NewClient(url string, timeout time.Duration) *Client {
	return &Client{
		url: url,
		httpClient: &http.Client{
			Timeout: timeout,
			// The endpoint must answer by itself
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// call executes a JSON-RPC method and returns its result. The URL is not included in the errors:
// it can have a token
func (c *Client) call(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: c.nextID.Add(1), Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("%s: failed to encode the request: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: failed to create the request", method)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// The error of the HTTP client includes the URL: keep only the cause
		return nil, fmt.Errorf("%s: request failed: %w", method, unwrapURLError(err))
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%s: failed to read the response: %w", method, unwrapURLError(err))
	}
	if len(payload) > maxResponseBytes {
		return nil, fmt.Errorf("%s: response bigger than %d bytes", method, maxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected HTTP status %d", method, resp.StatusCode)
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(payload, &rpcResp); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON-RPC response: %w", method, err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("%s: %w", method, &RPCError{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message})
	}
	return rpcResp.Result, nil
}

// unwrapURLError returns the cause of an error of the HTTP client, without the URL
func unwrapURLError(err error) error {
	var urlErr interface{ Unwrap() error }
	if errors.As(err, &urlErr) {
		if cause := urlErr.Unwrap(); cause != nil {
			return cause
		}
	}
	return err
}

// ChainID returns the chain id of the endpoint
func (c *Client) ChainID(ctx context.Context) (uint64, error) {
	result, err := c.call(ctx, "eth_chainId")
	if err != nil {
		return 0, err
	}
	var chainID hexutil.Uint64
	if err := json.Unmarshal(result, &chainID); err != nil {
		return 0, fmt.Errorf("eth_chainId: invalid result: %w", err)
	}
	return uint64(chainID), nil
}

// CodeAt returns the code of a contract in the latest block. It is empty for an account that is not a contract
func (c *Client) CodeAt(ctx context.Context, addr common.Address) ([]byte, error) {
	result, err := c.call(ctx, "eth_getCode", addr, "latest")
	if err != nil {
		return nil, err
	}
	var code hexutil.Bytes
	if err := json.Unmarshal(result, &code); err != nil {
		return nil, fmt.Errorf("eth_getCode: invalid result: %w", err)
	}
	return code, nil
}

// GlobalExitRootMap reads AgglayerGER.globalExitRootMap(ger) in the latest block. The contract stores there
// the hash of the block previous to the one that inserted the GER, and zero if it was never inserted.
func (c *Client) GlobalExitRootMap(
	ctx context.Context,
	agglayerGER common.Address,
	ger common.Hash,
) (common.Hash, error) {
	data := make([]byte, 0, selectorLength+common.HashLength)
	data = append(data, crypto.Keccak256([]byte(globalExitRootMapSignature))[:selectorLength]...)
	data = append(data, ger.Bytes()...)
	result, err := c.call(ctx, "eth_call", map[string]any{
		"to":   agglayerGER,
		"data": hexutil.Bytes(data),
	}, "latest")
	if err != nil {
		return common.Hash{}, err
	}
	var out hexutil.Bytes
	if err := json.Unmarshal(result, &out); err != nil {
		return common.Hash{}, fmt.Errorf("eth_call: invalid result: %w", err)
	}
	if len(out) != wordLength {
		return common.Hash{}, fmt.Errorf("globalExitRootMap returned %d bytes, expected %d "+
			"(is %s the AgglayerGER contract?)", len(out), wordLength, agglayerGER)
	}
	return common.BytesToHash(out), nil
}

type rpcBlock struct {
	Number     *hexutil.Uint64 `json:"number"`
	Hash       *common.Hash    `json:"hash"`
	ParentHash *common.Hash    `json:"parentHash"`
}

func decodeBlock(method string, result json.RawMessage) (*Block, error) {
	if len(result) == 0 || string(result) == "null" {
		return nil, ErrBlockNotFound
	}
	var raw rpcBlock
	if err := json.Unmarshal(result, &raw); err != nil {
		return nil, fmt.Errorf("%s: invalid block: %w", method, err)
	}
	// A pending block has no number or hash
	if raw.Number == nil || raw.Hash == nil || raw.ParentHash == nil {
		return nil, fmt.Errorf("%s: the block has no number or hash (a pending block?)", method)
	}
	return &Block{Number: uint64(*raw.Number), Hash: *raw.Hash, ParentHash: *raw.ParentHash}, nil
}

// BlockByHash returns the block with the hash, or ErrBlockNotFound. The node can return a block that is not in
// the canonical chain (an orphan): use BlockByNumber to know if it is.
func (c *Client) BlockByHash(ctx context.Context, hash common.Hash) (*Block, error) {
	result, err := c.call(ctx, "eth_getBlockByHash", hash, false)
	if err != nil {
		return nil, err
	}
	return decodeBlock("eth_getBlockByHash", result)
}

// BlockByNumber returns the block of the canonical chain with the number, or ErrBlockNotFound
func (c *Client) BlockByNumber(ctx context.Context, number uint64) (*Block, error) {
	result, err := c.call(ctx, "eth_getBlockByNumber", hexutil.Uint64(number), false)
	if err != nil {
		return nil, err
	}
	return decodeBlock("eth_getBlockByNumber", result)
}

// BlockNumberByTag returns the number of the block of a tag: latest, safe or finalized
func (c *Client) BlockNumberByTag(ctx context.Context, tag string) (uint64, error) {
	result, err := c.call(ctx, "eth_getBlockByNumber", tag, false)
	if err != nil {
		return 0, err
	}
	block, err := decodeBlock("eth_getBlockByNumber", result)
	if err != nil {
		return 0, fmt.Errorf("block of the tag %q: %w", tag, err)
	}
	return block.Number, nil
}
