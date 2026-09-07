package bridgelooptester

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// Gate names carried by a *DeadlineExceededError the hop engine raises itself, matching the names
// the proxy layer uses for the same wire calls so a stalled hop reads the same way whoever
// produced the diagnosis.
const (
	gateL1InfoTreeIndex   = "l1-info-tree-index"
	gateInjectedLeaf      = "injected-l1-info-leaf"
	gateClaimProof        = "claim-proof"
	gateClaimed           = "claimed"
	gateManualGracePeriod = "manual-grace-period"
)

// Bridge leaf types, as recorded in a BridgeEvent: an asset deposit is claimed with claimAsset, a
// message deposit with claimMessage.
const (
	leafTypeAsset   uint8 = 0
	leafTypeMessage uint8 = 1
)

// claimProofHeight is the height of the Merkle branches GET /bridge/v1/claim-proof returns, and the
// fixed size the bridge ABI's claimAsset/claimMessage proof arguments have.
const claimProofHeight = 32

// uint256Bits is the width of an EVM word, used to build the max-uint256 ERC20 approval.
const uint256Bits = 256

// claimRecordLookupBudget bounds the best-effort GET /bridge/v1/claims cross-check the engine makes
// once a claim is observed on-chain. It is deliberately short: the record only enriches the hop's
// diagnostics (which account submitted the claim, in which transaction) and the claim syncer is
// routinely a few seconds behind the on-chain isClaimed read that already decided the hop, so this
// lookup must never hold a hop up.
const claimRecordLookupBudget = 2 * time.Second

// defaultNativeGasSlackWei is DefaultNativeGasSlack's value: 1e15 wei, i.e. 0.001 ETH.
const defaultNativeGasSlackWei uint64 = 1_000_000_000_000_000

// DefaultNativeGasSlack is the extra native-currency shortfall the destination balance check
// tolerates on a native hop, on top of the gas the tool itself provably spent on that network.
// It exists because a native balance is not exclusive to one hop: the same signing account also
// pays for whatever other loop shares it on that network. Override it with HopDeps.NativeGasSlack.
var DefaultNativeGasSlack = new(big.Int).SetUint64(defaultNativeGasSlackWei)

// maxUint256 is the allowance the engine grants the source bridge, so a soak run pays for one
// approve per (network, token) instead of one per hop.
var maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint256Bits), big.NewInt(1))

// HopRunner is the one-hop unit of work the orchestrator drives. It exists as an interface so the
// orchestrator (and its tests) can substitute a fake hop engine; *HopEngine is the implementation.
type HopRunner interface {
	// RunHop drives one hop to a terminal state and returns its record. The returned *HopResult is
	// non-nil whenever the request itself was well-formed, including when RunHop also returns an
	// error: the result then says how far the hop got and why it stopped.
	RunHop(ctx context.Context, req HopRequest) (*HopResult, error)
}

// HopNetwork bundles everything the hop engine needs for one endpoint of a hop. The NetworkClient
// must be the *shared* instance for its (network, signing key) pair: SendTx serializes nonces per
// instance, so a second client wrapping the same key on the same network produces nonce collisions
// (see NetworkClient's concurrency contract).
type HopNetwork struct {
	// Config is the network's configuration, read for MinNativeReserve and for names in messages.
	Config Network
	// Client is the shared JSON-RPC/signing client for this network.
	Client NetworkClient
	// Bridge is the bridge contract wrapper bound to Config.BridgeAddr on this network.
	Bridge Bridge
}

// HopTimings are the durations that bound one hop, taken from Global.
type HopTimings struct {
	// PollInterval is how often a readiness gate or an isClaimed check is re-polled.
	PollInterval time.Duration
	// HopTimeout is the total wall-clock budget for one hop. Every gate's deadline is the budget
	// *remaining* at the moment the gate is entered, so a hop that runs out of time always fails
	// with a *DeadlineExceededError naming the gate it was stuck on rather than with a bare
	// context timeout.
	HopTimeout time.Duration
	// ManualGracePeriod is a manual hop's negative-assertion window (nothing may claim the deposit
	// during it) and an auto hop's budget for the autoclaim service to claim it.
	ManualGracePeriod time.Duration
}

// HopTimingsFromGlobal reads the hop engine's timings out of a loaded Global config section.
func HopTimingsFromGlobal(global Global) HopTimings {
	return HopTimings{
		PollInterval:      global.PollInterval.Duration,
		HopTimeout:        global.HopTimeout.Duration,
		ManualGracePeriod: global.ManualGracePeriod.Duration,
	}
}

// HopDeps are the injected dependencies of a HopEngine. Everything the engine touches arrives
// through here, so a unit test can drive a complete hop against mocks.NetworkClient/mocks.Bridge/
// mocks.Token/mocks.Proxy without a chain or an HTTP server.
type HopDeps struct {
	// Networks holds one HopNetwork per participating network, keyed by aggkit network ID. Every
	// hop's Source and Destination must be present.
	Networks map[uint32]HopNetwork
	// Proxy is the aggkit-proxy observation layer.
	Proxy Proxy
	// Logger receives one structured line per state transition.
	Logger aggkitcommon.Logger
	// Timings bounds the hop (see HopTimings).
	Timings HopTimings
	// NativeGasSlack overrides DefaultNativeGasSlack, the extra shortfall a native hop's
	// destination balance check tolerates. Nil or negative means DefaultNativeGasSlack.
	NativeGasSlack *big.Int
	// NewTokenFn binds an ERC20 on a network. Nil means NewToken. Injected so a test can hand back
	// a mocks.Token for an address the engine discovers at runtime.
	NewTokenFn func(client NetworkClient, address common.Address) (Token, error)
	// PersistCheckpoint is called on entry to every state, *before* that state's side effect is
	// attempted, so a checkpoint on disk is never behind the chain. An error from it fails the hop:
	// continuing with an unpersisted state would silently give up resumability. Nil disables
	// persistence, in which case a hop is still resumable from HopResult.Checkpoint if the caller
	// keeps it another way.
	PersistCheckpoint func(ctx context.Context, checkpoint HopCheckpoint) error
	// Now is the clock, injected so tests need not sleep. Nil means time.Now.
	Now func() time.Time
}

// HopRequest describes the one hop to run. Everything in it is derived by the orchestrator from the
// Loop and Hop config plus the ERC20 it deployed; the engine itself reads no configuration file.
type HopRequest struct {
	// LoopName is the configured Loop.Name, recorded in the result and in every log line.
	LoopName string
	// Iteration is the 1-based cycle number, recorded for diagnostics (0 if the caller does not
	// track cycles).
	Iteration uint64
	// HopIndex is this hop's 0-based position in the loop's route.
	HopIndex int
	// Hop is the configured leg: Source, Destination and the Claim expectation.
	Hop Hop
	// Asset selects what to move (AssetETH or AssetERC20).
	Asset AssetKind
	// Amount is how much to move, in wei (ETH) or token base units (ERC20). Required, must be > 0.
	Amount *big.Int
	// TokenOriginNetwork is the network the ERC20 was deployed on. Required for AssetERC20, must be
	// nil for AssetETH.
	TokenOriginNetwork *uint32
	// TokenOriginAddress is the ERC20's address on TokenOriginNetwork. Required for AssetERC20 (the
	// engine derives the wrapped address on every other network from it), must be zero for
	// AssetETH.
	TokenOriginAddress common.Address
	// DestinationAddress is the address the deposit is made claimable by. Zero (the normal case for
	// a circular route) means the destination network's own signing account, so the value lands
	// exactly where the next hop will spend it from.
	DestinationAddress common.Address
	// Resume, when non-nil, is the checkpoint of an interrupted hop this call should continue
	// instead of starting a new one. See HopState for the per-state resume contract.
	Resume *HopCheckpoint
}

// HopEngine drives one hop of a loop from end to end, as the resumable state machine specified in
// DESIGN.md §4. It holds no per-hop state: every RunHop call is independent, and concurrent calls
// for different hops are safe as long as their HopNetworks' NetworkClients are the shared
// per-(network, key) instances.
//
// # Leaf-index threading
//
// The one subtlety the engine implements explicitly, because nothing downstream enforces it: the
// index fed into GET /bridge/v1/claim-proof must be I', the *actually injected* index that
// Proxy.WaitInjectedLeaf returned, not I, the index Proxy.WaitL1InfoTreeIndex returned. A
// concurrent bridge elsewhere can cause the aggoracle to inject a later-covering leaf first, so
// I' >= I, and proving a claim against I when I' was injected desyncs the claim's exit roots from
// what the destination really has. waitGates therefore keeps the two in separate fields
// (HopResult.L1InfoTreeIndex and HopResult.InjectedLeafIndex), passes only the latter to
// WaitClaimProof, and records HopResult.InjectedLeafAdvanced whenever they actually differed - that
// last flag exists because I' > I is exactly the condition that makes a wrong implementation fail
// intermittently instead of always.
//
// # Claim-mode expectations are assertions
//
// A hop's Claim mode is a test assertion, not a hint. An auto hop nobody claimed within
// ManualGracePeriod, and a manual hop that something else claimed during it, both fail with a
// *ClaimModeViolationError and are never retried: retrying would turn a real autoclaim-policy
// defect into an invisible delay.
type HopEngine struct {
	networks map[uint32]HopNetwork
	proxy    Proxy
	logger   aggkitcommon.Logger
	timings  HopTimings
	gasSlack *big.Int
	newToken func(client NetworkClient, address common.Address) (Token, error)
	persist  func(ctx context.Context, checkpoint HopCheckpoint) error
	now      func() time.Time
}

