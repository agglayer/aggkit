package epochnotifier

import (
	"time"

	"github.com/agglayer/aggkit/aggsender/types"
)

func TimeToEndOfEpoch(epochToBlock *EpochConfig, currentBlock types.Block, blockRate time.Duration) time.Duration {
	// Calculate the current epoch
	currentEpoch := epochToBlock.EpochNumber(currentBlock.Number)
	// Calculate the end of the current epoch
	endOfCurrentEpoch := epochToBlock.EndBlockEpoch(currentEpoch)
	// Calculate the time to the end of the current epoch
	timeToEpochEnd := time.Until(currentBlock.Timestamp.Add(time.Duration(endOfCurrentEpoch-currentBlock.Number) * blockRate))
	return timeToEpochEnd
}

func BlockToTime(blockNumber uint64, sampleBlock types.Block, blockRate time.Duration) time.Time {
	return sampleBlock.Timestamp.Add(time.Duration(blockNumber-sampleBlock.Number) * blockRate)
}

func EpochRate(epochToBlock *EpochConfig, blockRate time.Duration) time.Duration {
	return time.Duration(epochToBlock.NumBlockPerEpoch) * blockRate
}
