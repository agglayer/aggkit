package exit_certificate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// newStepAServer returns a stub that serves a one-account state dump; with no wrapped tokens the
// Transfer-log scan is skipped, so Step A completes on the dump alone.
func newStepAServer(t *testing.T) string {
	t.Helper()
	return newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		if method == rpcMethodDebugAccountRange {
			return map[string]any{
				"accounts": map[string]any{stepAAddr1: map[string]any{"address": stepAAddr1}},
				"next":     "",
			}
		}
		return "0x"
	})
}

// TestRunSingleA drives runSingleA against a state-dump stub. It covers the run.go Step A wrapper:
// target-block loading, the missing-LBT warning path, and the output file.
func TestRunSingleA(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	saveJSON(dir, fileStep0TargetBlock, uint64(2))

	cfg := &Config{
		L2RPCURL: newStepAServer(t), L2BridgeAddress: common.BytesToAddress([]byte("bridge")),
		Options: Options{OutputDir: dir, RPCBatchSize: 10, ConcurrencyLimit: 2},
	}

	// No LBT file → wrapped tokens unavailable, logged as a warning; the step still runs.
	require.NoError(t, runSingleA(context.Background(), cfg, dir))
	require.True(t, fileExists(filepath.Join(dir, fileStepAAddresses)))

	var addrs []common.Address
	require.NoError(t, loadJSON(dir, fileStepAAddresses, &addrs))
	require.Equal(t, []common.Address{common.HexToAddress(stepAAddr1)}, addrs)
}

// TestRunAllStepASuccess covers the runAll Step A wrapper against the same state-dump stub.
func TestRunAllStepASuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := &Config{
		L2RPCURL: newStepAServer(t), L2BridgeAddress: common.BytesToAddress([]byte("bridge")),
		Options: Options{OutputDir: dir, RPCBatchSize: 10, ConcurrencyLimit: 2},
	}

	res, err := runAllStepA(context.Background(), cfg, dir, 2, nil)
	require.NoError(t, err)
	require.Equal(t, []common.Address{common.HexToAddress(stepAAddr1)}, res.Addresses)
	require.True(t, fileExists(filepath.Join(dir, fileStepAAddresses)))
}
