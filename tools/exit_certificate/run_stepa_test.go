package exit_certificate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestRunSingleAChain drives runSingleA1 then runSingleA2 against a stub whose blocks carry no
// transactions, so address collection yields an empty set without any debug_traceTransaction call.
// It covers the run.go Step A wrappers and their file chaining.
func TestRunSingleAChain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	saveJSON(dir, fileStep0TargetBlock, uint64(2))

	url := newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		if method == rpcMethodEthGetBlockByNumber {
			return map[string]any{"transactions": []string{}}
		}
		return "0x"
	})
	cfg := &Config{
		L2RPCURL: url, L2BridgeAddress: common.BytesToAddress([]byte("bridge")),
		Options: Options{
			OutputDir: dir, RPCBatchSize: 10, ConcurrencyLimit: 2, StepAWindowSize: 100,
		},
	}

	require.NoError(t, runSingleA1(context.Background(), cfg, dir))
	require.True(t, fileExists(filepath.Join(dir, fileStepA1Addresses)))
	require.True(t, fileExists(filepath.Join(dir, fileStepA1FailedTrace)))

	require.NoError(t, runSingleA2(context.Background(), cfg, dir))
	require.True(t, fileExists(filepath.Join(dir, fileStepAAddresses)))

	// runSingleA runs A1 then A2 in sequence.
	require.NoError(t, runSingleA(context.Background(), cfg, dir))
}

// TestRunAllStepASuccess covers the runAll Step A wrapper (RunStepA1 + RunStepA2) against a stub with
// transaction-free blocks.
func TestRunAllStepASuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	url := newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		if method == rpcMethodEthGetBlockByNumber {
			return map[string]any{"transactions": []string{}}
		}
		return "0x"
	})
	cfg := &Config{
		L2RPCURL: url, L2BridgeAddress: common.BytesToAddress([]byte("bridge")),
		Options: Options{OutputDir: dir, RPCBatchSize: 10, ConcurrencyLimit: 2, StepAWindowSize: 100},
	}

	res, err := runAllStepA(context.Background(), cfg, dir, 2, nil)
	require.NoError(t, err)
	require.Empty(t, res.Addresses)
	require.True(t, fileExists(filepath.Join(dir, fileStepAAddresses)))
}
