package types

import (
	"fmt"
	"math/big"
	"time"

	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

const (
	// L1OriginNetwork is the origin network used by first-scope L1-to-L2 Auto Claim requests.
	L1OriginNetwork uint32 = 0
	// LegacyZkEVMRollupNetwork is the legacy zkEVM rollup network ID used for pre-Etrog global indexes.
	LegacyZkEVMRollupNetwork uint32 = 1
	// DefaultRequestPageSize is used when request-list callers omit a page size.
	DefaultRequestPageSize uint32 = 100
	// MaxRequestPageSize bounds list allocations and SQL LIMIT values derived from caller input.
	MaxRequestPageSize uint32 = 1000
)

// RequestKey uniquely identifies an Auto Claim request across origin, destination, and deposit count.
type RequestKey string

// RequestStatus identifies the lifecycle state of an Auto Claim request.
type RequestStatus string

const (
	// RequestStatusDetected means a bridge exit has been discovered and stored.
	RequestStatusDetected RequestStatus = "detected"
	// RequestStatusPolicyApproved means policy evaluation approved automatic claiming.
	RequestStatusPolicyApproved RequestStatus = "policy-approved"
	// RequestStatusPolicyRejected means policy evaluation rejected automatic claiming.
	RequestStatusPolicyRejected RequestStatus = "policy-rejected"
	// RequestStatusManualApprovalRequired means the request is waiting for an external manual decision.
	RequestStatusManualApprovalRequired RequestStatus = "manual-approval-required"
	// RequestStatusQueued means the request is ready for claim transaction submission.
	RequestStatusQueued RequestStatus = "queued"
	// RequestStatusSending means the claimer is preparing or submitting a claim transaction.
	RequestStatusSending RequestStatus = "sending"
	// RequestStatusSent means the transaction manager has accepted or sent a claim transaction.
	RequestStatusSent RequestStatus = "sent"
	// RequestStatusConfirmed means the target network confirmed the claim.
	RequestStatusConfirmed RequestStatus = "confirmed"
	// RequestStatusFailed means the request cannot progress without operator or retry intervention.
	RequestStatusFailed RequestStatus = "failed"
	// RequestStatusDryRun means the request was fully prepared but the claim transaction was not
	// submitted because Auto Claim is running in dry-run mode.
	RequestStatusDryRun RequestStatus = "dry-run"
)

// String returns the deterministic string value for the request status.
func (s RequestStatus) String() string {
	return string(s)
}

// IsTerminal returns true when the status is a terminal lifecycle state.
func (s RequestStatus) IsTerminal() bool {
	switch s {
	case RequestStatusPolicyRejected, RequestStatusConfirmed, RequestStatusFailed, RequestStatusDryRun:
		return true
	default:
		return false
	}
}

// CanTransitionTo returns true when a request may move from this status to next.
func (s RequestStatus) CanTransitionTo(next RequestStatus) bool {
	allowedNextStatuses, ok := allowedStatusTransitions[s]
	if !ok {
		return false
	}
	for _, allowed := range allowedNextStatuses {
		if allowed == next {
			return true
		}
	}
	return false
}

// CanTransition returns true when a request may move from current to next.
func CanTransition(current, next RequestStatus) bool {
	return current.CanTransitionTo(next)
}

var allowedStatusTransitions = map[RequestStatus][]RequestStatus{
	RequestStatusDetected: {
		RequestStatusPolicyApproved,
		RequestStatusPolicyRejected,
		RequestStatusManualApprovalRequired,
		RequestStatusFailed,
	},
	RequestStatusManualApprovalRequired: {
		RequestStatusPolicyApproved,
		RequestStatusPolicyRejected,
		RequestStatusFailed,
	},
	RequestStatusPolicyApproved: {
		RequestStatusQueued,
		RequestStatusFailed,
	},
	RequestStatusQueued: {
		RequestStatusSending,
		RequestStatusConfirmed,
		RequestStatusDryRun,
		RequestStatusFailed,
	},
	RequestStatusSending: {
		RequestStatusQueued,
		RequestStatusSent,
		RequestStatusConfirmed,
		RequestStatusDryRun,
		RequestStatusFailed,
	},
	RequestStatusSent: {
		RequestStatusQueued,
		RequestStatusConfirmed,
		RequestStatusFailed,
	},
}

// PolicyResult identifies the outcome of an Auto Claim policy evaluation.
type PolicyResult string

const (
	// PolicyResultApproved means the request may be claimed automatically.
	PolicyResultApproved PolicyResult = "approved"
	// PolicyResultRejected means the request must not be claimed automatically.
	PolicyResultRejected PolicyResult = "rejected"
	// PolicyResultManual means the request requires manual approval or rejection.
	PolicyResultManual PolicyResult = "manual"
)

// String returns the deterministic string value for the policy result.
func (r PolicyResult) String() string {
	return string(r)
}

// AutoClaimRequest is the shared domain record tracked from bridge discovery through claim completion.
type AutoClaimRequest struct {
	Key                  RequestKey
	Status               RequestStatus
	Bridge               BridgeExit
	GlobalIndex          *big.Int
	L1InfoTreeIndex      *uint32
	Proof                *ClaimProof
	PolicyDecision       *PolicyDecision
	ManualDecision       *PolicyDecision
	ClaimTxHash          *common.Hash
	TxManagerID          *common.Hash
	RetryCount           uint64
	MaxRetries           uint64
	LastObservedSendAt   *time.Time
	LastObservedResultAt *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
	LastError            string
	// LER is the source network's local exit root the stored leaf proof is built against
	// (zero for L1-origin requests). The state machine branches on this value during proof preparation.
	LER common.Hash
	// VerifyBlockNum is the L1 block at which the source network's LER was verified
	// (zero for L1-origin requests). Used to select the L1 info tree leaf during proof preparation.
	VerifyBlockNum uint64
	// LeafProof is the source network's leaf-to-LER Merkle proof fetched at detection time
	// (empty for L1-origin requests). Refreshed by the proof preparer when the LER is stale.
	LeafProof treetypes.Proof
}

// BridgeExit contains the bridge leaf fields copied from bridge sync data.
type BridgeExit struct {
	// SourceNetwork is the network on which the bridge exit was initiated (0 for L1-origin exits).
	// It is the real claim identity together with DestinationNetwork and DepositCount, and it is the
	// network encoded into the claim global index. This is distinct from OriginNetwork, which is the
	// bridged token's origin network.
	SourceNetwork      uint32
	BlockNum           uint64
	BlockPos           uint64
	FromAddress        *common.Address
	TxHash             common.Hash
	BlockTimestamp     uint64
	LeafType           bridgesynctypes.LeafType
	OriginNetwork      uint32
	OriginAddress      common.Address
	DestinationNetwork uint32
	DestinationAddress common.Address
	Amount             *big.Int
	Metadata           []byte
	DepositCount       uint32
	TxnSender          common.Address
	Source             bridgesync.BridgeSource
	ToAddress          common.Address
	GlobalIndex        *big.Int
	L1InfoTreeIndex    *uint32
	PreEtrog           bool
}

// PolicyDecision records the policy or manual decision that controls whether a request may be claimed.
type PolicyDecision struct {
	PolicyName string
	Result     PolicyResult
	Reason     string
	Metadata   map[string]string
	Decider    string
	DeciderID  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// TransactionAttempt records one transaction-manager attempt for claiming a request.
type TransactionAttempt struct {
	RequestKey       RequestKey
	ClaimerID        string
	AttemptNumber    uint64
	TxManagerID      common.Hash
	ClaimTxHash      common.Hash
	Status           ethtxtypes.MonitoredTxStatus
	StatusReason     string
	RetryCount       uint64
	MaxRetries       uint64
	CreatedAt        time.Time
	UpdatedAt        time.Time
	SentAt           *time.Time
	ConfirmedAt      *time.Time
	LastObservedAt   *time.Time
	LastError        string
	TransactionData  []byte
	TargetBridgeAddr common.Address
}

// ClaimerTarget identifies a destination network and transaction settings owned by one claimer.
type ClaimerTarget struct {
	ID                 string
	DestinationNetwork uint32
	NetworkType        string
	BridgeAddr         common.Address
	GasOffset          uint64
	WaitPeriod         time.Duration
	RetryAfter         time.Duration
	MaxRetries         uint64
	// DryRun, when true, makes the sender prepare the claim but skip submitting the transaction.
	DryRun bool
}

// ClaimProof contains the selected L1 info tree leaf and proofs required to submit a target claim.
type ClaimProof struct {
	L1InfoTreeIndex     uint32
	L1InfoTreeLeaf      *l1infotreesync.L1InfoTreeLeaf
	MainnetExitRoot     common.Hash
	RollupExitRoot      common.Hash
	GlobalExitRoot      common.Hash
	ProofLocalExitRoot  treetypes.Proof
	ProofRollupExitRoot treetypes.Proof
	ABILocalExitRoot    ABIProof
	ABIRollupExitRoot   ABIProof
	PreparedAt          time.Time
	LastTargetCheckedAt *time.Time
}

// ABIProof is the fixed-size proof shape expected by bridge contract claim methods.
type ABIProof [treetypes.DefaultHeight][common.HashLength]byte

// RequestFilter contains optional filters for listing Auto Claim requests.
type RequestFilter struct {
	SourceNetwork      *uint32
	OriginNetwork      *uint32
	DestinationNetwork *uint32
	Status             *RequestStatus
	PolicyResult       *PolicyResult
	BridgeTxHash       *common.Hash
	ClaimTxHash        *common.Hash
	FromBlock          *uint64
	ToBlock            *uint64
	PageNumber         uint32
	PageSize           uint32
}

// RecoveryFilter contains filters for restart recovery request queries.
type RecoveryFilter struct {
	DestinationNetwork *uint32
	Statuses           []RequestStatus
	PageNumber         uint32
	PageSize           uint32
}

// RequestPage contains a paginated request list and total count.
type RequestPage struct {
	Requests []*AutoClaimRequest
	Count    int
}

// BridgePage contains bridge exits discovered in a polling window.
type BridgePage struct {
	Bridges     []BridgeExit
	FromBlock   uint64
	ToBlock     uint64
	NextCursor  *BridgeCursor
	TotalCount  int
	LastSynced  uint64
	HasMoreData bool
}

// BridgeCursor stores the durable bridge-discovery cursor.
type BridgeCursor struct {
	FromBlock uint64
	ToBlock   uint64
	BlockNum  uint64
	BlockPos  uint64
}

// LERCursor stores the durable per-source-network local-exit-root discovery cursor used by the
// L2-to-Lx bridge detector to track the last processed LER of a source network.
type LERCursor struct {
	SourceNetwork      uint32
	LastLER            common.Hash
	LastVerifyBlockNum uint64
}

// DeriveRequestKey derives the unique request key from the source network, destination network, and
// deposit count. The tuple (sourceNetwork, destinationNetwork, depositCount) is the real claim identity
// and mirrors the claim global index.
func DeriveRequestKey(sourceNetwork, destinationNetwork, depositCount uint32) RequestKey {
	return RequestKey(fmt.Sprintf("%d:%d:%d", sourceNetwork, destinationNetwork, depositCount))
}

// DeriveL1GlobalIndex derives the global index for first-scope L1-origin requests.
func DeriveL1GlobalIndex(depositCount uint32) *big.Int {
	return bridgesync.GenerateGlobalIndexForNetworkID(L1OriginNetwork, depositCount)
}

// DeriveGlobalIndex derives the global index for a bridge origin network and deposit count.
func DeriveGlobalIndex(originNetwork, depositCount uint32) *big.Int {
	return bridgesync.GenerateGlobalIndexForNetworkID(originNetwork, depositCount)
}

// DeriveGlobalIndexForSource derives the claim global index for a request keyed by its source network
// and deposit count. For L1-origin requests (sourceNetwork == 0) it yields the mainnet-flagged index;
// for rollup-origin requests it encodes rollupIndex = sourceNetwork - 1. This is the source-aware
// replacement for DeriveL1GlobalIndex used across the claim path.
func DeriveGlobalIndexForSource(sourceNetwork, depositCount uint32) *big.Int {
	return bridgesync.GenerateGlobalIndexForNetworkID(sourceNetwork, depositCount)
}

// NewBridgeExitFromSync converts a bridge sync record into an Auto Claim bridge exit.
func NewBridgeExitFromSync(bridge bridgesync.Bridge) BridgeExit {
	return NewBridgeExitFromSyncWithEtrog(bridge, 0)
}

// NewBridgeExitFromSyncWithEtrog converts a bridge sync record using Etrog-upgrade awareness.
func NewBridgeExitFromSyncWithEtrog(bridge bridgesync.Bridge, etrogL1UpgradeBlock uint64) BridgeExit {
	// These bridges were initiated on L1, so their global index always encodes the L1 (mainnet)
	// network. bridge.OriginNetwork is the bridged token's origin network, not the bridge origin.
	globalIndex, preEtrog := bridgesync.GlobalIndexForBridge(
		bridge.DestinationNetwork, bridge.BlockNum, bridge.DepositCount, L1OriginNetwork, etrogL1UpgradeBlock)

	return BridgeExit{
		BlockNum:           bridge.BlockNum,
		BlockPos:           bridge.BlockPos,
		FromAddress:        bridge.FromAddress,
		TxHash:             bridge.TxHash,
		BlockTimestamp:     bridge.BlockTimestamp,
		LeafType:           bridgesynctypes.LeafType(bridge.LeafType),
		OriginNetwork:      bridge.OriginNetwork,
		OriginAddress:      bridge.OriginAddress,
		DestinationNetwork: bridge.DestinationNetwork,
		DestinationAddress: bridge.DestinationAddress,
		Amount:             copyBigInt(bridge.Amount),
		Metadata:           append([]byte(nil), bridge.Metadata...),
		DepositCount:       bridge.DepositCount,
		TxnSender:          bridge.TxnSender,
		Source:             bridge.Source,
		ToAddress:          bridge.ToAddress,
		GlobalIndex:        globalIndex,
		PreEtrog:           preEtrog,
	}
}

// NewRequestFromBridgeExit builds a detected Auto Claim request from a discovered bridge exit.
func NewRequestFromBridgeExit(bridge BridgeExit, now time.Time) AutoClaimRequest {
	key := DeriveRequestKey(bridge.SourceNetwork, bridge.DestinationNetwork, bridge.DepositCount)
	globalIndex := copyBigInt(bridge.GlobalIndex)
	if globalIndex == nil {
		globalIndex = DeriveGlobalIndexForSource(bridge.SourceNetwork, bridge.DepositCount)
	}
	var l1InfoTreeIndex *uint32
	if bridge.L1InfoTreeIndex != nil {
		index := *bridge.L1InfoTreeIndex
		l1InfoTreeIndex = &index
	}

	return AutoClaimRequest{
		Key:             key,
		Status:          RequestStatusDetected,
		Bridge:          bridge,
		GlobalIndex:     globalIndex,
		L1InfoTreeIndex: l1InfoTreeIndex,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// ProofToABIProof converts an internal tree proof into the ABI-ready proof array shape.
func ProofToABIProof(proof treetypes.Proof) ABIProof {
	var abiProof ABIProof
	for i := 0; i < len(proof) && i < len(abiProof); i++ {
		abiProof[i] = proof[i]
	}
	return abiProof
}

func copyBigInt(value *big.Int) *big.Int {
	if value == nil {
		return nil
	}
	return new(big.Int).Set(value)
}
