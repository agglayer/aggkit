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
			return []map[string]string{{"data": sovereignData, "blockNumber": "0x1", "logIndex": "0x0"}}
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

// sovereignRangeStub serves eth_getLogs for the SetSovereignTokenAddress scan, honouring the
// fromBlock/toBlock filter so each event is returned only by its own block range — unlike
// sovereignStub, which repeats the same payload for every range.
func sovereignRangeStub(t *testing.T, logs []eventLogEntry) string {
	t.Helper()
	return newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		if method != rpcMethodEthGetLogs {
			return "0x"
		}
		var f struct {
			Topics    []string `json:"topics"`
			FromBlock string   `json:"fromBlock"`
			ToBlock   string   `json:"toBlock"`
		}
		_ = json.Unmarshal(params[0], &f)
		if len(f.Topics) == 0 || !strings.EqualFold(f.Topics[0], setSovereignTokenTopic.Hex()) {
			return []map[string]string{}
		}
		from, err := hexToUint64(f.FromBlock)
		if err != nil {
			t.Errorf("parse fromBlock %q: %v", f.FromBlock, err)
			return []map[string]string{}
		}
		to, err := hexToUint64(f.ToBlock)
		if err != nil {
			t.Errorf("parse toBlock %q: %v", f.ToBlock, err)
			return []map[string]string{}
		}
		out := []map[string]string{}
		for _, lg := range logs {
			if lg.BlockNumber >= from && lg.BlockNumber <= to {
				out = append(out, map[string]string{
					"data":        lg.Data,
					"blockNumber": toBlockTag(lg.BlockNumber),
					"logIndex":    toBlockTag(lg.LogIndex),
				})
			}
		}
		return out
	})
}

// TestApplySovereignTokenOverridesChronological covers AET-16: when the same origin token is
// remapped more than once, the chronologically latest onchain remap must win regardless of the
// worker-pool completion order, and the intermediate sovereign addresses must become legacy.
func TestApplySovereignTokenOverridesChronological(t *testing.T) {
	t.Parallel()
	origin := common.BytesToAddress([]byte("origin"))
	wrapped := common.BytesToAddress([]byte("wrapped"))
	sovereign1 := common.BytesToAddress([]byte("sovereign1"))
	sovereign2 := common.BytesToAddress([]byte("sovereign2"))

	// Two remaps of the same origin token in different block ranges (BlockRange=50):
	// block 10 → sovereign1, block 60 → sovereign2. The latest (sovereign2) must win.
	url := sovereignRangeStub(t, []eventLogEntry{
		{Data: makeWrappedTokenData(1, origin, sovereign1), BlockNumber: 10},
		{Data: makeWrappedTokenData(1, origin, sovereign2), BlockNumber: 60},
	})
	cfg := &Config{
		L2RPCURL: url, L2BridgeAddress: common.BytesToAddress([]byte("bridge")),
		Options: Options{BlockRange: 50, ConcurrencyLimit: 4, RPCBatchSize: 10},
	}

	events := []wrappedTokenEvent{
		{OriginNetwork: 1, OriginTokenAddress: origin, WrappedTokenAddr: wrapped},
	}
	out, err := applySovereignTokenOverrides(context.Background(), cfg, 100, events)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, sovereign2, out[0].WrappedTokenAddr)
	require.Equal(t, []common.Address{wrapped, sovereign1}, out[0].LegacyAddrs)
}

// TestApplySovereignTokenOverridesSameBlockLogIndex checks that within a block the log index
// decides which remap is the latest.
func TestApplySovereignTokenOverridesSameBlockLogIndex(t *testing.T) {
	t.Parallel()
	origin := common.BytesToAddress([]byte("origin"))
	sovereign1 := common.BytesToAddress([]byte("sovereign1"))
	sovereign2 := common.BytesToAddress([]byte("sovereign2"))

	url := sovereignRangeStub(t, []eventLogEntry{
		{Data: makeWrappedTokenData(1, origin, sovereign2), BlockNumber: 10, LogIndex: 3},
		{Data: makeWrappedTokenData(1, origin, sovereign1), BlockNumber: 10, LogIndex: 1},
	})
	cfg := &Config{
		L2RPCURL: url, L2BridgeAddress: common.BytesToAddress([]byte("bridge")),
		Options: Options{BlockRange: 50, ConcurrencyLimit: 2, RPCBatchSize: 10},
	}

	// No prior NewWrappedToken event → new entry with the latest sovereign and the earlier
	// one recorded as legacy.
	out, err := applySovereignTokenOverrides(context.Background(), cfg, 20, nil)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, sovereign2, out[0].WrappedTokenAddr)
	require.Equal(t, []common.Address{sovereign1}, out[0].LegacyAddrs)
}

func TestMergeLegacyAddrs(t *testing.T) {
	t.Parallel()
	w := common.BytesToAddress([]byte("w"))
	s1 := common.BytesToAddress([]byte("s1"))
	s2 := common.BytesToAddress([]byte("s2"))

	// Original wrapped + intermediate sovereign become legacy; the live one does not.
	require.Equal(t, []common.Address{w, s1},
		mergeLegacyAddrs(nil, w, []common.Address{s1, s2}))

	// Remapped back to the original wrapped address: only the intermediate is legacy.
	require.Equal(t, []common.Address{s1},
		mergeLegacyAddrs(nil, w, []common.Address{s1, w}))

	// No original wrapped address (sovereign-only token).
	require.Equal(t, []common.Address{s1},
		mergeLegacyAddrs(nil, common.Address{}, []common.Address{s1, s2}))

	// Duplicates in history are recorded once.
	require.Equal(t, []common.Address{w, s1},
		mergeLegacyAddrs(nil, w, []common.Address{s1, s1, s2}))

	// Pre-existing legacy entries are preserved and not duplicated.
	require.Equal(t, []common.Address{s1, w},
		mergeLegacyAddrs([]common.Address{s1}, w, []common.Address{s1, s2}))
}

func TestRetrieveBlockHeadersNotImplemented(t *testing.T) {
	t.Parallel()
	a := &ethClientAdapter{}
	_, err := a.RetrieveBlockHeaders(context.Background(), []uint64{1}, 1)
	require.Error(t, err)
}
