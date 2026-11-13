package client

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/api"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/api/mocks"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/config"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/service"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// goldenDir has the golden fixtures that define the wire contract of the validator
var goldenDir = filepath.Join("..", "api", "testdata")

// fixture is the golden response: the signature of the GER of the fixtures with the private key 1
func fixture(t *testing.T) api.SignResponse {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(goldenDir, "sign_response.json"))
	require.NoError(t, err)
	var response api.SignResponse
	require.NoError(t, json.Unmarshal(data, &response))
	return response
}

// startValidator serves the real REST API of the validator, with a mock as its service, and returns its client
func startValidator(t *testing.T, mutate func(*config.Config)) (*Client, *mocks.Service) {
	t.Helper()
	cfg := config.Default()
	cfg.L1ChainID = 11155111
	cfg.PublicREST.InsecureHTTP = true
	cfg.PublicREST.MaxRequestsPerIPAndSecond = 0
	if mutate != nil {
		mutate(&cfg)
	}
	svc := mocks.NewService(t)
	server := httptest.NewServer(api.New(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg, svc).Handler())
	t.Cleanup(server.Close)

	client, err := New(Config{BaseURL: server.URL})
	require.NoError(t, err)
	return client, svc
}

func TestClient_SignGER(t *testing.T) {
	golden := fixture(t)
	client, svc := startValidator(t, nil)
	svc.EXPECT().SignGER(mock.Anything, golden.GER).
		Return(&service.Result{GER: golden.GER, Signature: golden.Signature, Signer: golden.Signer}, nil).Once()

	response, err := client.SignGER(context.Background(), golden.GER)
	require.NoError(t, err)
	require.Equal(t, golden, *response)
	require.NoError(t, Verify(golden.GER, response, nil))
	expected := golden.Signer
	require.NoError(t, Verify(golden.GER, response, &expected))
}

func TestClient_SignGER_ValidatorSaysNo(t *testing.T) {
	ger := common.HexToHash("0x01")
	tests := []struct {
		name       string
		serviceErr error
		status     int
		code       string
		retryable  bool
		retryAfter time.Duration
	}{
		{name: "unknown", serviceErr: service.ErrGERUnknown, status: http.StatusNotFound, code: api.CodeGERUnknown},
		{
			name: "not canonical", serviceErr: service.ErrGERBlockNotCanonical, status: http.StatusConflict,
			code: api.CodeGERBlockNotCanon,
		},
		{
			name: "not final", serviceErr: service.ErrGERNotFinal, status: http.StatusTooEarly,
			code: api.CodeGERNotFinal, retryable: true, retryAfter: 5 * time.Second,
		},
		{name: "invalid", serviceErr: service.ErrInvalidGER, status: http.StatusBadRequest, code: api.CodeInvalidRequest},
		{
			name: "internal", serviceErr: errors.New("l1 is down"), status: http.StatusInternalServerError,
			code: api.CodeInternal, retryable: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, svc := startValidator(t, nil)
			svc.EXPECT().SignGER(mock.Anything, ger).Return(nil, tc.serviceErr).Once()

			response, err := client.SignGER(context.Background(), ger)
			require.Nil(t, response)
			var apiErr *Error
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, tc.status, apiErr.Status)
			require.Equal(t, tc.code, apiErr.Code)
			require.NotEmpty(t, apiErr.Message)
			require.Equal(t, tc.retryable, apiErr.Retryable())
			require.Equal(t, tc.retryAfter, apiErr.RetryAfter)
			require.Contains(t, err.Error(), tc.code)
		})
	}
}

func TestClient_SignGER_RateLimited(t *testing.T) {
	ger := common.HexToHash("0x01")
	client, svc := startValidator(t, func(cfg *config.Config) {
		cfg.PublicREST.MaxRequestsPerIPAndSecond = 0.001
		cfg.PublicREST.MaxRequestsBurstPerIP = 1
	})
	svc.EXPECT().SignGER(mock.Anything, ger).Return(nil, service.ErrGERUnknown).Once()

	_, err := client.SignGER(context.Background(), ger)
	require.Error(t, err)

	_, err = client.SignGER(context.Background(), ger)
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, api.CodeRateLimited, apiErr.Code)
	require.True(t, apiErr.Retryable())
	require.Equal(t, time.Second, apiErr.RetryAfter)
}

func TestClient_Info(t *testing.T) {
	client, svc := startValidator(t, nil)
	signer := common.HexToAddress("0x7e5f4552091a69125d5dfcb7b8c2659029395bdf")
	ger := common.HexToAddress("0x1111111111111111111111111111111111111111")
	svc.EXPECT().Info().Return(service.Info{
		Signer: signer, AgglayerGER: ger, FinalityTag: "safe", FinalityNumBlks: 3,
	}).Once()

	info, err := client.Info(context.Background())
	require.NoError(t, err)
	require.Equal(t, signer, info.Signer)
	require.Equal(t, ger, info.AgglayerGER)
	require.Equal(t, uint64(11155111), info.L1ChainID)
	require.Equal(t, api.FinalityInfo{Tag: "safe", NumBlocks: 3}, info.Finality)
}

