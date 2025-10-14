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

	// Setup simulated L1 environment with bridge and GER contracts
	client, auth, _, _, bridgeAddr, bridgeContract := newSimulatedL1ForBridgeTest(t)

	rd, err := reorgdetector.New(client.Client(), reorgdetector.Config{
		DBPath:              dbPathReorg,
		CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 100),
		FinalizedBlock:      aggkittypes.LatestBlock,
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
	commitBlocks(t, client, 3, time.Millisecond*50)
	waitForBridgeSyncerToCatchUp(ctx, t, syncer, client)

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
	client.Commit()
	t.Logf("  Created bridge tx: %s", tx1.Hash().Hex())

	// Wait for syncer to process
	waitForBridgeSyncerToCatchUp(ctx, t, syncer, client)

	// Step 3: Record GER root
	t.Log("Step 3: Recording GER root after first bridge")
	gerRootAfterFirstBridge, err := bridgeContract.GetRoot(nil)
	require.NoError(t, err)
	t.Logf("  GER root after first bridge: %s", common.Hash(gerRootAfterFirstBridge).Hex())

	// Step 4: Record the block hash to fork from later
	t.Log("Step 4: Recording block hash for fork point")
	forkBlockNum, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	forkBlockHeader, err := client.Client().HeaderByNumber(ctx, big.NewInt(int64(forkBlockNum)))
	require.NoError(t, err)
	forkBlockHash := forkBlockHeader.Hash()
	t.Logf("  Fork point: block %d, hash %s", forkBlockNum, forkBlockHash.Hex())

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
	client.Commit()
	t.Logf("  Created bridge tx: %s", tx2.Hash().Hex())

	// Commit additional blocks
	commitBlocks(t, client, 2, time.Millisecond*50)
	waitForBridgeSyncerToCatchUp(ctx, t, syncer, client)

	// Check bridge count in L1 DB
	lastProcessed, err := syncer.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	bridgesBeforeFork, err := syncer.GetBridges(ctx, 0, lastProcessed)
	require.NoError(t, err)
	t.Logf("  Bridges in DB before fork: %d", len(bridgesBeforeFork))
	require.Equal(t, 2, len(bridgesBeforeFork), "Should have 2 bridges before fork")

	// Step 6: Record GER root again
	t.Log("Step 6: Recording GER root after second bridge")
	gerRootAfterSecondBridge, err := bridgeContract.GetRoot(nil)
	require.NoError(t, err)
	t.Logf("  GER root after second bridge: %s", common.Hash(gerRootAfterSecondBridge).Hex())

	//print curr block number
	currBlockNum, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	t.Logf("  Before fork Current block number: %d", currBlockNum)

	// Step 7: Fork from the recorded block
	t.Log("Step 7: Creating fork from block", forkBlockNum)
	err = client.Fork(forkBlockHash)
	require.NoError(t, err)
	t.Log("  Fork created successfully")

	// print curr block number
	currBlockNum, err = client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	t.Logf("  Current block number: %d", currBlockNum)

	// Step 8: Wait for syncer to process everything (rollback to fork point)
	t.Log("Step 8: Waiting for syncer to process the reorg")
	time.Sleep(time.Second * 10) // Give time for reorg detection
	waitForBridgeSyncerToCatchUp(ctx, t, syncer, client)

	// commit a block to check
	client.Commit()
	currBlockNum, err = client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	t.Logf("  After commit block Current block number: %d", currBlockNum)
	time.Sleep(time.Second * 10)

	// Step 9: Record GER and it should be equal to GER from step 3
	t.Log("Step 9: Verifying GER root matches fork point (step 3)")
	gerRootAfterReorg, err := bridgeContract.GetRoot(nil)
	require.NoError(t, err)
	t.Logf("  GER root after reorg: %s", common.Hash(gerRootAfterReorg).Hex())
	t.Logf("  Expected GER (from step 3): %s", common.Hash(gerRootAfterFirstBridge).Hex())
	require.Equal(t, common.Hash(gerRootAfterFirstBridge).Hex(), common.Hash(gerRootAfterReorg).Hex(),
		"GER root after reorg should match the fork point")

	// Step 10: Check bridges from bridge L1 DB
	t.Log("Step 10: Checking bridges in L1 DB after reorg")
	lastProcessedAfterReorg, err := syncer.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	bridgesAfterReorg, err := syncer.GetBridges(ctx, 0, lastProcessedAfterReorg)
	require.NoError(t, err)
	t.Logf("  Bridges in DB after reorg: %d", len(bridgesAfterReorg))
	require.Equal(t, 1, len(bridgesAfterReorg), "Should have only 1 bridge after reorg (second bridge rolled back)")

	// Step 11: Check pending transactions
	t.Log("Step 11: Checking pending transactions")
	pendingTx, err := client.Client().PendingTransactionCount(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, int(pendingTx))

	// Step 12: Commit some blocks on the new chain
	t.Log("Step 12: Committing blocks on the forked chain")
	commitBlocks(t, client, 5, time.Millisecond*50)
	waitForBridgeSyncerToCatchUp(ctx, t, syncer, client)

	pendingTxFinal, err := client.Client().PendingTransactionCount(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, int(pendingTxFinal))

	// Step 13: Check GER again and bridges count again
	t.Log("Step 13: Final verification of GER and bridge count")
	gerRootFinal, err := bridgeContract.GetRoot(nil)
	require.NoError(t, err)
	require.Equal(t, common.Hash(gerRootAfterSecondBridge).Hex(), common.Hash(gerRootFinal).Hex())

	lastProcessedFinal, err := syncer.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	bridgesFinal, err := syncer.GetBridges(ctx, 0, lastProcessedFinal)
	require.NoError(t, err)
	t.Logf("  Final bridges in DB: %d", len(bridgesFinal))
	require.Equal(t, 2, len(bridgesFinal), "Should still have only 1 bridge in DB")

	t.Log("✅ Test completed successfully - syncer handled reorg correctly")
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

	balance, _ := new(big.Int).SetString("10000000000000000000000", 10) //nolint:mnd
	address := auth.From
	genesisAlloc := map[common.Address]types.Account{
		address: {
			Balance: balance,
		},
	}

	blockGasLimit := uint64(999999999999999999) //nolint:mnd
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