var _ HopRunner = (*HopEngine)(nil)

// NewHopEngine validates deps and returns a HopEngine. It refuses a configuration whose HopTimeout
// does not exceed its ManualGracePeriod: a hop must be able to sit out its whole grace period
// inside its own budget, or the negative assertion a manual hop makes would be silently truncated.
func NewHopEngine(deps HopDeps) (*HopEngine, error) {
	if len(deps.Networks) == 0 {
		return nil, fmt.Errorf("new hop engine: at least one network is required")
	}
	if deps.Proxy == nil {
		return nil, fmt.Errorf("new hop engine: proxy is required")
	}
	if deps.Logger == nil {
		return nil, fmt.Errorf("new hop engine: logger is required")
	}
	if err := validateHopNetworks(deps.Networks); err != nil {
		return nil, err
	}
	if err := validateHopTimings(deps.Timings); err != nil {
		return nil, err
	}

	engine := &HopEngine{
		networks: deps.Networks,
		proxy:    deps.Proxy,
		logger:   deps.Logger,
		timings:  deps.Timings,
		gasSlack: DefaultNativeGasSlack,
		newToken: NewToken,
		persist:  deps.PersistCheckpoint,
		now:      time.Now,
	}
	if deps.NativeGasSlack != nil && deps.NativeGasSlack.Sign() >= 0 {
		engine.gasSlack = new(big.Int).Set(deps.NativeGasSlack)
	}
	if deps.NewTokenFn != nil {
		engine.newToken = deps.NewTokenFn
	}
	if deps.Now != nil {
		engine.now = deps.Now
	}

	return engine, nil
}

// validateHopNetworks checks that every HopNetwork is complete and keyed by its own network ID.
func validateHopNetworks(networks map[uint32]HopNetwork) error {
	for networkID, network := range networks {
		switch {
		case network.Client == nil:
			return fmt.Errorf("new hop engine: network %d has no NetworkClient", networkID)
		case network.Bridge == nil:
			return fmt.Errorf("new hop engine: network %d has no Bridge", networkID)
		case network.Client.NetworkID() != networkID:
			return fmt.Errorf("new hop engine: network %d is keyed by %d but its NetworkClient reports %d",
				networkID, networkID, network.Client.NetworkID())
		}
	}

	return nil
}

// validateHopTimings checks the durations bounding a hop are usable and mutually consistent.
func validateHopTimings(timings HopTimings) error {
	switch {
	case timings.PollInterval <= 0:
		return fmt.Errorf("new hop engine: PollInterval must be > 0, got %s", timings.PollInterval)
	case timings.HopTimeout <= 0:
		return fmt.Errorf("new hop engine: HopTimeout must be > 0, got %s", timings.HopTimeout)
	case timings.ManualGracePeriod <= 0:
		return fmt.Errorf("new hop engine: ManualGracePeriod must be > 0, got %s", timings.ManualGracePeriod)
	case timings.HopTimeout <= timings.ManualGracePeriod:
		return fmt.Errorf("new hop engine: HopTimeout (%s) must exceed ManualGracePeriod (%s), otherwise a hop "+
			"cannot sit out its own grace period and a manual hop's negative assertion would be truncated",
			timings.HopTimeout, timings.ManualGracePeriod)
	}

	return nil
}

// RunHop implements HopRunner: it drives one hop through the DESIGN.md §4 state machine and returns
// its record. See HopEngine's doc comment for the leaf-index threading and claim-mode contracts.
func (e *HopEngine) RunHop(ctx context.Context, req HopRequest) (*HopResult, error) {
	run, err := e.newRun(req)
	if err != nil {
		return run.result, err
	}

	err = run.execute(ctx)
	run.finish(err)

	return run.result, err
}

// hopRun is the mutable state of one in-progress RunHop call.
type hopRun struct {
	engine *HopEngine
	req    HopRequest
	src    HopNetwork
	dst    HopNetwork
	result *HopResult

	// deadline is the wall-clock instant the hop's HopTimeout budget expires.
	deadline time.Time
	// event is the deposit's decoded BridgeEvent, the source of every claim argument.
	event *BridgeEvent
	// proof is the claim proof fetched for InjectedLeafIndex.
	proof *bridgeservicetypes.ClaimProof
	// srcToken/dstToken cache the bound ERC20s, nil for a native hop.
	srcToken Token
	dstToken Token

	phase      HopPhase
	phaseStart time.Time
	phaseOpen  bool
}

// newRun validates req and builds the run skeleton. A malformed request still yields a *HopResult
// (carrying whatever identity fields were usable) so a caller's report never has a hole in it.
func (e *HopEngine) newRun(req HopRequest) (*hopRun, error) {
	now := e.now()
	run := &hopRun{
		engine:   e,
		req:      req,
		deadline: now.Add(e.timings.HopTimeout),
		result: &HopResult{
			LoopName:           req.LoopName,
			Iteration:          req.Iteration,
			HopIndex:           req.HopIndex,
			Source:             req.Hop.Source,
			Destination:        req.Hop.Destination,
			ClaimMode:          req.Hop.Claim,
			Asset:              req.Asset,
			TokenOriginNetwork: req.TokenOriginNetwork,
			TokenOriginAddress: req.TokenOriginAddress,
			ClaimedBy:          ClaimActorNone,
			Outcome:            HopOutcomeFailed,
			FinalState:         HopStatePending,
			StartedAt:          now,
		},
	}
	if req.Amount != nil {
		run.result.Amount = new(big.Int).Set(req.Amount)
	}

	if err := run.bindNetworks(); err != nil {
		return run, err
	}
	if err := run.validateRequest(); err != nil {
		return run, err
	}

	run.result.DestinationAddress = req.DestinationAddress
	if run.result.DestinationAddress == (common.Address{}) {
		run.result.DestinationAddress = run.dst.Client.From()
	}

	return run, nil
}

// bindNetworks resolves the hop's source and destination HopNetworks.
func (r *hopRun) bindNetworks() error {
	src, ok := r.engine.networks[r.req.Hop.Source]
	if !ok {
		return fmt.Errorf("run hop %s[%d] %d->%d: no network client configured for source network %d",
			r.req.LoopName, r.req.HopIndex, r.req.Hop.Source, r.req.Hop.Destination, r.req.Hop.Source)
	}
	dst, ok := r.engine.networks[r.req.Hop.Destination]
	if !ok {
		return fmt.Errorf("run hop %s[%d] %d->%d: no network client configured for destination network %d",
			r.req.LoopName, r.req.HopIndex, r.req.Hop.Source, r.req.Hop.Destination, r.req.Hop.Destination)
	}

	r.src, r.dst = src, dst
	r.result.SourceName = src.Config.Name
	r.result.DestinationName = dst.Config.Name

	return nil
}

// validateRequest rejects a request the state machine cannot make sense of. These are programming
// errors in the caller, not run-time conditions, so they are plain errors with no sentinel.
func (r *hopRun) validateRequest() error {
	switch {
	case r.req.Hop.Source == r.req.Hop.Destination:
		return fmt.Errorf("%s: source and destination network must differ, both are %d",
			r.hopLabel(), r.req.Hop.Source)
	case r.req.Amount == nil || r.req.Amount.Sign() <= 0:
		return fmt.Errorf("%s: amount must be > 0, got %s", r.hopLabel(), bigIntString(r.req.Amount))
	case r.req.Hop.Claim != ClaimAuto && r.req.Hop.Claim != ClaimManual:
		return fmt.Errorf("%s: unknown claim mode %q, want %q or %q",
			r.hopLabel(), r.req.Hop.Claim, ClaimAuto, ClaimManual)
	}

	switch r.req.Asset {
	case AssetETH:
		if r.req.TokenOriginNetwork != nil {
			return fmt.Errorf("%s: an %q hop must not carry a TokenOriginNetwork (got %d)",
				r.hopLabel(), AssetETH, *r.req.TokenOriginNetwork)
		}
	case AssetERC20:
		if r.req.TokenOriginNetwork == nil {
			return fmt.Errorf("%s: an %q hop requires a TokenOriginNetwork", r.hopLabel(), AssetERC20)
		}
		if r.req.TokenOriginAddress == (common.Address{}) {
			return fmt.Errorf("%s: an %q hop requires a non-zero TokenOriginAddress", r.hopLabel(), AssetERC20)
		}
		if _, ok := r.engine.networks[*r.req.TokenOriginNetwork]; !ok {
			return fmt.Errorf("%s: no network client configured for TokenOriginNetwork %d",
				r.hopLabel(), *r.req.TokenOriginNetwork)
		}
	default:
		return fmt.Errorf("%s: unknown asset kind %q, want %q or %q",
			r.hopLabel(), r.req.Asset, AssetETH, AssetERC20)
	}

	return nil
}

