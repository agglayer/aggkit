package common

import (
	"errors"
	"regexp"
	"strings"
)

const maxRangeMatchGroups = 2

// parseMaxRangeFromError extracts the max range value from error message
// Expected format: "block range too large, max range: 1000"
func ParseMaxRangeFromError(errMsg string) (uint64, error) {
	if !strings.Contains(errMsg, "block range too large") {
		return 0, errors.New("not a block range error")
	}

	re := regexp.MustCompile(`max range:\s*(\d+)`)
	matches := re.FindStringSubmatch(errMsg)
	if len(matches) < maxRangeMatchGroups {
		return 0, errors.New("could not parse max range from error message")
	}

	maxRange, err := ParseUint64HexOrDecimal(matches[1])
	if err != nil {
		return 0, errors.New("failed to convert max range to uint64")
	}

	return maxRange, nil
}
