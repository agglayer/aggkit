package bridgelooptester_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	bridgeserviceclient "github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// fastPoll/fastDeadline are short enough to keep the retry/deadline-exceeded tests quick without
// flaking on a loaded CI host.
const (
	fastPoll     = 5 * time.Millisecond
	fastDeadline = 100 * time.Millisecond
)

// newProxyClient builds a *bridgelooptester.ProxyClient against server.
func newProxyClient(t *testing.T, server *httptest.Server) *bridgelooptester.ProxyClient {
	t.Helper()

	client, err := bridgelooptester.NewProxyClient(bridgelooptester.Global{ProxyURL: server.URL})
	require.NoError(t, err)

	return client
}

func TestWaitL1InfoTreeIndex(t *testing.T) {
	t.Run("ready on first call", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/bridge/v1/l1-info-tree-index", r.URL.Path)
			require.Equal(t, "1", r.URL.Query().Get("network_id"))
			require.Equal(t, "42", r.URL.Query().Get("deposit_count"))
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(uint32(7))
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		index, err := p.WaitL1InfoTreeIndex(context.Background(), 1, 42, fastPoll, fastDeadline)

		require.NoError(t, err)
		require.Equal(t, uint32(7), index)
	})

	t.Run("404 then ready", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) <= 2 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(uint32(9))
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		index, err := p.WaitL1InfoTreeIndex(context.Background(), 1, 42, fastPoll, fastDeadline)

		require.NoError(t, err)
		require.Equal(t, uint32(9), index)
		require.GreaterOrEqual(t, calls.Load(), int32(3))
	})

	t.Run("503 then ready (transient, retried like ErrNotFound)", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) <= 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(uint32(3))
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		index, err := p.WaitL1InfoTreeIndex(context.Background(), 1, 42, fastPoll, fastDeadline)

		require.NoError(t, err)
		require.Equal(t, uint32(3), index)
	})

	t.Run("hard error aborts immediately, no retry", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		_, err := p.WaitL1InfoTreeIndex(context.Background(), 1, 42, fastPoll, fastDeadline)

		require.Error(t, err)
		require.NotErrorIs(t, err, bridgeserviceclient.ErrNotFound)
		require.NotErrorIs(t, err, bridgeserviceclient.ErrServiceUnavailable)
		var deadlineErr *bridgelooptester.DeadlineExceededError
		require.False(t, errors.As(err, &deadlineErr))
		require.Contains(t, err.Error(), "500")

		// A hard failure must not be retried: exactly one request should have been made, even
		// though the deadline would have allowed for several poll intervals.
		time.Sleep(2 * fastPoll)
		require.Equal(t, int32(1), calls.Load())
	})

	t.Run("deadline exceeded names the gate and concrete inputs", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		_, err := p.WaitL1InfoTreeIndex(context.Background(), 1, 42, fastPoll, fastDeadline)

		require.Error(t, err)
		require.ErrorIs(t, err, bridgelooptester.ErrDeadlineExceeded)
		require.ErrorIs(t, err, bridgeserviceclient.ErrNotFound)

		var deadlineErr *bridgelooptester.DeadlineExceededError
		require.True(t, errors.As(err, &deadlineErr))
		require.Equal(t, "l1-info-tree-index", deadlineErr.Gate)
		require.Contains(t, deadlineErr.Detail, "network_id=1")
		require.Contains(t, deadlineErr.Detail, "deposit_count=42")
		require.Equal(t, fastDeadline, deadlineErr.Deadline)
	})

	t.Run("ctx cancellation is reported distinctly from deadline exceeded", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		p := newProxyClient(t, server)
		start := time.Now()
		// deadline is deliberately much larger than the ctx timeout, so only ctx cancellation
		// can be what ends the wait.
		_, err := p.WaitL1InfoTreeIndex(ctx, 1, 42, fastPoll, 5*time.Second)
		elapsed := time.Since(start)

		require.Error(t, err)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		var deadlineErr *bridgelooptester.DeadlineExceededError
		require.False(t, errors.As(err, &deadlineErr), "ctx cancellation must not be reported as *DeadlineExceededError")
		require.Less(t, elapsed, 1*time.Second, "ctx cancellation must be honoured promptly, not after the full deadline")
	})

	t.Run("rejects a non-positive poll interval or deadline", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("must not perform any HTTP call for an invalid poll configuration")
		}))
		defer server.Close()

		p := newProxyClient(t, server)

		_, err := p.WaitL1InfoTreeIndex(context.Background(), 1, 42, 0, fastDeadline)
		require.Error(t, err)

		_, err = p.WaitL1InfoTreeIndex(context.Background(), 1, 42, fastPoll, 0)
		require.Error(t, err)
	})
}