// hopLabel renders the hop's identity for an error message.
func (r *hopRun) hopLabel() string {
	return fmt.Sprintf("run hop %s[%d] %d->%d (%s, %s)",
		r.req.LoopName, r.req.HopIndex, r.req.Hop.Source, r.req.Hop.Destination, r.req.Asset, r.req.Hop.Claim)
}

// execute drives the state machine. Its shape is the resume contract: everything before the bridge
// is skipped for a hop that already has one, and everything before the claim is skipped for a hop
// that was already claimed.
func (r *hopRun) execute(ctx context.Context) error {
	resumeFrom, err := r.prepareResume()
	if err != nil {
		return err
	}
	if resumeFrom.IsTerminal() {
		return r.replayTerminal(resumeFrom)
	}

	if err := r.resolveAsset(ctx); err != nil {
		return err
	}

	if stateHasBridge(resumeFrom) {
		if err := r.recoverBridge(ctx); err != nil {
			return err
		}
	} else {
		if err := r.readDestinationBalanceBefore(ctx); err != nil {
			return err
		}
		if err := r.checkSourceBalance(ctx); err != nil {
			return err
		}
		if err := r.approveIfNeeded(ctx); err != nil {
			return err
		}
		if err := r.submitBridge(ctx); err != nil {
			return err
		}
	}

	if resumeFrom == HopStateClaimed {
		if err := r.reconfirmResumedClaim(ctx); err != nil {
			return err
		}
	} else {
		if err := r.waitGates(ctx); err != nil {
			return err
		}
		if err := r.settleClaim(ctx, resumeFrom); err != nil {
			return err
		}
	}

	if err := r.verifyDestinationBalance(ctx); err != nil {
		return err
	}

	return r.enterState(ctx, HopStateVerified)
}

// stateHasBridge reports whether a checkpoint in state already names a mined bridge transaction,
// i.e. whether a resume must recover the deposit rather than create one.
func stateHasBridge(state HopState) bool {
	switch state {
	case HopStateBridged, HopStateWaitingOriginIndex, HopStateWaitingGERInjection,
		HopStateFetchingClaimProof, HopStateAwaitingClaim, HopStateSubmittingClaim, HopStateClaimed:
		return true
	case HopStatePending, HopStateApproving, HopStateBridging, HopStateVerified, HopStateFailed:
		return false
	default:
		return false
	}
}

// prepareResume folds a resume checkpoint into the result and decides which state to re-enter at.
// It performs no I/O: every state's actual on-chain position is re-derived later by the idempotent
// reads execute makes. It is also where the one unobservable state is refused - see
// AmbiguousResumeError.
func (r *hopRun) prepareResume() (HopState, error) {
	if r.req.Resume == nil {
		return HopStatePending, nil
	}

	checkpoint := *r.req.Resume
	state := checkpoint.State
	if state == "" {
		state = HopStatePending
	}

	r.result.Resumed = true
	r.result.ResumedFrom = state
	r.result.BridgeTxHash = checkpoint.BridgeTxHash
	r.result.ClaimTxHash = checkpoint.ClaimTxHash
	r.result.SourceTokenAddress = checkpoint.SourceTokenAddress
	r.result.DestinationTokenAddress = checkpoint.DestinationTokenAddress
	r.result.DestinationBalanceBefore = checkpoint.destinationBalanceBefore()
	if !checkpoint.StartedAt.IsZero() {
		r.result.StartedAt = checkpoint.StartedAt
	}

	switch state {
	case HopStatePending, HopStateApproving:
		// Nothing irreversible happened, or only an idempotent approve did: start over.
		return HopStatePending, nil
	case HopStateBridging:
		return "", &AmbiguousResumeError{
			State:       state,
			Source:      r.req.Hop.Source,
			Destination: r.req.Hop.Destination,
			Account:     r.src.Client.From(),
			Amount:      r.req.Amount,
		}
	case HopStateBridged, HopStateWaitingOriginIndex, HopStateWaitingGERInjection,
		HopStateFetchingClaimProof, HopStateAwaitingClaim, HopStateSubmittingClaim, HopStateClaimed:
		if checkpoint.BridgeTxHash == (common.Hash{}) {
			return "", fmt.Errorf("%s: checkpoint state %q carries no bridge transaction hash, so the deposit "+
				"cannot be re-derived; reset the checkpoint to state %q to start the hop over",
				r.hopLabel(), state, HopStatePending)
		}

		return state, nil
	case HopStateVerified, HopStateFailed:
		return state, nil
	default:
		return "", fmt.Errorf("%s: unknown checkpoint state %q", r.hopLabel(), state)
	}
}

// replayTerminal reports a hop whose checkpoint says it already reached a terminal state, without
// touching the chain again.
func (r *hopRun) replayTerminal(state HopState) error {
	r.result.FinalState = state
	r.result.States = append(r.result.States, state)

	if state == HopStateVerified {
		r.engine.logger.Infof("bridge_loop_tester: %s: resumed from a terminal checkpoint state %q, "+
			"nothing left to do", r.hopLabel(), state)

		return nil
	}

	return fmt.Errorf("%s: resumed from a terminal checkpoint state %q: a failed hop is a test result, "+
		"not something to re-drive; reset the checkpoint to state %q to run the hop again",
		r.hopLabel(), state, HopStatePending)
}

// enterState records, logs and persists a state transition. It is called on entry to each state,
// before that state's side effect is attempted, so a persisted checkpoint is never behind the
// chain. A persistence failure fails the hop: silently continuing would give up resumability.
func (r *hopRun) enterState(ctx context.Context, state HopState) error {
	r.result.States = append(r.result.States, state)
	r.result.FinalState = state
	r.result.Checkpoint = r.checkpoint(state)

	r.engine.logger.Infof("bridge_loop_tester: hop transition loop=%q iteration=%d hop=%d route=%d->%d "+
		"asset=%s claim=%s state=%s bridge_tx=%s deposit_count=%d global_index=%s leaf_index=%d "+
		"injected_leaf_index=%d claimed_by=%s elapsed=%s",
		r.req.LoopName, r.req.Iteration, r.req.HopIndex, r.req.Hop.Source, r.req.Hop.Destination,
		r.req.Asset, r.req.Hop.Claim, state, r.result.BridgeTxHash, r.result.DepositCount,
		globalIndexString(r.result.GlobalIndex), r.result.L1InfoTreeIndex, r.result.InjectedLeafIndex,
		r.result.ClaimedBy, r.engine.now().Sub(r.result.StartedAt))

	if r.engine.persist == nil {
		return nil
	}
	if err := r.engine.persist(ctx, r.result.Checkpoint); err != nil {
		return fmt.Errorf("%s: persist checkpoint for state %q: %w", r.hopLabel(), state, err)
	}

	return nil
}

// checkpoint snapshots the hop's externally-observable position, for state.
func (r *hopRun) checkpoint(state HopState) HopCheckpoint {
	checkpoint := HopCheckpoint{
		State:                   state,
		BridgeTxHash:            r.result.BridgeTxHash,
		ClaimTxHash:             r.result.ClaimTxHash,
		DepositCount:            r.result.DepositCount,
		L1InfoTreeIndex:         r.result.L1InfoTreeIndex,
		InjectedLeafIndex:       r.result.InjectedLeafIndex,
		SourceTokenAddress:      r.result.SourceTokenAddress,
		DestinationTokenAddress: r.result.DestinationTokenAddress,
		StartedAt:               r.result.StartedAt,
	}
	if r.result.DestinationBalanceBefore != nil {
		checkpoint.DestinationBalanceBefore = r.result.DestinationBalanceBefore.String()
	}

	return checkpoint
}

// startPhase closes any open phase and opens phase.
func (r *hopRun) startPhase(phase HopPhase) {
	r.endPhase()
	r.phase = phase
	r.phaseStart = r.engine.now()
	r.phaseOpen = true
}

// endPhase closes the open phase, if any, recording its duration.
func (r *hopRun) endPhase() {
	if !r.phaseOpen {
		return
	}
	r.result.Phases = append(r.result.Phases, HopPhaseTiming{
		Phase:     r.phase,
		StartedAt: r.phaseStart,
		Duration:  r.engine.now().Sub(r.phaseStart),
	})
	r.phaseOpen = false
}

// gateBudget returns the hop budget still available for a gate, or a *DeadlineExceededError naming
// the gate when the budget is already spent. Deriving every gate's deadline from the remaining
// budget is what makes a stalled hop fail with a diagnosis rather than with a bare timeout.
func (r *hopRun) gateBudget(gate, detail string) (time.Duration, error) {
	remaining := r.deadline.Sub(r.engine.now())
	if remaining <= 0 {
		return 0, &DeadlineExceededError{Gate: gate, Detail: detail, Deadline: r.engine.timings.HopTimeout}
	}

	return remaining, nil
}

