package bridgelooptester

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	aggkit "github.com/agglayer/aggkit"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
)

// mintCycles is how many cycles' worth of an ERC20 loop's Amount the tool mints when it first
// deploys the loop's token. A circular route returns the value to its origin every cycle, so one
// cycle's worth would in principle be enough; minting a margin means a hop that strands value
// part-way round the ring does not immediately starve the loop of anything to bridge.
const mintCycles = 100

// tokenSymbol and tokenNamePrefix are the ERC20 metadata the tool deploys its own test token with.
const (
	tokenSymbol     = "BLT"
	tokenNamePrefix = "BridgeLoopTester "
)

// signerFingerprintBytes is how many bytes of the signer-config digest identify a signing key in
// the client pool's key and in log lines. Eight bytes is plenty to distinguish the handful of keys
// one config holds, and short enough to read.
const signerFingerprintBytes = 8

// OrchestratorDeps are the injected dependencies of an Orchestrator. Every field is optional: a
// zero OrchestratorDeps produces a fully real orchestrator that dials the configured RPCs and the
// configured proxy. Tests and the in-process e2e harness override what they need.
type OrchestratorDeps struct {
	// Logger receives the run's structured log lines. Nil means log.GetDefaultLogger().
	Logger aggkitcommon.Logger
	// Proxy is the aggkit-proxy observation layer. Nil means NewProxyClient(cfg.Global).
	Proxy Proxy
	// Store persists the resumable state. Nil means a FileStateStore at Global.StatePath, or
	// NoopStateStore when that is empty.
	Store StateStore
	// NewNetworkClientFn builds one network's client. Nil means NewNetworkClient (which dials
	// cfg.RPCURL and builds cfg.Signer's signer). An e2e harness that already holds clients and
	// keys substitutes NewNetworkClientWithBackend here.
	NewNetworkClientFn func(ctx context.Context, cfg Network, logger aggkitcommon.Logger) (NetworkClient, error)
	// NewBridgeFn binds a network's bridge contract. Nil means NewBridge.
	NewBridgeFn func(client NetworkClient, address common.Address) (Bridge, error)
	// NewTokenFn binds an ERC20. Nil means NewToken.
	NewTokenFn func(client NetworkClient, address common.Address) (Token, error)
	// DeployTokenFn deploys the test ERC20. Nil means DeployToken.
	DeployTokenFn func(ctx context.Context, client NetworkClient, name, symbol string) (Token, common.Hash, error)
	// NewHopRunnerFn builds the HopRunner for one loop. Nil means a *HopEngine over the pooled
	// networks, wired to persist that loop's checkpoints. It is per loop because a checkpoint
	// carries no loop identity, so the persistence closure must supply it.
	NewHopRunnerFn func(loopName string) (HopRunner, error)
	// Metrics is the metrics recorder. Nil means Run registers NewMetrics() when
	// Global.MetricsAddr is set, and no metrics at all otherwise.
	Metrics *Metrics
	// NativeGasSlack overrides DefaultNativeGasSlack for every loop's hop engine: the extra
	// native-currency shortfall a native hop's destination balance check tolerates on top of the
	// gas the hop itself provably spent (see HopDeps.NativeGasSlack). Nil means the default.
	//
	// It belongs here rather than in the config because it is a property of how many loops share
	// one signing account, not of the network: all loops on a network sign with the same key, so
	// while one loop's hop waits on its destination credit, another loop's approve, bridge or claim
	// on that same network debits the very account the credit is being measured on. The default
	// 0.001 ETH covers roughly one such transaction; a run with several concurrent loops on one
	// network needs more, or a hop's balance check will intermittently report a shortfall that is
	// really a sibling loop's gas.
	NativeGasSlack *big.Int
	// HopAttempts is how many times a hop is attempted in total (the first attempt plus any
	// retries) before its cycle is abandoned (see the failure policy on Orchestrator). Zero or
	// negative means cfg.Global.HopAttempts is used instead (which Validate guarantees is > 0),
	// falling back to defaultHopAttempts only if that is exceptionally also unset. Set this to
	// override the configured value, e.g. in a test.
	HopAttempts int
	// ResumeHalted, when true, clears the Halted flag of every loop in the loaded state, so a run
	// re-drives loops a previous run halted for a non-retryable reason. Off by default: a halted
	// loop recorded a test failure, and silently re-driving it on the next start would hide it.
	ResumeHalted bool
	// Now is the clock, injected so tests need not sleep. Nil means time.Now.
	Now func() time.Time
}

// poolKey identifies one pooled NetworkClient by the pair whose nonce sequence it owns: the
// network it talks to and the key it signs with.
type poolKey struct {
	networkID   uint32
	fingerprint string
}

// String renders the key for a log line.
func (k poolKey) String() string {
	return fmt.Sprintf("network=%d signer=%s", k.networkID, k.fingerprint)
}

// pooledNetwork is one entry of the client pool: the configuration, the shared client, and the
// bridge bound to it.
type pooledNetwork struct {
	Config Network
	Client NetworkClient
	Bridge Bridge
	Key    poolKey
}

// networkPool holds exactly one NetworkClient per (network, signing key) pair.
//
// This is not an optimisation. NetworkClient.SendTx serializes nonces per *instance*: it reserves
// the next nonce under its own mutex and only re-reads the node's pending nonce after a failed
// submission. Two clients wrapping the same key on the same network each believe they own that
// sequence, so a soak run with several loops on one network would produce duplicate nonces and
// wedge every subsequent submission from that account behind a gap. Building the pool once, at
// startup, and handing the same instance to every loop is what makes concurrent loops safe.
type networkPool struct {
	byKey     map[poolKey]*pooledNetwork
	byNetwork map[uint32]*pooledNetwork
	order     []poolKey
}

