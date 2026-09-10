package bridgelooptester

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	trackerapi "github.com/agglayer/aggkit/bridgetracker/api"
	"github.com/ethereum/go-ethereum/common"
)

// ClaimActor identifies who claimed a hop's deposit on the destination network. It is the
// observable that makes a hop's Claim mode a real assertion rather than a comment.
type ClaimActor string

const (
	// ClaimActorNone means the deposit was not claimed at all.
	ClaimActorNone ClaimActor = "none"
	// ClaimActorTool means this tool submitted the claim itself (a manual hop's expected outcome,
	// and the only actor a manual hop is allowed to observe).
	ClaimActorTool ClaimActor = "tool"
	// ClaimActorExternal means something other than this tool claimed the deposit - an autoclaim
	// service, in the topology this tool is built to test (an auto hop's expected outcome, and a
	// manual hop's failure).
	ClaimActorExternal ClaimActor = "external"
	// ClaimActorUnknown means the deposit is claimed on-chain but the claimer could not be
	// attributed: the tool did not submit the claim in this process's lifetime, and neither the
	// destination chain's own ClaimEvent log nor the proxy's /bridge/v1/claims record named the
	// claim transaction whose signature would identify the claimer. On a hop this process drove
	// end to end the log is always in the window it scans, so this is in practice reachable only
	// on a resumed hop (whose claim predates the window) or against a node that will not serve
	// logs at all.
	ClaimActorUnknown ClaimActor = "unknown"
)

// ClaimAttributionSource records where a claim's identity - its transaction, and through that
// transaction's signature its sender - was read from. It is a diagnostic, but a load-bearing one:
// it is what distinguishes an attribution the destination chain itself vouches for from one that
// depended on an indexer, and therefore what makes a run's ClaimedBy values auditable.
type ClaimAttributionSource string

const (
	// ClaimAttributionNone means nothing named the claim transaction.
	ClaimAttributionNone ClaimAttributionSource = ""
	// ClaimAttributionSelf means the tool submitted the claim and took the transaction from its own
	// receipt.
	ClaimAttributionSelf ClaimAttributionSource = "self"
	// ClaimAttributionChain means the destination bridge's own ClaimEvent/DetailedClaimEvent log
	// named the claim transaction. Authoritative and available in the same block that flips
	// isClaimed, with no indexer in the path.
	ClaimAttributionChain ClaimAttributionSource = "chain"
	// ClaimAttributionProxy means the proxy's GET /bridge/v1/claims record named the claim
	// transaction, the fallback used when the chain's log could not be located (a resumed hop, or a
	// node that refused the log query).
	ClaimAttributionProxy ClaimAttributionSource = "proxy"
)

// HopOutcome is a hop's verdict, the field a report or a metric buckets on.
type HopOutcome string

const (
	// HopOutcomeSuccess means the hop bridged, was claimed by the expected actor, and its
	// destination balance reconciled.
	HopOutcomeSuccess HopOutcome = "success"
	// HopOutcomeFailed means the hop did not complete: a readiness gate timed out, a claim-mode
	// expectation was violated, a transaction reverted, a balance did not reconcile, or a
	// dependency errored.
	HopOutcomeFailed HopOutcome = "failed"
)

