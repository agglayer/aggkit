package exit_certificate

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// newStubRPCServer returns a fast JSON-RPC server that answers the read-only calls the off-chain
// Step G2 path makes (eth_call for getRoot/gas-token, eth_chainId, eth_blockNumber) with zero values,
// so readLocalExitRoot/fetchGasTokenInfo succeed instantly instead of retrying with backoff.
func newStubRPCServer(t *testing.T) string {
	t.Helper()
	zeroWord := "0x0000000000000000000000000000000000000000000000000000000000000000"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		var result string
		switch req.Method {
		case "eth_call":
			result = zeroWord
		case "eth_chainId", "eth_blockNumber":
			result = "0x1"
		default:
			result = "0x"
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// makeG1LiteDB creates a Step G1 lite DB in cfg's output dir holding `genesis` bridges with
// contiguous deposit counts 0..genesis-1, leaving the exit tree unbuilt (as Step G1 does).
func makeG1LiteDB(t *testing.T, cfg *Config, genesis int) {
	t.Helper()
	require.NoError(t, os.MkdirAll(cfg.Options.OutputDir, 0o755))
	syncer, err := bridgesyncerlite.New(context.Background(),
		bridgesyncerlite.Config{DBPath: g1LiteDBPath(cfg)}, log.WithFields("module", "test"))
	require.NoError(t, err)
	defer func() { require.NoError(t, syncer.Close()) }()

	leaves := make([]bridgesyncerlite.BridgeLeaf, genesis)
	for i := range genesis {
		leaves[i] = bridgesyncerlite.BridgeLeaf{
			BlockNum:           uint64(10 + i),
			BlockPos:           uint64(i),
			OriginAddress:      common.BytesToAddress([]byte{byte(i + 1)}),
			DestinationNetwork: 1,
			DestinationAddress: common.BytesToAddress([]byte{byte(i + 100)}),
			Amount:             big.NewInt(int64(i) * 10),
			DepositCount:       uint32(i),
		}
	}
	require.NoError(t, syncer.StoreBridges(context.Background(), leaves))
}

func testConfig(t *testing.T) *Config {
	t.Helper()
	return &Config{
		Options: Options{OutputDir: t.TempDir()},
	}
}

func TestLiteDBPaths(t *testing.T) {
	t.Parallel()
	cfg := &Config{Options: Options{OutputDir: "/tmp/out"}}
	require.Equal(t, filepath.Join("/tmp/out", "step-g1-l2bridgesyncerlite.sqlite"), g1LiteDBPath(cfg))
	require.Equal(t, filepath.Join("/tmp/out", "step-g-l2bridgesyncerlite.sqlite"), g2LiteDBPath(cfg))
}

func TestLiteForkNextDepositCount(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)

	// missing DB → error
	_, err := liteForkNextDepositCount(context.Background(), cfg)
	require.Error(t, err)

	makeG1LiteDB(t, cfg, 3)
	next, err := liteForkNextDepositCount(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, uint32(3), next)
}

func TestBuildLiteTreeWithReplayed(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)

	// missing G1 DB → error
	_, err := buildLiteTreeWithReplayed(context.Background(), cfg, nil)
	require.Error(t, err)

	makeG1LiteDB(t, cfg, 2)
	replayed := []bridgesyncerlite.BridgeLeaf{
		{BlockNum: 50, BlockPos: 0, DestinationNetwork: 1, Amount: big.NewInt(5), DepositCount: 2},
		{BlockNum: 50, BlockPos: 1, DestinationNetwork: 1, Amount: big.NewInt(6), DepositCount: 3},
	}
	root, err := buildLiteTreeWithReplayed(context.Background(), cfg, replayed)
	require.NoError(t, err)
	require.NotEqual(t, common.Hash{}, root)
	// the G2 working copy must have been created, leaving G1's DB intact
	require.FileExists(t, g2LiteDBPath(cfg))
	require.FileExists(t, g1LiteDBPath(cfg))
}

