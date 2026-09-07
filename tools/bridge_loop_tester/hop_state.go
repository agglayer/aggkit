package bridgelooptester

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// HopState is one state of the per-hop state machine specified in DESIGN.md §4.
//
// Every state is deliberately *externally observable*: a hop interrupted by a process restart is
// re-derived from a HopCheckpoint plus idempotent reads (the bridge transaction's receipt, the
// /bridge/v1 readiness endpoints, and an on-chain isClaimed call on the destination bridge) rather
// than from anything remembered only in this process's memory. The table below is the resume
// contract - HopEngine.RunHop implements exactly it.
//
//	State                  | Written to the checkpoint | How a resume detects/continues it
//	-----------------------|---------------------------|--------------------------------------------
//	HopStatePending        | before any read or write  | Nothing was submitted. Restart the hop from
//	                       |                           | scratch; no double-spend is possible.
//	HopStateApproving      | before submitting approve | ERC20 approve may or may not have landed.
//	                       |                           | Re-read allowance(from, bridge) and approve
//	                       |                           | again only if it is still short: approve is
//	                       |                           | idempotent, so this is always safe.
//	HopStateBridging       | before submitting bridge  | AMBIGUOUS: the bridge transaction may have
//	                       |                           | been accepted by the node without its hash
//	                       |                           | reaching the checkpoint. Continuing would
//	                       |                           | risk bridging Amount twice, so RunHop
//	                       |                           | refuses with *AmbiguousResumeError instead
//	                       |                           | of guessing. See that type for the manual
//	                       |                           | recovery procedure.
//	HopStateBridged        | after the bridge receipt  | Re-fetch the receipt of BridgeTxHash on the
//	                       |                           | source and re-parse its BridgeEvent
//	                       |                           | (DESIGN.md §5) to recover depositCount, leaf
//	                       |                           | type, amount and metadata, then continue at
//	                       |                           | HopStateWaitingOriginIndex.
//	HopStateWaitingOrigin- | on entry                  | Same as HopStateBridged: the origin-index
//	Index                  |                           | poll is an idempotent read, so it is simply
//	                       |                           | re-run.
//	HopStateWaitingGER-    | on entry                  | Same: re-run the injected-leaf poll. The
//	Injection              |                           | index it returns (I') is re-derived, never
//	                       |                           | trusted from the checkpoint.
//	HopStateFetchingClaim- | on entry                  | Same: re-run the claim-proof poll.
//	Proof                  |                           |
//	HopStateAwaitingClaim  | on entry                  | Re-read isClaimed(depositCount, source) on
//	                       |                           | the destination bridge. For a manual hop a
//	                       |                           | true result here is a genuine claim-mode
//	                       |                           | violation: this state is only ever written
//	                       |                           | before the tool has submitted anything, so
//	                       |                           | the claim provably came from someone else.
//	                       |                           | For an auto hop a true result completes the
//	                       |                           | hop. The manual grace period restarts from
//	                       |                           | zero on resume - conservatively longer, so a
//	                       |                           | restart can never shorten the negative
//	                       |                           | assertion window.
//	HopStateSubmittingClaim| before submitting claim   | The claim may or may not have landed. Re-read
//	                       |                           | isClaimed: true completes the hop (the tool
//	                       |                           | itself is the presumed claimer, cross-checked
//	                       |                           | against the /bridge/v1/claims record's
//	                       |                           | from_address when the proxy can answer);
//	                       |                           | false re-submits, and a submission that
//	                       |                           | reverts with AlreadyClaimed is reconciled
//	                       |                           | against isClaimed again.
//	HopStateClaimed        | after the claim is seen   | Skip straight to the destination balance
//	                       |                           | check.
//	HopStateVerified       | terminal, success         | Terminal: RunHop returns immediately without
//	                       |                           | touching the chain again.
//	HopStateFailed         | never by the engine       | A verdict, not a position: a failed hop's
//	                       |                           | checkpoint keeps the last state it actually
//	                       |                           | reached, so a caller may re-drive it. A caller
//	                       |                           | that instead wants the hop declared dead can
//	                       |                           | persist HopStateFailed itself, and RunHop then
//	                       |                           | refuses to re-drive it, since a hop that failed
//	                       |                           | is a test result rather than work to retry.
type HopState string

