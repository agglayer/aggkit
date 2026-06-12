package exit_certificate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestRunSingleBChain drives runSingleB1 → B2 → B3 end-to-end against a combined stub, with the
// step-0/step-A prerequisite files written up front. It covers the three Step B run.go wrappers and
// their file chaining (B1 writes the contract-addresses B2/B3 consume).
func TestRunSingleBChain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rich := common.HexToAddress("0x0000000000000000000000000000000000000001")
	poor := common.HexToAddress("0x0000000000000000000000000000000000000002")
	tok := common.BytesToAddress([]byte("wrap"))
	orig := common.BytesToAddress([]byte("orig"))

	// Prerequisites normally produced by Step 0 and Step A.
	saveJSON(dir, fileStep0TargetBlock, uint64(100))
	saveJSON(dir, fileStep0LBT, []LBTEntry{
		{WrappedTokenAddress: tok, OriginNetwork: 1, OriginTokenAddress: orig, Balance: "1000"},
	})
	saveJSON(dir, fileStepAAddresses, []common.Address{rich, poor})

	url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		switch method {
		case rpcMethodEthGetCode:
			return "0x" // all EOAs, no contracts
		case rpcMethodEthGetBalance:
			if blockTagOf(t, params) == genesisTag {
				return "0x0" // genesis guard passes
			}
			if firstAddr(t, params) == rich {
				return "0x64"
			}
			return "0x0"
		default:
			return "0x0" // eth_call balanceOf etc → zero
		}
	})
	cfg := &Config{
		L2RPCURL: url, L2BridgeAddress: common.BytesToAddress([]byte("bridge")),
		L2NetworkID: 1, Options: Options{OutputDir: dir, RPCBatchSize: 10, ConcurrencyLimit: 2},
	}

	require.NoError(t, runSingleB1(context.Background(), cfg, dir))
	require.True(t, fileExists(filepath.Join(dir, fileStepBEOABalances)))
	require.True(t, fileExists(filepath.Join(dir, fileStepBContractAddresses)))

	require.NoError(t, runSingleB2(context.Background(), cfg, dir))
	require.True(t, fileExists(filepath.Join(dir, fileStepB2DetectedERC20s)))

	require.NoError(t, runSingleB3(context.Background(), cfg, dir))
	require.True(t, fileExists(filepath.Join(dir, fileStepB3ERC20Holders)))

	// runSingleB runs all three; rerunning is idempotent over the same fixtures.
	require.NoError(t, runSingleB(context.Background(), cfg, dir))
}
