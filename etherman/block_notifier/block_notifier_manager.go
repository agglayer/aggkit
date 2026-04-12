package blocknotifier

import (
	"context"
	"sync"

	aggkitcommon "github.com/agglayer/aggkit/common"
	ethermantypes "github.com/agglayer/aggkit/etherman/types"
	aggkittypes "github.com/agglayer/aggkit/types"
)

// BlockNotifierManager manages multiple BlockNotifiers for different finality types.
// implements BlockNotifierGetter interface.
type BlockNotifierManager struct {
	mutex           sync.Mutex
	blockNotifiers  map[aggkittypes.BlockNumberFinality]ethermantypes.BlockNotifier
	constructorFunc func(aggkittypes.BlockNumberFinality) (ethermantypes.BlockNotifier, error)
	logger          aggkitcommon.Logger
}

var _ ethermantypes.BlockNotifierManager = (*BlockNotifierManager)(nil)

func NewBlockNotifierManager(logger aggkitcommon.Logger,
	constructorFunc func(aggkittypes.BlockNumberFinality) (ethermantypes.BlockNotifier, error)) *BlockNotifierManager {
	return &BlockNotifierManager{
		blockNotifiers:  make(map[aggkittypes.BlockNumberFinality]ethermantypes.BlockNotifier),
		constructorFunc: constructorFunc,
		logger:          logger,
	}
}

// TODO: You must only have real blockNotifiers for latest, safe, finalized...
// the rest (that are offsets) must use the principal ones to reduce
// the numbers of RPC requests and goroutines
func (bnm *BlockNotifierManager) GetBlockNotifier(ctx context.Context,
	blockFinality aggkittypes.BlockNumberFinality) (ethermantypes.BlockNotifier, error) {
	bnm.mutex.Lock()
	defer bnm.mutex.Unlock()

	bn, exists := bnm.blockNotifiers[blockFinality]
	if !exists {
		bn, err := bnm.constructorFunc(blockFinality)
		if err != nil {
			return nil, err
		}
		bnm.blockNotifiers[blockFinality] = bn
		err = bn.Initialize(ctx)
		if err != nil {
			return nil, err
		}
		bnm.logger.Infof("Starting BlockNotifier for finality=%s currentBlock=%d",
			blockFinality.String(), bn.GetCurrentBlockNumber())
		go bn.Start(ctx)
		return bn, nil
	}
	return bn, nil
}
func (bnm *BlockNotifierManager) GetCurrentBlockNumber(ctx context.Context,
	blockFinality aggkittypes.BlockNumberFinality) (uint64, error) {
	if blockFinality.IsConstant() {
		return blockFinality.Specific, nil
	}
	bn, err := bnm.GetBlockNotifier(ctx, blockFinality)
	if err != nil {
		return 0, err
	}
	return bn.GetCurrentBlockNumber(), nil
}
