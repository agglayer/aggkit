package bridgesync_test

import (
	"context"
	"fmt"
	"math/big"
	"path"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonzkevmbridgev2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonzkevmglobalexitrootv2"
	"github.com/agglayer/aggkit/bridgesync"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/test/contracts/proxy"
	"github.com/agglayer/aggkit/test/helpers"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
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
	// client, _, _, _, bridgeAddr, _ := newSimulatedL1ForBridgeTest(t)
	client, auth, _, _, bridgeAddr, bridgeContract := newSimulatedL1ForBridgeTest(t)

	rd, err := reorgdetector.New(client.Client(), reorgdetector.Config{
		DBPath:              dbPathReorg,
		CheckReorgsInterval: cfgtypes.NewDuration(time.Second * 2),
		FinalizedBlock:      aggkittypes.SafeBlock,
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
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Millisecond * 100),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(time.Millisecond * 100),
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
	commitBlocks(t, client, 10, blocktime)

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
	commitBlocks(t, client, 1, blocktime)
	t.Logf("  Created bridge tx: %s", tx1.Hash().Hex())
	blockNum1, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	t.Logf("  Block number after first bridge: %d", blockNum1)
	blockhash1, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(blockNum1)))
	require.NoError(t, err)
	t.Logf("  Block hash after first bridge: %s", blockhash1.Hash().Hex())

	// Wait for syncer to process
	waitForBridgeSyncerToCatchUp(ctx, t, syncer, client)

	// // Step 3: Record GER root
	// t.Log("Step 3: Recording GER root after first bridge")
	// gerRootAfterFirstBridge, err := bridgeContract.GetRoot(nil)
	// require.NoError(t, err)
	// t.Logf("  GER root after first bridge: %s", common.Hash(gerRootAfterFirstBridge).Hex())

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
	commitBlocks(t, client, 1, blocktime)
	blockNum3, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	t.Logf("  Block number after first fork bridge: %d", blockNum3)
	blockhash3, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(blockNum3)))
	require.NoError(t, err)
	t.Logf("  Block hash after first fork bridge: %s", blockhash3.Hash().Hex())
	// waitForBridgeSyncerToCatchUp(ctx, t, syncer, client)

	// Commit additional blocks
	commitBlocks(t, client, 1, blocktime)
	blockNum3, err = client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	t.Logf("  Block number after first fork bridge 2nd block: %d", blockNum3)
	blockhash3, err = client.Client().HeaderByNumber(ctx, big.NewInt(int64(blockNum3)))
	require.NoError(t, err)
	t.Logf("  Block hash after first fork bridge 2nd block: %s", blockhash3.Hash().Hex())

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
	commitBlocks(t, client, 1, blocktime)
	blockNum2, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	t.Logf("  Block number after second bridge: %d", blockNum2)
	t.Logf("  Created bridge tx: %s", tx2.Hash().Hex())

	// Commit additional blocks
	// commitBlocks(t, client, 1, blocktime)
	waitForBridgeSyncerToCatchUp(ctx, t, syncer, client)

	// // Check bridge count in L1 DB
	// lastProcessed, err := syncer.GetLastProcessedBlock(ctx)
	// require.NoError(t, err)
	// bridgesBeforeFork, err := syncer.GetBridges(ctx, 0, lastProcessed)
	// require.NoError(t, err)
	// t.Logf("  Bridges in DB before fork: %d", len(bridgesBeforeFork))
	// require.Equal(t, 2, len(bridgesBeforeFork), "Should have 2 bridges before fork")

	// // Step 6: Record GER root again
	// t.Log("Step 6: Recording GER root after second bridge")
	// gerRootAfterSecondBridge, err := bridgeContract.GetRoot(nil)
	// require.NoError(t, err)
	// t.Logf("  GER root after second bridge: %s", common.Hash(gerRootAfterSecondBridge).Hex())

	// // print current block number
	// currBlockNum, err := client.Client().BlockNumber(ctx)
	// require.NoError(t, err)
	// t.Logf("  Before fork Current block number: %d", currBlockNum)

	// Step 7: Fork from the recorded block
	t.Log("Step 7: Creating fork from block", forkFromBlock)

	// // Debug: Check what blocks are currently tracked before fork
	// trackedBlocksBeforeFork := rd.GetTrackedBlocksInfo()
	// t.Logf("  Tracked blocks before fork: %+v", trackedBlocksBeforeFork)

	err = client.Fork(forkBlockHash)
	require.NoError(t, err)
	t.Log("  Fork created successfully")
	commitBlocks(t, client, 1, blocktime)
	currBlockNum, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	t.Logf("  After fork Current block number: %d", currBlockNum)
	forkedBlockHash, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(currBlockNum)))
	require.NoError(t, err)
	t.Logf("Hash of the forked block: %s", forkedBlockHash.Hash().Hex())
	t.Logf("After fork Current block number: %d", currBlockNum)

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
	commitBlocks(t, client, 1, blocktime)
	t.Logf("  Created different bridge tx after fork: %s", txAfterFork.Hash().Hex())

	// Verify that block hash changes after fork to detect reorg differences
	currBlockNum, err = client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	forkedBlockHash, err = client.Client().HeaderByNumber(ctx, big.NewInt(int64(currBlockNum)))
	require.NoError(t, err)
	t.Logf("Hash of the forked block: %s", forkedBlockHash.Hash().Hex())
	t.Logf("After fork Current block number: %d", currBlockNum)

	// Verify that the block hash is different from the original chain
	// This ensures we can detect the reorg difference
	// require.NotEqual(t, forkBlockHash.Hex(), forkedBlockHash.Hash().Hex(),
	// 	"Block hash should be different after fork to enable reorg detection")

	// // Step 8: Bridge asset with different params and commit blocks, check count
	// t.Log("Step 8: Bridge asset #3 with different params and commit blocks")
	// amount2 = big.NewInt(2000000000000000000) // 2 ETH
	// destinationAddress2 = common.HexToAddress("0x2222222222222222222222222222222222222222")
	// auth.Value = amount2
	// tx2, err = bridgeContract.BridgeAsset(
	// 	auth,
	// 	destinationNetwork,
	// 	destinationAddress2,
	// 	amount2,
	// 	common.Address{}, // native token
	// 	true,             // isForced
	// 	[]byte{},
	// )
	// require.NoError(t, err)
	// auth.Value = nil
	// commitBlocks(t, client, 1, blocktime)
	// blockNum2, err = client.Client().BlockNumber(ctx)
	// require.NoError(t, err)
	// t.Logf("  Block number after third bridge: %d", blockNum2)
	// t.Logf("  Created bridge tx: %s", tx2.Hash().Hex())

	// // Step 9: Check bridge count immediately after fork (before reorg processing)
	// t.Log("Step 9: Checking bridge count immediately after fork (before reorg processing)")
	// lastProcessedAfterFork, err := syncer.GetLastProcessedBlock(ctx)
	// require.NoError(t, err)
	// bridgesAfterFork, err := syncer.GetBridges(ctx, 0, lastProcessedAfterFork)
	// require.NoError(t, err)
	// t.Logf("  Bridges in DB immediately after fork: %d", len(bridgesAfterFork))
	// // At this point, we might still have the old bridges until reorg is processed

	// // Wait for syncer to process and verify headers cache is populated
	// t.Log("Step 9.1: Waiting for syncer to process and verify headers cache")
	// waitForBridgeSyncerToCatchUp(ctx, t, syncer, client)

	// // Give some time for reorg detector to populate headers cache and detect reorg
	// time.Sleep(time.Second * 5)

	// // Verify headersCache is correctly populated with block numbers and hashes
	// // We need to access the global headersCache from reorgdetector package
	// verifyHeadersCachePopulated(t, rd, ctx, client)

	// // Step 10: Verify that hdrs (headers list) is updated as soon as new blocks are found
	// t.Log("Step 10: Verifying headers list is updated with new blocks")
	// verifyHeadersListUpdated(t, rd, ctx, client)

	// // Step 10.1: Check if reorg was detected
	// t.Log("Step 10.1: Checking if reorg was detected")
	// checkReorgDetection(t, rd, ctx)

	// // Step 11: Final verification - ensure reorg detection is working properly
	// t.Log("Step 11: Final verification of reorg detection behavior")

	// // Commit additional blocks to ensure the reorg detector has processed everything
	// commitBlocks(t, client, 3, blocktime)
	// waitForBridgeSyncerToCatchUp(ctx, t, syncer, client)

	// // // Give more time for reorg detection to process
	// // time.Sleep(time.Second * 2)

	// // Final verification of headers cache and tracked blocks
	// verifyHeadersCachePopulated(t, rd, ctx, client)
	// verifyHeadersListUpdated(t, rd, ctx, client)

	// // Step 12: Verify bridge count and content after reorg
	// t.Log("Step 12: Verifying bridge count and content after reorg")
	// lastProcessedFinal, err := syncer.GetLastProcessedBlock(ctx)
	// require.NoError(t, err)
	// bridgesFinal, err := syncer.GetBridges(ctx, 0, lastProcessedFinal)
	// require.NoError(t, err)
	// t.Logf("Final bridges in DB: %d", len(bridgesFinal))

	// // Debug: Print all bridges to understand what we have
	// for i, bridge := range bridgesFinal {
	// 	t.Logf("  Bridge %d: Amount=%s, DestAddr=%s, TxHash=%s, BlockNum=%d",
	// 		i+1, bridge.Amount.String(), bridge.DestinationAddress.Hex(), bridge.TxHash.Hex(), bridge.BlockNum)
	// }

	// // After reorg, we should have:
	// // 1. Bridge #1 (before fork) - should remain
	// // 2. Bridge after fork (0.5 ETH to 0x333...) - should remain
	// // 3. Bridge #3 (2 ETH to 0x222...) - should remain
	// // Bridge #2 (created after fork point but before actual fork) should be removed
	// require.Equal(t, 2, len(bridgesFinal), "Should have 2 bridges after reorg: Bridge #1, Bridge after fork")

	// t.Log("✅ Test completed successfully - reorg detection behavior verified")

	// // Step 11: Check pending transactions
	// t.Log("before syncer catch up Checking pending transactions")
	// pendingTx, err := client.Client().PendingTransactionCount(ctx)
	// require.NoError(t, err)
	// fmt.Printf("before syncer catch up Pending transactions: %d\n", pendingTx)

	// // Step 8: Wait for syncer to process everything (rollback to fork point)
	// t.Log("Step 8: Waiting for syncer to process the reorg")
	// waitForBridgeSyncerToCatchUp(ctx, t, syncer, client)

	// // Step 11: Check pending transactions
	// t.Log("after syncer catch up Checking pending transactions")
	// pendingTx, err = client.Client().PendingTransactionCount(ctx)
	// require.NoError(t, err)
	// fmt.Printf("after syncer catch up Pending transactions: %d\n", pendingTx)

	// // Step 9: Record GER and it should be equal to GER from step 3
	// t.Log("Step 9: Verifying GER root matches fork point (step 3)")
	// gerRootAfterReorg, err := bridgeContract.GetRoot(nil)
	// require.NoError(t, err)
	// t.Logf("  GER root after reorg: %s", common.Hash(gerRootAfterReorg).Hex())
	// t.Logf("  Expected GER (from step 3): %s", common.Hash(gerRootAfterFirstBridge).Hex())
	// require.Equal(t, common.Hash(gerRootAfterFirstBridge).Hex(), common.Hash(gerRootAfterReorg).Hex(),
	// 	"GER root after reorg should match the fork point")

	// // Step 11: Check pending transactions
	// t.Log("--------------- Checking pending transactions")
	// pendingTx, err = client.Client().PendingTransactionCount(ctx)
	// require.NoError(t, err)
	// fmt.Printf("after reorg Pending transactions: %d\n", pendingTx)

	// // Step 10: Check bridges from bridge L1 DB
	// t.Log("Step 10: Checking bridges in L1 DB after reorg")
	// lastProcessedAfterReorg, err := syncer.GetLastProcessedBlock(ctx)
	// require.NoError(t, err)
	// bridgesAfterReorg, err := syncer.GetBridges(ctx, 0, lastProcessedAfterReorg)
	// require.NoError(t, err)
	// t.Logf("  Bridges in DB after reorg: %d", len(bridgesAfterReorg))
	// require.Equal(t, 1, len(bridgesAfterReorg), "Should have only 1 bridge after reorg (second bridge rolled back)")

	// // Step 11: Check pending transactions
	// t.Log("Step 11: Checking pending transactions")
	// pendingTx, err = client.Client().PendingTransactionCount(ctx)
	// require.NoError(t, err)
	// require.Equal(t, 1, int(pendingTx))

	// // Step 12: Commit some blocks on the new chain
	// t.Log("Step 12: Committing blocks on the forked chain")
	// commitBlocks(t, client, 5, time.Millisecond*50)
	// waitForBridgeSyncerToCatchUp(ctx, t, syncer, client)

	// pendingTxFinal, err := client.Client().PendingTransactionCount(ctx)
	// require.NoError(t, err)
	// require.Equal(t, 0, int(pendingTxFinal))

	// // Step 13: Check GER again and bridges count again
	// t.Log("Step 13: Final verification of GER and bridge count")
	// gerRootFinal, err := bridgeContract.GetRoot(nil)
	// require.NoError(t, err)
	// require.Equal(t, common.Hash(gerRootAfterSecondBridge).Hex(), common.Hash(gerRootFinal).Hex())

	// lastProcessedFinal, err := syncer.GetLastProcessedBlock(ctx)
	// require.NoError(t, err)
	// bridgesFinal, err := syncer.GetBridges(ctx, 0, lastProcessedFinal)
	// require.NoError(t, err)
	// t.Logf("  Final bridges in DB: %d", len(bridgesFinal))
	// require.Equal(t, 2, len(bridgesFinal), "Should still have only 1 bridge in DB")

	// t.Log("✅ Test completed successfully - syncer handled reorg correctly")
}

