package l1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

const secretToken = "s3cr3t-t0k3n"

var (
	gerAddr  = common.HexToAddress("0x1111111111111111111111111111111111111111")
	testGER  = common.HexToHash("0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	blockHsh = common.HexToHash("0xaaaa000000000000000000000000000000000000000000000000000000000001")
	parentHs = common.HexToHash("0xbbbb000000000000000000000000000000000000000000000000000000000002")
)

// rpcServer is a JSON-RPC server that answers each method with a function
type rpcServer struct {
	*httptest.Server
}

type rpcCall struct {
	Method string
	Params []json.RawMessage
}

// newRPCServer starts a server. The handler returns the result of the method, or an error to answer with
func newRPCServer(t *testing.T, handler func(call rpcCall) (result any, rpcErr *RPCError)) *rpcServer {
	t.Helper()
	srv := &rpcServer{}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req struct {
			JSONRPC string            `json:"jsonrpc"`
			ID      uint64            `json:"id"`
			Method  string            `json:"method"`
			Params  []json.RawMessage `json:"params"`
		}
		require.NoError(t, json.Unmarshal(body, &req))
		require.Equal(t, "2.0", req.JSONRPC)
		result, rpcErr := handler(rpcCall{Method: req.Method, Params: req.Params})
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			response["error"] = map[string]any{"code": rpcErr.Code, "message": rpcErr.Message}
		} else {
			response["result"] = result
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// client returns a client of the server, whose URL has a token in the path as the ones of the providers
func (s *rpcServer) client() *Client {
	return NewClient(s.URL+"/"+secretToken, 2*time.Second)
}

func blockJSON(number string, hash, parent common.Hash) map[string]any {
	return map[string]any{"number": number, "hash": hash.Hex(), "parentHash": parent.Hex(), "timestamp": "0x1"}
}

func TestClient_ChainID(t *testing.T) {
	srv := newRPCServer(t, func(call rpcCall) (any, *RPCError) {
		require.Equal(t, "eth_chainId", call.Method)
		require.Empty(t, call.Params)
		return "0xaa36a7", nil
	})
	chainID, err := srv.client().ChainID(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(11155111), chainID)
}

func TestClient_CodeAt(t *testing.T) {
	srv := newRPCServer(t, func(call rpcCall) (any, *RPCError) {
		require.Equal(t, "eth_getCode", call.Method)
		require.JSONEq(t, `"`+strings.ToLower(gerAddr.Hex())+`"`, string(call.Params[0]))
		require.JSONEq(t, `"latest"`, string(call.Params[1]))
		return "0x6080", nil
	})
	code, err := srv.client().CodeAt(context.Background(), gerAddr)
	require.NoError(t, err)
	require.Equal(t, []byte{0x60, 0x80}, code)

	srv = newRPCServer(t, func(rpcCall) (any, *RPCError) { return "0x", nil })
	code, err = srv.client().CodeAt(context.Background(), gerAddr)
	require.NoError(t, err)
	require.Empty(t, code, "an account without code")
}

func TestClient_GlobalExitRootMap(t *testing.T) {
	selector := crypto.Keccak256([]byte("globalExitRootMap(bytes32)"))[:4]
	srv := newRPCServer(t, func(call rpcCall) (any, *RPCError) {
		require.Equal(t, "eth_call", call.Method)
		var msg struct {
			To   common.Address `json:"to"`
			Data string         `json:"data"`
		}
		require.NoError(t, json.Unmarshal(call.Params[0], &msg))
		require.Equal(t, gerAddr, msg.To)
		// selector + the GER as the only argument
		require.Equal(t, "0x"+common.Bytes2Hex(selector)+common.Bytes2Hex(testGER.Bytes()), msg.Data)
		require.JSONEq(t, `"latest"`, string(call.Params[1]))
		return blockHsh.Hex(), nil
	})

	got, err := srv.client().GlobalExitRootMap(context.Background(), gerAddr, testGER)
	require.NoError(t, err)
	require.Equal(t, blockHsh, got)
}

func TestClient_GlobalExitRootMap_Unknown(t *testing.T) {
	srv := newRPCServer(t, func(rpcCall) (any, *RPCError) { return common.Hash{}.Hex(), nil })
	got, err := srv.client().GlobalExitRootMap(context.Background(), gerAddr, testGER)
	require.NoError(t, err)
	require.Equal(t, common.Hash{}, got, "zero means the GER is not in the contract")
}

func TestClient_GlobalExitRootMap_NotTheContract(t *testing.T) {
	for _, result := range []string{"0x", "0x1234", "0x" + strings.Repeat("00", 33)} {
		srv := newRPCServer(t, func(rpcCall) (any, *RPCError) { return result, nil })
		_, err := srv.client().GlobalExitRootMap(context.Background(), gerAddr, testGER)
		require.ErrorContains(t, err, "is "+gerAddr.Hex()+" the AgglayerGER contract?", result)
	}
}

func TestClient_Blocks(t *testing.T) {
	t.Run("by hash", func(t *testing.T) {
		srv := newRPCServer(t, func(call rpcCall) (any, *RPCError) {
			require.Equal(t, "eth_getBlockByHash", call.Method)
			require.JSONEq(t, `"`+blockHsh.Hex()+`"`, string(call.Params[0]))
			require.JSONEq(t, `false`, string(call.Params[1]), "only the header is needed")
			return blockJSON("0x10", blockHsh, parentHs), nil
		})
		block, err := srv.client().BlockByHash(context.Background(), blockHsh)
		require.NoError(t, err)
		require.Equal(t, &Block{Number: 16, Hash: blockHsh, ParentHash: parentHs}, block)
	})

	t.Run("by number", func(t *testing.T) {
		srv := newRPCServer(t, func(call rpcCall) (any, *RPCError) {
			require.Equal(t, "eth_getBlockByNumber", call.Method)
			require.JSONEq(t, `"0x10"`, string(call.Params[0]))
			require.JSONEq(t, `false`, string(call.Params[1]))
			return blockJSON("0x10", blockHsh, parentHs), nil
		})
		block, err := srv.client().BlockByNumber(context.Background(), 16)
		require.NoError(t, err)
		require.Equal(t, uint64(16), block.Number)
	})

	t.Run("number of a tag", func(t *testing.T) {
		for _, tag := range []string{"latest", "safe", "finalized"} {
			srv := newRPCServer(t, func(call rpcCall) (any, *RPCError) {
				require.Equal(t, "eth_getBlockByNumber", call.Method)
				require.JSONEq(t, `"`+tag+`"`, string(call.Params[0]))
				return blockJSON("0x2a", blockHsh, parentHs), nil
			})
			number, err := srv.client().BlockNumberByTag(context.Background(), tag)
			require.NoError(t, err)
			require.Equal(t, uint64(42), number, tag)
		}
	})

	t.Run("a block the node doesn't know", func(t *testing.T) {
		srv := newRPCServer(t, func(rpcCall) (any, *RPCError) { return nil, nil })
		_, err := srv.client().BlockByHash(context.Background(), blockHsh)
		require.ErrorIs(t, err, ErrBlockNotFound)
		_, err = srv.client().BlockByNumber(context.Background(), 1)
		require.ErrorIs(t, err, ErrBlockNotFound)
		_, err = srv.client().BlockNumberByTag(context.Background(), "finalized")
		require.ErrorIs(t, err, ErrBlockNotFound)
		require.ErrorContains(t, err, `block of the tag "finalized"`)
	})

	t.Run("a pending block has no number", func(t *testing.T) {
		srv := newRPCServer(t, func(rpcCall) (any, *RPCError) {
			return map[string]any{"number": nil, "hash": nil, "parentHash": parentHs.Hex()}, nil
		})
		_, err := srv.client().BlockByNumber(context.Background(), 1)
		require.ErrorContains(t, err, "pending block")
	})

	t.Run("an invalid block", func(t *testing.T) {
		srv := newRPCServer(t, func(rpcCall) (any, *RPCError) {
			return map[string]any{"number": "not hex", "hash": blockHsh.Hex(), "parentHash": parentHs.Hex()}, nil
		})
		_, err := srv.client().BlockByHash(context.Background(), blockHsh)
		require.ErrorContains(t, err, "invalid block")
	})
}

func TestClient_RPCError(t *testing.T) {
	srv := newRPCServer(t, func(rpcCall) (any, *RPCError) {
		return nil, &RPCError{Code: -32000, Message: "finalized block not found"}
	})

	_, err := srv.client().BlockNumberByTag(context.Background(), "finalized")
	var rpcErr *RPCError
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, -32000, rpcErr.Code)
	require.Equal(t, "finalized block not found", rpcErr.Message)
	require.ErrorContains(t, err, "eth_getBlockByNumber")
}

func TestClient_Failures(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{
			name: "HTTP error status",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
			},
			wantErr: "unexpected HTTP status 429",
		},
		{
			name: "response that is not JSON",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("<html>gateway</html>"))
			},
			wantErr: "invalid JSON-RPC response",
		},
		{
			name: "empty response",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			wantErr: "invalid JSON-RPC response",
		},
		{
			name: "response too big",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(strings.Repeat("a", maxResponseBytes+1)))
			},
			wantErr: "response bigger than",
		},
		{
			name: "redirect",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "http://example.invalid/", http.StatusTemporaryRedirect)
			},
			wantErr: "unexpected HTTP status 307",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			t.Cleanup(server.Close)
			client := NewClient(server.URL+"/"+secretToken, 2*time.Second)

			_, err := client.ChainID(context.Background())
			require.ErrorContains(t, err, tc.wantErr)
			require.NotContains(t, err.Error(), secretToken, "the error must not leak the token of the URL")
		})
	}
}