// resolveAsset resolves the asset's address on both endpoints: nothing to do for a native hop, and
// for an ERC20 hop the origin token on its own network plus the bridge-computed wrapped
// representation everywhere else.
func (r *hopRun) resolveAsset(ctx context.Context) error {
	r.startPhase(PhaseResolveAsset)
	defer r.endPhase()

	if r.req.Asset != AssetERC20 {
		return nil
	}

	originNetwork := *r.req.TokenOriginNetwork

	sourceToken, err := r.tokenAddressOn(ctx, r.src, originNetwork)
	if err != nil {
		return err
	}
	destinationToken, err := r.tokenAddressOn(ctx, r.dst, originNetwork)
	if err != nil {
		return err
	}

	r.result.SourceTokenAddress = sourceToken
	r.result.DestinationTokenAddress = destinationToken

	return nil
}

// tokenAddressOn returns the ERC20's address on network: the origin address when network *is* the
// token's origin network, otherwise the wrapped representation the bridge there computes.
func (r *hopRun) tokenAddressOn(ctx context.Context, network HopNetwork, originNetwork uint32) (common.Address, error) {
	if network.Client.NetworkID() == originNetwork {
		return r.req.TokenOriginAddress, nil
	}

	wrapped, err := network.Bridge.GetTokenWrappedAddress(ctx, originNetwork, r.req.TokenOriginAddress)
	if err != nil {
		return common.Address{}, fmt.Errorf("%s: resolve the wrapped address of token %s (origin network %d) "+
			"on %s: %w", r.hopLabel(), r.req.TokenOriginAddress, originNetwork, network.Config.Name, err)
	}
	if wrapped == (common.Address{}) {
		return common.Address{}, fmt.Errorf("%s: the bridge on %s computed a zero wrapped address for token %s "+
			"(origin network %d)", r.hopLabel(), network.Config.Name, r.req.TokenOriginAddress, originNetwork)
	}

	return wrapped, nil
}

// sourceToken binds (once) the ERC20 on the source network.
func (r *hopRun) sourceToken() (Token, error) {
	if r.srcToken != nil {
		return r.srcToken, nil
	}

	token, err := r.engine.newToken(r.src.Client, r.result.SourceTokenAddress)
	if err != nil {
		return nil, fmt.Errorf("%s: bind token %s on %s: %w",
			r.hopLabel(), r.result.SourceTokenAddress, r.src.Config.Name, err)
	}
	r.srcToken = token

	return token, nil
}

// destinationToken binds (once) the ERC20 on the destination network.
func (r *hopRun) destinationToken() (Token, error) {
	if r.dstToken != nil {
		return r.dstToken, nil
	}

	token, err := r.engine.newToken(r.dst.Client, r.result.DestinationTokenAddress)
	if err != nil {
		return nil, fmt.Errorf("%s: bind token %s on %s: %w",
			r.hopLabel(), r.result.DestinationTokenAddress, r.dst.Config.Name, err)
	}
	r.dstToken = token

	return token, nil
}

// readDestinationBalanceBefore snapshots the destination balance before the hop moves anything, so
// verifyDestinationBalance can assert a delta rather than an absolute value.
//
// For an ERC20 hop this read is allowed to fail: the destination's wrapped representation is only
// deployed by the bridge on the first claim into that network, so balanceOf reverts before it
// exists. That is recorded as an unknown baseline (which degrades the later check to a non-exact
// one), not as a hop failure. A native balance read that fails is a real RPC problem and fails the
// hop.
func (r *hopRun) readDestinationBalanceBefore(ctx context.Context) error {
	balance, err := r.readDestinationBalance(ctx)
	if err == nil {
		r.result.DestinationBalanceBefore = balance
		return nil
	}
	if r.req.Asset != AssetERC20 {
		return err
	}

	r.result.BalanceNote = fmt.Sprintf("destination balance baseline unknown: reading token %s on %s failed "+
		"(expected before the bridge has deployed the wrapped representation there): %v",
		r.result.DestinationTokenAddress, r.dst.Config.Name, err)
	r.engine.logger.Debugf("bridge_loop_tester: %s: %s", r.hopLabel(), r.result.BalanceNote)

	return nil
}

// readDestinationBalance reads the hop's asset balance of the destination address on the
// destination network.
func (r *hopRun) readDestinationBalance(ctx context.Context) (*big.Int, error) {
	if r.req.Asset == AssetERC20 {
		token, err := r.destinationToken()
		if err != nil {
			return nil, err
		}
		balance, err := token.BalanceOf(ctx, r.result.DestinationAddress)
		if err != nil {
			return nil, fmt.Errorf("%s: read the destination token balance of %s on %s: %w",
				r.hopLabel(), r.result.DestinationAddress, r.dst.Config.Name, err)
		}

		return balance, nil
	}

	balance, err := r.dst.Client.NativeBalance(ctx, r.result.DestinationAddress)
	if err != nil {
		return nil, fmt.Errorf("%s: read the destination native balance of %s on %s: %w",
			r.hopLabel(), r.result.DestinationAddress, r.dst.Config.Name, err)
	}

	return balance, nil
}

// checkSourceBalance refuses to start a hop the source account cannot fund. For a native hop the
// amount and the gas float come out of the same balance, so the requirement is
// Amount + MinNativeReserve; for an ERC20 hop the amount comes out of the token balance and only
// the gas float is checked against the native one.
func (r *hopRun) checkSourceBalance(ctx context.Context) error {
	r.startPhase(PhaseBalanceCheck)
	defer r.endPhase()

	account := r.src.Client.From()
	reserve := r.src.Config.MinNativeReserve.BigInt()

	nativeBalance, err := r.src.Client.NativeBalance(ctx, account)
	if err != nil {
		return fmt.Errorf("%s: read the source native balance of %s on %s: %w",
			r.hopLabel(), account, r.src.Config.Name, err)
	}
	r.result.SourceNativeBalanceBefore = nativeBalance

	if r.req.Asset != AssetERC20 {
		required := new(big.Int).Add(r.req.Amount, reserve)
		r.result.SourceBalanceBefore = nativeBalance
		if nativeBalance.Cmp(required) < 0 {
			return &InsufficientBalanceError{
				Network: r.src.Config.Name, NetworkID: r.src.Client.NetworkID(), Account: account,
				Asset: AssetETH, Required: required, Available: nativeBalance, MinNativeReserve: reserve,
			}
		}

		return nil
	}

	if nativeBalance.Cmp(reserve) < 0 {
		return &InsufficientBalanceError{
			Network: r.src.Config.Name, NetworkID: r.src.Client.NetworkID(), Account: account,
			Asset: AssetETH, Required: reserve, Available: nativeBalance, MinNativeReserve: reserve,
		}
	}

	token, err := r.sourceToken()
	if err != nil {
		return err
	}
	tokenBalance, err := token.BalanceOf(ctx, account)
	if err != nil {
		return fmt.Errorf("%s: read the source token balance of %s on %s: %w",
			r.hopLabel(), account, r.src.Config.Name, err)
	}
	r.result.SourceBalanceBefore = tokenBalance

	if tokenBalance.Cmp(r.req.Amount) < 0 {
		return &InsufficientBalanceError{
			Network: r.src.Config.Name, NetworkID: r.src.Client.NetworkID(), Account: account,
			Asset: AssetERC20, Token: r.result.SourceTokenAddress, Required: r.req.Amount,
			Available: tokenBalance, MinNativeReserve: reserve,
		}
	}

	return nil
}

// approveIfNeeded makes sure the source bridge may move the hop's ERC20 amount. The approval it
// grants is max-uint256, so a soak run pays for one approve per (network, token) rather than one
// per hop; the allowance read in front of it is what makes that a one-off.
func (r *hopRun) approveIfNeeded(ctx context.Context) error {
	if r.req.Asset != AssetERC20 {
		return nil
	}

	r.startPhase(PhaseApprove)
	defer r.endPhase()

	token, err := r.sourceToken()
	if err != nil {
		return err
	}

	account := r.src.Client.From()
	bridgeAddr := r.src.Bridge.Address()
	allowance, err := token.Allowance(ctx, account, bridgeAddr)
	if err != nil {
		return fmt.Errorf("%s: read the allowance of the bridge %s over %s's token %s on %s: %w",
			r.hopLabel(), bridgeAddr, account, r.result.SourceTokenAddress, r.src.Config.Name, err)
	}
	r.result.AllowanceBefore = allowance

	if allowance.Cmp(r.req.Amount) >= 0 {
		return nil
	}

	if err := r.enterState(ctx, HopStateApproving); err != nil {
		return err
	}

	receipt, err := token.Approve(ctx, bridgeAddr, maxUint256)
	if err != nil {
		return fmt.Errorf("%s: approve the bridge %s for token %s on %s: %w",
			r.hopLabel(), bridgeAddr, r.result.SourceTokenAddress, r.src.Config.Name, err)
	}
	r.result.Approved = true
	r.result.ApproveTxHash = receipt.TxHash

	return nil
}