// HopResult is the full record of one attempted hop: what was moved, where, by whom, how long each
// phase took, and what the verdict was. It is the tool's unit of diagnostics - a soak run's whole
// value is in these records - so it errs on the side of recording more than any single consumer
// needs. RunHop returns a non-nil *HopResult even when it also returns an error: the result then
// describes how far the hop got and why it stopped.
type HopResult struct {
	// --- identity -----------------------------------------------------------------------------

	// LoopName is the configured Loop.Name this hop belongs to.
	LoopName string `json:"loop_name"`
	// Iteration is the 1-based cycle number of the loop this hop was part of (0 when the caller
	// does not track cycles).
	Iteration uint64 `json:"iteration"`
	// HopIndex is the 0-based position of this hop within the loop's Hops slice.
	HopIndex int `json:"hop_index"`
	// Source is the network the bridge transaction was submitted on.
	Source uint32 `json:"source"`
	// SourceName is the configured human-readable name of the source network.
	SourceName string `json:"source_name"`
	// Destination is the network the deposit is claimable on.
	Destination uint32 `json:"destination"`
	// DestinationName is the configured human-readable name of the destination network.
	DestinationName string `json:"destination_name"`
	// ClaimMode is the hop's configured claim expectation (ClaimAuto or ClaimManual).
	ClaimMode ClaimMode `json:"claim_mode"`

	// --- asset --------------------------------------------------------------------------------

	// Asset is the kind of value moved (AssetETH or AssetERC20).
	Asset AssetKind `json:"asset"`
	// Amount is how much was bridged, in wei (ETH) or token base units (ERC20).
	Amount *big.Int `json:"amount"`
	// TokenOriginNetwork is the ERC20's origin network, nil for an ETH hop.
	TokenOriginNetwork *uint32 `json:"token_origin_network,omitempty"`
	// TokenOriginAddress is the ERC20's address on its origin network, zero for an ETH hop.
	TokenOriginAddress common.Address `json:"token_origin_address,omitempty"`
	// SourceTokenAddress is the asset's address on the source network - the origin ERC20 when the
	// source *is* the origin network, otherwise its wrapped representation. Zero for an ETH hop.
	SourceTokenAddress common.Address `json:"source_token_address,omitempty"`
	// DestinationTokenAddress is the asset's address on the destination network. Zero for an ETH
	// hop.
	DestinationTokenAddress common.Address `json:"destination_token_address,omitempty"`
	// DestinationAddress is the address the deposit was made claimable by.
	DestinationAddress common.Address `json:"destination_address"`

	// --- approve ------------------------------------------------------------------------------

	// AllowanceBefore is the ERC20 allowance the source bridge held before the hop, nil for an ETH
	// hop or when it was never read.
	AllowanceBefore *big.Int `json:"allowance_before,omitempty"`
	// Approved reports whether this hop had to submit an approve transaction.
	Approved bool `json:"approved"`
	// ApproveTxHash is the approve transaction's hash, zero when none was submitted.
	ApproveTxHash common.Hash `json:"approve_tx_hash,omitempty"`

	// --- bridge -------------------------------------------------------------------------------

	// SourceBalanceBefore is the source balance read before bridging (native balance for an ETH
	// hop, token balance for an ERC20 hop), nil when it was never read.
	SourceBalanceBefore *big.Int `json:"source_balance_before,omitempty"`
	// SourceNativeBalanceBefore is the source network's native balance before bridging, always
	// read (it is what MinNativeReserve is checked against). Nil when it was never read.
	SourceNativeBalanceBefore *big.Int `json:"source_native_balance_before,omitempty"`
	// BridgeTxHash is the bridgeAsset transaction's hash, zero when nothing was signed. It is set
	// from the network layer's pre-broadcast hook, so it is populated even for a submission that
	// failed before or during the broadcast.
	BridgeTxHash common.Hash `json:"bridge_tx_hash,omitempty"`
	// BridgeTxNonce is the account nonce the bridge transaction consumes, meaningful only when
	// BridgeTxHash is non-zero.
	BridgeTxNonce uint64 `json:"bridge_tx_nonce,omitempty"`
	// BridgeBlockNumber is the block the bridge transaction was mined in.
	BridgeBlockNumber uint64 `json:"bridge_block_number,omitempty"`
	// BridgeGasUsed is the gas the bridge transaction consumed.
	BridgeGasUsed uint64 `json:"bridge_gas_used,omitempty"`
	// DepositCount is the deposit's index in the source network's local exit tree, decoded from
	// the bridge receipt's BridgeEvent (DESIGN.md §5).
	DepositCount uint32 `json:"deposit_count"`
	// LeafType is the deposit's leaf type: 0 for an asset deposit (claimAsset), 1 for a message
	// deposit (claimMessage).
	LeafType uint8 `json:"leaf_type"`
	// GlobalIndex is the deposit's bridge global index, from GlobalIndex(Source, DepositCount).
	GlobalIndex *big.Int `json:"global_index,omitempty"`
	// EventOriginNetwork is the *token's* origin network as recorded in the BridgeEvent, which is
	// not the hop's Source for a wrapped-token hop.
	EventOriginNetwork uint32 `json:"event_origin_network"`
	// EventOriginAddress is the token's address on EventOriginNetwork, as recorded in the
	// BridgeEvent.
	EventOriginAddress common.Address `json:"event_origin_address,omitempty"`

	// --- readiness gates ----------------------------------------------------------------------

	// L1InfoTreeIndex is I: the L1 info tree index GET /bridge/v1/l1-info-tree-index returned for
	// the deposit.
	L1InfoTreeIndex uint32 `json:"l1_info_tree_index"`
	// InjectedLeafIndex is I': the *actually injected* L1 info tree index
	// GET /bridge/v1/injected-l1-info-leaf returned, which may exceed L1InfoTreeIndex when a
	// concurrent bridge caused a later-covering leaf to be injected first. This is the index fed
	// into GET /bridge/v1/claim-proof - see HopEngine's doc comment.
	InjectedLeafIndex uint32 `json:"injected_leaf_index"`
	// InjectedLeafAdvanced reports whether I' > I actually happened on this hop, i.e. whether the
	// leaf-index threading mattered here. Recorded because it is the one condition that makes a
	// wrong implementation fail intermittently rather than always.
	InjectedLeafAdvanced bool `json:"injected_leaf_advanced"`
	// InjectedLeafSkipped reports whether the injected-leaf gate was skipped because the
	// destination is L1, in which case I' == I by definition (DESIGN.md §2).
	InjectedLeafSkipped bool `json:"injected_leaf_skipped"`
	// MainnetExitRoot is the claim proof's L1 info tree leaf mainnet exit root.
	MainnetExitRoot common.Hash `json:"mainnet_exit_root,omitempty"`
	// RollupExitRoot is the claim proof's L1 info tree leaf rollup exit root.
	RollupExitRoot common.Hash `json:"rollup_exit_root,omitempty"`
	// GlobalExitRoot is the claim proof's L1 info tree leaf global exit root.
	GlobalExitRoot common.Hash `json:"global_exit_root,omitempty"`

	// --- claim --------------------------------------------------------------------------------

	// ClaimedBy attributes the claim (see ClaimActor). This is the field a claim-mode assertion is
	// made on: ClaimActorExternal is expected for ClaimAuto, ClaimActorTool for ClaimManual.
	ClaimedBy ClaimActor `json:"claimed_by"`
	// ClaimAttribution says where the claim's identity came from (see ClaimAttributionSource). It
	// is the audit trail behind ClaimedBy: ClaimAttributionChain means the destination chain's own
	// ClaimEvent log named the claim transaction, ClaimAttributionProxy means only the proxy's
	// claim record could, and ClaimAttributionNone means neither could.
	ClaimAttribution ClaimAttributionSource `json:"claim_attribution,omitempty"`
	// ClaimTxHash is the tool's own claim transaction hash, zero when the tool did not claim.
	ClaimTxHash common.Hash `json:"claim_tx_hash,omitempty"`
	// ClaimBlockNumber is the block the tool's claim transaction was mined in.
	ClaimBlockNumber uint64 `json:"claim_block_number,omitempty"`
	// ClaimGasUsed is the gas the tool's claim transaction consumed.
	ClaimGasUsed uint64 `json:"claim_gas_used,omitempty"`
	// ClaimGasCost is ClaimGasUsed times the claim receipt's effective gas price - the native
	// shortfall the destination balance check must tolerate on a native hop the tool claimed. Nil
	// when the tool did not claim.
	ClaimGasCost *big.Int `json:"claim_gas_cost,omitempty"`
	// ExternalClaimTxHash is the claim transaction hash the proxy's /bridge/v1/claims record
	// reported, when it was readable. Populated for cross-checking an autoclaim service's work,
	// and left zero when the record was not readable (it is a diagnostic, never a gate).
	ExternalClaimTxHash common.Hash `json:"external_claim_tx_hash,omitempty"`
	// ExternalClaimFromAddress is the from_address of the proxy's /bridge/v1/claims record, i.e.
	// which account actually submitted the claim, when it was readable.
	ExternalClaimFromAddress common.Address `json:"external_claim_from_address,omitempty"`
	// GracePeriod is the ManualGracePeriod actually applied (a manual hop's negative-assertion
	// window, or an auto hop's budget for the autoclaim service).
	GracePeriod time.Duration `json:"grace_period,omitempty"`
	// ClaimObservedAt is when the deposit was first observed claimed on the destination bridge.
	ClaimObservedAt time.Time `json:"claim_observed_at,omitempty"`

	// --- destination balance ------------------------------------------------------------------

	// DestinationBalanceBefore is the destination balance before the hop moved anything, nil when
	// it could not be read (a wrapped-token contract the destination bridge has not deployed yet)
	// or when a resume did not carry it.
	DestinationBalanceBefore *big.Int `json:"destination_balance_before,omitempty"`
	// DestinationBalanceAfter is the destination balance after the claim was observed.
	DestinationBalanceAfter *big.Int `json:"destination_balance_after,omitempty"`
	// DestinationDelta is DestinationBalanceAfter - DestinationBalanceBefore, nil when the before
	// value was unknown.
	DestinationDelta *big.Int `json:"destination_delta,omitempty"`
	// BalanceExact reports whether the delta was asserted to equal Amount exactly. True only for
	// an ERC20 hop with a known before value: an ERC20 balance is untouched by gas. A native hop
	// can never be exact - see HopEngine.verifyDestinationBalance.
	BalanceExact bool `json:"balance_exact"`
	// BalanceVerified reports whether the destination balance check ran and passed.
	BalanceVerified bool `json:"balance_verified"`
	// BalanceNote explains a check that was degraded or skipped (e.g. an unknown before value), or
	// records the tolerated native shortfall. Empty when the exact check ran cleanly.
	BalanceNote string `json:"balance_note,omitempty"`

	// --- outcome and diagnostics --------------------------------------------------------------

	// Outcome is the hop's verdict.
	Outcome HopOutcome `json:"outcome"`
	// FinalState is the state the hop ended in (HopStateVerified on success).
	FinalState HopState `json:"final_state"`
	// States is the ordered trail of states the hop actually entered, for reconstructing what
	// happened without correlating log lines.
	States []HopState `json:"states"`
	// Phases is the ordered per-phase timing breakdown.
	Phases []HopPhaseTiming `json:"phases"`
	// StartedAt is when the hop started (preserved across a resume).
	StartedAt time.Time `json:"started_at"`
	// FinishedAt is when the hop reached a terminal state.
	FinishedAt time.Time `json:"finished_at"`
	// Duration is FinishedAt - StartedAt.
	Duration time.Duration `json:"duration"`
	// Resumed reports whether this hop was resumed from a checkpoint rather than started fresh.
	Resumed bool `json:"resumed"`
	// ResumedFrom is the checkpoint state a resumed hop re-entered at.
	ResumedFrom HopState `json:"resumed_from,omitempty"`
	// Err is the error that ended the hop, nil on success. Identical to the error RunHop returned.
	Err error `json:"-"`
	// ErrMessage is Err's message, so a JSON-marshalled report keeps the failure reason.
	ErrMessage string `json:"error,omitempty"`
	// StalledGate names the readiness gate that never became satisfied, when the hop failed with a
	// *DeadlineExceededError ("l1-info-tree-index", "injected-l1-info-leaf", "claim-proof",
	// "claimed"). This is the "diagnosis, not a bare timeout" the tool promises.
	StalledGate string `json:"stalled_gate,omitempty"`
	// ClaimModeViolated reports whether the hop failed specifically because its Claim expectation
	// was violated - the tool's headline assertion about a network's autoclaim policy.
	ClaimModeViolated bool `json:"claim_mode_violated"`
	// Tracking is a snapshot of GET /tracker/v1/network/<source>/tx/<bridge tx> taken for
	// diagnosis, nil when the tracker was not consulted or did not answer. Never used to drive a
	// state transition (DESIGN.md §3).
	Tracking *trackerapi.TrackingData `json:"tracking,omitempty"`
	// Checkpoint is the last checkpoint the hop produced, so a caller can persist it verbatim.
	Checkpoint HopCheckpoint `json:"checkpoint"`
}

