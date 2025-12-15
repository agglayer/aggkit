package common

import (
	"regexp"
	"strings"
)

const maxRangeMatchGroups = 2

var (
	// Matches "max range: 1000" or "max range:1000"
	reMaxRange = regexp.MustCompile(`max range:\s*(\d+)`)
	// Matches "exceeded maximum block range: 5000"
	reExceededBlockRange = regexp.MustCompile(`exceeded maximum block range:\s*(\d+)`)
)

// ParseMaxRangeFromError extracts the max range value from error message
// Expected formats:
//   - "block range too large, max range: 1000"
//   - "exceeded maximum block range: 5000"
func ParseMaxRangeFromError(errMsg string) (uint64, bool) {
	var matches []string

	if strings.Contains(errMsg, "block range too large") {
		matches = reMaxRange.FindStringSubmatch(errMsg)
	} else if strings.Contains(errMsg, "exceeded maximum block range") {
		matches = reExceededBlockRange.FindStringSubmatch(errMsg)
	} else {
		return 0, false
	}

	if len(matches) < maxRangeMatchGroups {
		return 0, false
	}

	maxRange, err := ParseUint64HexOrDecimal(matches[1])
	if err != nil {
		return 0, false
	}

	return maxRange, true
}