func TestClient_Health(t *testing.T) {
	client, _ := startValidator(t, nil)
	health, err := client.Health(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ok", health.Status)
}

func TestClient_NotAValidator(t *testing.T) {
	golden := fixture(t)
	handlers := map[string]http.HandlerFunc{
		"a wrong URL: a 404 that is not of the API": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "404 page not found\x1b[31m", http.StatusNotFound)
		},
		"a proxy": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, strings.Repeat("bad gateway ", 100), http.StatusBadGateway)
		},
		"a redirect is not followed": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://127.0.0.1:1/elsewhere", http.StatusFound)
		},
	}
	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL})
			require.NoError(t, err)

			_, err = client.SignGER(context.Background(), golden.GER)
			var apiErr *Error
			require.ErrorAs(t, err, &apiErr)
			require.Empty(t, apiErr.Code, "the code is only of the answers of the API")
			require.False(t, apiErr.Retryable())
			require.LessOrEqual(t, len(apiErr.Message), maxMessageLength+len("..."))
			require.NotContains(t, apiErr.Message, "\x1b", "no control characters in a terminal")
		})
	}

	t.Run("a 200 that is not the API", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>hello</html>"))
		}))
		t.Cleanup(server.Close)
		client, err := New(Config{BaseURL: server.URL})
		require.NoError(t, err)

		_, err = client.Info(context.Background())
		require.ErrorIs(t, err, ErrInvalidResponse)
	})

	t.Run("it signs another GER", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(golden)
		}))
		t.Cleanup(server.Close)
		client, err := New(Config{BaseURL: server.URL})
		require.NoError(t, err)

		_, err = client.SignGER(context.Background(), common.HexToHash("0x02"))
		require.ErrorIs(t, err, ErrInvalidResponse)
	})

	t.Run("a response that is too big", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("a", maxResponseBytes+1)))
		}))
		t.Cleanup(server.Close)
		client, err := New(Config{BaseURL: server.URL})
		require.NoError(t, err)

		_, err = client.Info(context.Background())
		require.ErrorIs(t, err, ErrInvalidResponse)
	})

	t.Run("nobody listens", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		url := server.URL
		server.Close()
		client, err := New(Config{BaseURL: url})
		require.NoError(t, err)

		_, err = client.Health(context.Background())
		require.ErrorContains(t, err, "request to the validator failed")
		var apiErr *Error
		require.NotErrorAs(t, err, &apiErr)
	})
}

func TestClient_Timeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	client, err := New(Config{BaseURL: server.URL, Timeout: 50 * time.Millisecond})
	require.NoError(t, err)

	_, err = client.Health(context.Background())
	require.ErrorContains(t, err, "request to the validator failed")
}

func TestVerify(t *testing.T) {
	golden := fixture(t)
	other := common.HexToAddress("0x02")

	t.Run("the signature of the golden fixture", func(t *testing.T) {
		require.NoError(t, Verify(golden.GER, &golden, nil))
	})

	t.Run("it is signed by the address that is expected", func(t *testing.T) {
		expected := golden.Signer
		require.NoError(t, Verify(golden.GER, &golden, &expected))
	})

	t.Run("it is signed by another address than the expected", func(t *testing.T) {
		err := Verify(golden.GER, &golden, &other)
		require.ErrorIs(t, err, ErrBadSignature)
		require.ErrorContains(t, err, "expected "+other.Hex())
	})

	t.Run("the response says another signer than the one that signed", func(t *testing.T) {
		lying := golden
		lying.Signer = other
		require.ErrorIs(t, Verify(golden.GER, &lying, nil), ErrBadSignature)
	})

	t.Run("the signature is of another GER", func(t *testing.T) {
		require.ErrorIs(t, Verify(common.HexToHash("0x03"), &golden, nil), ErrBadSignature)
	})

	t.Run("a signature that is not 65 bytes", func(t *testing.T) {
		short := golden
		short.Signature = golden.Signature[:64]
		err := Verify(golden.GER, &short, nil)
		require.ErrorIs(t, err, ErrBadSignature)
		require.ErrorContains(t, err, "64 bytes")
	})
}

func TestNew(t *testing.T) {
	for _, badURL := range []string{"", "localhost:8080", "ftp://localhost", "http://", "://x", "/v1"} {
		t.Run("invalid URL "+badURL, func(t *testing.T) {
			_, err := New(Config{BaseURL: badURL})
			require.ErrorContains(t, err, "invalid URL of the validator")
		})
	}

	t.Run("a CA file that doesn't exist", func(t *testing.T) {
		_, err := New(Config{BaseURL: "https://localhost", CACertFile: filepath.Join(t.TempDir(), "ca.pem")})
		require.ErrorContains(t, err, "failed to read the CA certificate file")
	})

	t.Run("a CA file without certificates", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ca.pem")
		require.NoError(t, os.WriteFile(path, []byte("not a certificate"), 0o600))
		_, err := New(Config{BaseURL: "https://localhost", CACertFile: path})
		require.ErrorContains(t, err, "no valid PEM certificates")
	})

	t.Run("a base URL with a path", func(t *testing.T) {
		var gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}))
		t.Cleanup(server.Close)
		client, err := New(Config{BaseURL: server.URL + "/validator/"})
		require.NoError(t, err)

		_, err = client.Health(context.Background())
		require.NoError(t, err)
		require.Equal(t, "/validator/health", gotPath, "a validator behind a reverse proxy with a prefix")
	})
}

func TestClient_TLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(server.Close)

	caFile := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	require.NoError(t, os.WriteFile(caFile, pemBytes, 0o600))

	t.Run("with the CA of the validator", func(t *testing.T) {
		client, err := New(Config{BaseURL: server.URL, CACertFile: caFile})
		require.NoError(t, err)
		_, err = client.Health(context.Background())
		require.NoError(t, err)
	})

	t.Run("without it the certificate is not trusted", func(t *testing.T) {
		client, err := New(Config{BaseURL: server.URL})
		require.NoError(t, err)
		_, err = client.Health(context.Background())
		require.ErrorContains(t, err, "request to the validator failed")
	})
}
