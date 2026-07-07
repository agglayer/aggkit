package exit_certificate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestFetchL1InfoTreeLeafCountRequiresConfig(t *testing.T) {
	t.Parallel()

	_, err := fetchL1InfoTreeLeafCount(context.Background(), &Config{})
	require.ErrorContains(t, err, "l1RpcUrl")

	_, err = fetchL1InfoTreeLeafCount(context.Background(), &Config{L1RPCURL: "http://localhost:1"})
	require.ErrorContains(t, err, "l1GlobalExitRootAddress")
}

func TestFetchL1InfoTreeLeafCount(t *testing.T) {
	t.Parallel()

	leafCountTopic := "0x" + common.Bytes2Hex(common.LeftPadBytes([]byte{0x2a}, 32)) // leafCount=42

	makeCfg := func(url string) *Config {
		return &Config{
			L1RPCURL:                url,
			L1GlobalExitRootAddress: common.HexToAddress("0x0000000000000000000000000000000000000abc"),
			Options:                 Options{BlockRange: 100},
		}
	}
	logsResult := []map[string]any{{
		"topics": []string{updateL1InfoTreeV2Topic.Hex(), leafCountTopic},
	}}

	t.Run("latest block when l1EndBlock is unset", func(t *testing.T) {
		t.Parallel()
		url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
			switch method {
			case rpcMethodEthBlockNumber:
				return "0x1a4" // 420
			case "eth_getLogs":
				var filter struct {
					ToBlock string `json:"toBlock"`
				}
				require.NoError(t, json.Unmarshal(params[0], &filter))
				require.Equal(t, toBlockTag(420), filter.ToBlock)
				return logsResult
			}
			t.Fatalf("unexpected method %s", method)
			return nil
		})
		leafCount, err := fetchL1InfoTreeLeafCount(context.Background(), makeCfg(url))
		require.NoError(t, err)
		require.Equal(t, uint32(42), leafCount)
	})

	t.Run("scan starts at l1EndBlock when set", func(t *testing.T) {
		t.Parallel()
		url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
			require.Equal(t, "eth_getLogs", method, "no eth_blockNumber call expected with an l1EndBlock cutoff")
			var filter struct {
				ToBlock string `json:"toBlock"`
			}
			require.NoError(t, json.Unmarshal(params[0], &filter))
			require.Equal(t, toBlockTag(300), filter.ToBlock)
			return logsResult
		})
		cfg := makeCfg(url)
		cfg.Options.L1EndBlock = 300
		leafCount, err := fetchL1InfoTreeLeafCount(context.Background(), cfg)
		require.NoError(t, err)
		require.Equal(t, uint32(42), leafCount)
	})
}
