package bridgesync_test

import (
	"context"
	"fmt"
	"math/big"
	"path"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgesync"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/test/helpers"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	rpc "github.com/ethereum/go-ethereum/rpc"
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

	l1Setup, _ := helpers.NewSimulatedEVMEnvironment(t, &helpers.EnvironmentConfig{L1RPCClient: rpcClient})
	ctx := t.Context()
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
		}

		lastDepositCount++
		tx, err := l1Setup.BridgeContract.BridgeAsset(
			l1Setup.Auth,
			bridge.DestinationNetwork,
			bridge.DestinationAddress,
			bridge.Amount,
			bridge.OriginAddress,
			true, nil,
		)
		require.NoError(t, err)
		helpers.CommitBlocks(t, l1Setup.SimBackend, 1, blockTime)

		simulatedClient := l1Setup.SimBackend.Client()
		bn, err := simulatedClient.BlockNumber(ctx)
		require.NoError(t, err)
		bridge.BlockNum = bn
		receipt, err := l1Setup.SimBackend.Client().TransactionReceipt(ctx, tx.Hash())
		require.NoError(t, err)
		bridge.TxHash = receipt.TxHash
		block, err := simulatedClient.BlockByNumber(ctx, new(big.Int).SetUint64(bn))
		require.NoError(t, err)
		bridge.BlockTimestamp = block.Time()
		require.NoError(t, err)
		require.Equal(t, receipt.Status, types.ReceiptStatusSuccessful)
		expectedBridges = append(expectedBridges, bridge)
		expectedRoot, err := l1Setup.BridgeContract.GetRoot(nil)
		require.NoError(t, err)
		finalizedBlock := getFinalizedBlockNumber(t, ctx, l1Setup.SimBackend.Client())
		log.Infof("*** iteration: %d, Bridge Root: %s latestBlock:%d finalizedBlock:%d", i, common.Hash(expectedRoot).Hex(), bn, finalizedBlock)
		bridgesSent++
		// Finish condition
		if bridgesSent >= totalBridges {
			break
		}
	}

	helpers.CommitBlocks(t, l1Setup.SimBackend, 11, blockTime)

	// Wait for syncer to catch up
	time.Sleep(time.Second * 2) // sleeping since the processor could be up to date, but have pending reorgs

	lb := getFinalizedBlockNumber(t, ctx, l1Setup.SimBackend.Client())
	helpers.RequireProcessorUpdated(t, l1Setup.BridgeSync, lb, l1Setup.SimBackend.Client())

	// Get bridges
	lastBlock, err := l1Setup.SimBackend.Client().BlockNumber(ctx)
	require.NoError(t, err)
	lastProcessedBlock, err := l1Setup.BridgeSync.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	actualBridges, err := l1Setup.BridgeSync.GetBridges(ctx, 0, lastProcessedBlock)
	require.NoError(t, err)
	log.Infof("lastBlockOnChain:%d lastProcessedBlock: %d, len(actualBridges): %d", lb, lastProcessedBlock, len(actualBridges))
	// Assert bridges
	expectedRoot, err := l1Setup.BridgeContract.GetRoot(nil)
	require.NoError(t, err)
	root, err := l1Setup.BridgeSync.GetExitRootByIndex(ctx, expectedBridges[len(expectedBridges)-1].DepositCount)
	require.NoError(t, err)
	log.Infof("expectedRoot: %s lastBlock: %d lastFinalized:%d DepositCount:%d ", common.Hash(expectedRoot).Hex(), lastBlock, lb, expectedBridges[len(expectedBridges)-1].DepositCount)
	for i := 79; i >= 0; i-- {
		root, err := l1Setup.BridgeSync.GetExitRootByIndex(ctx, uint32(i))
		require.NoError(t, err, fmt.Sprintf("DepositCount:%d", i))
		log.Infof("DepositCount:%d root: %s", i, root.Hash.Hex())
	}
	require.Equal(t, common.Hash(expectedRoot).Hex(), root.Hash.Hex())
	require.Equal(t, expectedBridges, actualBridges)
}

