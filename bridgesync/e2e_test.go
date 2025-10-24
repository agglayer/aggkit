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
	blocktime := time.Second * 6

	// Setup simulated L1 environment with bridge and GER contracts
	//nolint:dogsled
	client, auth, _, _, bridgeAddr, bridgeContract, _ := helpers.NewSimulatedL1(t)

	rd, err := reorgdetector.New(client.Client(), reorgdetector.Config{
		DBPath:              dbPathReorg,
		CheckReorgsInterval: cfgtypes.NewDuration(time.Second * 1),
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
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Second * 3),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(time.Second * 1),
		MaxRetryAttemptsAfterError:         10,
		RequireStorageContentCompatibility: true,
		DBQueryTimeout:                     cfgtypes.NewDuration(5 * time.Second),
	}

	ethClient := aggkittypes.NewDefaultEthClient(client.Client(), &aggkittypes.NoopRPCClient{})

	// Create the bridge syncer with reorg detector
	syncer, err := bridgesync.NewL1(ctx, bridgeSyncCfg, rd, ethClient, originNetwork, true)
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
	forkFromBlock := forkBlockNum
	forkBlockHeader, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(forkFromBlock)))
	require.NoError(t, err)
	forkBlockHash := forkBlockHeader.Hash()
	t.Logf("  Fork point: block %d, hash %s", forkFromBlock, forkBlockHash.Hex())

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
	t.Log("Step 7: Creating fork from block", forkFromBlock)

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

// // TestMultipleConsecutiveReorgs tests multiple consecutive reorgs and validates reorg count
// func TestMultipleConsecutiveReorgs(t *testing.T) {
// 	ctx := context.Background()
// 	dbPathSyncer := path.Join(t.TempDir(), "bridgesyncTestMultipleReorgs_sync.sqlite")
// 	dbPathReorg := path.Join(t.TempDir(), "bridgesyncTestMultipleReorgs_reorg.sqlite")
// 	blocktime := time.Second * 3

// 	// Setup simulated L1 environment
//  //nolint:dogsled
// 	client, auth, _, _, bridgeAddr, bridgeContract, _ := helpers.NewSimulatedL1(t)

// 	rd, err := reorgdetector.New(client.Client(), reorgdetector.Config{
// 		DBPath:              dbPathReorg,
// 		CheckReorgsInterval: cfgtypes.NewDuration(time.Second * 1),
// 		FinalizedBlock:      aggkittypes.FinalizedBlock,
// 	}, reorgdetector.L1)
// 	require.NoError(t, err)
// 	require.NoError(t, rd.Start(ctx))

// 	// Create bridge syncer with reorg detector
// 	const originNetwork = uint32(1)
// 	bridgeSyncCfg := bridgesync.Config{
// 		DBPath:                             dbPathSyncer,
// 		BridgeAddr:                         bridgeAddr,
// 		BlockFinality:                      aggkittypes.LatestBlock,
// 		SyncBlockChunkSize:                 10,
// 		InitialBlockNum:                    0,
// 		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Second * 2),
// 		RetryAfterErrorPeriod:              cfgtypes.NewDuration(time.Second * 1),
// 		MaxRetryAttemptsAfterError:         10,
// 		RequireStorageContentCompatibility: true,
// 		DBQueryTimeout:                     cfgtypes.NewDuration(5 * time.Second),
// 	}

// 	ethClient := aggkittypes.NewDefaultEthClient(client.Client(), &aggkittypes.NoopRPCClient{})
// 	syncer, err := bridgesync.NewL1(ctx, bridgeSyncCfg, rd, ethClient, originNetwork, true)
// 	require.NoError(t, err)
// 	require.NotNil(t, syncer)

// 	// Start the syncer
// 	go syncer.Start(ctx)

// 	// Helper function to get reorg count from database
// 	getReorgCount := func() int {
// 		var count int
// 		err := rd.GetDB().QueryRow("SELECT COUNT(*) FROM reorg_event").Scan(&count)
// 		require.NoError(t, err)
// 		return count
// 	}