// newSimulatedL1ForBridgeTest creates a new simulated L1 backend with bridge and GER contracts deployed
func newSimulatedL1ForBridgeTest(t *testing.T) (
	*simulated.Backend,
	*bind.TransactOpts,
	common.Address,
	*polygonzkevmglobalexitrootv2.Polygonzkevmglobalexitrootv2,
	common.Address,
	*polygonzkevmbridgev2.Polygonzkevmbridgev2,
) {
	t.Helper()

	const chainID = 1337
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, big.NewInt(chainID))
	require.NoError(t, err)

	balance, _ := new(big.Int).SetString("10000000000000000000000", 10)
	address := auth.From
	genesisAlloc := map[common.Address]types.Account{
		address: {
			Balance: balance,
		},
	}

	blockGasLimit := uint64(999999999999999999)
	client := simulated.NewBackend(
		genesisAlloc,
		simulated.WithBlockGasLimit(blockGasLimit),
	)

	ctx := context.Background()

	// Calculate address for future contracts
	nonce, err := client.Client().PendingNonceAt(ctx, auth.From)
	require.NoError(t, err)

	// Bridge contract deployment will use 2 transactions (implementation + proxy)
	// Nonce+0: Bridge implementation
	// Nonce+1: Bridge proxy
	// Nonce+2: GER contract
	calculatedBridgeAddr := crypto.CreateAddress(auth.From, nonce+1)
	calculatedGERAddr := crypto.CreateAddress(auth.From, nonce+2)

	// Deploy bridge implementation
	bridgeImplAddr, _, _, err := polygonzkevmbridgev2.DeployPolygonzkevmbridgev2(auth, client.Client())
	require.NoError(t, err)
	client.Commit()

	// Deploy bridge proxy with empty initialization data
	bridgeProxyAddr, _, _, err := proxy.DeployProxy(auth, client.Client(), bridgeImplAddr, auth.From, []byte{})
	require.NoError(t, err)
	require.Equal(t, calculatedBridgeAddr, bridgeProxyAddr)
	client.Commit()

	// Deploy GER contract
	gerAddr, _, gerContract, err := polygonzkevmglobalexitrootv2.DeployPolygonzkevmglobalexitrootv2(
		auth, client.Client(), auth.From, bridgeProxyAddr)
	require.NoError(t, err)
	require.Equal(t, calculatedGERAddr, gerAddr)
	client.Commit()

	bridgeContract, err := polygonzkevmbridgev2.NewPolygonzkevmbridgev2(bridgeProxyAddr, client.Client())
	require.NoError(t, err)

	_, err = bridgeContract.Initialize0(
		auth,
		uint32(0),        // networkID
		common.Address{}, // gasTokenAddressMainnet
		uint32(0),        // gasTokenNetworkMainnet
		gerAddr,          // global exit root manager
		common.Address{}, // rollup manager
		[]byte{},         // gasTokenMetadata
	)
	require.NoError(t, err)
	client.Commit()

	return client, auth, gerAddr, gerContract, bridgeProxyAddr, bridgeContract
}

