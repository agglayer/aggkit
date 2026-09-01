package reorgdetector

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector/migrations"
	aggkittypes "github.com/agglayer/aggkit/types"
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

// func to get the tracked blocks for a subscriber by block number and hash, it should be 1 or none, from headercache
func (rd *ReorgDetector) GetTrackedBlockByBlockNumber(id string, blockNumber uint64) (*Header, error) {
	rd.trackedBlocksLock.RLock()
	defer rd.trackedBlocksLock.RUnlock()
	header, ok := rd.trackedBlocks[id]
	if !ok {
		return nil, db.ErrNotFound
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
			headers := hdrs.getSorted()
			for _, hdr := range headers {
				// Get the actual header from the network or from the cache
				currentHeader, err := rd.client.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(hdr.Num))
				if err != nil {
					return fmt.Errorf("failed to get the header %d: %w", hdr.Num, err)
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
					if hdr.Num <= lastFinalizedBlock.Number {
						hdrs.removeRange(hdr.Num, hdr.Num)

						if err := rd.removeTrackedBlockRange(id, hdr.Num, hdr.Num); err != nil {
							return fmt.Errorf("error removing blocks from DB for subscriber %s between blocks %d and %d: %w",
								id, hdr.Num, hdr.Num, err)
						}
					}

					continue
				}
				event := ReorgEvent{
					DetectedAt:   startTime.Unix(),
					FromBlock:    hdr.Num,
					ToBlock:      headers[len(headers)-1].Num,
					SubscriberID: id,
					CurrentHash:  currentHeader.Hash,
					TrackedHash:  hdr.Hash,
				}
				if err := rd.insertReorgEvent(event); err != nil {
					return fmt.Errorf("failed to insert reorg event: %w", err)
				}
				rd.log.Warnf("Reorg detected %s for subscriber %s between blocks %d and %d. currentHash: %s trackHash: %s",
					rd.network, event.SubscriberID, event.FromBlock, event.ToBlock, event.CurrentHash, event.TrackedHash)
				// Notify the subscriber about the reorg
				rd.notifySubscriber(id, hdr)
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

				break
			}
			return nil
		})
	}

	return errGroup.Wait()
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
		rd.subscriptions[id] = &Subscription{
			ReorgedBlock:   make(chan uint64),
			ReorgProcessed: make(chan bool),
		}
	}

	return nil
}
