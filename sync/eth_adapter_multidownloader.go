package sync

// This adapt ethClient to the interface of MultiDownloader

import (
	"context"
	"fmt"
	"math/big"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

type AdaptEthClient struct {
	ethClient aggkittypes.BaseEthereumClienter
}

var _ (aggkittypes.MultiDownloader) = (*AdaptEthClient)(nil)

func NewAdaptEthClient(ethClient aggkittypes.BaseEthereumClienter) *AdaptEthClient {
	return &AdaptEthClient{
		ethClient: ethClient,
	}
}

func (a *AdaptEthClient) ChainID(ctx context.Context) (uint64, error) {
	chainID, err := a.ethClient.ChainID(ctx)
	if err != nil {
		return 0, fmt.Errorf("AdaptEthClient.ChainID: cannot get chainID: %w", err)
	}
	if chainID == nil {
		return 0, errChainIDUndefined
	}
	return chainID.Uint64(), nil
}

func (a *AdaptEthClient) BlockNumber(ctx context.Context, finality aggkittypes.BlockNumberFinality) (uint64, error) {
	return finality.BlockNumber(ctx, a.ethClient)
}

func (a *AdaptEthClient) BlockHeader(ctx context.Context, finality aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, error) {
	header, err := finality.BlockHeader(ctx, a.ethClient)
	if err != nil {
		return nil, fmt.Errorf("AdaptEthClient.BlockHeader: cannot get BlockHeader for finality=%s: %w", finality.String(), err)
	}
	return aggkittypes.NewBlockHeaderFromEthHeader(header), nil
}

func (a *AdaptEthClient) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	return a.ethClient.FilterLogs(ctx, query)
}

func (a *AdaptEthClient) HeaderByNumber(ctx context.Context, number *big.Int) (*aggkittypes.BlockHeader, error) {
	header, err := a.ethClient.HeaderByNumber(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("AdaptEthClient.HeaderByNumber: cannot get BlockHeader number=%s: %w", number.String(), err)
	}
	return aggkittypes.NewBlockHeaderFromEthHeader(header), nil
}

func (a *AdaptEthClient) EthClient() aggkittypes.BaseEthereumClienter {
	return a.ethClient
}

func (a *AdaptEthClient) RegisterSyncer(data aggkittypes.SyncerConfig) {
	// No-op for single eth client
}

func (a *AdaptEthClient) Start(ctx context.Context) error {
	// No-op for single eth client
	return nil
}
