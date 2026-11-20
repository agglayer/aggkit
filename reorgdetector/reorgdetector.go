package reorgdetector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector/migrations"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"golang.org/x/sync/errgroup"
)

type Network string

const (
	L1 Network = "l1"
	L2 Network = "l2"
)

func (n Network) String() string {
	return string(n)
}

type ReorgDetector struct {
	client             aggkittypes.BaseEthereumClienter
	db                 *sql.DB
	checkReorgInterval time.Duration
	finalizedBlockType aggkittypes.BlockNumberFinality
	network            Network

	trackedBlocksLock sync.RWMutex
	trackedBlocks     map[string]*headersList

	subscriptionsLock sync.RWMutex
	subscriptions     map[string]*Subscription
	headersCache      map[uint64]*aggkittypes.BlockHeader
	headersCacheLock  sync.Mutex

	// missingBlocks has, per subscriber, the tracked block that was found above the chain head in the previous
	// detection pass. It is reported as a reorg only if it is still above the head in the next pass
	missingBlocks     map[string]Header
	missingBlocksLock sync.Mutex

	log *log.Logger
}

func New(client aggkittypes.BaseEthereumClienter, cfg Config, network Network) (*ReorgDetector, error) {
	log := log.WithFields("reorg-detector", network.String())
	err := migrations.RunMigrations(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	db, err := db.NewSQLiteDB(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if cfg.FinalizedBlock.IsEmpty() {
		log.Warnf("Finalized block is not set. Setting to finalized block")
		cfg.FinalizedBlock = aggkittypes.FinalizedBlock
	}

	return &ReorgDetector{
		client:             client,
		db:                 db,
		checkReorgInterval: cfg.GetCheckReorgsInterval(),
		finalizedBlockType: cfg.FinalizedBlock,
		network:            network,
		trackedBlocks:      make(map[string]*headersList),
		subscriptions:      make(map[string]*Subscription),
		log:                log,
		headersCache:       make(map[uint64]*aggkittypes.BlockHeader),
		missingBlocks:      make(map[string]Header),
	}, nil
}

// Start starts the reorg detector
func (rd *ReorgDetector) Start(ctx context.Context) error {
	// Load tracked blocks from the DB
	if err := rd.loadTrackedHeaders(); err != nil {
		return fmt.Errorf("failed to load tracked headers: %w", err)
	}

	// Continuously check reorgs in tracked by subscribers blocks
	go func() {
		ticker := time.NewTicker(rd.checkReorgInterval)
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				// err is scoped to this iteration: a named return shared with the caller here
				// would let this goroutine (which keeps running after Start returns) race with
				// the caller's own final write to it on the "return nil" below.
				if err := rd.detectReorgInTrackedList(ctx); err != nil {
					log.Errorf("failed to detect reorg in tracked list: %v", err)
				}
			}
		}
	}()

	return nil
}

func (rd *ReorgDetector) String() string {
	if rd == nil {
		return "ReorgDetector{nil}"
	}
	return fmt.Sprintf("ReorgDetector{network: %s, finalized: %v, check_interval: %s}",
		rd.network, rd.finalizedBlockType, rd.checkReorgInterval)
}

// GetFinalizedBlockType returns the finalized block name
func (rd *ReorgDetector) GetFinalizedBlockType() aggkittypes.BlockNumberFinality {
	return rd.finalizedBlockType
}

// GetDB returns the database connection for testing purposes
func (rd *ReorgDetector) GetDB() *sql.DB {
	return rd.db
}

// AddBlockToTrack adds a block to the tracked list for a subscriber
func (rd *ReorgDetector) AddBlockToTrack(ctx context.Context, id string, num uint64, hash common.Hash) error {
	// Skip if the given block has already been stored
	rd.trackedBlocksLock.RLock()
	trackedBlocks, ok := rd.trackedBlocks[id]
	if !ok {
		rd.trackedBlocksLock.RUnlock()
		return fmt.Errorf("subscriber %s is not subscribed", id)
	}
	rd.trackedBlocksLock.RUnlock()
	if existingHeader, err := trackedBlocks.get(num); err == nil && existingHeader.Hash == hash {
		return nil
	}

	// Store the given header to the tracked list
	hdr := newHeader(num, hash)
	if err := rd.saveTrackedBlock(id, hdr); err != nil {
		return fmt.Errorf("failed to save tracked block: %w", err)
	}

	return nil
}

// ErrSubscriberNotFound is returned by GetTrackedBlockByBlockNumber when the id has no tracker, so it can't be
// confused with db.ErrNotFound, which means that the block is not tracked by a known subscriber
var ErrSubscriberNotFound = errors.New("reorgdetector: subscriber not found")

