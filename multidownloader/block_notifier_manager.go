package multidownloader

import (
	"context"
	"sync"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
)

// BlockNotifierManager manages multiple BlockNotifiers for different finality types.
// implements BlockNotifierGetter interface.
type BlockNotifierManager struct {
	mutex           sync.Mutex
	blockNotifiers  map[aggkittypes.BlockNumberFinality]mdrtypes.BlockNotifier
	constructorFunc func(aggkittypes.BlockNumberFinality) (mdrtypes.BlockNotifier, error)
	logger          aggkitcommon.Logger
}

func NewBlockNotifierManager(logger aggkitcommon.Logger,
	constructorFunc func(aggkittypes.BlockNumberFinality) (mdrtypes.BlockNotifier, error)) *BlockNotifierManager {
	return &BlockNotifierManager{
		blockNotifiers:  make(map[aggkittypes.BlockNumberFinality]mdrtypes.BlockNotifier),
		constructorFunc: constructorFunc,
		logger:          logger,
	}
}

// TODO: You must only have real blockNotifiers for latest, safe, finalized...
// the rest (that are offsets) must use the principal ones to reduce
// the numbers of RPC requests and goroutines
func (bnm *BlockNotifierManager) GetBlockNotifier(ctx context.Context,
	blockFinality aggkittypes.BlockNumberFinality) (mdrtypes.BlockNotifier, error) {
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
