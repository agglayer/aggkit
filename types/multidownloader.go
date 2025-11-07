package types

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// This struct is for decoupling the MultiDownloader interface from aggkittypes.SyncerConfig
type SyncerConfig struct {
	// SyncerID is the unique identifier for the syncer
	SyncerID string
	// ContractAddr is list of contract addresses to sync
	ContractsAddr []common.Address
	// Starting block
	FromBlock uint64
	// Taget for final block
	ToBlock             BlockNumberFinality
	RequiredBlockHeader bool
}

type MultiDownloader interface {
	ChainID(ctx context.Context) (uint64, error)
	BlockNumber(ctx context.Context, finality BlockNumberFinality) (uint64, error)
	FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]ethtypes.Log, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*BlockHeader, error)
	EthClient() BaseEthereumClienter
	RegisterSyncer(data SyncerConfig)
	Start(ctx context.Context) error
}
