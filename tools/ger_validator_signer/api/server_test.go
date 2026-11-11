package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/api/mocks"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/config"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/service"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	testGER     = common.HexToHash("0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	validator   = common.HexToAddress("0x7e5f4552091a69125d5dfcb7b8c2659029395bdf")
	errL1Secret = errors.New("l1 rpc https://l1.example.com/s3cr3t-t0k3n is down")
)

func goldenFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return data
}

func goldenCode(t *testing.T, name string) string {
	t.Helper()
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(goldenFile(t, name), &errResp))
	return errResp.Code
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.L1ChainID = 11155111
	cfg.REST.InsecureHTTP = true
	cfg.RateLimit.RequestsPerSecond = 0 // no limit, unless a test wants it
	return cfg
}

func newTestServer(t *testing.T, cfg config.Config) (*Server, *mocks.Service) {
	t.Helper()
	svc := mocks.NewService(t)
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg, svc), svc
}

// do sends a request to the handler of the server, from the client address
func do(t *testing.T, server *Server, method, path, body, clientAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = clientAddr
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	return recorder
}

func post(t *testing.T, server *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, server, http.MethodPost, "/v1/ger/sign", body, "192.0.2.1:1234")
}

func requireError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) ErrorResponse {
	t.Helper()
	require.Equal(t, status, recorder.Code)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &errResp))
	require.Equal(t, code, errResp.Code)
	require.NotEmpty(t, errResp.Message)
	return errResp
}

func TestSign_GoldenFixtures(t *testing.T) {
	server, svc := newTestServer(t, testConfig())
	var expected SignResponse
	require.NoError(t, json.Unmarshal(goldenFile(t, "sign_response.json"), &expected))
	svc.EXPECT().SignGER(mock.Anything, testGER).
		Return(&service.Result{GER: expected.GER, Signature: expected.Signature, Signer: expected.Signer}, nil).Once()

	recorder := post(t, server, string(goldenFile(t, "sign_request.json")))

	require.Equal(t, http.StatusOK, recorder.Code)
	// The server answers exactly what the client of the AggOracle is tested against
	require.JSONEq(t, string(goldenFile(t, "sign_response.json")), recorder.Body.String())
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
}

func TestSign_ErrorsFollowTheGoldenFixtures(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		status     int
		fixture    string
		retryAfter string
	}{
		{"unknown GER", service.ErrGERUnknown, http.StatusNotFound, "error_ger_unknown.json", ""},
		{"block not canonical", fmt.Errorf("%w: the block 5 is another", service.ErrGERBlockNotCanonical),
			http.StatusConflict, "error_ger_block_not_canonical.json", ""},
		{"not final", &service.NotFinalError{InsertionBlock: 101, FinalBlock: 100, HasFinalBlock: true},
			http.StatusTooEarly, "error_ger_not_final.json", "5"},
		{"invalid GER", fmt.Errorf("%w: it is the zero hash", service.ErrInvalidGER),
			http.StatusBadRequest, "error_invalid_request.json", ""},
		{"internal", errors.New("boom"), http.StatusInternalServerError, "error_internal.json", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, svc := newTestServer(t, testConfig())
			svc.EXPECT().SignGER(mock.Anything, testGER).Return(nil, tc.err).Once()

			recorder := post(t, server, string(goldenFile(t, "sign_request.json")))

			requireError(t, recorder, tc.status, goldenCode(t, tc.fixture))
			require.Equal(t, tc.retryAfter, recorder.Header().Get("Retry-After"))
		})
	}
}

func TestSign_InternalErrorsDoNotLeakDetails(t *testing.T) {
	server, svc := newTestServer(t, testConfig())
	svc.EXPECT().SignGER(mock.Anything, testGER).Return(nil, fmt.Errorf("failed to read: %w", errL1Secret)).Once()

	recorder := post(t, server, string(goldenFile(t, "sign_request.json")))

	errResp := requireError(t, recorder, http.StatusInternalServerError, CodeInternal)
	require.Equal(t, "internal error", errResp.Message)
	require.NotContains(t, recorder.Body.String(), "s3cr3t-t0k3n")
	require.NotContains(t, recorder.Body.String(), "l1.example.com")
}