const (
	// HopStatePending is the initial state: the asset has not been resolved and nothing has been
	// submitted on either network.
	HopStatePending HopState = "pending"
	// HopStateApproving means an ERC20 approve for the source bridge is about to be, or has been,
	// submitted. Entered only when the current allowance is short of the hop's amount.
	HopStateApproving HopState = "approving"
	// HopStateBridging means the bridgeAsset transaction is about to be submitted. It is the one
	// state whose outcome a restart cannot observe - see HopState's table and AmbiguousResumeError.
	HopStateBridging HopState = "bridging"
	// HopStateBridged means the bridgeAsset transaction is mined and its BridgeEvent decoded, so
	// the hop's deposit count and global index are known (DESIGN.md §4, S0 -> S1).
	HopStateBridged HopState = "bridged"
	// HopStateWaitingOriginIndex is DESIGN.md §4's S1: polling
	// GET /bridge/v1/l1-info-tree-index for the source network's own index of the deposit.
	HopStateWaitingOriginIndex HopState = "waiting-origin-index"
	// HopStateWaitingGERInjection is DESIGN.md §4's S2: polling
	// GET /bridge/v1/injected-l1-info-leaf until the destination has injected a global exit root
	// covering the leaf. Skipped entirely for an L1 destination.
	HopStateWaitingGERInjection HopState = "waiting-ger-injection"
	// HopStateFetchingClaimProof is DESIGN.md §4's S3: polling GET /bridge/v1/claim-proof with the
	// actually-injected leaf index.
	HopStateFetchingClaimProof HopState = "fetching-claim-proof"
	// HopStateAwaitingClaim is DESIGN.md §4's S4: the claim-mode decision point, driven by
	// isClaimed on the destination bridge.
	HopStateAwaitingClaim HopState = "awaiting-claim"
	// HopStateSubmittingClaim is DESIGN.md §4's S5submit: the tool's own claim submission, reached
	// only by a manual hop whose grace period expired unclaimed.
	HopStateSubmittingClaim HopState = "submitting-claim"
	// HopStateClaimed is DESIGN.md §4's S6: the deposit is claimed on the destination.
	HopStateClaimed HopState = "claimed"
	// HopStateVerified is the terminal success state: claimed and the destination balance delta
	// checked.
	HopStateVerified HopState = "verified"
	// HopStateFailed is the failure verdict (DESIGN.md §4's S5err), covering a violated claim-mode
	// expectation, a readiness gate that timed out, a reverted transaction and a balance that did
	// not reconcile. It is reported in HopResult.FinalState but never written to a checkpoint by
	// the engine - see HopState's table.
	HopStateFailed HopState = "failed"
)

// IsTerminal reports whether s is a state RunHop will not drive any further.
func (s HopState) IsTerminal() bool {
	return s == HopStateVerified || s == HopStateFailed
}

// HopPhase names one timed segment of a hop, so a soak run's report shows where the time went
// (which is most of the diagnostic value of a long run). Phases are recorded in the order they
// were entered, and a phase that a hop never entered simply does not appear.
type HopPhase string

const (
	// PhaseResolveAsset covers resolving the asset's address on the source and destination
	// networks (native, the origin ERC20, or its wrapped representation).
	PhaseResolveAsset HopPhase = "resolve-asset"
	// PhaseBalanceCheck covers reading the source balance and checking it against the hop amount
	// plus the network's MinNativeReserve.
	PhaseBalanceCheck HopPhase = "balance-check"
	// PhaseApprove covers the ERC20 allowance read and, when short, the approve transaction.
	PhaseApprove HopPhase = "approve"
	// PhaseBridge covers submitting bridgeAsset and waiting for its receipt.
	PhaseBridge HopPhase = "bridge"
	// PhaseOriginIndex covers the GET /bridge/v1/l1-info-tree-index wait.
	PhaseOriginIndex HopPhase = "origin-index"
	// PhaseGERInjection covers the GET /bridge/v1/injected-l1-info-leaf wait.
	PhaseGERInjection HopPhase = "ger-injection"
	// PhaseClaimProof covers the GET /bridge/v1/claim-proof wait.
	PhaseClaimProof HopPhase = "claim-proof"
	// PhaseAutoClaimWait covers an auto hop's wait for an autoclaim service to claim the deposit.
	PhaseAutoClaimWait HopPhase = "auto-claim-wait"
	// PhaseManualGracePeriod covers a manual hop's negative-assertion window, during which nothing
	// is allowed to claim the deposit.
	PhaseManualGracePeriod HopPhase = "manual-grace-period"
	// PhaseClaimSubmit covers the tool's own claimAsset/claimMessage submission.
	PhaseClaimSubmit HopPhase = "claim-submit"
	// PhaseVerifyBalance covers the destination balance delta check.
	PhaseVerifyBalance HopPhase = "verify-balance"
)

