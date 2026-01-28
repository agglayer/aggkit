package client

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Run("creates client with default timeout", func(t *testing.T) {
		client := New(Config{
			BaseURL: "http://localhost:8080",
		})

		require.NotNil(t, client)
		require.Equal(t, "http://localhost:8080", client.baseURL)
		require.Equal(t, 30*time.Second, client.httpClient.Timeout)
	})

	t.Run("creates client with custom timeout", func(t *testing.T) {
		client := New(Config{
			BaseURL: "http://localhost:8080",
			Timeout: 10 * time.Second,
		})

		require.NotNil(t, client)
		require.Equal(t, 10*time.Second, client.httpClient.Timeout)
	})

	t.Run("trims trailing slash from base URL", func(t *testing.T) {
		client := New(Config{
			BaseURL: "http://localhost:8080/",
		})

		require.Equal(t, "http://localhost:8080", client.baseURL)
	})
}

func TestHealthCheck(t *testing.T) {
	t.Run("successful health check", func(t *testing.T) {
		expectedResp := &types.HealthCheckResponse{
			Status:  "OK",
			Time:    time.Now(),
			Version: "1.0.0",
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/", r.URL.Path)

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.HealthCheck(context.Background())

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, expectedResp.Status, resp.Status)
		require.Equal(t, expectedResp.Version, resp.Version)
	})

	t.Run("handles server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.HealthCheck(context.Background())

		require.Error(t, err)
		require.Nil(t, resp)
		require.Contains(t, err.Error(), "500")
	})

	t.Run("handles context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		resp, err := client.HealthCheck(ctx)

		require.Error(t, err)
		require.Nil(t, resp)
	})
}

