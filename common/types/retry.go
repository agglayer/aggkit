package types

import "time"

type RetryHandler interface {
	Validate() error
	MustExecuteAttempt(attempt int) bool
	Delay(attempt int) time.Duration
	StringAttempt(attempt int) string
}

// RetryPolicyConfigurer is an interface that defines methods for configuring retry policies.
// Each class that implements a retry policy configuration should implement this interface.
type RetryPolicyConfigurer interface {
	// Validate configuration
	Validate() error
	// NewRetryHandler returns a RetryHandler based on the configuration
	NewRetryHandler() (RetryHandler, error)
	// String returns a string representation of the configuration
	String() string
	// Brief is a brief string representation of the object
	Brief() string
}