// Route renders the hop's direction as "<source>-><destination>", the form used in log lines and
// metric labels.
func (r *HopResult) Route() string {
	return fmt.Sprintf("%d->%d", r.Source, r.Destination)
}

// Succeeded reports whether the hop completed successfully.
func (r *HopResult) Succeeded() bool {
	return r.Outcome == HopOutcomeSuccess
}

// ErrClaimModeViolation is the sentinel every *ClaimModeViolationError wraps. A hop that fails with
// it is a *test result* - the network's claim policy is not what the config declares - and must be
// reported, never retried or swallowed.
var ErrClaimModeViolation = errors.New("bridge_loop_tester: hop claim-mode expectation violated")

// ClaimModeViolationError reports that a hop's configured Claim mode did not hold: an auto hop that
// no autoclaim service claimed within its grace period, or a manual hop that something else claimed
// during the window in which the tool asserts nothing may.
//
// This is the tool's headline assertion. It is deliberately a hard failure with a
// fully-diagnosable message rather than something the hop engine retries: retrying would turn a
// real autoclaim-policy defect into an invisible delay.
type ClaimModeViolationError struct {
	// Expected is the claim mode the hop was configured with.
	Expected ClaimMode
	// Observed attributes what actually happened (ClaimActorNone for an auto hop nobody claimed,
	// ClaimActorExternal for a manual hop someone else claimed).
	Observed ClaimActor
	// Source is the hop's source network.
	Source uint32
	// Destination is the hop's destination network.
	Destination uint32
	// DepositCount is the deposit's index in the source network's local exit tree.
	DepositCount uint32
	// GlobalIndex is the deposit's bridge global index.
	GlobalIndex *big.Int
	// GracePeriod is the window that was applied.
	GracePeriod time.Duration
	// BridgeTxHash is the hop's bridge transaction, for replaying the diagnosis by hand.
	BridgeTxHash common.Hash
	// ClaimTxHash is the offending claim transaction, when the proxy could name it.
	ClaimTxHash common.Hash
	// ClaimFromAddress is the account that submitted the offending claim, when the proxy could
	// name it.
	ClaimFromAddress common.Address
	// ProofAvailable reports whether the claim proof was already available when the expectation
	// was violated. For an auto hop this separates "the autoclaim service is not claiming" (proof
	// available) from "nothing could have claimed this yet" (proof unavailable).
	ProofAvailable bool
}

