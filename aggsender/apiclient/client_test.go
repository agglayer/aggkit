package apiclient

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	require.NoError(t, json.NewEncoder(w).Encode(body))
}

func TestGetStatus(t *testing.T) {
	expected := types.AggsenderInfo{}
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, statusPath, r.URL.Path)
		require.Empty(t, r.URL.Query())
		writeJSON(t, w, http.StatusOK, expected)
	})

	result, err := NewClient(server.URL).GetStatus()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, expected, *result)
}

func TestGetCertificateHeaderPerHeight(t *testing.T) {
	height := uint64(1)
	expected := types.Certificate{Header: &types.CertificateHeader{Height: height}}
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, certificateHeaderPath, r.URL.Path)
		require.Equal(t, "1", r.URL.Query().Get(heightQueryParam))
		writeJSON(t, w, http.StatusOK, expected)
	})

	cert, err := NewClient(server.URL).GetCertificateHeaderPerHeight(&height)
	require.NoError(t, err)
	require.NotNil(t, cert)
	require.Equal(t, expected, *cert)

	// Without a height the query parameter is absent.
	serverNoHeight := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, certificateHeaderPath, r.URL.Path)
		require.NotContains(t, r.URL.Query(), heightQueryParam)
		writeJSON(t, w, http.StatusOK, expected)
	})
	_, err = NewClient(serverNoHeight.URL).GetCertificateHeaderPerHeight(nil)
	require.NoError(t, err)
}

func TestGetCertificateBridgeExits(t *testing.T) {
	height := uint64(42)
	expected := []*agglayertypes.BridgeExit{
		{
			LeafType:           0,
			DestinationNetwork: 1,
			DestinationAddress: common.HexToAddress("0xdeadbeef"),
			Amount:             big.NewInt(1000),
		},
	}
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, certificateBridgeExitsPath, r.URL.Path)
		require.Equal(t, "42", r.URL.Query().Get(heightQueryParam))
		writeJSON(t, w, http.StatusOK, expected)
	})

	exits, err := NewClient(server.URL).GetCertificateBridgeExits(&height)
	require.NoError(t, err)
	require.Len(t, exits, 1)
	require.Equal(t, expected[0].DestinationNetwork, exits[0].DestinationNetwork)
	require.Equal(t, expected[0].DestinationAddress, exits[0].DestinationAddress)
}

func TestClientTrimsTrailingSlashOfBaseURL(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, statusPath, r.URL.Path)
		writeJSON(t, w, http.StatusOK, types.AggsenderInfo{})
	})

	_, err := NewClient(server.URL + "/").GetStatus()
	require.NoError(t, err)
}

func TestClientCallUsesTimeout(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		writeJSON(t, w, http.StatusOK, types.AggsenderInfo{})
	})

	sut := NewClient(server.URL)
	sut.requestTimeout = 50 * time.Millisecond

	_, err := sut.GetStatus()
	require.Error(t, err)
}

func TestGetStatus_Errors(t *testing.T) {
	t.Run("connection error", func(t *testing.T) {
		server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {})
		url := server.URL
		server.Close()

		_, err := NewClient(url).GetStatus()
		require.Error(t, err)
	})

	t.Run("error response body", func(t *testing.T) {
		server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, http.StatusInternalServerError, map[string]string{"error": "storage failure"})
		})

		_, err := NewClient(server.URL).GetStatus()
		require.Error(t, err)
		require.Contains(t, err.Error(), "500")
		require.Contains(t, err.Error(), "storage failure")
	})

	t.Run("not found", func(t *testing.T) {
		server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, http.StatusNotFound, map[string]string{"error": "certificate not found"})
		})

		_, err := NewClient(server.URL).GetCertificateHeaderPerHeight(nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "404")
		require.Contains(t, err.Error(), "certificate not found")
	})

	t.Run("invalid json", func(t *testing.T) {
		server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("not-json"))
			require.NoError(t, err)
		})

		_, err := NewClient(server.URL).GetStatus()
		require.Error(t, err)
	})

	t.Run("response body over the limit", func(t *testing.T) {
		server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write(bytes.Repeat([]byte("a"), maxResponseBytes+1))
			require.NoError(t, err)
		})

		_, err := NewClient(server.URL).GetStatus()
		require.Error(t, err)
		require.Contains(t, err.Error(), "response body exceeds")
	})
}