func getFinalizedBlockNumber(t *testing.T, ctx context.Context, client simulated.Client) uint64 {
	t.Helper()
	lastBlockHeader, err := client.HeaderByNumber(ctx, big.NewInt(int64(rpc.FinalizedBlockNumber)))
	require.NoError(t, err)
	return lastBlockHeader.Number.Uint64()
}

// TestBridgeL1SyncerWithReorgDetector tests the bridge L1 syncer with reorg detector
func TestBridgeL1SyncerWithReorgDetector(t *testing.T) {
	ctx := context.Background()
	dbPathSyncer := path.Join(t.TempDir(), "bridgesyncTestWithReorgs_sync.sqlite")
	dbPathReorg := path.Join(t.TempDir(), "bridgesyncTestWithReorgs_reorg.sqlite")
	blocktime := time.Millisecond * 100

	// Setup simulated L1 environment with bridge and GER contracts
	//nolint:dogsled
	client, auth, _, _, bridgeAddr, bridgeContract, _ := helpers.NewSimulatedL1(t)

	rd, err := reorgdetector.New(client.Client(), reorgdetector.Config{
		DBPath:              dbPathReorg,
		CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 10),
		FinalizedBlock:      aggkittypes.FinalizedBlock,
	}, reorgdetector.L1)
	require.NoError(t, err)
	require.NoError(t, rd.Start(ctx))

	// Create bridge syncer with reorg detector
	const originNetwork = uint32(1)
	bridgeSyncCfg := bridgesync.Config{
		DBPath:                             dbPathSyncer,
		BridgeAddr:                         bridgeAddr,
		BlockFinality:                      aggkittypes.LatestBlock,
		SyncBlockChunkSize:                 10,
		InitialBlockNum:                    0,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Millisecond * 30),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(time.Millisecond * 10),
		MaxRetryAttemptsAfterError:         10,
		RequireStorageContentCompatibility: true,
		DBQueryTimeout:                     cfgtypes.NewDuration(5 * time.Second),
	}

	ethClient := aggkittypes.NewDefaultEthClient(client.Client(), &aggkittypes.NoopRPCClient{})

	// Create the bridge syncer with reorg detector
	syncer, err := bridgesync.NewL1(ctx, bridgeSyncCfg, rd, ethClient, originNetwork)
	require.NoError(t, err)
	require.NotNil(t, syncer)
	require.Equal(t, originNetwork, syncer.OriginNetwork())

	// Start the syncer
	go syncer.Start(ctx)

	// Step 1: Commit some blocks
	t.Log("Step 1: Committing initial blocks")
	helpers.CommitBlocks(t, client, 5, blocktime)

	// Step 2: Bridge asset and commit block
	t.Log("Step 2: Bridge asset #1 and commit block")
	amount1 := big.NewInt(1000000000000000000) // 1 ETH
	destinationNetwork := uint32(2)
	destinationAddress1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	auth.Value = amount1
	tx1, err := bridgeContract.BridgeAsset(
		auth,
		destinationNetwork,
		destinationAddress1,
		amount1,
		common.Address{}, // native token
		true,             // isForced
		[]byte{},         // permitData
	)
	require.NoError(t, err)
	auth.Value = nil
	helpers.CommitBlocks(t, client, 1, blocktime)
	t.Logf("  Created bridge tx: %s", tx1.Hash().Hex())
	blockNum1, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	t.Logf("  Block number after first bridge: %d", blockNum1)

	// Wait for syncer to process
	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

	// Step 4: Record the block hash to fork from later (fork from the current block to ensure reorg detection)
	t.Log("Step 4: Recording block hash for fork point")
	forkBlockNum, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	// Fork from the current block (which should be tracked) to ensure reorg detection
	forkBlockHeader, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(forkBlockNum)))
	require.NoError(t, err)
	forkBlockHash := forkBlockHeader.Hash()
	t.Logf("  Fork point: block %d, hash %s", forkBlockNum, forkBlockHash.Hex())

	// Commit additional blocks
	helpers.CommitBlocks(t, client, 2, blocktime)

	// Step 5: Bridge asset with different params and commit blocks, check count
	t.Log("Step 5: Bridge asset #2 with different params and commit blocks")
	amount2 := big.NewInt(2000000000000000000) // 2 ETH
	destinationAddress2 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	auth.Value = amount2
	tx2, err := bridgeContract.BridgeAsset(
		auth,
		destinationNetwork,
		destinationAddress2,
		amount2,
		common.Address{}, // native token
		true,             // isForced
		[]byte{},
	)
	require.NoError(t, err)
	auth.Value = nil
	helpers.CommitBlocks(t, client, 1, blocktime)
	blockNum2, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	t.Logf("  Block number after second bridge: %d", blockNum2)
	t.Logf("  Created bridge tx: %s", tx2.Hash().Hex())

	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

	// Check bridge count in L1 DB
	lastProcessed, err := syncer.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	bridgesBeforeFork, err := syncer.GetBridges(ctx, 0, lastProcessed)
	require.NoError(t, err)
	t.Logf("  Bridges in DB before fork: %d", len(bridgesBeforeFork))
	require.Equal(t, 2, len(bridgesBeforeFork), "Should have 2 bridges before fork")

	// Step 7: Fork from the recorded block
	t.Log("Step 7: Creating fork from block", forkBlockNum)

	err = client.Fork(forkBlockHash)
	require.NoError(t, err)
	t.Log("  Fork created successfully")
	helpers.CommitBlocks(t, client, 1, blocktime)
	currBlockNum, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	t.Logf("  After fork Current block number: %d", currBlockNum)
	forkedBlockHash, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(currBlockNum)))
	require.NoError(t, err)
	t.Logf("Hash of the forked block: %s", forkedBlockHash.Hash().Hex())

	// Create a different transaction after fork to ensure block hash changes
	t.Log("Step 7.1: Creating different transaction after fork to change block hash")
	auth.Value = big.NewInt(500000000000000000) // 0.5 ETH - different amount
	txAfterFork, err := bridgeContract.BridgeAsset(
		auth,
		destinationNetwork,
		common.HexToAddress("0x3333333333333333333333333333333333333333"), // different address
		big.NewInt(500000000000000000),
		common.Address{}, // native token
		true,             // isForced
		[]byte{},
	)
	require.NoError(t, err)
	auth.Value = nil
	helpers.CommitBlocks(t, client, 1, blocktime)
	t.Logf("  Created different bridge tx after fork: %s", txAfterFork.Hash().Hex())

	// Verify that block hash changes after fork to detect reorg differences
	currBlockNum, err = client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	forkedBlockHash, err = client.Client().HeaderByNumber(ctx, big.NewInt(int64(currBlockNum)))
	require.NoError(t, err)
	t.Logf("Hash of the forked block: %s", forkedBlockHash.Hash().Hex())
	t.Logf("After fork Current block number: %d", currBlockNum)
	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

	// Step 9: Check bridge count after fork
	t.Log("Step 9: Checking bridge count after fork")
	lastProcessedAfterFork, err := syncer.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	bridgesAfterFork, err := syncer.GetBridges(ctx, 0, lastProcessedAfterFork)
	require.NoError(t, err)
	t.Logf("  Bridges in DB immediately after fork: %d", len(bridgesAfterFork))

	require.Equal(t, 3, len(bridgesAfterFork), "Should have 3 bridges after reorg: Bridge #1, Bridge after fork, Bridge #2")

	t.Log("✅ Test completed successfully - syncer handled reorg correctly")
}

