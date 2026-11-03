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
	// CheckRPCLogsAgainstBloom enables the eth_getLogs completeness (logsBloom) check for the logs
	// of this syncer's contracts. Default false. Only applied in the multidownloader StepUnsafe (unfinalized zone);
	// StepSafe never runs it
	CheckRPCLogsAgainstBloom bool
}

type MultiDownloaderLegacy interface {
	ChainID(ctx context.Context) (uint64, error)
	BlockNumber(ctx context.Context, finality BlockNumberFinality) (uint64, error)
	FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]ethtypes.Log, error)
	// Get block header by number and finality
	// if number is nil, it gets the latest block
	HeaderByNumber(ctx context.Context, number *BlockNumberFinality) (*BlockHeader, error)
	EthClient() BaseEthereumClienter
	RegisterSyncer(data SyncerConfig) error
	Start(ctx context.Context) error
}
