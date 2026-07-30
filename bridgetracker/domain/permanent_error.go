package domain

import "errors"

// PermanentError marks a driven-port failure that retrying cannot fix, so the tracker gives up
// on it immediately instead of waiting out a retry/timeout budget. Adapters wrap their own
// errors with Permanent when they know the condition is unrecoverable (e.g. a definitively
// reverted tx, or a statically-missing source); every other error defaults to transient
type PermanentError struct {
	err error
}

// Permanent wraps err as a PermanentError, or returns nil unchanged
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{err: err}
}

// Error implements error
func (e *PermanentError) Error() string {
	return e.err.Error()
}

// Unwrap lets errors.Is/errors.As see through to the wrapped cause
func (e *PermanentError) Unwrap() error {
	return e.err
}

// IsPermanent reports whether err, or any error in its chain, was marked permanent via Permanent
func IsPermanent(err error) bool {
	var permErr *PermanentError
	return errors.As(err, &permErr)
}
