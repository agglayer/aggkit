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

type RetryPolicyConfig struct {
	MaxRetries        int
	InitialBackoff    types.Duration
	MaxBackoff        types.Duration
	BackoffMultiplier float64
	Delays            []types.Duration
	Mode              RetryConfigMode
}

type RetryHandlerConfigurer interface {
	// Validate configuration
	Validate() error
	// RetryHandler returns a RetryHandler based on the configuration
	RetryHandler() *RetryHandler
	// String returns a string representation of the configuration
	String() string
	// Brief is a brief string representation of the object
	Brief() string
}

func (r *RetryPolicyConfig) Validate() error {
	cfg, err := r.Factory()
	if err != nil {
		return err
	}
	return cfg.Validate()
}

func (r *RetryPolicyConfig) String() string {
	cfg, err := r.Factory()
	if err != nil {
		return fmt.Sprintf("RetryPolicyConfig{Error: %s}", err)
	}
	if cfg == nil {
		return "RetryPolicyConfig{nil}"
	}
	return fmt.Sprintf("RetryPolicyConfig{Mode: %s, Config: %s}", r.Mode, cfg.String())
}

func (r *RetryPolicyConfig) Factory() (RetryHandlerConfigurer, error) {
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
