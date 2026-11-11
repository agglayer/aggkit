package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/apiclient"
	"github.com/stretchr/testify/require"
)

// newImportedExitsServer answers the imported bridge exits route with the given
// exits, or with the given status code when status is not 200.
func newImportedExitsServer(t *testing.T, status int, exits []*agglayertypes.ImportedBridgeExit) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/aggsender/v1/certificate-imported-bridge-exits", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		require.NoError(t, json.NewEncoder(w).Encode(exits))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestCertificateContainsGlobalIndex(t *testing.T) {
	wanted := &agglayertypes.GlobalIndex{RollupIndex: 1, LeafIndex: 3}
	other := &agglayertypes.GlobalIndex{RollupIndex: 1, LeafIndex: 4}

	t.Run("found", func(t *testing.T) {
		server := newImportedExitsServer(t, http.StatusOK, []*agglayertypes.ImportedBridgeExit{
			{GlobalIndex: other},
			{GlobalIndex: wanted},
		})

		found, err := certificateContainsGlobalIndex(apiclient.NewClient(server.URL), 7, wanted)
		require.NoError(t, err)
		require.True(t, found)
	})

	t.Run("not found", func(t *testing.T) {
		server := newImportedExitsServer(t, http.StatusOK, []*agglayertypes.ImportedBridgeExit{
			{GlobalIndex: other},
		})

		found, err := certificateContainsGlobalIndex(apiclient.NewClient(server.URL), 7, wanted)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("certificate without imported bridge exits", func(t *testing.T) {
		server := newImportedExitsServer(t, http.StatusNotFound, nil)

		found, err := certificateContainsGlobalIndex(apiclient.NewClient(server.URL), 7, wanted)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("server error", func(t *testing.T) {
		server := newImportedExitsServer(t, http.StatusInternalServerError, nil)

		_, err := certificateContainsGlobalIndex(apiclient.NewClient(server.URL), 7, wanted)
		require.Error(t, err)
	})
}
