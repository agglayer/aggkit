package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/api"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/api/mocks"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/client"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/config"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/service"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// startValidator serves the real REST API of the validator, with a mock as its service, and returns its URL
func startValidator(t *testing.T, mutate func(*config.Config)) (string, *mocks.Service) {
	t.Helper()
	cfg := config.Default()
	cfg.L1ChainID = 11155111
	cfg.REST.InsecureHTTP = true
	cfg.RateLimit.RequestsPerSecond = 0
	if mutate != nil {
		mutate(&cfg)
	}
	svc := mocks.NewService(t)
	server := httptest.NewServer(api.New(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg, svc).Handler())
	t.Cleanup(server.Close)
	return server.URL, svc
}

// golden is the response of the golden fixture: its GER, signed with the private key 1
func golden(t *testing.T) api.SignResponse {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "api", "testdata", "sign_response.json"))
	require.NoError(t, err)
	var response api.SignResponse
	require.NoError(t, json.Unmarshal(data, &response))
	return response
}

// goldenResult is the golden response as what the service answers, and as what the API answers
func goldenResult(t *testing.T) (*service.Result, api.SignResponse) {
	t.Helper()
	response := golden(t)
	return &service.Result{GER: response.GER, Signature: response.Signature, Signer: response.Signer}, response
}

func TestClient_Usage(t *testing.T) {
	t.Run("no command", func(t *testing.T) {
		code, stdout, stderr := execute(t, "client")
		require.Equal(t, exitUsage, code)
		require.Empty(t, stdout)
		require.Contains(t, stderr, "ger_validator_signer client sign")
	})

	t.Run("unknown command", func(t *testing.T) {
		code, _, stderr := execute(t, "client", "attack")
		require.Equal(t, exitUsage, code)
		require.Contains(t, stderr, `unknown client command "attack"`)
	})

	for _, arg := range []string{"help", "-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			code, stdout, stderr := execute(t, "client", arg)
			require.Equal(t, exitOK, code)
			require.Contains(t, stdout, "--expect-signer")
			require.Empty(t, stderr)
		})
	}

	t.Run("the help of a command", func(t *testing.T) {
		code, _, stderr := execute(t, "client", "sign", "-h")
		require.Equal(t, exitOK, code)
		require.Contains(t, stderr, "-ger", "the flags are printed by the flag package, to stderr")
	})

	t.Run("the main help mentions the client", func(t *testing.T) {
		_, stdout, _ := execute(t, "help")
		require.Contains(t, stdout, "ger_validator_signer client")
	})
}

func TestClient_SignArguments(t *testing.T) {
	ger := golden(t).GER.Hex()
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "the GER is required", args: []string{}, wantErr: "the flag --ger is required"},
		{name: "the GER is not hex", args: []string{"--ger", "hello"}, wantErr: "--ger must be 32 bytes"},
		{name: "the GER is short", args: []string{"--ger", "0x1234"}, wantErr: "--ger must be 32 bytes"},
		{name: "the GER has no prefix", args: []string{"--ger", ger[2:]}, wantErr: "--ger must be 32 bytes"},
		{name: "the signer is not an address", args: []string{"--ger", ger, "--expect-signer", "0x12"},
			wantErr: "--expect-signer must be an address"},
		{name: "no requests", args: []string{"--ger", ger, "--count", "0"}, wantErr: "--count must be between"},
		{name: "too many requests", args: []string{"--ger", ger, "--count", "100001"}, wantErr: "--count must be between"},
		{name: "no parallelism", args: []string{"--ger", ger, "--parallel", "0"}, wantErr: "--parallel must be between"},
		{name: "too much parallelism", args: []string{"--ger", ger, "--parallel", "1001"},
			wantErr: "--parallel must be between"},
		{name: "the GER as an argument", args: []string{ger}, wantErr: "unexpected argument"},
		{name: "an unknown flag", args: []string{"--ger", ger, "--bogus"}, wantErr: "flag provided but not defined"},
		{name: "the URL is invalid", args: []string{"--ger", ger, "--url", "localhost"},
			wantErr: "invalid URL of the validator"},
		{name: "the CA doesn't exist", args: []string{"--ger", ger, "--url", "https://localhost", "--ca-cert", "/no/ca.pem"},
			wantErr: "failed to read the CA certificate file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := execute(t, append([]string{"client", "sign"}, tc.args...)...)
			require.Equal(t, exitUsage, code)
			require.Empty(t, stdout)
			require.Contains(t, stderr, tc.wantErr)
		})
	}
}

