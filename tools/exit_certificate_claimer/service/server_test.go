package claimer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// newTestServer builds a Server backed by a claimer whose local exit root matches the certificate
// (so claim params resolve) and returns it ready to receive httptest requests.
func newTestServer(t *testing.T) (*Server, common.Address) {
	t.Helper()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)

	claimer, _ := buildTestClaimer(t, cert.NewLocalExitRoot)
	cfg := &Config{Address: "127.0.0.1", Port: 7080, ReadTimeoutSeconds: 1, WriteTimeoutSeconds: 1}
	srv := NewServer(cfg, claimer, claimer.logger)
	return srv, cert.Leaves[0].DestinationAddress
}

func doRequest(t *testing.T, srv *Server, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	return rec
}

func TestServerHealth(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)
	rec := doRequest(t, srv, apiBasePathV2+"/health")
	require.Equal(t, http.StatusOK, rec.Code)

	var body HealthResponseV2
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "ok", body.Status)
	require.Equal(t, uint32(1), body.NetworkID)
	require.NotEmpty(t, body.Version.Version)
	require.NotEmpty(t, body.Version.GoVersion)
	require.NotEmpty(t, body.Version.OS)
	require.NotEmpty(t, body.Version.Arch)
}

func TestServerBridges(t *testing.T) {
	t.Parallel()

	srv, destAddr := newTestServer(t)
	rec := doRequest(t, srv, apiBasePathV2+"/bridges?address="+destAddr.Hex())
	require.Equal(t, http.StatusOK, rec.Code)

	var resp BridgesResponseV2
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, uint32(1), resp.NetworkID)
	require.Equal(t, destAddr.Hex(), resp.Address)
	require.Len(t, resp.Bridges, 1)
}

func TestServerBridgesMissingAddress(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)
	rec := doRequest(t, srv, apiBasePathV2+"/bridges")
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var resp errorResponseV2
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Contains(t, resp.Error, addressParam+" query parameter is required")
}

func TestServerBridgesInvalidAddress(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)
	rec := doRequest(t, srv, apiBasePathV2+"/bridges?address=not-an-address")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServerClaimParams(t *testing.T) {
	t.Parallel()

	srv, destAddr := newTestServer(t)
	rec := doRequest(t, srv, apiBasePathV2+"/claim-params?address="+destAddr.Hex())
	require.Equal(t, http.StatusOK, rec.Code)

	var resp ClaimParamsResponseV2
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Claims, 1)
	require.Equal(t, uint32(5), resp.Claims[0].DepositCount)
}

func TestServerClaimParamsInvalidDepositCount(t *testing.T) {
	t.Parallel()

	srv, destAddr := newTestServer(t)
	rec := doRequest(t, srv,
		apiBasePathV2+"/claim-params?address="+destAddr.Hex()+"&deposit_count=not-a-number")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServerClaimParamsNotSettledConflict(t *testing.T) {
	t.Parallel()

	// A claimer whose settled local exit root does not match the certificate yields a 409.
	claimer, destAddr := buildTestClaimer(t, common.HexToHash("0xdeadbeef"))
	cfg := &Config{Address: "127.0.0.1", Port: 7080, ReadTimeoutSeconds: 1, WriteTimeoutSeconds: 1}
	srv := NewServer(cfg, claimer, claimer.logger)

	rec := doRequest(t, srv, apiBasePathV2+"/claim-params?address="+destAddr.Hex())
	require.Equal(t, http.StatusConflict, rec.Code)
}

// API v1 is removed: /claimer/v1/health keeps answering (only v2 is listed), any other v1 path is 410 Gone
// and an unknown path outside v1 is still a 404.
func TestServerAPIV1Removed(t *testing.T) {
	t.Parallel()

	srv, destAddr := newTestServer(t)

	rec := doRequest(t, srv, apiBasePathV1+"/health")
	require.Equal(t, http.StatusOK, rec.Code)
	var health HealthResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &health))
	require.Equal(t, "ok", health.Status)
	require.Equal(t, []int{apiVersion2}, health.APIVersions)

	for _, path := range []string{
		"/bridges?dest_address=" + destAddr.Hex(),
		"/claim-params?dest_address=" + destAddr.Hex(),
		"/anything",
	} {
		rec := doRequest(t, srv, apiBasePathV1+path)
		require.Equal(t, http.StatusGone, rec.Code, path)
		var gone apiRemovedResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &gone), path)
		require.Equal(t, "claimer API v1 has been removed, use /claimer/v2", gone.Error)
		require.Equal(t, []int{apiVersion2}, gone.APIVersions)
	}

	rec = doRequest(t, srv, "/claimer/v3/bridges")
	require.Equal(t, http.StatusNotFound, rec.Code)
}
