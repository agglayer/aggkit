package epochnotifier

import (
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
)

type EpochConfig struct {
	FirstEpochBlock  uint64
	NumBlockPerEpoch uint
}

func NewEpochConfigromAgglayer(aggLayer agglayer.AggLayerClientGetEpochConfiguration,
) (*EpochConfig, error) {
	if aggLayer == nil {
		return nil, fmt.Errorf("NewEpochConfigromAgglayer: aggLayerClient is required")
	}
	clockConfig, err := aggLayer.GetEpochConfiguration()
	if err != nil {
		return nil, fmt.Errorf("NewEpochConfigromAgglayer: error getting clock configuration from AggLayer: %w", err)
	}
	return NewEpochConfig(clockConfig.GenesisBlock, uint(clockConfig.EpochDuration)), nil
}

func NewEpochConfig(startingEpochBlock uint64, numBlockPerEpoch uint) *EpochConfig {
	return &EpochConfig{
		FirstEpochBlock:  startingEpochBlock,
		NumBlockPerEpoch: numBlockPerEpoch,
	}
}

func (a *EpochConfig) String() string {
	if a == nil {
		return "EpochConfig{nil}"
	}
	return fmt.Sprintf("EpochConfig{startEpochBlock=%d, numBlockPerEpoch=%d}",
		a.FirstEpochBlock, a.NumBlockPerEpoch)
}

// Validate validates the configuration
func (a *EpochConfig) Validate() error {
	if a.NumBlockPerEpoch == 0 {
		return fmt.Errorf("EpochConfig.Valiadte: numBlockPerEpoch: num block per epoch is required > 0 ")
	}
	return nil
}

// StartingBlockEpoch returns the starting block for an epoch number
func (a *EpochConfig) StartingBlockEpoch(epoch uint64) uint64 {
	if epoch == 0 {
		return a.FirstEpochBlock - 1
	}
	return a.FirstEpochBlock + ((epoch - 1) * uint64(a.NumBlockPerEpoch))
}

// EndBlockEpoch returns the ending block for an epoch number
func (a *EpochConfig) EndBlockEpoch(epoch uint64) uint64 {
	return a.StartingBlockEpoch(epoch + 1)
}

// EpochNumber returns the epoch number for a block
func (a *EpochConfig) EpochNumber(currentBlock uint64) uint64 {
	if currentBlock < a.FirstEpochBlock {
		return 0
	}
	return 1 + ((currentBlock - a.FirstEpochBlock) / uint64(a.NumBlockPerEpoch))
}
