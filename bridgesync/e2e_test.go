package bridgesync_test

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/helpers"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBridgeEventE2E(t *testing.T) {
	const (
		blockTime    = time.Millisecond * 10
		totalBridges = 80
	)

	rpcClient := mocks.NewRPCClienter(t)
	rpcClient.EXPECT().Call(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	setup := helpers.NewE2EEnvWithEVML2(t, &helpers.EnvironmentConfig{L1RPCClient: rpcClient})
	ctx := context.Background()
	// Send bridge txs
	bridgesSent := 0
	expectedBridges := []bridgesync.Bridge{}
	lastDepositCount := uint32(0)

	for i := 1; i > 0; i++ {
		// Send bridge
		bridge := bridgesync.Bridge{
			Amount:             big.NewInt(0),
			DepositCount:       lastDepositCount,
			DestinationNetwork: uint32(i + 1),
			DestinationAddress: common.HexToAddress("f00"),
			Metadata:           []byte{},
			IsNativeToken:      true,
		}

		lastDepositCount++
		tx, err := setup.L1Environment.BridgeContract.BridgeAsset(
			setup.L1Environment.Auth,
			bridge.DestinationNetwork,
			bridge.DestinationAddress,
			bridge.Amount,
			bridge.OriginAddress,
			true, nil,
		)
		require.NoError(t, err)
		helpers.CommitBlocks(t, setup.L1Environment.SimBackend, 1, blockTime)

		simulatedClient := setup.L1Environment.SimBackend.Client()
		bn, err := simulatedClient.BlockNumber(ctx)
		require.NoError(t, err)
		bridge.BlockNum = bn
		receipt, err := setup.L1Environment.SimBackend.Client().TransactionReceipt(ctx, tx.Hash())
		require.NoError(t, err)
		bridge.TxHash = receipt.TxHash
		block, err := simulatedClient.BlockByNumber(ctx, new(big.Int).SetUint64(bn))
		require.NoError(t, err)
		bridge.BlockTimestamp = block.Time()
		require.NoError(t, err)
		require.Equal(t, receipt.Status, types.ReceiptStatusSuccessful)
		expectedBridges = append(expectedBridges, bridge)
		expectedRoot, err := setup.L1Environment.BridgeContract.GetRoot(nil)
		require.NoError(t, err)
		finalizedBlock := getFinalizedBlockNumber(t, ctx, setup.L1Environment.SimBackend.Client())
		log.Infof("*** iteration: %d, Bridge Root: %s latestBlock:%d finalizedBlock:%d", i, common.Hash(expectedRoot).Hex(), bn, finalizedBlock)
		bridgesSent++
		// Finish condition
		if bridgesSent >= totalBridges {
			break
		}
	}

	helpers.CommitBlocks(t, setup.L1Environment.SimBackend, 11, blockTime)

	// Wait for syncer to catch up
	time.Sleep(time.Second * 2) // sleeping since the processor could be up to date, but have pending reorgs

	lb := getFinalizedBlockNumber(t, ctx, setup.L1Environment.SimBackend.Client())
	helpers.RequireProcessorUpdated(t, setup.L1Environment.BridgeSync, lb)

	// Get bridges
	lastBlock, err := setup.L1Environment.SimBackend.Client().BlockNumber(ctx)
	require.NoError(t, err)
	lastProcessedBlock, err := setup.L1Environment.BridgeSync.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	actualBridges, err := setup.L1Environment.BridgeSync.GetBridges(ctx, 0, lastProcessedBlock)
	require.NoError(t, err)
	log.Infof("lastBlockOnChain:%d lastProcessedBlock: %d, len(actualBridges): %d", lb, lastProcessedBlock, len(actualBridges))
	// Assert bridges
	expectedRoot, err := setup.L1Environment.BridgeContract.GetRoot(nil)
	require.NoError(t, err)
	root, err := setup.L1Environment.BridgeSync.GetExitRootByIndex(ctx, expectedBridges[len(expectedBridges)-1].DepositCount)
	require.NoError(t, err)
	log.Infof("expectedRoot: %s lastBlock: %d lastFinalized:%d DepositCount:%d ", common.Hash(expectedRoot).Hex(), lastBlock, lb, expectedBridges[len(expectedBridges)-1].DepositCount)
	for i := 79; i >= 0; i-- {
		root, err := setup.L1Environment.BridgeSync.GetExitRootByIndex(ctx, uint32(i))
		require.NoError(t, err, fmt.Sprintf("DepositCount:%d", i))
		log.Infof("DepositCount:%d root: %s", i, root.Hash.Hex())
	}
	require.Equal(t, common.Hash(expectedRoot).Hex(), root.Hash.Hex())
	require.Equal(t, expectedBridges, actualBridges)
}

func getFinalizedBlockNumber(t *testing.T, ctx context.Context, client simulated.Client) uint64 {
	t.Helper()
	lastBlockFinalityType, err := aggkittypes.FinalizedBlock.ToBlockNum()
	require.NoError(t, err)
	lastBlockHeader, err := client.HeaderByNumber(ctx, lastBlockFinalityType)
	require.NoError(t, err)
	return lastBlockHeader.Number.Uint64()
}