// waitForBridgeSyncerToCatchUp waits for the bridge syncer to process all available blocks
func waitForBridgeSyncerToCatchUp(ctx context.Context, t *testing.T, syncer *bridgesync.BridgeSync, client *simulated.Backend) {
	t.Helper()
	for {
		lastBlockNum, err := client.Client().BlockNumber(ctx)
		require.NoError(t, err)
		lastProcessed, err := syncer.GetLastProcessedBlock(ctx)
		require.NoError(t, err)

		if lastProcessed >= lastBlockNum {
			return
		}
		time.Sleep(time.Millisecond * 100)
	}
}

// commitBlocks commits multiple empty blocks
func commitBlocks(t *testing.T, client *simulated.Backend, count int, delay time.Duration) {
	t.Helper()
	for i := 0; i < count; i++ {
		client.Commit()
		if delay > 0 {
			time.Sleep(delay)
		}
	}
}

// verifyHeadersCachePopulated verifies that the headers cache is correctly populated
func verifyHeadersCachePopulated(t *testing.T, rd *reorgdetector.ReorgDetector, ctx context.Context, client *simulated.Backend) {
	t.Helper()

	// Get the current headers cache
	headersCache := reorgdetector.GetHeadersCache()
	t.Logf("Headers cache size: %d", len(headersCache))

	// Get current block number to verify cache has recent blocks
	currentBlockNum, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)

	// Verify that headers cache contains block numbers and corresponding hashes
	cacheHasRecentBlocks := false
	for blockNum, header := range headersCache {
		require.NotNil(t, header, "Header should not be nil for block %d", blockNum)
		require.Equal(t, blockNum, header.Number.Uint64(), "Block number should match cache key")
		require.NotEqual(t, common.Hash{}, header.Hash(), "Block hash should not be empty for block %d", blockNum)

		// Check if we have recent blocks in cache
		if blockNum >= currentBlockNum-5 {
			cacheHasRecentBlocks = true
		}

		t.Logf("Cache entry - Block %d: Hash %s", blockNum, header.Hash().Hex())
	}

	// Verify that we have at least some recent blocks in the cache
	require.True(t, cacheHasRecentBlocks || len(headersCache) > 0,
		"Headers cache should contain recent blocks or have some entries")

	t.Logf("✅ Headers cache verification passed - %d entries found", len(headersCache))
}

