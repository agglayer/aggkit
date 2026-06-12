package exit_certificate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// sovereignStub serves eth_getLogs for the SetSovereignTokenAddress scan, returning the given event
// payload for that topic and nothing for any other topic.
func sovereignStub(t *testing.T, sovereignData string) string {
	t.Helper()
	return newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		if method != rpcMethodEthGetLogs {
			return "0x"
		}
		var f struct {
			Topics []string `json:"topics"`
		}
		_ = json.Unmarshal(params[0], &f)
		if len(f.Topics) > 0 && strings.EqualFold(f.Topics[0], setSovereignTokenTopic.Hex()) {
			return []map[string]string{{"data": sovereignData}}
		}
		return []map[string]string{}
	})
}

func TestApplySovereignTokenOverrides(t *testing.T) {
	t.Parallel()
	origin := common.BytesToAddress([]byte("origin"))
	sovereign := common.BytesToAddress([]byte("sovereign"))
	wrapped := common.BytesToAddress([]byte("wrapped"))

	url := sovereignStub(t, makeWrappedTokenData(1, origin, sovereign))
	cfg := &Config{
		L2RPCURL: url, L2BridgeAddress: common.BytesToAddress([]byte("bridge")),
		Options: Options{BlockRange: 50, ConcurrencyLimit: 2, RPCBatchSize: 10},
	}

	// A prior NewWrappedToken event for the same origin token gets its wrapped address overridden.
	events := []wrappedTokenEvent{
		{OriginNetwork: 1, OriginTokenAddress: origin, WrappedTokenAddr: wrapped},
	}
	out, err := applySovereignTokenOverrides(context.Background(), cfg, 100, events)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, sovereign, out[0].WrappedTokenAddr)
	require.Contains(t, out[0].LegacyAddrs, wrapped)
}

func TestApplySovereignTokenOverridesNewEntry(t *testing.T) {
	t.Parallel()
	origin := common.BytesToAddress([]byte("origin2"))
	sovereign := common.BytesToAddress([]byte("sovereign2"))

	url := sovereignStub(t, makeWrappedTokenData(1, origin, sovereign))
	cfg := &Config{
		L2RPCURL: url, L2BridgeAddress: common.BytesToAddress([]byte("bridge")),
		Options: Options{BlockRange: 50, ConcurrencyLimit: 2, RPCBatchSize: 10},
	}

	// No prior NewWrappedToken event → the override is added as a new entry.
	out, err := applySovereignTokenOverrides(context.Background(), cfg, 100, nil)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, sovereign, out[0].WrappedTokenAddr)
}

func TestApplySovereignTokenOverridesNone(t *testing.T) {
	t.Parallel()
	url := newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		if method == rpcMethodEthGetLogs {
			return []map[string]string{} // no SetSovereignTokenAddress events
		}
		return "0x"
	})
	cfg := &Config{
		L2RPCURL: url, L2BridgeAddress: common.BytesToAddress([]byte("bridge")),
		Options: Options{BlockRange: 50, ConcurrencyLimit: 2, RPCBatchSize: 10},
	}
	events := []wrappedTokenEvent{{OriginNetwork: 1, OriginTokenAddress: common.BytesToAddress([]byte("o"))}}
	out, err := applySovereignTokenOverrides(context.Background(), cfg, 100, events)
	require.NoError(t, err)
	require.Equal(t, events, out)
}

func TestRetrieveBlockHeadersNotImplemented(t *testing.T) {
	t.Parallel()
	a := &ethClientAdapter{}
	_, err := a.RetrieveBlockHeaders(context.Background(), []uint64{1}, 1)
	require.Error(t, err)
}