func TestSign_TimeoutAndPanic(t *testing.T) {
	t.Run("the request timeout", func(t *testing.T) {
		cfg := testConfig()
		cfg.REST.RequestTimeout = config.Duration{Duration: 50 * time.Millisecond}
		server, svc := newTestServer(t, cfg)
		svc.EXPECT().SignGER(mock.Anything, testGER).
			RunAndReturn(func(ctx context.Context, _ common.Hash) (*service.Result, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}).Once()

		start := time.Now()
		recorder := post(t, server, string(goldenFile(t, "sign_request.json")))
		require.Less(t, time.Since(start), 5*time.Second)
		errResp := requireError(t, recorder, http.StatusInternalServerError, CodeInternal)
		require.Equal(t, "the request timed out", errResp.Message)
	})

	t.Run("a panic is an internal error", func(t *testing.T) {
		server, svc := newTestServer(t, testConfig())
		svc.EXPECT().SignGER(mock.Anything, testGER).
			RunAndReturn(func(context.Context, common.Hash) (*service.Result, error) {
				panic("boom")
			}).Once()

		recorder := post(t, server, string(goldenFile(t, "sign_request.json")))
		requireError(t, recorder, http.StatusInternalServerError, CodeInternal)
	})
}

func TestSign_InvalidRequests(t *testing.T) {
	const validGER = `"0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"`
	tests := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"empty body", "", "the body is empty"},
		{"not JSON", "ger=0x01", "not valid JSON"},
		{"truncated JSON", `{"ger": `, "not valid JSON"},
		{"not an object", `[1,2]`, "has an invalid type"},
		{"a field of another type", `{"ger": 5}`, `the field "ger" has an invalid type`},
		{"unknown field", `{"ger": ` + validGER + `, "extra": true}`, "unknown field"},
		{"two objects", `{"ger": ` + validGER + `}{"ger": ` + validGER + `}`, "must be one JSON object"},
		{"trailing garbage", `{"ger": ` + validGER + `} xyz`, "must be one JSON object"},
		{"no ger", `{}`, "ger must be a 32 bytes hex string"},
		{"empty ger", `{"ger": ""}`, "ger must be a 32 bytes hex string"},
		{"without 0x", `{"ger": "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"}`, "0x prefix"},
		{"too short", `{"ger": "0x0102"}`, "32 bytes"},
		{"too long", `{"ger": "0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f2021"}`, "32 bytes"},
		{"not hex", `{"ger": "0xzz02030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"}`, "32 bytes"},
		{"odd length", `{"ger": "0x102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"}`, "32 bytes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newTestServer(t, testConfig()) // the service must not be called
			recorder := post(t, server, tc.body)
			errResp := requireError(t, recorder, http.StatusBadRequest, CodeInvalidRequest)
			require.Contains(t, errResp.Message, tc.wantMsg)
		})
	}
}

func TestSign_BodyTooBig(t *testing.T) {
	cfg := testConfig()
	cfg.REST.MaxBodyBytes = 64
	server, _ := newTestServer(t, cfg)

	recorder := post(t, server, `{"ger": "`+strings.Repeat("a", 1000)+`"}`)

	errResp := requireError(t, recorder, http.StatusRequestEntityTooLarge, CodeInvalidRequest)
	require.Contains(t, errResp.Message, "maximum is 64 bytes")
}

