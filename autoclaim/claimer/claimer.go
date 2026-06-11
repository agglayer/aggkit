package claimer

import (
	"context"
	"errors"
	"fmt"
	"time"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

const (
	defaultRecoverPageSize = uint32(100)
	defaultPollPeriod      = time.Second
)

var (
	// ErrDisabled is returned when a disabled claimer is asked to enqueue or advance work.
	ErrDisabled = errors.New("autoclaim claimer is disabled")
	// ErrDestinationMismatch is returned when a request targets another destination network.
	ErrDestinationMismatch = errors.New("autoclaim request destination does not match claimer")
	// ErrPolicyBlocked is returned when policy evaluation hits an operational error that requires intervention.
	ErrPolicyBlocked = errors.New("autoclaim policy blocked")
)

var _ autoclaimtypes.Claimer = (*Claimer)(nil)

// Option configures a Claimer.
type Option func(*Claimer)

// WithEnabled configures whether the claimer may enqueue, recover, or process requests.
func WithEnabled(enabled bool) Option {
	return func(claimer *Claimer) {
		claimer.enabled = enabled
	}
}

// WithPollPeriod configures how often Start re-checks recoverable requests.
func WithPollPeriod(period time.Duration) Option {
	return func(claimer *Claimer) {
		if period > 0 {
			claimer.pollPeriod = period
		}
	}
}

// WithRecoverPageSize configures the page size used during restart recovery.
func WithRecoverPageSize(pageSize uint32) Option {
	return func(claimer *Claimer) {
		if pageSize > 0 {
			claimer.recoverPageSize = pageSize
		}
	}
}

// WithNow configures the clock used for request timestamps and lifecycle transitions.
func WithNow(now func() time.Time) Option {
	return func(claimer *Claimer) {
		if now != nil {
			claimer.now = now
		}
	}
}

// WithTargetClaimReader configures the target bridge reader used for already-claimed pre-checks.
func WithTargetClaimReader(targetClaimReader autoclaimtypes.TargetClaimReader) Option {
	return func(claimer *Claimer) {
		claimer.targetClaimReader = targetClaimReader
	}
}

// WithLogger configures optional background processing logs.
func WithLogger(log aggkitcommon.Logger) Option {
	return func(claimer *Claimer) {
		claimer.log = log
	}
}

// Claimer orchestrates Auto Claim requests for one destination network.
type Claimer struct {
	target            autoclaimtypes.ClaimerTarget
	storage           autoclaimtypes.Storage
	policy            autoclaimtypes.Policy
	proofPreparer     autoclaimtypes.ProofPreparer
	sender            autoclaimtypes.ClaimSender
	targetClaimReader autoclaimtypes.TargetClaimReader
	enabled           bool
	pollPeriod        time.Duration
	recoverPageSize   uint32
	now               func() time.Time
	log               aggkitcommon.Logger
}

type preparedProofPolicy interface {
	RequiresPreparedProof() bool
}

// New creates one claimer for one configured destination network.
func New(
	target autoclaimtypes.ClaimerTarget,
	storage autoclaimtypes.Storage,
	policy autoclaimtypes.Policy,
	proofPreparer autoclaimtypes.ProofPreparer,
	sender autoclaimtypes.ClaimSender,
	options ...Option,
) (*Claimer, error) {
	if target.ID == "" {
		return nil, fmt.Errorf("autoclaim claimer target ID is empty")
	}
	if storage == nil {
		return nil, fmt.Errorf("autoclaim claimer storage is nil")
	}
	if policy == nil {
		return nil, fmt.Errorf("autoclaim claimer policy is nil")
	}
	if proofPreparer == nil {
		return nil, fmt.Errorf("autoclaim claimer proof preparer is nil")
	}
	if sender == nil {
		return nil, fmt.Errorf("autoclaim claimer sender is nil")
	}

	claimer := &Claimer{
		target:          target,
		storage:         storage,
		policy:          policy,
		proofPreparer:   proofPreparer,
		sender:          sender,
		enabled:         true,
		pollPeriod:      defaultPollPeriod,
		recoverPageSize: defaultRecoverPageSize,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
	for _, option := range options {
		option(claimer)
	}

	return claimer, nil
}

// Target returns the destination network and transaction settings owned by the claimer.
func (c *Claimer) Target() autoclaimtypes.ClaimerTarget {
	return c.target
}

// IsClaimed checks whether the destination bridge already marks a bridge exit as claimed.
func (c *Claimer) IsClaimed(ctx context.Context, bridge autoclaimtypes.BridgeExit) (bool, error) {
	if c.targetClaimReader == nil {
		return false, fmt.Errorf("autoclaim claimer %s target claim reader is nil", c.target.ID)
	}
	globalIndex := bridge.GlobalIndex
	if globalIndex == nil {
		globalIndex = autoclaimtypes.DeriveL1GlobalIndex(bridge.DepositCount)
	}
	claimed, err := c.targetClaimReader.IsClaimed(ctx, globalIndex)
	if err != nil {
		return false, fmt.Errorf("check target claim state for %s: %w", globalIndex, err)
	}
	return claimed, nil
}

// Start runs restart recovery and then periodically resumes recoverable requests until ctx is cancelled.
func (c *Claimer) Start(ctx context.Context) {
	if !c.enabled {
		return
	}

	if err := c.Recover(ctx); err != nil {
		c.logErrorf("autoclaim claimer %s recovery failed: %v", c.target.ID, err)
		if errors.Is(err, ErrPolicyBlocked) {
			return
		}
	}

	ticker := time.NewTicker(c.pollPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Recover(ctx); err != nil {
				c.logErrorf("autoclaim claimer %s recovery failed: %v", c.target.ID, err)
				if errors.Is(err, ErrPolicyBlocked) {
					return
				}
			}
		}
	}
}

// Enqueue stores a discovered bridge request and advances it when policy allows automatic claiming.
func (c *Claimer) Enqueue(ctx context.Context, bridge autoclaimtypes.BridgeExit) error {
	if !c.enabled {
		return ErrDisabled
	}
	if bridge.DestinationNetwork != c.target.DestinationNetwork {
		return fmt.Errorf("%w: claimer %s handles %d, request targets %d",
			ErrDestinationMismatch, c.target.ID, c.target.DestinationNetwork, bridge.DestinationNetwork)
	}

	request := autoclaimtypes.NewRequestFromBridgeExit(bridge, c.now())
	request.MaxRetries = c.target.MaxRetries

	stored, _, err := c.storage.EnqueueRequest(ctx, request)
	if err != nil {
		return fmt.Errorf("enqueue autoclaim request %s: %w", request.Key, err)
	}
	if stored.Status.IsTerminal() {
		return nil
	}

	return c.Advance(ctx, stored.Key)
}

// Advance progresses one stored request as far as currently possible.
func (c *Claimer) Advance(ctx context.Context, key autoclaimtypes.RequestKey) error {
	if !c.enabled {
		return ErrDisabled
	}

	request, err := c.storage.GetRequest(ctx, key)
	if err != nil {
		return fmt.Errorf("get autoclaim request %s: %w", key, err)
	}
	if err := c.requireDestination(*request); err != nil {
		return err
	}

	for {
		switch request.Status {
		case autoclaimtypes.RequestStatusDetected:
			next, advanceErr := c.evaluatePolicy(ctx, *request)
			if advanceErr != nil {
				return advanceErr
			}
			if next.Status == request.Status {
				return nil
			}
			request = next
		case autoclaimtypes.RequestStatusManualApprovalRequired:
			next, advanceErr := c.advanceManualDecision(ctx, *request)
			if advanceErr != nil {
				return advanceErr
			}
			if next.Status == request.Status {
				return nil
			}
			request = next
		case autoclaimtypes.RequestStatusPolicyApproved:
			request, err = c.transition(ctx, *request, autoclaimtypes.RequestStatusQueued)
		case autoclaimtypes.RequestStatusQueued, autoclaimtypes.RequestStatusSending, autoclaimtypes.RequestStatusSent:
			return c.sendWhenReady(ctx, *request)
		default:
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// Recover resumes approved and in-flight requests for this claimer destination.
func (c *Claimer) Recover(ctx context.Context) error {
	if !c.enabled {
		return ErrDisabled
	}

	keys, err := c.recoverableKeys(ctx)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := c.Advance(ctx, key); err != nil {
			c.logErrorf("advance recoverable autoclaim request %s: %v", key, err)
			if errors.Is(err, ErrPolicyBlocked) {
				return err
			}
		}
	}

	return nil
}

func (c *Claimer) recoverableKeys(ctx context.Context) ([]autoclaimtypes.RequestKey, error) {
	destinationNetwork := c.target.DestinationNetwork
	statuses := []autoclaimtypes.RequestStatus{
		autoclaimtypes.RequestStatusDetected,
		autoclaimtypes.RequestStatusPolicyApproved,
		autoclaimtypes.RequestStatusQueued,
		autoclaimtypes.RequestStatusSending,
		autoclaimtypes.RequestStatusSent,
	}
	keys := make([]autoclaimtypes.RequestKey, 0)
	pageNumber := uint32(0)
	for {
		page, err := c.storage.ListRecoverableRequests(ctx, autoclaimtypes.RecoveryFilter{
			DestinationNetwork: &destinationNetwork,
			Statuses:           statuses,
			PageNumber:         pageNumber,
			PageSize:           c.recoverPageSize,
		})
		if err != nil {
			return nil, fmt.Errorf("list recoverable autoclaim requests for %d: %w", destinationNetwork, err)
		}
		if len(page.Requests) == 0 {
			return keys, nil
		}

		for _, request := range page.Requests {
			if request == nil {
				continue
			}
			keys = append(keys, request.Key)
		}

		pageNumber++
		if len(page.Requests) < int(c.recoverPageSize) {
			return keys, nil
		}
	}
}

func (c *Claimer) evaluatePolicy(
	ctx context.Context,
	request autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.AutoClaimRequest, error) {
	proofPolicy, needsPreparedProof := c.policy.(preparedProofPolicy)
	if needsPreparedProof && proofPolicy.RequiresPreparedProof() && request.Proof == nil {
		prepared, err := c.preparePolicyProof(ctx, request)
		if err != nil {
			return nil, err
		}
		if prepared == nil {
			return &request, nil
		}
		request = *prepared
	}

	decision, err := c.policy.Evaluate(ctx, request)
	if err != nil {
		if updateErr := c.storage.UpdateLastError(ctx, request.Key, err.Error(), c.now()); updateErr != nil {
			return nil, fmt.Errorf("record policy error for autoclaim request %s: %w", request.Key, updateErr)
		}
		return nil, fmt.Errorf("%w: evaluate policy for autoclaim request %s: %w", ErrPolicyBlocked, request.Key, err)
	}
	if decision == nil {
		return &request, nil
	}
	if err := c.storage.RecordPolicyDecision(ctx, request.Key, *decision); err != nil {
		return nil, fmt.Errorf("record policy decision for autoclaim request %s: %w", request.Key, err)
	}

	switch decision.Result {
	case autoclaimtypes.PolicyResultApproved:
		return c.transition(ctx, request, autoclaimtypes.RequestStatusPolicyApproved)
	case autoclaimtypes.PolicyResultRejected:
		return c.transition(ctx, request, autoclaimtypes.RequestStatusPolicyRejected)
	case autoclaimtypes.PolicyResultManual:
		return c.transition(ctx, request, autoclaimtypes.RequestStatusManualApprovalRequired)
	default:
		return nil, fmt.Errorf("unknown policy decision for autoclaim request %s: %s", request.Key, decision.Result)
	}
}

func (c *Claimer) preparePolicyProof(
	ctx context.Context,
	request autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.AutoClaimRequest, error) {
	proof, err := c.proofPreparer.PrepareProof(ctx, request)
	if err != nil {
		if updateErr := c.storage.UpdateLastError(ctx, request.Key, err.Error(), c.now()); updateErr != nil {
			return nil, fmt.Errorf("record proof error for autoclaim request %s: %w", request.Key, updateErr)
		}
		return nil, fmt.Errorf("%w: prepare proof for autoclaim request %s: %w", ErrPolicyBlocked, request.Key, err)
	}
	if proof == nil || !proofReadyForRequest(*proof, request) {
		return nil, nil
	}
	if err := c.storage.SaveProof(ctx, request.Key, *proof); err != nil {
		return nil, fmt.Errorf("save proof for autoclaim request %s: %w", request.Key, err)
	}

	prepared := request
	prepared.Proof = proof
	prepared.L1InfoTreeIndex = &proof.L1InfoTreeIndex
	return &prepared, nil
}

func (c *Claimer) advanceManualDecision(
	ctx context.Context,
	request autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.AutoClaimRequest, error) {
	if request.ManualDecision == nil {
		return &request, nil
	}

	switch request.ManualDecision.Result {
	case autoclaimtypes.PolicyResultApproved:
		return c.transition(ctx, request, autoclaimtypes.RequestStatusPolicyApproved)
	case autoclaimtypes.PolicyResultRejected:
		return c.transition(ctx, request, autoclaimtypes.RequestStatusPolicyRejected)
	default:
		return &request, nil
	}
}

func (c *Claimer) sendWhenReady(ctx context.Context, request autoclaimtypes.AutoClaimRequest) error {
	current := &request
	var err error
	if current.Status == autoclaimtypes.RequestStatusQueued {
		current, err = c.transition(ctx, *current, autoclaimtypes.RequestStatusSending)
		if err != nil {
			return err
		}
	}

	proof := current.Proof
	if proof == nil {
		proof, err = c.proofPreparer.PrepareProof(ctx, *current)
		if err != nil {
			if updateErr := c.storage.UpdateLastError(ctx, current.Key, err.Error(), c.now()); updateErr != nil {
				return fmt.Errorf("record proof error for autoclaim request %s: %w", current.Key, updateErr)
			}
			return fmt.Errorf("prepare proof for autoclaim request %s: %w", current.Key, err)
		}
		if proof == nil {
			c.logInfof("autoclaim request %s: proof not ready yet (waiting for L2 GER injection)", current.Key)
			if current.Status == autoclaimtypes.RequestStatusSending {
				_, err = c.transition(ctx, *current, autoclaimtypes.RequestStatusQueued)
			}
			return err
		}
		if !proofReadyForRequest(*proof, *current) {
			c.logInfof("autoclaim request %s: proof not ready for request constraints", current.Key)
			if current.Status == autoclaimtypes.RequestStatusSending {
				_, err = c.transition(ctx, *current, autoclaimtypes.RequestStatusQueued)
			}
			return err
		}
		if err := c.storage.SaveProof(ctx, current.Key, *proof); err != nil {
			return fmt.Errorf("save proof for autoclaim request %s: %w", current.Key, err)
		}
	}

	_, err = c.sender.SubmitClaim(ctx, *current, *proof, c.target)
	if err == nil {
		return nil
	}

	retryScheduled, failErr := c.failIfRetriesExhausted(ctx, current.Key, err)
	if failErr != nil {
		return failErr
	}
	if retryScheduled {
		return nil
	}
	return fmt.Errorf("submit claim for autoclaim request %s: %w", current.Key, err)
}

func (c *Claimer) failIfRetriesExhausted(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	sendErr error,
) (bool, error) {
	latest, err := c.storage.GetRequest(ctx, key)
	if err != nil {
		return false, fmt.Errorf("get autoclaim request after send error %s: %w", key, err)
	}
	if latest.Status.IsTerminal() {
		return false, nil
	}
	maxRetries := latest.MaxRetries
	if maxRetries == 0 {
		maxRetries = c.target.MaxRetries
	}
	if latest.RetryCount < maxRetries {
		if updateErr := c.storage.UpdateLastError(ctx, key, sendErr.Error(), c.now()); updateErr != nil {
			return false, fmt.Errorf("record send error for autoclaim request %s: %w", key, updateErr)
		}
		if latest.Status == autoclaimtypes.RequestStatusSending {
			if _, err := c.transition(ctx, *latest, autoclaimtypes.RequestStatusQueued); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	if latest.Status.CanTransitionTo(autoclaimtypes.RequestStatusFailed) {
		if _, err := c.transition(ctx, *latest, autoclaimtypes.RequestStatusFailed); err != nil {
			return false, err
		}
	}
	if updateErr := c.storage.UpdateLastError(ctx, key, sendErr.Error(), c.now()); updateErr != nil {
		return false, fmt.Errorf("record send error for autoclaim request %s: %w", key, updateErr)
	}
	return false, nil
}

func proofReadyForRequest(
	proof autoclaimtypes.ClaimProof,
	request autoclaimtypes.AutoClaimRequest,
) bool {
	if request.Bridge.BlockNum == 0 {
		return proof.MainnetExitRoot != (common.Hash{}) && proof.GlobalExitRoot != (common.Hash{})
	}
	if proof.L1InfoTreeLeaf == nil {
		return false
	}
	return proof.MainnetExitRoot != (common.Hash{}) &&
		proof.GlobalExitRoot != (common.Hash{}) &&
		proof.L1InfoTreeLeaf.BlockNumber >= request.Bridge.BlockNum
}

func (c *Claimer) transition(
	ctx context.Context,
	request autoclaimtypes.AutoClaimRequest,
	to autoclaimtypes.RequestStatus,
) (*autoclaimtypes.AutoClaimRequest, error) {
	if request.Status == to {
		return &request, nil
	}
	next, err := c.storage.TransitionRequest(ctx, request.Key, request.Status, to, c.now())
	if err != nil {
		return nil, fmt.Errorf("transition autoclaim request %s from %s to %s: %w",
			request.Key, request.Status, to, err)
	}
	return next, nil
}

func (c *Claimer) requireDestination(request autoclaimtypes.AutoClaimRequest) error {
	if request.Bridge.DestinationNetwork == c.target.DestinationNetwork {
		return nil
	}
	return fmt.Errorf("%w: claimer %s handles %d, request %s targets %d",
		ErrDestinationMismatch, c.target.ID, c.target.DestinationNetwork, request.Key, request.Bridge.DestinationNetwork)
}

func (c *Claimer) logErrorf(format string, args ...interface{}) {
	if c.log != nil {
		c.log.Errorf(format, args...)
	}
}

func (c *Claimer) logInfof(format string, args ...interface{}) {
	if c.log != nil {
		c.log.Infof(format, args...)
	}
}
