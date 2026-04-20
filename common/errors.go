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
	for _, re := range []*regexp.Regexp{reMaxRange, reExceededBlockRange, reEthGetLogsLimited, reQueryExceedsMaxBlockRange} {
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