// GetTrackedBlockByBlockNumber returns the tracked block of a subscriber by block number (1 or none, from
// headercache). It returns ErrSubscriberNotFound if the id is not subscribed and db.ErrNotFound if the block
// is not tracked
func (rd *ReorgDetector) GetTrackedBlockByBlockNumber(id string, blockNumber uint64) (*Header, error) {
	rd.trackedBlocksLock.RLock()
	defer rd.trackedBlocksLock.RUnlock()
	header, ok := rd.trackedBlocks[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSubscriberNotFound, id)
	}
	return header.get(blockNumber)
}

// detectReorgInTrackedList detects reorgs in the tracked blocks.
// Notifies subscribers if reorg has happened
func (rd *ReorgDetector) detectReorgInTrackedList(ctx context.Context) error {
	// Get the latest finalized block
	lastFinalizedBlock, err := rd.client.CustomHeaderByNumber(ctx, &rd.finalizedBlockType)
	if err != nil {
		return fmt.Errorf("failed to get the latest finalized block: %w", err)
	}
	var (
		errGroup errgroup.Group
	)

	subscriberIDs := rd.getSubscriberIDs()
	startTime := time.Now()
	for _, id := range subscriberIDs {
		// This is done like this because of a possible deadlock
		// between AddBlocksToTrack and detectReorgInTrackedList
		rd.trackedBlocksLock.RLock()
		hdrs, ok := rd.trackedBlocks[id]
		rd.trackedBlocksLock.RUnlock()

		if !ok {
			continue
		}

		rd.log.Debugf("Checking reorgs in all tracked blocks (finalized up to block %d)",
			lastFinalizedBlock.Number)

		errGroup.Go(func() error {
			return rd.detectReorgForSubscriber(ctx, id, hdrs, lastFinalizedBlock.Number, startTime)
		})
	}

	return errGroup.Wait()
}

// detectReorgForSubscriber checks the tracked blocks of a subscriber, in ascending order, against the chain.
// The first tracked block whose hash differs from the one in the chain, or that is above the chain head
// (the chain was rolled back and it has not grown back yet), is reported as a reorg from that block
func (rd *ReorgDetector) detectReorgForSubscriber(ctx context.Context, id string, hdrs *headersList,
	lastFinalizedBlockNum uint64, startTime time.Time) error {
	// The block found above the head in the previous pass (if any). It is consumed here, so the confirmation
	// only counts if the same block is missing in two consecutive passes
	previousMissing, hadPreviousMissing := rd.takeMissingBlock(id)

	headers := hdrs.getSorted()
	for _, hdr := range headers {
		// Get the actual header from the network or from the cache
		currentHeader, err := rd.client.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(hdr.Num))
		if err != nil {
			head, aboveHead, checkErr := rd.isAboveHead(ctx, hdr.Num, err)
			if checkErr != nil {
				return checkErr
			}
			if !aboveHead {
				return fmt.Errorf("failed to get the header %d: %w", hdr.Num, err)
			}
			if hdr.Num <= lastFinalizedBlockNum || head < lastFinalizedBlockNum {
				// The chain cannot be shorter than its finalized block: the head comes from a node that is behind
				return fmt.Errorf("failed to get the header %d: inconsistent chain head %d (finalized block %d): %w",
					hdr.Num, head, lastFinalizedBlockNum, err)
			}
			if !hadPreviousMissing || previousMissing != hdr {
				// It could be a node that is behind the one used by the subscriber: wait for the next pass
				rd.setMissingBlock(id, hdr)
				rd.log.Warnf("Tracked block %d (%s) of subscriber %s is above the chain head %d. "+
					"It will be reported as a reorg if it is still missing in the next check",
					hdr.Num, hdr.Hash, id, head)
				return nil
			}
			// The chain is shorter than the tracked block: the block has been reorged and there is no block in
			// its place, so the current hash is empty
			return rd.processReorg(id, hdrs, hdr, headers[len(headers)-1].Num, common.Hash{}, startTime)
		}

		rd.headersCacheLock.Lock()
		oldHeader, ok := rd.headersCache[hdr.Num]
		if !ok || oldHeader == nil {
			rd.headersCache[hdr.Num] = currentHeader
			oldHeader = currentHeader
		}
		rd.headersCacheLock.Unlock()

		// Check if the block hash matches with the actual block hash
		if hdr.Hash == oldHeader.Hash && currentHeader.Hash == hdr.Hash {
			// Delete block from the tracked blocks list if it is less than or equal to the last finalized block
			// and hashes matches. If higher than finalized block, we assume a reorg still might happen.
			if hdr.Num <= lastFinalizedBlockNum {
				hdrs.removeRange(hdr.Num, hdr.Num)

				if err := rd.removeTrackedBlockRange(id, hdr.Num, hdr.Num); err != nil {
					return fmt.Errorf("error removing blocks from DB for subscriber %s between blocks %d and %d: %w",
						id, hdr.Num, hdr.Num, err)
				}
			}

			continue
		}

		return rd.processReorg(id, hdrs, hdr, headers[len(headers)-1].Num, currentHeader.Hash, startTime)
	}

	return nil
}