// submitBridge is DESIGN.md §4's S0: submit bridgeAsset on the source and decode the BridgeEvent
// the receipt carries, which yields the deposit count without polling any list endpoint (§5).
func (r *hopRun) submitBridge(ctx context.Context) error {
	// HopStateBridging is checkpointed before the submission precisely because its outcome is the
	// one thing a restart cannot observe - see AmbiguousResumeError.
	if err := r.enterState(ctx, HopStateBridging); err != nil {
		return err
	}

	r.startPhase(PhaseBridge)
	defer r.endPhase()

	request := BridgeAssetRequest{
		DestinationNetwork:        r.req.Hop.Destination,
		DestinationAddress:        r.result.DestinationAddress,
		Amount:                    r.req.Amount,
		ForceUpdateGlobalExitRoot: true,
	}

	var (
		bridged *BridgeResult
		err     error
	)
	if r.req.Asset == AssetERC20 {
		bridged, err = r.src.Bridge.BridgeAssetERC20(ctx, r.result.SourceTokenAddress, request)
	} else {
		bridged, err = r.src.Bridge.BridgeAssetNative(ctx, request)
	}
	if err != nil {
		return fmt.Errorf("%s: submit the bridge transaction on %s: %w", r.hopLabel(), r.src.Config.Name, err)
	}

	r.applyBridge(bridged.Receipt, &bridged.Event)

	if err := r.enterState(ctx, HopStateBridged); err != nil {
		return err
	}
	r.registerWithTracker(ctx)

	return nil
}

// recoverBridge re-derives a resumed hop's deposit from the checkpointed bridge transaction hash,
// which is the whole basis of resumability: the receipt's BridgeEvent gives back the deposit count,
// leaf type, amount and metadata without trusting anything remembered in memory (DESIGN.md §4/§5).
func (r *hopRun) recoverBridge(ctx context.Context) error {
	r.startPhase(PhaseBridge)
	defer r.endPhase()

	txHash := r.result.BridgeTxHash
	receipt, err := r.src.Client.Backend().TransactionReceipt(ctx, txHash)
	if err != nil {
		return fmt.Errorf("%s: resume: re-read the receipt of bridge tx %s on %s: %w",
			r.hopLabel(), txHash, r.src.Config.Name, err)
	}
	if receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return fmt.Errorf("%s: resume: bridge tx %s on %s was mined with a failed status, so the hop never "+
			"deposited anything; reset the checkpoint to state %q to run it again",
			r.hopLabel(), txHash, r.src.Config.Name, HopStatePending)
	}

	event, err := r.src.Bridge.BridgeEventFromReceipt(receipt)
	if err != nil {
		return fmt.Errorf("%s: resume: re-decode the BridgeEvent of tx %s on %s: %w",
			r.hopLabel(), txHash, r.src.Config.Name, err)
	}

	checkpointed := r.req.Resume.DepositCount
	r.applyBridge(receipt, event)
	if checkpointed != 0 && checkpointed != event.DepositCount {
		r.engine.logger.Warnf("bridge_loop_tester: %s: resume: checkpoint recorded deposit_count=%d but the "+
			"receipt of bridge tx %s decodes to deposit_count=%d; trusting the receipt",
			r.hopLabel(), checkpointed, txHash, event.DepositCount)
	}

	return nil
}

// applyBridge folds a bridge receipt and its decoded event into the result, including the global
// index (computed with the shared bridgesync helper via GlobalIndex, never hand-rolled).
func (r *hopRun) applyBridge(receipt *ethtypes.Receipt, event *BridgeEvent) {
	r.event = event
	r.result.BridgeTxHash = receipt.TxHash
	r.result.BridgeGasUsed = receipt.GasUsed
	if receipt.BlockNumber != nil {
		r.result.BridgeBlockNumber = receipt.BlockNumber.Uint64()
	}
	r.result.DepositCount = event.DepositCount
	r.result.LeafType = event.LeafType
	r.result.EventOriginNetwork = event.OriginNetwork
	r.result.EventOriginAddress = event.OriginAddress
	r.result.GlobalIndex = GlobalIndex(r.req.Hop.Source, event.DepositCount)
}

// registerWithTracker calls GET /tracker/v1/network/<source>/tx/<bridge tx> once, which registers
// the deposit with the bridge tracker and gives the report an independent view of its progress. It
// is strictly diagnostic (DESIGN.md §3, Gap G2): a failure is logged and the hop continues.
func (r *hopRun) registerWithTracker(ctx context.Context) {
	tracking, err := r.engine.proxy.TrackBridge(ctx, r.req.Hop.Source, r.result.BridgeTxHash)
	if err != nil {
		r.engine.logger.Debugf("bridge_loop_tester: %s: registering bridge tx %s with the tracker failed "+
			"(diagnostics only, the hop continues): %v", r.hopLabel(), r.result.BridgeTxHash, err)

		return
	}
	r.result.Tracking = tracking
}

// waitGates is DESIGN.md §4's S1 -> S2 -> S3: the three readiness gates, each bounded by the hop
// budget still remaining so a stall names the gate it was stuck on.
//
// This is where the leaf index is threaded: I comes from the l1-info-tree-index gate, I' from the
// injected-l1-info-leaf gate (which may return a later, actually-injected index), and only I' is
// passed to the claim-proof gate. See HopEngine's doc comment.
func (r *hopRun) waitGates(ctx context.Context) error {
	if err := r.enterState(ctx, HopStateWaitingOriginIndex); err != nil {
		return err
	}
	indexI, err := r.waitOriginIndex(ctx)
	if err != nil {
		return err
	}
	r.result.L1InfoTreeIndex = indexI

	// I' starts equal to I and only the injected-leaf gate can advance it. For an L1 destination
	// that gate does not exist (settlement onto L1 is the L1 GER update), so I' == I by definition.
	indexIPrime := indexI
	if r.req.Hop.Destination == mainnetNetworkID {
		r.result.InjectedLeafSkipped = true
		r.engine.logger.Debugf("bridge_loop_tester: %s: destination is L1, so the injected-l1-info-leaf gate "+
			"is skipped and the claim proof uses leaf_index=%d directly", r.hopLabel(), indexI)
	} else {
		if err := r.enterState(ctx, HopStateWaitingGERInjection); err != nil {
			return err
		}
		if indexIPrime, err = r.waitInjectedLeaf(ctx, indexI); err != nil {
			return err
		}
	}

	r.result.InjectedLeafIndex = indexIPrime
	r.result.InjectedLeafAdvanced = indexIPrime > indexI
	if r.result.InjectedLeafAdvanced {
		r.engine.logger.Infof("bridge_loop_tester: %s: the injected l1 info tree index advanced from %d to %d "+
			"(a concurrent bridge caused a later-covering leaf to be injected first); the claim proof is "+
			"fetched for %d", r.hopLabel(), indexI, indexIPrime, indexIPrime)
	}

	if err := r.enterState(ctx, HopStateFetchingClaimProof); err != nil {
		return err
	}

	return r.waitClaimProof(ctx)
}

// waitOriginIndex polls GET /bridge/v1/l1-info-tree-index for the deposit, returning I.
func (r *hopRun) waitOriginIndex(ctx context.Context) (uint32, error) {
	r.startPhase(PhaseOriginIndex)
	defer r.endPhase()

	detail := fmt.Sprintf("network_id=%d deposit_count=%d", r.req.Hop.Source, r.result.DepositCount)
	budget, err := r.gateBudget(gateL1InfoTreeIndex, detail)
	if err != nil {
		return 0, err
	}

	return r.engine.proxy.WaitL1InfoTreeIndex(ctx, r.req.Hop.Source, uint64(r.result.DepositCount),
		r.engine.timings.PollInterval, budget)
}

// waitInjectedLeaf polls GET /bridge/v1/injected-l1-info-leaf for a global exit root covering
// indexI on the destination, returning the *actually injected* index I'.
func (r *hopRun) waitInjectedLeaf(ctx context.Context, indexI uint32) (uint32, error) {
	r.startPhase(PhaseGERInjection)
	defer r.endPhase()

	detail := fmt.Sprintf("network_id=%d leaf_index=%d", r.req.Hop.Destination, indexI)
	budget, err := r.gateBudget(gateInjectedLeaf, detail)
	if err != nil {
		return 0, err
	}

	return r.engine.proxy.WaitInjectedLeaf(ctx, r.req.Hop.Destination, indexI,
		r.engine.timings.PollInterval, budget)
}

// waitClaimProof polls GET /bridge/v1/claim-proof for the deposit at the injected index I' and
// records the exit roots the claim will be proved against.
func (r *hopRun) waitClaimProof(ctx context.Context) error {
	r.startPhase(PhaseClaimProof)
	defer r.endPhase()

	// Note the argument: r.result.InjectedLeafIndex (I'), never r.result.L1InfoTreeIndex (I).
	leafIndex := r.result.InjectedLeafIndex
	detail := fmt.Sprintf("network_id=%d leaf_index=%d deposit_count=%d",
		r.req.Hop.Source, leafIndex, r.result.DepositCount)
	budget, err := r.gateBudget(gateClaimProof, detail)
	if err != nil {
		return err
	}

	proof, err := r.engine.proxy.WaitClaimProof(ctx, r.req.Hop.Source, leafIndex, r.result.DepositCount,
		r.engine.timings.PollInterval, budget)
	if err != nil {
		return err
	}

	r.proof = proof
	r.result.MainnetExitRoot = common.HexToHash(string(proof.L1InfoTreeLeaf.MainnetExitRoot))
	r.result.RollupExitRoot = common.HexToHash(string(proof.L1InfoTreeLeaf.RollupExitRoot))
	r.result.GlobalExitRoot = common.HexToHash(string(proof.L1InfoTreeLeaf.GlobalExitRoot))

	return nil
}

