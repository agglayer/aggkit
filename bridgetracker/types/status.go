package types

import (
	"encoding/json"
	"fmt"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
)

// ErrorData is the error structure shared by the REST error responses and the
// WebSocket "error" messages
type ErrorData struct {
	// Code is an HTTP-like error code (e.g. 400 invalid params, 404 bridge tx not found)
	Code int `json:"code"`
	// Message is a human-readable description of the error
	Message string `json:"message"`
}

// BridgeType identifies the direction of a bridge
type BridgeType int

const (
	// BridgeTypeL1ToL2 is a bridge from mainnet to a rollup
	BridgeTypeL1ToL2 BridgeType = iota
	// BridgeTypeL2ToL1 is a bridge from a rollup to mainnet
	BridgeTypeL2ToL1
	// BridgeTypeL2ToL2 is a bridge between two rollups
	BridgeTypeL2ToL2
)

// String representation of the enum
func (b BridgeType) String() string {
	switch b {
	case BridgeTypeL1ToL2:
		return "L1->L2"
	case BridgeTypeL2ToL1:
		return "L2->L1"
	case BridgeTypeL2ToL2:
		return "L2->L2"
	default:
		return fmt.Sprintf("Unknown(%d)", int(b))
	}
}

// BridgeLeafType identifies the kind of leaf the bridge created (asset or message)
type BridgeLeafType int

const (
	// BridgeLeafTypeAsset the bridge was created with bridgeAsset (leaf_type=0)
	BridgeLeafTypeAsset BridgeLeafType = iota
	// BridgeLeafTypeMessage the bridge was created with bridgeMessage (leaf_type=1)
	BridgeLeafTypeMessage
)

// String representation of the enum
func (b BridgeLeafType) String() string {
	switch b {
	case BridgeLeafTypeAsset:
		return "Asset"
	case BridgeLeafTypeMessage:
		return "Message"
	default:
		return fmt.Sprintf("Unknown(%d)", int(b))
	}
}

// BridgeStep identifies each step a bridge goes through, from its creation
// with bridgeAsset until it is claimed on the destination network
type BridgeStep int

const (
	// StepWaitingGERUpdate the bridge has been created (BridgeEvent emitted) on an L1
	// origin, but the Global Exit Root has not been updated yet (e.g.
	// forceUpdateGlobalExitRoot=false)
	StepWaitingGERUpdate BridgeStep = iota
	// StepWaitingLERUpdate the bridge has been created (BridgeEvent emitted) on an L2
	// origin, but its Local Exit Root has not been updated yet
	StepWaitingLERUpdate
	// StepPendingInclusion the bridge is not yet part of any certificate
	StepPendingInclusion
	// StepCertificatePending the bridge is included in a certificate sent to the Agglayer;
	// covers every status the certificate goes through (Pending, Proven, Candidate, InError)
	// until it settles — those intermediate statuses do not change the step, only the
	// certificate data carried in its Result (see BridgeStepPath.Result)
	StepCertificatePending
	// StepWaitL1SettledGER the certificate has settled, but its settlement tx has not been
	// confirmed on L1 yet: the tracker waits for that tx to reach the configured L1 finality
	// and its receipt to carry both VerifyBatchesTrustedAggregator and UpdateL1InfoTree
	// (UpdateL1InfoTreeV2 is captured too, if present, but is not required). Only reached by
	// L2-originated bridges (L2->L1 and L2->L2), right after StepCertificatePending
	StepWaitL1SettledGER
	// StepWaitingGERInjection the certificate is settled but the Global Exit Root has
	// not been injected on the destination network yet
	StepWaitingGERInjection
	// StepWaitingClaim the Global Exit Root that includes the bridge has been injected
	// on the destination network, so the bridge is ready to be claimed
	StepWaitingClaim
	// StepClaimed the bridge has been claimed on the destination network
	StepClaimed
)

var bridgeStepNames = map[BridgeStep]string{
	StepWaitingGERUpdate:    "WaitingGERUpdate",
	StepWaitingLERUpdate:    "WaitingLERUpdate",
	StepPendingInclusion:    "PendingInclusion",
	StepCertificatePending:  "CertificatePending",
	StepWaitL1SettledGER:    "WaitL1SettledGER",
	StepWaitingGERInjection: "WaitingGERInjection",
	StepWaitingClaim:        "WaitingClaim",
	StepClaimed:             "Claimed",
}

// String representation of the enum
func (s BridgeStep) String() string {
	if name, ok := bridgeStepNames[s]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", int(s))
}

// StepStatus is the status of a single step within the bridge path
type StepStatus int

const (
	// StepStatusPending the step has not started yet
	StepStatusPending StepStatus = iota
	// StepStatusInProgress the step is the one currently in progress
	StepStatusInProgress
	// StepStatusDone the step has been completed
	StepStatusDone
	// StepStatusError the step failed; details are carried in BridgeStepPath.Error
	StepStatusError
)

