package gervalidator

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
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func readGoldenRaw(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(goldenDir, name))
	require.NoError(t, err)
	return data
}

func newHTTPClient(t *testing.T, cfg ClientConfig) *HTTPClient {
	t.Helper()
	client, err := NewHTTPClient(cfg)
	require.NoError(t, err)
	return client
}

func serveJSON(status int, body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
}

func startServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func TestHTTPClient_SignGER(t *testing.T) {
	var expectedResp SignResponse
	readGolden(t, "sign_response.json", &expectedResp)

	server := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/ger/sign", r.URL.Path)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.Equal(t, "application/json", r.Header.Get("Accept"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		// The request is exactly the one of the golden fixture
		require.JSONEq(t, string(readGoldenRaw(t, "sign_request.json")), string(body))

		serveJSON(http.StatusOK, readGoldenRaw(t, "sign_response.json"))(w, r)
	}))

	client := newHTTPClient(t, ClientConfig{RequestTimeout: testTimeout})
	resp, err := client.SignGER(context.Background(), server.URL, expectedResp.GER)
	require.NoError(t, err)
	require.Equal(t, &expectedResp, resp)

	// And what the client returns is a signature the collector accepts
	_, signer, err := RecoverSigner(resp.GER, resp.Signature)
	require.NoError(t, err)
	require.Equal(t, expectedResp.Signer, signer)
}

func TestHTTPClient_SignGER_URLs(t *testing.T) {
	var requestedPath string
	server := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		serveJSON(http.StatusOK, readGoldenRaw(t, "sign_response.json"))(w, r)
	}))
	client := newHTTPClient(t, ClientConfig{})

	tests := map[string]string{
		server.URL:                "/v1/ger/sign",
		server.URL + "/":          "/v1/ger/sign",
		server.URL + "/api":       "/api/v1/ger/sign",
		server.URL + "/api/":      "/api/v1/ger/sign",
		server.URL + "/a/b/c/":    "/a/b/c/v1/ger/sign",
		server.URL + "/with%20sp": "/with sp/v1/ger/sign",
	}
	for baseURL, expectedPath := range tests {
		_, err := client.SignGER(context.Background(), baseURL, testGER)
		require.NoError(t, err, baseURL)
		require.Equal(t, expectedPath, requestedPath, baseURL)
	}
}

func TestHTTPClient_SignGER_InvalidURL(t *testing.T) {
	client := newHTTPClient(t, ClientConfig{})
	for _, baseURL := range []string{"", "ftp://validator.example.com", "validator.example.com", "://bad", "http://"} {
		_, err := client.SignGER(context.Background(), baseURL, testGER)
		require.ErrorIs(t, err, ErrNoURL, "url %q", baseURL)
		require.Equal(t, FailureInvalid, ClassifyFailure(err))
	}
}

func TestHTTPClient_SignGER_ErrorResponses(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		status   int
		expected error
		kind     FailureKind
	}{
		{"GER unknown", "error_ger_unknown.json", http.StatusNotFound, ErrGERUnknown, FailureGERRejected},
		{"block not canonical", "error_ger_block_not_canonical.json", http.StatusConflict, ErrGERNotCanonical, FailureGERRejected},
		{"not final", "error_ger_not_final.json", http.StatusTooEarly, ErrGERNotFinal, FailureTransient},
		{"rate limited", "error_rate_limited.json", http.StatusTooManyRequests, ErrRateLimited, FailureTransient},
		{"invalid request", "error_invalid_request.json", http.StatusBadRequest, ErrBadRequest, FailureInvalid},
		// The code of the body decides, not the HTTP status
		{"code with an unexpected status", "error_ger_not_final.json", http.StatusServiceUnavailable, ErrGERNotFinal, FailureTransient},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := startServer(t, serveJSON(tc.status, readGoldenRaw(t, tc.fixture)))
			client := newHTTPClient(t, ClientConfig{})

			resp, err := client.SignGER(context.Background(), server.URL, testGER)
			require.Nil(t, resp)
			require.ErrorIs(t, err, tc.expected)
			require.Equal(t, tc.kind, ClassifyFailure(err))
			var errResp ErrorResponse
			readGolden(t, tc.fixture, &errResp)
			require.ErrorContains(t, err, errResp.Message)
		})
	}
}