// 	// Helper function to create bridge transaction
// 	createBridgeTx := func(amount *big.Int, destAddr common.Address, txName string) common.Hash {
// 		auth.Value = amount
// 		tx, err := bridgeContract.BridgeAsset(
// 			auth,
// 			uint32(2), // destination network
// 			destAddr,
// 			amount,
// 			common.Address{}, // native token
// 			true,             // isForced
// 			[]byte{},
// 		)
// 		require.NoError(t, err)
// 		auth.Value = nil
// 		helpers.CommitBlocks(t, client, 1, blocktime)
// 		t.Logf("  Created %s: %s", txName, tx.Hash().Hex())
// 		return tx.Hash()
// 	}

// 	// Helper function to create fork and validate reorg count
// 	createForkAndValidate := func(forkBlockNum uint64, expectedReorgCount int, description string) {
// 		t.Logf("Creating fork at block %d - %s", forkBlockNum, description)

// 		// Get block hash to fork from
// 		forkBlockHeader, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(forkBlockNum)))
// 		require.NoError(t, err)
// 		forkBlockHash := forkBlockHeader.Hash()

// 		// Create fork
// 		err = client.Fork(forkBlockHash)
// 		require.NoError(t, err)
// 		helpers.CommitBlocks(t, client, 2, blocktime)

// 		// Validate reorg count
// 		actualReorgCount := getReorgCount()
// 		t.Logf("  Expected reorg count: %d, Actual: %d", expectedReorgCount, actualReorgCount)
// 		require.Equal(t, expectedReorgCount, actualReorgCount, "Reorg count mismatch for %s", description)
// 	}

// 	// Step 1: Initial setup and first bridge
// 	t.Log("Step 1: Initial setup and first bridge")
// 	helpers.CommitBlocks(t, client, 3, blocktime)
// 	_ = createBridgeTx(big.NewInt(1000000000000000000), common.HexToAddress("0x1111111111111111111111111111111111111111"), "Bridge #1")
// 	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

// 	// Record fork point for first reorg
// 	forkBlock1, err := client.Client().BlockNumber(ctx)
// 	require.NoError(t, err)

// 	// Step 2: Second bridge
// 	t.Log("Step 2: Second bridge")
// 	_ = createBridgeTx(big.NewInt(2000000000000000000), common.HexToAddress("0x2222222222222222222222222222222222222222"), "Bridge #2")
// 	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

// 	// Step 3: Third bridge
// 	t.Log("Step 3: Third bridge")
// 	_ = createBridgeTx(big.NewInt(3000000000000000000), common.HexToAddress("0x3333333333333333333333333333333333333333"), "Bridge #3")
// 	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

// 	// len of bridges should be 3
// 	lastProcessed, err := syncer.GetLastProcessedBlock(ctx)
// 	require.NoError(t, err)
// 	bridges, err := syncer.GetBridges(ctx, 0, lastProcessed)
// 	require.NoError(t, err)
// 	require.Equal(t, 3, len(bridges))

// 	// Step 4: First reorg - fork from block after first bridge
// 	t.Log("Step 4: First reorg")
// 	createForkAndValidate(forkBlock1, 1, "First reorg - should have 1 reorg")

// 	// Create different transaction after first fork and let it settle
// 	_ = createBridgeTx(big.NewInt(1500000000000000000), common.HexToAddress("0x4444444444444444444444444444444444444444"), "Bridge #1 Fork")
// 	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

// 	// Let some blocks pass to establish the new chain
// 	helpers.CommitBlocks(t, client, 3, blocktime)
// 	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

// 	// len of bridges should be 4
// 	lastProcessed, err = syncer.GetLastProcessedBlock(ctx)
// 	require.NoError(t, err)
// 	bridges, err = syncer.GetBridges(ctx, 0, lastProcessed)
// 	require.NoError(t, err)
// 	require.Equal(t, 4, len(bridges))