func TestBuildLiteTreeFromCertificate(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	makeG1LiteDB(t, cfg, 2)

	gasNet := uint32(0)
	gasAddr := common.Address{}
	cert := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			{ // native exit
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0xaaa"),
				Amount:             big.NewInt(100),
				Metadata:           []byte{0x01},
			},
			{ // ERC-20 exit
				TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 1, OriginTokenAddress: common.HexToAddress("0xtok")},
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0xbbb"),
				Amount:             big.NewInt(200),
				Metadata:           []byte{0x02, 0x03},
			},
		},
	}

	genMeta := [][]byte{{0x01}, {0x02, 0x03}}
	root, metadatas, err := buildLiteTreeFromCertificate(context.Background(), cfg, cert, 1000, gasNet, gasAddr, genMeta)
	require.NoError(t, err)
	require.NotEqual(t, common.Hash{}, root)
	require.Len(t, metadatas, 2)
	// the returned metadata is the (raw) generated metadata, used verbatim for the leaf encoding.
	require.Equal(t, []byte{0x01}, metadatas[0])
	require.Equal(t, []byte{0x02, 0x03}, metadatas[1])

	// deterministic: same inputs produce the same root
	root2, _, err := buildLiteTreeFromCertificate(context.Background(), cfg, cert, 1000, gasNet, gasAddr, genMeta)
	require.NoError(t, err)
	require.Equal(t, root, root2)

	// different metadata → different leaves → different root
	changedMeta := [][]byte{{0xff}, {0x02, 0x03}}
	rootChanged, _, err := buildLiteTreeFromCertificate(context.Background(), cfg, cert, 1000, gasNet, gasAddr, changedMeta)
	require.NoError(t, err)
	require.NotEqual(t, root, rootChanged)

	// a metadata count mismatch is an error
	_, _, err = buildLiteTreeFromCertificate(context.Background(), cfg, cert, 1000, gasNet, gasAddr, [][]byte{{0x01}})
	require.Error(t, err)
}

func TestRunStepG2NilCertificate(t *testing.T) {
	t.Parallel()
	_, err := RunStepG2(context.Background(), testConfig(t), 1000, nil, nil)
	require.Error(t, err)
}

func TestRunStepG2EmptyExits(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.L2RPCURL = newStubRPCServer(t)
	res, err := RunStepG2(context.Background(), cfg, 1000, &agglayertypes.Certificate{}, nil)
	require.NoError(t, err)
	require.Equal(t, uint64(0), res.BridgeExitCount)
}

func TestRunStepG2LiteOnly(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.L2RPCURL = newStubRPCServer(t)
	cfg.Options.VerifyNewLocalExitRootUsingShadowFork = false // off-chain mode, no Anvil
	makeG1LiteDB(t, cfg, 2)

	cert := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			{DestinationNetwork: 1, DestinationAddress: common.HexToAddress("0xaaa"), Amount: big.NewInt(1), Metadata: []byte{0x09}},
		},
	}
	res, err := RunStepG2(context.Background(), cfg, 1000, cert, nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.BridgeExitCount)
	require.NotEqual(t, common.Hash{}, res.NewLocalExitRoot)
	require.Len(t, res.BridgeExitMetadata, 1)
}

func TestCopyAndRemoveLiteDB(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.sqlite")
	dst := filepath.Join(dir, "dst.sqlite")
	require.NoError(t, os.WriteFile(src, []byte("dbcontents"), 0o644))
	// a WAL sidecar should be copied too
	require.NoError(t, os.WriteFile(src+"-wal", []byte("wal"), 0o644))

	require.NoError(t, copyLiteDB(src, dst))
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, "dbcontents", string(got))
	require.FileExists(t, dst+"-wal")

	// removeLiteDB deletes the file and sidecars; missing files are not an error
	require.NoError(t, removeLiteDB(dst))
	require.NoFileExists(t, dst)
	require.NoFileExists(t, dst+"-wal")
	require.NoError(t, removeLiteDB(filepath.Join(dir, "does-not-exist.sqlite")))
}

func TestCopyLiteDBMissingSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// copyLiteDB skips absent sidecars but the main file absent means nothing is copied; the
	// destination simply does not get created. It should not error on missing source files.
	require.NoError(t, copyLiteDB(filepath.Join(dir, "nope.sqlite"), filepath.Join(dir, "out.sqlite")))
}
