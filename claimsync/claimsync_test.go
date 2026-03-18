package claimsync

import (
	"context"
	"math/big"
	"path"
	"testing"
	"time"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/test/contracts/claimmock"
	tree "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestClaimSyncerWaitUntilSetNextRequiredBlock verifies the deferred start behavior of ClaimSyncer:
// it must not begin syncing until an explicit starting block is provided via SetNextRequiredBlock.
//
// Steps:
//  1. Spin up a local Geth node and deploy a Claimmock contract simulating the bridge.
//  2. Create a ClaimSyncer and start it in a goroutine.
//  3. Emit a ClaimAsset event on-chain and wait for the tx receipt.
//  4. Assert GetLastProcessedBlock returns found=false — the syncer is idle, waiting for a start signal.
//  5. Call SetNextRequiredBlock(ctx, 1) to unlock the syncer.
//  6. Assert GetLastProcessedBlock returns found=true — the syncer processed the blocks and captured the event.
func TestClaimSyncerWaitUntilSetNextRequiredBlock(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
	ctx, cancelFn := context.WithCancel(context.Background())
	// Setup Docker L1
	client, auth := startGeth(t, ctx, cancelFn)
	// Deploy contracts
	bridgeAddr, deployTx, bridgeContract, err := claimmock.DeployClaimmock(auth, client)
	require.NoError(t, err)
	_, err = waitForReceipt(ctx, client, deployTx.Hash(), 10)
	require.NoError(t, err)
	log.Infof("*** Deployed fake bridge contract %s", bridgeAddr.Hex())
	dbPathSyncer := path.Join(t.TempDir(), "claimsyncer.sqlite")

	cfg := ConfigStandalone{
		DBPath:                             dbPathSyncer,
		BlockFinality:                      aggkittypes.LatestBlock,
		InitialBlockNum:                    0,
		SyncBlockChunkSize:                 100,
		RetryAfterErrorPeriod:              configtypes.NewDuration(time.Millisecond * 100),
		WaitForNewBlocksPeriod:             configtypes.NewDuration(time.Millisecond * 100),
		RequireStorageContentCompatibility: true,
		ConfigEmbedded: ConfigEmbedded{
			DBQueryTimeout: configtypes.NewDuration(5 * time.Second),
			BridgeAddr:     bridgeAddr,
		},
		AutoStart: configtypes.FalseMode,
	}
	logger := log.WithFields("test", "TestClaimSync")
	reorgDetector, err := reorgdetector.New(client, reorgdetector.Config{
		DBPath:              path.Join(t.TempDir(), "reorgdetector.sqlite"),
		CheckReorgsInterval: configtypes.NewDuration(30 * time.Second),
		FinalizedBlock:      aggkittypes.LatestBlock,
	}, reorgdetector.L1)
	require.NoError(t, err)
	claimSyncer, err := NewClaimSync(ctx, cfg, reorgDetector, client, 0, claimsynctypes.L1ClaimSyncer, logger)
	require.NoError(t, err)
	go claimSyncer.Start(ctx)
	globalIndex := big.NewInt(1)
	mainnetExitRoot := common.HexToHash("beef")
	rollupExitRoot := common.HexToHash("dead")
	tx, err := bridgeContract.ClaimAsset(
		auth,
		[tree.DefaultHeight][common.HashLength]byte{}, // proofLocal
		[tree.DefaultHeight][common.HashLength]byte{}, // proofRollup
		globalIndex,
		mainnetExitRoot,
		rollupExitRoot,
		uint32(0),          // originNetwork
		common.Address{},   // originTokenAddress/originAddress
		uint32(0),          // destinationNetwork
		common.Address{},   // destinationAddress
		big.NewInt(0),      // amount
		[]byte("metadata"), // metadata
	)
	require.NoError(t, err)

	txReceipt, err := waitForReceipt(ctx, client, tx.Hash(), 10)
	require.NoError(t, err)
	waitTillBlockNumber := txReceipt.BlockNumber.Uint64()
	logger.Info("*** ClaimSyncer must be waiting to receive the starting point")
	_, found, err2 := claimSyncer.GetLastProcessedBlock(ctx)
	require.NoError(t, err2)
	require.False(t, found)
	logger.Info("*** Setting next required block to 1, so must starting syncing and sync the ClaimAsset")
	err = claimSyncer.SetNextRequiredBlock(ctx, 0)
	require.NoError(t, err)
	for i := 0; i < 10; i++ {
		currentBlockNumber, _, err := claimSyncer.GetLastProcessedBlock(ctx)
		require.NoError(t, err)
		logger.Infof("*** Wait for block %d, current %d", waitTillBlockNumber, currentBlockNumber)
		if currentBlockNumber >= waitTillBlockNumber {
			break
		}
		time.Sleep(time.Second)
	}
	lastBlockProcessed, found, err2 := claimSyncer.GetLastProcessedBlock(ctx)
	require.NoError(t, err2)
	require.True(t, found)
	require.GreaterOrEqual(t, lastBlockProcessed, waitTillBlockNumber)
	logger.Infof("*** Last block processed: %d", lastBlockProcessed)
	claims, err := claimSyncer.GetClaims(ctx, 0, lastBlockProcessed)
	require.NoError(t, err)
	logger.Infof("*** Claims retrieved: %v", claims)
	require.Equal(t, 1, len(claims))
}