// isAboveHead tells if the block blockNum, whose header could not be obtained (headerErr), is above the current
// chain head. It only returns true if the error is a "not found" and the head, which is returned as well, is lower
// than blockNum. Any other error getting the header is not conclusive, so it is not treated as a missing block
func (rd *ReorgDetector) isAboveHead(ctx context.Context, blockNum uint64,
	headerErr error) (head uint64, aboveHead bool, err error) {
	if !errors.Is(headerErr, ethereum.NotFound) && !aggkittypes.IsErrNotFound(headerErr) {
		return 0, false, nil
	}
	head, err = rd.client.BlockNumber(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("failed to get the header %d (%w) and failed to get the chain head: %w",
			blockNum, headerErr, err)
	}

	return head, blockNum > head, nil
}

// takeMissingBlock returns the block of the subscriber that was above the chain head in the previous pass
// and forgets it
func (rd *ReorgDetector) takeMissingBlock(id string) (Header, bool) {
	rd.missingBlocksLock.Lock()
	defer rd.missingBlocksLock.Unlock()
	hdr, ok := rd.missingBlocks[id]
	delete(rd.missingBlocks, id)

	return hdr, ok
}

// setMissingBlock stores the tracked block of the subscriber that is above the chain head in this pass
func (rd *ReorgDetector) setMissingBlock(id string, hdr Header) {
	rd.missingBlocksLock.Lock()
	defer rd.missingBlocksLock.Unlock()
	rd.missingBlocks[id] = hdr
}

// processReorg stores the reorg event of the subscriber, that goes from the block reorgedBlock to the block
// toBlock, notifies the subscriber and removes the reorged blocks from the tracked list
func (rd *ReorgDetector) processReorg(id string, hdrs *headersList, reorgedBlock Header, toBlock uint64,
	currentHash common.Hash, startTime time.Time) error {
	event := ReorgEvent{
		DetectedAt:   startTime.Unix(),
		FromBlock:    reorgedBlock.Num,
		ToBlock:      toBlock,
		SubscriberID: id,
		CurrentHash:  currentHash,
		TrackedHash:  reorgedBlock.Hash,
	}
	if err := rd.insertReorgEvent(event); err != nil {
		return fmt.Errorf("failed to insert reorg event: %w", err)
	}
	rd.log.Warnf("Reorg detected %s for subscriber %s between blocks %d and %d. currentHash: %s trackHash: %s",
		rd.network, event.SubscriberID, event.FromBlock, event.ToBlock, event.CurrentHash, event.TrackedHash)
	// Notify the subscriber about the reorg
	rd.notifySubscriber(id, reorgedBlock)
	// Remove the reorged block and all the following blocks from DB
	if err := rd.removeTrackedBlockRange(event.SubscriberID, event.FromBlock, event.ToBlock); err != nil {
		return fmt.Errorf("error removing blocks from DB for subscriber %s between blocks %d and %d: %w",
			event.SubscriberID, event.FromBlock, event.ToBlock, err)
	}
	// Remove the reorged block and all the following blocks from memory
	hdrs.removeRange(event.FromBlock, event.ToBlock)

	// clean the headers cache
	rd.headersCacheLock.Lock()
	for i := event.FromBlock; i <= event.ToBlock; i++ {
		delete(rd.headersCache, i)
	}
	rd.headersCacheLock.Unlock()

	return nil
}

// loadTrackedHeaders loads tracked headers from the DB and stores them in memory
func (rd *ReorgDetector) loadTrackedHeaders() (err error) {
	rd.trackedBlocksLock.Lock()
	defer rd.trackedBlocksLock.Unlock()

	// Load tracked blocks for all subscribers from the DB
	if rd.trackedBlocks, err = rd.getTrackedBlocks(); err != nil {
		return fmt.Errorf("failed to get tracked blocks: %w", err)
	}

	rd.subscriptionsLock.Lock()
	defer rd.subscriptionsLock.Unlock()
	// Go over tracked blocks and create subscription for each tracker
	for id := range rd.trackedBlocks {
		rd.subscriptions[id] = newSubscription()
	}

	return nil
}
