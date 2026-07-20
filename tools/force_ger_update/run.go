package force_ger_update

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	aggkit "github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/urfave/cli/v2"
)

// dialTimeout bounds how long Run waits to establish and validate each L1 connection (HTTP, and
// WS when configured) on startup.
const dialTimeout = 10 * time.Second

// Run is the CLI entrypoint for the force_ger_update tool: it loads and validates the
// configuration, dials L1 (HTTP, and WS when L1WSURL is set), starts the ethtxmanager, builds the
// GER monitor and forced-update sender, and runs the main timer loop until interrupted
// (SIGINT/SIGTERM) or a fatal setup error occurs.
func Run(c *cli.Context) error {
	cfg, err := LoadConfig(c)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := cfg.ForceGERUpdate.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	fgu := cfg.ForceGERUpdate

	ctx, stop := signal.NotifyContext(c.Context, os.Interrupt, syscall.SIGTERM)
	defer stop()

	dialCtx, cancelDial := context.WithTimeout(ctx, dialTimeout)
	defer cancelDial()

	l1Client, l1ChainID, err := dialL1(dialCtx, fgu.L1URL)
	if err != nil {
		return fmt.Errorf("dial L1 (%s): %w", fgu.L1URL, err)
	}

	var wsClient aggkittypes.BaseEthereumClienter
	if fgu.L1WSURL != "" {
		wsClient, _, err = dialL1(dialCtx, fgu.L1WSURL)
		if err != nil {
			return fmt.Errorf("dial L1 websocket (%s): %w", fgu.L1WSURL, err)
		}
	}

	ethTxManager, err := ethtxmanager.New(fgu.EthTxManager)
	if err != nil {
		return fmt.Errorf("create ethtxmanager: %w", err)
	}
	go ethTxManager.Start()
	defer ethTxManager.Stop()

	monitor, err := NewMonitor(fgu, l1Client, wsClient)
	if err != nil {
		return fmt.Errorf("create GER monitor: %w", err)
	}

	sender, err := NewSender(fgu, ethTxManager)
	if err != nil {
		return fmt.Errorf("create forced-update sender: %w", err)
	}

	lastGERUpdate, err := monitor.LastGERUpdate()
	if err != nil {
		return fmt.Errorf("boot scan for last GER update: %w", err)
	}

	logStartupSummary(cfg, l1ChainID, ethTxManager.From(), lastGERUpdate)

	return runLoop(ctx, monitor, sender, lastGERUpdate, fgu.CheckInterval.Duration, fgu.MaxTimeWithoutGERUpdate.Duration)
}

// dialL1 dials an Ethereum JSON-RPC endpoint (HTTP or WS, depending on url's scheme) and wraps it
// so it satisfies aggkittypes.BaseEthereumClienter (as required by NewMonitor). Calling ChainID
// right away doubles as a cheap connectivity check: for an HTTP endpoint, DialContext itself never
// touches the network, so an unreachable node is only detected here.
func dialL1(ctx context.Context, url string) (aggkittypes.BaseEthereumClienter, *big.Int, error) {
	raw, err := ethclient.DialContext(ctx, url)
	if err != nil {
		return nil, nil, fmt.Errorf("dial: %w", err)
	}

	client := etherman.NewDefaultEthClient(raw, raw.Client(), nil)

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch chain ID: %w", err)
	}

	return client, chainID, nil
}