var stepStatusNames = map[StepStatus]string{
	StepStatusPending:    "pending",
	StepStatusInProgress: "inProgress",
	StepStatusDone:       "done",
	StepStatusError:      "error",
}

// String representation of the enum
func (s StepStatus) String() string {
	if name, ok := stepStatusNames[s]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", int(s))
}

// LERType identifies which exit tree the Local Exit Root belongs to
type LERType int

const (
	// LERTypeNA the Local Exit Root is not available / not applicable
	LERTypeNA LERType = iota
	// LERTypeMainnet the Local Exit Root belongs to the mainnet exit tree
	LERTypeMainnet
	// LERTypeLocal the Local Exit Root belongs to a rollup local exit tree
	LERTypeLocal
)

// String representation of the enum
func (l LERType) String() string {
	switch l {
	case LERTypeNA:
		return "NA"
	case LERTypeMainnet:
		return "Mainnet"
	case LERTypeLocal:
		return "Local"
	default:
		return fmt.Sprintf("Unknown(%d)", int(l))
	}
}

// Duration is a time.Duration that marshals to/from a human-readable
// string (e.g. "5m0s") instead of nanoseconds
type Duration struct {
	time.Duration
}

// NewDuration returns a Duration wrapper
func NewDuration(d time.Duration) *Duration {
	return &Duration{Duration: d}
}

// MarshalJSON is the implementation of the json.Marshaler interface
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// UnmarshalJSON is the implementation of the json.Unmarshaler interface
func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	duration, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = duration
	return nil
}

// StepErrorType classifies a step error by whether it is expected to clear on retry
type StepErrorType int

const (
	// StepErrorTransient the error is expected to be resolved by retrying the step
	StepErrorTransient StepErrorType = iota
	// StepErrorPermanent the error will not resolve by retrying and requires intervention
	StepErrorPermanent
	// StepErrorExhausted the error was transient but retries have been given up on
	StepErrorExhausted
)

var stepErrorTypeNames = map[StepErrorType]string{
	StepErrorTransient: "transient",
	StepErrorPermanent: "permanent",
	StepErrorExhausted: "exhausted",
}

// String representation of the enum
func (e StepErrorType) String() string {
	if name, ok := stepErrorTypeNames[e]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", int(e))
}

// ErrorStep carries the details of a step that is in StepStatusError
type ErrorStep struct {
	ErrorType StepErrorType `json:"error_type"`
	// ErrorTypeString is the string representation of ErrorType, auto-populated on JSON marshaling
	ErrorTypeString string `json:"error_type_string"`
	// RetryCount is the number of retries attempted so far for this step
	RetryCount int `json:"retry_count"`
	// Description holds the human-readable description of the error; one entry per occurrence
	Description []string `json:"description"`
}

// MarshalJSON is the implementation of the json.Marshaler interface.
// It populates the string representation of the numeric enum fields
func (e ErrorStep) MarshalJSON() ([]byte, error) {
	e.ErrorTypeString = e.ErrorType.String()
	type errorStepAlias ErrorStep
	return json.Marshal(errorStepAlias(e))
}

// GERUpdateResult is the result of StepWaitingGERUpdate once it completes: the GER produced
// by the update on the origin network (L1) and the block it was updated in
type GERUpdateResult struct {
	// L1InfoTreeIndex is the leaf index the update landed at, resolved from the contract's own
	// leaf count (1-based) as of the update's block: index = count - 1
	L1InfoTreeIndex uint32      `json:"l1_info_tree_index"`
	GER             common.Hash `json:"ger"`
	MainnetExitRoot common.Hash `json:"mer"`
	RollupExitRoot  common.Hash `json:"rer"`
	BlockNumber     uint64      `json:"block_number"`
	BlockTimestamp  uint64      `json:"block_timestamp"`
	LogIndex        uint        `json:"log_index"`
}

// InjectedGERResult is the result of StepWaitingGERInjection once it completes: the GER
// injected on the destination network that covers the bridge. The injection source does not
// expose the block it was injected in, unlike GERUpdateResult
type InjectedGERResult struct {
	GER common.Hash `json:"ger"`
}

// LERUpdateResult is the result of StepWaitingLERUpdate once it completes: the LER produced
// by the update on the origin L2 network and the block it was updated in
type LERUpdateResult struct {
	NetworkID   uint32      `json:"network_id"`
	LER         common.Hash `json:"ler"`
	BlockNumber uint64      `json:"block_number"`
}

// ClaimResult is the result of StepWaitingClaim once it completes: the claim transaction on
// the destination network and the block it was mined in
type ClaimResult struct {
	ClaimTx     common.Hash `json:"claim_tx"`
	BlockNumber uint64      `json:"block_number"`
}

