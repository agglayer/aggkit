package epochnotifier

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/common"
)

type Trigger interface {
	// OnNewBlock returns true if a notification is required
	OnNewBlock(
		epoch *EpochConfig,
		lastEpochEvent *types.EpochEvent, newBlock *types.EventNewBlock) (bool, *types.EpochEvent)
	// CheckCanSendCertificate returns an error if the certificate can't be sent
	CheckCanSendCertificate(epoch *EpochConfig, lastBlock types.Block) error
}

type EpochNotifierBase struct {
	blockNotifier          types.BlockNotifier
	logger                 types.Logger
	Trigger                Trigger
	lastStartingEpochBlock uint64

	EpochConfig EpochConfig
	types.GenericSubscriber[types.EpochEvent]
}

func NewEpochNotifierBase(blockNotifier types.BlockNotifier,
	logger types.Logger,
	config EpochConfig,
	Trigger Trigger,
	subscriber types.GenericSubscriber[types.EpochEvent]) (*EpochNotifierBase, error) {
	if subscriber == nil {
		subscriber = common.NewGenericSubscriberImpl[types.EpochEvent]()
	}

	err := config.Validate()
	if err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &EpochNotifierBase{
		blockNotifier:          blockNotifier,
		logger:                 logger,
		Trigger:                Trigger,
		lastStartingEpochBlock: config.FirstEpochBlock,
		EpochConfig:            config,
		GenericSubscriber:      subscriber,
	}, nil
}

func (e *EpochNotifierBase) String() string {
	return fmt.Sprintf("EpochNotifierBase: config: %s", e.EpochConfig.String())
}

// Start starts the notifier synchronously
func (e *EpochNotifierBase) Start(ctx context.Context) {
	eventNewBlockChannel := e.blockNotifier.Subscribe("EpochNotifierPerBlock")
	e.startInternal(ctx, eventNewBlockChannel)
}

func (e *EpochNotifierBase) CheckCanSendCertificate() error {
	currentBlock := e.blockNotifier.GetCurrentBlockNumber()
	if currentBlock == nil {
		return fmt.Errorf("can't get current block")
	}
	return e.Trigger.CheckCanSendCertificate(&e.EpochConfig, *currentBlock)
}

type internalStatus struct {
	lastBlockSeen   *types.Block
	waitingForEpoch uint64
}

func (e *EpochNotifierBase) startInternal(ctx context.Context, eventNewBlockChannel <-chan types.EventNewBlock) {
	status := internalStatus{
		lastBlockSeen:   nil,
		waitingForEpoch: e.EpochConfig.EpochNumber(e.EpochConfig.FirstEpochBlock),
	}
	for {
		select {
		case <-ctx.Done():
			return
		case newBlock := <-eventNewBlockChannel:
			var event *types.EpochEvent
			status, event = e.step(status, &newBlock)
			if event != nil {
				e.logger.Debugf("new Epoch Event: %s", event.String())
				e.GenericSubscriber.Publish(*event)
			}
		}
	}
}

func (e *EpochNotifierBase) step(status internalStatus,
	newBlockEvent *types.EventNewBlock) (internalStatus, *types.EpochEvent) {
	currentBlock := newBlockEvent.Block
	if currentBlock.Number < e.EpochConfig.FirstEpochBlock {
		// This is a bit strange, the first epoch is in the future
		e.logger.Warnf("Block number %d is before the starting first epoch block %d."+
			" Please check your config", currentBlock, e.EpochConfig.FirstEpochBlock)
		return status, nil
	}
	// No new block
	if status.lastBlockSeen != nil && (currentBlock.Number <= status.lastBlockSeen.Number) {
		return status, nil
	}
	status.lastBlockSeen = &currentBlock

	mustNotify, event := e.Trigger.OnNewBlock(&e.EpochConfig, nil, newBlockEvent)

	logFunc := e.logger.Debugf
	if mustNotify {
		logFunc = e.logger.Infof
	}
	logFunc("New block seen  BlockNumber:%d (finality:%s) blockRate: %s, notify: %v, event: %s, config:%s",
		newBlockEvent.BlockFinalityType, newBlockEvent.Block.Number, newBlockEvent.BlockRate, mustNotify, event.String(), e.EpochConfig.String())
	if mustNotify {
		// Notify the epoch has started
		status.waitingForEpoch = event.Epoch + 1
		return status, event
	}
	return status, nil
}