// Error renders the violation with everything needed to decide whether the autoclaim policy or the
// tool's configuration is at fault.
func (e *ClaimModeViolationError) Error() string {
	switch e.Expected {
	case ClaimManual:
		return fmt.Sprintf("bridge_loop_tester: claim-mode violation: hop %d->%d deposit_count=%d "+
			"global_index=%s (bridge tx %s) is configured Claim=%q, which asserts that nothing claims it, "+
			"but it was claimed by %s during the %s grace period (claim tx %s, from %s): "+
			"an autoclaim service appears to be active for this route",
			e.Source, e.Destination, e.DepositCount, globalIndexString(e.GlobalIndex), e.BridgeTxHash,
			ClaimManual, e.Observed, e.GracePeriod, hashString(e.ClaimTxHash), addressString(e.ClaimFromAddress))
	case ClaimAuto:
		return fmt.Sprintf("bridge_loop_tester: claim-mode violation: hop %d->%d deposit_count=%d "+
			"global_index=%s (bridge tx %s) is configured Claim=%q, which asserts that an autoclaim service "+
			"claims it, but it was still unclaimed after %s (claim proof available: %t): "+
			"no autoclaim service is claiming this route",
			e.Source, e.Destination, e.DepositCount, globalIndexString(e.GlobalIndex), e.BridgeTxHash,
			ClaimAuto, e.GracePeriod, e.ProofAvailable)
	default:
		return fmt.Sprintf("bridge_loop_tester: claim-mode violation: hop %d->%d deposit_count=%d "+
			"has unknown claim mode %q", e.Source, e.Destination, e.DepositCount, e.Expected)
	}
}

