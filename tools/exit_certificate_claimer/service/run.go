package claimer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v2"
)

// dirPermissions is the mode of the directories the claimer creates.
const dirPermissions = 0o755

// Run is the urfave/cli action entry point: it initializes logging and runs the service. A fatal
// error is emitted through the configured logger — honoring --log-json — rather than returned to
// urfave/cli's plain-text error handling.
func Run(c *cli.Context) error {
	logLevel := "info"
	if c.Bool("verbose") {
		logLevel = "debug"
	}
	logEnvironment := log.EnvironmentDevelopment
	if c.Bool("log-json") {
		logEnvironment = log.EnvironmentProduction
	}
	log.Init(log.Config{
		Environment: logEnvironment,
		Level:       logLevel,
		Outputs:     []string{"stderr"},
	})
	logger := log.WithFields("module", "exit-certificate-claimer")

	if err := run(c, logger); err != nil {
		logger.Error(err)
		return cli.Exit("", 1)
	}
	return nil
}

// run loads the config, opens the data sources, and runs the HTTP server until interrupted.
func run(c *cli.Context, logger *log.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := loadOrDeriveConfig(ctx, c, logger)
	if err != nil {
		return err
	}
	if cfg.RunSource != "" {
		if err := FetchRunSource(ctx, cfg, logger); err != nil {
			return err
		}
		// The L1 Info Tree DB stays local: make sure its directory exists on a fresh machine.
		if err := os.MkdirAll(filepath.Dir(cfg.L1InfoTreeDBPath), dirPermissions); err != nil {
			return fmt.Errorf("create the directory of l1InfoTreeDBPath: %w", err)
		}
	}

	files := cfg.servedRun
	if files == nil { // local files
		files = &runFiles{
			Certificate: cfg.SignedCertificatePath, LocalExitTreeDB: cfg.LocalExitTreeDBPath,
			StepWaitResult: cfg.StepWaitResultPath, PendingBridges: cfg.PendingBridgesPath,
		}
	}
	openL1 := func(ctx context.Context, settlementGER common.Hash) (L1InfoTreeQuerier, error) {
		return OpenL1InfoTree(ctx, cfg.L1Sync, cfg.L1InfoTreeDBPath, settlementGER, logger)
	}
	initial, err := loadRun(ctx, cfg, files, openL1, logger)
	if err != nil {
		return err
	}
	server := newServer(cfg, initial, logger)
	defer server.runs.close()

	if cfg.FollowIntervalSeconds > 0 {
		follow, err := newFollower(cfg, server, openL1, runarchive.Open, logger)
		if err != nil {
			return err
		}
		server.follow = follow
		go follow.Run(ctx)
	}

	if err := server.Start(ctx); err != nil {
		return fmt.Errorf("server stopped: %w", err)
	}
	return nil
}

// l1Opener returns the L1 Info Tree, synced until the settlement GER of a run is indexed (OpenL1InfoTree).
type l1Opener func(ctx context.Context, settlementGER common.Hash) (L1InfoTreeQuerier, error)

// errL1Sync marks a loadRun failure of the L1 Info Tree stage.
var errL1Sync = errors.New("L1 info tree")

// loadRun loads the files of a run, makes sure its settlement is indexed in the L1 Info Tree (syncing it
// from L1 if needed) and builds the claimer that serves it, checked against that settlement. On failure
// nothing is left open.
func loadRun(
	ctx context.Context, cfg *Config, files *runFiles, openL1 l1Opener, logger *log.Logger,
) (*servedRun, error) {
	cert, err := LoadCertificate(files.Certificate)
	if err != nil {
		return nil, err
	}
	logger.Infof("loaded certificate: network %d, height %d, %d bridge exits, new local exit root %s",
		cert.NetworkID, cert.Height, len(cert.Leaves), cert.NewLocalExitRoot.Hex())

	waitResult, err := LoadStepWaitResult(files.StepWaitResult)
	if err != nil {
		return nil, err
	}
	logger.Infof("loaded wait result: certificate %s settled (status %s)",
		waitResult.CertificateHash.Hex(), waitResult.FinalStatus)

	settlementGER, err := SettlementGER(waitResult)
	if err != nil {
		return nil, err
	}

	localTree, err := OpenLocalExitTree(ctx, files.LocalExitTreeDB, logger)
	if err != nil {
		return nil, err
	}
	closeTree := func() {
		if closeErr := localTree.Close(); closeErr != nil {
			logger.Warnf("closing local exit tree: %v", closeErr)
		}
	}

	l1, err := openL1(ctx, settlementGER)
	if err != nil {
		closeTree()
		return nil, fmt.Errorf("%w: %w", errL1Sync, err)
	}

	claimer := NewClaimer(logger, cert, localTree, l1, cfg.NetworkID, waitResult)
	if files.PendingBridges != "" {
		pending, err := LoadPendingBridges(files.PendingBridges)
		if err != nil {
			closeTree()
			return nil, err
		}
		logger.Infof("loaded %d pending pre-certificate bridges from %s", len(pending), files.PendingBridges)
		claimer.WithPendingBridges(pending)
	}
	if _, err := claimer.WithMessageRoutes(cfg.MessageRoutes); err != nil {
		closeTree()
		return nil, err
	}
	logger.Infof("message routes configured: %d", len(cfg.MessageRoutes))
	if err := claimer.Check(ctx); err != nil {
		closeTree()
		return nil, fmt.Errorf("claimer check: %w", err)
	}

	info := &RunInfo{
		Height: cert.Height, CertificateHash: waitResult.CertificateHash.Hex(), DataVersion: files.DataVersion,
	}
	if files.Source != nil {
		info.Source = files.Source.String()
	}
	return &servedRun{claimer: claimer, info: info, files: files, release: closeTree}, nil
}
