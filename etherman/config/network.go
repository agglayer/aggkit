package config

import (
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/common"
	gethcommon "github.com/ethereum/go-ethereum/common"
)

var (
	ErrMissingRPCURL                       = errors.New("missing RPC URL")
	ErrMissingRollupAddress                = errors.New("missing rollup address")
	ErrMissingRollupManagerAddress         = errors.New("missing rollup manager address")
	ErrMissingPOLTokenAddress              = errors.New("missing POL token address")
	ErrMissingGlobalExitRootManagerAddress = errors.New("missing global exit root manager address")
	ErrInvalidBlocksChunkSize              = errors.New("blocks chunk size must be greater than 0")
	ErrInvalidRollupManagerCreationBlock   = errors.New("rollup manager creation block must be greater than 0")
)

// Config holds the common configuration for the Aggkit services
type CommonConfig struct {
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
	RollupAddr gethcommon.Address `json:"polygonZkEVMAddress"`
	// RollupManagerAddr Address of the L1 contract
	RollupManagerAddr gethcommon.Address `json:"polygonRollupManagerAddress"`
	// POLTokenAddr Address of the L1 POL token Contract
	POLTokenAddr gethcommon.Address `json:"polTokenAddress"`
	// GlobalExitRootManagerAddr Address of the L1 GlobalExitRootManager contract
	GlobalExitRootManagerAddr gethcommon.Address `json:"polygonZkEVMGlobalExitRootAddress"`
	// BlocksChunkSize defines the number of blocks to be queried in each chunk when filtering events
	BlocksChunkSize uint64 `json:"blocksChunkSize"`
	// RollupManagerCreationBlock is the block number when the RollupManager contract was deployed
	RollupManagerCreationBlock uint64 `json:"rollupManagerCreationBlock"`
}

// Validate checks if the L1NetworkConfig is valid
func (c *L1NetworkConfig) Validate() error {
	if err := c.RPC.Validate(); err != nil {
		return fmt.Errorf("invalid RPC configuration: %w", err)
	}
	if c.RollupAddr == (gethcommon.Address{}) {
		return ErrMissingRollupAddress
	}
	if c.RollupManagerAddr == (gethcommon.Address{}) {
		return ErrMissingRollupManagerAddress
	}
	if c.POLTokenAddr == (gethcommon.Address{}) {
		return ErrMissingPOLTokenAddress
	}
	if c.GlobalExitRootManagerAddr == (gethcommon.Address{}) {
		return ErrMissingGlobalExitRootManagerAddress
	}
	if c.BlocksChunkSize == 0 {
		return ErrInvalidBlocksChunkSize
	}
	if c.RollupManagerCreationBlock == 0 {
		return ErrInvalidRollupManagerCreationBlock
	}
	return nil
}

// RPCClientConfig represents the configuration of the RPC client
type RPCClientConfig struct {
	common.RetryPolicyGenericConfig `mapstructure:",squash"`
	// URL is the URL of the RPC client
	URL string `mapstructure:"URL"`
}

// Validate checks if the RPCClientConfig is valid
func (c *RPCClientConfig) Validate() error {
	if c.URL == "" {
		return ErrMissingRPCURL
	}

	return c.RetryPolicyGenericConfig.Validate()
}

type RPCMode string

var (
	RPCModeBasic RPCMode = "basic"
	RPCModeHash  RPCMode = "hash"
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
	if err := c.RPCClientConfig.Validate(); err != nil {
		return fmt.Errorf("invalid RPC configuration: %w", err)
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
