package claimer

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/agglayer/aggkit/log"
	"github.com/urfave/cli/v2"
)

// Run is the urfave/cli action entry point: it initializes logging and runs the service. With
// --log-json a fatal error is emitted through the logger (keeping the whole output machine
// parseable); otherwise it is returned so main prints the plain "Error: ..." line.
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
		if c.Bool("log-json") {
			logger.Error(err)
			return cli.Exit("", 1)
		}
		return err
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

	cert, err := LoadCertificate(cfg.SignedCertificatePath)
	if err != nil {
		return err
	}
	logger.Infof("loaded certificate: network %d, %d bridge exits, new local exit root %s",
		cert.NetworkID, len(cert.Leaves), cert.NewLocalExitRoot.Hex())

	waitResult, err := LoadStepWaitResult(cfg.StepWaitResultPath)
	if err != nil {
		return err
	}
	logger.Infof("loaded wait result: certificate %s settled (status %s)",
		waitResult.CertificateHash.Hex(), waitResult.FinalStatus)

	settlementGER, err := SettlementGER(waitResult)
	if err != nil {
		return err
	}

	localTree, err := OpenLocalExitTree(ctx, cfg.LocalExitTreeDBPath, logger)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := localTree.Close(); closeErr != nil {
			logger.Warnf("closing local exit tree: %v", closeErr)
		}
	}()

	l1, err := OpenL1InfoTree(ctx, cfg.L1Sync, cfg.L1InfoTreeDBPath, settlementGER, logger)
	if err != nil {
		return err
	}

	claimer := NewClaimer(logger, cert, localTree, l1, cfg.NetworkID, waitResult)
	if err := claimer.Check(ctx); err != nil {
		return fmt.Errorf("claimer check: %w", err)
	}
	server := NewServer(cfg, claimer, logger)

	if err := server.Start(ctx); err != nil {
		return fmt.Errorf("server stopped: %w", err)
	}
	return nil
}
