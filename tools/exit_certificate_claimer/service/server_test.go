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
	rec := doRequest(t, srv, apiBasePath+"/health")
	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "ok", body["status"])
	require.Equal(t, float64(1), body["network_id"])
}

func TestServerBridges(t *testing.T) {
	t.Parallel()

	srv, destAddr := newTestServer(t)
	rec := doRequest(t, srv, apiBasePath+"/bridges?dest_address="+destAddr.Hex())
	require.Equal(t, http.StatusOK, rec.Code)

	var resp BridgesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, uint32(1), resp.NetworkID)
	require.Equal(t, destAddr.Hex(), resp.DestinationAddress)
	require.Len(t, resp.Bridges, 1)
}

func TestServerBridgesMissingDestAddress(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)
	rec := doRequest(t, srv, apiBasePath+"/bridges")
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var resp errorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Contains(t, resp.Error, destAddressParam+" query parameter is required")
}

func TestServerBridgesInvalidDestAddress(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)
	rec := doRequest(t, srv, apiBasePath+"/bridges?dest_address=not-an-address")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServerClaimParams(t *testing.T) {
	t.Parallel()

	srv, destAddr := newTestServer(t)
	rec := doRequest(t, srv, apiBasePath+"/claim-params?dest_address="+destAddr.Hex())
	require.Equal(t, http.StatusOK, rec.Code)

	var resp ClaimParamsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Claims, 1)
	require.Equal(t, uint32(5), resp.Claims[0].DepositCount)
}

func TestServerClaimParamsInvalidDepositCount(t *testing.T) {
	t.Parallel()

	srv, destAddr := newTestServer(t)
	rec := doRequest(t, srv,
		apiBasePath+"/claim-params?dest_address="+destAddr.Hex()+"&deposit_count=not-a-number")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServerClaimParamsNotSettledConflict(t *testing.T) {
	t.Parallel()

	// A claimer whose settled local exit root does not match the certificate yields a 409.
	claimer, destAddr := buildTestClaimer(t, common.HexToHash("0xdeadbeef"))
	cfg := &Config{Address: "127.0.0.1", Port: 7080, ReadTimeoutSeconds: 1, WriteTimeoutSeconds: 1}
	srv := NewServer(cfg, claimer, claimer.logger)

	rec := doRequest(t, srv, apiBasePath+"/claim-params?dest_address="+destAddr.Hex())
	require.Equal(t, http.StatusConflict, rec.Code)
}
