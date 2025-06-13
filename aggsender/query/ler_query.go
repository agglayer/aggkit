package query

import (
	"fmt"
	"math/big"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
)

var _ types.LERQuerier = (*lerDataQuerier)(nil)

// lerDataQuerier is responsible for querying Layer 1 (L1) genesis block data and managing
// rollup-specific information using the provided RollupManagerContract. It stores the L1
// genesis block number, the rollup identifier, and a reference to the contract interface
// for interacting with rollup management functionality.
type lerDataQuerier struct {
	l1GenesisBlock    uint64
	rollupDataQuerier types.RollupDataQuerier
}

// NewLERDataQuerier creates a new instance of LERQuerier for querying Layer 1 Ethereum Rollup data.
// It initializes the RollupManager contract using the provided address and Ethereum client.
//
// Parameters:
//   - rollupManagerAddr: The Ethereum address of the RollupManager contract.
//   - l1GenesisBlock: The block number of the Layer 1 genesis block.
//   - l1Client: An implementation of BaseEthereumClienter for interacting with the Ethereum network.
//
// Returns:
//   - types.LERQuerier: An initialized LERQuerier for querying rollup data.
//   - error: An error if the RollupManager contract could not be created.
func NewLERDataQuerier(
	rollupManagerAddr common.Address,
	l1GenesisBlock uint64,
	rollupDataQuerier types.RollupDataQuerier) (types.LERQuerier, error) {
	return &lerDataQuerier{
		l1GenesisBlock:    l1GenesisBlock,
		rollupDataQuerier: rollupDataQuerier,
	}, nil
}

// GetLastLocalExitRoot retrieves the last local exit root for the rollup associated with this
// lerDataQuerier instance. It queries the RollupManager contract at the L1 genesis block for
// the rollup data corresponding to the configured rollup ID. Returns the last local exit root
// as a common.Hash, or an error if the contract call fails.
func (l *lerDataQuerier) GetLastLocalExitRoot() (common.Hash, error) {
	rollupData, err := l.rollupDataQuerier.GetRollupData(new(big.Int).SetUint64(l.l1GenesisBlock))
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to get rollup data: %w", err)
	}

	return rollupData.LastLocalExitRoot, nil
}
