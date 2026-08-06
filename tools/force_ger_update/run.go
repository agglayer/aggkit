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
// the event's block timestamp — one of the two ways "when was the GER last updated" gets reset, the
// other being our own confirmed send, see below); every checkInterval it computes
// elapsed = time.Now() - lastGERUpdate and, when elapsed is at least maxTimeWithoutGERUpdate and no
// forced update is currently in flight, triggers sender.SendForcedGERUpdate in a background
// goroutine. Because lastGERUpdate starts at the zero time.Time when the boot scan found nothing
// (see GERMonitor.LastGERUpdate), elapsed is enormous in that case, so the very first tick fires a
// send without any special-casing.
//
// SendForcedGERUpdate blocks until the transaction reaches a terminal status (or ctx is
// cancelled); the goroutine reports its outcome on sendDone rather than clearing the in-flight
// guard itself. The main select loop below is the only place that ever clears it — and it always
// does so in the same iteration where it applies a genuine confirmation's confirmedAt to
// lastGERUpdate, never before. That ordering (reset timer, THEN clear the guard, both in one
// single-threaded step) is what makes this race-free: a later ticker tick can only ever observe
// inFlight == false after lastGERUpdate has already been advanced, so it can never fire a
// redundant second send off a stale timer. This replaced an earlier version where the goroutine
// cleared the guard itself immediately after SendForcedGERUpdate returned, relying solely on the
// GERMonitor to independently observe the resulting UpdateL1InfoTree event to reset the timer:
// the guard could drop as soon as the ethtxmanager reported Mined, which can happen before the
// monitor's own poll/watch cycle has scanned the block containing that event, so a checkInterval
// tick landing in that gap still saw the old (pre-reset) lastGERUpdate and legitimately fired a
// second, redundant send — observed in production as occasional back-to-back bridgeMessage
// transactions. A later monitor event for the same tx is still consumed as usual; it just
// re-confirms a lastGERUpdate that was already reset (neither reset path below ever moves the
// timer backwards).
//
// Timer semantics / clock skew: lastGERUpdate is either an L1 *block* timestamp (chain clock, from a
// monitor event) or a local wall-clock time (from a confirmed send), while elapsed is always
// measured against the tool's local wall clock (time.Since). In practice L1 block timestamps track
// real time closely, so the small skew between the two clocks is immaterial next to a
// MaxTimeWithoutGERUpdate that is realistically minutes-to-hours. The skew is also safe in both
// directions: if a block timestamp is slightly ahead of the local clock, time.Since is negative,
// elapsed stays below the threshold, and the tool simply waits a little longer before forcing an
// update (it never over-fires from skew, and a negative duration never panics).
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

	// sendDone carries the outcome of our own sends to the select loop below. Buffered 1: the
	// in-flight guard ensures at most one send is ever outstanding, so the goroutine can never
	// block trying to deliver here.
	sendDone := make(chan sendOutcome, 1)

	triggerSend := func() {
		// CompareAndSwap(false, true) atomically checks-and-sets: exactly one caller can win this
		// race per in-flight window, so concurrent/rapid ticks can never launch two sends at once.
		// Only the main loop's sendDone case (below) ever clears inFlight back to false.
		if !inFlight.CompareAndSwap(false, true) {
			log.Debugf("force_ger_update: forced update already in flight, skipping")
			return
		}

		wg.Add(1)
		go func() {
			defer wg.Done()

			log.Infof("force_ger_update: elapsed time since last GER update exceeded threshold, " +
				"sending forced GER update")
			confirmedAt, err := sender.SendForcedGERUpdate(ctx)
			select {
			case sendDone <- sendOutcome{confirmedAt: confirmedAt, err: err}:
			case <-ctx.Done():
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
			if ev.BlockTimestamp.After(lastGERUpdate) {
				lastGERUpdate = ev.BlockTimestamp
			}

		case res := <-sendDone:
			if res.err != nil {
				log.Errorf("force_ger_update: forced GER update failed: %v", res.err)
			} else if !res.confirmedAt.IsZero() {
				log.Infof("force_ger_update: forced GER update confirmed at %s, resetting timer", res.confirmedAt)
				if res.confirmedAt.After(lastGERUpdate) {
					lastGERUpdate = res.confirmedAt
				}
			}
			// Clearing the guard here, after the (possible) reset above, is what closes the race:
			// see this function's doc comment.
			inFlight.Store(false)

		case <-ticker.C:
			elapsed := time.Since(lastGERUpdate)
			if elapsed >= maxTimeWithoutGERUpdate {
				triggerSend()
			}
		}
	}
}

// sendOutcome carries a completed SendForcedGERUpdate call's result from the goroutine that ran it
// back to runLoop's single-threaded select loop.
type sendOutcome struct {
	confirmedAt time.Time
	err         error
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
