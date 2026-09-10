package bridgelooptester

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// FailureClass classifies a hop failure by what the orchestrator must do about it. It is the
// vocabulary of the failure policy documented on Orchestrator: everything the tool can do wrong
// falls into exactly one of these, and each one has a single, fixed response.
type FailureClass string

const (
	// FailureNone means no failure.
	FailureNone FailureClass = ""
	// FailureTransient is anything that could plausibly succeed on a second attempt: a readiness
	// gate that timed out, an RPC or proxy error, a destination balance that did not reconcile.
	// Retried in place, then left for the next cycle to resume - never fatal to the loop.
	FailureTransient FailureClass = "transient"
	// FailureClaimMode is a *ClaimModeViolationError: an "auto" hop nobody claimed, or a "manual"
	// hop something else claimed. This is the assertion the tool exists to make, so it is a test
	// failure and always fatal to its loop - retrying would turn a real autoclaim-policy defect
	// into an invisible delay.
	FailureClaimMode FailureClass = "claim-mode-violation"
	// FailureAmbiguousResume is an *AmbiguousResumeError: a signed bridge transaction with no
	// receipt whose nonce a different transaction consumed. Fatal to its loop and reported with
	// the full decision evidence - continuing could double-bridge, and skipping the hop would
	// abandon value on an intermediate network.
	FailureAmbiguousResume FailureClass = "ambiguous-resume"
	// FailureInsufficientBalance is an *InsufficientBalanceError: the signer cannot fund the hop
	// without dipping into MinNativeReserve, or holds too little of the asset. Fatal to its loop:
	// a soak run has no way to top itself up, so retrying for days would only fill the log.
	FailureInsufficientBalance FailureClass = "insufficient-balance"
	// FailureConfiguration is a malformed request or a network the engine does not know about -
	// a bug or a bad config, not something a retry fixes. Fatal to its loop.
	FailureConfiguration FailureClass = "configuration"
	// FailureCancelled means the run's context was cancelled (SIGINT/SIGTERM, or the caller's
	// own deadline). Not a failure of the tool or the bridge: the loop stops cleanly.
	FailureCancelled FailureClass = "cancelled"
)

// Fatal reports whether a failure of this class permanently halts the loop it happened in, as
// opposed to being retried and resumed.
func (c FailureClass) Fatal() bool {
	switch c {
	case FailureClaimMode, FailureAmbiguousResume, FailureInsufficientBalance, FailureConfiguration:
		return true
	case FailureNone, FailureTransient, FailureCancelled:
		return false
	default:
		return true
	}
}

// ClassifyHopFailure maps a hop error onto the failure policy's classes. It matches on the typed
// sentinels the hop engine and the proxy layer export, never on message text.
func ClassifyHopFailure(err error) FailureClass {
	switch {
	case err == nil:
		return FailureNone
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return FailureCancelled
	case errors.Is(err, ErrClaimModeViolation):
		return FailureClaimMode
	case errors.Is(err, ErrAmbiguousResume):
		return FailureAmbiguousResume
	case errors.Is(err, ErrInsufficientBalance):
		return FailureInsufficientBalance
	default:
		return FailureTransient
	}
}

// ValueLocation says where a loop's value currently sits. It exists because a route is circular:
// a healthy loop is always at rest on its origin network between cycles, so any other resting
// place means a cycle stopped part-way round and left value behind. Distinguishing the two from
// the outside is otherwise guesswork, and it is the first thing an operator needs to know.
type ValueLocation struct {
	// NetworkID is the network the value sits on (for a stranded in-flight hop, its source: the
	// deposit has left the source's balance but has not been claimed on the destination).
	NetworkID uint32 `json:"network_id"`
	// NetworkName is that network's configured name.
	NetworkName string `json:"network_name,omitempty"`
	// Stranded is true when the value is *not* resting on the loop's origin network, i.e. a cycle
	// stopped mid-ring.
	Stranded bool `json:"stranded"`
	// InFlight is true when the value has left the source network but has not yet been claimed on
	// the destination - the deposit exists on the bridge and the hop can be resumed at its claim
	// gate.
	InFlight bool `json:"in_flight"`
	// HopIndex is the hop the next cycle would resume at.
	HopIndex int `json:"hop_index"`
	// Detail is a one-line human summary, suitable for `status` output.
	Detail string `json:"detail,omitempty"`
}

