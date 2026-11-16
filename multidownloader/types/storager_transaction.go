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