// 	// // Get the new fork point for second reorg
// 	// forkBlock2New, err := client.Client().BlockNumber(ctx)
// 	// require.NoError(t, err)
// 	// forkBlock2New = forkBlock2New - 1 // Fork from previous block

// 	// // Step 5: Second reorg - fork from the new chain
// 	// t.Log("Step 5: Second reorg")
// 	// createForkAndValidate(forkBlock2New, 2, "Second reorg - should have 2 reorgs")

// 	// // Create different transaction after second fork and let it settle
// 	// _ = createBridgeTx(big.NewInt(2500000000000000000), common.HexToAddress("0x5555555555555555555555555555555555555555"), "Bridge #2 Fork")
// 	// helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

// 	// // Let some blocks pass to establish the new chain
// 	// helpers.CommitBlocks(t, client, 3, time.Second*2)
// 	// helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

// 	// // Get the new fork point for third reorg
// 	// forkBlock3New, err := client.Client().BlockNumber(ctx)
// 	// require.NoError(t, err)
// 	// forkBlock3New = forkBlock3New - 1 // Fork from previous block

// 	// // Step 6: Third reorg - fork from the new chain
// 	// t.Log("Step 6: Third reorg")
// 	// createForkAndValidate(forkBlock3New, 3, "Third reorg - should have 3 reorgs")

// 	// // Create different transaction after third fork and let it settle
// 	// _ = createBridgeTx(big.NewInt(3500000000000000000), common.HexToAddress("0x6666666666666666666666666666666666666666"), "Bridge #3 Fork")
// 	// helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

// 	// // Let some blocks pass to establish the new chain
// 	// helpers.CommitBlocks(t, client, 3, time.Second*2)
// 	// helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

// 	// // Get the new fork point for fourth reorg
// 	// forkBlock4New, err := client.Client().BlockNumber(ctx)
// 	// require.NoError(t, err)
// 	// forkBlock4New = forkBlock4New - 1 // Fork from previous block

// 	// // Step 7: Fourth reorg - fork from the new chain
// 	// t.Log("Step 7: Fourth reorg")
// 	// createForkAndValidate(forkBlock4New, 4, "Fourth reorg - should have 4 reorgs")

// 	// // Final validation
// 	// finalReorgCount := getReorgCount()
// 	// t.Logf("Final reorg count: %d", finalReorgCount)
// 	// require.Equal(t, 4, finalReorgCount, "Should have exactly 4 reorgs recorded")

// 	// // Verify bridges are correctly handled
// 	// lastProcessed, err := syncer.GetLastProcessedBlock(ctx)
// 	// require.NoError(t, err)
// 	// bridges, err := syncer.GetBridges(ctx, 0, lastProcessed)
// 	// require.NoError(t, err)
// 	// t.Logf("Final bridge count: %d", len(bridges))

// 	// t.Log("✅ Multiple consecutive reorgs test completed successfully")
// }

// // TestReorgWithSameHashEdgeCase tests reorg detection when blocks have same hash
// func TestReorgWithSameHashEdgeCase(t *testing.T) {
// 	ctx := context.Background()
// 	dbPathSyncer := path.Join(t.TempDir(), "bridgesyncTestSameHashReorg_sync.sqlite")
// 	dbPathReorg := path.Join(t.TempDir(), "bridgesyncTestSameHashReorg_reorg.sqlite")
// 	blocktime := time.Second * 2

// 	// Setup simulated L1 environment
//  //nolint:dogsled
// 	client, auth, _, _, bridgeAddr, bridgeContract, _ := helpers.NewSimulatedL1(t)