func TestSign_AcceptsUppercaseHex(t *testing.T) {
	server, svc := newTestServer(t, testConfig())
	svc.EXPECT().SignGER(mock.Anything, testGER).
		Return(&service.Result{GER: testGER, Signature: []byte{1}, Signer: validator}, nil).Once()

	recorder := post(t, server, `{"ger": "0x`+strings.ToUpper(strings.TrimPrefix(testGER.Hex(), "0x"))+`"}`)
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestSign_ZeroGERIsTheServiceDecision(t *testing.T) {
	server, svc := newTestServer(t, testConfig())
	svc.EXPECT().SignGER(mock.Anything, common.Hash{}).Return(nil, service.ErrInvalidGER).Once()

	recorder := post(t, server, `{"ger": "`+common.Hash{}.Hex()+`"}`)
	requireError(t, recorder, http.StatusBadRequest, CodeInvalidRequest)
}

func TestRoutes(t *testing.T) {
	server, _ := newTestServer(t, testConfig())

	t.Run("method not allowed", func(t *testing.T) {
		require.Equal(t, http.StatusMethodNotAllowed, do(t, server, http.MethodGet, "/v1/ger/sign", "", "192.0.2.1:1").Code)
		require.Equal(t, http.StatusMethodNotAllowed, do(t, server, http.MethodPost, "/v1/info", "", "192.0.2.1:1").Code)
		require.Equal(t, http.StatusMethodNotAllowed, do(t, server, http.MethodDelete, "/health", "", "192.0.2.1:1").Code)
	})

	t.Run("unknown route", func(t *testing.T) {
		require.Equal(t, http.StatusNotFound, do(t, server, http.MethodGet, "/v1/other", "", "192.0.2.1:1").Code)
		require.Equal(t, http.StatusNotFound, do(t, server, http.MethodGet, "/", "", "192.0.2.1:1").Code)
	})

	t.Run("health", func(t *testing.T) {
		recorder := do(t, server, http.MethodGet, "/health", "", "192.0.2.1:1")
		require.Equal(t, http.StatusOK, recorder.Code)
		require.JSONEq(t, `{"status":"ok"}`, recorder.Body.String())
	})
}

func TestInfo(t *testing.T) {
	cfg := testConfig()
	server, svc := newTestServer(t, cfg)
	agglayerGER := common.HexToAddress("0x1111111111111111111111111111111111111111")
	svc.EXPECT().Info().Return(service.Info{
		Signer: validator, AgglayerGER: agglayerGER, FinalityTag: "latest", FinalityNumBlks: 6,
	}).Once()

	recorder := do(t, server, http.MethodGet, "/v1/info", "", "192.0.2.1:1")

	require.Equal(t, http.StatusOK, recorder.Code)
	var info InfoResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &info))
	require.Equal(t, validator, info.Signer)
	require.Equal(t, agglayerGER, info.AgglayerGER)
	require.Equal(t, uint64(11155111), info.L1ChainID)
	require.Equal(t, FinalityInfo{Tag: "latest", NumBlocks: 6}, info.Finality)
	require.NotEmpty(t, info.Version)
	require.JSONEq(t, `"`+strings.ToLower(validator.Hex())+`"`, mustMarshal(t, info.Signer))
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}

