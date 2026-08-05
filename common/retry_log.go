package common

// Bounded-retry-logging policy: loops that can retry indefinitely (e.g. a driver retrying a
// halted processor, or a halted-processor guard that keeps rejecting calls) must not log at
// error level on every single attempt, since that can produce millions of log lines during a
// prolonged incident. Instead: log at error level for the first RetryErrorLogBurst attempts,
// then only every RetryErrorLogInterval-th attempt afterwards; log at debug level otherwise.
const (
	RetryErrorLogBurst    = 5
	RetryErrorLogInterval = 100
)

// ShouldLogRetryAtError reports whether the given (1-based) attempt/hit count should be logged
// at error level under the bounded-retry-logging policy described above. Callers should log at
// debug level when this returns false.
func ShouldLogRetryAtError(attempt int) bool {
	if attempt <= 0 {
		return false
	}
	return attempt <= RetryErrorLogBurst || attempt%RetryErrorLogInterval == 0
}