// 	rd, err := reorgdetector.New(client.Client(), reorgdetector.Config{
// 		DBPath:              dbPathReorg,
// 		CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 500),
// 		FinalizedBlock:      aggkittypes.FinalizedBlock,
// 	}, reorgdetector.L1)
// 	require.NoError(t, err)
// 	require.NoError(t, rd.Start(ctx))

// 	// Create bridge syncer with reorg detector
// 	const originNetwork = uint32(1)
// 	bridgeSyncCfg := bridgesync.Config{
// 		DBPath:                             dbPathSyncer,
// 		BridgeAddr:                         bridgeAddr,
// 		BlockFinality:                      aggkittypes.LatestBlock,
// 		SyncBlockChunkSize:                 10,
// 		InitialBlockNum:                    0,
// 		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Second * 1),
// 		RetryAfterErrorPeriod:              cfgtypes.NewDuration(time.Millisecond * 500),
// 		MaxRetryAttemptsAfterError:         10,
// 		RequireStorageContentCompatibility: true,
// 		DBQueryTimeout:                     cfgtypes.NewDuration(5 * time.Second),
// 	}

// 	ethClient := aggkittypes.NewDefaultEthClient(client.Client(), &aggkittypes.NoopRPCClient{})
// 	syncer, err := bridgesync.NewL1(ctx, bridgeSyncCfg, rd, ethClient, originNetwork, true)
// 	require.NoError(t, err)
// 	require.NotNil(t, syncer)

// 	// Start the syncer
// 	go syncer.Start(ctx)

// 	// Helper function to get reorg count
// 	getReorgCount := func() int {
// 		var count int
// 		err := rd.GetDB().QueryRow("SELECT COUNT(*) FROM reorg_event").Scan(&count)
// 		require.NoError(t, err)
// 		return count
// 	}

// 	// Helper function to create identical transactions (same parameters)
// 	createIdenticalBridgeTx := func(amount *big.Int, destAddr common.Address, txName string) common.Hash {
// 		auth.Value = amount
// 		tx, err := bridgeContract.BridgeAsset(
// 			auth,
// 			uint32(2), // destination network
// 			destAddr,
// 			amount,
// 			common.Address{}, // native token
// 			true,             // isForced
// 			[]byte{},
// 		)
// 		require.NoError(t, err)
// 		auth.Value = nil
// 		helpers.CommitBlocks(t, client, 1, blocktime)
// 		t.Logf("  Created %s: %s", txName, tx.Hash().Hex())
// 		return tx.Hash()
// 	}

// 	// Step 1: Create initial blocks and bridge
// 	t.Log("Step 1: Initial setup")
// 	helpers.CommitBlocks(t, client, 5, blocktime)

// 	// Create first bridge with specific parameters
// 	amount := big.NewInt(1000000000000000000)
// 	destAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
// 	_ = createIdenticalBridgeTx(amount, destAddr, "Bridge #1")
// 	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

// 	// Record the block hash and number for potential same-hash scenario
// 	blockNum1, err := client.Client().BlockNumber(ctx)
// 	require.NoError(t, err)
// 	blockHeader1, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(blockNum1)))
// 	require.NoError(t, err)
// 	originalHash := blockHeader1.Hash()
// 	t.Logf("  Original block %d hash: %s", blockNum1, originalHash.Hex())

// 	// Step 2: Create fork point
// 	t.Log("Step 2: Creating fork point")
// 	forkBlockNum := blockNum1
// 	forkBlockHeader, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(forkBlockNum)))
// 	require.NoError(t, err)
// 	forkBlockHash := forkBlockHeader.Hash()

// 	// Step 3: Create fork and try to create identical transaction
// 	t.Log("Step 3: Creating fork and identical transaction")
// 	err = client.Fork(forkBlockHash)
// 	require.NoError(t, err)

// 	// Create identical bridge transaction (same parameters as before)
// 	_ = createIdenticalBridgeTx(amount, destAddr, "Bridge #2 (Identical)")
// 	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

