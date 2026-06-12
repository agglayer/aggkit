package exit_certificate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestRunSingleG2LiteNonEmpty drives runSingleG2 in off-chain (no shadow-fork) mode with a single
// native bridge exit. It builds an empty Step G1 lite DB up front, then serves the bridge getRoot /
// gasTokenMetadata eth_calls so RunStepG2 builds the lite exit tree and writes its outputs.
func TestRunSingleG2LiteNonEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
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
	saveJSON(dir, fileStepG1ShadowForkBlock, StepG1Result{ShadowForkBlock: 100})
	saveJSON(dir, fileStepECertificate, &certificateJSON{NetworkID: 1, BridgeExits: bridgeExits})

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
			// gasTokenNetwork/gasTokenAddress: an empty result makes fetchGasTokenInfo fall back to the
			// ETH default (network 0, zero address), which is what this native exit expects.
			return quoted("0x"), nil
		}
	})

	cfg := &Config{
		L2RPCURL:        srv.URL,
		L2BridgeAddress: common.HexToAddress("0x2222222222222222222222222222222222222222"),
		L2NetworkID:     1,
		Options:         Options{OutputDir: dir, VerifyNewLocalExitRootUsingShadowFork: false},
	}

	require.NoError(t, runSingleG2(ctx, cfg, dir))
	require.True(t, fileExists(filepath.Join(dir, fileStepGNewLocalExitRoot)))
	require.True(t, fileExists(filepath.Join(dir, fileStepGReorderedCertificate)))
}