func TestWaitInjectedLeaf(t *testing.T) {
	t.Run("no-op for destination 0", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("must not perform any HTTP call when destination is L1 (network 0)")
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		index, err := p.WaitInjectedLeaf(context.Background(), 0, 11, fastPoll, fastDeadline)

		require.NoError(t, err)
		require.Equal(t, uint32(11), index)
	})

	t.Run("returns the actual leaf index, which may exceed the requested one", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/bridge/v1/injected-l1-info-leaf", r.URL.Path)
			require.Equal(t, "2", r.URL.Query().Get("network_id"))
			require.Equal(t, "11", r.URL.Query().Get("leaf_index"))
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(bridgeservicetypes.L1InfoTreeLeafResponse{L1InfoTreeIndex: 15})
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		index, err := p.WaitInjectedLeaf(context.Background(), 2, 11, fastPoll, fastDeadline)

		require.NoError(t, err)
		require.Equal(t, uint32(15), index, "must surface the actual injected index, not the requested one")
	})

	t.Run("404 then ready", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) <= 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(bridgeservicetypes.L1InfoTreeLeafResponse{L1InfoTreeIndex: 11})
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		index, err := p.WaitInjectedLeaf(context.Background(), 2, 11, fastPoll, fastDeadline)

		require.NoError(t, err)
		require.Equal(t, uint32(11), index)
	})

	t.Run("deadline exceeded names the gate", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		_, err := p.WaitInjectedLeaf(context.Background(), 2, 11, fastPoll, fastDeadline)

		var deadlineErr *bridgelooptester.DeadlineExceededError
		require.True(t, errors.As(err, &deadlineErr))
		require.Equal(t, "injected-l1-info-leaf", deadlineErr.Gate)
		require.Contains(t, deadlineErr.Detail, "network_id=2")
		require.Contains(t, deadlineErr.Detail, "leaf_index=11")
	})
}

func TestWaitClaimProof(t *testing.T) {
	t.Run("ready on first call", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/bridge/v1/claim-proof", r.URL.Path)
			require.Equal(t, "1", r.URL.Query().Get("network_id"))
			require.Equal(t, "15", r.URL.Query().Get("leaf_index"))
			require.Equal(t, "42", r.URL.Query().Get("deposit_count"))
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(bridgeservicetypes.ClaimProof{})
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		proof, err := p.WaitClaimProof(context.Background(), 1, 15, 42, fastPoll, fastDeadline)

		require.NoError(t, err)
		require.NotNil(t, proof)
	})

	t.Run("404 then ready", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) <= 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(bridgeservicetypes.ClaimProof{})
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		proof, err := p.WaitClaimProof(context.Background(), 1, 15, 42, fastPoll, fastDeadline)

		require.NoError(t, err)
		require.NotNil(t, proof)
	})

	t.Run("hard error aborts immediately", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"missing mandatory query parameter"}`))
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		_, err := p.WaitClaimProof(context.Background(), 1, 15, 42, fastPoll, fastDeadline)

		require.Error(t, err)
		require.NotErrorIs(t, err, bridgeserviceclient.ErrNotFound)
		require.Contains(t, err.Error(), "400")
	})
}

func TestWaitClaimed(t *testing.T) {
	globalIndex := big.NewInt(123456)

	t.Run("empty result then a matching claim", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/bridge/v1/claims", r.URL.Path)
			require.Equal(t, "3", r.URL.Query().Get("network_id"))
			require.Equal(t, globalIndex.String(), r.URL.Query().Get("global_index"))

			w.WriteHeader(http.StatusOK)
			if calls.Add(1) <= 1 {
				_ = json.NewEncoder(w).Encode(bridgeservicetypes.ClaimsResult{})
				return
			}
			_ = json.NewEncoder(w).Encode(bridgeservicetypes.ClaimsResult{
				Claims: []*bridgeservicetypes.ClaimResponse{{GlobalIndex: bridgeservicetypes.BigIntString(globalIndex.String())}},
				Count:  1,
			})
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		claim, err := p.WaitClaimed(context.Background(), 3, globalIndex, fastPoll, fastDeadline)

		require.NoError(t, err)
		require.NotNil(t, claim)
		require.Equal(t, globalIndex, claim.GlobalIndex.ToBigInt())
	})

	t.Run("deadline exceeded while claims stay empty", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(bridgeservicetypes.ClaimsResult{})
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		_, err := p.WaitClaimed(context.Background(), 3, globalIndex, fastPoll, fastDeadline)

		var deadlineErr *bridgelooptester.DeadlineExceededError
		require.True(t, errors.As(err, &deadlineErr))
		require.Equal(t, "claimed", deadlineErr.Gate)
		require.ErrorIs(t, err, bridgeserviceclient.ErrNotFound)
	})

	t.Run("requires a non-nil globalIndex", func(t *testing.T) {
		p := newProxyClient(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("must not perform any HTTP call without a globalIndex")
		})))
		_, err := p.WaitClaimed(context.Background(), 3, nil, fastPoll, fastDeadline)
		require.Error(t, err)
	})
}

func TestTrackBridge(t *testing.T) {
	txHash := common.HexToHash("0xabc0000000000000000000000000000000000000000000000000000000000f")

	t.Run("registers/looks up a bridge", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, fmt.Sprintf("/tracker/v1/network/1/tx/%s", txHash.Hex()), r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w,
				`{"tracking_status":"Running","claim_status":"pending","network_id":1,"tx_hash":%q,`+
					`"bridge_status":null,"step_index":null,"all_steps":null,"error":null}`, txHash.Hex())
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		data, err := p.TrackBridge(context.Background(), 1, txHash)

		require.NoError(t, err)
		require.Equal(t, "Running", data.TrackingStatus)
		require.Equal(t, "pending", data.ClaimStatus)
		require.Equal(t, txHash, data.TxHash)
	})

	t.Run("503 (registry at capacity) is reported as ErrServiceUnavailable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"registry at capacity"}`))
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		_, err := p.TrackBridge(context.Background(), 1, txHash)

		require.ErrorIs(t, err, bridgeserviceclient.ErrServiceUnavailable)
	})

	t.Run("400 is a hard error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid tx_hash parameter"}`))
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		_, err := p.TrackBridge(context.Background(), 1, txHash)

		require.Error(t, err)
		require.NotErrorIs(t, err, bridgeserviceclient.ErrServiceUnavailable)
		require.NotErrorIs(t, err, bridgeserviceclient.ErrNotFound)
	})
}

func TestBridgeByDepositCount(t *testing.T) {
	t.Run("successful request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/bridge/v1/bridge-by-deposit-count", r.URL.Path)
			require.Equal(t, "1", r.URL.Query().Get("network_id"))
			require.Equal(t, "5", r.URL.Query().Get("deposit_count"))
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(bridgeservicetypes.BridgeResponse{DepositCount: 5, OriginNetwork: 1})
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		bridge, err := p.BridgeByDepositCount(context.Background(), 1, 5)

		require.NoError(t, err)
		require.Equal(t, uint32(5), bridge.DepositCount)
	})

	t.Run("404 is plain ErrNotFound, with no retry", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not indexed"}`))
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		_, err := p.BridgeByDepositCount(context.Background(), 1, 9)

		require.ErrorIs(t, err, bridgeserviceclient.ErrNotFound)
		require.Equal(t, int32(1), calls.Load(), "BridgeByDepositCount is a single call, not a Wait* retry loop")
	})
}

func TestProxyClientHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/tracker/v1/health", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","api_revision":2,"instance_id":"i-1","config_sha1":"abc"}`))
	}))
	defer server.Close()

	p := newProxyClient(t, server)
	resp, err := p.Health(context.Background())

	require.NoError(t, err)
	require.Equal(t, "ok", resp.Status)
	require.Equal(t, 2, resp.APIRevision)
}

func TestBridgeAddresses(t *testing.T) {
	t.Run("successful request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/bridge/v1/config", r.URL.Path)
			require.Equal(t, "1", r.URL.Query().Get("network_id"))
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(bridgeservicetypes.PublicConfigResponse{NetworkID: 1})
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		cfg, err := p.BridgeAddresses(context.Background(), 1)

		require.NoError(t, err)
		require.Equal(t, uint32(1), cfg.NetworkID)
	})

	t.Run("404 for network 0 is reported as ErrL1BridgeServiceUnavailable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no bridge service resolved for network 0"}`))
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		_, err := p.BridgeAddresses(context.Background(), 0)

		require.ErrorIs(t, err, bridgelooptester.ErrL1BridgeServiceUnavailable)
		require.ErrorIs(t, err, bridgeserviceclient.ErrNotFound)
	})

	t.Run("404 for a non-zero network is plain ErrNotFound", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no bridge service resolved for network 5"}`))
		}))
		defer server.Close()

		p := newProxyClient(t, server)
		_, err := p.BridgeAddresses(context.Background(), 5)

		require.ErrorIs(t, err, bridgeserviceclient.ErrNotFound)
		require.NotErrorIs(t, err, bridgelooptester.ErrL1BridgeServiceUnavailable)
	})
}

func TestNewProxyClient(t *testing.T) {
	t.Run("requires a ProxyURL", func(t *testing.T) {
		_, err := bridgelooptester.NewProxyClient(bridgelooptester.Global{})
		require.Error(t, err)
	})

	t.Run("WithHTTPTimeout bounds every HTTP round trip", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}))
		defer server.Close()

		client, err := bridgelooptester.NewProxyClient(bridgelooptester.Global{ProxyURL: server.URL},
			bridgelooptester.WithHTTPTimeout(10*time.Millisecond))
		require.NoError(t, err)

		_, err = client.Health(context.Background())
		require.Error(t, err, "a round trip slower than WithHTTPTimeout must fail rather than hang")
	})
}
