package grpc

// ServiceConfig represents the top-level configuration for service method behaviors,
// including retry policies. It mirrors the gRPC service configuration format.
type ServiceConfig struct {
	// List of method-specific configurations.
	MethodConfig []MethodConfig `json:"methodConfig"`
}

// MethodConfig defines behavior overrides (e.g., retry policy) for specific RPC methods or services.
type MethodConfig struct {
	// List of service/method pairs this config applies to.
	Name []MethodName `json:"name"`
	// Optional retry policy to apply to the specified methods.
	RetryPolicy *RetryPolicy `json:"retryPolicy"`
}

// MethodName identifies a gRPC method or service that the retry policy should apply to.
// Either Service or Method may be empty for wildcard matching.
type MethodName struct {
	// Optional: Match all methods of a specific service if Method is empty.
	Service string `json:"service,omitempty"`
	// Optional: Match a specific method of the given service.
	Method string `json:"method,omitempty"`
}

// RetryPolicy specifies how failed RPCs should be retried, including delays and allowed status codes.
type RetryPolicy struct {
	// Maximum number of attempts (initial + retries).
	MaxAttempts int `json:"maxAttempts"`
	// Delay before first retry (e.g., "0.1s").
	InitialBackoff string `json:"initialBackoff"`
	// Maximum delay between retries (e.g., "2s").
	MaxBackoff string `json:"maxBackoff"`
	// Multiplier for exponential backoff (e.g., 2.0).
	BackoffMultiplier float64 `json:"backoffMultiplier"`
	// gRPC status codes that are considered retryable (e.g., "UNAVAILABLE").
	RetryableStatusCodes []string `json:"retryableStatusCodes"`
}
