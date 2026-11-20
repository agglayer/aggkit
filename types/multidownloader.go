package types

import (
	"context"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// TransactionRequirement declares that a syncer additionally needs the full
// transaction data for the logs it syncs. The zero value (Enabled=false)
// disables the requirement and is the default: transaction fetching is opt-in.
type TransactionRequirement struct {
	// Enabled indicates that transaction data is required for this syncer.
	Enabled bool
	// FromBlock is the first block for which transactions are required.
	// It may be earlier than the syncer log FromBlock because historical logs
	// may already exist and their transactions may need to be backfilled. The
	// effective frontier is max(SyncerConfig.FromBlock, FromBlock); the upper
	// bound is SyncerConfig.ToBlock.
	FromBlock uint64
	// Topic0 lists event signature (topic0) hashes that trigger a transaction
	// fetch. Entries are OR-ed. An empty list means every log requires a
	// transaction, including topicless logs.
	Topic0 []common.Hash
}

type SyncerConfig struct {
	// SyncerID is the unique identifier for the syncer
	SyncerID string
	// ContractAddresses is list of contract addresses to sync
	ContractAddresses []common.Address
	// Starting block
	FromBlock uint64
	// Target for final block (e.g. LatestBlock, SafeBlock, FinalizedBlock)
	ToBlock BlockNumberFinality
	// TransactionRequirements optionally requests the full transaction data for
	// the logs this syncer syncs. It is disabled by default (zero value).
	TransactionRequirements TransactionRequirement
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
