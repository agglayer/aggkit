package claimer

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// L1 Info Tree syncer timing defaults, mirroring aggkit's [L1InfoTreeSync] config defaults. They
// must be non-zero: a zero WaitForNewBlocksPeriod makes the downloader's time.NewTicker panic.
const (
	defaultWaitForNewBlocksPeriod     = 100 * time.Millisecond
	defaultRetryAfterErrorPeriod      = time.Second
	defaultMaxRetryAttemptsAfterError = -1
)

// L1InfoTreeQuerier is the subset of the l1infotreesync API the claimer needs to assemble the
// rollup-side claimAsset parameters. *l1infotreesync.L1InfoTreeSync satisfies it.
type L1InfoTreeQuerier interface {
	GetLatestL1InfoLeaf(ctx context.Context) (*l1infotreesync.L1InfoTreeLeaf, error)
	GetLocalExitRoot(ctx context.Context, networkID uint32, rollupExitRoot common.Hash) (common.Hash, error)
	GetRollupExitTreeMerkleProof(ctx context.Context, networkID uint32, root common.Hash) (treetypes.Proof, error)
}

// OpenL1InfoTree opens the L1 Info Tree syncer. In read-only mode it just attaches to the existing
// SQLite database. When cfg.Enabled is set it dials L1, wires a multidownloader-based syncer, and
// starts both in the background so the database keeps up with L1 (the same DB is then queried).
func OpenL1InfoTree(
	ctx context.Context, cfg L1SyncConfig, dbPath string, logger *log.Logger,
) (*l1infotreesync.L1InfoTreeSync, error) {
	if !cfg.Enabled {
		syncer, err := l1infotreesync.NewReadOnly(ctx, dbPath)
		if err != nil {
			return nil, fmt.Errorf("opening read-only L1 info tree at %q: %w", dbPath, err)
		}
		return syncer, nil
	}

	finality := aggkittypes.LatestBlock
	if cfg.BlockFinality != "" {
		f, err := aggkittypes.NewBlockNumberFinality(cfg.BlockFinality)
		if err != nil {
			return nil, fmt.Errorf("invalid l1Sync.blockFinality %q: %w", cfg.BlockFinality, err)
		}
		finality = *f
	}

	l1Client, err := etherman.NewRPCClient(ctx, logger, ethermanconfig.RPCClientConfig{
		URL:  cfg.RPCURL,
		Mode: ethermanconfig.RPCModeBasic,
	})
	if err != nil {
		return nil, fmt.Errorf("dialing L1 RPC %q: %w", cfg.RPCURL, err)
	}

	// The multidownloader keeps its own storage and reorg processor next to the L1 Info Tree DB,
	// so no separate reorg detector is needed.
	mdCfg := multidownloader.NewConfigDefault("l1infotree", filepath.Dir(dbPath))
	mdCfg.BlockFinality = finality
	if cfg.SyncBlockChunkSize > 0 {
		mdCfg.BlockChunkSize = uint32(cfg.SyncBlockChunkSize)
	}

	l1MultiDownloader, err := multidownloader.NewEVMMultidownloader(
		logger, mdCfg, "l1",
		l1Client, // ethClient
		l1Client, // rpcClient
		nil,      // storage (created inside the multidownloader)
		nil,      // blockNotifierManager (created inside the multidownloader)
		nil,      // reorgProcessor (created inside the multidownloader)
	)
	if err != nil {
		return nil, fmt.Errorf("creating L1 multidownloader: %w", err)
	}

	syncer, err := l1infotreesync.NewMultidownloadBased(
		ctx,
		l1infotreesync.Config{
			DBPath:                     dbPath,
			GlobalExitRootAddr:         common.HexToAddress(cfg.GlobalExitRootAddr),
			RollupManagerAddr:          common.HexToAddress(cfg.RollupManagerAddr),
			BlockFinality:              finality,
			SyncBlockChunkSize:         cfg.SyncBlockChunkSize,
			InitialBlock:               cfg.InitialBlock,
			WaitForNewBlocksPeriod:     configtypes.Duration{Duration: defaultWaitForNewBlocksPeriod},
			RetryAfterErrorPeriod:      configtypes.Duration{Duration: defaultRetryAfterErrorPeriod},
			MaxRetryAttemptsAfterError: defaultMaxRetryAttemptsAfterError,
		},
		l1MultiDownloader,
		l1infotreesync.FlagNone,
	)
	if err != nil {
		return nil, fmt.Errorf("creating L1 info tree syncer: %w", err)
	}

	// Initialize must run after NewMultidownloadBased has registered the syncer.
	if err := l1MultiDownloader.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("initializing L1 multidownloader: %w", err)
	}
	go func() {
		if startErr := l1MultiDownloader.Start(ctx); startErr != nil && ctx.Err() == nil {
			logger.Errorf("L1 multidownloader stopped: %v", startErr)
		}
	}()
	go syncer.Start(ctx)

	return syncer, nil
}