// Is reports whether target is ErrClaimModeViolation, so a caller can classify the failure without
// unwrapping the concrete type.
func (e *ClaimModeViolationError) Is(target error) bool {
	return errors.Is(target, ErrClaimModeViolation)
}

// ErrInsufficientBalance is the sentinel every *InsufficientBalanceError wraps. It means the run is
// out of funds, not that anything about the bridge is broken - the one failure a soak run is
// expected to hit eventually, since every cycle burns gas.
var ErrInsufficientBalance = errors.New("bridge_loop_tester: insufficient balance to run the hop")

// InsufficientBalanceError reports that the source network's signing account cannot fund the hop:
// either the asset balance is below the hop amount, or spending it would breach the network's
// configured MinNativeReserve gas float.
type InsufficientBalanceError struct {
	// Network is the human-readable name of the source network.
	Network string
	// NetworkID is the source network's aggkit network ID.
	NetworkID uint32
	// Account is the signing account that is short.
	Account common.Address
	// Asset is what is short (AssetETH or AssetERC20).
	Asset AssetKind
	// Token is the ERC20's address on the source network, zero for a native shortfall.
	Token common.Address
	// Required is how much the hop needs, including MinNativeReserve for a native hop.
	Required *big.Int
	// Available is what the account actually holds.
	Available *big.Int
	// MinNativeReserve is the configured gas float that must survive the hop.
	MinNativeReserve *big.Int
}

