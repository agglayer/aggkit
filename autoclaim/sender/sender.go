package sender

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	aggoracletypes "github.com/agglayer/aggkit/aggoracle/types"
	"github.com/agglayer/aggkit/autoclaim/claimtx"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/ethereum/go-ethereum/common"
)

const (
	claimAssetMethod   = "claimAsset"
	claimMessageMethod = "claimMessage"
	defaultPollPeriod  = time.Second
)

var (
	// ErrAlreadyConfirmed is returned when storage or the target bridge already has the claim confirmed.
	ErrAlreadyConfirmed = errors.New("claim already confirmed")
	// ErrRetryableStatus is returned when a monitored transaction failed or was evicted and retry budget remains.
	ErrRetryableStatus = errors.New("claim transaction retryable")
	// ErrTerminalStatus is returned when a monitored transaction reached a terminal failed state without retry budget.
	ErrTerminalStatus = errors.New("claim transaction terminal failure")
)

var _ autoclaimtypes.ClaimSender = (*Sender)(nil)

// Option configures Sender behavior.
type Option func(*Sender)

// WithPollPeriod sets the fallback interval between transaction-manager result checks.
func WithPollPeriod(period time.Duration) Option {
	return func(sender *Sender) {
		if period > 0 {
			sender.pollPeriod = period
		}
	}
}

// WithNow sets the clock used for persisted sender timestamps.
func WithNow(now func() time.Time) Option {
	return func(sender *Sender) {
		if now != nil {
			sender.now = now
		}
	}
}

// Sender encodes and submits bridge claim transactions through EthTxManager.
type Sender struct {
	storage           autoclaimtypes.Storage
	ethTxManager      aggoracletypes.EthTxManager
	targetClaimReader autoclaimtypes.TargetClaimReader
	pollPeriod        time.Duration
	now               func() time.Time
}

