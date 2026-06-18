package rollupdata

import (
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
)

const defaultUpdateBufferSize = 16

var (
	// ErrMissingRollupManagerAddress is returned when the rollup manager address is empty.
	ErrMissingRollupManagerAddress = errors.New("missing rollup manager address")
	// ErrInvalidUpdateBufferSize is returned when the update channel buffer size is negative.
	ErrInvalidUpdateBufferSize = errors.New("update buffer size must be greater than or equal to 0")
	// ErrNilEthereumClient is returned when the Ethereum client is nil.
	ErrNilEthereumClient = errors.New("eth client is nil")
)

// Config contains the rollup data querier configuration.
type Config struct {
	RollupManagerAddr common.Address `mapstructure:"RollupManagerAddr"`
	UpdateBufferSize  int            `mapstructure:"UpdateBufferSize"`
}

// Validate checks the rollup data configuration.
func (c Config) Validate() error {
	if c.RollupManagerAddr == (common.Address{}) {
		return ErrMissingRollupManagerAddress
	}
	if c.UpdateBufferSize < 0 {
		return fmt.Errorf("%w: %d", ErrInvalidUpdateBufferSize, c.UpdateBufferSize)
	}

	return nil
}

func (c Config) updateBufferSize() int {
	if c.UpdateBufferSize == 0 {
		return defaultUpdateBufferSize
	}

	return c.UpdateBufferSize
}