// HopPhaseTiming is how long one HopPhase took.
type HopPhaseTiming struct {
	// Phase names the segment.
	Phase HopPhase `json:"phase"`
	// StartedAt is when the phase was entered.
	StartedAt time.Time `json:"started_at"`
	// Duration is how long the phase took.
	Duration time.Duration `json:"duration"`
}

// HopCheckpoint is the minimum a caller must persist to resume a hop after a process restart, and
// the only thing HopEngine.RunHop accepts as a resume input. It is deliberately small and
// JSON-friendly (S7 writes it to Global.StatePath): everything else a resumed hop needs is
// re-derived from the chain and the proxy, per HopState's resume table.
//
// The engine hands a fresh checkpoint to HopDeps.PersistCheckpoint on entry to every state,
// *before* the state's side effect (the approve, the bridge, the claim) is attempted, so a
// checkpoint on disk is never behind the chain.
type HopCheckpoint struct {
	// State is the state the hop had reached. Advisory for everything except the two ambiguous
	// pre-submission states (HopStateBridging, HopStateSubmittingClaim), whose resume semantics
	// depend on it - see HopState.
	State HopState `json:"state"`
	// BridgeTxHash is the hash of the hop's bridgeAsset transaction, the anchor everything else is
	// re-derived from (DESIGN.md §4). Zero until the bridge is mined.
	BridgeTxHash common.Hash `json:"bridge_tx_hash,omitempty"`
	// ClaimTxHash is the hash of the tool's own claim transaction, when it submitted one.
	ClaimTxHash common.Hash `json:"claim_tx_hash,omitempty"`
	// DepositCount is the deposit count decoded from the bridge receipt. Advisory: a resume
	// re-decodes it from the receipt and warns on a mismatch rather than trusting this value.
	DepositCount uint32 `json:"deposit_count,omitempty"`
	// L1InfoTreeIndex is the index I that GET /bridge/v1/l1-info-tree-index returned. Advisory,
	// re-polled on resume.
	L1InfoTreeIndex uint32 `json:"l1_info_tree_index,omitempty"`
	// InjectedLeafIndex is the actually-injected index I' that GET /bridge/v1/injected-l1-info-leaf
	// returned. Advisory, re-polled on resume - never fed into the claim proof from here.
	InjectedLeafIndex uint32 `json:"injected_leaf_index,omitempty"`
	// SourceTokenAddress is the asset's resolved address on the source network (zero for native).
	SourceTokenAddress common.Address `json:"source_token_address,omitempty"`
	// DestinationTokenAddress is the asset's resolved address on the destination network (zero for
	// native).
	DestinationTokenAddress common.Address `json:"destination_token_address,omitempty"`
	// DestinationBalanceBefore is the destination balance read before the hop moved anything,
	// as a base-10 decimal string (wei or token base units). Empty means "unknown", which
	// degrades the balance check to a non-exact one - see HopResult.BalanceExact.
	DestinationBalanceBefore string `json:"destination_balance_before,omitempty"`
	// StartedAt is when the hop was first started, preserved across restarts so the report shows
	// the true wall-clock cost of a hop that outlived a restart.
	StartedAt time.Time `json:"started_at,omitempty"`
}

// destinationBalanceBefore parses DestinationBalanceBefore, returning nil when it is empty or
// unparseable (both meaning "unknown").
func (c HopCheckpoint) destinationBalanceBefore() *big.Int {
	if c.DestinationBalanceBefore == "" {
		return nil
	}

	value, ok := new(big.Int).SetString(c.DestinationBalanceBefore, decimalBase)
	if !ok {
		return nil
	}

	return value
}
