package common

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/rpc"
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

	// Matches "too many batch requests, max is 40"
	reTooManyBatchRequests = regexp.MustCompile(`too many batch requests, max is\s*(\d+)`)
	// Matches "batch limit 100 exceeded"
	reBatchLimitExceeded = regexp.MustCompile(`batch limit (\d+) exceeded`)
	// Matches "batch of more than 100 requests"
	reBatchOfMoreThan = regexp.MustCompile(`(?i)batch of more than (\d+) requests`)
	// Matches batch-limit messages that do not carry the limit (the caller must reduce the size by itself)
	reBatchLimitKeyword = regexp.MustCompile(
		`(?i)(batch too large|too many batch requests|batch size (limit|too large|exceeded)|` +
			`batch (request )?limit|batch request (was )?too large)`)
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

// ParseMaxBatchSizeFromError extracts a provider's maximum JSON-RPC batch size from an error message.
// Expected formats:
//   - "too many batch requests, max is 40"
//   - "batch limit 100 exceeded"
//   - "batch of more than 100 requests"
//
// It returns false when no pattern matches or when the parsed value is 0.
func ParseMaxBatchSizeFromError(errMsg string) (uint64, bool) {
	for _, re := range []*regexp.Regexp{reTooManyBatchRequests, reBatchLimitExceeded, reBatchOfMoreThan} {
		matches := re.FindStringSubmatch(errMsg)
		if len(matches) < maxRangeMatchGroups {
			continue
		}
		maxSize, err := ParseUint64HexOrDecimal(matches[1])
		if err != nil || maxSize == 0 {
			return 0, false
		}
		return maxSize, true
	}
	return 0, false
}

// IsBatchLimitError reports whether err means that a JSON-RPC batch was too large for the provider.
// HTTP 429 is deliberately not considered a batch-limit error.
func IsBatchLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if _, ok := ParseMaxBatchSizeFromError(msg); ok {
		return true
	}
	if reBatchLimitKeyword.MatchString(msg) {
		return true
	}
	var httpErr rpc.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusRequestEntityTooLarge
}