// settleClaim is DESIGN.md §4's S4: the claim-mode decision point. resumeFrom matters here for
// exactly one reason - a hop interrupted in HopStateSubmittingClaim may have claimed already, and
// that is the only circumstance in which a claim found on-chain is attributed to the tool rather
// than treated as someone else's.
func (r *hopRun) settleClaim(ctx context.Context, resumeFrom HopState) error {
	toolMayHaveClaimed := resumeFrom == HopStateSubmittingClaim

	if err := r.enterState(ctx, HopStateAwaitingClaim); err != nil {
		return err
	}

	claimed, err := r.isClaimed(ctx)
	if err != nil {
		return err
	}
	if claimed {
		return r.settlePreexistingClaim(ctx, toolMayHaveClaimed)
	}

	switch r.req.Hop.Claim {
	case ClaimAuto:
		return r.awaitAutoClaim(ctx)
	case ClaimManual:
		return r.awaitGracePeriodThenClaim(ctx)
	default:
		return fmt.Errorf("%s: unknown claim mode %q", r.hopLabel(), r.req.Hop.Claim)
	}
}

// reconfirmResumedClaim handles a resume from HopStateClaimed: the claim was already observed
// before the restart, so all that is left is to confirm it on-chain (cheap, and it catches a
// checkpoint that does not belong to this deposit) before verifying the balance.
func (r *hopRun) reconfirmResumedClaim(ctx context.Context) error {
	claimed, err := r.isClaimed(ctx)
	if err != nil {
		return err
	}
	if !claimed {
		return fmt.Errorf("%s: resume: checkpoint state %q claims the deposit was claimed, but "+
			"isClaimed(%d, %d) on %s reports false; reset the checkpoint to state %q to re-drive the claim",
			r.hopLabel(), HopStateClaimed, r.result.DepositCount, r.req.Hop.Source, r.dst.Config.Name,
			HopStateAwaitingClaim)
	}

	r.result.ClaimObservedAt = r.engine.now()
	r.lookupClaimRecord(ctx)
	r.result.ClaimedBy = r.attributeClaim(r.result.ClaimTxHash != (common.Hash{}))
	r.adoptRecordedClaimTxHash()

	return r.enterState(ctx, HopStateClaimed)
}

// settlePreexistingClaim handles a deposit that was already claimed when S4 was entered. For an
// auto hop that is simply the expected outcome arriving early. For a manual hop it is a violation
// unless the tool itself is the claimer, which is only possible when the hop was resumed from
// HopStateSubmittingClaim - the state that is checkpointed before the tool submits anything.
func (r *hopRun) settlePreexistingClaim(ctx context.Context, toolMayHaveClaimed bool) error {
	r.result.ClaimObservedAt = r.engine.now()
	r.lookupClaimRecord(ctx)
	r.result.ClaimedBy = r.attributeClaim(toolMayHaveClaimed)
	r.adoptRecordedClaimTxHash()

	if r.req.Hop.Claim == ClaimManual && r.result.ClaimedBy != ClaimActorTool {
		return r.claimModeViolation(r.result.ClaimedBy)
	}

	r.engine.logger.Infof("bridge_loop_tester: %s: the deposit was already claimed by %s when the claim "+
		"decision was reached (claim tx %s)", r.hopLabel(), r.result.ClaimedBy, hashString(r.result.ClaimTxHash))

	return r.enterState(ctx, HopStateClaimed)
}

// awaitAutoClaim waits out an auto hop's grace period for an autoclaim service to claim the
// deposit. Still unclaimed at the end is a *ClaimModeViolationError - the negative result this
// tool exists to detect - unless the hop's own budget truncated the window, in which case the
// honest verdict is a stalled "claimed" gate instead.
func (r *hopRun) awaitAutoClaim(ctx context.Context) error {
	r.startPhase(PhaseAutoClaimWait)
	defer r.endPhase()

	grace := r.engine.timings.ManualGracePeriod
	r.result.GracePeriod = grace

	detail := r.claimGateDetail()
	window, truncated, err := r.claimWindow(grace, gateClaimed, detail)
	if err != nil {
		return err
	}

	claimed, err := r.pollIsClaimed(ctx, window)
	if err != nil {
		return err
	}
	if !claimed {
		if truncated {
			return &DeadlineExceededError{
				Gate: gateClaimed, Detail: detail, Deadline: r.engine.timings.HopTimeout,
			}
		}

		return r.claimModeViolation(ClaimActorNone)
	}

	r.result.ClaimObservedAt = r.engine.now()
	r.lookupClaimRecord(ctx)
	r.result.ClaimedBy = r.attributeClaim(false)

	return r.enterState(ctx, HopStateClaimed)
}

// awaitGracePeriodThenClaim is a manual hop's negative assertion followed by the tool's own claim:
// nothing may claim the deposit for the whole grace period, and only then does the tool submit.
// A claim observed during the window is a *ClaimModeViolationError, never a reason to skip ahead.
func (r *hopRun) awaitGracePeriodThenClaim(ctx context.Context) error {
	r.startPhase(PhaseManualGracePeriod)

	grace := r.engine.timings.ManualGracePeriod
	r.result.GracePeriod = grace

	detail := r.claimGateDetail()
	window, truncated, err := r.claimWindow(grace, gateManualGracePeriod, detail)
	if err != nil {
		r.endPhase()

		return err
	}
	if truncated {
		r.endPhase()

		// The grace period is the assertion. A truncated window cannot support it, so report the
		// hop as stalled rather than pretend the shorter wait proved anything.
		return &DeadlineExceededError{
			Gate: gateManualGracePeriod, Detail: detail, Deadline: r.engine.timings.HopTimeout,
		}
	}

	claimed, err := r.pollIsClaimed(ctx, window)
	r.endPhase()
	if err != nil {
		return err
	}
	if claimed {
		r.result.ClaimObservedAt = r.engine.now()
		r.lookupClaimRecord(ctx)
		r.result.ClaimedBy = r.attributeClaim(false)

		return r.claimModeViolation(r.result.ClaimedBy)
	}

	return r.submitClaim(ctx)
}

// claimWindow caps a claim wait at the hop budget still remaining. It reports whether the cap
// actually bit, because a truncated window changes the verdict a still-unclaimed deposit deserves:
// a stalled gate, not a claim-mode violation.
func (r *hopRun) claimWindow(requested time.Duration, gate, detail string) (time.Duration, bool, error) {
	remaining, err := r.gateBudget(gate, detail)
	if err != nil {
		return 0, false, err
	}
	if remaining < requested {
		return remaining, true, nil
	}

	return requested, false, nil
}

// pollIsClaimed polls the destination bridge's isClaimed until it reports true or window elapses.
// A window that elapses is not an error: the caller decides what "still unclaimed" means for its
// claim mode.
func (r *hopRun) pollIsClaimed(ctx context.Context, window time.Duration) (bool, error) {
	windowCtx, cancel := context.WithTimeout(ctx, window)
	defer cancel()

	ticker := time.NewTicker(r.engine.timings.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-windowCtx.Done():
			if ctx.Err() != nil {
				return false, fmt.Errorf("%s: waiting for the deposit to be claimed on %s: %w",
					r.hopLabel(), r.dst.Config.Name, ctx.Err())
			}

			return false, nil
		case <-ticker.C:
		}

		claimed, err := r.isClaimed(ctx)
		if err != nil {
			return false, err
		}
		if claimed {
			return true, nil
		}
	}
}

// isClaimed reads the authoritative claimed/not-claimed signal: isClaimed on the destination
// bridge, keyed by the deposit count and the hop's *source* network (DESIGN.md §8) - not the
// token's origin network, which differs for a wrapped-token hop.
func (r *hopRun) isClaimed(ctx context.Context) (bool, error) {
	claimed, err := r.dst.Bridge.IsClaimed(ctx, r.result.DepositCount, r.req.Hop.Source)
	if err != nil {
		return false, fmt.Errorf("%s: read isClaimed(%d, %d) on %s: %w",
			r.hopLabel(), r.result.DepositCount, r.req.Hop.Source, r.dst.Config.Name, err)
	}

	return claimed, nil
}