func TestSwaggerSpec(t *testing.T) {
	server, _ := newTestServer(t, testConfig())

	recorder := do(t, server, http.MethodGet, "/swagger/doc.json", "", "192.0.2.1:1")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	var spec struct {
		Swagger string                    `json:"swagger"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &spec))
	require.Equal(t, "2.0", spec.Swagger)
	require.Contains(t, spec.Paths, "/v1/ger/sign")
	require.Contains(t, spec.Paths["/v1/ger/sign"], "post")
	require.Contains(t, spec.Paths, "/v1/info")
	require.Contains(t, spec.Paths, "/health")

	yamlRecorder := do(t, server, http.MethodGet, "/swagger/doc.yaml", "", "192.0.2.1:1")
	require.Equal(t, http.StatusOK, yamlRecorder.Code)
	require.Equal(t, "application/yaml", yamlRecorder.Header().Get("Content-Type"))
	require.Contains(t, yamlRecorder.Body.String(), "/v1/ger/sign")
}

func TestRateLimit(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimit = config.RateLimitConfig{RequestsPerSecond: 0.001, Burst: 2} // refills in 1000s: no refill in the test
	server, svc := newTestServer(t, cfg)
	svc.EXPECT().SignGER(mock.Anything, testGER).
		Return(&service.Result{GER: testGER, Signature: []byte{1}, Signer: validator}, nil).Times(4)
	svc.EXPECT().Info().Return(service.Info{}).Maybe()
	body := string(goldenFile(t, "sign_request.json"))
	send := func(client string) *httptest.ResponseRecorder {
		return do(t, server, http.MethodPost, "/v1/ger/sign", body, client)
	}

	require.Equal(t, http.StatusOK, send("198.51.100.1:1000").Code)
	require.Equal(t, http.StatusOK, send("198.51.100.1:1001").Code, "the port doesn't matter: it is the same client")
	limited := send("198.51.100.1:1002")
	errResp := requireError(t, limited, http.StatusTooManyRequests, goldenCode(t, "error_rate_limited.json"))
	require.Equal(t, "too many requests", errResp.Message)
	require.Equal(t, "1", limited.Header().Get("Retry-After"))

	// Another client is not affected
	require.Equal(t, http.StatusOK, send("198.51.100.2:1000").Code)
	require.Equal(t, http.StatusOK, send("198.51.100.2:1001").Code)
	require.Equal(t, http.StatusTooManyRequests, send("198.51.100.2:1002").Code)

	// info is limited too, and the limit is shared with sign
	require.Equal(t, http.StatusTooManyRequests,
		do(t, server, http.MethodGet, "/v1/info", "", "198.51.100.1:1000").Code)

	// but the health check never is
	for range 10 {
		require.Equal(t, http.StatusOK, do(t, server, http.MethodGet, "/health", "", "198.51.100.1:1000").Code)
	}
}

// The headers added by proxies can be sent by anybody: they must not choose the client that is limited
func TestRateLimit_IgnoresForwardedHeaders(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimit = config.RateLimitConfig{RequestsPerSecond: 0.001, Burst: 1}
	server, svc := newTestServer(t, cfg)
	svc.EXPECT().SignGER(mock.Anything, testGER).
		Return(&service.Result{GER: testGER, Signature: []byte{1}, Signer: validator}, nil).Once()
	body := string(goldenFile(t, "sign_request.json"))

	for i, expected := range []int{http.StatusOK, http.StatusTooManyRequests, http.StatusTooManyRequests} {
		req := httptest.NewRequest(http.MethodPost, "/v1/ger/sign", strings.NewReader(body))
		req.RemoteAddr = "198.51.100.7:1000"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		req.Header.Set("X-Real-IP", fmt.Sprintf("203.0.113.%d", i))
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, req)
		require.Equal(t, expected, recorder.Code, "request %d", i)
	}
}

func TestRateLimit_Disabled(t *testing.T) {
	server, svc := newTestServer(t, testConfig()) // RequestsPerSecond is 0
	svc.EXPECT().SignGER(mock.Anything, testGER).
		Return(&service.Result{GER: testGER, Signature: []byte{1}, Signer: validator}, nil).Times(100)
	body := string(goldenFile(t, "sign_request.json"))
	for range 100 {
		require.Equal(t, http.StatusOK, post(t, server, body).Code)
	}
}

func TestClientLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := newClientLimiter(2, 2) // 2 per second, a burst of 2
	limiter.now = func() time.Time { return now }

	require.True(t, limiter.allow("a"))
	require.True(t, limiter.allow("a"))
	require.False(t, limiter.allow("a"), "the burst is spent")
	require.True(t, limiter.allow("b"), "another client")

	now = now.Add(500 * time.Millisecond) // one token is back
	require.True(t, limiter.allow("a"))
	require.False(t, limiter.allow("a"))

	now = now.Add(time.Hour)
	require.True(t, limiter.allow("a"), "everything is back")
	require.True(t, limiter.allow("a"))
	require.False(t, limiter.allow("a"))
}

func TestClientLimiter_NilIsNoLimit(t *testing.T) {
	var limiter *clientLimiter
	for range 5 {
		require.True(t, limiter.allow("a"))
	}
	require.Nil(t, newClientLimiter(0, 10))
	require.Nil(t, newClientLimiter(-1, 10))
}

func TestClientLimiter_BoundedMemory(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := newClientLimiter(1, 1)
	limiter.now = func() time.Time { return now }

	for i := range maxTrackedClients + 500 {
		now = now.Add(time.Millisecond)
		require.True(t, limiter.allow(fmt.Sprintf("client-%d", i)))
	}
	require.LessOrEqual(t, len(limiter.clients), maxTrackedClients)

	// The newest clients are the ones that are kept: their bucket is still spent
	last := fmt.Sprintf("client-%d", maxTrackedClients+499)
	require.False(t, limiter.allow(last))

	// Clients that were idle for long are forgotten first
	now = now.Add(clientIdleTTL + time.Second)
	require.True(t, limiter.allow("newcomer"))
	require.Less(t, len(limiter.clients), maxTrackedClients/2+10, "the idle ones were dropped")
}

func TestClientAddress(t *testing.T) {
	tests := map[string]string{
		"192.0.2.1:8080":   "192.0.2.1",
		"[2001:db8::1]:80": "2001:db8::1",
		"no-port":          "no-port",
	}
	for remoteAddr, expected := range tests {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remoteAddr
		require.Equal(t, expected, clientAddress(req))
	}
}

func TestToAPIError(t *testing.T) {
	tests := []struct {
		err     error
		status  int
		code    string
		message string
		retry   int
	}{
		{service.ErrInvalidGER, http.StatusBadRequest, CodeInvalidRequest, service.ErrInvalidGER.Error(), 0},
		{service.ErrGERUnknown, http.StatusNotFound, CodeGERUnknown, service.ErrGERUnknown.Error(), 0},
		{service.ErrGERBlockNotCanonical, http.StatusConflict, CodeGERBlockNotCanon, service.ErrGERBlockNotCanonical.Error(), 0},
		{service.ErrGERNotFinal, http.StatusTooEarly, CodeGERNotFinal, service.ErrGERNotFinal.Error(), retryAfterSeconds},
		{context.DeadlineExceeded, http.StatusInternalServerError, CodeInternal, "the request timed out", 0},
		{context.Canceled, http.StatusInternalServerError, CodeInternal, "internal error", 0},
		{errL1Secret, http.StatusInternalServerError, CodeInternal, "internal error", 0},
	}
	for _, tc := range tests {
		got := toAPIError(tc.err)
		require.Equal(t, apiError{status: tc.status, code: tc.code, message: tc.message, retryAfter: tc.retry}, got, tc.err.Error())
	}
}

func TestServe_HTTP(t *testing.T) {
	server, svc := newTestServer(t, testConfig())
	svc.EXPECT().Info().Return(service.Info{Signer: validator}).Maybe()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()

	resp, err := http.Get("http://" + listener.Addr().String() + "/health") //nolint:noctx
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, `{"status":"ok"}`, string(body))

	cancel() // graceful shutdown
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		require.Fail(t, "the server didn't stop")
	}
	_, err = http.Get("http://" + listener.Addr().String() + "/health") //nolint:noctx
	require.Error(t, err, "the server is stopped")
}

func TestServe_ListenerError(t *testing.T) {
	server, _ := newTestServer(t, testConfig())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, listener.Close()) // a listener that fails

	err = server.Serve(context.Background(), listener)
	require.Error(t, err)
}

// selfSignedCert writes a self-signed certificate for 127.0.0.1 and its key, and returns their files
func selfSignedCert(t *testing.T) (certFile, keyFile string, certPool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ger-validator-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600))

	certPool = x509.NewCertPool()
	parsed, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	certPool.AddCert(parsed)
	return certFile, keyFile, certPool
}

func TestServe_TLS(t *testing.T) {
	certFile, keyFile, certPool := selfSignedCert(t)
	cfg := testConfig()
	cfg.REST.InsecureHTTP = false
	cfg.REST.TLSCertFile = certFile
	cfg.REST.TLSKeyFile = keyFile
	server, _ := newTestServer(t, cfg)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = server.Serve(ctx, listener)
	}()
	defer func() { cancel(); wg.Wait() }()

	url := "https://" + listener.Addr().String() + "/health"
	trusting := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: certPool, MinVersion: tls.VersionTLS12}}}
	resp, err := trusting.Get(url) //nolint:noctx
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// A client that doesn't trust the certificate can't talk to it
	_, err = (&http.Client{Timeout: 2 * time.Second}).Get(url) //nolint:noctx
	require.Error(t, err)

	// Nor can a client that speaks plain HTTP
	plain, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + listener.Addr().String() + "/health") //nolint:noctx
	if err == nil {
		defer plain.Body.Close()
		require.Equal(t, http.StatusBadRequest, plain.StatusCode, "Go answers a plain request to a TLS port with a 400")
	}

	// TLS 1.1 is not accepted: the client is the one that enables it, so the refusal comes from the server
	_, err = tls.Dial("tcp", listener.Addr().String(), &tls.Config{
		RootCAs: certPool, MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11,
	})
	require.ErrorContains(t, err, "protocol version")

	// TLS 1.2 is
	conn, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{
		RootCAs: certPool, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12,
	})
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func TestWireFormat(t *testing.T) {
	// The request that the AggOracle sends is accepted
	var request SignRequest
	require.NoError(t, json.Unmarshal(goldenFile(t, "sign_request.json"), &request))
	hash, err := parseGER(request.GER)
	require.NoError(t, err)
	require.Equal(t, testGER, hash)

	// And the response and the errors are what the fixtures say
	response := SignResponse{GER: testGER, Signature: mustDecodeHex(t, goldenFile(t, "sign_response.json")), Signer: validator}
	require.JSONEq(t, string(goldenFile(t, "sign_response.json")), mustMarshal(t, response))

	for _, fixture := range []string{
		"error_invalid_request.json", "error_ger_unknown.json", "error_ger_block_not_canonical.json",
		"error_ger_not_final.json", "error_rate_limited.json", "error_internal.json",
	} {
		var errResp ErrorResponse
		require.NoError(t, json.Unmarshal(goldenFile(t, fixture), &errResp), fixture)
		require.JSONEq(t, string(goldenFile(t, fixture)), mustMarshal(t, errResp), fixture)
	}
}

func mustDecodeHex(t *testing.T, signResponseJSON []byte) hexutil.Bytes {
	t.Helper()
	var parsed struct {
		Signature hexutil.Bytes `json:"signature"`
	}
	require.NoError(t, json.Unmarshal(signResponseJSON, &parsed))
	return parsed.Signature
}