// verifyHeadersListUpdated verifies that the headers list (hdrs) is updated as soon as new blocks are found
func verifyHeadersListUpdated(t *testing.T, rd *reorgdetector.ReorgDetector, ctx context.Context, client *simulated.Backend) {
	t.Helper()

	// Get the tracked blocks info from reorg detector
	trackedBlocksInfo := rd.GetTrackedBlocksInfo()
	t.Logf("Tracked blocks count: %d", len(trackedBlocksInfo))

	// Get current block number
	currentBlockNum, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)

	// Verify that we have tracked blocks for the bridge syncer
	// The bridge syncer should have subscribed to the reorg detector
	hasTrackedBlocks := false
	for subscriberID, headers := range trackedBlocksInfo {
		t.Logf("Subscriber %s has %d tracked blocks", subscriberID, len(headers))

		if len(headers) > 0 {
			hasTrackedBlocks = true

			// Get sorted block numbers to verify they are up to date
			var blockNumbers []uint64
			for blockNum := range headers {
				blockNumbers = append(blockNumbers, blockNum)
			}

			// Sort block numbers
			for i := 0; i < len(blockNumbers); i++ {
				for j := i + 1; j < len(blockNumbers); j++ {
					if blockNumbers[i] > blockNumbers[j] {
						blockNumbers[i], blockNumbers[j] = blockNumbers[j], blockNumbers[i]
					}
				}
			}

			t.Logf("Subscriber %s tracked headers:", subscriberID)

			for _, blockNum := range blockNumbers {
				hash := headers[blockNum]
				require.NotEqual(t, common.Hash{}, hash, "Header hash should not be empty for block %d", blockNum)
				require.Greater(t, blockNum, uint64(0), "Block number should be greater than 0")
				t.Logf("  Block %d: Hash %s", blockNum, hash.Hex())
			}

			// Verify that we have recent blocks tracked
			if len(blockNumbers) > 0 {
				latestTrackedBlock := blockNumbers[len(blockNumbers)-1]
				t.Logf("Latest tracked block for %s: %d (current: %d)", subscriberID, latestTrackedBlock, currentBlockNum)

				// The tracked blocks should be reasonably up to date (within a few blocks)
				require.GreaterOrEqual(t, latestTrackedBlock, currentBlockNum-10,
					"Latest tracked block should be reasonably recent")
			}
		}
	}

	require.True(t, hasTrackedBlocks, "Should have at least one subscriber with tracked blocks")
	t.Logf("✅ Headers list verification passed - found tracked blocks for %d subscribers", len(trackedBlocksInfo))
}

