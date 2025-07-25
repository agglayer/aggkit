package common

import (
	"fmt"

	commontypes "github.com/agglayer/aggkit/common/types"
	"github.com/agglayer/aggkit/config/types"
)

type RetryConfigMode string

const (
	RetryConfigModeNoRetries RetryConfigMode = ""
	RetryConfigModeDelays    RetryConfigMode = "delays"
	RetryConfigModeBackoff   RetryConfigMode = "backoff"
)

var (
	ErrInvalidRetryConfigMode                                   = fmt.Errorf("invalid retry config mode")
	_                         commontypes.RetryPolicyConfigurer = (*RetryPolicyGenericConfig)(nil)
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
	// internal field to cache the underlying config
	cache commontypes.RetryPolicyConfigurer `mapstructure:"-"`
}

func (r *RetryPolicyGenericConfig) Validate() error {
	cfg, err := r.Factory()
	if err != nil {
		return err
	}
	return cfg.Validate()
}

func (r *RetryPolicyGenericConfig) NewRetryHandler() (commontypes.RetryHandler, error) {
	cfg, err := r.Factory()
	if err != nil {
		return nil, err
	}
	return cfg.NewRetryHandler()
}

func (r *RetryPolicyGenericConfig) Brief() string {
	cfg, err := r.Factory()
	if err != nil {
		return fmt.Sprintf("RetryPolicyConfig{Error: %s}", err)
	}
	return fmt.Sprintf("%s/%s", r.Mode, cfg.Brief())
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

// CleanCache clears the internal cache of the RetryPolicyGenericConfig.
// useful is you modify any params and want to rebuild the underlaying object
func (r *RetryPolicyGenericConfig) CleanCache() {
	// Clean the cache to force a new Factory call next time
	r.cache = nil
}

func (r *RetryPolicyGenericConfig) SetCache(instance commontypes.RetryPolicyConfigurer) {
	r.cache = instance
}

func (r *RetryPolicyGenericConfig) Factory() (commontypes.RetryPolicyConfigurer, error) {
	if r.cache != nil {
		return r.cache, nil
	}
	var cache commontypes.RetryPolicyConfigurer
	var err error
	switch r.Mode {
	case RetryConfigModeDelays:
		cache, err = NewRetryDelaysConfig(r)
	case RetryConfigModeBackoff:
		cache, err = NewRetryBackoffConfig(r)
	case RetryConfigModeNoRetries:
		cache = &RetryDelaysConfig{MaxRetries: 0}

	default:
		return nil, fmt.Errorf("%w: unknown mode %s", ErrInvalidRetryConfigMode, r.Mode)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create retry policy config: %w", err)
	}
	r.SetCache(cache)
	return cache, nil
}