// 	// Check if we have a reorg event (even with same transaction parameters)
// 	time.Sleep(time.Second * 3) // Allow time for reorg detection
// 	reorgCount := getReorgCount()
// 	t.Logf("  Reorg count after identical transaction: %d", reorgCount)

// 	// Step 4: Create another fork with different transaction to ensure hash change
// 	t.Log("Step 4: Creating fork with different transaction to ensure hash change")
// 	currentBlock, err := client.Client().BlockNumber(ctx)
// 	require.NoError(t, err)
// 	currentBlockHeader, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(currentBlock)))
// 	require.NoError(t, err)

// 	err = client.Fork(currentBlockHeader.Hash())
// 	require.NoError(t, err)

// 	// Create different transaction to ensure block hash changes
// 	auth.Value = big.NewInt(2000000000000000000) // Different amount
// 	_, err = bridgeContract.BridgeAsset(
// 		auth,
// 		uint32(2),
// 		common.HexToAddress("0x2222222222222222222222222222222222222222"), // Different address
// 		big.NewInt(2000000000000000000),
// 		common.Address{},
// 		true,
// 		[]byte{},
// 	)
// 	require.NoError(t, err)
// 	auth.Value = nil
// 	helpers.CommitBlocks(t, client, 1, blocktime)
// 	t.Logf("  Created different bridge tx")
// 	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

// 	// Final validation
// 	finalReorgCount := getReorgCount()
// 	t.Logf("Final reorg count: %d", finalReorgCount)

// 	// We expect exactly 1 reorg (the second fork with different transaction)
// 	require.Equal(t, 1, finalReorgCount, "Should have exactly 1 reorg recorded")

// 	// Verify bridges are correctly handled
// 	lastProcessed, err := syncer.GetLastProcessedBlock(ctx)
// 	require.NoError(t, err)
// 	bridges, err := syncer.GetBridges(ctx, 0, lastProcessed)
// 	require.NoError(t, err)
// 	t.Logf("Final bridge count: %d", len(bridges))

// 	t.Log("✅ Same hash reorg edge case test completed successfully")
// }

// // TestStressReorgs tests rapid reorgs with high transaction volume
// func TestStressReorgs(t *testing.T) {
// 	ctx := context.Background()
// 	dbPathSyncer := path.Join(t.TempDir(), "bridgesyncTestStressReorgs_sync.sqlite")
// 	dbPathReorg := path.Join(t.TempDir(), "bridgesyncTestStressReorgs_reorg.sqlite")
// 	blocktime := time.Millisecond * 100 // Very fast block time for stress testing

// 	// Setup simulated L1 environment
//  //nolint:dogsled
// 	client, auth, _, _, bridgeAddr, bridgeContract, _ := helpers.NewSimulatedL1(t)

// 	rd, err := reorgdetector.New(client.Client(), reorgdetector.Config{
// 		DBPath:              dbPathReorg,
// 		CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 100), // Very frequent checks
// 		FinalizedBlock:      aggkittypes.FinalizedBlock,
// 	}, reorgdetector.L1)
// 	require.NoError(t, err)
// 	require.NoError(t, rd.Start(ctx))

// 	// Create bridge syncer with reorg detector
// 	const originNetwork = uint32(1)
// 	bridgeSyncCfg := bridgesync.Config{
// 		DBPath:                             dbPathSyncer,
// 		BridgeAddr:                         bridgeAddr,
// 		BlockFinality:                      aggkittypes.LatestBlock,
// 		SyncBlockChunkSize:                 5, // Smaller chunk size for faster processing
// 		InitialBlockNum:                    0,
// 		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Millisecond * 200),
// 		RetryAfterErrorPeriod:              cfgtypes.NewDuration(time.Millisecond * 100),
// 		MaxRetryAttemptsAfterError:         20, // More retries for stress test
// 		RequireStorageContentCompatibility: true,
// 		DBQueryTimeout:                     cfgtypes.NewDuration(10 * time.Second),
// 	}

