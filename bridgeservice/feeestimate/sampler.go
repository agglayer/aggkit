package feeestimate

import (
	"context"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/jellydator/ttlcache/v3"
	"golang.org/x/sync/errgroup"
)

// txObservation is one fetched transaction together with its receipt. Both are immutable
// once the transaction is mined, which is what makes caching observations by hash safe.
type txObservation struct {
	tx      *ethtypes.Transaction
	receipt *ethtypes.Receipt
}

// txStore fetches transactions and their receipts by hash from the chain, caching the
// immutable results in an LRU bounded by Config.TxCacheSize and fanning the fetches out
// over at most Config.RPCConcurrency concurrent RPC calls. A transaction that is unknown or
// still pending is not cached: it is simply not returned, and retried on the next recompute.
type txStore struct {
	chain       ChainReader
	cache       *ttlcache.Cache[common.Hash, *txObservation]
	concurrency int
}

// newTxStore builds a txStore over chain with the given LRU capacity and concurrency bound.
func newTxStore(chain ChainReader, capacity, concurrency int) *txStore {
	return &txStore{
		chain: chain,
		cache: ttlcache.New(
			ttlcache.WithCapacity[common.Hash, *txObservation](uint64(capacity)),
		),
		concurrency: concurrency,
	}
}

// fetch returns the observations of the given hashes, skipping without error the ones that
// are unknown or still pending. It fails closed on any RPC error (D1): a partial sample set
// would silently bias the statistics.
func (s *txStore) fetch(ctx context.Context, hashes []common.Hash) (map[common.Hash]*txObservation, error) {
	observations := make(map[common.Hash]*txObservation, len(hashes))
	if len(hashes) == 0 {
		return observations, nil
	}

	results := make([]*txObservation, len(hashes))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(s.concurrency)
	for i, hash := range hashes {
		group.Go(func() error {
			observation, err := s.fetchOne(groupCtx, hash)
			if err != nil {
				return err
			}
			results[i] = observation
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	for i, observation := range results {
		if observation != nil {
			observations[hashes[i]] = observation
		}
	}
	return observations, nil
}

// fetchOne returns the observation of one hash from the cache or the chain, or nil when the
// transaction is unknown or still pending.
func (s *txStore) fetchOne(ctx context.Context, hash common.Hash) (*txObservation, error) {
	if item := s.cache.Get(hash); item != nil {
		return item.Value(), nil
	}

	receipt, err := s.chain.TransactionReceipt(ctx, hash)
	if errors.Is(err, ethereum.NotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching receipt of tx %s: %w", hash.Hex(), err)
	}

	tx, pending, err := s.chain.TransactionByHash(ctx, hash)
	if errors.Is(err, ethereum.NotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching tx %s: %w", hash.Hex(), err)
	}
	if pending {
		return nil, nil
	}

	observation := &txObservation{tx: tx, receipt: receipt}
	s.cache.Set(hash, observation, ttlcache.NoTTL)
	return observation, nil
}

// isSuccessfulBridgeTx reports whether obs is a successfully executed transaction that was
// sent directly to the bridge contract: the part of the sample eligibility rule (D4) every
// estimator side shares.
func isSuccessfulBridgeTx(obs *txObservation, bridgeAddr common.Address) bool {
	if obs == nil || obs.tx == nil || obs.receipt == nil {
		return false
	}
	if obs.receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return false
	}
	to := obs.tx.To()
	return to != nil && *to == bridgeAddr
}

// countBridgeLogs counts the receipt logs the bridge contract emitted whose first topic is
// one of topics.
func countBridgeLogs(obs *txObservation, bridgeAddr common.Address, topics []common.Hash) int {
	count := 0
	for _, log := range obs.receipt.Logs {
		if log == nil || len(log.Topics) == 0 || log.Address != bridgeAddr {
			continue
		}
		for _, topic := range topics {
			if log.Topics[0] == topic {
				count++
				break
			}
		}
	}
	return count
}