// Error renders the shortfall with the numbers needed to decide how much to top the account up by.
func (e *InsufficientBalanceError) Error() string {
	return fmt.Sprintf("%v: %s (network %d) account %s holds %s of %s (token %s) but the hop needs %s "+
		"(amount plus MinNativeReserve %s)",
		ErrInsufficientBalance, e.Network, e.NetworkID, e.Account, bigIntString(e.Available), e.Asset,
		addressString(e.Token), bigIntString(e.Required), bigIntString(e.MinNativeReserve))
}

// Is reports whether target is ErrInsufficientBalance.
func (e *InsufficientBalanceError) Is(target error) bool {
	return errors.Is(target, ErrInsufficientBalance)
}

// Unwrap returns ErrInsufficientBalance so errors.Is reaches the sentinel through wrapping too.
func (e *InsufficientBalanceError) Unwrap() error { return ErrInsufficientBalance }

// ErrBalanceMismatch is the sentinel every *BalanceMismatchError wraps: the claim succeeded but the
// destination balance did not move the way it must have. That is a bridge-correctness failure, the
// most serious verdict this tool can produce.
var ErrBalanceMismatch = errors.New("bridge_loop_tester: destination balance did not reconcile")

// BalanceMismatchError reports a destination balance that does not match what the claim should have
// credited.
type BalanceMismatchError struct {
	// Network is the human-readable name of the destination network.
	Network string
	// NetworkID is the destination network's aggkit network ID.
	NetworkID uint32
	// Account is the address the deposit was claimable by.
	Account common.Address
	// Asset is what was bridged.
	Asset AssetKind
	// Token is the asset's address on the destination network, zero for native.
	Token common.Address
	// Expected is the credit the claim should have produced (the hop amount).
	Expected *big.Int
	// Before is the balance read before the hop, nil when it was unknown.
	Before *big.Int
	// After is the balance read after the claim was observed.
	After *big.Int
	// Delta is After - Before, nil when Before was unknown.
	Delta *big.Int
	// Tolerance is the shortfall that was tolerated for a native hop (gas), zero for an ERC20 hop.
	Tolerance *big.Int
	// Detail explains which part of the check failed.
	Detail string
}

// Error renders the mismatch with all four balances, so it is obvious whether nothing arrived, too
// little arrived, or too much did.
func (e *BalanceMismatchError) Error() string {
	return fmt.Sprintf("%v: %s (network %d) account %s %s (token %s): %s "+
		"(expected credit %s, before %s, after %s, delta %s, tolerated shortfall %s)",
		ErrBalanceMismatch, e.Network, e.NetworkID, e.Account, e.Asset, addressString(e.Token), e.Detail,
		bigIntString(e.Expected), bigIntString(e.Before), bigIntString(e.After), bigIntString(e.Delta),
		bigIntString(e.Tolerance))
}

// Is reports whether target is ErrBalanceMismatch.
func (e *BalanceMismatchError) Is(target error) bool {
	return errors.Is(target, ErrBalanceMismatch)
}

// Unwrap returns ErrBalanceMismatch so errors.Is reaches the sentinel through wrapping too.
func (e *BalanceMismatchError) Unwrap() error { return ErrBalanceMismatch }

// ErrAmbiguousResume is the sentinel every *AmbiguousResumeError wraps: every check the engine can
// make on an interrupted bridge submission was made and they still do not add up to a safe answer,
// so continuing automatically could produce a second deposit.
var ErrAmbiguousResume = errors.New("bridge_loop_tester: hop cannot be resumed unambiguously")

