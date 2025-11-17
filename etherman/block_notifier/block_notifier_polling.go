package blocknotifier

import (
	"context"
	"fmt"
	"sync"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	ethermantypes "github.com/agglayer/aggkit/etherman/types"
	ethmantypes "github.com/agglayer/aggkit/etherman/types"
	aggkittypes "github.com/agglayer/aggkit/types"
)

var (
	timeNowFunc                             = time.Now
	_           ethermantypes.BlockNotifier = (*BlockNotifierPolling)(nil)
)

const (
	AutomaticBlockInterval = time.Second * 0
	// minBlockInterval is the minimum interval at which the AggSender will check for new blocks
	minBlockInterval = time.Second
	// maxBlockInterval is the maximum interval at which the AggSender will check for new blocks
	maxBlockInterval = time.Minute
	// Percentage period of reach the next block
	percentForNextBlock = 80
)

type ConfigBlockNotifierPolling struct {
	// BlockFinalityType is the finality of the block to be notified
	BlockFinalityType aggkittypes.BlockNumberFinality
	// CheckNewBlockInterval is the interval at which the AggSender will check for new blocks
	// if is 0 it will be calculated automatically
	CheckNewBlockInterval time.Duration
}

type BlockNotifierPolling struct {
	ethClient     aggkittypes.BaseEthereumClienter
	blockFinality aggkittypes.BlockNumberFinality
	logger        aggkitcommon.Logger
	config        ConfigBlockNotifierPolling
	mu            sync.Mutex
	lastStatus    *blockNotifierPollingInternalStatus
	aggkitcommon.PubSub[ethmantypes.EventNewBlock]
}

// NewBlockNotifierPolling creates a new BlockNotifierPolling.
// if param `subscriber` is nil a new GenericSubscriber[types.EventNewBlock] will be created.
// To use this class you need to subscribe and each time that a new block appear the subscriber
// will be notified through the channel. (check unit tests TestExploratoryBlockNotifierPolling
// for more information)
func NewBlockNotifierPolling(ethClient aggkittypes.BaseEthereumClienter,
	config ConfigBlockNotifierPolling,
	logger aggkitcommon.Logger,
	subscriber aggkitcommon.PubSub[ethmantypes.EventNewBlock]) (*BlockNotifierPolling, error) {
	if subscriber == nil {
		subscriber = aggkitcommon.NewGenericSubscriber[ethmantypes.EventNewBlock]()
	}

	return &BlockNotifierPolling{
		ethClient:     ethClient,
		blockFinality: config.BlockFinalityType,
		logger:        logger,
		config:        config,
		PubSub:        subscriber,
	}, nil
}

func (b *BlockNotifierPolling) String() string {
	status := b.getGlobalStatus()
	res := fmt.Sprintf("BlockNotifierPolling: finality=%s", b.config.BlockFinalityType.String())
	if status != nil {
		res += fmt.Sprintf(" lastBlockSeen=%d", status.lastBlockSeen)
	} else {
		res += " lastBlockSeen=none"
	}
	return res
}

func (b *BlockNotifierPolling) Initialize(ctx context.Context) error {
	_, newStatus, _ := b.step(ctx, nil)
	status := newStatus
	b.setGlobalStatus(status)
	return nil
}

// Start starts the BlockNotifierPolling blocking the current goroutine
func (b *BlockNotifierPolling) Start(ctx context.Context) {
	ticker := time.NewTimer(b.config.CheckNewBlockInterval)
	defer ticker.Stop()

	var status *blockNotifierPollingInternalStatus = nil

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			delay, newStatus, event := b.step(ctx, status)
			status = newStatus
			b.setGlobalStatus(status)
			if event != nil {
				b.Publish(*event)
			}
			ticker.Reset(delay)
		}
	}
}
func (b *BlockNotifierPolling) BlockFinality() aggkittypes.BlockNumberFinality {
	return b.blockFinality
}

func (b *BlockNotifierPolling) GetCurrentBlockNumber() uint64 {
	status := b.getGlobalStatus()
	if status == nil {
		return 0
	}
	return status.lastBlockSeen
}

func (b *BlockNotifierPolling) setGlobalStatus(status *blockNotifierPollingInternalStatus) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastStatus = status
}

func (b *BlockNotifierPolling) getGlobalStatus() *blockNotifierPollingInternalStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastStatus == nil {
		return nil
	}
	copyStatus := *b.lastStatus
	return &copyStatus
}

