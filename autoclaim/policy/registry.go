package policy

import (
	"fmt"

	autoclaimconfig "github.com/agglayer/aggkit/autoclaim/config"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
)

// Registry constructs named Auto Claim policies from config.
type Registry struct {
	targetSimulator TargetSimulator
}

// RegistryOption configures a policy registry.
type RegistryOption func(*Registry)

// NewRegistry creates a policy registry with the supplied options.
func NewRegistry(options ...RegistryOption) *Registry {
	registry := &Registry{}
	for _, option := range options {
		option(registry)
	}
	return registry
}

// WithTargetSimulator configures the simulator used by policies that need target-chain checks.
func WithTargetSimulator(targetSimulator TargetSimulator) RegistryOption {
	return func(registry *Registry) {
		registry.targetSimulator = targetSimulator
	}
}

// NewPolicy constructs a policy by name.
func (r *Registry) NewPolicy(
	name autoclaimconfig.PolicyName,
	config autoclaimconfig.PolicyConfig,
) (autoclaimtypes.Policy, error) {
	switch name {
	case autoclaimconfig.PolicyNameAllowAll:
		return newStaticPolicy(name, autoclaimtypes.PolicyResultApproved, ReasonAllowAll), nil
	case autoclaimconfig.PolicyNameAPIApprove:
		return newStaticPolicy(name, autoclaimtypes.PolicyResultManual, ReasonAPIApprovalRequired), nil
	case autoclaimconfig.PolicyNameNoMessage:
		return newNoMessagePolicy(), nil
	case autoclaimconfig.PolicyNameBasicFilter:
		return newBasicFilterPolicy(config, r.targetSimulator), nil
	default:
		return nil, fmt.Errorf("unknown auto claim policy: %s", name)
	}
}

// NewPolicy constructs a policy by name using a temporary registry.
func NewPolicy(
	name autoclaimconfig.PolicyName,
	config autoclaimconfig.PolicyConfig,
	options ...RegistryOption,
) (autoclaimtypes.Policy, error) {
	return NewRegistry(options...).NewPolicy(name, config)
}
