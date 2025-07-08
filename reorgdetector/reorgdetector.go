package reorgdetector

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector/migrations"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
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
	client               aggkittypes.BaseEthereumClienter
	db                   *sql.DB
	checkReorgInterval   time.Duration
	finalizedBlockType   aggkittypes.BlockNumberFinality
	finalizedBlockNumber *big.Int
	network              Network

	trackedBlocksLock sync.RWMutex
	trackedBlocks     map[string]*headersList

	subscriptionsLock sync.RWMutex
	subscriptions     map[string]*Subscription

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

	finalizedBlockNumber, err := cfg.FinalizedBlock.ToBlockNum()
	if err != nil {
		return nil, err
	}

	return &ReorgDetector{
		client:               client,
		db:                   db,
		checkReorgInterval:   cfg.GetCheckReorgsInterval(),
		finalizedBlockType:   cfg.FinalizedBlock,
		finalizedBlockNumber: finalizedBlockNumber,
		network:              network,
		trackedBlocks:        make(map[string]*headersList),
		subscriptions:        make(map[string]*Subscription),
		log:                  log,
	}, nil
}

func (rd *ReorgDetector) IsDisabled() bool {
	return rd.finalizedBlockType == aggkittypes.LatestBlock
}

// Start starts the reorg detector
func (rd *ReorgDetector) Start(ctx context.Context) (err error) {
	// Load tracked blocks from the DB
	if err = rd.loadTrackedHeaders(); err != nil {
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
				if err = rd.detectReorgInTrackedList(ctx); err != nil {
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

// AddBlockToTrack adds a block to the tracked list for a subscriber
func (rd *ReorgDetector) AddBlockToTrack(ctx context.Context, id string, num uint64, hash common.Hash) error {
	if rd.IsDisabled() {
		return nil
	}
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

// detectReorgInTrackedList detects reorgs in the tracked blocks.
// Notifies subscribers if reorg has happened
func (rd *ReorgDetector) detectReorgInTrackedList(ctx context.Context) error {
	if rd.IsDisabled() {
		return nil
	}
	// Get the latest finalized block
	lastFinalisedBlock, err := rd.client.HeaderByNumber(ctx, rd.finalizedBlockNumber)
	if err != nil {
		return fmt.Errorf("failed to get the latest finalized block: %w", err)
	}

	var (
		headersCacheLock sync.Mutex
		headersCache     = map[uint64]*types.Header{
			lastFinalisedBlock.Number.Uint64(): lastFinalisedBlock,
		}
		errGroup errgroup.Group
	)

	subscriberIDs := rd.getSubscriberIDs()
	startTime := time.Now()
	for _, id := range subscriberIDs {
		id := id

		// This is done like this because of a possible deadlock
		// between AddBlocksToTrack and detectReorgInTrackedList
		rd.trackedBlocksLock.RLock()
		hdrs, ok := rd.trackedBlocks[id]
		rd.trackedBlocksLock.RUnlock()

		if !ok {
			continue
		}

		rd.log.Debugf("Checking reorgs in tracked blocks up to block %d", lastFinalisedBlock.Number.Uint64())

		errGroup.Go(func() error {
			headers := hdrs.getSorted()
			for _, hdr := range headers {
				// Get the actual header from the network or from the cache
				var err error
				headersCacheLock.Lock()
				currentHeader, ok := headersCache[hdr.Num]
				if !ok || currentHeader == nil {
					if currentHeader, err = rd.client.HeaderByNumber(ctx, new(big.Int).SetUint64(hdr.Num)); err != nil {
						headersCacheLock.Unlock()
						return fmt.Errorf("failed to get the header %d: %w", hdr.Num, err)
					}
					headersCache[hdr.Num] = currentHeader
				}
				headersCacheLock.Unlock()

				// Check if the block hash matches with the actual block hash
				if hdr.Hash == currentHeader.Hash() {
					// Delete block from the tracked blocks list if it is less than or equal to the last finalized block
					// and hashes matches. If higher than finalized block, we assume a reorg still might happen.
					if hdr.Num <= lastFinalisedBlock.Number.Uint64() {
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
					CurrentHash:  currentHeader.Hash(),
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