func TestClient_ErrorsDoNotLeakTheURL(t *testing.T) {
	t.Run("connection refused", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		url := server.URL
		server.Close()

		_, err := NewClient(url+"/"+secretToken, time.Second).ChainID(context.Background())
		require.Error(t, err)
		require.NotContains(t, err.Error(), secretToken)
		require.NotContains(t, err.Error(), url)
	})

	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			select {
			case <-release:
			case <-time.After(5 * time.Second):
			}
		}))
		t.Cleanup(func() { close(release); server.Close() })

		_, err := NewClient(server.URL+"/"+secretToken, 50*time.Millisecond).ChainID(context.Background())
		require.Error(t, err)
		require.NotContains(t, err.Error(), secretToken)
	})

	t.Run("cancelled context", func(t *testing.T) {
		srv := newRPCServer(t, func(rpcCall) (any, *RPCError) { return "0x1", nil })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := srv.client().ChainID(ctx)
		require.ErrorIs(t, err, context.Canceled)
		require.NotContains(t, err.Error(), secretToken)
	})
}

func TestClient_RequestIDsIncrease(t *testing.T) {
	var ids []uint64
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID uint64 `json:"id"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		mu.Lock()
		ids = append(ids, req.ID)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, time.Second)
	for range 3 {
		_, err := client.ChainID(context.Background())
		require.NoError(t, err)
	}
	require.Equal(t, []uint64{1, 2, 3}, ids)
}