// runLoop is the main timer loop, factored out of Run so it can be driven directly by tests with
// mocked/hand-written GERMonitor and ForcedUpdateSender implementations (no real network,
// ethtxmanager, or clients involved).
//
// It starts monitor.Start(ctx) to observe UpdateL1InfoTree events (each one resets lastGERUpdate to
// the event's block timestamp — the single source of truth for "when was the GER last updated");
// every checkInterval it computes elapsed = time.Now() - lastGERUpdate and, when elapsed is at
// least maxTimeWithoutGERUpdate and no forced update is currently in flight, triggers
// sender.SendForcedGERUpdate in a background goroutine. Because lastGERUpdate starts at the
// zero time.Time when the boot scan found nothing (see GERMonitor.LastGERUpdate), elapsed is
// enormous in that case, so the very first tick fires a send without any special-casing.
//
// SendForcedGERUpdate blocks until the transaction reaches a terminal status (or ctx is
// cancelled), so the in-flight guard here is what prevents a second send from being started while
// one is still pending; the timer itself is only ever reset by an observed monitor event, never by
// the send completing.
//
// runLoop returns nil once ctx is cancelled, after every goroutine it started has finished.
func runLoop(
	ctx context.Context,
	monitor GERMonitor,
	sender ForcedUpdateSender,
	lastGERUpdate time.Time,
	checkInterval time.Duration,
	maxTimeWithoutGERUpdate time.Duration,
) error {
	events, err := monitor.Start(ctx)
	if err != nil {
		return fmt.Errorf("start GER monitor: %w", err)
	}

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	var (
		wg       sync.WaitGroup
		inFlight atomic.Bool
	)
	defer wg.Wait()

	triggerSend := func() {
		// CompareAndSwap(false, true) atomically checks-and-sets: exactly one caller can win this
		// race per in-flight window, so concurrent/rapid ticks can never launch two sends at once.
		if !inFlight.CompareAndSwap(false, true) {
			log.Debugf("force_ger_update: forced update already in flight, skipping")
			return
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer inFlight.Store(false)

			log.Infof("force_ger_update: elapsed time since last GER update exceeded threshold, " +
				"sending forced GER update")
			if err := sender.SendForcedGERUpdate(ctx); err != nil {
				log.Errorf("force_ger_update: forced GER update failed: %v", err)
			}
		}()
	}

	for {
		select {
		case <-ctx.Done():
			return nil

		case ev, ok := <-events:
			if !ok {
				// The monitor channel is closed once (and only once) ctx is cancelled; nil it out
				// so this case is never selected again (a closed channel would otherwise be ready
				// forever and busy-spin the loop until the ctx.Done() case is also selected).
				events = nil
				continue
			}
			log.Infof("force_ger_update: observed UpdateL1InfoTree at block %d (timestamp %s), "+
				"resetting timer", ev.BlockNumber, ev.BlockTimestamp)
			lastGERUpdate = ev.BlockTimestamp

		case <-ticker.C:
			elapsed := time.Since(lastGERUpdate)
			if elapsed >= maxTimeWithoutGERUpdate {
				triggerSend()
			}
		}
	}
}

// logStartupSummary logs a config summary (no secrets), the sender address, and the boot-derived
// last GER update time and its age.
func logStartupSummary(cfg *Config, l1ChainID *big.Int, senderAddr common.Address, lastGERUpdate time.Time) {
	fgu := cfg.ForceGERUpdate
	watchMode := "poll"
	if fgu.L1WSURL != "" {
		watchMode = "watch"
	}

	lastGERUpdateDesc := "never observed within lookback window (treated as stale)"
	age := "infinite"
	if !lastGERUpdate.IsZero() {
		lastGERUpdateDesc = lastGERUpdate.Format(time.RFC3339)
		age = time.Since(lastGERUpdate).String()
	}

	log.Infof(
		"force_ger_update %s starting: senderAddress=%s L1URL=%s L1ChainID=%s watchMode=%s "+
			"GlobalExitRootManagerAddr=%s BridgeAddr=%s MaxTimeWithoutGERUpdate=%s CheckInterval=%s "+
			"EventPollInterval=%s InitialLookbackBlocks=%d FilterLogsChunkSize=%d DestinationNetwork=%d "+
			"DestinationAddress=%s DryRun=%t lastGERUpdate=%s lastGERUpdateAge=%s",
		aggkit.Version,
		senderAddr,
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
		lastGERUpdateDesc,
		age,
	)
}