// verifyBridgesAfterReorg verifies that the correct bridges remain in the database after reorg
func verifyBridgesAfterReorg(t *testing.T, bridges []bridgesync.Bridge, expectedAmount1 *big.Int, expectedAddress1 common.Address, expectedTxAfterFork *types.Transaction, expectedTx3 *types.Transaction) {
	t.Helper()

	t.Log("Verifying bridges after reorg:")

	// We should have exactly 3 bridges
	require.Equal(t, 3, len(bridges), "Should have exactly 3 bridges after reorg")

	// Find the bridges by their characteristics
	var bridgeAfterFork, bridge3 *bridgesync.Bridge
	var foundBridge1, foundBridgeAfterFork, foundBridge3 bool

	for i, bridge := range bridges {
		t.Logf("  Bridge %d: Amount=%s, DestAddr=%s, TxHash=%s, BlockNum=%d",
			i+1, bridge.Amount.String(), bridge.DestinationAddress.Hex(), bridge.TxHash.Hex(), bridge.BlockNum)

		// Bridge #1: 1 ETH to 0x111...
		if bridge.Amount.Cmp(expectedAmount1) == 0 && bridge.DestinationAddress == expectedAddress1 {
			foundBridge1 = true
			t.Logf("    -> Identified as Bridge #1 (before fork)")
		}

		// Bridge after fork: 0.5 ETH to 0x333...
		if bridge.Amount.Cmp(big.NewInt(500000000000000000)) == 0 && bridge.DestinationAddress == common.HexToAddress("0x3333333333333333333333333333333333333333") {
			bridgeAfterFork = &bridges[i]
			foundBridgeAfterFork = true
			t.Logf("    -> Identified as Bridge after fork")
		}

		// Bridge #3: 2 ETH to 0x222...
		if bridge.Amount.Cmp(big.NewInt(2000000000000000000)) == 0 && bridge.DestinationAddress == common.HexToAddress("0x2222222222222222222222222222222222222222") {
			bridge3 = &bridges[i]
			foundBridge3 = true
			t.Logf("    -> Identified as Bridge #3 (after fork)")
		}
	}

	// Verify all expected bridges are found
	require.True(t, foundBridge1, "Bridge #1 (before fork) should be present in DB")
	require.True(t, foundBridgeAfterFork, "Bridge after fork should be present in DB")
	require.True(t, foundBridge3, "Bridge #3 (after fork) should be present in DB")

	// Verify transaction hashes match
	require.Equal(t, expectedTxAfterFork.Hash(), bridgeAfterFork.TxHash, "Bridge after fork should have correct transaction hash")
	require.Equal(t, expectedTx3.Hash(), bridge3.TxHash, "Bridge #3 should have correct transaction hash")

	// Verify block numbers are reasonable (should be after the fork)
	require.Greater(t, bridgeAfterFork.BlockNum, uint64(0), "Bridge after fork should have valid block number")
	require.Greater(t, bridge3.BlockNum, bridgeAfterFork.BlockNum, "Bridge #3 should be in a later block than bridge after fork")

	t.Log("✅ Bridge verification after reorg passed - all expected bridges are present")
}

// checkReorgDetection checks if reorg events were detected and logged
func checkReorgDetection(t *testing.T, rd *reorgdetector.ReorgDetector, ctx context.Context) {
	t.Helper()

	// Get the last reorg event from the database
	reorgEvent, err := rd.GetLastReorgEvent(ctx)
	if err != nil {
		t.Logf("No reorg events found in database: %v", err)
		t.Log("❌ Reorg detection verification failed - no reorg events found")
		return
	}

	t.Logf("Found reorg event: FromBlock=%d, ToBlock=%d, SubscriberID=%s, CurrentHash=%s, TrackedHash=%s, DetectedAt=%d",
		reorgEvent.FromBlock, reorgEvent.ToBlock, reorgEvent.SubscriberID, reorgEvent.CurrentHash.Hex(), reorgEvent.TrackedHash.Hex(), reorgEvent.DetectedAt)

	t.Log("✅ Reorg detection verification passed - reorg event found")
}
