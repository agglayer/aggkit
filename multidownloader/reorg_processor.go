package multidownloader

import (
	"context"
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	dbtypes "github.com/agglayer/aggkit/db/types"
	mdtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
)

type ReorgProcessor struct {
	log           aggkitcommon.Logger
	port          mdtypes.ReorgPorter
	developerMode bool
}

func NewReorgProcessor(log aggkitcommon.Logger,
	ethClient aggkittypes.BaseEthereumClienter,
	rpcClient aggkittypes.RPCClienter,
	storage mdtypes.Storager,
	developerMode bool) *ReorgProcessor {
	return &ReorgProcessor{
		log: log,
		port: &ReorgPort{
			ethClient: ethClient,
			rpcClient: rpcClient,
			storage:   storage,
		},
		developerMode: developerMode,
	}
}

// After detecting a reorg at detectedReorgError.OffendingBlockNumber,
// - find affected blocks
// - store the reorg info in storage
func (rm *ReorgProcessor) ProcessReorg(ctx context.Context,
	detectedReorgError mdtypes.DetectedReorgError) error {
	var err error
	// We known that offendingBlockNumber is affected, so we go backwards until we find
	// the first unaffected block
	offendingBlockNumber := detectedReorgError.OffendingBlockNumber
	if offendingBlockNumber == 0 {
		return fmt.Errorf("ProcessReorg: reorg detected at block 0, " +
			"this should never happen, check the reorg detection logic")
	}
	tx, err := rm.port.NewTx(ctx)
	if err != nil {
		return fmt.Errorf("ProcessReorg: error starting new tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			rm.log.Debugf("ProcessReorg: rolling back tx")
			if err := tx.Rollback(); err != nil {
				rm.log.Errorf("ProcessReorg: error rolling back tx: %v", err)
			}
		}
	}()
	firstUnaffectedBlock, err := rm.findFirstUnaffectedBlock(ctx, tx, offendingBlockNumber-1)
	if err != nil {
		return fmt.Errorf("ProcessReorg: error finding first unaffected block: %w", err)
	}
	if detectedReorgError.ReorgDetectionReason == mdtypes.ReorgDetectionReason_Forced {
		if rm.developerMode {
			rm.log.Warnf("ProcessReorg: executing a forcedReorg in block %d "+
				"It acts as missing blocks, so is going to delete blocks > %d."+
				"Overriding real unaffected block found %d."+
				"(forbidden in production! but developerMode is enabled))!!. ",
				offendingBlockNumber, offendingBlockNumber, firstUnaffectedBlock)
			firstUnaffectedBlock = offendingBlockNumber - 1
		} else {
			rm.log.Warnf("ProcessReorg: forced reorg at block %d with developerMode disabled, "+
				"using the first unaffected block found %d",
				offendingBlockNumber, firstUnaffectedBlock)
			// Continue with the reorg using the firstUnaffectedBlock found
		}
	}

	lastBlockNumberInStorage, err := rm.port.GetLastBlockNumberInStorage(tx)
	if err != nil {
		return fmt.Errorf("ProcessReorg: error getting last block number in storage: %w", err)
	}
	latestBlockNumberInRPC, err := rm.port.GetBlockNumberInRPC(ctx, aggkittypes.LatestBlock)
	if err != nil {
		return fmt.Errorf("ProcessReorg: error getting latest block number in RPC: %w", err)
	}

	finalizedBlockNumberInRPC, err := rm.port.GetBlockNumberInRPC(ctx, aggkittypes.FinalizedBlock)
	if err != nil {
		return fmt.Errorf("ProcessReorg: error getting finalized block number in RPC: %w", err)
	}
	rm.log.Infof("ProcessReorg: reorg detected from block %d to block %d",
		firstUnaffectedBlock+1, lastBlockNumberInStorage)
	// TODO: Add hash to blockNumbers
	reorgData := mdtypes.ReorgData{
		BlockRangeAffected:        aggkitcommon.NewBlockRange(firstUnaffectedBlock+1, lastBlockNumberInStorage),
		DetectedAtBlock:           detectedReorgError.OffendingBlockNumber,
		DetectedTimestamp:         rm.port.TimeNowUnix(),
		NetworkLatestBlock:        latestBlockNumberInRPC,
		NetworkFinalizedBlock:     finalizedBlockNumberInRPC,
		NetworkFinalizedBlockName: aggkittypes.FinalizedBlock,
		Description:               detectedReorgError.Error(),
	}
	reorgID, err := rm.port.MoveReorgedBlocks(tx, reorgData)
	if err != nil {
		return fmt.Errorf("ProcessReorg: error moving reorged blocks: %w", err)
	}
	reorgData.ReorgID = reorgID
	committed = true
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ProcessReorg: cannot commit tx: %w", err)
	}
	rm.log.Warnf("ProcessReorg: finalized reorgProcess: %s", reorgData.String())
	return nil
}

func (rm *ReorgProcessor) findFirstUnaffectedBlock(ctx context.Context,
	tx dbtypes.Querier,
	startBlockNumber uint64) (uint64, error) {
	currentBlockNumber := startBlockNumber
	for {
		if currentBlockNumber == 0 {
			// Genesis block reached, stop here
			return 0, fmt.Errorf("findFirstUnaffectedBlock: genesis block reached while checking reorgs, "+
				"cannot find unaffected block. First block checked: %d", startBlockNumber)
		}
		data, err := rm.port.GetBlockStorageAndRPC(ctx, tx, currentBlockNumber)
		if err != nil {
			return 0, fmt.Errorf("findFirstUnaffectedBlock: error getting block storage and RPC: %w", err)
		}
		match, err := rm.checkBlocks(data)
		if err != nil {
			return 0, fmt.Errorf("findFirstUnaffectedBlock: error checking blocks: %w", err)
		}
		if match {
			// Found the first unaffected block
			return currentBlockNumber, nil
		}
		currentBlockNumber--
	}
}

// checkBlocks compares storage and rpc block headers and returns true if they match
func (rm *ReorgProcessor) checkBlocks(blocks *mdtypes.CompareBlockHeaders) (bool, error) {
	if blocks == nil {
		return false, fmt.Errorf("checkBlocks: blocks is nil")
	}
	if blocks.StorageHeader == nil || blocks.RpcHeader == nil {
		// Block not in storage or not in RPC so is a missmatch
		rm.log.Warnf("checkBlocks: block %d missing storage=%t and rpc=%t",
			blocks.BlockNumber, blocks.ExistsStorageBlock(), blocks.ExistsRPCBlock())
		return false, nil
	}
	if blocks.StorageHeader.Number != blocks.RpcHeader.Number {
		return false, fmt.Errorf("checkBlocks block numbers do not match: storage=%d rpc=%d",
			blocks.StorageHeader.Number, blocks.RpcHeader.Number)
	}
	// This is a sanity check, never have to happen because we trust in finalized  blocks!
	if blocks.StorageHeader.Hash != blocks.RpcHeader.Hash {
		if blocks.IsFinalized == mdtypes.Finalized {
			rm.log.Warnf("checkBlocks: block %d is finalized and mismatch hash %s!=%s", blocks.StorageHeader.Number,
				blocks.StorageHeader.Hash.Hex(), blocks.RpcHeader.Hash.Hex())
		}
		return false, nil
	}
	return true, nil
}
