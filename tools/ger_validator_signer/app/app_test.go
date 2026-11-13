package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/app/mocks"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/config"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	agglayerGER = common.HexToAddress("0x1111111111111111111111111111111111111111")
	testGER     = common.HexToHash("0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	errRPC      = errors.New("rpc down")
)

func testConfig() config.Config {
	cfg := config.Default()
	cfg.L1RPCURL = "https://l1.example.com/s3cr3t-t0k3n"
	cfg.L1ChainID = 11155111
	cfg.AgglayerGERAddr = agglayerGER
	return cfg
}

func TestCheckL1(t *testing.T) {
	cfg := testConfig()
	zero := common.Hash{}

	tests := []struct {
		name    string
		setup   func(m *mocks.L1StartupReader)
		wantErr string
	}{
		{
			name: "everything is as configured",
			setup: func(m *mocks.L1StartupReader) {
				m.EXPECT().ChainID(mock.Anything).Return(11155111, nil)
				m.EXPECT().CodeAt(mock.Anything, agglayerGER).Return([]byte{0x60}, nil)
				m.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, zero).Return(zero, nil)
			},
		},
		{
			name:    "the chain id can't be read",
			setup:   func(m *mocks.L1StartupReader) { m.EXPECT().ChainID(mock.Anything).Return(0, errRPC) },
			wantErr: "failed to get the chain id of L1: ",
		},
		{
			name:    "another chain",
			setup:   func(m *mocks.L1StartupReader) { m.EXPECT().ChainID(mock.Anything).Return(1, nil) },
			wantErr: "is the chain 1, but L1ChainID is 11155111",
		},
		{
			name: "the code can't be read",
			setup: func(m *mocks.L1StartupReader) {
				m.EXPECT().ChainID(mock.Anything).Return(11155111, nil)
				m.EXPECT().CodeAt(mock.Anything, agglayerGER).Return(nil, errRPC)
			},
			wantErr: "failed to get the code of AgglayerGERAddr",
		},
		{
			name: "no contract at the address",
			setup: func(m *mocks.L1StartupReader) {
				m.EXPECT().ChainID(mock.Anything).Return(11155111, nil)
				m.EXPECT().CodeAt(mock.Anything, agglayerGER).Return(nil, nil)
			},
			wantErr: "there is no contract at AgglayerGERAddr",
		},
		{
			name: "a contract that is not AgglayerGER",
			setup: func(m *mocks.L1StartupReader) {
				m.EXPECT().ChainID(mock.Anything).Return(11155111, nil)
				m.EXPECT().CodeAt(mock.Anything, agglayerGER).Return([]byte{0x60}, nil)
				m.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, zero).Return(zero, errRPC)
			},
			wantErr: "is it the AgglayerGER contract?",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := mocks.NewL1StartupReader(t)
			tc.setup(reader)

			err := CheckL1(context.Background(), reader, cfg)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
			require.NotContains(t, err.Error(), "s3cr3t-t0k3n", "the token of the URL must not be in the errors")
		})
	}
}

func TestNewLogger(t *testing.T) {
	logConfig := func(environment, level string, outputs ...string) config.LogConfig {
		return config.LogConfig{Environment: environment, Level: level, Outputs: outputs}
	}

	t.Run("development: text, at the level", func(t *testing.T) {
		var out bytes.Buffer
		logger, err := NewLogger(logConfig(config.LogEnvironmentDevelopment, "warn", "stderr"), &out)
		require.NoError(t, err)
		logger.Info("not shown")
		logger.Warn("shown", "key", "value")
		require.NotContains(t, out.String(), "not shown")
		require.Contains(t, out.String(), "level=WARN msg=shown key=value")
	})

	t.Run("production: JSON", func(t *testing.T) {
		var out bytes.Buffer
		logger, err := NewLogger(logConfig(config.LogEnvironmentProduction, "info", "stderr"), &out)
		require.NoError(t, err)
		logger.Info("shown", "key", "value")
		var entry map[string]any
		require.NoError(t, json.Unmarshal(out.Bytes(), &entry))
		require.Equal(t, "INFO", entry["level"])
		require.Equal(t, "shown", entry["msg"])
		require.Equal(t, "value", entry["key"])
	})

	t.Run("to a file, appended, and to stderr", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "validator.log")
		require.NoError(t, os.WriteFile(path, []byte("previous\n"), 0o600))
		var out bytes.Buffer
		logger, err := NewLogger(logConfig(config.LogEnvironmentDevelopment, "info", path, "stderr"), &out)
		require.NoError(t, err)
		logger.Info("shown")
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(content), "previous\n"), "the file is appended")
		require.Contains(t, string(content), "msg=shown")
		require.Contains(t, out.String(), "msg=shown")
	})

	t.Run("errors", func(t *testing.T) {
		var out bytes.Buffer
		_, err := NewLogger(logConfig(config.LogEnvironmentDevelopment, "trace", "stderr"), &out)
		require.ErrorContains(t, err, `invalid log level "trace"`)
		_, err = NewLogger(logConfig(config.LogEnvironmentDevelopment, "info",
			filepath.Join(t.TempDir(), "missing", "validator.log")), &out)
		require.ErrorContains(t, err, "failed to open the log output")
	})
}

