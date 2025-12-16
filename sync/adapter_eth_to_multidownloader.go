package sync

// This adapt ethClient to the interface of MultiDownloader

import (
	"context"
	"fmt"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// AdaptEthClientToMultidownloader adapts a BaseEthereumClienter
// to the MultiDownloader interface (aggkittypes.MultiDownloader)
// This is a scaffolding for migrate progressively the syncers to use MultiDownloader
// meanwhile with this adapted can keep using a regular eth client
type AdaptEthClientToMultidownloader struct {
	ethClient aggkittypes.BaseEthereumClienter
}

var _ (aggkittypes.MultiDownloader) = (*AdaptEthClientToMultidownloader)(nil)

func NewAdapterEthClientToMultidownloader(ethClient aggkittypes.BaseEthereumClienter) *AdaptEthClientToMultidownloader {
	return &AdaptEthClientToMultidownloader{
		ethClient: ethClient,
	}
}

func (a *AdaptEthClientToMultidownloader) ChainID(ctx context.Context) (uint64, error) {
	chainID, err := a.ethClient.ChainID(ctx)
	if err != nil {
		return 0, fmt.Errorf("AdaptEthClient.ChainID: cannot get chainID: %w", err)
	}
	if chainID == nil {
		return 0, errChainIDUndefined
	}
	return chainID.Uint64(), nil
}

func (a *AdaptEthClientToMultidownloader) BlockNumber(ctx context.Context,
	finality aggkittypes.BlockNumberFinality) (uint64, error) {
	return finality.BlockNumber(ctx, a.ethClient)
}

func (a *AdaptEthClientToMultidownloader) BlockHeader(ctx context.Context,
	finality aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, error) {
	header, err := a.ethClient.CustomHeaderByNumber(ctx, &finality)
	if err != nil {
		return nil, fmt.Errorf("AdaptEthClient.BlockHeader: cannot get BlockHeader for finality=%s: %w",
			finality.String(), err)
	}
	return header, nil
}

func (a *AdaptEthClientToMultidownloader) HeaderByNumber(ctx context.Context,
	number *aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, error) {
	header, err := a.ethClient.CustomHeaderByNumber(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("AdaptEthClient.HeaderByNumber: cannot get BlockHeader number=%s: %w", number.String(), err)
	}
	return header, nil
}

func (a *AdaptEthClientToMultidownloader) FilterLogs(ctx context.Context,
	query ethereum.FilterQuery) ([]types.Log, error) {
	return a.ethClient.FilterLogs(ctx, query)
}

func (a *AdaptEthClientToMultidownloader) EthClient() aggkittypes.BaseEthereumClienter {
	return a.ethClient
}

func (a *AdaptEthClientToMultidownloader) RegisterSyncer(data aggkittypes.SyncerConfig) error {
	// No-op for single eth client
	return nil
}

func (a *AdaptEthClientToMultidownloader) Start(ctx context.Context) error {
	// No-op for single eth client
	return nil
}