// String renders the location for a log line or `status` output.
func (v ValueLocation) String() string {
	if v.Detail != "" {
		return v.Detail
	}

	return fmt.Sprintf("network %d (%s)", v.NetworkID, v.NetworkName)
}

// HopPlan is what a DryRun records instead of a HopResult: the hop the tool *would* have run,
// with every value it resolved from live reads (token addresses, balances) but no transaction.
type HopPlan struct {
	// LoopName, HopIndex and the route identify the planned hop.
	LoopName        string    `json:"loop_name"`
	HopIndex        int       `json:"hop_index"`
	Source          uint32    `json:"source"`
	SourceName      string    `json:"source_name"`
	Destination     uint32    `json:"destination"`
	DestinationName string    `json:"destination_name"`
	Claim           ClaimMode `json:"claim_mode"`
	// Asset and Amount are what would move.
	Asset  AssetKind `json:"asset"`
	Amount *big.Int  `json:"amount"`
	// SourceTokenAddress and DestinationTokenAddress are the resolved ERC20 addresses (zero for
	// a native hop).
	SourceTokenAddress      common.Address `json:"source_token_address,omitempty"`
	DestinationTokenAddress common.Address `json:"destination_token_address,omitempty"`
	// From is the account that would sign the bridge transaction on Source.
	From common.Address `json:"from"`
	// SourceBalance is the live balance of the asset on Source, and SourceNativeBalance the live
	// native balance there (gas).
	SourceBalance       *big.Int `json:"source_balance,omitempty"`
	SourceNativeBalance *big.Int `json:"source_native_balance,omitempty"`
	// Fundable reports whether SourceBalance covers Amount without dipping into
	// MinNativeReserve; Note explains it when it does not, or carries any other caveat.
	Fundable bool   `json:"fundable"`
	Note     string `json:"note,omitempty"`
}

// Route renders the planned hop's route as "<source>-><destination>".
func (p HopPlan) Route() string {
	return fmt.Sprintf("%d->%d", p.Source, p.Destination)
}

// CycleReport is one pass around a loop's ring, with the full HopResult of every hop it ran, in
// execution order. Nothing is summarised away: every field the hop engine recorded (per-phase
// timings, InjectedLeafAdvanced, ClaimedBy, checkpoints) is still here.
type CycleReport struct {
	// Iteration is the 1-based cycle number this pass was.
	Iteration uint64 `json:"iteration"`
	// StartedAt, FinishedAt and Duration bound the pass.
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
	Duration   time.Duration `json:"duration"`
	// StartHopIndex is the hop this pass began at: 0 for a healthy loop, i > 0 for one resuming
	// a ring a previous pass left stranded.
	StartHopIndex int `json:"start_hop_index"`
	// Hops holds one entry per hop attempt, in order. A hop retried after a transient failure
	// contributes one entry per attempt, so the record shows the retries rather than hiding them.
	Hops []*HopResult `json:"hops"`
	// RingClosed is true when this pass ran every remaining hop successfully and the value is
	// back on the loop's origin network.
	RingClosed bool `json:"ring_closed"`
	// FailureClass and Err describe why the pass stopped, when it did not close the ring.
	FailureClass FailureClass `json:"failure_class,omitempty"`
	Err          string       `json:"error,omitempty"`
}