// fakeL1 is an L1 node: a chain of blocks, the AgglayerGER contract and the tags of the chain
type fakeL1 struct {
	chainID   uint64
	code      map[common.Address][]byte
	gers      map[common.Hash]common.Hash // GER -> hash of the block previous to the one that inserted it
	blocks    map[common.Hash]fakeBlock   // by hash: orphans included
	canonical map[uint64]common.Hash
	tags      map[string]uint64
	mu        sync.Mutex
	calls     map[string]int
}

type fakeBlock struct {
	number uint64
	hash   common.Hash
	parent common.Hash
}

func blockHash(n uint64, fork byte) common.Hash {
	return common.BytesToHash([]byte{fork, byte(n >> 8), byte(n)})
}

// newFakeL1 creates a chain of 120 blocks, where the GER was inserted in the block 101
func newFakeL1(t *testing.T) (*fakeL1, *httptest.Server) {
	t.Helper()
	l1 := &fakeL1{
		chainID:   11155111,
		code:      map[common.Address][]byte{agglayerGER: {0x60, 0x80}},
		gers:      map[common.Hash]common.Hash{},
		blocks:    map[common.Hash]fakeBlock{},
		canonical: map[uint64]common.Hash{},
		tags:      map[string]uint64{"latest": 120, "safe": 112, "finalized": 104},
		calls:     map[string]int{},
	}
	for n := uint64(0); n <= 120; n++ {
		block := fakeBlock{number: n, hash: blockHash(n, 1)}
		if n > 0 {
			block.parent = blockHash(n-1, 1)
		}
		l1.blocks[block.hash] = block
		l1.canonical[n] = block.hash
	}
	l1.gers[testGER] = blockHash(100, 1) // inserted in the block 101

	server := httptest.NewServer(http.HandlerFunc(l1.serve))
	t.Cleanup(server.Close)
	return l1, server
}

func (l *fakeL1) blockJSON(b fakeBlock) map[string]any {
	return map[string]any{"number": hexutil.Uint64(b.number), "hash": b.hash, "parentHash": b.parent}
}

