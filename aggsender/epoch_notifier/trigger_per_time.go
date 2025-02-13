package epochnotifier

import (
	"fmt"
	"time"

	aggsenderconfig "github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/types"
)

type ExtraInfoEventEpochPerTime struct {
	PendingBlocks int
	PendingTime   time.Duration
	Reason        string
}

func (e *ExtraInfoEventEpochPerTime) String() string {
	return fmt.Sprintf("EventPerTime: pendingBlocks=%d, pendingTime=%s, Reason=%s",
		e.PendingBlocks, e.PendingTime, e.Reason)
}

type TriggerPerTime struct {
	config aggsenderconfig.SubmitCertificateConfig
	logger types.Logger
	// lastBlockRate is the last block rate received or the one configured
	// it can be 0 that means that we don't have yet a valid blockRate
	lastBlockRate time.Duration
}

func NewTriggerPerTime(config aggsenderconfig.SubmitCertificateConfig, logger types.Logger) *TriggerPerTime {
	return &TriggerPerTime{
		config: config,
		logger: logger,
	}
}

func (t *TriggerPerTime) String() string {
	return "TriggerPerTime{}"
}

// OnNewBlock returns true if a notification is required
// based on the last event received and lastBlock received. Also returns the next event
func (t *TriggerPerTime) OnNewBlock(
	epoch *EpochConfig,
	lastEpochEvent *types.EpochEvent,
	newBlockEvent *types.EventNewBlock) (bool, *types.EpochEvent) {
	if newBlockEvent == nil || epoch == nil {
		return false, nil
	}
	var err error
	// Update block rate
	err = t.updateBlockRate(newBlockEvent)
	if err != nil {
		t.logger.Errorf("OnNewBlock, can't calculate blockRate: %s", err)
		return false, nil
	}
	// First adjust config to make sense with blockRate
	t.config = t.adjustConfig(epoch)

	insideWindows, reason := t.isInsideSubmitWindow(epoch, newBlockEvent.Block)
	if insideWindows && t.changeEpoch(epoch, lastEpochEvent, newBlockEvent.Block.Number) {
		remainingTimeForEndEpoch := TimeToEndOfEpoch(epoch, newBlockEvent.Block, t.getBlockRate())
		return true, &types.EpochEvent{
			Epoch: epoch.EpochNumber(newBlockEvent.Block.Number),
			ExtraInfo: &ExtraInfoEventEpochPerTime{
				PendingBlocks: int(epoch.EndBlockEpoch(epoch.EpochNumber(newBlockEvent.Block.Number)) - newBlockEvent.Block.Number),
				PendingTime:   remainingTimeForEndEpoch,
				Reason:        reason,
			},
		}
	}
	return false, nil
}

func (t *TriggerPerTime) CheckCanSendCertificate(epoch *EpochConfig,
	lastBlock types.Block) error {

	ok, reason := t.isInsideSubmitWindow(epoch, lastBlock)
	if !ok {
		return fmt.Errorf("can't send certificate: %s", reason)
	}
	return nil
}

func (t *TriggerPerTime) changeEpoch(epoch *EpochConfig, lastEpochEvent *types.EpochEvent, lastBlockNumber uint64) bool {
	if lastEpochEvent == nil {
		return true
	}
	currentEpoch := epoch.EpochNumber(lastBlockNumber)
	return lastEpochEvent.Epoch != currentEpoch
}

func (t *TriggerPerTime) getPendingBlocks(epoch *EpochConfig, newBlock *types.EventNewBlock) int {
	return int(epoch.EndBlockEpoch(epoch.EpochNumber(newBlock.Block.Number)) - newBlock.Block.Number)
}

// IsInsideSubmitWindow returns true if the certificate can be sent
// if not it returns false and the reason as text
func (t *TriggerPerTime) isInsideSubmitWindow(epoch *EpochConfig,
	newBlock types.Block) (bool, string) {
	blockRate := t.lastBlockRate
	remainingTimeForEndEpoch := TimeToEndOfEpoch(epoch, newBlock, t.getBlockRate())
	if remainingTimeForEndEpoch < t.config.SubmitIfRemainsForNextEpoch &&
		remainingTimeForEndEpoch > t.config.NoSubmitIfRemainsForNextEpoch {
		return true, fmt.Sprintf("inside submit window")
	}

	// check if the expected time will be never met
	if EpochRate(epoch, blockRate) < t.config.SubmitIfRemainsForNextEpoch {
		// epochRate is lower than TimeBeforeEndingEpoch, so we can't send the certificate
		// we decide to allow to send a certificate just at starting of a new epoch
		currentEpoch := epoch.EpochNumber(newBlock.Number)
		if epoch.StartingBlockEpoch(currentEpoch) == newBlock.Number {
			return true, fmt.Sprintf("missconfigured StartWindowBeforeEndingEpoch(%s), so allow just starting of epoch %d",
				t.config.SubmitIfRemainsForNextEpoch)
		}
	}
	return false, fmt.Sprintf("outside submit window")
}

func (t *TriggerPerTime) adjustConfig(epoch *EpochConfig) aggsenderconfig.SubmitCertificateConfig {
	blockRate := t.lastBlockRate
	res := aggsenderconfig.SubmitCertificateConfig{
		L1BlockCreationRate:           blockRate,
		SubmitIfRemainsForNextEpoch:   t.config.SubmitIfRemainsForNextEpoch,
		NoSubmitIfRemainsForNextEpoch: t.config.NoSubmitIfRemainsForNextEpoch,
	}
	epochRate := EpochRate(epoch, blockRate)
	if epochRate < t.config.SubmitIfRemainsForNextEpoch {
		// adjust to starting block
		t.logger.Warnf("adjusting SubmitIfRemainsForNextEpoch to epoch rate %s", epochRate)
		res.SubmitIfRemainsForNextEpoch = epochRate - blockRate
	}

	if t.config.NoSubmitIfRemainsForNextEpoch > res.SubmitIfRemainsForNextEpoch {
		t.logger.Warnf("adjusting NoSubmitIfRemainsForNextEpoch to SubmitIfRemainsForNextEpoch %s", res.SubmitIfRemainsForNextEpoch)
		res.NoSubmitIfRemainsForNextEpoch = res.SubmitIfRemainsForNextEpoch
	}
	return res
}

func (t *TriggerPerTime) updateBlockRate(newBlock *types.EventNewBlock) error {
	if t.config.L1BlockCreationRate != 0 {
		t.lastBlockRate = t.config.L1BlockCreationRate
		return nil
	}
	if newBlock != nil && newBlock.BlockRate != 0 {
		t.lastBlockRate = newBlock.BlockRate
		return nil
	}
	return fmt.Errorf("blockRate: block rate can't be estimated,config is zero and newBLock event is not set")
}

func (t *TriggerPerTime) getBlockRate() time.Duration {
	return t.lastBlockRate
}