// 	ethClient := aggkittypes.NewDefaultEthClient(client.Client(), &aggkittypes.NoopRPCClient{})
// 	syncer, err := bridgesync.NewL1(ctx, bridgeSyncCfg, rd, ethClient, originNetwork, true)
// 	require.NoError(t, err)
// 	require.NotNil(t, syncer)

// 	// Start the syncer
// 	go syncer.Start(ctx)

// 	// Helper function to get reorg count
// 	getReorgCount := func() int {
// 		var count int
// 		err := rd.GetDB().QueryRow("SELECT COUNT(*) FROM reorg_event").Scan(&count)
// 		require.NoError(t, err)
// 		return count
// 	}

// 	// Helper function to create rapid bridge transactions
// 	createRapidBridgeTx := func(amount *big.Int, destAddr common.Address, txName string) common.Hash {
// 		auth.Value = amount
// 		tx, err := bridgeContract.BridgeAsset(
// 			auth,
// 			uint32(2),
// 			destAddr,
// 			amount,
// 			common.Address{},
// 			true,
// 			[]byte{},
// 		)
// 		require.NoError(t, err)
// 		auth.Value = nil
// 		helpers.CommitBlocks(t, client, 1, blocktime)
// 		return tx.Hash()
// 	}

// 	// Step 1: Create initial blocks
// 	t.Log("Step 1: Creating initial blocks")
// 	helpers.CommitBlocks(t, client, 3, blocktime)

// 	// Step 2: Create multiple bridges rapidly
// 	t.Log("Step 2: Creating multiple bridges rapidly")
// 	var forkPoints []uint64
// 	for i := 0; i < 10; i++ {
// 		amount := big.NewInt(int64((i + 1) * 1000000000000000000))
// 		destAddr := common.HexToAddress(fmt.Sprintf("0x%040d", i+1))
// 		tx := createRapidBridgeTx(amount, destAddr, fmt.Sprintf("Bridge #%d", i+1))
// 		t.Logf("  Created Bridge #%d: %s", i+1, tx.Hex())

// 		// Record fork points every 2 transactions
// 		if i%2 == 1 {
// 			blockNum, err := client.Client().BlockNumber(ctx)
// 			require.NoError(t, err)
// 			forkPoints = append(forkPoints, blockNum)
// 		}

// 		// Small delay to allow processing
// 		time.Sleep(time.Millisecond * 50)
// 	}

// 	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

// 	// Step 3: Create rapid reorgs
// 	t.Log("Step 3: Creating rapid reorgs")
// 	expectedReorgCount := 0

// 	for i, forkPoint := range forkPoints {
// 		t.Logf("  Creating reorg #%d from block %d", i+1, forkPoint)

// 		// Get block hash to fork from
// 		forkBlockHeader, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(forkPoint)))
// 		require.NoError(t, err)
// 		forkBlockHash := forkBlockHeader.Hash()

// 		// Create fork
// 		err = client.Fork(forkBlockHash)
// 		require.NoError(t, err)
// 		helpers.CommitBlocks(t, client, 1, blocktime)

// 		// Create different transaction after fork
// 		amount := big.NewInt(int64((i + 1) * 500000000000000000))
// 		destAddr := common.HexToAddress(fmt.Sprintf("0x%040d", (i+1)*100))
// 		tx := createRapidBridgeTx(amount, destAddr, fmt.Sprintf("Fork Bridge #%d", i+1))
// 		t.Logf("    Created Fork Bridge #%d: %s", i+1, tx.Hex())

// 		expectedReorgCount++

// 		// Small delay between reorgs
// 		time.Sleep(time.Millisecond * 100)
// 	}

// 	// Step 4: Wait for all reorgs to be detected
// 	t.Log("Step 4: Waiting for reorg detection")
// 	time.Sleep(time.Second * 3)