func TestClient_Health(t *testing.T) {
	url, _ := startValidator(t, nil)

	t.Run("it runs", func(t *testing.T) {
		code, stdout, stderr := execute(t, "client", "health", "--url", url)
		require.Equal(t, exitOK, code)
		require.Equal(t, "ok\n", stdout)
		require.Empty(t, stderr)
	})

	t.Run("json", func(t *testing.T) {
		code, stdout, _ := execute(t, "client", "health", "--url", url, "--json")
		require.Equal(t, exitOK, code)
		var health api.HealthResponse
		require.NoError(t, json.Unmarshal([]byte(stdout), &health))
		require.Equal(t, "ok", health.Status)
	})

	t.Run("it doesn't run", func(t *testing.T) {
		closed := httptest.NewServer(nil)
		closedURL := closed.URL
		closed.Close()

		code, stdout, stderr := execute(t, "client", "health", "--url", closedURL)
		require.Equal(t, exitError, code)
		require.Empty(t, stdout)
		require.Contains(t, stderr, "request to the validator failed")

		code, stdout, _ = execute(t, "client", "health", "--url", closedURL, "--json")
		require.Equal(t, exitError, code)
		var failed struct {
			Error errorJSON `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(stdout), &failed))
		require.Equal(t, outcomeNoAnswer, failed.Error.Outcome)
	})
}

func TestClient_URLFromTheEnvironment(t *testing.T) {
	url, _ := startValidator(t, nil)
	t.Setenv(urlEnvVar, url)

	code, stdout, _ := execute(t, "client", "health")
	require.Equal(t, exitOK, code)
	require.Equal(t, "ok\n", stdout)

	t.Run("the flag wins", func(t *testing.T) {
		code, _, stderr := execute(t, "client", "health", "--url", "http://127.0.0.1:1")
		require.Equal(t, exitError, code)
		require.Contains(t, stderr, "127.0.0.1:1")
	})
}

func TestClient_Info(t *testing.T) {
	url, svc := startValidator(t, nil)
	signer := common.HexToAddress("0x7e5f4552091a69125d5dfcb7b8c2659029395bdf")
	ger := common.HexToAddress("0x1111111111111111111111111111111111111111")
	svc.EXPECT().Info().Return(service.Info{
		Signer: signer, AgglayerGER: ger, FinalityTag: "safe", FinalityNumBlks: 3,
	}).Times(2)

	code, stdout, stderr := execute(t, "client", "info", "--url", url)
	require.Equal(t, exitOK, code)
	require.Empty(t, stderr)
	require.Contains(t, stdout, signer.Hex())
	require.Contains(t, stdout, ger.Hex())
	require.Contains(t, stdout, "11155111")
	require.Contains(t, stdout, "safe, 3 blocks")

	code, stdout, _ = execute(t, "client", "info", "--url", url, "--json")
	require.Equal(t, exitOK, code)
	var info api.InfoResponse
	require.NoError(t, json.Unmarshal([]byte(stdout), &info))
	require.Equal(t, signer, info.Signer)
	require.Equal(t, api.FinalityInfo{Tag: "safe", NumBlocks: 3}, info.Finality)
}

func TestClient_Sign(t *testing.T) {
	result, response := goldenResult(t)

	t.Run("it signs and the signature is verified", func(t *testing.T) {
		url, svc := startValidator(t, nil)
		svc.EXPECT().SignGER(mock.Anything, response.GER).Return(result, nil).Once()

		code, stdout, stderr := execute(t, "client", "sign", "--url", url, "--ger", response.GER.Hex(),
			"--expect-signer", response.Signer.Hex())
		require.Equal(t, exitOK, code)
		require.Empty(t, stderr)
		require.Contains(t, stdout, response.Signer.Hex())
		require.Contains(t, stdout, response.Signature.String())
		require.Contains(t, stdout, "Verified:   yes: v 28", "the fixture ends in 0x1c")
	})

	t.Run("json", func(t *testing.T) {
		url, svc := startValidator(t, nil)
		svc.EXPECT().SignGER(mock.Anything, response.GER).Return(result, nil).Once()

		code, stdout, _ := execute(t, "client", "sign", "--url", url, "--ger", response.GER.Hex(), "--json")
		require.Equal(t, exitOK, code)
		var out signJSON
		require.NoError(t, json.Unmarshal([]byte(stdout), &out))
		require.Equal(t, response.GER, out.GER)
		require.Equal(t, response.Signature, out.Signature)
		require.Equal(t, response.Signer, *out.Signer)
		require.True(t, out.Verified)
		require.Nil(t, out.Error)
	})

	t.Run("the signer is not the expected one", func(t *testing.T) {
		url, svc := startValidator(t, nil)
		svc.EXPECT().SignGER(mock.Anything, response.GER).Return(result, nil).Twice()
		other := common.HexToAddress("0x02")

		code, stdout, stderr := execute(t, "client", "sign", "--url", url, "--ger", response.GER.Hex(),
			"--expect-signer", other.Hex())
		require.Equal(t, exitError, code)
		require.Empty(t, stdout)
		require.Contains(t, stderr, "bad signature")
		require.Contains(t, stderr, "expected "+other.Hex())

		code, stdout, _ = execute(t, "client", "sign", "--url", url, "--ger", response.GER.Hex(),
			"--expect-signer", other.Hex(), "--json")
		require.Equal(t, exitError, code)
		var out signJSON
		require.NoError(t, json.Unmarshal([]byte(stdout), &out))
		require.False(t, out.Verified, "the signature was received but it is not what was asked")
		require.Equal(t, outcomeBadSignature, out.Error.Outcome)
	})

	t.Run("a signature that is not of that signer", func(t *testing.T) {
		url, svc := startValidator(t, nil)
		forged := &service.Result{GER: response.GER, Signature: result.Signature, Signer: common.HexToAddress("0x03")}
		svc.EXPECT().SignGER(mock.Anything, response.GER).Return(forged, nil).Once()

		code, _, stderr := execute(t, "client", "sign", "--url", url, "--ger", response.GER.Hex())
		require.Equal(t, exitError, code)
		require.Contains(t, stderr, "bad signature")
	})
}

func TestClient_Sign_ValidatorSaysNo(t *testing.T) {
	ger := common.HexToHash("0x01").Hex()
	tests := []struct {
		name          string
		serviceErr    error
		outcome       string
		retryable     bool
		wantRetryHint string
	}{
		{name: "unknown", serviceErr: service.ErrGERUnknown, outcome: api.CodeGERUnknown},
		{
			name: "not final", serviceErr: service.ErrGERNotFinal, outcome: api.CodeGERNotFinal, retryable: true,
			wantRetryHint: "it can be retried later, the validator asks to wait 5s",
		},
		{name: "internal", serviceErr: errors.New("boom"), outcome: api.CodeInternal, retryable: true,
			wantRetryHint: "it can be retried later"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url, svc := startValidator(t, nil)
			svc.EXPECT().SignGER(mock.Anything, common.HexToHash(ger)).Return(nil, tc.serviceErr).Twice()

			code, stdout, stderr := execute(t, "client", "sign", "--url", url, "--ger", ger)
			require.Equal(t, exitError, code)
			require.Empty(t, stdout)
			require.Contains(t, stderr, "was not signed")
			require.Contains(t, stderr, tc.outcome)
			if tc.wantRetryHint != "" {
				require.Contains(t, stderr, tc.wantRetryHint)
			} else {
				require.NotContains(t, stderr, "retried")
			}

			code, stdout, _ = execute(t, "client", "sign", "--url", url, "--ger", ger, "--json")
			require.Equal(t, exitError, code)
			var out signJSON
			require.NoError(t, json.Unmarshal([]byte(stdout), &out))
			require.False(t, out.Verified)
			require.Nil(t, out.Signer)
			require.Equal(t, tc.outcome, out.Error.Outcome)
			require.Equal(t, tc.retryable, out.Error.Retryable)
		})
	}
}

func TestClient_Sign_Burst(t *testing.T) {
	result, response := goldenResult(t)

	t.Run("everything is signed", func(t *testing.T) {
		url, svc := startValidator(t, nil)
		svc.EXPECT().SignGER(mock.Anything, response.GER).Return(result, nil).Times(20)

		code, stdout, stderr := execute(t, "client", "sign", "--url", url, "--ger", response.GER.Hex(),
			"--count", "20", "--parallel", "4")
		require.Equal(t, exitOK, code)
		require.Empty(t, stderr)
		require.Contains(t, stdout, "20 of 20 requests, 4 at a time")
		require.Contains(t, stdout, "signed")
		require.Contains(t, stdout, "latency: min")
	})

	t.Run("the rate limit says no to the rest", func(t *testing.T) {
		url, svc := startValidator(t, func(cfg *config.Config) {
			cfg.RateLimit.RequestsPerSecond = 0.001
			cfg.RateLimit.Burst = 5
		})
		svc.EXPECT().SignGER(mock.Anything, response.GER).Return(result, nil).Times(5)

		code, stdout, _ := execute(t, "client", "sign", "--url", url, "--ger", response.GER.Hex(),
			"--count", "20", "--parallel", "4", "--json")
		require.Equal(t, exitError, code, "not everything was signed")
		var out burstJSON
		require.NoError(t, json.Unmarshal([]byte(stdout), &out))
		require.Equal(t, 20, out.Requests)
		require.Equal(t, 20, out.Done)
		require.Equal(t, 4, out.Parallel)
		require.False(t, out.Interrupted)
		require.Equal(t, map[string]int{outcomeSigned: 5, api.CodeRateLimited: 15}, out.Outcomes)
		require.LessOrEqual(t, out.MinMS, out.AvgMS)
		require.LessOrEqual(t, out.AvgMS, out.MaxMS)
	})

	t.Run("the outcomes are listed, the most frequent first", func(t *testing.T) {
		url, svc := startValidator(t, func(cfg *config.Config) {
			cfg.RateLimit.RequestsPerSecond = 0.001
			cfg.RateLimit.Burst = 2
		})
		svc.EXPECT().SignGER(mock.Anything, response.GER).Return(result, nil).Times(2)

		code, stdout, _ := execute(t, "client", "sign", "--url", url, "--ger", response.GER.Hex(), "--count", "10")
		require.Equal(t, exitError, code)
		require.Regexp(t, `(?s)RATE_LIMITED +8\n.*signed +2\n`, stdout)
	})
}

// A burst that the user interrupts stops sending, and says so
func TestRunBurst_Interrupted(t *testing.T) {
	url, _ := startValidator(t, nil)
	validatorClient, err := client.New(client.Config{BaseURL: url})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	burst := runBurst(ctx, validatorClient, signRequest{ger: common.HexToHash("0x01")},
		&signFlags{count: 1000, parallel: 1})
	require.True(t, burst.interrupted)
	require.Less(t, burst.done, burst.count)
	require.Equal(t, exitError, reportBurst(false, io.Discard, io.Discard, burst))
}

func TestOutcomeOf(t *testing.T) {
	require.Equal(t, outcomeSigned, outcomeOf(nil))
	require.Equal(t, "GER_UNKNOWN", outcomeOf(&client.Error{Status: 404, Code: "GER_UNKNOWN"}))
	require.Equal(t, "HTTP 502", outcomeOf(&client.Error{Status: 502}), "the answer of a proxy has no code")
	require.Equal(t, outcomeBadSignature, outcomeOf(client.ErrBadSignature))
	require.Equal(t, outcomeInvalidResponse, outcomeOf(client.ErrInvalidResponse))
	require.Equal(t, outcomeNoAnswer, outcomeOf(errors.New("connection refused")))
}