// LoopReport aggregates everything one loop did during a run.
type LoopReport struct {
	// Name, Asset, Amount and Route describe the configured loop.
	Name   string    `json:"name"`
	Asset  AssetKind `json:"asset"`
	Amount *big.Int  `json:"amount"`
	Route  []string  `json:"route"`
	// TokenOriginNetwork and TokenAddress record the ERC20 the loop moved (zero/nil for ETH).
	TokenOriginNetwork *uint32        `json:"token_origin_network,omitempty"`
	TokenAddress       common.Address `json:"token_address,omitempty"`
	TokenDeployed      bool           `json:"token_deployed"`
	// Cycles holds one entry per ring pass attempted, each carrying its hops' full HopResults.
	Cycles []CycleReport `json:"cycles"`
	// Plan holds the hops a DryRun would have run; empty outside DryRun.
	Plan []HopPlan `json:"plan,omitempty"`
	// CyclesAttempted and CyclesCompleted count ring passes started and closed.
	CyclesAttempted uint64 `json:"cycles_attempted"`
	CyclesCompleted uint64 `json:"cycles_completed"`
	// Halted, HaltClass and Err record a permanent stop (see FailureClass.Fatal).
	Halted    bool         `json:"halted"`
	HaltClass FailureClass `json:"halt_class,omitempty"`
	Err       string       `json:"error,omitempty"`
	// ValueLocation says where the loop's value ended up: on its origin (healthy) or stranded
	// part-way round the ring.
	ValueLocation ValueLocation `json:"value_location"`
}

// AllHops returns every HopResult the loop produced, across every cycle, in execution order.
func (l *LoopReport) AllHops() []*HopResult {
	var hops []*HopResult
	for i := range l.Cycles {
		hops = append(hops, l.Cycles[i].Hops...)
	}

	return hops
}

// Cycle returns the report of the given 1-based iteration, or nil when the loop never ran it.
func (l *LoopReport) Cycle(iteration uint64) *CycleReport {
	for i := range l.Cycles {
		if l.Cycles[i].Iteration == iteration {
			return &l.Cycles[i]
		}
	}

	return nil
}

// ReportTotals are the run-level counters, aggregated across every loop.
type ReportTotals struct {
	// LoopsRun is how many enabled loops were driven; LoopsHalted how many stopped permanently.
	LoopsRun    int `json:"loops_run"`
	LoopsHalted int `json:"loops_halted"`
	// CyclesAttempted and CyclesCompleted sum the per-loop counters.
	CyclesAttempted uint64 `json:"cycles_attempted"`
	CyclesCompleted uint64 `json:"cycles_completed"`
	// HopsAttempted counts hop attempts (retries included); HopsSucceeded and HopsFailed split
	// them by outcome.
	HopsAttempted   int `json:"hops_attempted"`
	HopsSucceeded   int `json:"hops_succeeded"`
	HopsFailed      int `json:"hops_failed"`
	HopsRetried     int `json:"hops_retried"`
	HopsResumed     int `json:"hops_resumed"`
	AutoClaimHops   int `json:"auto_claim_hops"`
	ManualClaimHops int `json:"manual_claim_hops"`
	// ClaimedByTool and ClaimedExternally split successful hops by who actually claimed them.
	ClaimedByTool       int `json:"claimed_by_tool"`
	ClaimedExternally   int `json:"claimed_externally"`
	ClaimModeViolations int `json:"claim_mode_violations"`
	AmbiguousResumes    int `json:"ambiguous_resumes"`
	// InjectedLeafAdvanced counts hops where the actually-injected L1 info tree leaf index (I')
	// exceeded the polled one (I) - the race that silently breaks a wrong implementation.
	InjectedLeafAdvanced int `json:"injected_leaf_advanced"`
	// StrandedLoops counts loops whose value did not end up back on their origin network.
	StrandedLoops int `json:"stranded_loops"`
}

// Report is the machine-readable record of one Run: every hop of every cycle of every loop, plus
// run-level totals. It is what an in-process caller (notably the e2e test) asserts on instead of
// scraping logs, and it round-trips through JSON.
type Report struct {
	// StartedAt, FinishedAt and Duration bound the run.
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
	Duration   time.Duration `json:"duration"`
	// DryRun is true when no transaction was submitted (see Global.DryRun): Loops carry Plan
	// entries instead of Cycles.
	DryRun bool `json:"dry_run"`
	// Iterations is the configured cycle bound (0 = unbounded).
	Iterations uint64 `json:"iterations"`
	// Cancelled is true when the run stopped because its context was cancelled (SIGINT/SIGTERM)
	// rather than because it finished its Iterations.
	Cancelled bool `json:"cancelled"`
	// Loops holds one report per enabled loop, in configuration order.
	Loops []LoopReport `json:"loops"`
	// Totals aggregates the whole run.
	Totals ReportTotals `json:"totals"`
	// Preflight records the live read-only checks the run performed before driving any hop.
	Preflight *PreflightReport `json:"preflight,omitempty"`
	// Err is the aggregated message of every fatal loop failure, empty when the run was clean.
	Err string `json:"error,omitempty"`
}