func TestHTTPClient_SignGER_ErrorsWithoutKnownCode(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		expected   error // nil means *APIError
		notUnknown bool
		code       string
	}{
		{name: "429 without body", status: http.StatusTooManyRequests, expected: ErrRateLimited},
		{name: "400 without body", status: http.StatusBadRequest, expected: ErrBadRequest},
		// A 404 without the GER_UNKNOWN code is a wrong URL, not an unknown GER
		{name: "404 without body", status: http.StatusNotFound, notUnknown: true},
		{name: "404 html page", status: http.StatusNotFound, body: "<html>not found</html>", notUnknown: true},
		{name: "500 internal", status: http.StatusInternalServerError, body: `{"code":"INTERNAL","message":"oops"}`, code: "INTERNAL"},
		{name: "502 gateway", status: http.StatusBadGateway, body: "bad gateway"},
		{name: "unknown code", status: http.StatusTeapot, body: `{"code":"WHATEVER","message":"m"}`, code: "WHATEVER"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := startServer(t, serveJSON(tc.status, []byte(tc.body)))
			client := newHTTPClient(t, ClientConfig{})

			_, err := client.SignGER(context.Background(), server.URL, testGER)
			require.Error(t, err)
			if tc.expected != nil {
				require.ErrorIs(t, err, tc.expected)
				return
			}
			var apiErr *APIError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, tc.status, apiErr.StatusCode)
			require.Equal(t, tc.code, apiErr.Code)
			require.NotErrorIs(t, err, ErrGERUnknown)
			require.Equal(t, FailureTransient, ClassifyFailure(err))
		})
	}
}

func TestAPIError_MessageIsTruncated(t *testing.T) {
	err := &APIError{StatusCode: 500, Code: "INTERNAL", Message: strings.Repeat("x", 5000)}
	require.Less(t, len(err.Error()), 400)
	require.Contains(t, err.Error(), "...")
}

