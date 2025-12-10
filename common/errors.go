package common

import (
	"regexp"
	"strings"
)

const maxRangeMatchGroups = 2

var re = regexp.MustCompile(`max range:\s*(\d+)`)

// ParseMaxRangeFromError extracts the max range value from error message
// Expected format: "block range too large, max range: 1000"
func ParseMaxRangeFromError(errMsg string) (uint64, bool) {
	if !strings.Contains(errMsg, "block range too large") {
		return 0, false
	}

	matches := re.FindStringSubmatch(errMsg)
	if len(matches) < maxRangeMatchGroups {
		return 0, false
	}

	maxRange, err := ParseUint64HexOrDecimal(matches[1])
	if err != nil {
		return 0, false
	}

	return maxRange, true
}
