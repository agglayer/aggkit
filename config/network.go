package config

import (
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrMissingRPCConfig                    = errors.New("missing RPC configuration")
	ErrMissingRPCURL                       = errors.New("missing RPC URL")
	ErrMissingRollupAddress                = errors.New("missing rollup address")
	ErrMissingRollupManagerAddress         = errors.New("missing rollup manager address")
	ErrMissingPOLTokenAddress              = errors.New("missing POL token address")
	ErrMissingGlobalExitRootManagerAddress = errors.New("missing global exit root manager address")
)

// Config holds the common configuration for the Aggkit services
type CommonConfig struct {
	// NetworkID is the networkID of the Aggkit being run
	NetworkID uint32 `mapstructure:"NetworkID"`
	// L2URL is the URL of the L2 node
	L2RPC L2RPCClientConfig `mapstructure:"L2RPC"`
}

// L1NetworkConfig represents the configuration of the network used in L1
type L1NetworkConfig struct {
	// RPC client configuration for the L1 network
	RPC RPCClientConfig `mapstructure:"RPC"`
	// Chain ID of the L1 network
	ChainID uint64 `json:"chainId"`
	// RollupAddr Address of the L1 rollup contract
	RollupAddr common.Address `json:"polygonZkEVMAddress"`
	// RollupManagerAddr Address of the L1 contract
	RollupManagerAddr common.Address `json:"polygonRollupManagerAddress"`
	// POLTokenAddr Address of the L1 POL token Contract
	POLTokenAddr common.Address `json:"polTokenAddress"`
	// GlobalExitRootManagerAddr Address of the L1 GlobalExitRootManager contract
	GlobalExitRootManagerAddr common.Address `json:"polygonZkEVMGlobalExitRootAddress"`
}

// Validate checks if the L1NetworkConfig is valid
func (c *L1NetworkConfig) Validate() error {
	if c.RPC == (RPCClientConfig{}) {
		return ErrMissingRPCConfig
	}
	if c.RPC.URL == "" {
		return ErrMissingRPCURL
	}
	if c.RollupAddr == (common.Address{}) {
		return ErrMissingRollupAddress
	}
	if c.RollupManagerAddr == (common.Address{}) {
		return ErrMissingRollupManagerAddress
	}
	if c.POLTokenAddr == (common.Address{}) {
		return ErrMissingPOLTokenAddress
	}
	if c.GlobalExitRootManagerAddr == (common.Address{}) {
		return ErrMissingGlobalExitRootManagerAddress
	}
	return nil
}

// RPCClientConfig represents the configuration of the RPC client
type RPCClientConfig struct {
	// URL is the URL of the RPC client
	URL string `mapstructure:"URL"`
	// MaxRetries is the maximum number of retries for RPC requests
	MaxRetries int `mapstructure:"MaxRetries"`
	// InitialBackoff is the initial backoff duration for retries
	InitialBackoff types.Duration `mapstructure:"InitialBackoff"`
}

type RPCMode string

var (
	RPCModeBasic RPCMode = "basic"
	RPCModeOp    RPCMode = "op"
)

// L2RPCClientConfig represents the configuration of the L2 RPC client
type L2RPCClientConfig struct {
	// RPCClientConfig contains the basic RPC client configuration
	RPCClientConfig `mapstructure:",squash"`
	// Mode defines the mode of the RPC client (basic or op)
	// In basic mode, the client connects to a standard RPC endpoint.
	// In op mode, the client connects to an Optimistic RPC endpoint.
	Mode RPCMode `jsonschema:"enum=basic, enum=op" mapstructure:"Mode"`
	// ExtraParams contains any additional parameters that may be needed for the RPC client
	ExtraParams map[string]any `jsonschema:"omitempty" mapstructure:",remain"`
}

// Validate checks if the L2RPCClientConfig is valid
func (c *L2RPCClientConfig) Validate() error {
	if c.RPCClientConfig == (RPCClientConfig{}) {
		return ErrMissingRPCConfig
	}
	if c.RPCClientConfig.URL == "" {
		return ErrMissingRPCURL
	}
	if c.Mode != RPCModeBasic && c.Mode != RPCModeOp {
		return fmt.Errorf("invalid RPC mode: %s", c.Mode)
	}
	return nil
}

func (c L2RPCClientConfig) GetString(key string) (string, error) {
	valueAny, ok := c.ExtraParams[key]
	if !ok {
		return "", fmt.Errorf("field %s not found in extra params of rpcclient config", key)
	}
	stringValue, ok := valueAny.(string)
	if !ok {
		return "", fmt.Errorf("field %s is not a string", key)
	}
	return stringValue, nil
}