// New creates a claim sender.
func New(
	storage autoclaimtypes.Storage,
	ethTxManager aggoracletypes.EthTxManager,
	targetClaimReader autoclaimtypes.TargetClaimReader,
	opts ...Option,
) (*Sender, error) {
	if storage == nil {
		return nil, fmt.Errorf("autoclaim sender storage is nil")
	}
	if ethTxManager == nil {
		return nil, fmt.Errorf("autoclaim sender eth tx manager is nil")
	}
	if targetClaimReader == nil {
		return nil, fmt.Errorf("autoclaim sender target claim reader is nil")
	}

	sender := &Sender{
		storage:           storage,
		ethTxManager:      ethTxManager,
		targetClaimReader: targetClaimReader,
		pollPeriod:        defaultPollPeriod,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
	for _, opt := range opts {
		opt(sender)
	}

	return sender, nil
}

// EthTxManager returns the sender transaction-manager boundary.
func (s *Sender) EthTxManager() aggoracletypes.EthTxManager {
	return s.ethTxManager
}

// SubmitClaim encodes, submits, persists, and monitors one claim transaction.
func (s *Sender) SubmitClaim(
	ctx context.Context,
	request autoclaimtypes.AutoClaimRequest,
	proof autoclaimtypes.ClaimProof,
	target autoclaimtypes.ClaimerTarget,
) (*autoclaimtypes.TransactionAttempt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	latest, err := s.storage.GetRequest(ctx, request.Key)
	if err != nil {
		return nil, fmt.Errorf("get autoclaim request before send %s: %w", request.Key, err)
	}
	if latest.Status == autoclaimtypes.RequestStatusConfirmed {
		return noOpAttempt(*latest, target, ethtxtypes.MonitoredTxStatusFinalized, "request already confirmed"), nil
	}

	globalIndex := claimGlobalIndex(*latest)
	claimed, err := s.targetClaimReader.IsClaimed(ctx, globalIndex)
	if err != nil {
		return nil, fmt.Errorf("check target claim state for %s: %w", globalIndex, err)
	}
	if claimed {
		if err := s.transitionTo(ctx, latest.Key, autoclaimtypes.RequestStatusConfirmed); err != nil {
			return nil, err
		}
		return noOpAttempt(*latest, target, ethtxtypes.MonitoredTxStatusFinalized, "target bridge already claimed"), nil
	}

	data, err := s.packClaim(*latest, proof, globalIndex)
	if err != nil {
		return nil, err
	}

	attempt := s.newAttempt(*latest, target, data)
	txManagerID, addErr := s.ethTxManager.Add(ctx, &target.BridgeAddr, common.Big0, data, target.GasOffset, nil)
	attempt.TxManagerID = txManagerID
	attempt.SentAt = timePtr(s.now())
	if addErr != nil && !errors.Is(addErr, ethtxmanager.ErrAlreadyExists) {
		attempt.Status = ethtxtypes.MonitoredTxStatusFailed
		attempt.StatusReason = "transaction manager add failed"
		attempt.LastError = addErr.Error()
		if err := s.storage.RecordTransactionAttempt(ctx, latest.Key, attempt); err != nil {
			return &attempt, fmt.Errorf("record failed transaction attempt: %w", err)
		}
		_ = s.storage.UpdateLastError(ctx, latest.Key, attempt.LastError, attempt.UpdatedAt)
		return &attempt, fmt.Errorf("add claim transaction: %w", addErr)
	}
	if errors.Is(addErr, ethtxmanager.ErrAlreadyExists) {
		attempt.StatusReason = "transaction manager already has claim"
	} else {
		attempt.StatusReason = "transaction manager accepted claim"
	}
	attempt.Status = ethtxtypes.MonitoredTxStatusCreated

	if err := s.storage.RecordTransactionAttempt(ctx, latest.Key, attempt); err != nil {
		return &attempt, fmt.Errorf("record submitted transaction attempt: %w", err)
	}
	if err := s.transitionTo(ctx, latest.Key, autoclaimtypes.RequestStatusSent); err != nil {
		return &attempt, err
	}

	return s.pollResult(ctx, latest.Key, attempt, target)
}

func (s *Sender) packClaim(
	request autoclaimtypes.AutoClaimRequest,
	proof autoclaimtypes.ClaimProof,
	globalIndex *big.Int,
) ([]byte, error) {
	if globalIndex != nil {
		request.GlobalIndex = new(big.Int).Set(globalIndex)
	}
	return claimtx.PackClaim(request, proof)
}

func (s *Sender) newAttempt(
	request autoclaimtypes.AutoClaimRequest,
	target autoclaimtypes.ClaimerTarget,
	data []byte,
) autoclaimtypes.TransactionAttempt {
	now := s.now()
	retryCount := request.RetryCount + 1
	maxRetries := target.MaxRetries
	if maxRetries == 0 {
		maxRetries = request.MaxRetries
	}

	return autoclaimtypes.TransactionAttempt{
		RequestKey:       request.Key,
		ClaimerID:        target.ID,
		AttemptNumber:    retryCount,
		RetryCount:       retryCount,
		MaxRetries:       maxRetries,
		CreatedAt:        now,
		UpdatedAt:        now,
		TransactionData:  append([]byte(nil), data...),
		TargetBridgeAddr: target.BridgeAddr,
	}
}

func (s *Sender) pollResult(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	attempt autoclaimtypes.TransactionAttempt,
	target autoclaimtypes.ClaimerTarget,
) (*autoclaimtypes.TransactionAttempt, error) {
	pollPeriod := target.WaitPeriod
	if pollPeriod <= 0 {
		pollPeriod = s.pollPeriod
	}

	for {
		result, err := s.ethTxManager.Result(ctx, attempt.TxManagerID)
		if err != nil {
			attempt.UpdatedAt = s.now()
			attempt.LastObservedAt = timePtr(attempt.UpdatedAt)
			attempt.LastError = err.Error()
			if recordErr := s.storage.RecordTransactionAttempt(ctx, key, attempt); recordErr != nil {
				return &attempt, fmt.Errorf("record result error transaction attempt: %w", recordErr)
			}
			return &attempt, fmt.Errorf("get claim transaction result %s: %w", attempt.TxManagerID, err)
		}

		s.applyResult(&attempt, result)
		if err := s.storage.RecordTransactionAttempt(ctx, key, attempt); err != nil {
			return &attempt, fmt.Errorf("record monitored transaction attempt: %w", err)
		}

		switch result.Status {
		case ethtxtypes.MonitoredTxStatusCreated, ethtxtypes.MonitoredTxStatusSent:
			if err := waitForNextPoll(ctx, pollPeriod); err != nil {
				return &attempt, err
			}
		case ethtxtypes.MonitoredTxStatusMined,
			ethtxtypes.MonitoredTxStatusSafe,
			ethtxtypes.MonitoredTxStatusFinalized:
			if err := s.transitionTo(ctx, key, autoclaimtypes.RequestStatusConfirmed); err != nil {
				return &attempt, err
			}
			return &attempt, nil
		case ethtxtypes.MonitoredTxStatusFailed:
			return s.handleFailedStatus(ctx, key, attempt, "claim transaction failed")
		case ethtxtypes.MonitoredTxStatusEvicted:
			return s.handleFailedStatus(ctx, key, attempt, "claim transaction evicted")
		default:
			attempt.LastError = fmt.Sprintf("unexpected transaction status %s", result.Status)
			if err := s.storage.UpdateLastError(ctx, key, attempt.LastError, s.now()); err != nil {
				return &attempt, err
			}
			return &attempt, fmt.Errorf("%w: %s", ErrTerminalStatus, attempt.LastError)
		}
	}
}

func (s *Sender) applyResult(
	attempt *autoclaimtypes.TransactionAttempt,
	result ethtxtypes.MonitoredTxResult,
) {
	now := s.now()
	attempt.Status = result.Status
	attempt.UpdatedAt = now
	attempt.LastObservedAt = &now
	attempt.StatusReason = result.Status.String()
	if txHash := claimTxHash(result); txHash != (common.Hash{}) {
		attempt.ClaimTxHash = txHash
	}
	if isConfirmedStatus(result.Status) {
		attempt.ConfirmedAt = &now
	}
}

func (s *Sender) handleFailedStatus(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	attempt autoclaimtypes.TransactionAttempt,
	reason string,
) (*autoclaimtypes.TransactionAttempt, error) {
	attempt.StatusReason = reason
	attempt.LastError = reason
	if err := s.storage.RecordTransactionAttempt(ctx, key, attempt); err != nil {
		return &attempt, fmt.Errorf("record failed monitored transaction attempt: %w", err)
	}
	if err := s.storage.UpdateLastError(ctx, key, reason, s.now()); err != nil {
		return &attempt, err
	}

	if attempt.RetryCount < attempt.MaxRetries {
		if err := s.transitionTo(ctx, key, autoclaimtypes.RequestStatusQueued); err != nil {
			return &attempt, err
		}
		return &attempt, fmt.Errorf("%w: %s", ErrRetryableStatus, reason)
	}

	if err := s.transitionTo(ctx, key, autoclaimtypes.RequestStatusFailed); err != nil {
		return &attempt, err
	}
	return &attempt, fmt.Errorf("%w: %s", ErrTerminalStatus, reason)
}

func (s *Sender) transitionTo(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	targetStatus autoclaimtypes.RequestStatus,
) error {
	for {
		latest, err := s.storage.GetRequest(ctx, key)
		if err != nil {
			return fmt.Errorf("get autoclaim request before transition %s: %w", key, err)
		}
		if latest.Status == targetStatus {
			return nil
		}

		nextStatus, ok := nextTransition(latest.Status, targetStatus)
		if !ok {
			return fmt.Errorf("autoclaim sender cannot transition %s from %s to %s",
				key, latest.Status, targetStatus)
		}
		if _, err := s.storage.TransitionRequest(ctx, key, latest.Status, nextStatus, s.now()); err != nil {
			return fmt.Errorf("transition autoclaim request %s from %s to %s: %w",
				key, latest.Status, nextStatus, err)
		}
	}
}

func nextTransition(
	current autoclaimtypes.RequestStatus,
	target autoclaimtypes.RequestStatus,
) (autoclaimtypes.RequestStatus, bool) {
	if current.CanTransitionTo(target) {
		return target, true
	}

	switch {
	case current == autoclaimtypes.RequestStatusPolicyApproved && target == autoclaimtypes.RequestStatusSent:
		return autoclaimtypes.RequestStatusQueued, true
	case current == autoclaimtypes.RequestStatusQueued && target == autoclaimtypes.RequestStatusSent:
		return autoclaimtypes.RequestStatusSending, true
	default:
		return "", false
	}
}

func waitForNextPoll(ctx context.Context, pollPeriod time.Duration) error {
	timer := time.NewTimer(pollPeriod)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func claimGlobalIndex(request autoclaimtypes.AutoClaimRequest) *big.Int {
	return claimtx.GlobalIndex(request)
}

func noOpAttempt(
	request autoclaimtypes.AutoClaimRequest,
	target autoclaimtypes.ClaimerTarget,
	status ethtxtypes.MonitoredTxStatus,
	reason string,
) *autoclaimtypes.TransactionAttempt {
	return &autoclaimtypes.TransactionAttempt{
		RequestKey:       request.Key,
		ClaimerID:        target.ID,
		Status:           status,
		StatusReason:     reason,
		RetryCount:       request.RetryCount,
		MaxRetries:       target.MaxRetries,
		TargetBridgeAddr: target.BridgeAddr,
	}
}

func claimTxHash(result ethtxtypes.MonitoredTxResult) common.Hash {
	for hash, txResult := range result.Txs {
		if txResult.Tx != nil {
			return txResult.Tx.Hash()
		}
		if hash != (common.Hash{}) {
			return hash
		}
	}
	return common.Hash{}
}

func isConfirmedStatus(status ethtxtypes.MonitoredTxStatus) bool {
	switch status {
	case ethtxtypes.MonitoredTxStatusMined,
		ethtxtypes.MonitoredTxStatusSafe,
		ethtxtypes.MonitoredTxStatusFinalized:
		return true
	default:
		return false
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}