// step is the main function of the BlockNotifierPolling, it checks if there is a new block
// it returns:
// - the delay for the next check
// - the new status
// - the new even to emit or nil
func (b *BlockNotifierPolling) step(ctx context.Context,
	previousState *blockNotifierPollingInternalStatus) (time.Duration,
	*blockNotifierPollingInternalStatus, *ethmantypes.EventNewBlock) {
	currentBlock, err := b.blockFinality.BlockNumber(ctx, b.ethClient)
	if err != nil {
		b.logger.Errorf("Failed to get block number %s: %v", b.blockFinality.String(), err)
		newState := previousState.clear()
		return b.nextBlockRequestDelay(nil, err), newState, nil
	}
	if previousState == nil {
		newState := previousState.initialBlock(currentBlock)
		return b.nextBlockRequestDelay(previousState, nil), newState, nil
	}
	if currentBlock == previousState.lastBlockSeen {
		// No new block, so no changes on state
		return b.nextBlockRequestDelay(previousState, nil), previousState, nil
	}
	// New blockNumber!
	eventToEmit := &ethmantypes.EventNewBlock{
		BlockNumber:       currentBlock,
		BlockFinalityType: b.config.BlockFinalityType,
	}
	if previousState.lastBlockSeen > currentBlock {
		b.logger.Warnf("Block number decreased [finality:%s]: %d -> %d",
			b.config.BlockFinalityType.String(), previousState.lastBlockSeen, currentBlock)
		// It start from scratch because something fails in calculation of block period
		newState := previousState.initialBlock(currentBlock)
		return b.nextBlockRequestDelay(nil, nil), newState, eventToEmit
	}

	if currentBlock-previousState.lastBlockSeen != 1 {
		if !b.config.BlockFinalityType.IsSafe() && !b.config.BlockFinalityType.IsFinalized() {
			b.logger.Warnf("Missed block(s) [finality:%s]: %d -> %d",
				b.config.BlockFinalityType.String(), previousState.lastBlockSeen, currentBlock)
		}

		// It start from scratch because something fails in calculation of block period
		newState := previousState.initialBlock(currentBlock)
		return b.nextBlockRequestDelay(nil, nil), newState, eventToEmit
	}
	newState := previousState.incomingNewBlock(currentBlock)
	b.logger.Debugf("New block seen [finality:%s]: %d. blockRate:%s",
		b.config.BlockFinalityType.String(), currentBlock, newState.previousBlockTime)
	eventToEmit.BlockRate = *newState.previousBlockTime
	return b.nextBlockRequestDelay(newState, nil), newState, eventToEmit
}

func (b *BlockNotifierPolling) nextBlockRequestDelay(status *blockNotifierPollingInternalStatus,
	err error) time.Duration {
	if b.config.CheckNewBlockInterval != AutomaticBlockInterval {
		return b.config.CheckNewBlockInterval
	}
	// Initial stages wait the minimum interval to increas accuracy
	if status == nil || status.previousBlockTime == nil {
		return minBlockInterval
	}
	if err != nil {
		// If error we wait twice the min interval
		return minBlockInterval * 2 //nolint:mnd // 2 times the interval
	}
	// we have a previous block time so we can calculate the interval
	now := timeNowFunc()
	expectedTimeNextBlock := status.lastBlockTime.Add(*status.previousBlockTime)
	distanceToNextBlock := expectedTimeNextBlock.Sub(now)
	interval := distanceToNextBlock * percentForNextBlock / 100 //nolint:mnd //  percent period for reach the next block
	return max(minBlockInterval, min(maxBlockInterval, interval))
}

type blockNotifierPollingInternalStatus struct {
	lastBlockSeen     uint64
	lastBlockTime     time.Time      // first appear of block lastBlockSeen
	previousBlockTime *time.Duration // time of the previous block to appear
}

func (s *blockNotifierPollingInternalStatus) String() string {
	if s == nil {
		return "nil"
	}
	return fmt.Sprintf("lastBlockSeen=%d lastBlockTime=%s previousBlockTime=%s",
		s.lastBlockSeen, s.lastBlockTime, s.previousBlockTime)
}

func (s *blockNotifierPollingInternalStatus) clear() *blockNotifierPollingInternalStatus {
	return &blockNotifierPollingInternalStatus{}
}

func (s *blockNotifierPollingInternalStatus) initialBlock(block uint64) *blockNotifierPollingInternalStatus {
	return &blockNotifierPollingInternalStatus{
		lastBlockSeen: block,
		lastBlockTime: timeNowFunc(),
	}
}

func (s *blockNotifierPollingInternalStatus) incomingNewBlock(block uint64) *blockNotifierPollingInternalStatus {
	now := timeNowFunc()
	timePreviousBlock := now.Sub(s.lastBlockTime)
	return &blockNotifierPollingInternalStatus{
		lastBlockSeen:     block,
		lastBlockTime:     now,
		previousBlockTime: &timePreviousBlock,
	}
}