// Loop returns the named loop's report, or nil when the run did not include it.
func (r *Report) Loop(name string) *LoopReport {
	for i := range r.Loops {
		if r.Loops[i].Name == name {
			return &r.Loops[i]
		}
	}

	return nil
}

// AllHops returns every HopResult of the run, loop by loop and cycle by cycle, in the order the
// hops were executed within each loop.
func (r *Report) AllHops() []*HopResult {
	var hops []*HopResult
	for i := range r.Loops {
		hops = append(hops, r.Loops[i].AllHops()...)
	}

	return hops
}

// Succeeded reports whether every loop of the run ran without a fatal failure and without a
// failed hop. A cancelled run that had done nothing wrong up to the cancellation still succeeds:
// stopping on a signal is not a test result.
func (r *Report) Succeeded() bool {
	return r.Err == "" && r.Totals.HopsFailed == 0 && r.Totals.LoopsHalted == 0
}

// Summary renders the run in one line, for the end of a log or a CLI's last word.
func (r *Report) Summary() string {
	return fmt.Sprintf("loops=%d halted=%d cycles=%d/%d hops=%d ok=%d failed=%d "+
		"claim_mode_violations=%d stranded=%d duration=%s",
		r.Totals.LoopsRun, r.Totals.LoopsHalted, r.Totals.CyclesCompleted, r.Totals.CyclesAttempted,
		r.Totals.HopsAttempted, r.Totals.HopsSucceeded, r.Totals.HopsFailed,
		r.Totals.ClaimModeViolations, r.Totals.StrandedLoops, r.Duration)
}

// computeTotals recomputes Totals from Loops. Called once, when the run finishes.
func (r *Report) computeTotals() {
	totals := ReportTotals{LoopsRun: len(r.Loops)}

	for i := range r.Loops {
		loop := &r.Loops[i]
		totals.CyclesAttempted += loop.CyclesAttempted
		totals.CyclesCompleted += loop.CyclesCompleted
		if loop.Halted {
			totals.LoopsHalted++
		}
		if loop.HaltClass == FailureAmbiguousResume {
			totals.AmbiguousResumes++
		}
		if loop.ValueLocation.Stranded {
			totals.StrandedLoops++
		}
		totals.accumulateHops(loop.AllHops())
	}

	r.Totals = totals
}

// accumulateHops folds a loop's hop results into the run totals.
func (t *ReportTotals) accumulateHops(hops []*HopResult) {
	seen := map[string]int{}

	for _, hop := range hops {
		t.HopsAttempted++
		key := fmt.Sprintf("%d/%d/%d", hop.Iteration, hop.HopIndex, hop.Source)
		seen[key]++
		if seen[key] > 1 {
			t.HopsRetried++
		}
		if hop.Resumed {
			t.HopsResumed++
		}
		if hop.InjectedLeafAdvanced {
			t.InjectedLeafAdvanced++
		}
		switch hop.ClaimMode {
		case ClaimAuto:
			t.AutoClaimHops++
		case ClaimManual:
			t.ManualClaimHops++
		}
		switch hop.ClaimedBy {
		case ClaimActorTool:
			t.ClaimedByTool++
		case ClaimActorExternal:
			t.ClaimedExternally++
		case ClaimActorNone, ClaimActorUnknown:
		}
		if hop.Succeeded() {
			t.HopsSucceeded++
		} else {
			t.HopsFailed++
		}
		if hop.ClaimModeViolated {
			t.ClaimModeViolations++
		}
	}
}
