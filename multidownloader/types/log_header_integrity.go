package types

import (
	"errors"
	"fmt"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/core/types"
)

// ErrLogBlockHeaderMismatch is the typed sentinel returned when an eth log does
// not match the block header that covers its block. A log is canonical only
// within the block identified by (block number, block hash): a log whose block
// number has no header, or whose BlockHash differs from the header hash, is a
// corruption signal (a mixed/contradictory download batch) that the caller must
// surface instead of persisting.
var ErrLogBlockHeaderMismatch = errors.New("log block header mismatch")

// CheckLogsMatchBlockHeaders validates that every log has a corresponding block
// header and that the log's block hash equals the header hash for the same block
// number. The headers are indexed by block number, so a log whose block number
// is absent (a missing header) or whose BlockHash contradicts the indexed header
// (a hash mismatch) is rejected. An empty log slice is valid regardless of the
// headers, and empty headers with no logs is valid too.
//
// Two headers that share a block number but carry different hashes are an
// inconsistent duplicate: they are rejected rather than silently letting the
// last one win. A nil header entry is treated as an absent header (it is
// skipped), so a log whose block number only has a nil header is reported as a
// missing header instead of panicking.
//
// It is the shared, single source of truth for this check: the multidownloader
// step logic and the storage write paths both delegate to it so that an invalid
// batch is rejected before any state or storage mutation happens.
func CheckLogsMatchBlockHeaders(logs []types.Log, blockHeaders aggkittypes.ListBlockHeaders) error {
	headerMap := aggkittypes.NewMapBlockHeadersEmpty(blockHeaders.Len())
	for _, header := range blockHeaders {
		if header == nil {
			// An absent header must not be dereferenced. ToMap() also skips nil
			// entries, so a log for this block number is reported as missing.
			continue
		}
		if existing, ok := headerMap[header.Number]; ok && existing.Hash != header.Hash {
			return fmt.Errorf("%w: inconsistent duplicate block headers for block number %d: hash %s != %s",
				ErrLogBlockHeaderMismatch, header.Number, existing.Hash.String(), header.Hash.String())
		}
		headerMap[header.Number] = header
	}
	return CheckLogsMatchBlockHeaderMap(logs, headerMap)
}

// CheckLogsMatchBlockHeaderMap is CheckLogsMatchBlockHeaders over an explicit
// block-number -> header map, for callers (such as SaveEthLogs) that derive the
// header set from the logs themselves instead of receiving it from an RPC call.
func CheckLogsMatchBlockHeaderMap(logs []types.Log, blockHeaders aggkittypes.MapBlockHeaders) error {
	for _, lg := range logs {
		header, exists := blockHeaders[lg.BlockNumber]
		if !exists || header == nil {
			return fmt.Errorf("%w: block header for log block number %d not found",
				ErrLogBlockHeaderMismatch, lg.BlockNumber)
		}
		if header.Number != lg.BlockNumber {
			return fmt.Errorf("%w: block header number %d does not match log block number %d",
				ErrLogBlockHeaderMismatch, header.Number, lg.BlockNumber)
		}
		if header.Hash != lg.BlockHash {
			return fmt.Errorf("%w: log block hash %s does not match block header hash %s for block number %d",
				ErrLogBlockHeaderMismatch, lg.BlockHash.String(), header.Hash.String(), lg.BlockNumber)
		}
	}
	return nil
}