func TestHTTPClient_SignGER_InvalidSuccessResponses(t *testing.T) {
	tests := map[string]string{
		"not json":        "this is not json",
		"empty body":      "",
		"bad signature":   `{"ger":"0x01","signature":"0xzz"}`,
		"ger is a number": `{"ger":1,"signature":"0x01"}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			server := startServer(t, serveJSON(http.StatusOK, []byte(body)))
			client := newHTTPClient(t, ClientConfig{})

			_, err := client.SignGER(context.Background(), server.URL, testGER)
			require.ErrorIs(t, err, ErrInvalidResponse)
			require.Equal(t, FailureInvalid, ClassifyFailure(err))
		})
	}
}

func TestHTTPClient_SignGER_ResponseTooBig(t *testing.T) {
	server := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("a", maxResponseBytes+1)))
	}))
	client := newHTTPClient(t, ClientConfig{})

	_, err := client.SignGER(context.Background(), server.URL, testGER)
	require.ErrorIs(t, err, ErrInvalidResponse)
	require.ErrorContains(t, err, "bigger than")
}

// A validator must answer by itself: a redirect is never followed
func TestHTTPClient_SignGER_DoesNotFollowRedirects(t *testing.T) {
	var targetHit bool
	target := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHit = true
		serveJSON(http.StatusOK, readGoldenRaw(t, "sign_response.json"))(w, r)
	}))
	redirector := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/ger/sign", http.StatusTemporaryRedirect)
	}))
	client := newHTTPClient(t, ClientConfig{})

	_, err := client.SignGER(context.Background(), redirector.URL, testGER)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusTemporaryRedirect, apiErr.StatusCode)
	require.False(t, targetHit, "the redirect must not be followed")
}

func TestHTTPClient_SignGER_Timeouts(t *testing.T) {
	release := make(chan struct{})
	server := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-release:
		case <-time.After(testTimeout):
		}
	}))
	t.Cleanup(func() { close(release) })

	t.Run("request timeout", func(t *testing.T) {
		client := newHTTPClient(t, ClientConfig{RequestTimeout: 50 * time.Millisecond})
		start := time.Now()
		_, err := client.SignGER(context.Background(), server.URL, testGER)
		require.Error(t, err)
		require.Less(t, time.Since(start), testTimeout)
		require.Equal(t, FailureTransient, ClassifyFailure(err))
	})

	t.Run("cancelled context", func(t *testing.T) {
		client := newHTTPClient(t, ClientConfig{})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := client.SignGER(ctx, server.URL, testGER)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, FailureTransient, ClassifyFailure(err))
	})

	t.Run("connection refused", func(t *testing.T) {
		closed := httptest.NewServer(http.NotFoundHandler())
		url := closed.URL
		closed.Close()
		client := newHTTPClient(t, ClientConfig{})
		_, err := client.SignGER(context.Background(), url, testGER)
		require.Error(t, err)
		require.Equal(t, FailureTransient, ClassifyFailure(err))
	})
}

func writeCertPEM(t *testing.T, path string, der []byte) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
}

func TestHTTPClient_TLS(t *testing.T) {
	server := httptest.NewTLSServer(serveJSON(http.StatusOK, readGoldenRaw(t, "sign_response.json")))
	t.Cleanup(server.Close)

	t.Run("a certificate that is not trusted is rejected", func(t *testing.T) {
		client := newHTTPClient(t, ClientConfig{})
		_, err := client.SignGER(context.Background(), server.URL, testGER)
		require.Error(t, err)
		require.ErrorContains(t, err, "certificate")
	})

	t.Run("a certificate from the configured CA is accepted", func(t *testing.T) {
		caFile := filepath.Join(t.TempDir(), "ca.pem")
		writeCertPEM(t, caFile, server.Certificate().Raw)
		client := newHTTPClient(t, ClientConfig{CACertFile: caFile})

		resp, err := client.SignGER(context.Background(), server.URL, testGER)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

// newClientCert creates a self-signed client certificate and writes it and its key in PEM files
func newClientCert(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "aggoracle-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	certFile = filepath.Join(dir, "client.pem")
	keyFile = filepath.Join(dir, "client.key")
	writeCertPEM(t, certFile, der)
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600))
	return certFile, keyFile
}

func TestHTTPClient_MutualTLS(t *testing.T) {
	var peerCerts int
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peerCerts = len(r.TLS.PeerCertificates)
		serveJSON(http.StatusOK, readGoldenRaw(t, "sign_response.json"))(w, r)
	}))
	server.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)

	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")
	writeCertPEM(t, caFile, server.Certificate().Raw)

	t.Run("without a client certificate the server rejects the connection", func(t *testing.T) {
		client := newHTTPClient(t, ClientConfig{CACertFile: caFile})
		_, err := client.SignGER(context.Background(), server.URL, testGER)
		require.Error(t, err)
	})

	t.Run("with a client certificate", func(t *testing.T) {
		certFile, keyFile := newClientCert(t, dir)
		client := newHTTPClient(t, ClientConfig{CACertFile: caFile, ClientCertFile: certFile, ClientKeyFile: keyFile})
		resp, err := client.SignGER(context.Background(), server.URL, testGER)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 1, peerCerts)
	})
}

func TestNewHTTPClient_InvalidTLSConfig(t *testing.T) {
	dir := t.TempDir()
	notPEM := filepath.Join(dir, "not.pem")
	require.NoError(t, os.WriteFile(notPEM, []byte("not a pem file"), 0o600))
	certFile, keyFile := newClientCert(t, dir)

	tests := []struct {
		name    string
		cfg     ClientConfig
		wantErr string
	}{
		{"missing CA file", ClientConfig{CACertFile: filepath.Join(dir, "missing.pem")}, "failed to read the CA certificate file"},
		{"CA file without certificates", ClientConfig{CACertFile: notPEM}, "no valid PEM certificates"},
		{"client cert without key", ClientConfig{ClientCertFile: certFile}, "must be set together"},
		{"client key without cert", ClientConfig{ClientKeyFile: keyFile}, "must be set together"},
		{"client cert that is not a PEM", ClientConfig{ClientCertFile: notPEM, ClientKeyFile: keyFile}, "failed to load the client certificate"},
		{"client cert and key from different files", ClientConfig{ClientCertFile: certFile, ClientKeyFile: notPEM}, "failed to load the client certificate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewHTTPClient(tc.cfg)
			require.ErrorContains(t, err, tc.wantErr)
			require.Nil(t, client)
		})
	}
}

// The wire format of the request and the responses is the one of the golden fixtures, which the validator
// module (ger_validator_signer) must produce and accept too
func TestWireFormatMatchesGoldenFixtures(t *testing.T) {
	var req SignRequest
	readGolden(t, "sign_request.json", &req)
	encoded, err := json.Marshal(req)
	require.NoError(t, err)
	require.JSONEq(t, string(readGoldenRaw(t, "sign_request.json")), string(encoded))

	var resp SignResponse
	readGolden(t, "sign_response.json", &resp)
	encoded, err = json.Marshal(resp)
	require.NoError(t, err)
	require.JSONEq(t, string(readGoldenRaw(t, "sign_response.json")), string(encoded))

	for _, fixture := range []string{
		"error_invalid_request.json", "error_ger_unknown.json", "error_ger_block_not_canonical.json",
		"error_ger_not_final.json", "error_rate_limited.json", "error_internal.json",
	} {
		var errResp ErrorResponse
		readGolden(t, fixture, &errResp)
		require.NotEmpty(t, errResp.Code, fixture)
		require.NotEmpty(t, errResp.Message, fixture)
		encoded, err = json.Marshal(errResp)
		require.NoError(t, err)
		require.JSONEq(t, string(readGoldenRaw(t, fixture)), string(encoded), fixture)
	}
}
