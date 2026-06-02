package exit_certificate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

const (
	// bridgeSyncChunkSize is the number of blocks bridgesync queries per eth_getLogs request.
	bridgeSyncChunkSize = 10000
	// bridgeSyncCatchUpPollPeriod is how often we poll bridgesync's last processed block while
	// waiting for it to catch up to the shadow-fork head.
	bridgeSyncCatchUpPollPeriod = 250 * time.Millisecond
	// bridgeSyncCatchUpTimeout bounds how long we wait for bridgesync to finish syncing.
	bridgeSyncCatchUpTimeout = 10 * time.Minute
)

// syncShadowForkBridges spins up an L2 bridgesync syncer against the Anvil shadow-fork at rpcURL,
// syncs every L2 bridge from genesis up to the fork head, and returns the bridges ordered by
// DepositCount.
//
// It reuses the production bridgesync component so bridge event parsing and exit-tree semantics
// match the live node exactly. All syncer state (sqlite DBs for the syncer and the reorg detector)
// lives in a temp dir that is removed before returning, and the background goroutines are stopped
// via a derived context on return.
func syncShadowForkBridges(
	ctx context.Context, rpcURL string, bridgeAddr common.Address, originNetwork uint32,
) ([]shadowForkBridge, error) {
	tmpDir, err := os.MkdirTemp("", "exit-cert-bridgesync-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir for bridgesync: %w", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
			log.Warnf("failed to remove bridgesync temp dir %s: %v", tmpDir, rmErr)
		}
	}()

	// syncCtx lets us stop the background syncer/reorg-detector goroutines when we return.
	syncCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	logger := log.WithFields("module", "exit-cert-bridgesync")

	rpcCfg := *ethermanconfig.NewDefaultRPCClientConfig()
	rpcCfg.URL = rpcURL
	rpcCfg.Mode = ethermanconfig.RPCModeBasic
	ethClient, err := etherman.NewRPCClient(syncCtx, logger, rpcCfg)
	if err != nil {
		return nil, fmt.Errorf("create RPC client for bridgesync: %w", err)
	}

	// FinalizedBlock=LatestBlock disables reorg detection — the shadow-fork never reorgs.
	rd, err := reorgdetector.New(ethClient, reorgdetector.Config{
		DBPath:              filepath.Join(tmpDir, "reorg.sqlite"),
		CheckReorgsInterval: cfgtypes.NewDuration(time.Second),
		FinalizedBlock:      aggkittypes.LatestBlock,
	}, reorgdetector.L2)
	if err != nil {
		return nil, fmt.Errorf("create reorg detector: %w", err)
	}
	if err := rd.Start(syncCtx); err != nil {
		return nil, fmt.Errorf("start reorg detector: %w", err)
	}

	syncCfg := bridgesync.Config{
		DBPath:                             filepath.Join(tmpDir, "bridgesync.sqlite"),
		BridgeAddr:                         bridgeAddr,
		BlockFinality:                      aggkittypes.LatestBlock,
		InitialBlockNum:                    0,
		SyncBlockChunkSize:                 bridgeSyncChunkSize,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Second),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(500 * time.Millisecond), //nolint:mnd
		MaxRetryAttemptsAfterError:         10,                                           //nolint:mnd
		RequireStorageContentCompatibility: false,
		DBQueryTimeout:                     cfgtypes.NewDuration(30 * time.Second), //nolint:mnd
	}

	syncer, err := bridgesync.NewL2(
		syncCtx, syncCfg, rd, ethClient, originNetwork, false, bridgesynctypes.EmptyLER,
	)
	if err != nil {
		return nil, fmt.Errorf("create L2 bridgesync: %w", err)
	}
	go syncer.Start(syncCtx)

	if err := waitForBridgeSyncCatchUp(syncCtx, ethClient, syncer); err != nil {
		return nil, err
	}

	lastProcessed, ok, err := syncer.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, fmt.Errorf("get last processed block: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("bridgesync reported no processed block")
	}

	bridges, err := syncer.GetBridges(ctx, 0, lastProcessed)
	if err != nil {
		return nil, fmt.Errorf("get bridges: %w", err)
	}

	out := make([]shadowForkBridge, 0, len(bridges))
	for _, b := range bridges {
		out = append(out, shadowForkBridge{
			BlockNum:           b.BlockNum,
			OriginNetwork:      b.OriginNetwork,
			OriginAddress:      b.OriginAddress,
			DestinationNetwork: b.DestinationNetwork,
			DestinationAddress: b.DestinationAddress,
			Amount:             b.Amount,
			DepositCount:       b.DepositCount,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DepositCount < out[j].DepositCount })
	log.Infof("bridgesync synced %d L2 bridges up to block %d", len(out), lastProcessed)
	return out, nil
}

// waitForBridgeSyncCatchUp blocks until bridgesync's last processed block reaches the current
// shadow-fork head, or the timeout elapses. The fork head is fixed (no new blocks are mined after
// the replay), so once the syncer reaches it there is nothing left to process.
func waitForBridgeSyncCatchUp(
	ctx context.Context, ethClient aggkittypes.EthClienter, syncer *bridgesync.BridgeSync,
) error {
	head, err := ethClient.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("read fork head block: %w", err)
	}
	deadline := time.Now().Add(bridgeSyncCatchUpTimeout)
	for {
		lastProcessed, _, err := syncer.GetLastProcessedBlock(ctx)
		if err != nil {
			return fmt.Errorf("poll last processed block: %w", err)
		}
		if lastProcessed >= head {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("bridgesync did not catch up to block %d (stuck at %d) after %s",
				head, lastProcessed, bridgeSyncCatchUpTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(bridgeSyncCatchUpPollPeriod):
		}
	}
}
