package force_ger_update

import (
	"context"
	"fmt"
	"time"

	aggkit "github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/urfave/cli/v2"
)

// dialTimeout bounds how long Run waits to establish and validate the L1 connection on startup.
const dialTimeout = 10 * time.Second

// Run is the CLI entrypoint for the force_ger_update tool.
//
// S1 scope: load and validate the configuration, dial L1, and log a startup summary. The GER
// monitor, forced-update sender, ethtxmanager wiring and the main timer loop are implemented in
// later steps (S2-S4) — this stub intentionally does not loop.
func Run(c *cli.Context) error {
	cfg, err := LoadConfig(c)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := cfg.ForceGERUpdate.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	dialCtx, cancel := context.WithTimeout(c.Context, dialTimeout)
	defer cancel()

	l1Client, err := ethclient.DialContext(dialCtx, cfg.ForceGERUpdate.L1URL)
	if err != nil {
		return fmt.Errorf("dial L1 (%s): %w", cfg.ForceGERUpdate.L1URL, err)
	}
	defer l1Client.Close()

	chainID, err := l1Client.ChainID(dialCtx)
	if err != nil {
		return fmt.Errorf("fetch L1 chain ID from %s: %w", cfg.ForceGERUpdate.L1URL, err)
	}

	logStartupSummary(cfg, chainID.String())

	// TODO(S2): GERMonitor implementation (boot fetch + watch/poll).
	// TODO(S3): ForcedUpdateSender implementation (bridgeMessage via ethtxmanager).
	// TODO(S4): ethtxmanager wiring + main timer loop + graceful shutdown.
	return nil
}

// logStartupSummary logs a config summary (no secrets) on boot.
func logStartupSummary(cfg *Config, l1ChainID string) {
	fgu := cfg.ForceGERUpdate
	watchMode := "poll"
	if fgu.L1WSURL != "" {
		watchMode = "watch"
	}

	log.Infof(
		"force_ger_update %s starting: L1URL=%s L1ChainID=%s watchMode=%s "+
			"GlobalExitRootManagerAddr=%s BridgeAddr=%s MaxTimeWithoutGERUpdate=%s CheckInterval=%s "+
			"EventPollInterval=%s InitialLookbackBlocks=%d FilterLogsChunkSize=%d DestinationNetwork=%d "+
			"DestinationAddress=%s DryRun=%t",
		aggkit.Version,
		fgu.L1URL,
		l1ChainID,
		watchMode,
		fgu.GlobalExitRootManagerAddr,
		fgu.BridgeAddr,
		fgu.MaxTimeWithoutGERUpdate.Duration,
		fgu.CheckInterval.Duration,
		fgu.EventPollInterval.Duration,
		fgu.InitialLookbackBlocks,
		fgu.FilterLogsChunkSize,
		fgu.DestinationNetwork,
		fgu.DestinationAddress,
		fgu.DryRun,
	)
}