func (l *fakeL1) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     uint64            `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	l.mu.Lock()
	l.calls[req.Method]++
	result := l.handle(req.Method, req.Params)
	l.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

func (l *fakeL1) handle(method string, params []json.RawMessage) any {
	switch method {
	case "eth_chainId":
		return hexutil.Uint64(l.chainID)
	case "eth_getCode":
		var addr common.Address
		_ = json.Unmarshal(params[0], &addr)
		return hexutil.Bytes(l.code[addr])
	case "eth_call":
		var msg struct {
			Data hexutil.Bytes `json:"data"`
		}
		_ = json.Unmarshal(params[0], &msg)
		ger := common.BytesToHash(msg.Data[4:])
		return l.gers[ger] // zero hash if it is not there
	case "eth_getBlockByHash":
		var hash common.Hash
		_ = json.Unmarshal(params[0], &hash)
		if block, ok := l.blocks[hash]; ok {
			return l.blockJSON(block)
		}
		return nil
	case "eth_getBlockByNumber":
		var tag string
		_ = json.Unmarshal(params[0], &tag)
		number, isTag := l.tags[tag]
		if !isTag {
			var n hexutil.Uint64
			_ = json.Unmarshal(params[0], &n)
			number = uint64(n)
		}
		if hash, ok := l.canonical[number]; ok {
			return l.blockJSON(l.blocks[hash])
		}
		return nil
	default:
		return nil
	}
}

func writeValidatorKeystore(t *testing.T) (path string, address common.Address) {
	t.Helper()
	keyBytes := make([]byte, 32)
	keyBytes[31] = 9
	key, err := crypto.ToECDSA(keyBytes)
	require.NoError(t, err)
	store := keystore.NewKeyStore(t.TempDir(), keystore.LightScryptN, keystore.LightScryptP)
	account, err := store.ImportECDSA(key, "testonly")
	require.NoError(t, err)
	return account.URL.Path, account.Address
}

func e2eConfig(t *testing.T, l1URL, keystorePath string, finalityTag string) config.Config {
	t.Helper()
	cfg := testConfig()
	cfg.L1RPCURL = l1URL
	cfg.BlockFinality = config.BlockFinality{Tag: finalityTag}
	cfg.Signer = config.SignerConfig{"Method": "local", "Path": keystorePath, "Password": "testonly"}
	cfg.PublicREST.InsecureHTTP = true
	cfg.PublicREST.MaxRequestsPerIPAndSecond = 0
	return cfg
}

// startValidator starts the validator and returns the URL of its API
func startValidator(t *testing.T, cfg config.Config) string {
	t.Helper()
	validator, err := New(context.Background(), cfg, testLogger())
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		require.NoError(t, validator.Serve(ctx, listener))
	}()
	t.Cleanup(func() { cancel(); wg.Wait() })
	return "http://" + listener.Addr().String()
}

func sign(t *testing.T, url string, ger common.Hash) (int, map[string]any, http.Header) {
	t.Helper()
	body := `{"ger":"` + ger.Hex() + `"}`
	resp, err := http.Post(url+"/v1/ger/sign", "application/json", strings.NewReader(body)) //nolint:noctx
	require.NoError(t, err)
	defer resp.Body.Close()
	var decoded map[string]any
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &decoded), string(data))
	return resp.StatusCode, decoded, resp.Header
}

// requireValidSignature checks the signature as AggOracleCommittee.injectGER does: 65 bytes, v 27 or 28, a low s,
// and the signer recovered from keccak256(GER) is the validator
func requireValidSignature(t *testing.T, ger common.Hash, signatureHex string, expectedSigner common.Address) {
	t.Helper()
	signature, err := hexutil.Decode(signatureHex)
	require.NoError(t, err)
	require.Len(t, signature, 65)
	require.Contains(t, []byte{27, 28}, signature[64])

	require.True(t, crypto.ValidateSignatureValues(signature[64]-27,
		new(bigInt).SetBytes(signature[:32]), new(bigInt).SetBytes(signature[32:64]), true), "r and s are valid and s is low")

	ethSignature := append([]byte(nil), signature...)
	ethSignature[64] -= 27
	pubKey, err := crypto.Ecrecover(crypto.Keccak256Hash(ger.Bytes()).Bytes(), ethSignature)
	require.NoError(t, err)
	require.Equal(t, expectedSigner, common.BytesToAddress(crypto.Keccak256(pubKey[1:])[12:]))
}

func TestEndToEnd_SignsAFinalGER(t *testing.T) {
	l1, l1Server := newFakeL1(t)
	keystorePath, validatorAddr := writeValidatorKeystore(t)
	url := startValidator(t, e2eConfig(t, l1Server.URL, keystorePath, "finalized"))

	status, body, _ := sign(t, url, testGER)

	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, testGER.Hex(), common.HexToHash(stringField(t, body, "ger")).Hex())
	require.Equal(t, strings.ToLower(validatorAddr.Hex()), body["signer"])
	requireValidSignature(t, testGER, stringField(t, body, "signature"), validatorAddr)

	// A second request is answered from the cache, without querying L1 again
	callsBefore := l1.calls["eth_call"]
	status, again, _ := sign(t, url, testGER)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, body["signature"], again["signature"])
	l1.mu.Lock()
	require.Equal(t, callsBefore, l1.calls["eth_call"])
	l1.mu.Unlock()
}

func TestEndToEnd_Info(t *testing.T) {
	_, l1Server := newFakeL1(t)
	keystorePath, validatorAddr := writeValidatorKeystore(t)
	cfg := e2eConfig(t, l1Server.URL, keystorePath, "latest")
	cfg.BlockFinality.NumBlocks = 6
	url := startValidator(t, cfg)

	resp, err := http.Get(url + "/v1/info") //nolint:noctx
	require.NoError(t, err)
	defer resp.Body.Close()
	var info map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&info))
	require.Equal(t, strings.ToLower(validatorAddr.Hex()), info["signer"])
	require.Equal(t, strings.ToLower(agglayerGER.Hex()), info["agglayer_ger"])
	require.InDelta(t, 11155111, info["l1_chain_id"], 0)
	require.Equal(t, map[string]any{"tag": "latest", "num_blocks": float64(6)}, info["finality"])
}

func TestEndToEnd_Rejections(t *testing.T) {
	t.Run("unknown GER", func(t *testing.T) {
		_, l1Server := newFakeL1(t)
		keystorePath, _ := writeValidatorKeystore(t)
		url := startValidator(t, e2eConfig(t, l1Server.URL, keystorePath, "finalized"))

		status, body, _ := sign(t, url, common.HexToHash("0x99"))
		require.Equal(t, http.StatusNotFound, status)
		require.Equal(t, "GER_UNKNOWN", body["code"])
	})

	t.Run("not final yet, and final later", func(t *testing.T) {
		l1, l1Server := newFakeL1(t)
		l1.tags["finalized"] = 100 // the GER was inserted in the block 101
		keystorePath, validatorAddr := writeValidatorKeystore(t)
		cfg := e2eConfig(t, l1Server.URL, keystorePath, "finalized")
		cfg.Cache.NegativeTTL = config.Duration{Duration: 0} // nothing negative is cached
		url := startValidator(t, cfg)

		status, body, headers := sign(t, url, testGER)
		require.Equal(t, http.StatusTooEarly, status)
		require.Equal(t, "GER_NOT_FINAL", body["code"])
		require.NotEmpty(t, headers.Get("Retry-After"))

		l1.mu.Lock()
		l1.tags["finalized"] = 101
		l1.mu.Unlock()
		status, body, _ = sign(t, url, testGER)
		require.Equal(t, http.StatusOK, status, body)
		requireValidSignature(t, testGER, stringField(t, body, "signature"), validatorAddr)
	})

	t.Run("latest minus 6 blocks", func(t *testing.T) {
		l1, l1Server := newFakeL1(t)
		l1.tags["latest"] = 106 // the final block is 100
		keystorePath, _ := writeValidatorKeystore(t)
		cfg := e2eConfig(t, l1Server.URL, keystorePath, "latest")
		cfg.BlockFinality.NumBlocks = 6
		cfg.Cache.NegativeTTL = config.Duration{Duration: 0}
		url := startValidator(t, cfg)

		status, body, _ := sign(t, url, testGER)
		require.Equal(t, http.StatusTooEarly, status, body)

		l1.mu.Lock()
		l1.tags["latest"] = 107 // the final block is 101
		l1.mu.Unlock()
		status, _, _ = sign(t, url, testGER)
		require.Equal(t, http.StatusOK, status)
	})

	t.Run("the block of the GER was reorged out", func(t *testing.T) {
		l1, l1Server := newFakeL1(t)
		// The chain of the node changed: the block 100 is another one now. The old one is an orphan the node still knows
		l1.canonical[100] = blockHash(100, 2)
		l1.blocks[blockHash(100, 2)] = fakeBlock{number: 100, hash: blockHash(100, 2), parent: blockHash(99, 1)}
		keystorePath, _ := writeValidatorKeystore(t)
		url := startValidator(t, e2eConfig(t, l1Server.URL, keystorePath, "latest"))

		status, body, _ := sign(t, url, testGER)
		require.Equal(t, http.StatusConflict, status)
		require.Equal(t, "GER_BLOCK_NOT_CANONICAL", body["code"])
	})
}

func TestNew_StartupFailures(t *testing.T) {
	keystorePath, _ := writeValidatorKeystore(t)

	t.Run("L1 is another chain", func(t *testing.T) {
		l1, l1Server := newFakeL1(t)
		l1.chainID = 1
		_, err := New(context.Background(), e2eConfig(t, l1Server.URL, keystorePath, "finalized"), testLogger())
		require.ErrorContains(t, err, "is the chain 1, but L1ChainID is 11155111")
	})

	t.Run("no contract at AgglayerGERAddr", func(t *testing.T) {
		l1, l1Server := newFakeL1(t)
		delete(l1.code, agglayerGER)
		_, err := New(context.Background(), e2eConfig(t, l1Server.URL, keystorePath, "finalized"), testLogger())
		require.ErrorContains(t, err, "there is no contract at AgglayerGERAddr")
	})

	t.Run("L1 is unreachable", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		url := server.URL
		server.Close()
		_, err := New(context.Background(), e2eConfig(t, url+"/s3cr3t-t0k3n", keystorePath, "finalized"), testLogger())
		require.ErrorContains(t, err, "failed to get the chain id of L1")
		require.NotContains(t, err.Error(), "s3cr3t-t0k3n")
	})

	t.Run("the signer can't be created", func(t *testing.T) {
		_, l1Server := newFakeL1(t)
		cfg := e2eConfig(t, l1Server.URL, keystorePath, "finalized")
		cfg.Signer["Password"] = "wrong"
		_, err := New(context.Background(), cfg, testLogger())
		require.Error(t, err)
	})
}

func TestRun_FailsToListen(t *testing.T) {
	_, l1Server := newFakeL1(t)
	keystorePath, _ := writeValidatorKeystore(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = occupied.Close() })

	cfg := e2eConfig(t, l1Server.URL, keystorePath, "finalized")
	cfg.PublicREST.Host = "127.0.0.1"
	tcpAddr, ok := occupied.Addr().(*net.TCPAddr)
	require.True(t, ok)
	cfg.PublicREST.Port = uint16(tcpAddr.Port)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = Run(ctx, cfg, testLogger())
	require.ErrorContains(t, err, "failed to listen")
}
