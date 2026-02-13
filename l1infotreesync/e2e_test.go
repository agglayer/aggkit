package l1infotreesync_test

import (
	"context"
	"fmt"
	"math/big"
	"path"
	"strconv"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l1infotreesync/mocks"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/test/contracts/verifybatchesmock"
	"github.com/agglayer/aggkit/test/helpers"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const useMultidownloaderForTests = true

func newSimulatedClient(t *testing.T) (
	*simulated.Backend,
	*bind.TransactOpts,
	common.Address,
	common.Address,
	*agglayerger.Agglayerger,
	*verifybatchesmock.Verifybatchesmock,
) {
	t.Helper()
	ctx := context.Background()

	deployerAuth, err := helpers.CreateAccount(big.NewInt(1337))
	require.NoError(t, err)

	client, setup := helpers.NewSimulatedBackend(t, nil, deployerAuth)

	nonce, err := client.Client().PendingNonceAt(ctx, setup.UserAuth.From)

	require.NoError(t, err)

	precalculatedGERAddr := crypto.CreateAddress(setup.UserAuth.From, nonce+1)
	verifyAddr, _, verifyContract, err := verifybatchesmock.DeployVerifybatchesmock(setup.UserAuth, client.Client(), precalculatedGERAddr)
	require.NoError(t, err)
	client.Commit()

	gerAddr, _, gerContract, err := agglayerger.DeployAgglayerger(setup.UserAuth, client.Client(), verifyAddr, setup.UserAuth.From)

	require.NoError(t, err)
	require.Equal(t, precalculatedGERAddr, gerAddr)
	client.Commit()

	err = setup.DeployBridge(client, gerAddr, 0)
	require.NoError(t, err)

	return client, setup.UserAuth, gerAddr, verifyAddr, gerContract, verifyContract
}

func TestE2E(t *testing.T) {
	ctx := t.Context()
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestE2E.sqlite")

	client, auth, gerAddr, verifyAddr, gerSc, _ := newSimulatedClient(t)
	cfg := l1infotreesync.Config{
		DBPath:                             dbPath,
		InitialBlock:                       0,
		SyncBlockChunkSize:                 10,
		BlockFinality:                      aggkittypes.LatestBlock,
		GlobalExitRootAddr:                 gerAddr,
		RollupManagerAddr:                  verifyAddr,
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(time.Millisecond * 100),
		MaxRetryAttemptsAfterError:         25,
		RequireStorageContentCompatibility: true,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Millisecond),
	}
	var syncer *l1infotreesync.L1InfoTreeSync
	var err error
	var evmMultidownloader *multidownloader.EVMMultidownloader
	if useMultidownloaderForTests {
		cfgMD := multidownloader.NewConfigDefault("l1", t.TempDir())
		cfgMD.Enabled = true
		finality, err := aggkittypes.NewBlockNumberFinality("latestBlock/-15")
		require.NoError(t, err)
		cfgMD.BlockFinality = *finality
		evmMultidownloader, err = multidownloader.NewEVMMultidownloader(
			log.WithFields("module", "multidownloader"),
			cfgMD,
			"testMD",
			etherman.NewDefaultEthClient(client.Client(), nil, nil),
			nil, // rpcClient
			nil,
			nil,
			nil, // reorgProcessor will be created internally
		)
		require.NoError(t, err)
		syncer, err = l1infotreesync.NewMultidownloadBased(ctx, cfg, evmMultidownloader, l1infotreesync.FlagAllowWrongContractsAddrs)
		require.NoError(t, err)
		go func() {
			// This always return an error becase at the end of the test the context is cancelled
			err = evmMultidownloader.Start(ctx)
			log.Infof("Multidownloader exited with error: %v", err)
		}()
	} else {
		mockReorgDetector := mocks.NewReorgDetectorMock(t)
		mockReorgDetector.EXPECT().Subscribe(mock.Anything).Return(&reorgdetector.Subscription{}, nil)
		mockReorgDetector.EXPECT().AddBlockToTrack(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockReorgDetector.EXPECT().GetFinalizedBlockType().Return(aggkittypes.FinalizedBlock).Once()
		mockReorgDetector.EXPECT().GetTrackedBlockByBlockNumber(mock.Anything, mock.Anything).Return(&reorgdetector.Header{}, nil)

		multidownloaderClient := sync.NewAdapterEthClientToMultidownloader(etherman.NewDefaultEthClient(client.Client(), nil, nil))
		syncer, err = l1infotreesync.NewLegacy(ctx, cfg, multidownloaderClient, mockReorgDetector,
			l1infotreesync.FlagAllowWrongContractsAddrs)
		require.NoError(t, err)
	}

	go syncer.Start(ctx)

	// Update GER 3 times
	for i := range 3 {
		tx, err := gerSc.UpdateExitRoot(auth, common.HexToHash(strconv.Itoa(i)))
		require.NoError(t, err)
		client.Commit()
		g, err := gerSc.L1InfoRootMap(nil, uint32(i+1))
		require.NoError(t, err)
		receipt, err := client.Client().TransactionReceipt(ctx, tx.Hash())
		require.NoError(t, err)
		require.Equal(t, receipt.Status, types.ReceiptStatusSuccessful)
		// Let the processor catch up
		helpers.RequireProcessorUpdated(t, syncer, receipt.BlockNumber.Uint64(), nil)

		expectedGER, err := gerSc.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
		require.NoError(t, err)
		info, err := syncer.GetInfoByIndex(ctx, uint32(i))
		require.NoError(t, err)
		require.Equal(t, common.Hash(expectedGER), info.GlobalExitRoot, fmt.Sprintf("index: %d", i))
		require.Equal(t, receipt.BlockNumber.Uint64(), info.BlockNumber)

		expectedRoot, err := gerSc.GetRoot(&bind.CallOpts{Pending: false})
		require.NoError(t, err)
		require.Equal(t, g, expectedRoot)
		actualRoot, err := syncer.GetL1InfoTreeRootByIndex(ctx, uint32(i))
		require.NoError(t, err)
		require.Equal(t, common.Hash(expectedRoot), actualRoot.Hash)

		latestGER, err := syncer.GetLatestL1InfoGER(ctx)
		require.NoError(t, err)
		require.Equal(t, common.Hash(expectedGER), latestGER)
	}
	log.Infof("FINISH TEST OK!!!!!!!!!!!!!!!!!!!!!!")
}

func TestWithReorgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                      string
		useMultidownloaderForTest bool
	}{
		{
			name:                      "with legacy reorgdetector",
			useMultidownloaderForTest: false,
		},
		{
			name:                      "with multidownloader",
			useMultidownloaderForTest: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			suffix := "legacy"
			if tt.useMultidownloaderForTest {
				suffix = "multidownloader"
			}
			dbPathSyncer := path.Join(t.TempDir(), "l1infotreesyncTestWithReorgs_sync_"+suffix+".sqlite")
			dbPathReorg := path.Join(t.TempDir(), "l1infotreesyncTestWithReorgs_reorg_"+suffix+".sqlite")

			client, auth, gerAddr, verifyAddr, gerSc, verifySC := newSimulatedClient(t)

			cfg := l1infotreesync.Config{
				DBPath:                             dbPathSyncer,
				InitialBlock:                       0,
				SyncBlockChunkSize:                 10,
				BlockFinality:                      aggkittypes.LatestBlock,
				GlobalExitRootAddr:                 gerAddr,
				RollupManagerAddr:                  verifyAddr,
				RetryAfterErrorPeriod:              cfgtypes.NewDuration(time.Millisecond * 100),
				MaxRetryAttemptsAfterError:         25,
				RequireStorageContentCompatibility: true,
				WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Millisecond),
			}

			var syncer *l1infotreesync.L1InfoTreeSync
			var err error
			var evmMultidownloader *multidownloader.EVMMultidownloader
			if tt.useMultidownloaderForTest {
				cfgMD := multidownloader.NewConfigDefault("l1", t.TempDir())
				cfgMD.Enabled = true
				finality, err := aggkittypes.NewBlockNumberFinality("latestBlock/-15")
				require.NoError(t, err)
				cfgMD.BlockFinality = *finality
				cfgMD.WaitPeriodToCheckCatchUp = cfgtypes.NewDuration(time.Millisecond * 1)
				cfgMD.PeriodToCheckReorgs = cfgtypes.NewDuration(time.Millisecond * 1)
				evmMultidownloader, err = multidownloader.NewEVMMultidownloader(
					log.WithFields("module", "multidownloader"),
					cfgMD,
					"testMD",
					etherman.NewDefaultEthClient(client.Client(), nil, nil),
					nil, // rpcClient
					nil, // Storage will be created internally
					nil, // blockNotifierManager will be created internally
					nil, // reorgProcessor will be created internally
				)
				require.NoError(t, err)
				syncer, err = l1infotreesync.NewMultidownloadBased(ctx, cfg, evmMultidownloader, l1infotreesync.FlagAllowWrongContractsAddrs)
				require.NoError(t, err)
				go func() {
					err = evmMultidownloader.Start(ctx)
					require.NoError(t, err)
				}()
			} else {
				rdConfig := reorgdetector.Config{
					DBPath:              dbPathReorg,
					CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 100),
					FinalizedBlock:      aggkittypes.FinalizedBlock,
				}
				rd, err := reorgdetector.New(etherman.NewDefaultEthClient(client.Client(), nil, nil), rdConfig, reorgdetector.L1)
				require.NoError(t, err)
				require.NoError(t, rd.Start(ctx))
				multidownloaderClient := sync.NewAdapterEthClientToMultidownloader(etherman.NewDefaultEthClient(client.Client(), nil, nil))
				syncer, err = l1infotreesync.NewLegacy(ctx, cfg, multidownloaderClient, rd, l1infotreesync.FlagAllowWrongContractsAddrs)
				require.NoError(t, err)
			}
			go syncer.Start(ctx)

			// Commit block 6
			header, err := client.Client().HeaderByHash(ctx, client.Commit())
			require.NoError(t, err)
			reorgFrom := header.Hash()

			// Commit block 7
			helpers.CommitBlocks(t, client, 1, time.Millisecond*500)

			updateL1InfoTreeAndRollupExitTree := func(i int, rollupID uint32) {
				// Update L1 Info Tree
				_, err := gerSc.UpdateExitRoot(auth, common.HexToHash(strconv.Itoa(i)))
				require.NoError(t, err)

				// Update L1 Info Tree + Rollup Exit Tree
				newLocalExitRoot := common.HexToHash(strconv.Itoa(i) + "ffff" + strconv.Itoa(1))
				_, err = verifySC.VerifyBatchesTrustedAggregator(auth, rollupID, 0, newLocalExitRoot, common.Hash{}, true)
				require.NoError(t, err)

				// Update Rollup Exit Tree
				newLocalExitRoot = common.HexToHash(strconv.Itoa(i) + "ffff" + strconv.Itoa(2))
				_, err = verifySC.VerifyBatchesTrustedAggregator(auth, rollupID, 0, newLocalExitRoot, common.Hash{}, false)
				require.NoError(t, err)
			}

			// create some events and update the trees
			updateL1InfoTreeAndRollupExitTree(1, 1)

			// Commit block 8 that contains the transaction that updates the trees
			helpers.CommitBlocks(t, client, 1, time.Millisecond*500)

			// Make sure syncer is up to date
			helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

			// Assert rollup exit root
			expectedRollupExitRoot, err := verifySC.GetRollupExitRoot(&bind.CallOpts{Pending: false})
			require.NoError(t, err)
			actualRollupExitRoot, err := syncer.GetLastRollupExitRoot(ctx)
			require.NoError(t, err)
			require.Equal(t, common.Hash(expectedRollupExitRoot), actualRollupExitRoot.Hash)

			// Assert L1 Info tree root
			expectedL1InfoRoot, err := gerSc.GetRoot(&bind.CallOpts{Pending: false})
			require.NoError(t, err)
			expectedGER, err := gerSc.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
			require.NoError(t, err)
			actualL1InfoRoot, err := syncer.GetLastL1InfoTreeRoot(ctx)
			require.NoError(t, err)
			info, err := syncer.GetInfoByIndex(ctx, actualL1InfoRoot.Index)
			require.NoError(t, err)

			require.Equal(t, common.Hash(expectedL1InfoRoot), actualL1InfoRoot.Hash)
			require.Equal(t, common.Hash(expectedGER), info.GlobalExitRoot, fmt.Sprintf("%+v", info))

			// Forking from block 6
			// Note: reorged trx will be added to pending transactions
			// and will be committed when the forked block is committed
			err = client.Fork(reorgFrom)
			require.NoError(t, err)

			blockNum, err := client.Client().BlockNumber(ctx)
			log.Infof("Current block number after fork: %d", blockNum)
			require.NoError(t, err)
			require.Equal(t, header.Number.Uint64(), blockNum)

			// Commit block 7, 8, 9 after the fork
			helpers.CommitBlocks(t, client, 5, time.Millisecond*100)

			// Assert rollup exit root after committing new blocks on the fork
			expectedRollupExitRoot, err = verifySC.GetRollupExitRoot(&bind.CallOpts{Pending: false})
			require.NoError(t, err)
			// TODO: Remove ths sleep
			if !tt.useMultidownloaderForTest {
				time.Sleep(time.Second * 1) // wait for syncer to process the reorg
			}
			checkBlocks(t, ctx, client.Client(), evmMultidownloader, 0, 10)

			lastProcessedBlock, err := syncer.GetLastProcessedBlock(ctx)
			require.NoError(t, err)
			log.Infof("Last processed block after reorg: %d", lastProcessedBlock)
			actualRollupExitRoot, err = syncer.GetLastRollupExitRoot(ctx)
			require.NoError(t, err)
			require.Equal(t, common.Hash(expectedRollupExitRoot), actualRollupExitRoot.Hash)

			showLeafs(t, ctx, syncer, "Before second fork: ")

			// Forking from block 6 again
			log.Infof("🖖🖖🖖🖖🖖🖖🖖🖖🖖🖖🖖🖖🖖🖖🖖🖖 Forking again from block (6) %d", reorgFrom.Hex())
			err = client.Fork(reorgFrom)
			require.NoError(t, err)
			time.Sleep(time.Millisecond * 500)
			// wait for syncer to process the reorg
			helpers.CommitBlocks(t, client, 1, time.Millisecond*100) // Commit block 7
			// TODO: Remove ths sleep
			if !tt.useMultidownloaderForTest {
				time.Sleep(time.Second * 1)
			}
			// create some events and update the trees
			updateL1InfoTreeAndRollupExitTree(2, 1)
			helpers.CommitBlocks(t, client, 1, time.Millisecond*100)

			// Make sure syncer is up to date
			helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

			// Assert rollup exit root after the fork
			expectedRollupExitRoot, err = verifySC.GetRollupExitRoot(&bind.CallOpts{Pending: false})
			require.NoError(t, err)
			actualRollupExitRoot, err = syncer.GetLastRollupExitRoot(ctx)
			require.NoError(t, err)
			showLeafs(t, ctx, syncer, "After second fork: ")
			checkBlocks(t, ctx, client.Client(), evmMultidownloader, 0, 10)

			require.Equal(t, common.Hash(expectedRollupExitRoot), actualRollupExitRoot.Hash)
		})
	}
}