// submitClaim is DESIGN.md §4's S5submit: the tool's own claimAsset/claimMessage. The state is
// checkpointed before the submission, which is what lets a restart tell "the tool may have claimed
// this" from "someone else did".
func (r *hopRun) submitClaim(ctx context.Context) error {
	if err := r.enterState(ctx, HopStateSubmittingClaim); err != nil {
		return err
	}

	r.startPhase(PhaseClaimSubmit)
	defer r.endPhase()

	request, err := r.claimRequest()
	if err != nil {
		return err
	}

	var receipt *ethtypes.Receipt
	switch r.event.LeafType {
	case leafTypeMessage:
		receipt, err = r.dst.Bridge.ClaimMessage(ctx, request)
	case leafTypeAsset:
		receipt, err = r.dst.Bridge.ClaimAsset(ctx, request)
	default:
		return fmt.Errorf("%s: the deposit's BridgeEvent carries unknown leaf type %d, so neither claimAsset "+
			"nor claimMessage applies", r.hopLabel(), r.event.LeafType)
	}
	if err != nil {
		return r.reconcileFailedClaim(ctx, err)
	}

	r.result.ClaimTxHash = receipt.TxHash
	r.result.ClaimGasUsed = receipt.GasUsed
	r.result.ClaimGasCost = receiptGasCost(receipt)
	if receipt.BlockNumber != nil {
		r.result.ClaimBlockNumber = receipt.BlockNumber.Uint64()
	}
	r.result.ClaimedBy = ClaimActorTool
	r.result.ClaimObservedAt = r.engine.now()

	// Confirm the claim through the same isClaimed query the state machine resumes on. A successful
	// claim receipt that isClaimed disagrees with would mean the deposit identity the tool tracks
	// (deposit count, source network) is not the one the bridge recorded - the exact defect that
	// would break resumability silently.
	claimed, err := r.isClaimed(ctx)
	if err != nil {
		return err
	}
	if !claimed {
		return fmt.Errorf("%s: the claim transaction %s on %s succeeded but isClaimed(%d, %d) still reports "+
			"false: the deposit identity used to track the claim does not match the bridge's",
			r.hopLabel(), receipt.TxHash, r.dst.Config.Name, r.result.DepositCount, r.req.Hop.Source)
	}

	if err := r.enterState(ctx, HopStateClaimed); err != nil {
		return err
	}
	r.lookupClaimRecord(ctx)

	return nil
}

// reconcileFailedClaim decides what a rejected claim submission means. Only one revert is
// forgivable: AlreadyClaimed, which means the deposit was claimed between the grace period ending
// and this submission landing. Even then a manual hop has still failed its assertion - something
// other than the tool claimed it - so the verdict is a claim-mode violation, not a success.
func (r *hopRun) reconcileFailedClaim(ctx context.Context, submitErr error) error {
	if !IsAlreadyClaimed(submitErr) {
		return fmt.Errorf("%s: submit the claim on %s: %w", r.hopLabel(), r.dst.Config.Name, submitErr)
	}

	claimed, err := r.isClaimed(ctx)
	if err != nil {
		return errors.Join(fmt.Errorf("%s: the claim submission on %s reverted with AlreadyClaimed: %w",
			r.hopLabel(), r.dst.Config.Name, submitErr), err)
	}
	if !claimed {
		return fmt.Errorf("%s: the claim submission on %s reverted with AlreadyClaimed but isClaimed(%d, %d) "+
			"still reports false: %w",
			r.hopLabel(), r.dst.Config.Name, r.result.DepositCount, r.req.Hop.Source, submitErr)
	}

	r.result.ClaimObservedAt = r.engine.now()
	r.lookupClaimRecord(ctx)
	r.result.ClaimedBy = r.attributeClaim(false)
	r.engine.logger.Warnf("bridge_loop_tester: %s: the claim submission lost a race and reverted with "+
		"AlreadyClaimed; the deposit is claimed by %s", r.hopLabel(), r.result.ClaimedBy)

	if r.req.Hop.Claim == ClaimManual {
		return r.claimModeViolation(r.result.ClaimedBy)
	}

	return r.enterState(ctx, HopStateClaimed)
}

// claimRequest assembles claimAsset/claimMessage's arguments: the proofs and exit roots from
// GET /bridge/v1/claim-proof, everything else from the deposit's own BridgeEvent, and the global
// index from the shared bridgesync helper.
func (r *hopRun) claimRequest() (ClaimRequest, error) {
	if r.proof == nil {
		return ClaimRequest{}, fmt.Errorf("%s: no claim proof was fetched, so the claim cannot be built",
			r.hopLabel())
	}
	if r.event == nil {
		return ClaimRequest{}, fmt.Errorf("%s: no BridgeEvent was decoded, so the claim cannot be built",
			r.hopLabel())
	}

	return ClaimRequest{
		ProofLocalExitRoot:  claimProofToBytes(r.proof.ProofLocalExitRoot),
		ProofRollupExitRoot: claimProofToBytes(r.proof.ProofRollupExitRoot),
		GlobalIndex:         r.result.GlobalIndex,
		MainnetExitRoot:     r.result.MainnetExitRoot,
		RollupExitRoot:      r.result.RollupExitRoot,
		OriginNetwork:       r.event.OriginNetwork,
		OriginAddress:       r.event.OriginAddress,
		DestinationNetwork:  r.event.DestinationNetwork,
		DestinationAddress:  r.event.DestinationAddress,
		Amount:              r.event.Amount,
		Metadata:            r.event.Metadata,
	}, nil
}

// lookupClaimRecord enriches the result with the proxy's own view of the claim (which account
// submitted it, in which transaction). Best-effort and tightly bounded: the record is a diagnostic
// that the on-chain isClaimed read has already made unnecessary for correctness, and the claim
// syncer routinely trails it by a few seconds.
func (r *hopRun) lookupClaimRecord(ctx context.Context) {
	budget := claimRecordLookupBudget
	if remaining := r.deadline.Sub(r.engine.now()); remaining <= 0 {
		return
	} else if remaining < budget {
		budget = remaining
	}

	claim, err := r.engine.proxy.WaitClaimed(ctx, r.req.Hop.Destination, r.result.GlobalIndex, budget, budget)
	if err != nil {
		r.engine.logger.Debugf("bridge_loop_tester: %s: the proxy could not name the claim of global_index=%s "+
			"on network %d within %s (diagnostics only): %v",
			r.hopLabel(), globalIndexString(r.result.GlobalIndex), r.req.Hop.Destination, budget, err)

		return
	}

	r.result.ExternalClaimTxHash = common.HexToHash(string(claim.TxHash))
	r.result.ExternalClaimFromAddress = common.HexToAddress(string(claim.FromAddress))
}

// attributeClaim decides who claimed a deposit the tool has just found claimed.
//
// toolMayHaveClaimed is true only when the tool provably reached the point of submitting a claim
// for this deposit (it submitted one in this process, or it is resuming from the state that is
// checkpointed immediately before submitting). When it is false the tool cannot have claimed, so
// the claim belongs to someone else - which is exactly what an auto hop wants and a manual hop
// forbids.
func (r *hopRun) attributeClaim(toolMayHaveClaimed bool) ClaimActor {
	claimant := r.result.ExternalClaimFromAddress
	self := r.dst.Client.From()

	if toolMayHaveClaimed {
		if claimant == (common.Address{}) || claimant == self {
			return ClaimActorTool
		}

		r.engine.logger.Warnf("bridge_loop_tester: %s: resumed from a claim submission, but the claim on record "+
			"was submitted by %s rather than by this tool's account %s", r.hopLabel(), claimant, self)

		return ClaimActorExternal
	}

	if claimant == (common.Address{}) {
		// Claimed on-chain, but the proxy could not say by whom. The tool provably did not do it.
		return ClaimActorUnknown
	}
	if claimant == self {
		r.engine.logger.Warnf("bridge_loop_tester: %s: the deposit was claimed by this tool's own account %s "+
			"even though this hop submitted no claim; something else is signing with the same key",
			r.hopLabel(), self)
	}

	return ClaimActorExternal
}

// claimModeViolation builds the hop's headline failure: the configured claim expectation did not
// hold. observed says what happened instead.
func (r *hopRun) claimModeViolation(observed ClaimActor) error {
	return &ClaimModeViolationError{
		Expected:         r.req.Hop.Claim,
		Observed:         observed,
		Source:           r.req.Hop.Source,
		Destination:      r.req.Hop.Destination,
		DepositCount:     r.result.DepositCount,
		GlobalIndex:      r.result.GlobalIndex,
		GracePeriod:      r.result.GracePeriod,
		BridgeTxHash:     r.result.BridgeTxHash,
		ClaimTxHash:      r.claimTxHashForReport(),
		ClaimFromAddress: r.result.ExternalClaimFromAddress,
		ProofAvailable:   r.proof != nil,
	}
}

// adoptRecordedClaimTxHash fills in the tool's own claim transaction hash from the proxy's claim
// record, for a hop that was attributed to the tool without this process having submitted the claim
// itself (a resume across the submission). It never overwrites a hash this process observed
// directly, and it never claims a hop the tool did not make.
func (r *hopRun) adoptRecordedClaimTxHash() {
	if r.result.ClaimedBy != ClaimActorTool || r.result.ClaimTxHash != (common.Hash{}) {
		return
	}
	r.result.ClaimTxHash = r.result.ExternalClaimTxHash
}

// claimTxHashForReport returns the most informative claim transaction hash known: the tool's own
// when it submitted one, otherwise whatever the proxy's claim record named.
func (r *hopRun) claimTxHashForReport() common.Hash {
	if r.result.ClaimTxHash != (common.Hash{}) {
		return r.result.ClaimTxHash
	}

	return r.result.ExternalClaimTxHash
}

// claimGateDetail renders the concrete inputs of a claim wait, in the query-parameter form the
// proxy layer's own gate diagnostics use, so both read the same way.
func (r *hopRun) claimGateDetail() string {
	return fmt.Sprintf("network_id=%d deposit_count=%d global_index=%s",
		r.req.Hop.Destination, r.result.DepositCount, globalIndexString(r.result.GlobalIndex))
}

