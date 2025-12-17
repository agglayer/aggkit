package common

import (
	"regexp"
)

const maxRangeMatchGroups = 2

var (
	// Matches "block range too large, max range: 1000"
	reMaxRange = regexp.MustCompile(`block range too large, max range:\s*(\d+)`)
	// Matches "exceeded maximum block range: 5000"
	reExceededBlockRange = regexp.MustCompile(`exceeded maximum block range:\s*(\d+)`)
)

// ParseMaxRangeFromError extracts the max range value from error message
// Expected formats:
//   - "block range too large, max range: 1000"
//   - "exceeded maximum block range: 5000"
func ParseMaxRangeFromError(errMsg string) (uint64, bool) {
	var matches []string
	for _, re := range []*regexp.Regexp{reMaxRange, reExceededBlockRange} {
		matches = re.FindStringSubmatch(errMsg)
		if len(matches) >= maxRangeMatchGroups {
			break
		}
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
