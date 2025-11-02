package multidownloader

// This is an adapter to make EVMMultidownloader act as much as ethereum.Client
// in order of be able to help changing this
// What is not compatible?: HeaderByNumber
// ethereumtype.Header require all the fields to compute the Hash()
// so we cannot populate a valid ethereumtype.Header from our types.BlockHeader

import (
	"context"
	"fmt"
	"math/big"

	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

func (dh *EVMMultidownloader) ChainID(ctx context.Context) (uint64, error) {
	chainID, err := dh.ethClient.ChainID(ctx)
	if err != nil {
		return 0, fmt.Errorf("EVMMultidownloader.ChainID: cannot get chainID: %w", err)
	}
	return chainID.Uint64(), nil
}

func (dh *EVMMultidownloader) BlockNumber(ctx context.Context, finality aggkittypes.BlockNumberFinality) (uint64, error) {
	bn, err := dh.blockNotifierManager.GetBlockNotifier(ctx, finality)
	if err != nil {
		return 0, fmt.Errorf("EVMMultidownloader.BlockNumber: cannot get BlockNotifier: %w", err)
	}
	return bn.GetCurrentBlockNumber(), nil
}

func (dh *EVMMultidownloader) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	logQuery := mdrtypes.NewLogQueryFromEthereumFilter(query)
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	if !dh.syncedSegments.IsAvailable(logQuery) {
		return nil, fmt.Errorf("EVMMultidownloader.FilterLogs: requested logs are not yet synced: %s", logQuery.String())
	}
	return dh.storage.GetEthLogs(nil, logQuery)
}

func (dh *EVMMultidownloader) HeaderByNumber(ctx context.Context, number *big.Int) (*aggkittypes.BlockHeader, error) {
	if number.Cmp(big.NewInt(0)) < 0 {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: negative block number not supported=%s", number.String())
	}
	block, err := dh.storage.GetBlockHeaderByNumber(nil, number.Uint64())
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: cannot get BlockHeader number=%d: %w", number.Uint64(), err)
	}
	if block != nil {
		return block, nil
	}

	ethBlock, err := dh.ethClient.HeaderByNumber(ctx, number) // Just to comply with the interface
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: cannot get full BlockHeader number=%d from ethClient: %w", number.Uint64(), err)
	}
	finalizedBlock, err := dh.getFinalizedBlockNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: cannot get finalized block number: %w", err)
	}
	isFinal := number.Uint64() <= finalizedBlock
	blockHeader := aggkittypes.NewBlockHeaderFromEthBlockHeader(ethBlock)
	err = dh.storage.SaveBlockHeader(nil, blockHeader, isFinal)
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: cannot save BlockHeader number=%d: %w", number.Uint64(), err)
	}
	return blockHeader, nil
}

func (dh *EVMMultidownloader) EthClient() aggkittypes.BaseEthereumClienter {
	return dh.ethClient
}
