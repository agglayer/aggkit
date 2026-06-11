package gertracker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// L1InfoTreeSyncer is the subset of l1infotreesync needed by the GER tracker.
type L1InfoTreeSyncer interface {
	GetInfoByGlobalExitRoot(ger common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error)
}

// GERTracker returns the latest GER injected on a target L2 and its resolved L1InfoTree leaf index.
type GERTracker interface {
	// LatestInjectedGER returns the GER hash and L1InfoTree leaf index for the most-recently
	// injected (and not removed) GER on the target L2.
	// Returns (nil, 0, nil) when no GER has been injected yet, or when the injected GER is
	// not yet known to l1infotreesync (syncer lagging) — callers treat this as "not ready".
	// Returns a non-nil error only for RPC/filter failures.
	LatestInjectedGER(ctx context.Context) (*common.Hash, uint32, error)
}

// L2GERManagerContract is the subset of agglayergerl2.Agglayergerl2 used by the GER tracker.
type L2GERManagerContract interface {
	FilterUpdateHashChainValue(
		opts *bind.FilterOpts,
		newGlobalExitRoot [][32]byte,
		newHashChainValue [][32]byte,
	) (*agglayergerl2.Agglayergerl2UpdateHashChainValueIterator, error)
	FilterUpdateRemovalHashChainValue(
		opts *bind.FilterOpts,
		removedGlobalExitRoot [][32]byte,
		newRemovalHashChainValue [][32]byte,
	) (*agglayergerl2.Agglayergerl2UpdateRemovalHashChainValueIterator, error)
}

// gerTracker implements GERTracker.
type gerTracker struct {
	l2GERManager     L2GERManagerContract
	l1InfoTreeSync   L1InfoTreeSyncer
	l2GERManagerAddr common.Address
}

// NewGERTracker creates a new GERTracker that queries the given L2 GER manager contract.
func NewGERTracker(
	l2GERManagerAddr common.Address,
	l2Client bind.ContractBackend,
	l1InfoTreeSync L1InfoTreeSyncer,
) (GERTracker, error) {
	binding, err := agglayergerl2.NewAgglayergerl2(l2GERManagerAddr, l2Client)
	if err != nil {
		return nil, fmt.Errorf("create agglayergerl2 binding for %s: %w", l2GERManagerAddr, err)
	}

	return &gerTracker{
		l2GERManager:     binding,
		l1InfoTreeSync:   l1InfoTreeSync,
		l2GERManagerAddr: l2GERManagerAddr,
	}, nil
}

// gerPosition holds the block number and log index for ordering GER events.
type gerPosition struct {
	blockNumber uint64
	logIndex    uint
}

// LatestInjectedGER returns the GER hash and L1InfoTree leaf index for the most-recently
// injected (and not removed) GER on the target L2.
//
// Performance note: this scans from genesis (Start: 0, End: nil) on every call. GER injection
// events are rare (one per AggOracle cycle) so the number of matching log entries is expected
// to remain small even on long-running chains. If profiling shows this is a bottleneck, consider
// caching the last-seen block number across calls.
func (g *gerTracker) LatestInjectedGER(ctx context.Context) (*common.Hash, uint32, error) {
	insertIterator, err := g.l2GERManager.FilterUpdateHashChainValue(
		&bind.FilterOpts{Context: ctx, Start: 0, End: nil}, nil, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("filter UpdateHashChainValue events for %s: %w", g.l2GERManagerAddr, err)
	}
	defer func() {
		if closeErr := insertIterator.Close(); closeErr != nil {
			log.Errorf("failed to close insert GER iterator: %v", closeErr)
		}
	}()

	inserted := make(map[common.Hash]gerPosition)
	for insertIterator.Next() {
		ger := common.Hash(insertIterator.Event.NewGlobalExitRoot)
		pos := gerPosition{
			blockNumber: insertIterator.Event.Raw.BlockNumber,
			logIndex:    insertIterator.Event.Raw.Index,
		}
		inserted[ger] = pos
	}
	if insertIterator.Error() != nil {
		return nil, 0, fmt.Errorf("iterate UpdateHashChainValue events: %w", insertIterator.Error())
	}

	removalIterator, err := g.l2GERManager.FilterUpdateRemovalHashChainValue(
		&bind.FilterOpts{Context: ctx, Start: 0, End: nil}, nil, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("filter UpdateRemovalHashChainValue events for %s: %w", g.l2GERManagerAddr, err)
	}
	defer func() {
		if closeErr := removalIterator.Close(); closeErr != nil {
			log.Errorf("failed to close removal GER iterator: %v", closeErr)
		}
	}()

	for removalIterator.Next() {
		ger := common.Hash(removalIterator.Event.RemovedGlobalExitRoot)
		delete(inserted, ger)
	}
	if removalIterator.Error() != nil {
		return nil, 0, fmt.Errorf("iterate UpdateRemovalHashChainValue events: %w", removalIterator.Error())
	}

	if len(inserted) == 0 {
		return nil, 0, nil
	}

	var latestGER common.Hash
	var latestPos gerPosition
	first := true
	for ger, pos := range inserted {
		if first ||
			pos.blockNumber > latestPos.blockNumber ||
			(pos.blockNumber == latestPos.blockNumber && pos.logIndex > latestPos.logIndex) {
			latestGER = ger
			latestPos = pos
			first = false
		}
	}

	leaf, err := g.l1InfoTreeSync.GetInfoByGlobalExitRoot(latestGER)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("get l1 info tree leaf for GER %s: %w", latestGER, err)
	}

	return &latestGER, leaf.L1InfoTreeIndex, nil
}