// newNetworkPool builds one client (and bridge) per (network, signing key) pair in cfg.Networks.
// It closes whatever it already built if a later network fails, so a partial pool never leaks a
// dialed connection.
func newNetworkPool(
	ctx context.Context,
	cfg *Config,
	logger aggkitcommon.Logger,
	newClient func(ctx context.Context, cfg Network, logger aggkitcommon.Logger) (NetworkClient, error),
	newBridge func(client NetworkClient, address common.Address) (Bridge, error),
) (*networkPool, error) {
	pool := &networkPool{
		byKey:     map[poolKey]*pooledNetwork{},
		byNetwork: map[uint32]*pooledNetwork{},
	}

	for _, network := range cfg.Networks {
		key := poolKey{networkID: network.NetworkID, fingerprint: signerFingerprint(network.Signer)}

		if existing, ok := pool.byKey[key]; ok {
			// Same network, same key: share the one client, exactly as loops on one network do.
			pool.byNetwork[network.NetworkID] = existing
			logger.Infof("bridge_loop_tester: network %d (%s) reuses the pooled client for %s",
				network.NetworkID, network.Name, key)
			continue
		}
		if existing, ok := pool.byNetwork[network.NetworkID]; ok {
			pool.Close()
			return nil, fmt.Errorf("new network pool: network %d is configured twice with different signing "+
				"keys (%s and %s); the tool pools one client per (network, signing key) pair and cannot "+
				"decide which key a hop on that network should use",
				network.NetworkID, existing.Key.fingerprint, key.fingerprint)
		}

		client, err := newClient(ctx, network, logger)
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("new network pool: %w", err)
		}
		bridge, err := newBridge(client, network.BridgeAddr)
		if err != nil {
			client.Close()
			pool.Close()
			return nil, fmt.Errorf("new network pool: network %d (%s): %w", network.NetworkID, network.Name, err)
		}

		entry := &pooledNetwork{Config: network, Client: client, Bridge: bridge, Key: key}
		pool.byKey[key] = entry
		pool.byNetwork[network.NetworkID] = entry
		pool.order = append(pool.order, key)

		logger.Infof("bridge_loop_tester: pooled client for %s network=%q chain_id=%s from=%s bridge=%s",
			key, network.Name, client.ChainID(), client.From(), network.BridgeAddr)
	}

	return pool, nil
}

// network returns the pooled entry for a network ID.
func (p *networkPool) network(networkID uint32) (*pooledNetwork, bool) {
	entry, ok := p.byNetwork[networkID]

	return entry, ok
}

// hopNetworks renders the pool as the HopDeps.Networks map the hop engine takes.
func (p *networkPool) hopNetworks() map[uint32]HopNetwork {
	networks := make(map[uint32]HopNetwork, len(p.byNetwork))
	for networkID, entry := range p.byNetwork {
		networks[networkID] = HopNetwork{Config: entry.Config, Client: entry.Client, Bridge: entry.Bridge}
	}

	return networks
}

// Close releases every pooled client exactly once, even where several networks share one.
func (p *networkPool) Close() {
	for _, key := range p.order {
		if entry, ok := p.byKey[key]; ok {
			entry.Client.Close()
		}
	}
	p.order = nil
	p.byKey = map[poolKey]*pooledNetwork{}
	p.byNetwork = map[uint32]*pooledNetwork{}
}