func checkBlocks(t *testing.T, ctx context.Context, rawClient simulated.Client, mdr *multidownloader.EVMMultidownloader, fromBlock, toBlock uint64) {
	t.Helper()
	if mdr == nil {
		log.Warn("checkBlocks: multidownloader is nil, skipping block check")
		return
	}
	for i := fromBlock; i <= toBlock; i++ {
		block, errRaw := rawClient.BlockByNumber(ctx, big.NewInt(int64(i)))
		blockMDR, errMDR := mdr.HeaderByNumber(ctx, aggkittypes.NewBlockNumber(i))
		require.Equal(t, errRaw == nil, errMDR == nil, "block number %d: errRaw=%v, errMDR=%v blockMDR=%s", i, errRaw, errMDR, blockMDR.String())
		if errRaw == nil && errMDR == nil {
			require.Equal(t, block.Hash(), blockMDR.Hash, "block number %d", i)
		}
	}
}

func showLeafs(t *testing.T, ctx context.Context, syncer *l1infotreesync.L1InfoTreeSync, prefix string) {
	t.Helper()
	for i := 0; i < 6; i++ {
		leaf, err := syncer.GetInfoByIndex(ctx, uint32(i))
		if err != nil {
			log.Infof(prefix+"Leaf %d: error: %s", i, err.Error())
		} else {
			log.Infof(prefix+"Leaf %d: %+v", i, leaf)
		}
	}
}