func TestGetBridges(t *testing.T) {
	t.Run("successful request with minimal params", func(t *testing.T) {
		expectedResp := &types.BridgesResult{
			Bridges: []*types.BridgeResponse{
				{
					BlockNum:     100,
					DepositCount: 1,
				},
			},
			Count: 1,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/bridge/v1/bridges", r.URL.Path)
			require.Equal(t, "1", r.URL.Query().Get("network_id"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetBridges(context.Background(), GetBridgesParams{
			NetworkID: 1,
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 1, resp.Count)
		require.Len(t, resp.Bridges, 1)
	})

	t.Run("successful request with all params", func(t *testing.T) {
		pageNum := uint32(2)
		pageSize := uint32(50)
		depositCount := uint64(10)
		fromAddr := "0x1234567890123456789012345678901234567890"

		expectedResp := &types.BridgesResult{
			Bridges: []*types.BridgeResponse{},
			Count:   0,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "1", r.URL.Query().Get("network_id"))
			require.Equal(t, "2", r.URL.Query().Get("page_number"))
			require.Equal(t, "50", r.URL.Query().Get("page_size"))
			require.Equal(t, "10", r.URL.Query().Get("deposit_count"))
			require.Equal(t, fromAddr, r.URL.Query().Get("from_address"))
			require.Equal(t, "2,3", r.URL.Query().Get("network_ids"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetBridges(context.Background(), GetBridgesParams{
			NetworkID:    1,
			PageNumber:   &pageNum,
			PageSize:     &pageSize,
			DepositCount: &depositCount,
			FromAddress:  &fromAddr,
			NetworkIDs:   []uint32{2, 3},
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("handles bad request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("invalid network_id"))
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetBridges(context.Background(), GetBridgesParams{
			NetworkID: 0,
		})

		require.Error(t, err)
		require.Nil(t, resp)
		require.Contains(t, err.Error(), "400")
	})
}

func TestGetClaims(t *testing.T) {
	t.Run("successful request with minimal params", func(t *testing.T) {
		expectedResp := &types.ClaimsResult{
			Claims: []*types.ClaimResponse{
				{
					BlockNum: 100,
				},
			},
			Count: 1,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/bridge/v1/claims", r.URL.Path)
			require.Equal(t, "1", r.URL.Query().Get("network_id"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetClaims(context.Background(), GetClaimsParams{
			NetworkID: 1,
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 1, resp.Count)
	})

	t.Run("successful request with all params", func(t *testing.T) {
		pageNum := uint32(1)
		pageSize := uint32(20)
		includeAll := true
		globalIndex := big.NewInt(123)

		expectedResp := &types.ClaimsResult{
			Claims: []*types.ClaimResponse{},
			Count:  0,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "1", r.URL.Query().Get("network_id"))
			require.Equal(t, "1", r.URL.Query().Get("page_number"))
			require.Equal(t, "20", r.URL.Query().Get("page_size"))
			require.Equal(t, "2,3", r.URL.Query().Get("network_ids"))
			require.Equal(t, "true", r.URL.Query().Get("include_all_fields"))
			require.Equal(t, "123", r.URL.Query().Get("global_index"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetClaims(context.Background(), GetClaimsParams{
			NetworkID:        1,
			PageNumber:       &pageNum,
			PageSize:         &pageSize,
			NetworkIDs:       []uint32{2, 3},
			IncludeAllFields: &includeAll,
			GlobalIndex:      globalIndex,
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func TestGetUnsetClaims(t *testing.T) {
	t.Run("successful request with no params", func(t *testing.T) {
		expectedResp := &types.UnsetClaimsResult{
			UnsetClaims: []*types.UnsetClaimResponse{
				{
					GlobalIndex: "123",
				},
			},
			Count: 1,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/bridge/v1/unset-claims", r.URL.Path)

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetUnsetClaims(context.Background(), GetUnsetClaimsParams{})

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 1, resp.Count)
	})

	t.Run("successful request with all params", func(t *testing.T) {
		pageNum := 1
		pageSize := 10
		globalIndex := big.NewInt(456)

		expectedResp := &types.UnsetClaimsResult{
			UnsetClaims: []*types.UnsetClaimResponse{},
			Count:       0,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "1", r.URL.Query().Get("page_number"))
			require.Equal(t, "10", r.URL.Query().Get("page_size"))
			require.Equal(t, "456", r.URL.Query().Get("global_index"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetUnsetClaims(context.Background(), GetUnsetClaimsParams{
			PageNumber:  &pageNum,
			PageSize:    &pageSize,
			GlobalIndex: globalIndex,
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func TestGetSetClaims(t *testing.T) {
	t.Run("successful request with no params", func(t *testing.T) {
		expectedResp := &types.SetClaimsResult{
			SetClaims: []*types.SetClaimResponse{
				{
					GlobalIndex: "789",
				},
			},
			Count: 1,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/bridge/v1/set-claims", r.URL.Path)

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetSetClaims(context.Background(), GetSetClaimsParams{})

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 1, resp.Count)
	})

	t.Run("successful request with all params", func(t *testing.T) {
		pageNum := 2
		pageSize := 15
		globalIndex := big.NewInt(999)

		expectedResp := &types.SetClaimsResult{
			SetClaims: []*types.SetClaimResponse{},
			Count:     0,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "2", r.URL.Query().Get("page_number"))
			require.Equal(t, "15", r.URL.Query().Get("page_size"))
			require.Equal(t, "999", r.URL.Query().Get("global_index"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetSetClaims(context.Background(), GetSetClaimsParams{
			PageNumber:  &pageNum,
			PageSize:    &pageSize,
			GlobalIndex: globalIndex,
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func TestGetTokenMappings(t *testing.T) {
	t.Run("successful request with minimal params", func(t *testing.T) {
		expectedResp := &types.TokenMappingsResult{
			TokenMappings: []*types.TokenMappingResponse{
				{
					OriginNetwork: 1,
				},
			},
			Count: 1,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/bridge/v1/token-mappings", r.URL.Path)
			require.Equal(t, "1", r.URL.Query().Get("network_id"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetTokenMappings(context.Background(), GetTokenMappingsParams{
			NetworkID: 1,
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 1, resp.Count)
	})

	t.Run("successful request with all params", func(t *testing.T) {
		pageNum := 1
		pageSize := 25
		tokenAddr := "0xabcdef0123456789abcdef0123456789abcdef01"

		expectedResp := &types.TokenMappingsResult{
			TokenMappings: []*types.TokenMappingResponse{},
			Count:         0,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "1", r.URL.Query().Get("network_id"))
			require.Equal(t, "1", r.URL.Query().Get("page_number"))
			require.Equal(t, "25", r.URL.Query().Get("page_size"))
			require.Equal(t, tokenAddr, r.URL.Query().Get("origin_token_address"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetTokenMappings(context.Background(), GetTokenMappingsParams{
			NetworkID:          1,
			PageNumber:         &pageNum,
			PageSize:           &pageSize,
			OriginTokenAddress: &tokenAddr,
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func TestGetLegacyTokenMigrations(t *testing.T) {
	t.Run("successful request with minimal params", func(t *testing.T) {
		expectedResp := &types.LegacyTokenMigrationsResult{
			TokenMigrations: []*types.LegacyTokenMigrationResponse{
				{
					BlockNum: 2,
				},
			},
			Count: 1,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/bridge/v1/legacy-token-migrations", r.URL.Path)
			require.Equal(t, "2", r.URL.Query().Get("network_id"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetLegacyTokenMigrations(context.Background(), GetLegacyTokenMigrationsParams{
			NetworkID: 2,
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 1, resp.Count)
	})

	t.Run("successful request with all params", func(t *testing.T) {
		pageNum := 3
		pageSize := 30

		expectedResp := &types.LegacyTokenMigrationsResult{
			TokenMigrations: []*types.LegacyTokenMigrationResponse{},
			Count:           0,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "2", r.URL.Query().Get("network_id"))
			require.Equal(t, "3", r.URL.Query().Get("page_number"))
			require.Equal(t, "30", r.URL.Query().Get("page_size"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetLegacyTokenMigrations(context.Background(), GetLegacyTokenMigrationsParams{
			NetworkID:  2,
			PageNumber: &pageNum,
			PageSize:   &pageSize,
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func TestGetL1InfoTreeIndex(t *testing.T) {
	t.Run("successful request", func(t *testing.T) {
		expectedIndex := uint32(42)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/bridge/v1/l1-info-tree-index", r.URL.Path)
			require.Equal(t, "1", r.URL.Query().Get("network_id"))
			require.Equal(t, "10", r.URL.Query().Get("deposit_count"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedIndex)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		index, err := client.GetL1InfoTreeIndex(context.Background(), 1, 10)

		require.NoError(t, err)
		require.Equal(t, expectedIndex, index)
	})

	t.Run("handles error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not found"))
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		index, err := client.GetL1InfoTreeIndex(context.Background(), 1, 999)

		require.Error(t, err)
		require.Equal(t, uint32(0), index)
	})
}

func TestGetInjectedL1InfoLeaf(t *testing.T) {
	t.Run("successful request", func(t *testing.T) {
		expectedResp := &types.L1InfoTreeLeafResponse{
			L1InfoTreeIndex: 5,
			BlockNumber:     100,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/bridge/v1/injected-l1-info-leaf", r.URL.Path)
			require.Equal(t, "2", r.URL.Query().Get("network_id"))
			require.Equal(t, "5", r.URL.Query().Get("leaf_index"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetInjectedL1InfoLeaf(context.Background(), 2, 5)

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, uint32(5), resp.L1InfoTreeIndex)
		require.Equal(t, uint64(100), resp.BlockNumber)
	})
}

func TestGetClaimProof(t *testing.T) {
	t.Run("successful request", func(t *testing.T) {
		var localProof types.Proof
		localProof[0] = "0x1234567890123456789012345678901234567890123456789012345678901234"

		var rollupProof types.Proof
		rollupProof[0] = "0xabcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

		expectedResp := &types.ClaimProof{
			ProofLocalExitRoot:  localProof,
			ProofRollupExitRoot: rollupProof,
			L1InfoTreeLeaf: types.L1InfoTreeLeafResponse{
				L1InfoTreeIndex: 10,
			},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/bridge/v1/claim-proof", r.URL.Path)
			require.Equal(t, "1", r.URL.Query().Get("network_id"))
			require.Equal(t, "10", r.URL.Query().Get("leaf_index"))
			require.Equal(t, "5", r.URL.Query().Get("deposit_count"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetClaimProof(context.Background(), 1, 10, 5)

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, types.Hash("0x1234567890123456789012345678901234567890123456789012345678901234"), resp.ProofLocalExitRoot[0])
		require.Equal(t, uint32(10), resp.L1InfoTreeLeaf.L1InfoTreeIndex)
	})
}

func TestGetLastReorgEvent(t *testing.T) {
	t.Run("successful request", func(t *testing.T) {
		expectedResp := &bridgesync.LastReorg{
			DetectedAt: 1234567890,
			FromBlock:  500,
			ToBlock:    600,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/bridge/v1/last-reorg-event", r.URL.Path)
			require.Equal(t, "0", r.URL.Query().Get("network_id"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetLastReorgEvent(context.Background(), 0)

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, uint64(500), resp.FromBlock)
		require.Equal(t, uint64(600), resp.ToBlock)
	})
}

func TestGetSyncStatus(t *testing.T) {
	t.Run("successful request", func(t *testing.T) {
		expectedResp := &types.SyncStatus{
			L1Info: &types.NetworkSyncInfo{
				ContractDepositCount:     100,
				SynchronizedDepositCount: 95,
				IsSynced:                 false,
				IsActive:                 true,
			},
			L2Info: &types.NetworkSyncInfo{
				ContractDepositCount:     50,
				SynchronizedDepositCount: 50,
				IsSynced:                 true,
				IsActive:                 true,
			},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/bridge/v1/sync-status", r.URL.Path)

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetSyncStatus(context.Background())

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.L1Info)
		require.NotNil(t, resp.L2Info)
		require.Equal(t, uint32(100), resp.L1Info.ContractDepositCount)
		require.Equal(t, uint32(50), resp.L2Info.ContractDepositCount)
		require.False(t, resp.L1Info.IsSynced)
		require.True(t, resp.L2Info.IsSynced)
	})
}

func TestGetRemoveGEREvents(t *testing.T) {
	t.Run("successful request with no params", func(t *testing.T) {
		expectedResp := &types.RemoveGEREventsResult{
			RemoveGEREvents: []*types.RemoveGEREventResponse{
				{
					GlobalExitRoot: "0xabcd",
					BlockNum:       200,
				},
			},
			Count: 1,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/bridge/v1/removed-gers", r.URL.Path)

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetRemoveGEREvents(context.Background(), GetRemoveGEREventsParams{})

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 1, resp.Count)
	})

	t.Run("successful request with all params", func(t *testing.T) {
		ger := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
		limit := 25

		expectedResp := &types.RemoveGEREventsResult{
			RemoveGEREvents: []*types.RemoveGEREventResponse{},
			Count:           0,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, ger, r.URL.Query().Get("global_exit_root"))
			require.Equal(t, "25", r.URL.Query().Get("limit"))

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(expectedResp)
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetRemoveGEREvents(context.Background(), GetRemoveGEREventsParams{
			GlobalExitRoot: &ger,
			Limit:          &limit,
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func TestDoRequest_Errors(t *testing.T) {
	t.Run("handles malformed JSON response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{invalid json"))
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.HealthCheck(context.Background())

		require.Error(t, err)
		require.Nil(t, resp)
		require.Contains(t, err.Error(), "decode response")
	})

	t.Run("handles network error", func(t *testing.T) {
		client := New(Config{
			BaseURL: "http://invalid-host-that-does-not-exist:9999",
			Timeout: 100 * time.Millisecond,
		})

		resp, err := client.HealthCheck(context.Background())

		require.Error(t, err)
		require.Nil(t, resp)
	})

	t.Run("handles service unavailable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("service unavailable"))
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		resp, err := client.GetBridges(context.Background(), GetBridgesParams{NetworkID: 1})

		require.Error(t, err)
		require.Nil(t, resp)
		require.Contains(t, err.Error(), "503")
	})
}