// signerFingerprint derives a short, stable identifier for a signing key from its SignerConfig,
// without ever exposing the configuration itself: the digest covers the method and every key/value
// pair (sorted, so map iteration order cannot change it), and only its first few bytes are kept.
// It is an identity, not a secret - two networks configured with the same key produce the same
// fingerprint, which is exactly what the pool needs to know.
func signerFingerprint(cfg signertypes.SignerConfig) string {
	keys := make([]string, 0, len(cfg.Config))
	for key := range cfg.Config {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	digest := sha256.New()
	_, _ = digest.Write([]byte(cfg.Method))
	for _, key := range keys {
		_, _ = fmt.Fprintf(digest, "\x00%s=%v", key, cfg.Config[key])
	}

	return hex.EncodeToString(digest.Sum(nil)[:signerFingerprintBytes])
}

// Orchestrator drives every enabled loop of a Config: one goroutine per loop, each walking its
// ring hop by hop, sleeping Global.LoopDelay between cycles, for Global.Iterations cycles (0 =
// forever) or until its context is cancelled.
//
// # Failure policy
//
// A soak tester that dies on the first transient is useless; one that silently drops a broken ring
// is worse. So a hop failure is classified (see FailureClass and ClassifyHopFailure) and each class
// has exactly one response:
//
//   - FailureTransient (a readiness gate that timed out, an RPC or proxy error, a destination
//     balance that did not reconcile): the *same hop* is attempted in total up to HopAttempts
//     times (Global.LoopDelay apart), resuming from the checkpoint the failed attempt reached - so
//     a hop that already bridged continues at its claim gate instead of bridging again. If it
//     still fails, the cycle
//     ends there and the loop keeps its hop cursor: the next cycle resumes the ring at the hop that
//     failed, because the value is stranded on that hop's source network and starting over at hop 0
//     would try to spend value that is not there. The loop is never abandoned for a transient.
//   - FailureClaimMode (*ClaimModeViolationError): fatal to that loop, no retry. An "auto" hop
//     nobody claimed, or a "manual" hop something else claimed, is the assertion this tool exists
//     to make; retrying it would turn a real autoclaim-policy defect into an invisible delay.
//   - FailureAmbiguousResume (*AmbiguousResumeError): fatal to that loop, reported at error level
//     with the full decision evidence (bridge tx hash and nonce, the account's mined and pending
//     nonces, how long the receipt was waited for). The hop is neither skipped nor re-driven:
//     skipping would abandon value mid-ring, re-driving could double-bridge.
//   - FailureInsufficientBalance (*InsufficientBalanceError): fatal to that loop. A soak run cannot
//     top itself up, so retrying for days would only fill the log with the same line.
//   - FailureConfiguration: fatal to that loop - a malformed request or an unknown network is a bug
//     or a bad config, and no retry fixes either.
//   - FailureCancelled: not a failure. The loop stops, the state is flushed, and the Report says
//     Cancelled.
//
// A loop halted by a fatal class stays halted across restarts (the flag is persisted), so a test
// failure cannot be papered over by a restart; OrchestratorDeps.ResumeHalted re-drives it
// deliberately.
//
// # Where the value is
//
// Because a route is circular, every loop is at rest on its origin network between cycles. A loop
// whose LoopState.HopIndex is not 0 therefore has value stranded part-way round its ring, and
// LoopReport.ValueLocation (and the `status` command) says exactly where - on the failing hop's
// source network if the bridge never happened, or in flight towards its destination if it did.
type Orchestrator struct {
	cfg         *Config
	logger      aggkitcommon.Logger
	proxy       Proxy
	pool        *networkPool
	store       StateStore
	metrics     *Metrics
	now         func() time.Time
	hopAttempts int
	// gasSlack is OrchestratorDeps.NativeGasSlack, passed through to every loop's hop engine. Nil
	// leaves the engine on DefaultNativeGasSlack.
	gasSlack *big.Int

	newHopRunner func(loopName string) (HopRunner, error)
	newToken     func(client NetworkClient, address common.Address) (Token, error)
	deployToken  func(ctx context.Context, client NetworkClient, name, symbol string) (Token, common.Hash, error)

	// stateMu guards state: every loop goroutine reads and writes it, and every write is followed
	// by an atomic Save so the on-disk snapshot never trails the chain by more than one state.
	stateMu sync.Mutex
	state   *State
}

// NewOrchestrator validates cfg, builds the client pool (one per (network, signing key) pair, see
// networkPool) and loads any persisted state. The caller owns the result and must Close it, which
// releases every pooled RPC connection.
func NewOrchestrator(ctx context.Context, cfg *Config, deps OrchestratorDeps) (*Orchestrator, error) {
	if cfg == nil {
		return nil, fmt.Errorf("new orchestrator: config is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("new orchestrator: configuration is invalid:\n%w", err)
	}

	o := &Orchestrator{
		cfg:         cfg,
		logger:      deps.Logger,
		proxy:       deps.Proxy,
		metrics:     deps.Metrics,
		now:         deps.Now,
		hopAttempts: deps.HopAttempts,
		gasSlack:    deps.NativeGasSlack,
		newToken:    deps.NewTokenFn,
		deployToken: deps.DeployTokenFn,
	}
	if o.logger == nil {
		o.logger = log.GetDefaultLogger()
	}
	if o.now == nil {
		o.now = time.Now
	}
	if o.hopAttempts <= 0 {
		o.hopAttempts = int(cfg.Global.HopAttempts)
	}
	if o.hopAttempts <= 0 {
		o.hopAttempts = defaultHopAttempts
	}
	if o.newToken == nil {
		o.newToken = NewToken
	}
	if o.deployToken == nil {
		o.deployToken = DeployToken
	}
	if o.proxy == nil {
		proxy, err := NewProxyClient(cfg.Global)
		if err != nil {
			return nil, fmt.Errorf("new orchestrator: %w", err)
		}
		o.proxy = proxy
	}
	store, err := resolveStateStore(deps.Store, cfg.Global.StatePath)
	if err != nil {
		return nil, fmt.Errorf("new orchestrator: %w", err)
	}
	o.store = store

	newClient := deps.NewNetworkClientFn
	if newClient == nil {
		newClient = func(ctx context.Context, cfg Network, logger aggkitcommon.Logger) (NetworkClient, error) {
			return NewNetworkClient(ctx, cfg, logger)
		}
	}
	newBridge := deps.NewBridgeFn
	if newBridge == nil {
		newBridge = NewBridge
	}

	pool, err := newNetworkPool(ctx, cfg, o.logger, newClient, newBridge)
	if err != nil {
		return nil, err
	}
	o.pool = pool

	o.newHopRunner = deps.NewHopRunnerFn
	if o.newHopRunner == nil {
		o.newHopRunner = o.newHopEngine
	}

	state, err := o.store.Load(ctx)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("new orchestrator: %w", err)
	}
	if deps.ResumeHalted {
		for _, record := range state.Loops {
			if record.Halted {
				o.logger.Warnf("bridge_loop_tester: loop %q was halted (%s: %s) and is being re-driven "+
					"because ResumeHalted is set", record.Name, record.HaltClass, record.LastError)
				record.Halted = false
				record.HaltClass = FailureNone
			}
		}
	}
	o.state = state

	return o, nil
}

// resolveStateStore picks the state store: the injected one, else a file store at statePath, else
// a no-op store.
func resolveStateStore(injected StateStore, statePath string) (StateStore, error) {
	if injected != nil {
		return injected, nil
	}
	if statePath == "" {
		return NoopStateStore{}, nil
	}

	return NewFileStateStore(statePath)
}

// Close releases every pooled RPC connection. It is safe to call more than once.
func (o *Orchestrator) Close() {
	if o.pool != nil {
		o.pool.Close()
	}
}

// State returns a snapshot of the orchestrator's current persisted state, for the `status` command
// and for a caller that wants to inspect a finished run's cursors.
func (o *Orchestrator) State() *State {
	o.stateMu.Lock()
	defer o.stateMu.Unlock()

	return o.state
}

// newHopEngine builds the default HopRunner for one loop: a *HopEngine over the pooled networks,
// whose checkpoints are persisted into that loop's record in the state file.
func (o *Orchestrator) newHopEngine(loopName string) (HopRunner, error) {
	return NewHopEngine(HopDeps{
		Networks:       o.pool.hopNetworks(),
		Proxy:          o.proxy,
		Logger:         o.logger,
		Timings:        HopTimingsFromGlobal(o.cfg.Global),
		NativeGasSlack: o.gasSlack,
		NewTokenFn:     o.newToken,
		Now:            o.now,
		PersistCheckpoint: func(ctx context.Context, checkpoint HopCheckpoint) error {
			return o.persistCheckpoint(ctx, loopName, checkpoint)
		},
	})
}

// persistCheckpoint records a hop checkpoint as the named loop's in-flight hop and saves the state
// atomically. The hop engine calls it on entry to every state, before that state's side effect, so
// the snapshot on disk is never behind the chain - and it is called twice for HopStateBridging
// (once before signing with a zero hash, once from the pre-broadcast hook with the hash and nonce),
// which is why it overwrites rather than appends.
func (o *Orchestrator) persistCheckpoint(ctx context.Context, loopName string, checkpoint HopCheckpoint) error {
	o.stateMu.Lock()
	record := o.state.Loop(loopName)
	saved := checkpoint
	record.InFlight = &saved
	record.UpdatedAt = o.now().UTC()
	state := o.state
	o.stateMu.Unlock()

	return o.store.Save(ctx, state)
}

// saveState persists the current state snapshot, logging rather than propagating a failure: a run
// that cannot write its state file is degraded (it loses cross-restart resumability) but not
// wrong, and stopping a multi-day soak run over it would be worse than continuing loudly.
func (o *Orchestrator) saveState(ctx context.Context) {
	o.stateMu.Lock()
	state := o.state
	o.stateMu.Unlock()

	if err := o.store.Save(ctx, state); err != nil {
		o.logger.Errorf("bridge_loop_tester: could not persist the state file: %v", err)
	}
}

// Run drives every enabled loop and returns the run's Report. It performs the run-mode preflight
// first (see (*Orchestrator).Preflight), starts the metrics server when Global.MetricsAddr is set,
// ensures each ERC20 loop's token exists, then runs one goroutine per loop until every loop has
// finished its Iterations or ctx is cancelled - at which point the state is flushed before Run
// returns.
//
// The returned error is non-nil when the run could not start (a preflight refusal, a token that
// could not be deployed) or when at least one loop halted on a fatal failure. The Report is
// returned in both cases: a failed run's record is the point of running it.
func (o *Orchestrator) Run(ctx context.Context) (*Report, error) {
	report := &Report{
		StartedAt:  o.now().UTC(),
		DryRun:     o.cfg.Global.DryRun,
		Iterations: o.cfg.Global.Iterations,
	}
	defer func() {
		report.FinishedAt = o.now().UTC()
		report.Duration = report.FinishedAt.Sub(report.StartedAt)
		report.computeTotals()
	}()

	o.logStartupBanner()

	preflight, err := o.Preflight(ctx, false)
	if err != nil {
		return report, err
	}
	report.Preflight = preflight
	o.logPreflight(preflight)
	if err := preflight.Err(); err != nil {
		return report, fmt.Errorf("run: preflight refused to start the run:\n%w", err)
	}

	if o.cfg.Global.MetricsAddr != "" {
		// Registered here rather than in NewOrchestrator so that validate/status/claim, which build
		// an Orchestrator but drive no hop, neither register metrics nor bind a port.
		if o.metrics == nil {
			o.metrics = NewMetrics()
		}
		stop, serveErr := o.metrics.Serve(ctx, o.cfg.Global.MetricsAddr, o.logger)
		if serveErr != nil {
			return report, fmt.Errorf("run: %w", serveErr)
		}
		defer stop()
	}

	loops := o.cfg.EnabledLoops()
	if o.cfg.Global.DryRun {
		report.Loops = o.plan(ctx, loops)
		o.saveState(ctx)

		return report, nil
	}

	tokens, err := o.ensureTokens(ctx, loops)
	if err != nil {
		return report, fmt.Errorf("run: %w", err)
	}
	o.saveState(ctx)

	report.Loops = o.runLoops(ctx, loops, tokens)
	o.saveState(ctx)

	report.Cancelled = ctx.Err() != nil
	report.Err = aggregateLoopErrors(report.Loops)
	report.computeTotals()
	// Stamp the run's end before logging it, not only in the deferred stamp above: logFinish reads
	// Report.Duration through Summary, and the defer has not run yet, so without this the closing
	// "run finished" line of every completed run reports duration=0s. The defer still refreshes
	// both fields afterwards, so the returned Report carries the later, more accurate value.
	report.FinishedAt = o.now().UTC()
	report.Duration = report.FinishedAt.Sub(report.StartedAt)
	o.logFinish(report)

	if report.Err != "" {
		return report, fmt.Errorf("run: %s", report.Err)
	}

	return report, nil
}

// runLoops runs one goroutine per loop and returns their reports in configuration order.
func (o *Orchestrator) runLoops(ctx context.Context, loops []Loop, tokens map[string]*TokenState) []LoopReport {
	reports := make([]LoopReport, len(loops))
	var wg sync.WaitGroup

	for i, loop := range loops {
		wg.Add(1)
		go func(index int, loop Loop) {
			defer wg.Done()
			reports[index] = o.runLoop(ctx, loop, tokens[loop.Name])
		}(i, loop)
	}
	wg.Wait()

	o.publishLoopHealth(reports)

	return reports
}

// publishLoopHealth pushes the halted/stranded loop counts to the metrics gauges.
func (o *Orchestrator) publishLoopHealth(reports []LoopReport) {
	halted, stranded := 0, 0
	for i := range reports {
		if reports[i].Halted {
			halted++
		}
		if reports[i].ValueLocation.Stranded {
			stranded++
		}
	}
	o.metrics.SetLoopHealth(halted, stranded)
}

// runLoop drives one loop's ring for as many cycles as Iterations allows, or until ctx is
// cancelled or a fatal failure halts it. See Orchestrator's failure policy.
func (o *Orchestrator) runLoop(ctx context.Context, loop Loop, token *TokenState) LoopReport {
	report := LoopReport{
		Name:               loop.Name,
		Asset:              loop.Asset,
		Amount:             loop.Amount.BigInt(),
		Route:              routeOf(loop),
		TokenOriginNetwork: loop.TokenOriginNetwork,
	}
	if token != nil {
		report.TokenAddress = token.Address
		report.TokenDeployed = true
	}

	runner, err := o.newHopRunner(loop.Name)
	if err != nil {
		report.Halted = true
		report.HaltClass = FailureConfiguration
		report.Err = err.Error()
		o.logger.Errorf("bridge_loop_tester: loop %q cannot start: %v", loop.Name, err)
		o.haltLoop(ctx, loop.Name, FailureConfiguration, err)
		report.ValueLocation = o.valueLocation(loop)

		return report
	}

	if halted, class, reason := o.loopHalted(loop.Name); halted {
		report.Halted = true
		report.HaltClass = class
		report.Err = reason
		report.ValueLocation = o.valueLocation(loop)
		o.logger.Errorf("bridge_loop_tester: loop %q stays halted from a previous run (%s: %s); reset it "+
			"with --resume-halted or by clearing its record in the state file", loop.Name, class, reason)

		return report
	}

	o.logger.Infof("bridge_loop_tester: loop starting name=%q asset=%s amount=%s route=%s claim_modes=%s "+
		"iterations=%d loop_delay=%s resume_hop=%d",
		loop.Name, loop.Asset, loop.Amount, strings.Join(report.Route, ","), claimModesOf(loop),
		o.cfg.Global.Iterations, o.cfg.Global.LoopDelay.Duration, o.loopCursor(loop.Name))

	for cyclesThisRun := uint64(0); ; cyclesThisRun++ {
		if o.cfg.Global.Iterations > 0 && cyclesThisRun >= o.cfg.Global.Iterations {
			o.logger.Infof("bridge_loop_tester: loop %q finished its configured %d cycle(s)",
				loop.Name, o.cfg.Global.Iterations)
			break
		}
		if cyclesThisRun > 0 && !o.sleep(ctx, o.cfg.Global.LoopDelay.Duration) {
			break
		}
		if ctx.Err() != nil {
			break
		}

		cycle := o.runCycle(ctx, loop, token, runner)
		report.Cycles = append(report.Cycles, cycle)
		report.CyclesAttempted++
		if cycle.RingClosed {
			report.CyclesCompleted++
		}
		o.metrics.CycleFinished(loop.Name, cycle.RingClosed)

		if cycle.FailureClass.Fatal() {
			report.Halted = true
			report.HaltClass = cycle.FailureClass
			report.Err = cycle.Err
			break
		}
		if cycle.FailureClass == FailureCancelled || ctx.Err() != nil {
			break
		}
	}

	report.ValueLocation = o.valueLocation(loop)
	o.logger.Infof("bridge_loop_tester: loop finished name=%q cycles=%d/%d halted=%t halt_class=%s value=%s",
		loop.Name, report.CyclesCompleted, report.CyclesAttempted, report.Halted, report.HaltClass,
		report.ValueLocation)

	return report
}

// runCycle walks one pass of a loop's ring, starting at the loop's persisted hop cursor - which is
// 0 for a healthy loop and the stranded hop for one a previous cycle left part-way round.
func (o *Orchestrator) runCycle(
	ctx context.Context, loop Loop, token *TokenState, runner HopRunner,
) (cycle CycleReport) {
	startHop := o.loopCursor(loop.Name)
	iteration := o.beginCycle(ctx, loop.Name)
	cycle = CycleReport{
		Iteration:     iteration,
		StartedAt:     o.now().UTC(),
		StartHopIndex: startHop,
	}
	// The result is named so this defer stamps the value the caller receives: with an unnamed
	// result the return value is copied before defers run, and the timings would be dropped.
	defer func() {
		cycle.FinishedAt = o.now().UTC()
		cycle.Duration = cycle.FinishedAt.Sub(cycle.StartedAt)
	}()

	for hopIndex := startHop; hopIndex < len(loop.Hops); hopIndex++ {
		if ctx.Err() != nil {
			cycle.FailureClass = FailureCancelled
			cycle.Err = ctx.Err().Error()

			return cycle
		}

		results, err := o.runHopWithRetries(ctx, loop, token, runner, iteration, hopIndex)
		cycle.Hops = append(cycle.Hops, results...)
		if err != nil {
			cycle.FailureClass = ClassifyHopFailure(err)
			cycle.Err = err.Error()
			o.recordHopFailure(ctx, loop, hopIndex, cycle.FailureClass, err)

			return cycle
		}

		o.advanceCursor(ctx, loop, hopIndex)
	}

	cycle.RingClosed = true
	o.completeCycle(ctx, loop)
	o.logger.Infof("bridge_loop_tester: cycle completed loop=%q iteration=%d hops=%d duration=%s",
		loop.Name, iteration, len(cycle.Hops), o.now().Sub(cycle.StartedAt))

	return cycle
}

// runHopWithRetries runs one hop up to HopAttempts times in total (the first attempt plus any
// retries) when a transient failure occurs. Each attempt after the first (i.e. each retry)
// resumes from the checkpoint the failed attempt reached, so a hop that already bridged
// continues at its claim gate rather than bridging a second time. It returns every attempt's
// result, and the error of the last one.
func (o *Orchestrator) runHopWithRetries(
	ctx context.Context,
	loop Loop,
	token *TokenState,
	runner HopRunner,
	iteration uint64,
	hopIndex int,
) ([]*HopResult, error) {
	hop := loop.Hops[hopIndex]
	route := fmt.Sprintf("%d->%d", hop.Source, hop.Destination)
	metricsKey := fmt.Sprintf("%s/%d/%d", loop.Name, iteration, hopIndex)

	var (
		results []*HopResult
		resume  = o.inFlightCheckpoint(loop.Name, hop)
		lastErr error
	)

	for attempt := 1; attempt <= o.hopAttempts; attempt++ {
		req := HopRequest{
			LoopName:           loop.Name,
			Iteration:          iteration,
			HopIndex:           hopIndex,
			Hop:                hop,
			Asset:              loop.Asset,
			Amount:             loop.Amount.BigInt(),
			TokenOriginNetwork: loop.TokenOriginNetwork,
			Resume:             resume,
		}
		if token != nil {
			req.TokenOriginAddress = token.Address
		}

		startedAt := o.now()
		o.metrics.HopStarted(metricsKey, startedAt)
		result, err := runner.RunHop(ctx, req)
		o.metrics.HopFinished(metricsKey, result)
		if result != nil {
			results = append(results, result)
			o.recordDiscoveredTokens(loop, result)
		}
		if err == nil {
			return results, nil
		}

		lastErr = err
		class := ClassifyHopFailure(err)
		if class != FailureTransient {
			o.logFatalHopFailure(loop, hopIndex, class, err)

			return results, err
		}

		if attempt == o.hopAttempts {
			o.logger.Errorf("bridge_loop_tester: hop gave up loop=%q iteration=%d hop=%d route=%s "+
				"attempts=%d class=%s: %v; the cycle stops here and the next one resumes this hop",
				loop.Name, iteration, hopIndex, route, attempt, class, err)

			break
		}

		resume = checkpointToResume(result)
		o.metrics.HopRetried(loop.Name, route)
		o.logger.Warnf("bridge_loop_tester: hop retrying loop=%q iteration=%d hop=%d route=%s "+
			"attempt=%d/%d resume_from=%s: %v",
			loop.Name, iteration, hopIndex, route, attempt, o.hopAttempts, resumeStateOf(resume), err)

		if !o.sleep(ctx, o.cfg.Global.LoopDelay.Duration) {
			return results, lastErr
		}
	}

	return results, lastErr
}

// checkpointToResume turns a failed attempt's result into the checkpoint the next attempt resumes
// from, or nil when the attempt never reached a recordable state.
func checkpointToResume(result *HopResult) *HopCheckpoint {
	if result == nil || result.Checkpoint.State == "" || result.Checkpoint.State == HopStateFailed {
		return nil
	}
	checkpoint := result.Checkpoint

	return &checkpoint
}

// resumeStateOf renders a resume checkpoint's state for a log line.
func resumeStateOf(checkpoint *HopCheckpoint) HopState {
	if checkpoint == nil {
		return HopStatePending
	}

	return checkpoint.State
}

// logFatalHopFailure reports a non-retryable hop failure at error level, spelling out the evidence
// that makes it non-retryable. An ambiguous resume gets the loudest treatment: it is the one case
// where the tool refuses to act and needs a human to reconcile the bridge's indexed state.
func (o *Orchestrator) logFatalHopFailure(loop Loop, hopIndex int, class FailureClass, err error) {
	hop := loop.Hops[hopIndex]

	var ambiguous *AmbiguousResumeError
	if errors.As(err, &ambiguous) {
		o.logger.Errorf("bridge_loop_tester: HALTING loop %q: hop %d (%d->%d) cannot be resumed "+
			"unambiguously and must not be re-driven. Evidence: state=%s bridge_tx=%s bridge_tx_nonce=%d "+
			"account_nonce=%d pending_nonce=%d receipt_wait=%s account=%s amount=%s. "+
			"Reconcile the bridge's indexed state for that deposit by hand, then reset this loop's "+
			"record in the state file; the tool will neither skip the hop nor bridge again. Full error: %v",
			loop.Name, hopIndex, hop.Source, hop.Destination, ambiguous.State, ambiguous.BridgeTxHash,
			ambiguous.BridgeTxNonce, ambiguous.AccountNonce, ambiguous.PendingNonce, ambiguous.ReceiptWait,
			ambiguous.Account, bigIntString(ambiguous.Amount), err)

		return
	}

	var violation *ClaimModeViolationError
	if errors.As(err, &violation) {
		o.logger.Errorf("bridge_loop_tester: HALTING loop %q: hop %d (%d->%d) violated its claim-mode "+
			"expectation, which is a test failure and never a transient: expected=%s observed=%s "+
			"deposit_count=%d global_index=%s grace_period=%s bridge_tx=%s claim_tx=%s claim_from=%s "+
			"proof_available=%t. Full error: %v",
			loop.Name, hopIndex, hop.Source, hop.Destination, violation.Expected, violation.Observed,
			violation.DepositCount, globalIndexString(violation.GlobalIndex), violation.GracePeriod,
			violation.BridgeTxHash, hashString(violation.ClaimTxHash), addressString(violation.ClaimFromAddress),
			violation.ProofAvailable, err)

		return
	}

	o.logger.Errorf("bridge_loop_tester: HALTING loop %q: hop %d (%d->%d) failed with a non-retryable "+
		"class %s: %v", loop.Name, hopIndex, hop.Source, hop.Destination, class, err)
}

// sleep waits for d, or returns false as soon as ctx is done.
func (o *Orchestrator) sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// loopCursor returns the hop index the named loop's next cycle resumes at.
func (o *Orchestrator) loopCursor(loopName string) int {
	o.stateMu.Lock()
	defer o.stateMu.Unlock()

	return o.state.Loop(loopName).HopIndex
}

// loopHalted reports whether the named loop is persisted as halted, and why.
func (o *Orchestrator) loopHalted(loopName string) (bool, FailureClass, string) {
	o.stateMu.Lock()
	defer o.stateMu.Unlock()

	record := o.state.Loop(loopName)

	return record.Halted, record.HaltClass, record.LastError
}

// inFlightCheckpoint returns the persisted in-flight checkpoint for a loop, but only when it
// belongs to the hop about to run: a checkpoint recorded for a different route would resume the
// wrong deposit, so it is discarded rather than trusted.
func (o *Orchestrator) inFlightCheckpoint(loopName string, hop Hop) *HopCheckpoint {
	o.stateMu.Lock()
	defer o.stateMu.Unlock()

	record := o.state.Loop(loopName)
	if record.InFlight == nil || record.InFlight.State == "" || record.InFlight.State == HopStateFailed {
		return nil
	}
	if record.ValueNetwork != hop.Source {
		return nil
	}
	checkpoint := *record.InFlight

	return &checkpoint
}

// beginCycle bumps the loop's attempted-cycle counter and returns the 1-based iteration number of
// the pass that is starting. The counter is cumulative across restarts, so a log line's iteration
// number is stable for the life of the state file; Global.Iterations bounds cycles *per run*, not
// the counter.
func (o *Orchestrator) beginCycle(ctx context.Context, loopName string) uint64 {
	o.stateMu.Lock()
	record := o.state.Loop(loopName)
	record.CyclesAttempted++
	record.UpdatedAt = o.now().UTC()
	iteration := record.CyclesAttempted
	o.stateMu.Unlock()

	o.saveState(ctx)

	return iteration
}

// advanceCursor records that a hop completed: the value now sits on its destination, and the next
// hop is the one to run. A cursor that reaches the end of the ring is reset by completeCycle.
func (o *Orchestrator) advanceCursor(ctx context.Context, loop Loop, hopIndex int) {
	o.stateMu.Lock()
	record := o.state.Loop(loop.Name)
	record.HopIndex = hopIndex + 1
	record.ValueNetwork = loop.Hops[hopIndex].Destination
	record.InFlight = nil
	record.LastError = ""
	record.UpdatedAt = o.now().UTC()
	o.stateMu.Unlock()

	o.saveState(ctx)
}

// completeCycle records a closed ring: the cursor returns to hop 0 and the value is back on the
// loop's origin network.
func (o *Orchestrator) completeCycle(ctx context.Context, loop Loop) {
	o.stateMu.Lock()
	record := o.state.Loop(loop.Name)
	record.HopIndex = 0
	record.ValueNetwork = loop.Hops[0].Source
	record.InFlight = nil
	record.CyclesCompleted++
	record.UpdatedAt = o.now().UTC()
	o.stateMu.Unlock()

	o.saveState(ctx)
}

// recordHopFailure persists a hop failure: the cursor stays on the failing hop (so the next cycle
// resumes the stranded ring there rather than starting over at hop 0), and a fatal class halts the
// loop for good.
func (o *Orchestrator) recordHopFailure(
	ctx context.Context, loop Loop, hopIndex int, class FailureClass, err error,
) {
	o.stateMu.Lock()
	record := o.state.Loop(loop.Name)
	record.HopIndex = hopIndex
	record.ValueNetwork = loop.Hops[hopIndex].Source
	record.LastError = err.Error()
	record.UpdatedAt = o.now().UTC()
	if class.Fatal() {
		record.Halted = true
		record.HaltClass = class
	}
	o.stateMu.Unlock()

	o.saveState(ctx)
}

// haltLoop marks a loop halted without a hop having run (e.g. its hop runner could not be built).
func (o *Orchestrator) haltLoop(ctx context.Context, loopName string, class FailureClass, err error) {
	o.stateMu.Lock()
	record := o.state.Loop(loopName)
	record.Halted = true
	record.HaltClass = class
	record.LastError = err.Error()
	record.UpdatedAt = o.now().UTC()
	o.stateMu.Unlock()

	o.saveState(ctx)
}

// recordDiscoveredTokens copies the wrapped-token addresses a hop resolved into the loop's token
// record, so the state file answers "which contract holds this loop's value on network N".
func (o *Orchestrator) recordDiscoveredTokens(loop Loop, result *HopResult) {
	if loop.Asset != AssetERC20 {
		return
	}

	o.stateMu.Lock()
	defer o.stateMu.Unlock()

	o.state.RecordWrapped(loop.Name, result.Source, result.SourceTokenAddress)
	o.state.RecordWrapped(loop.Name, result.Destination, result.DestinationTokenAddress)
}

// valueLocation reports where a loop's value currently sits, and whether that means it is
// stranded. See ValueLocation for why this matters on a circular route.
func (o *Orchestrator) valueLocation(loop Loop) ValueLocation {
	o.stateMu.Lock()
	record := o.state.Loop(loop.Name)
	hopIndex := record.HopIndex
	inFlight := record.InFlight
	bridged := inFlight != nil && inFlight.BridgeTxHash != (common.Hash{})
	o.stateMu.Unlock()

	origin := loop.Hops[0].Source
	if hopIndex <= 0 || hopIndex >= len(loop.Hops) {
		return ValueLocation{
			NetworkID:   origin,
			NetworkName: o.networkName(origin),
			HopIndex:    0,
			Detail: fmt.Sprintf("at rest on the loop's origin network %d (%s); the ring is closed",
				origin, o.networkName(origin)),
		}
	}

	hop := loop.Hops[hopIndex]
	location := ValueLocation{
		NetworkID:   hop.Source,
		NetworkName: o.networkName(hop.Source),
		Stranded:    true,
		InFlight:    bridged,
		HopIndex:    hopIndex,
	}
	if bridged {
		// Deliberately does not claim the deposit is unclaimed: the state file records where this
		// tool got to, not what the destination bridge says. A hop halted on a claim-mode violation
		// stops here precisely *because* something else claimed it, so asserting "was not claimed"
		// would contradict the violation the tool just reported.
		location.Detail = fmt.Sprintf("STRANDED in flight: hop %d (%d->%d) bridged in %s; this tool had "+
			"not claimed it on network %d (%s) when the cycle stopped, so the deposit is either still "+
			"unclaimed on the bridge or was claimed by something else; the next cycle resumes at its "+
			"claim gate and reports which",
			hopIndex, hop.Source, hop.Destination, inFlight.BridgeTxHash, hop.Destination,
			o.networkName(hop.Destination))

		return location
	}
	location.Detail = fmt.Sprintf("STRANDED at rest on network %d (%s): hop %d (%d->%d) never bridged, so "+
		"the value never left; the next cycle retries that hop",
		hop.Source, o.networkName(hop.Source), hopIndex, hop.Source, hop.Destination)

	return location
}

// networkName returns a network's configured name, or a placeholder when it is not configured.
func (o *Orchestrator) networkName(networkID uint32) string {
	if entry, ok := o.pool.network(networkID); ok {
		return entry.Config.Name
	}
	for _, network := range o.cfg.Networks {
		if network.NetworkID == networkID {
			return network.Name
		}
	}

	return unknownValue
}

// ensureTokens deploys and mints each ERC20 loop's token once, reusing whatever a previous run
// already recorded in the state file.
func (o *Orchestrator) ensureTokens(ctx context.Context, loops []Loop) (map[string]*TokenState, error) {
	tokens := map[string]*TokenState{}

	for _, loop := range loops {
		if loop.Asset != AssetERC20 {
			continue
		}
		record, err := o.ensureToken(ctx, loop)
		if err != nil {
			return nil, err
		}
		tokens[loop.Name] = record
	}

	return tokens, nil
}

// ensureToken returns the loop's ERC20, deploying it (and minting a working balance) the first
// time and reusing the recorded address on every later run. A recorded address with no code at it
// - a state file carried to a different chain, or a chain that was reset under the tool - is
// redeployed rather than used, since every later call against it would revert opaquely.
func (o *Orchestrator) ensureToken(ctx context.Context, loop Loop) (*TokenState, error) {
	if loop.TokenOriginNetwork == nil {
		return nil, fmt.Errorf("loop %q: Asset = %q requires TokenOriginNetwork", loop.Name, AssetERC20)
	}
	originID := *loop.TokenOriginNetwork
	origin, ok := o.pool.network(originID)
	if !ok {
		return nil, fmt.Errorf("loop %q: TokenOriginNetwork %d has no pooled client", loop.Name, originID)
	}

	record := o.tokenRecord(loop.Name)
	if record != nil && record.Address != (common.Address{}) && record.OriginNetwork == originID {
		code, err := origin.Client.Backend().CodeAt(ctx, record.Address, nil)
		if err != nil {
			return nil, fmt.Errorf("loop %q: check the recorded token %s on %s: %w",
				loop.Name, record.Address, origin.Config.Name, err)
		}
		if len(code) > 0 {
			o.logger.Infof("bridge_loop_tester: loop %q reuses its recorded ERC20 %s on network %d (%s)",
				loop.Name, record.Address, originID, origin.Config.Name)

			return o.ensureMinted(ctx, loop, origin, record)
		}
		o.logger.Warnf("bridge_loop_tester: loop %q recorded ERC20 %s on network %d (%s) but there is no "+
			"code at that address; deploying a fresh one",
			loop.Name, record.Address, originID, origin.Config.Name)
	}

	name := tokenNamePrefix + loop.Name
	token, deployTxHash, err := o.deployToken(ctx, origin.Client, name, tokenSymbol)
	if err != nil {
		return nil, fmt.Errorf("loop %q: deploy the ERC20 on network %d (%s): %w",
			loop.Name, originID, origin.Config.Name, err)
	}

	record = &TokenState{
		LoopName:      loop.Name,
		OriginNetwork: originID,
		Address:       token.Address(),
		Name:          name,
		Symbol:        tokenSymbol,
		DeployTxHash:  deployTxHash,
		DeployedAt:    o.now().UTC(),
	}
	o.setTokenRecord(record)
	o.logger.Infof("bridge_loop_tester: loop %q deployed ERC20 %s (%s/%s) on network %d (%s)",
		loop.Name, record.Address, name, tokenSymbol, originID, origin.Config.Name)

	return o.ensureMinted(ctx, loop, origin, record)
}

// ensureMinted tops the signing account up on the token's origin network when it holds less than
// one cycle's Amount, minting mintCycles cycles' worth at a time. The test ERC20 is freely
// mintable, so this needs no funding source.
func (o *Orchestrator) ensureMinted(
	ctx context.Context, loop Loop, origin *pooledNetwork, record *TokenState,
) (*TokenState, error) {
	token, err := o.newToken(origin.Client, record.Address)
	if err != nil {
		return nil, fmt.Errorf("loop %q: bind the ERC20 %s on %s: %w",
			loop.Name, record.Address, origin.Config.Name, err)
	}

	account := origin.Client.From()
	balance, err := token.BalanceOf(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("loop %q: read the ERC20 balance of %s on %s: %w",
			loop.Name, account, origin.Config.Name, err)
	}

	amount := loop.Amount.BigInt()
	if balance.Cmp(amount) >= 0 {
		return record, nil
	}

	mintAmount := new(big.Int).Mul(amount, big.NewInt(mintCycles))
	if _, err := token.Mint(ctx, account, mintAmount); err != nil {
		return nil, fmt.Errorf("loop %q: mint %s of ERC20 %s to %s on %s: %w",
			loop.Name, mintAmount, record.Address, account, origin.Config.Name, err)
	}

	o.stateMu.Lock()
	record.AddMinted(mintAmount)
	o.state.SetToken(record)
	o.stateMu.Unlock()

	o.logger.Infof("bridge_loop_tester: loop %q minted %s of ERC20 %s to %s on network %d (%s) "+
		"(balance was %s, one cycle needs %s)",
		loop.Name, mintAmount, record.Address, account, record.OriginNetwork, origin.Config.Name,
		balance, amount)

	return record, nil
}

// tokenRecord returns a copy-safe handle on a loop's persisted token record.
func (o *Orchestrator) tokenRecord(loopName string) *TokenState {
	o.stateMu.Lock()
	defer o.stateMu.Unlock()

	return o.state.Token(loopName)
}

// setTokenRecord persists a loop's token record.
func (o *Orchestrator) setTokenRecord(record *TokenState) {
	o.stateMu.Lock()
	o.state.SetToken(record)
	o.stateMu.Unlock()
}

// logStartupBanner records what is about to run, once, so a multi-day log says which build and
// which configuration produced it.
func (o *Orchestrator) logStartupBanner() {
	version := aggkit.GetVersion()
	o.logger.Infof("bridge_loop_tester: starting version=%s git_rev=%s proxy=%s networks=%d loops=%d "+
		"enabled_loops=%d iterations=%d dry_run=%t state_path=%q metrics_addr=%q hop_attempts=%d",
		version.Version, version.GitRev, o.cfg.Global.ProxyURL, len(o.cfg.Networks), len(o.cfg.Loops),
		len(o.cfg.EnabledLoops()), o.cfg.Global.Iterations, o.cfg.Global.DryRun, o.cfg.Global.StatePath,
		o.cfg.Global.MetricsAddr, o.hopAttempts)

	o.stateMu.Lock()
	state := o.state
	o.stateMu.Unlock()
	for _, loop := range o.cfg.EnabledLoops() {
		record, ok := state.Loops[loop.Name]
		if !ok || (record.HopIndex == 0 && record.CyclesAttempted == 0 && !record.Halted) {
			continue
		}
		o.logger.Infof("bridge_loop_tester: resuming loop %q from persisted state cycles=%d/%d hop=%d "+
			"value_network=%d in_flight=%s halted=%t",
			loop.Name, record.CyclesCompleted, record.CyclesAttempted, record.HopIndex, record.ValueNetwork,
			resumeStateOf(record.InFlight), record.Halted)
	}
}

// logPreflight reports what the live preflight found, one line per finding.
func (o *Orchestrator) logPreflight(report *PreflightReport) {
	for _, entry := range report.Networks {
		o.logger.Infof("bridge_loop_tester: preflight network=%d name=%q chain_id=%d from=%s "+
			"native_balance=%s min_reserve=%s gas_token=%s gas_token_is_ether=%t weth=%s bridge=%s "+
			"proxy_bridge=%s",
			entry.NetworkID, entry.Name, entry.ChainID, entry.From, bigIntString(entry.NativeBalance),
			bigIntString(entry.MinNativeReserve), entry.GasTokenAddress, entry.GasTokenIsEther,
			addressString(entry.WETHToken), entry.BridgeAddrConfigured,
			addressString(entry.BridgeAddrReported))
		for _, note := range entry.Notes {
			o.logger.Infof("bridge_loop_tester: preflight network=%d note: %s", entry.NetworkID, note)
		}
	}
	for _, warning := range report.Warnings {
		o.logger.Warnf("bridge_loop_tester: preflight warning: %s", warning)
	}
	for _, problem := range report.Errors {
		o.logger.Errorf("bridge_loop_tester: preflight refusal: %s", problem)
	}
}

// logFinish reports the run's totals and every loop's final value location.
func (o *Orchestrator) logFinish(report *Report) {
	o.logger.Infof("bridge_loop_tester: run finished %s cancelled=%t", report.Summary(), report.Cancelled)
	for i := range report.Loops {
		loop := &report.Loops[i]
		if loop.ValueLocation.Stranded {
			o.logger.Errorf("bridge_loop_tester: loop %q left value behind: %s",
				loop.Name, loop.ValueLocation.Detail)
			continue
		}
		o.logger.Infof("bridge_loop_tester: loop %q value: %s", loop.Name, loop.ValueLocation.Detail)
	}
}

// aggregateLoopErrors joins every halted loop's error into one message, or returns "".
func aggregateLoopErrors(loops []LoopReport) string {
	var problems []string
	for i := range loops {
		if loops[i].Halted {
			problems = append(problems, fmt.Sprintf("loop %q halted (%s): %s",
				loops[i].Name, loops[i].HaltClass, loops[i].Err))
		}
	}

	return strings.Join(problems, "; ")
}

// routeOf renders a loop's ring as one "<source>-><destination>" entry per hop.
func routeOf(loop Loop) []string {
	route := make([]string, 0, len(loop.Hops))
	for _, hop := range loop.Hops {
		route = append(route, fmt.Sprintf("%d->%d", hop.Source, hop.Destination))
	}

	return route
}

// claimModesOf renders a loop's per-hop claim modes, for the loop-starting log line.
func claimModesOf(loop Loop) string {
	modes := make([]string, 0, len(loop.Hops))
	for _, hop := range loop.Hops {
		modes = append(modes, string(hop.Claim))
	}

	return strings.Join(modes, ",")
}

// Run builds an Orchestrator for cfg with default dependencies, drives every enabled loop, and
// returns the run's Report. It is the library entry point: no file is read (cfg may be built
// entirely in Go), no signal handler is installed, no global state is touched, and nothing calls
// os.Exit - cancelling ctx is how a caller stops the run, and the state is flushed before Run
// returns.
//
// Use NewOrchestrator directly to inject a logger, a proxy, pre-dialed network clients or a state
// store.
func Run(ctx context.Context, cfg *Config) (*Report, error) {
	orchestrator, err := NewOrchestrator(ctx, cfg, OrchestratorDeps{})
	if err != nil {
		return nil, err
	}
	defer orchestrator.Close()

	return orchestrator.Run(ctx)
}