func TestStressAndReorgs(t *testing.T) {
	t.Skip("Skipping E2E test, this test is works locally but fails in CI")
	const (
		totalIterations       = 3
		blocksInIteration     = 140
		reorgEveryXIterations = 70
		reorgSizeInBlocks     = 2
		maxRollupID           = 31
	)

	ctx := context.Background()
	dbPathSyncer := path.Join(t.TempDir(), "l1infotreesyncTestStressAndReorgs_sync.sqlite")
	dbPathReorg := path.Join(t.TempDir(), "l1infotreesyncTestStressAndReorgs_reorg.sqlite")

	client, auth, gerAddr, verifyAddr, gerSc, verifySC := newSimulatedClient(t)
	// Start reorg detector
	reorgDetectorCfg := reorgdetector.Config{
		DBPath:              dbPathReorg,
		CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 100),
		FinalizedBlock:      aggkittypes.FinalizedBlock}
	rd, err := reorgdetector.New(etherman.NewDefaultEthClient(client.Client(), nil, nil), reorgDetectorCfg, reorgdetector.L1)
	require.NoError(t, err)
	require.NoError(t, rd.Start(ctx))

	var multidownloaderClient aggkittypes.MultiDownloaderLegacy
	if useMultidownloaderForTests {
		cfgMD := multidownloader.NewConfigDefault("l1", t.TempDir())
		cfgMD.Enabled = true
		multidownloaderClient, err = multidownloader.NewEVMMultidownloader(
			log.WithFields("module", "multidownloader"),
			cfgMD,
			"testMD",
			etherman.NewDefaultEthClient(client.Client(), nil, nil),
			nil, // rpcClient
			nil, // Storage will be created internally
			nil, // blockNotifierManager will be created internally
			nil, // reorgProcessor will be created internally
		)
		require.NoError(t, err)
	} else {
		multidownloaderClient = sync.NewAdapterEthClientToMultidownloader(etherman.NewDefaultEthClient(client.Client(), nil, nil))
	}

	cfg := l1infotreesync.Config{
		DBPath:                             dbPathSyncer,
		InitialBlock:                       0,
		SyncBlockChunkSize:                 10,
		BlockFinality:                      aggkittypes.LatestBlock,
		GlobalExitRootAddr:                 gerAddr,
		RollupManagerAddr:                  verifyAddr,
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(time.Second * 1),
		MaxRetryAttemptsAfterError:         25,
		RequireStorageContentCompatibility: true,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Millisecond * 100),
	}
	syncer, err := l1infotreesync.NewLegacy(ctx, cfg, multidownloaderClient, rd, l1infotreesync.FlagAllowWrongContractsAddrs)
	require.NoError(t, err)
	go syncer.Start(ctx)

	updateL1InfoTreeAndRollupExitTree := func(i, j int, rollupID uint32) {
		// Update L1 Info Tree
		_, err := gerSc.UpdateExitRoot(auth, common.HexToHash(strconv.Itoa(i)))
		require.NoError(t, err)

		// Update L1 Info Tree + Rollup Exit Tree
		newLocalExitRoot := common.HexToHash(strconv.Itoa(i) + "ffff" + strconv.Itoa(j))
		_, err = verifySC.VerifyBatches(auth, rollupID, 0, newLocalExitRoot, common.Hash{}, true)
		require.NoError(t, err)

		// Update Rollup Exit Tree
		newLocalExitRoot = common.HexToHash(strconv.Itoa(i) + "fffa" + strconv.Itoa(j))
		_, err = verifySC.VerifyBatches(auth, rollupID, 0, newLocalExitRoot, common.Hash{}, false)
		require.NoError(t, err)
	}

	for i := 1; i <= totalIterations; i++ {
		for j := 1; j <= blocksInIteration; j++ {
			helpers.CommitBlocks(t, client, 1, time.Millisecond*10)
			if j%reorgEveryXIterations == 0 {
				helpers.Reorg(t, client, reorgSizeInBlocks)
			} else {
				updateL1InfoTreeAndRollupExitTree(i, j, uint32(j%maxRollupID)+1)
			}
		}
	}

	helpers.CommitBlocks(t, client, 2, time.Millisecond*100)
	helpers.WaitForSyncerToCatchUp(ctx, t, syncer, client)

	// Assert L1 Info tree root
	expectedL1InfoRoot, err := gerSc.GetRoot(&bind.CallOpts{Pending: false})
	require.NoError(t, err)
	expectedGER, err := gerSc.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
	require.NoError(t, err)
	lastRoot, err := syncer.GetLastL1InfoTreeRoot(ctx)
	require.NoError(t, err)
	info, err := syncer.GetInfoByIndex(ctx, lastRoot.Index)
	require.NoError(t, err, fmt.Sprintf("index: %d", lastRoot.Index))

	require.Equal(t, common.Hash(expectedGER), info.GlobalExitRoot, fmt.Sprintf("%+v", info))
	require.Equal(t, common.Hash(expectedL1InfoRoot), lastRoot.Hash)
}
