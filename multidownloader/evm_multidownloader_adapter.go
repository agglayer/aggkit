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
	"time"

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

func (dh *EVMMultidownloader) BlockNumber(ctx context.Context,
	finality aggkittypes.BlockNumberFinality) (uint64, error) {
	bn, err := dh.blockNotifierManager.GetBlockNotifier(ctx, finality)
	if err != nil {
		return 0, fmt.Errorf("EVMMultidownloader.BlockNumber: cannot get BlockNotifier: %w", err)
	}
	return bn.GetCurrentBlockNumber(), nil
}

func (dh *EVMMultidownloader) BlockHeader(ctx context.Context,
	finality aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, error) {
	number, err := dh.BlockNumber(ctx, finality)
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.BlockHeader: cannot get block number for finality=%s: %w",
			finality.String(), err)
	}
	return dh.HeaderByNumber(ctx, big.NewInt(int64(number)))
}

func (dh *EVMMultidownloader) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	dh.log.Debugf("EVMMultidownloader.FilterLogs: received query: %+v", query)
	defer dh.log.Debugf("EVMMultidownloader.FilterLogs: finished query: %+v", query)
	logQuery := mdrtypes.NewLogQueryFromEthereumFilter(query)
	for !dh.IsAvailable(logQuery) {
		dh.log.Infof("EVMMultidownloader.FilterLogs: waiting %s for logs to be available: %s",
			dh.cfg.WaitPeriodToCheckCatchUp.String(), logQuery.String())
		time.Sleep(dh.cfg.WaitPeriodToCheckCatchUp.Duration)
	}
	logs, err := dh.storage.GetEthLogs(nil, logQuery)
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.FilterLogs: cannot get logs: %w", err)
	}
	dh.log.Debugf("EVMMultidownloader.FilterLogs(%d - %d): len(logs)= %d", query.FromBlock, query.ToBlock, len(logs))

	return logs, nil
}

func (dh *EVMMultidownloader) HeaderByNumber(ctx context.Context, number *big.Int) (*aggkittypes.BlockHeader, error) {
	dh.log.Debugf("EVMMultidownloader.HeaderByNumber: received number: %s", number.String())
	defer dh.log.Debugf("EVMMultidownloader.HeaderByNumber: finished number: %s", number.String())
	if number.Cmp(big.NewInt(0)) < 0 {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: negative block number not supported=%s", number.String())
	}

	block, err := dh.storage.GetBlockHeaderByNumber(nil, number.Uint64())
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: cannot get BlockHeader number=%d: %w",
			number.Uint64(), err)
	}
	if block != nil {
		return block, nil
	}
	// This is a fallback mechanism in case the block is not found in storage (must be on storage!)
	dh.log.Infof("EVMMultidownloader.HeaderByNumber: block number=%d not found in storage, fetching from ethClient",
		number.Uint64())
	ethBlock, err := dh.ethClient.HeaderByNumber(ctx, number) // Just to comply with the interface
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: fails ethClient.HeaderByNumber(%d). Err:  %w",
			number.Uint64(), err)
	}
	blockHeader := aggkittypes.NewBlockHeaderFromEthBlockHeader(ethBlock)
	return blockHeader, nil
}

func (dh *EVMMultidownloader) EthClient() aggkittypes.BaseEthereumClienter {
	return dh.ethClient
}
