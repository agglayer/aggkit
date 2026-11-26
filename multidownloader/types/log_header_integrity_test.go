package types

import (
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

var (
	integrityHashA = common.HexToHash("0xaaaa")
	integrityHashB = common.HexToHash("0xbbbb")
)

func integrityLog(blockNumber uint64, blockHash common.Hash) types.Log {
	return types.Log{
		BlockNumber: blockNumber,
		BlockHash:   blockHash,
	}
}

// TestCheckLogsMatchBlockHeaders covers the shared guard used by both the
// multidownloader step logic and the storage write paths. It must reject a
// missing header and a hash mismatch, tolerate an empty batch, treat nil header
// entries as absent (never panic), and reject inconsistent duplicate headers
// instead of silently letting the last one win.
func TestCheckLogsMatchBlockHeaders(t *testing.T) {
	t.Run("empty logs and empty headers is valid", func(t *testing.T) {
		require.NoError(t, CheckLogsMatchBlockHeaders(nil, nil))
		require.NoError(t, CheckLogsMatchBlockHeaders([]types.Log{}, aggkittypes.ListBlockHeaders{}))
	})

	t.Run("matching logs and headers is valid", func(t *testing.T) {
		logs := []types.Log{integrityLog(100, integrityHashA), integrityLog(101, integrityHashB)}
		headers := aggkittypes.ListBlockHeaders{
			aggkittypes.NewBlockHeader(100, integrityHashA, 0, nil),
			aggkittypes.NewBlockHeader(101, integrityHashB, 0, nil),
		}
		require.NoError(t, CheckLogsMatchBlockHeaders(logs, headers))
	})

	t.Run("extra headers without logs are valid", func(t *testing.T) {
		logs := []types.Log{integrityLog(100, integrityHashA)}
		headers := aggkittypes.ListBlockHeaders{
			aggkittypes.NewBlockHeader(100, integrityHashA, 0, nil),
			aggkittypes.NewBlockHeader(101, integrityHashB, 0, nil),
		}
		require.NoError(t, CheckLogsMatchBlockHeaders(logs, headers))
	})

	t.Run("log without a header is rejected", func(t *testing.T) {
		logs := []types.Log{integrityLog(101, integrityHashB)}
		headers := aggkittypes.ListBlockHeaders{aggkittypes.NewBlockHeader(100, integrityHashA, 0, nil)}
		err := CheckLogsMatchBlockHeaders(logs, headers)
		require.ErrorIs(t, err, ErrLogBlockHeaderMismatch)
		require.Contains(t, err.Error(), "block header for log block number 101 not found")
	})

	t.Run("log hash mismatch is rejected", func(t *testing.T) {
		logs := []types.Log{integrityLog(100, integrityHashB)}
		headers := aggkittypes.ListBlockHeaders{aggkittypes.NewBlockHeader(100, integrityHashA, 0, nil)}
		err := CheckLogsMatchBlockHeaders(logs, headers)
		require.ErrorIs(t, err, ErrLogBlockHeaderMismatch)
		require.Contains(t, err.Error(), "does not match block header hash")
	})

	t.Run("nil header entry is treated as absent, never dereferenced", func(t *testing.T) {
		logs := []types.Log{integrityLog(100, integrityHashA)}
		headers := aggkittypes.ListBlockHeaders{nil, aggkittypes.NewBlockHeader(100, integrityHashA, 0, nil), nil}
		require.NoError(t, CheckLogsMatchBlockHeaders(logs, headers))

		// A log whose block only has a nil header must be reported as missing.
		err := CheckLogsMatchBlockHeaders([]types.Log{integrityLog(101, integrityHashB)},
			aggkittypes.ListBlockHeaders{nil, aggkittypes.NewBlockHeader(100, integrityHashA, 0, nil)})
		require.ErrorIs(t, err, ErrLogBlockHeaderMismatch)
	})

	t.Run("all nil headers with no logs is valid", func(t *testing.T) {
		require.NoError(t, CheckLogsMatchBlockHeaders(nil, aggkittypes.ListBlockHeaders{nil, nil}))
	})

	t.Run("consistent duplicate headers are accepted", func(t *testing.T) {
		logs := []types.Log{integrityLog(100, integrityHashA)}
		headers := aggkittypes.ListBlockHeaders{
			aggkittypes.NewBlockHeader(100, integrityHashA, 0, nil),
			aggkittypes.NewBlockHeader(100, integrityHashA, 60, nil),
		}
		require.NoError(t, CheckLogsMatchBlockHeaders(logs, headers))
	})

	t.Run("inconsistent duplicate headers are rejected, not last-wins", func(t *testing.T) {
		// The first header matches the log, the later contradictory header would
		// silently win if the headers were collapsed into a map. It must be
		// rejected instead.
		logs := []types.Log{integrityLog(100, integrityHashA)}
		headers := aggkittypes.ListBlockHeaders{
			aggkittypes.NewBlockHeader(100, integrityHashA, 0, nil),
			aggkittypes.NewBlockHeader(100, integrityHashB, 0, nil),
		}
		err := CheckLogsMatchBlockHeaders(logs, headers)
		require.ErrorIs(t, err, ErrLogBlockHeaderMismatch)
		require.Contains(t, err.Error(), "inconsistent duplicate block headers")
	})
}

// TestCheckLogsMatchBlockHeaderMap pins the map-based variant used by
// SaveEthLogs, which derives the header set from the logs themselves. A later
// log that contradicts the header derived from an earlier log of the same block
// must be rejected.
func TestCheckLogsMatchBlockHeaderMap(t *testing.T) {
	headers := aggkittypes.MapBlockHeaders{
		100: aggkittypes.NewBlockHeader(100, integrityHashA, 0, nil),
	}

	t.Run("matching log is valid", func(t *testing.T) {
		require.NoError(t, CheckLogsMatchBlockHeaderMap([]types.Log{integrityLog(100, integrityHashA)}, headers))
	})

	t.Run("later mismatch in the same block is rejected", func(t *testing.T) {
		logs := []types.Log{
			integrityLog(100, integrityHashA),
			integrityLog(100, integrityHashB),
		}
		err := CheckLogsMatchBlockHeaderMap(logs, headers)
		require.ErrorIs(t, err, ErrLogBlockHeaderMismatch)
	})

	t.Run("missing map entry is rejected", func(t *testing.T) {
		err := CheckLogsMatchBlockHeaderMap([]types.Log{integrityLog(101, integrityHashB)}, headers)
		require.ErrorIs(t, err, ErrLogBlockHeaderMismatch)
	})

	t.Run("nil map entry is rejected", func(t *testing.T) {
		nullHeaders := aggkittypes.MapBlockHeaders{100: nil}
		err := CheckLogsMatchBlockHeaderMap([]types.Log{integrityLog(100, integrityHashA)}, nullHeaders)
		require.ErrorIs(t, err, ErrLogBlockHeaderMismatch)
	})

	t.Run("header number mismatch is rejected", func(t *testing.T) {
		// The map key is 100 but the header claims a different number.
		misnumbered := aggkittypes.MapBlockHeaders{100: aggkittypes.NewBlockHeader(101, integrityHashA, 0, nil)}
		err := CheckLogsMatchBlockHeaderMap([]types.Log{integrityLog(100, integrityHashA)}, misnumbered)
		require.ErrorIs(t, err, ErrLogBlockHeaderMismatch)
		require.Contains(t, err.Error(), "does not match log block number")
	})
}
