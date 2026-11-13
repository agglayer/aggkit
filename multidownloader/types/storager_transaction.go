package types

import (
	"errors"
	"fmt"

	dbtypes "github.com/agglayer/aggkit/db/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// TransactionStorage is an OPTIONAL storage extension for canonical transaction
// inclusion. It is deliberately separate from Storager so that adding it does
// not force every Storager implementation or mock to grow.
//
// Consumers that need transaction storage must type-assert their Storager to
// this interface (see AsTransactionStorage) and fail clearly when it is not
// implemented. This guarantees that a storage backend without the extension can
// never silently pretend the transaction path is enabled/working.
//
// Inclusions are canonical: every write validates (block_number, block_hash)
// against the blocks table and every read joins blocks on both the number and
// the hash, so orphaned or reorged rows are never returned.
type TransactionStorage interface {
	// SaveTransactions persists the given canonical transactions. Each
	// transaction's (block_number, block_hash) must match a canonical block,
	// otherwise the whole save fails with
	// aggkittypes.ErrTransactionInclusionMismatch and no row is written. The
	// save is atomic (a DB transaction with rollback) and idempotent per
	// (tx_hash, block_hash).
	SaveTransactions(tx dbtypes.Querier, txs []aggkittypes.RPCTransaction) error
	// GetTransaction returns the canonical transaction identified by
	// (txHash, blockHash), or nil when it is not stored or is no longer
	// canonical (orphaned/reorged).
	GetTransaction(tx dbtypes.Querier, txHash common.Hash,
		blockHash common.Hash) (*aggkittypes.RPCTransaction, error)
	// GetTransactionsByBlock returns the canonical transactions stored for a
	// given block number, only when the block hash still matches the canonical
	// block.
	GetTransactionsByBlock(tx dbtypes.Querier, blockNumber uint64,
		blockHash common.Hash) ([]aggkittypes.RPCTransaction, error)
	// MissingTransactionsForLogQuery returns the distinct tx hashes of canonical
	// stored logs matching the query that do not yet have a canonical
	// transaction row. When topic0 is non-empty only logs whose first topic is
	// in topic0 are considered; an empty topic0 matches all logs, including
	// topicless ones. It is a pure log-existence computation: it returns no
	// error and an empty result when there are no relevant logs, and it does
	// NOT consider whether the query range is covered by any syncer (coverage is
	// reported separately by TransactionAvailabilityForLogQuery.LogCoverage).
	MissingTransactionsForLogQuery(tx dbtypes.Querier, query LogQuery,
		topic0 []common.Hash) ([]common.Hash, error)
	// TransactionAvailabilityForLogQuery returns the enriched transaction
	// availability for a log query, computed on demand from the current
	// canonical log coverage and per-tx row existence (no cached per-syncer
	// interval, no negative cache). Both the missing set and the log coverage
	// are read from a single coherent snapshot under the storage lock.
	TransactionAvailabilityForLogQuery(tx dbtypes.Querier, query LogQuery,
		topic0 []common.Hash) (TransactionAvailability, error)
}

// TransactionAvailability is the enriched transaction availability for a log
// query. Complete is true only when the requested log query is fully covered by
// the persisted syncer coverage (LogCoverage) AND every relevant canonical log
// has its transaction stored (no missing hashes). An interval that is not
// covered by any syncer, or that names an unknown address, is never reported
// complete even when it happens to contain no logs. There is no negative
// caching: the value is recomputed from the current coverage and per-tx row
// existence on every call.
type TransactionAvailability struct {
	// MissingTxHashes are the distinct tx hashes of the relevant canonical logs
	// that still miss a canonical transaction row. It is empty when there are
	// no relevant logs, independently of LogCoverage.
	MissingTxHashes []common.Hash
	// LogCoverage is true when the requested addresses/block range (or block
	// hash) are fully covered by the persisted syncer coverage. When false the
	// query must be treated as incomplete regardless of MissingTxHashes.
	LogCoverage bool
	// Complete is true when LogCoverage is true and there are no missing
	// transactions.
	Complete bool
}

// AsTransactionStorage returns the TransactionStorage extension of s, or a
// clear error when s does not implement it. It never silently succeeds, so a
// caller can distinguish "transactions disabled because the storage does not
// support them" from "everything is available".
func AsTransactionStorage(s Storager) (TransactionStorage, error) {
	if s == nil {
		return nil, errors.New("AsTransactionStorage: nil storager")
	}
	ts, ok := s.(TransactionStorage)
	if !ok {
		return nil, fmt.Errorf(
			"AsTransactionStorage: storager %T does not implement TransactionStorage (transaction storage disabled)",
			s)
	}
	return ts, nil
}
