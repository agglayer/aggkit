package client

import (
	"errors"
	"fmt"
)

// ErrInvalidResponse is returned (wrapped) when the bridge service answers HTTP 200 with a body
// that decodes as JSON but breaks an invariant of its schema, e.g. a null entry in a list of
// objects. The whole response is rejected: callers never see a partially valid result
var ErrInvalidResponse = errors.New("invalid bridge service response")

// requireNoNullEntries rejects a decoded list of objects holding a null entry (a nil pointer), so
// consumers can read every element without a nil check. field is the JSON name of the list
func requireNoNullEntries[T any](field string, entries []*T) error {
	for i, entry := range entries {
		if entry == nil {
			return fmt.Errorf("%w: %s[%d] is null", ErrInvalidResponse, field, i)
		}
	}
	return nil
}
