package types

import (
	"context"
	"math/big"
	"time"

	aggoracletypes "github.com/agglayer/aggkit/aggoracle/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/common"
)

// BridgeSource exposes bridge sync data for watchdog discovery.
type BridgeSource interface {
	GetBridges(ctx context.Context, fromBlock, toBlock uint64) ([]bridgesync.Bridge, error)
	GetLastProcessedBlock(ctx context.Context) (uint64, bool, error)
}

// ProofPreparer prepares the L1 info tree leaf and Merkle proofs required for a claim.
type ProofPreparer interface {
	PrepareProof(ctx context.Context, request AutoClaimRequest) (*ClaimProof, error)
}

// TargetClaimReader checks claim state on the destination bridge.
type TargetClaimReader interface {
	IsClaimed(ctx context.Context, globalIndex *big.Int) (bool, error)
}

// Policy evaluates whether a request should be approved, rejected, or held for manual approval.
type Policy interface {
	Evaluate(ctx context.Context, request AutoClaimRequest) (*PolicyDecision, error)
}

// ClaimSender submits claim transactions through the configured transaction manager boundary.
type ClaimSender interface {
	SubmitClaim(
		ctx context.Context,
		request AutoClaimRequest,
		proof ClaimProof,
		target ClaimerTarget,
	) (*TransactionAttempt, error)
	EthTxManager() aggoracletypes.EthTxManager
}

// Storage owns persistence for requests, decisions, proofs, transaction attempts, and lifecycle transitions.
type Storage interface {
	EnqueueRequest(ctx context.Context, request AutoClaimRequest) (*AutoClaimRequest, bool, error)
	GetRequest(ctx context.Context, key RequestKey) (*AutoClaimRequest, error)
	ListRequests(ctx context.Context, filter RequestFilter) (*RequestPage, error)
	ListRecoverableRequests(ctx context.Context, filter RecoveryFilter) (*RequestPage, error)
	RecordPolicyDecision(ctx context.Context, key RequestKey, decision PolicyDecision) error
	RecordManualDecision(ctx context.Context, key RequestKey, decision PolicyDecision) error
	SaveProof(ctx context.Context, key RequestKey, proof ClaimProof) error
	RecordTransactionAttempt(ctx context.Context, key RequestKey, attempt TransactionAttempt) error
	TransitionRequest(
		ctx context.Context,
		key RequestKey,
		from RequestStatus,
		to RequestStatus,
		now time.Time,
	) (*AutoClaimRequest, error)
	UpdateLastError(ctx context.Context, key RequestKey, lastError string, now time.Time) error
}

// Claimer accepts discovered requests for one target network and advances them through the claim lifecycle.
type Claimer interface {
	Target() ClaimerTarget
	Enqueue(ctx context.Context, bridge BridgeExit) error
	Advance(ctx context.Context, key RequestKey) error
}

// ClaimerRegistry resolves destination-network claimers for watchdog routing.
type ClaimerRegistry interface {
	ClaimerForDestination(ctx context.Context, destinationNetwork uint32) (Claimer, bool, error)
}

// TransactionManagerFactory constructs transaction managers for configured claimer targets.
type TransactionManagerFactory interface {
	NewEthTxManager(ctx context.Context, target ClaimerTarget) (aggoracletypes.EthTxManager, error)
}

// TargetClaimState records the latest known claim state for a global index on a target network.
type TargetClaimState struct {
	GlobalIndex          *big.Int
	Claimed              bool
	LastCheckedAt        time.Time
	LastClaimTxHash      common.Hash
	LastConfirmedAtBlock *big.Int
}
