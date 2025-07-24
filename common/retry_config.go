package common

import (
	"fmt"

	"github.com/agglayer/aggkit/config/types"
)

type RetryConfigMode string

const (
	RetryConfigModeNoRetries RetryConfigMode = ""
	RetryConfigModeDelays    RetryConfigMode = "delays"
	RetryConfigModeBackoff   RetryConfigMode = "backoff"
)

var (
	ErrInvalidRetryConfigMode = fmt.Errorf("invalid retry config mode")
)

// RetryPolicyGenericConfig defines the configuration for retry policies in the system.
// it's a merge of struct RetryBackoffConfig and RetryDelaysConfig in order of
// simplify reading from config file (check types)
type RetryPolicyGenericConfig struct {
	Mode              RetryConfigMode // "delays", "backoff", or "" for no retries
	MaxRetries        int
	Delays            []types.Duration
	InitialBackoff    types.Duration
	MaxBackoff        types.Duration
	BackoffMultiplier float64
}

// RetryPolicyConfigurer is an interface that defines methods for configuring retry policies.
// Each class that implements a retry policy configuration should implement this interface.
type RetryPolicyConfigurer interface {
	// Validate configuration
	Validate() error
	// NewRetryHandler returns a RetryHandler based on the configuration
	NewRetryHandler() *RetryHandler
	// String returns a string representation of the configuration
	String() string
	// Brief is a brief string representation of the object
	Brief() string
}

func (r *RetryPolicyGenericConfig) Validate() error {
	cfg, err := r.Factory()
	if err != nil {
		return err
	}
	return cfg.Validate()
}

func (r *RetryPolicyGenericConfig) String() string {
	cfg, err := r.Factory()
	if err != nil {
		return fmt.Sprintf("RetryPolicyConfig{Error: %s}", err)
	}
	if cfg == nil {
		return "RetryPolicyConfig{nil}"
	}
	return fmt.Sprintf("RetryPolicyConfig{Mode: %s, Config: %s}", r.Mode, cfg.String())
}

func (r *RetryPolicyGenericConfig) Factory() (RetryPolicyConfigurer, error) {
	switch r.Mode {
	case RetryConfigModeDelays:
		return &RetryDelaysConfig{
			Delays:     r.Delays,
			MaxRetries: r.MaxRetries,
		}, nil
	case RetryConfigModeBackoff:
		return &RetryBackoffConfig{
			InitialBackoff:    r.InitialBackoff,
			MaxBackoff:        r.MaxBackoff,
			BackoffMultiplier: r.BackoffMultiplier,
			MaxRetries:        r.MaxRetries,
		}, nil
	case RetryConfigModeNoRetries:
		return &RetryDelaysConfig{MaxRetries: 0}, nil

	default:
		return nil, fmt.Errorf("%w: bad mode %s", ErrInvalidRetryConfigMode, r.Mode)
	}
}