// AmbiguousResumeError is the residual refusal of the HopStateBridging resume path, and the only
// case in that path that still needs a human.
//
// It is reached only when *all* of the following hold, in this order:
//
//  1. the checkpoint names a bridge transaction (so it was signed, and the hash is known);
//  2. that transaction has no receipt on the source network;
//  3. its nonce is no longer available - the signing account's mined nonce has moved past it, or
//     the node holds a queued transaction at it - so the engine cannot claim "the node never saw
//     this";
//  4. waiting ReceiptWait for the receipt to show up produced nothing.
//
// What is left is genuinely undecidable from outside: the nonce may have been consumed by this very
// bridge under a hash the tool never learned (a fee-bumped replacement), in which case a deposit
// exists, or by something unrelated, in which case no deposit exists. Re-submitting risks two
// deposits of Amount; skipping abandons one. The engine therefore refuses, and the message names
// every check it made plus the one place that can settle it - the bridge's own indexed deposits for
// this account on the source network.
type AmbiguousResumeError struct {
	// State is the checkpoint state that could not be resumed (always HopStateBridging today).
	State HopState
	// Source is the hop's source network.
	Source uint32
	// Destination is the hop's destination network.
	Destination uint32
	// Account is the signing account whose deposits should be inspected.
	Account common.Address
	// Amount is the amount the interrupted bridge would have moved.
	Amount *big.Int
	// BridgeTxHash is the signed-but-unreceipted bridge transaction from the checkpoint.
	BridgeTxHash common.Hash
	// BridgeTxNonce is the nonce that transaction consumes.
	BridgeTxNonce uint64
	// AccountNonce is the account's mined nonce, as read during the resume.
	AccountNonce uint64
	// PendingNonce is the account's pending nonce (mined plus queued), as read during the resume.
	PendingNonce uint64
	// ReceiptWait is how long the engine waited for the receipt before giving up. Zero when the
	// mined nonce had already moved past the transaction, which makes waiting pointless.
	ReceiptWait time.Duration
}

// Error renders every check that was made, what it showed, and how an operator settles it.
func (e *AmbiguousResumeError) Error() string {
	return fmt.Sprintf("%v: checkpoint state %q for hop %d->%d (account %s, amount %s) names bridge "+
		"transaction %s at nonce %d, and every check the engine can make was inconclusive: "+
		"(1) the transaction has no receipt on network %d; "+
		"(2) the account's mined nonce is %d and its pending nonce is %d, both at or past %d, so that nonce "+
		"is no longer free and the transaction cannot be treated as never broadcast; "+
		"(3) %s. "+
		"The nonce may therefore have been consumed by this deposit under a hash the tool never saw, or by an "+
		"unrelated transaction, and re-submitting would risk a second deposit of %s. "+
		"Reconcile it by hand against the bridge's indexed state: GET /bridge/v1/bridges?network_id=%d and "+
		"look for a deposit from %s of %s around that nonce. If one is there, resume with a checkpoint naming "+
		"its transaction hash in state %q; if none is, reset the checkpoint to state %q",
		ErrAmbiguousResume, e.State, e.Source, e.Destination, e.Account, bigIntString(e.Amount),
		e.BridgeTxHash, e.BridgeTxNonce, e.Source,
		e.AccountNonce, e.PendingNonce, e.BridgeTxNonce,
		e.receiptCheck(), bigIntString(e.Amount),
		e.Source, e.Account, bigIntString(e.Amount), HopStateBridged, HopStatePending)
}

// receiptCheck renders the third check: either the wait that produced nothing, or why waiting would
// have been pointless.
func (e *AmbiguousResumeError) receiptCheck() string {
	if e.ReceiptWait <= 0 {
		return fmt.Sprintf("waiting for the receipt was pointless, because nonce %d has already been mined "+
			"by a different transaction, so this one can never appear", e.BridgeTxNonce)
	}

	return fmt.Sprintf("waiting %s for the receipt produced none", e.ReceiptWait)
}

// Is reports whether target is ErrAmbiguousResume.
func (e *AmbiguousResumeError) Is(target error) bool {
	return errors.Is(target, ErrAmbiguousResume)
}

// Unwrap returns ErrAmbiguousResume so errors.Is reaches the sentinel through wrapping too.
func (e *AmbiguousResumeError) Unwrap() error { return ErrAmbiguousResume }

// unknownValue is how an unset or unreadable value renders in an error message.
const unknownValue = "unknown"

// bigIntString renders a possibly-nil *big.Int for an error message.
func bigIntString(value *big.Int) string {
	if value == nil {
		return unknownValue
	}

	return value.String()
}

// globalIndexString renders a possibly-nil global index for an error message.
func globalIndexString(value *big.Int) string {
	return bigIntString(value)
}

// hashString renders a possibly-zero hash for an error message.
func hashString(hash common.Hash) string {
	if hash == (common.Hash{}) {
		return unknownValue
	}

	return hash.String()
}

// addressString renders a possibly-zero address for an error message.
func addressString(address common.Address) string {
	if address == (common.Address{}) {
		return "none"
	}

	return address.String()
}