// TestReorgWithSameHashEdgeCase tests reorg detection when blocks have same hash
func TestReorgWithSameHashEdgeCase(t *testing.T) {
	ctx := context.Background()
	dbPathSyncer := path.Join(t.TempDir(), "bridgesyncTestSameHashReorg_sync.sqlite")
	dbPathReorg := path.Join(t.TempDir(), "bridgesyncTestSameHashReorg_reorg.sqlite")
	blocktime := time.Millisecond * 100

	// Setup simulated L1 environment
	//nolint:dogsled
	client, auth, _, _, bridgeAddr, bridgeContract, _ := helpers.NewSimulatedL1(t)

	rd, err := reorgdetector.New(client.Client(), reorgdetector.Config{
		DBPath:              dbPathReorg,
		CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 10),
		FinalizedBlock:      aggkittypes.FinalizedBlock,
	}, reorgdetector.L1)
	require.NoError(t, err)
	require.NoError(t, rd.Start(ctx))

	// Create bridge syncer with reorg detector
	const originNetwork = uint32(1)
	bridgeSyncCfg := bridgesync.Config{
		DBPath:                             dbPathSyncer,
		BridgeAddr:                         bridgeAddr,
		BlockFinality:                      aggkittypes.LatestBlock,
		SyncBlockChunkSize:                 10,
		InitialBlockNum:                    0,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Millisecond * 30),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(time.Millisecond * 10),
		MaxRetryAttemptsAfterError:         10,
		RequireStorageContentCompatibility: true,
		DBQueryTimeout:                     cfgtypes.NewDuration(5 * time.Second),
	}

	ethClient := aggkittypes.NewDefaultEthClient(client.Client(), &aggkittypes.NoopRPCClient{})
	syncer, err := bridgesync.NewL1(ctx, bridgeSyncCfg, rd, ethClient, originNetwork)
	require.NoError(t, err)
	require.NotNil(t, syncer)

	// Start the syncer
	go syncer.Start(ctx)

	// Helper function to get reorg count
	getReorgCount := func() int {
		var count int
		err := rd.GetDB().QueryRow("SELECT COUNT(*) FROM reorg_event").Scan(&count)
		require.NoError(t, err)
		return count
	}

	// Create initial blocks and bridge
	t.Log("Initial setup")
	helpers.CommitBlocks(t, client, 5, blocktime)

	// Create first bridge with specific parameters
	amount := big.NewInt(1000000000000000000)
	destAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	auth.Value = amount
	tx, err := bridgeContract.BridgeAsset(
		auth,
		uint32(2), // destination network
		destAddr,
		amount,
		common.Address{}, // native token
		true,             // isForced
		[]byte{},
	)
	require.NoError(t, err)
	auth.Value = nil
	helpers.CommitBlocks(t, client, 1, blocktime)
	t.Logf("Created tx: %s", tx.Hash().Hex())

	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

	// commit 3 blocks
	helpers.CommitBlocks(t, client, 3, blocktime)
	time.Sleep(time.Millisecond * 30)

	// Create fork point
	t.Log("Creating fork point")
	forkBlockNum, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	forkBlockHeader, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(forkBlockNum)))
	require.NoError(t, err)
	forkBlockHash := forkBlockHeader.Hash()
	t.Logf("Fork block %d hash: %s", forkBlockNum, forkBlockHash.Hex())

	// new commit 5 extra blocks
	helpers.CommitBlocks(t, client, 5, blocktime)
	time.Sleep(time.Millisecond * 50)

	// fork it from the fork block hash
	t.Log("Creating fork")
	err = client.Fork(forkBlockHash)
	require.NoError(t, err)

	// Check if we have a reorg event
	time.Sleep(time.Millisecond * 100) // Allow time for reorg detection
	reorgCount := getReorgCount()
	require.Equal(t, 0, reorgCount)
	t.Logf("  Reorg count after identical transaction: %d", reorgCount)

	t.Log("✅ Same hash reorg edge case test completed successfully")
}