// 	// Step 5: Validate reorg count
// 	finalReorgCount := getReorgCount()
// 	t.Logf("Expected reorg count: %d, Actual: %d", expectedReorgCount, finalReorgCount)
// 	require.Equal(t, expectedReorgCount, finalReorgCount, "Reorg count should match expected")

// 	// Step 6: Verify final state
// 	lastProcessed, err := syncer.GetLastProcessedBlock(ctx)
// 	require.NoError(t, err)
// 	bridges, err := syncer.GetBridges(ctx, 0, lastProcessed)
// 	require.NoError(t, err)
// 	t.Logf("Final bridge count: %d", len(bridges))

// 	// Verify we have bridges from both original chain and fork chains
// 	require.Greater(t, len(bridges), 0, "Should have bridges after stress test")

// 	t.Log("✅ Stress reorg test completed successfully")
// }

// // TestDeepReorgChain tests a deep reorg chain with multiple levels
// func TestDeepReorgChain(t *testing.T) {
// 	ctx := context.Background()
// 	dbPathSyncer := path.Join(t.TempDir(), "bridgesyncTestDeepReorg_sync.sqlite")
// 	dbPathReorg := path.Join(t.TempDir(), "bridgesyncTestDeepReorg_reorg.sqlite")
// 	blocktime := time.Second * 2

// 	// Setup simulated L1 environment
//  //nolint:dogsled
// 	client, auth, _, _, bridgeAddr, bridgeContract, _ := helpers.NewSimulatedL1(t)

// 	rd, err := reorgdetector.New(client.Client(), reorgdetector.Config{
// 		DBPath:              dbPathReorg,
// 		CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 500),
// 		FinalizedBlock:      aggkittypes.FinalizedBlock,
// 	}, reorgdetector.L1)
// 	require.NoError(t, err)
// 	require.NoError(t, rd.Start(ctx))

// 	// Create bridge syncer with reorg detector
// 	const originNetwork = uint32(1)
// 	bridgeSyncCfg := bridgesync.Config{
// 		DBPath:                             dbPathSyncer,
// 		BridgeAddr:                         bridgeAddr,
// 		BlockFinality:                      aggkittypes.LatestBlock,
// 		SyncBlockChunkSize:                 10,
// 		InitialBlockNum:                    0,
// 		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Second * 1),
// 		RetryAfterErrorPeriod:              cfgtypes.NewDuration(time.Millisecond * 500),
// 		MaxRetryAttemptsAfterError:         10,
// 		RequireStorageContentCompatibility: true,
// 		DBQueryTimeout:                     cfgtypes.NewDuration(5 * time.Second),
// 	}

// 	ethClient := aggkittypes.NewDefaultEthClient(client.Client(), &aggkittypes.NoopRPCClient{})
// 	syncer, err := bridgesync.NewL1(ctx, bridgeSyncCfg, rd, ethClient, originNetwork, true)
// 	require.NoError(t, err)
// 	require.NotNil(t, syncer)

// 	// Start the syncer
// 	go syncer.Start(ctx)

// 	// Helper function to get reorg count
// 	getReorgCount := func() int {
// 		var count int
// 		err := rd.GetDB().QueryRow("SELECT COUNT(*) FROM reorg_event").Scan(&count)
// 		require.NoError(t, err)
// 		return count
// 	}

// 	// Helper function to create bridge transaction
// 	createBridgeTx := func(amount *big.Int, destAddr common.Address, txName string) common.Hash {
// 		auth.Value = amount
// 		tx, err := bridgeContract.BridgeAsset(
// 			auth,
// 			uint32(2),
// 			destAddr,
// 			amount,
// 			common.Address{},
// 			true,
// 			[]byte{},
// 		)
// 		require.NoError(t, err)
// 		auth.Value = nil
// 		helpers.CommitBlocks(t, client, 1, blocktime)
// 		t.Logf("  Created %s: %s", txName, tx.Hash().Hex())
// 		return tx.Hash()
// 	}

