package types

import (
	"context"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

type SyncerConfig struct {
	// SyncerID is the unique identifier for the syncer
	SyncerID string
	// ContractAddresses is list of contract addresses to sync
	ContractAddresses []common.Address
	// Starting block
	FromBlock uint64
	// Target for final block (e.g. LatestBlock, SafeBlock, FinalizedBlock)
	ToBlock BlockNumberFinality
}

type MultiDownloader interface {
	ChainID(ctx context.Context) (uint64, error)
	BlockNumber(ctx context.Context, finality BlockNumberFinality) (uint64, error)
	// TODO: delete this method because it's only required for a intermediate fix of old RerogDetector
	BlockHeader(ctx context.Context, finality BlockNumberFinality) (*BlockHeader, error)
	FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]ethtypes.Log, error)
	HeaderByNumber(ctx context.Context, number *BlockNumberFinality) (*BlockHeader, error)
	EthClient() BaseEthereumClienter
	RegisterSyncer(data SyncerConfig) error
	Start(ctx context.Context) error
}
