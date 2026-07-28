package exit_certificate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// setupLiteG2 prepares everything runSingleG2 needs to run in off-chain (no shadow-fork) mode
// with a single native bridge exit: an empty Step G1 lite DB, the G1/step-e fixtures, and a stub
// serving the bridge getRoot / gasTokenMetadata / gas-token eth_calls. Returns the ready Config
// (its OutputDir is dir).
func setupLiteG2(t *testing.T, dir string) *Config {
	t.Helper()
	ctx := context.Background()

	// Empty Step G1 lite DB (genesis→fork bridges = none → cert exits start at deposit count 0).
	g1, err := bridgesyncerlite.New(ctx,
		bridgesyncerlite.Config{DBPath: filepath.Join(dir, fileStepG1LiteDB)}, log.GetDefaultLogger())
	require.NoError(t, err)
	require.NoError(t, g1.Close())

	// One native bridge exit (token_info origin address zero → gas token).
	bridgeExits, err := json.Marshal([]map[string]any{{
		"leaf_type": "Transfer",
		"token_info": map[string]any{
			"origin_network":       0,
			"origin_token_address": "0x0000000000000000000000000000000000000000",
		},
		"dest_network": 0,
		"dest_address": "0x1111111111111111111111111111111111111111",
		"amount":       "1000",
	}})
	require.NoError(t, err)
	require.NoError(t, saveJSON(dir, fileStepG1ShadowForkBlock, StepG1Result{ShadowForkBlock: 100}))
	require.NoError(t, saveJSON(dir, fileStepECertificate, &certificateJSON{NetworkID: 1, BridgeExits: bridgeExits}))

	getRootSel := selectorHex(bridgeABI, "getRoot")
	gasMetaSel := selectorHex(bridgeABI, "gasTokenMetadata")
	rootOut, err := bridgeABI.Methods["getRoot"].Outputs.Pack([32]byte{})
	require.NoError(t, err)
	gasMetaOut, err := bridgeABI.Methods["gasTokenMetadata"].Outputs.Pack([]byte{})
	require.NoError(t, err)

	srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
		if method != rpcMethodEthCall {
			return quoted("0x"), nil // eth_getCode and any other probe → empty
		}
		call, _ := params[0].(map[string]any)
		data, _ := call["data"].(string)
		data = strings.TrimPrefix(data, "0x")
		switch {
		case strings.HasPrefix(data, getRootSel):
			return hexResult(rootOut), nil
		case strings.HasPrefix(data, gasMetaSel):
			return hexResult(gasMetaOut), nil
		default:
			// gasTokenNetwork/gasTokenAddress: a zero ABI word decodes as the ETH values (network 0,
			// zero address), which is what this native exit expects.
			return hexResult(make([]byte, abiWordBytes)), nil
		}
	})

	return &Config{
		L2RPCURL:        srv.URL,
		L2BridgeAddress: common.HexToAddress("0x2222222222222222222222222222222222222222"),
		L2NetworkID:     1,
		Options:         Options{OutputDir: dir, VerifyNewLocalExitRootUsingShadowFork: false},
	}
}

// TestRunSingleG2LiteNonEmpty drives runSingleG2 in off-chain (no shadow-fork) mode with a single
// native bridge exit: RunStepG2 builds the lite exit tree and writes its outputs.
func TestRunSingleG2LiteNonEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := setupLiteG2(t, dir)

	require.NoError(t, runSingleG2(context.Background(), cfg, dir))
	require.True(t, fileExists(filepath.Join(dir, fileStepGNewLocalExitRoot)))
	require.True(t, fileExists(filepath.Join(dir, fileStepGReorderedCertificate)))
}

// TestRunSingleG2LiteSaveError covers the AET-39 write-error branch: the step succeeds but its
// result file cannot be written, which must fail the step instead of leaving a stale file behind.
func TestRunSingleG2LiteSaveError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := setupLiteG2(t, dir)
	require.NoError(t, os.Mkdir(filepath.Join(dir, fileStepGNewLocalExitRoot), 0o755))

	err := runSingleG2(context.Background(), cfg, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), fileStepGNewLocalExitRoot)
}
