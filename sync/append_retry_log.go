package sync

import (
	"errors"

	aggkitcommon "github.com/agglayer/aggkit/common"
)

// ErrAppendNotReadyYet can be wrapped by a LogAppender error when the failure is an expected, transient
// condition that resolves by itself (e.g. data from another syncer that is still catching up). The downloader
// still retries the log, but logs the retries at WARN instead of ERROR
var ErrAppendNotReadyYet = errors.New("not ready yet, will retry")

// appendRetryLogger is the subset of the logger used to log the retries of an appender
type appendRetryLogger interface {
	Debugf(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

// LogAppendRetry logs a failed attempt to append a log. The level is WARN if err wraps ErrAppendNotReadyYet and
// ERROR otherwise, but only for the attempts selected by aggkitcommon.ShouldLogRetryAtError (the first ones and
// then one of every RetryErrorLogInterval); the rest are logged at DEBUG, so a log retried for a long time
// doesn't flood the logs
func LogAppendRetry(logger appendRetryLogger, attempts int, err error) {
	logf := logger.Errorf
	switch {
	case !aggkitcommon.ShouldLogRetryAtError(attempts):
		logf = logger.Debugf
	case errors.Is(err, ErrAppendNotReadyYet):
		logf = logger.Warnf
	}
	logf("error trying to append log (attempt %d): %v", attempts, err)
}
