package common

import (
	"regexp"
	"strings"
)

const maxRangeMatchGroups = 2

var (
	// Matches "block range too large, max range: 1000"
	reMaxRange = regexp.MustCompile(`block range too large, max range:\s*(\d+)`)
	// Matches "exceeded maximum block range: 5000"
	reExceededBlockRange = regexp.MustCompile(`exceeded maximum block range:\s*(\d+)`)
	// Matches "eth_getLogs is limited to a 10,000 range" (number may contain comma thousands separators)
	reEthGetLogsLimited = regexp.MustCompile(`eth_getLogs is limited to a\s+([\d,]+)\s+range`)
	// Matches "query exceeds max block range 100000"
	reQueryExceedsMaxBlockRange = regexp.MustCompile(`query exceeds max block range\s+(\d+)`)
)

// ParseMaxRangeFromError extracts the max range value from error message
// Expected formats:
//   - "block range too large, max range: 1000"
//   - "exceeded maximum block range: 5000"
//   - "eth_getLogs is limited to a 10,000 range"
func ParseMaxRangeFromError(errMsg string) (uint64, bool) {
	var matches []string
	for _, re := range []*regexp.Regexp{reMaxRange,
		reExceededBlockRange,
		reEthGetLogsLimited,
		reQueryExceedsMaxBlockRange} {
		matches = re.FindStringSubmatch(errMsg)
		if len(matches) >= maxRangeMatchGroups {
			break
		}
	}

	if len(matches) < maxRangeMatchGroups {
		return 0, false
	}

	// Strip comma thousands separators (e.g. "10,000" -> "10000") before parsing
	numStr := strings.ReplaceAll(matches[1], ",", "")
	maxRange, err := ParseUint64HexOrDecimal(numStr)
	if err != nil {
		return 0, false
	}

	return maxRange, true
}

// tooManyResultsSubstrings lists eth_getLogs/FilterLogs error phrases observed across RPC
// providers that indicate the call was rejected because the result set was too large, without
// reporting an explicit block-range cap (unlike the messages ParseMaxRangeFromError recognises).
var tooManyResultsSubstrings = []string{
	// geth family, e.g. "Query returned more than 20000 results. Try with this block range [...]."
	"Query returned more than",
	// some managed RPC providers report an oversized response this way instead
	"Response size exceeded",
}

// IsTooManyResultsError reports whether errMsg is an eth_getLogs/FilterLogs "too many results"
// style response: a size rejection with no explicit block-range cap, so no exact window size can
// be recovered from the message itself and any reduction has to be heuristic.
func IsTooManyResultsError(errMsg string) bool {
	for _, substr := range tooManyResultsSubstrings {
		if strings.Contains(errMsg, substr) {
			return true
		}
	}
	return false
}

// IsSizeOrRangeError reports whether errMsg is a recognised eth_getLogs/FilterLogs size or
// block-range problem: either an explicit range cap (ParseMaxRangeFromError) or a "too many
// results" response (IsTooManyResultsError). It classifies the error family regardless of whether
// a smaller window can still be computed for it (that decision belongs to NextEthGetLogsWindow),
// so callers can use it to recognise the family even when their current window is already at its
// minimum.
func IsSizeOrRangeError(errMsg string) bool {
	if _, ok := ParseMaxRangeFromError(errMsg); ok {
		return true
	}
	return IsTooManyResultsError(errMsg)
}

// NextEthGetLogsWindow is the single source of truth for how an eth_getLogs/FilterLogs error maps
// to the next block-range window size a caller should retry with. Given the error returned by a
// FilterLogs-style call and the block-range window size (number of blocks) that produced it, it
// reports whether the error is a retryable size/range problem and, if so, the window size to retry
// with next.
//
// Two error families are recognised:
//   - an explicit block-range cap reported by the RPC provider (see ParseMaxRangeFromError): the
//     next window is the reported cap, used only when it is itself smaller than currentWindow --
//     a reported cap that is not an improvement is treated as unrecoverable (ok=false) rather than
//     guessed at, since honouring it would not make forward progress;
//   - a "too many results" style response with no explicit cap (see IsTooManyResultsError): the
//     next window is currentWindow halved.
//
// ok is false for any other error (including nil) or when currentWindow is already at its minimum
// (<= 1), in which case the caller must not alter its window size and the error should propagate
// (or be treated as exhausted) unchanged.
//
// When ok is true, newWindow is always > 0 and strictly less than currentWindow, so a caller that
// loops calling this on repeated failures is guaranteed to terminate.
func NextEthGetLogsWindow(err error, currentWindow uint64) (newWindow uint64, ok bool) {
	if err == nil || currentWindow <= 1 {
		return 0, false
	}

	errMsg := err.Error()

	if maxRange, isMaxRangeErr := ParseMaxRangeFromError(errMsg); isMaxRangeErr {
		if maxRange > 0 && maxRange < currentWindow {
			return maxRange, true
		}
		return 0, false
	}

	if IsTooManyResultsError(errMsg) {
		return currentWindow / 2, true //nolint:mnd
	}

	return 0, false
}
