package types

import (
	"errors"
	"strings"
)

// ErrNotFound is used when the object is not found
var ErrNotFound = errors.New("not found")

// IsErrNotFound checks if the error is a "not found" error
func IsErrNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNotFound) {
		return true
	}
	if err.Error() == ErrNotFound.Error() {
		return true
	}
	// If error contains "not found" (case sensitive) is an ErrNotFound
	if strings.Contains(err.Error(), "not found") {
		return true
	}
	return false
}