// verifyDestinationBalance checks that the claim actually credited the destination address.
//
// The check is exact for an ERC20 hop and can only ever be tolerant for a native one, for a reason
// worth stating plainly: a native balance is not exclusive to the bridged value. The very same
// balance pays the gas for the claim transaction the tool just submitted, and in a soak run it also
// pays for whatever other loop shares that signing account on that network. So the arithmetic
// "after - before == amount" is true for a token (whose balance no transaction fee touches) and
// structurally false for the native currency. The native case therefore asserts what is actually
// guaranteed - value arrived, and it fell short of the hop amount by no more than the gas the tool
// provably spent plus a configured slack - and records the tolerated shortfall in the result so a
// human can see it rather than having it hidden inside a passing check.
func (r *hopRun) verifyDestinationBalance(ctx context.Context) error {
	r.startPhase(PhaseVerifyBalance)
	defer r.endPhase()

	after, err := r.readDestinationBalance(ctx)
	if err != nil {
		return err
	}
	r.result.DestinationBalanceAfter = after

	before := r.result.DestinationBalanceBefore
	if before == nil {
		return r.verifyWithoutBaseline(after)
	}

	delta := new(big.Int).Sub(after, before)
	r.result.DestinationDelta = delta

	if r.req.Asset == AssetERC20 {
		return r.verifyExactTokenDelta(delta)
	}

	return r.verifyTolerantNativeDelta(delta)
}

// verifyWithoutBaseline runs the degraded check available when the pre-hop destination balance was
// never read (a wrapped token the bridge had not deployed yet, or a resume that carried no
// baseline): the post-claim balance must at least be consistent with the credit having happened.
func (r *hopRun) verifyWithoutBaseline(after *big.Int) error {
	if r.req.Asset == AssetERC20 && after.Cmp(r.req.Amount) < 0 {
		return r.balanceMismatch(nil, after, nil, new(big.Int),
			"the destination token balance is below the hop amount, so the claim did not credit it")
	}
	if r.req.Asset != AssetERC20 && after.Sign() <= 0 {
		return r.balanceMismatch(nil, after, nil, new(big.Int),
			"the destination native balance is zero, so the claim did not credit it")
	}

	r.result.BalanceVerified = true
	r.result.BalanceExact = false
	if r.result.BalanceNote == "" {
		r.result.BalanceNote = "no pre-hop destination balance was available, so only the post-claim balance " +
			"was checked, not the exact delta"
	}

	return nil
}

// verifyExactTokenDelta asserts an ERC20 credit of exactly the hop amount. An ERC20 balance is
// untouched by transaction fees, so there is nothing to tolerate here and nothing should be.
func (r *hopRun) verifyExactTokenDelta(delta *big.Int) error {
	if delta.Cmp(r.req.Amount) != 0 {
		return r.balanceMismatch(r.result.DestinationBalanceBefore, r.result.DestinationBalanceAfter, delta,
			new(big.Int), "the destination token balance did not increase by exactly the hop amount")
	}

	r.result.BalanceVerified = true
	r.result.BalanceExact = true

	return nil
}

// verifyTolerantNativeDelta asserts a native credit that arrived and fell short by no more than the
// gas allowance. See verifyDestinationBalance for why an exact assertion is impossible here.
func (r *hopRun) verifyTolerantNativeDelta(delta *big.Int) error {
	tolerance := r.nativeTolerance()
	shortfall := new(big.Int).Sub(r.req.Amount, delta)
	before, after := r.result.DestinationBalanceBefore, r.result.DestinationBalanceAfter

	switch {
	case delta.Sign() <= 0:
		return r.balanceMismatch(before, after, delta, tolerance,
			"the destination native balance did not increase, so the claim credited nothing")
	case shortfall.Sign() > 0 && shortfall.Cmp(tolerance) > 0:
		return r.balanceMismatch(before, after, delta, tolerance,
			"the destination native credit fell short of the hop amount by more than the gas allowance")
	case shortfall.Sign() < 0:
		r.result.BalanceNote = fmt.Sprintf("the destination native balance grew by %s, %s more than the hop "+
			"amount %s: something other than this hop also credited %s on %s (tolerated - a native balance is "+
			"shared with every other loop using the same account)",
			delta, new(big.Int).Neg(shortfall), r.req.Amount, r.result.DestinationAddress, r.dst.Config.Name)
	default:
		r.result.BalanceNote = fmt.Sprintf("tolerated a native shortfall of %s against a gas allowance of %s "+
			"(claim gas cost %s plus slack %s)",
			shortfall, tolerance, bigIntString(r.result.ClaimGasCost), r.engine.gasSlack)
	}

	r.result.BalanceVerified = true
	r.result.BalanceExact = false

	return nil
}

// nativeTolerance is the native shortfall a destination balance check tolerates: the gas the tool
// provably spent claiming on that network, plus the configured slack for whatever else shares the
// account.
func (r *hopRun) nativeTolerance() *big.Int {
	tolerance := new(big.Int).Set(r.engine.gasSlack)
	if r.result.ClaimGasCost != nil {
		tolerance.Add(tolerance, r.result.ClaimGasCost)
	}

	return tolerance
}

// balanceMismatch builds a *BalanceMismatchError for the destination endpoint.
func (r *hopRun) balanceMismatch(before, after, delta, tolerance *big.Int, detail string) error {
	token := common.Address{}
	if r.req.Asset == AssetERC20 {
		token = r.result.DestinationTokenAddress
	}

	return &BalanceMismatchError{
		Network:   r.dst.Config.Name,
		NetworkID: r.dst.Client.NetworkID(),
		Account:   r.result.DestinationAddress,
		Asset:     r.req.Asset,
		Token:     token,
		Expected:  r.req.Amount,
		Before:    before,
		After:     after,
		Delta:     delta,
		Tolerance: tolerance,
		Detail:    detail,
	}
}

// finish stamps the hop's timings and verdict onto the result. HopStateFailed is a verdict, not a
// checkpoint: the checkpoint keeps the last state the hop actually reached, so a caller that wants
// to re-drive an interrupted hop still can, while a caller that wants to mark it dead can persist
// HopStateFailed itself.
func (r *hopRun) finish(err error) {
	r.endPhase()

	now := r.engine.now()
	r.result.FinishedAt = now
	r.result.Duration = now.Sub(r.result.StartedAt)

	if err == nil {
		r.result.Outcome = HopOutcomeSuccess
		r.engine.logger.Infof("bridge_loop_tester: hop completed loop=%q iteration=%d hop=%d route=%d->%d "+
			"asset=%s claim=%s claimed_by=%s deposit_count=%d global_index=%s leaf_index=%d "+
			"injected_leaf_index=%d bridge_tx=%s claim_tx=%s duration=%s",
			r.req.LoopName, r.req.Iteration, r.req.HopIndex, r.req.Hop.Source, r.req.Hop.Destination,
			r.req.Asset, r.req.Hop.Claim, r.result.ClaimedBy, r.result.DepositCount,
			globalIndexString(r.result.GlobalIndex), r.result.L1InfoTreeIndex, r.result.InjectedLeafIndex,
			r.result.BridgeTxHash, hashString(r.result.ClaimTxHash), r.result.Duration)

		return
	}

	r.result.Outcome = HopOutcomeFailed
	r.result.Err = err
	r.result.ErrMessage = err.Error()
	r.result.ClaimModeViolated = errors.Is(err, ErrClaimModeViolation)
	r.result.FinalState = HopStateFailed

	var gateErr *DeadlineExceededError
	if errors.As(err, &gateErr) {
		r.result.StalledGate = gateErr.Gate
	}

	r.engine.logger.Errorf("bridge_loop_tester: hop failed loop=%q iteration=%d hop=%d route=%d->%d asset=%s "+
		"claim=%s last_state=%s stalled_gate=%q claim_mode_violated=%t duration=%s: %v",
		r.req.LoopName, r.req.Iteration, r.req.HopIndex, r.req.Hop.Source, r.req.Hop.Destination, r.req.Asset,
		r.req.Hop.Claim, r.result.Checkpoint.State, r.result.StalledGate, r.result.ClaimModeViolated,
		r.result.Duration, err)
}

// claimProofToBytes converts a proxy claim proof's hex-string Merkle branch into the fixed-size byte
// array the bridge ABI's claimAsset/claimMessage arguments have.
func claimProofToBytes(proof bridgeservicetypes.Proof) [claimProofHeight][common.HashLength]byte {
	var out [claimProofHeight][common.HashLength]byte
	for i := range proof {
		if i >= len(out) {
			break
		}
		out[i] = common.HexToHash(string(proof[i]))
	}

	return out
}

// receiptGasCost returns the native cost of a transaction (gas used times effective gas price), or
// nil when the receipt does not report an effective gas price.
func receiptGasCost(receipt *ethtypes.Receipt) *big.Int {
	if receipt == nil || receipt.EffectiveGasPrice == nil {
		return nil
	}

	return new(big.Int).Mul(new(big.Int).SetUint64(receipt.GasUsed), receipt.EffectiveGasPrice)
}