// TestBridgeL1SyncerWithMultipleReorgs tests the bridge L1 syncer with multiple reorgs
func TestBridgeL1SyncerWithMultipleReorgs(t *testing.T) {
	ctx := context.Background()
	dbPathSyncer := path.Join(t.TempDir(), "bridgesyncTestWithReorgs_sync.sqlite")
	dbPathReorg := path.Join(t.TempDir(), "bridgesyncTestWithReorgs_reorg.sqlite")
	blocktime := time.Millisecond * 250

	// Setup simulated L1 environment with bridge and GER contracts
	//nolint:dogsled
	client, auth, _, _, bridgeAddr, bridgeContract, _ := helpers.NewSimulatedL1(t)

	rd, err := reorgdetector.New(client.Client(), reorgdetector.Config{
		DBPath:              dbPathReorg,
		CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 10),
		FinalizedBlock:      aggkittypes.FinalizedBlock,
	}, reorgdetector.L1)
	require.NoError(t, err)
	require.NoError(t, rd.Start(ctx))

	// Create bridge syncer with reorg detector
	const originNetwork = uint32(1)
	bridgeSyncCfg := bridgesync.Config{
		DBPath:                             dbPathSyncer,
		BridgeAddr:                         bridgeAddr,
		BlockFinality:                      aggkittypes.LatestBlock,
		SyncBlockChunkSize:                 10,
		InitialBlockNum:                    0,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Millisecond * 30),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(time.Millisecond * 10),
		MaxRetryAttemptsAfterError:         10,
		RequireStorageContentCompatibility: true,
		DBQueryTimeout:                     cfgtypes.NewDuration(5 * time.Second),
	}

	ethClient := aggkittypes.NewDefaultEthClient(client.Client(), &aggkittypes.NoopRPCClient{})

	// Create the bridge syncer with reorg detector
	syncer, err := bridgesync.NewL1(ctx, bridgeSyncCfg, rd, ethClient, originNetwork)
	require.NoError(t, err)
	require.NotNil(t, syncer)
	require.Equal(t, originNetwork, syncer.OriginNetwork())

	// Start the syncer
	go syncer.Start(ctx)

	// Helper function to get reorg count from database
	getReorgCount := func() int {
		var count int
		err := rd.GetDB().QueryRow("SELECT COUNT(*) FROM reorg_event").Scan(&count)
		require.NoError(t, err)
		return count
	}

	// Step 1: Commit some blocks
	t.Log("Step 1: Committing initial blocks")
	helpers.CommitBlocks(t, client, 3, blocktime)

	// Step 2: Bridge asset and commit block
	t.Log("Step 2: Bridge asset #1 and commit block")
	amount1 := big.NewInt(1000000000000000000) // 1 ETH
	destinationNetwork := uint32(2)
	destinationAddress1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	auth.Value = amount1
	_, err = bridgeContract.BridgeAsset(
		auth,
		destinationNetwork,
		destinationAddress1,
		amount1,
		common.Address{}, // native token
		true,             // isForced
		[]byte{},         // permitData
	)
	require.NoError(t, err)
	auth.Value = nil
	helpers.CommitBlocks(t, client, 2, blocktime)

	// Step 4: Record the block hash to fork from later (fork from the current block to ensure reorg detection)
	t.Log("Step 4: Recording block hash for fork point")
	forkBlockNum, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	// Fork from the current block (which should be tracked) to ensure reorg detection
	forkBlockHeader, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(forkBlockNum)))
	require.NoError(t, err)
	forkBlockHash := forkBlockHeader.Hash()
	t.Logf("  Fork point: block %d, hash %s", forkBlockNum, forkBlockHash.Hex())

	helpers.CommitBlocks(t, client, 2, blocktime)

	// Step 5: Bridge asset with different params and commit blocks, check count
	t.Log("Step 5: Bridge asset #2 with different params and commit blocks")
	amount2 := big.NewInt(2000000000000000000) // 2 ETH
	destinationAddress2 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	auth.Value = amount2
	_, err = bridgeContract.BridgeAsset(
		auth,
		destinationNetwork,
		destinationAddress2,
		amount2,
		common.Address{}, // native token
		true,             // isForced
		[]byte{},
	)
	require.NoError(t, err)
	auth.Value = nil
	helpers.CommitBlocks(t, client, 2, blocktime)

	// Check bridge count in L1 DB
	lastProcessed, err := syncer.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	bridgesBeforeFork, err := syncer.GetBridges(ctx, 0, lastProcessed)
	require.NoError(t, err)
	t.Logf("  Bridges in DB before fork: %d", len(bridgesBeforeFork))
	require.Equal(t, 2, len(bridgesBeforeFork), "Should have 2 bridges before fork")

	// Step 7: Fork from the recorded block
	t.Log("Step 7: Creating fork from block", forkBlockNum)

	err = client.Fork(forkBlockHash)
	require.NoError(t, err)
	t.Log("  Fork created successfully")
	helpers.CommitBlocks(t, client, 2, blocktime)

	// Create a different transaction after fork to ensure block hash changes
	t.Log("Step 7.1: bridge asset #3 Creating different transaction after fork to change block hash")
	auth.Value = big.NewInt(500000000000000000) // 0.5 ETH - different amount
	txAfterFork, err := bridgeContract.BridgeAsset(
		auth,
		destinationNetwork,
		common.HexToAddress("0x3333333333333333333333333333333333333333"), // different address
		big.NewInt(500000000000000000),
		common.Address{}, // native token
		true,             // isForced
		[]byte{},
	)
	require.NoError(t, err)
	auth.Value = nil
	helpers.CommitBlocks(t, client, 2, blocktime)
	t.Logf("  Created third bridge tx after fork: %s", txAfterFork.Hash().Hex())

	time.Sleep(time.Millisecond * 500)

	// Check reorg event count
	reorgCount := getReorgCount()
	require.Equal(t, 1, reorgCount)

	// Step 9: Check bridge count after fork
	t.Log("Step 9: Checking bridge count after fork")
	lastProcessedAfterFork, err := syncer.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	bridgesAfterFork, err := syncer.GetBridges(ctx, 0, lastProcessedAfterFork)
	require.NoError(t, err)
	t.Logf("  Bridges in DB immediately after fork: %d", len(bridgesAfterFork))

	require.Equal(t, 3, len(bridgesAfterFork), "Should have 3 bridges after reorg: Bridge #1, Bridge #2, Bridge #3")

	t.Log("✅ Test completed successfully - syncer handled reorg correctly")

	// Step 10: Commit additional blocks
	t.Log("Step 10: Committing additional blocks")
	helpers.CommitBlocks(t, client, 2, blocktime)

	// record block hash to fork from
	forkBlockNum2, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	forkBlockHeader2, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(forkBlockNum2)))
	require.NoError(t, err)
	forkBlockHash2 := forkBlockHeader2.Hash()
	t.Logf("Fork block %d hash: %s", forkBlockNum2, forkBlockHash2.Hex())

	// bridge again
	t.Log("Step 11: bridge asset #4")
	auth.Value = big.NewInt(3000000000000000000) // 3 ETH
	tx3, err := bridgeContract.BridgeAsset(
		auth,
		destinationNetwork,
		destinationAddress2,
		big.NewInt(3000000000000000000),
		common.Address{}, // native token
		true,             // isForced
		[]byte{},
	)
	require.NoError(t, err)
	auth.Value = nil
	helpers.CommitBlocks(t, client, 2, blocktime)
	t.Logf("  Created fourth bridge tx: %s", tx3.Hash().Hex())

	// fork from the fork block hash
	err = client.Fork(forkBlockHash2)
	require.NoError(t, err)
	t.Log("  Fork created successfully after fourth bridge")
	helpers.CommitBlocks(t, client, 2, blocktime)

	// Check if we have a reorg event
	reorgCount = getReorgCount()
	require.Equal(t, 2, reorgCount)

	// Check bridge count in L1 DB
	lastProcessed, err = syncer.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	bridgesAfterFourthBridge, err := syncer.GetBridges(ctx, 0, lastProcessed)
	require.NoError(t, err)
	t.Logf("  Bridges in DB after fourth bridge: %d", len(bridgesAfterFourthBridge))
	require.Equal(t, 4, len(bridgesAfterFourthBridge), "Should have 4 bridges")

	// bridge again
	t.Log("Step 11: bridge asset #5")
	auth.Value = big.NewInt(4000000000000000000) // 4 ETH
	_, err = bridgeContract.BridgeAsset(
		auth,
		destinationNetwork,
		destinationAddress2,
		big.NewInt(4000000000000000000),
		common.Address{}, // native token
		true,             // isForced
		[]byte{},
	)
	require.NoError(t, err)
	auth.Value = nil
	helpers.CommitBlocks(t, client, 2, blocktime)

	// Check bridge count in L1 DB
	lastProcessed, err = syncer.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	bridgesAfterFifthBridge, err := syncer.GetBridges(ctx, 0, lastProcessed)
	require.NoError(t, err)
	t.Logf("  Bridges in DB after fifth bridge: %d", len(bridgesAfterFifthBridge))
	require.Equal(t, 5, len(bridgesAfterFifthBridge), "Should have 5 bridges")

	// Check if we have a reorg event
	reorgCount = getReorgCount()
	require.Equal(t, 2, reorgCount)

	t.Log("✅ Test completed successfully - syncer handled multiple reorgs correctly")
}