// L1SettledGERResult is the result of StepWaitL1SettledGER once it completes: the evidence,
// read off the certificate's settlement tx receipt on L1, that the settlement propagated to
// the L1 Global Exit Root. HasVerifyBatchesTrustedAggregator and HasUpdateL1InfoTree are both
// required for the step to complete; HasUpdateL1InfoTreeV2 is only informational. GER is the
// Global Exit Root produced by the settlement (computed from UpdateL1InfoTree's mainnet/rollup
// exit roots), used by StepWaitingGERInjection to check whether it has reached the destination.
// L1InfoTreeIndex is the leaf index GER landed at: populated straight from UpdateL1InfoTreeV2's
// LeafCount when that (optional) event fires, otherwise resolved by the step itself with one
// extra lookup (GER -> leaf) before it can complete — either way, by the time this step is
// Done, L1InfoTreeIndex is never nil
type L1SettledGERResult struct {
	TxHash                            common.Hash `json:"tx_hash"`
	BlockNumber                       uint64      `json:"block_number"`
	GER                               common.Hash `json:"ger"`
	L1InfoTreeIndex                   *uint32     `json:"l1_info_tree_index,omitempty"`
	HasVerifyBatchesTrustedAggregator bool        `json:"has_verify_batches_trusted_aggregator"`
	HasUpdateL1InfoTree               bool        `json:"has_update_l1_info_tree"`
	HasUpdateL1InfoTreeV2             bool        `json:"has_update_l1_info_tree_v2"`
}

// GERData holds the exit roots of a Global Exit Root update relevant to a bridge
type GERData struct {
	// NetworkID is the network the GER belongs to (0 -> Mainnet)
	NetworkID uint32 `json:"network_id"`
	// GER is the Global Exit Root
	GER *common.Hash `json:"ger,omitempty"`
	// MER is the Mainnet Exit Root
	MER *common.Hash `json:"mer,omitempty"`
	// RER is the Rollup Exit Root
	RER *common.Hash `json:"rer,omitempty"`
	// LER is the Local Exit Root
	LER *common.Hash `json:"ler,omitempty"`
	// LERType identifies which exit tree the LER belongs to
	LERType LERType `json:"ler_type"`
	// LERTypeString is the string representation of LERType, auto-populated on JSON marshaling
	LERTypeString string `json:"ler_type_string"`
	// BlockNumber is the block where the GER update happened. Only populated when resolving
	// the origin GER of an L1-originated bridge. Internal only: GERData is not serialized on
	// any tracker response, it is the domain layer's currency to decide GER coverage
	BlockNumber *uint64 `json:"-"`
}

// MarshalJSON is the implementation of the json.Marshaler interface.
// It populates the string representation of the numeric enum fields
func (g GERData) MarshalJSON() ([]byte, error) {
	g.LERTypeString = g.LERType.String()
	type gerDataAlias GERData
	return json.Marshal(gerDataAlias(g))
}

// CertificateData holds the Agglayer certificate information related to a bridge
type CertificateData struct {
	CertificateID common.Hash                     `json:"certificate_id"`
	Status        agglayertypes.CertificateStatus `json:"status"`
	// StatusString is the string representation of Status, auto-populated on JSON marshaling
	StatusString string `json:"status_string"`
	// Error is only set if the certificate carries an error message (relevant for InError certs)
	Error            string       `json:"error,omitempty"`
	SettlementTxHash *common.Hash `json:"settlement_tx_hash,omitempty"`
}

// MarshalJSON is the implementation of the json.Marshaler interface.
// It populates the string representation of the numeric enum fields
func (c CertificateData) MarshalJSON() ([]byte, error) {
	c.StatusString = c.Status.String()
	type certificateDataAlias CertificateData
	return json.Marshal(certificateDataAlias(c))
}

// CertificateInclusionData is the data CertificateSource.CertificateFor resolves for the
// certificate that covers (or may come to cover) a bridge: its status (CertificateData, also
// StepCertificatePending's own Result type) plus the LER transition it produced, which only
// PendingInclusionResolver needs
type CertificateInclusionData struct {
	CertificateData
	// PreviousLocalExitRoot is the LER right before this certificate, nil for a network's first
	// certificate
	PreviousLocalExitRoot *common.Hash
	// NewLocalExitRoot is the LER this certificate advances to, the one that covers the bridge
	NewLocalExitRoot common.Hash
}

// PendingInclusionResult is the result of StepPendingInclusion once it completes: the
// certificate that first includes the bridge and the LER transition it produced
type PendingInclusionResult struct {
	CertificateID common.Hash  `json:"certificate_id"`
	NewLER        common.Hash  `json:"new_ler"`
	PreviousLER   *common.Hash `json:"previous_ler,omitempty"`
}