// 	// Step 1: Create initial chain with multiple bridges
// 	t.Log("Step 1: Creating initial chain")
// 	helpers.CommitBlocks(t, client, 3, blocktime)

// 	// Create initial bridges
// 	var forkPoints []uint64
// 	for i := 0; i < 5; i++ {
// 		amount := big.NewInt(int64((i + 1) * 1000000000000000000))
// 		destAddr := common.HexToAddress(fmt.Sprintf("0x%040d", i+1))
// 		tx := createBridgeTx(amount, destAddr, fmt.Sprintf("Initial Bridge #%d", i+1))
// 		t.Logf("  Created Initial Bridge #%d: %s", i+1, tx.Hex())

// 		// Record fork points
// 		blockNum, err := client.Client().BlockNumber(ctx)
// 		require.NoError(t, err)
// 		forkPoints = append(forkPoints, blockNum)

// 		helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)
// 	}

// 	// Step 2: Create deep reorg chain
// 	t.Log("Step 2: Creating deep reorg chain")
// 	expectedReorgCount := 0

// 	// First level reorg
// 	t.Log("  Creating first level reorg")
// 	forkBlock1 := forkPoints[1]
// 	forkBlockHeader1, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(forkBlock1)))
// 	require.NoError(t, err)
// 	err = client.Fork(forkBlockHeader1.Hash())
// 	require.NoError(t, err)

// 	// Create different transaction after first fork
// 	_ = createBridgeTx(big.NewInt(1500000000000000000), common.HexToAddress("0x1111111111111111111111111111111111111111"), "First Fork Bridge")
// 	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)
// 	expectedReorgCount++

// 	// Second level reorg (fork from the fork)
// 	t.Log("  Creating second level reorg")
// 	forkBlock2, err := client.Client().BlockNumber(ctx)
// 	require.NoError(t, err)
// 	forkBlockHeader2, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(forkBlock2)))
// 	require.NoError(t, err)
// 	err = client.Fork(forkBlockHeader2.Hash())
// 	require.NoError(t, err)

// 	// Create different transaction after second fork
// 	_ = createBridgeTx(big.NewInt(2500000000000000000), common.HexToAddress("0x2222222222222222222222222222222222222222"), "Second Fork Bridge")
// 	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)
// 	expectedReorgCount++

// 	// Third level reorg (fork from the second fork)
// 	t.Log("  Creating third level reorg")
// 	forkBlock3, err := client.Client().BlockNumber(ctx)
// 	require.NoError(t, err)
// 	forkBlockHeader3, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(forkBlock3)))
// 	require.NoError(t, err)
// 	err = client.Fork(forkBlockHeader3.Hash())
// 	require.NoError(t, err)

// 	// Create different transaction after third fork
// 	_ = createBridgeTx(big.NewInt(3500000000000000000), common.HexToAddress("0x3333333333333333333333333333333333333333"), "Third Fork Bridge")
// 	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)
// 	expectedReorgCount++

// 	// Step 3: Validate reorg count
// 	time.Sleep(time.Second * 3) // Allow more time for reorg detection
// 	finalReorgCount := getReorgCount()
// 	t.Logf("Expected reorg count: %d, Actual: %d", expectedReorgCount, finalReorgCount)
// 	require.Equal(t, expectedReorgCount, finalReorgCount, "Deep reorg chain should have correct reorg count")

// 	// Step 4: Verify final state
// 	lastProcessed, err := syncer.GetLastProcessedBlock(ctx)
// 	require.NoError(t, err)
// 	bridges, err := syncer.GetBridges(ctx, 0, lastProcessed)
// 	require.NoError(t, err)
// 	t.Logf("Final bridge count: %d", len(bridges))

// 	// Verify we have bridges from the final fork chain
// 	require.Greater(t, len(bridges), 0, "Should have bridges after deep reorg chain")

// 	t.Log("✅ Deep reorg chain test completed successfully")
// }
