package claimer

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"sync"
	"testing"
	"time"

	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	aggoracletypes "github.com/agglayer/aggkit/aggoracle/types"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var testNow = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

func TestEnqueueIsIdempotent(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	claimer := newTestClaimer(t, storage, approvedPolicy(), readyProof(), sender)
	bridge := makeBridge(1, 10)

	require.NoError(t, claimer.Enqueue(ctx, bridge))
	require.NoError(t, claimer.Enqueue(ctx, bridge))

	require.Equal(t, 1, storage.enqueueInsertions)
	require.Equal(t, 1, sender.submitCalls)
	require.Equal(t, autoclaimtypes.RequestStatusConfirmed, storage.mustRequest(t, bridge).Status)
}

func TestPolicyApprovedFlowSendsAndConfirms(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	claimer := newTestClaimer(t, storage, approvedPolicy(), readyProof(), sender)
	bridge := makeBridge(2, 10)

	require.NoError(t, claimer.Enqueue(ctx, bridge))

	request := storage.mustRequest(t, bridge)
	require.Equal(t, autoclaimtypes.RequestStatusConfirmed, request.Status)
	require.Equal(t, autoclaimtypes.PolicyResultApproved, request.PolicyDecision.Result)
	require.NotNil(t, request.Proof)
	require.Equal(t, 1, sender.submitCalls)
	require.Equal(t, uint32(10), sender.lastTarget.DestinationNetwork)
}

func TestPolicyRejectedFlowDoesNotSend(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	claimer := newTestClaimer(t, storage, rejectedPolicy(), readyProof(), sender)
	bridge := makeBridge(3, 10)

	require.NoError(t, claimer.Enqueue(ctx, bridge))

	request := storage.mustRequest(t, bridge)
	require.Equal(t, autoclaimtypes.RequestStatusPolicyRejected, request.Status)
	require.Equal(t, autoclaimtypes.PolicyResultRejected, request.PolicyDecision.Result)
	require.Equal(t, 0, sender.submitCalls)
}

func TestPolicyErrorBlocksRequestUntilRecoveryRetrySucceeds(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	policyErr := errors.New("target simulation unavailable")
	policy := &blockingPolicy{
		result: autoclaimtypes.PolicyResultApproved,
		err:    policyErr,
	}
	claimer := newTestClaimer(t, storage, policy, readyProof(), sender)
	bridge := makeBridge(33, 10)

	err := claimer.Enqueue(ctx, bridge)
	require.ErrorIs(t, err, ErrPolicyBlocked)
	require.ErrorIs(t, err, policyErr)

	request := storage.mustRequest(t, bridge)
	require.Equal(t, autoclaimtypes.RequestStatusDetected, request.Status)
	require.Nil(t, request.PolicyDecision)
	require.Equal(t, policyErr.Error(), request.LastError)
	require.Equal(t, 0, sender.submitCalls)

	policy.err = nil
	require.NoError(t, claimer.Recover(ctx))

	request = storage.mustRequest(t, bridge)
	require.Equal(t, autoclaimtypes.RequestStatusConfirmed, request.Status)
	require.NotNil(t, request.PolicyDecision)
	require.Equal(t, autoclaimtypes.PolicyResultApproved, request.PolicyDecision.Result)
	require.Equal(t, 1, sender.submitCalls)
}

func TestRecoverStopsAtFirstBlockingPolicyError(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	policyErr := errors.New("target simulation unavailable")
	claimer := newTestClaimer(
		t,
		storage,
		fakePolicy{result: autoclaimtypes.PolicyResultApproved, err: policyErr},
		readyProof(),
		sender,
	)
	firstBridge := makeBridge(34, 10)
	secondBridge := makeBridge(35, 10)
	insertStoredRequest(t, ctx, storage, firstBridge, autoclaimtypes.RequestStatusDetected)
	insertStoredRequest(t, ctx, storage, secondBridge, autoclaimtypes.RequestStatusPolicyApproved)

	err := claimer.Recover(ctx)

	require.ErrorIs(t, err, ErrPolicyBlocked)
	require.Equal(t, autoclaimtypes.RequestStatusDetected, storage.mustRequest(t, firstBridge).Status)
	require.Equal(t, autoclaimtypes.RequestStatusPolicyApproved, storage.mustRequest(t, secondBridge).Status)
	require.Equal(t, 0, sender.submitCalls)
}

func TestStartExitsAfterBlockingPolicyError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	claimer := newTestClaimer(
		t,
		storage,
		fakePolicy{result: autoclaimtypes.PolicyResultApproved, err: errors.New("target simulation unavailable")},
		readyProof(),
		sender,
		WithPollPeriod(time.Millisecond),
	)
	insertStoredRequest(t, ctx, storage, makeBridge(36, 10), autoclaimtypes.RequestStatusDetected)

	done := make(chan struct{})
	go func() {
		defer close(done)
		claimer.Start(ctx)
	}()

	requireClosed(t, done)
}

func TestManualFlowStaysIdleUntilApproved(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	claimer := newTestClaimer(t, storage, manualPolicy(), readyProof(), sender)
	bridge := makeBridge(4, 10)

	require.NoError(t, claimer.Enqueue(ctx, bridge))
	request := storage.mustRequest(t, bridge)
	require.Equal(t, autoclaimtypes.RequestStatusManualApprovalRequired, request.Status)
	require.Equal(t, 0, sender.submitCalls)

	_, err := storage.TransitionRequest(
		ctx,
		request.Key,
		autoclaimtypes.RequestStatusManualApprovalRequired,
		autoclaimtypes.RequestStatusPolicyApproved,
		testNow.Add(time.Minute),
	)
	require.NoError(t, err)
	require.NoError(t, claimer.Advance(ctx, request.Key))

	require.Equal(t, autoclaimtypes.RequestStatusConfirmed, storage.mustRequest(t, bridge).Status)
	require.Equal(t, 1, sender.submitCalls)
}

func TestManualFlowUsesManualDecisionToAdvance(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	claimer := newTestClaimer(t, storage, manualPolicy(), readyProof(), sender)
	bridge := makeBridge(44, 10)

	require.NoError(t, claimer.Enqueue(ctx, bridge))
	request := storage.mustRequest(t, bridge)
	require.Equal(t, autoclaimtypes.RequestStatusManualApprovalRequired, request.Status)

	require.NoError(t, storage.RecordManualDecision(ctx, request.Key, autoclaimtypes.PolicyDecision{
		PolicyName: "manual",
		Result:     autoclaimtypes.PolicyResultApproved,
		Reason:     "approved by test",
	}))
	require.NoError(t, claimer.Advance(ctx, request.Key))

	request = storage.mustRequest(t, bridge)
	require.Equal(t, autoclaimtypes.RequestStatusConfirmed, request.Status)
	require.NotNil(t, request.PolicyDecision)
	require.Equal(t, autoclaimtypes.PolicyResultManual, request.PolicyDecision.Result)
	require.NotNil(t, request.ManualDecision)
	require.Equal(t, autoclaimtypes.PolicyResultApproved, request.ManualDecision.Result)
	require.Equal(t, 1, sender.submitCalls)
}

func TestProofNotReadyLeavesRequestQueued(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	claimer := newTestClaimer(t, storage, approvedPolicy(), pendingProof(), sender)
	bridge := makeBridge(5, 10)

	require.NoError(t, claimer.Enqueue(ctx, bridge))

	request := storage.mustRequest(t, bridge)
	require.Equal(t, autoclaimtypes.RequestStatusQueued, request.Status)
	require.Nil(t, request.Proof)
	require.Equal(t, 0, sender.submitCalls)
}

func TestProofNotReadyBeforeProofPolicyLeavesRequestDetected(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	claimer := newTestClaimer(t, storage, proofRequiredPolicy{
		result: autoclaimtypes.PolicyResultApproved,
	}, pendingProof(), sender)
	bridge := makeBridge(37, 10)

	require.NoError(t, claimer.Enqueue(ctx, bridge))

	request := storage.mustRequest(t, bridge)
	require.Equal(t, autoclaimtypes.RequestStatusDetected, request.Status)
	require.Nil(t, request.PolicyDecision)
	require.Nil(t, request.Proof)
	require.Equal(t, 0, sender.submitCalls)
}

func TestProofErrorBeforeProofPolicyBlocksRequest(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	proofErr := errors.New("proof unavailable")
	claimer := newTestClaimer(t, storage, proofRequiredPolicy{
		result: autoclaimtypes.PolicyResultApproved,
	}, fakeProofPreparer{err: proofErr}, sender)
	bridge := makeBridge(38, 10)

	err := claimer.Enqueue(ctx, bridge)

	require.ErrorIs(t, err, ErrPolicyBlocked)
	require.ErrorIs(t, err, proofErr)
	request := storage.mustRequest(t, bridge)
	require.Equal(t, autoclaimtypes.RequestStatusDetected, request.Status)
	require.Equal(t, proofErr.Error(), request.LastError)
	require.Nil(t, request.PolicyDecision)
	require.Equal(t, 0, sender.submitCalls)
}

func TestStaleProofLeavesRequestQueuedWithoutSending(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	proof := autoclaimtypes.ClaimProof{
		L1InfoTreeIndex: 1,
		L1InfoTreeLeaf:  &l1infotreesync.L1InfoTreeLeaf{BlockNumber: 100},
		PreparedAt:      testNow,
	}
	claimer := newTestClaimer(t, storage, approvedPolicy(), fakeProofPreparer{proof: &proof}, sender)
	bridge := makeBridge(50, 10)
	bridge.BlockNum = proof.L1InfoTreeLeaf.BlockNumber + 1

	require.NoError(t, claimer.Enqueue(ctx, bridge))

	request := storage.mustRequest(t, bridge)
	require.Equal(t, autoclaimtypes.RequestStatusQueued, request.Status)
	require.Nil(t, request.Proof)
	require.Equal(t, uint64(0), request.RetryCount)
	require.Equal(t, 0, sender.submitCalls)
}

func TestProofWithoutL1InfoTreeLeafLeavesRequestQueuedWithoutSending(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	proof := autoclaimtypes.ClaimProof{
		L1InfoTreeIndex: 1,
		PreparedAt:      testNow,
	}
	claimer := newTestClaimer(t, storage, approvedPolicy(), fakeProofPreparer{proof: &proof}, sender)
	bridge := makeBridge(51, 10)

	require.NoError(t, claimer.Enqueue(ctx, bridge))

	request := storage.mustRequest(t, bridge)
	require.Equal(t, autoclaimtypes.RequestStatusQueued, request.Status)
	require.Nil(t, request.Proof)
	require.Equal(t, uint64(0), request.RetryCount)
	require.Equal(t, 0, sender.submitCalls)
}

func TestRetryExhaustionFailsRequest(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sendErr := errors.New("tx failed")
	sender := &fakeSender{
		storage: storage,
		err:     sendErr,
		onSubmit: func(ctx context.Context, request autoclaimtypes.AutoClaimRequest) {
			attempt := autoclaimtypes.TransactionAttempt{
				RequestKey:    request.Key,
				ClaimerID:     "claimer-10",
				AttemptNumber: 1,
				RetryCount:    1,
				MaxRetries:    1,
				Status:        ethtxtypes.MonitoredTxStatusFailed,
				LastError:     sendErr.Error(),
				CreatedAt:     testNow,
				UpdatedAt:     testNow,
			}
			require.NoError(t, storage.RecordTransactionAttempt(ctx, request.Key, attempt))
		},
	}
	target := makeTarget(10)
	target.MaxRetries = 1
	claimer := newTestClaimerForTarget(t, target, storage, approvedPolicy(), readyProof(), sender)
	bridge := makeBridge(6, 10)

	err := claimer.Enqueue(ctx, bridge)
	require.ErrorIs(t, err, sendErr)

	request := storage.mustRequest(t, bridge)
	require.Equal(t, autoclaimtypes.RequestStatusFailed, request.Status)
	require.Equal(t, sendErr.Error(), request.LastError)
	require.Equal(t, 1, sender.submitCalls)
}

func TestRetryableSendErrorLeavesRequestQueued(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sendErr := errors.New("global exit root not injected")
	sender := &fakeSender{
		storage: storage,
		err:     sendErr,
		onSubmit: func(ctx context.Context, request autoclaimtypes.AutoClaimRequest) {
			attempt := autoclaimtypes.TransactionAttempt{
				RequestKey:    request.Key,
				ClaimerID:     "claimer-10",
				AttemptNumber: 1,
				RetryCount:    1,
				MaxRetries:    3,
				Status:        ethtxtypes.MonitoredTxStatusFailed,
				LastError:     sendErr.Error(),
				CreatedAt:     testNow,
				UpdatedAt:     testNow,
			}
			require.NoError(t, storage.RecordTransactionAttempt(ctx, request.Key, attempt))
		},
	}
	target := makeTarget(10)
	target.MaxRetries = 3
	claimer := newTestClaimerForTarget(t, target, storage, approvedPolicy(), readyProof(), sender)
	bridge := makeBridge(7, 10)

	require.NoError(t, claimer.Enqueue(ctx, bridge))

	request := storage.mustRequest(t, bridge)
	require.Equal(t, autoclaimtypes.RequestStatusQueued, request.Status)
	require.Equal(t, uint64(1), request.RetryCount)
	require.Equal(t, sendErr.Error(), request.LastError)
	require.Equal(t, 1, sender.submitCalls)
}

func TestRestartRecoveryResumesRecoverableRequests(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	claimer := newTestClaimer(t, storage, approvedPolicy(), readyProof(), sender)
	bridge := makeBridge(8, 10)
	request := autoclaimtypes.NewRequestFromBridgeExit(bridge, testNow)
	request.Status = autoclaimtypes.RequestStatusPolicyApproved
	request.MaxRetries = 2
	_, inserted, err := storage.EnqueueRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, inserted)

	require.NoError(t, claimer.Recover(ctx))

	require.Equal(t, autoclaimtypes.RequestStatusConfirmed, storage.mustRequest(t, bridge).Status)
	require.Equal(t, 1, sender.submitCalls)
}

func TestRestartRecoveryUsesStableSnapshotAcrossPages(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	claimer := newTestClaimer(
		t,
		storage,
		approvedPolicy(),
		readyProof(),
		sender,
		WithRecoverPageSize(2),
	)
	recoverable := []autoclaimtypes.RequestStatus{
		autoclaimtypes.RequestStatusPolicyApproved,
		autoclaimtypes.RequestStatusQueued,
		autoclaimtypes.RequestStatusSending,
		autoclaimtypes.RequestStatusSent,
		autoclaimtypes.RequestStatusPolicyApproved,
	}
	bridges := make([]autoclaimtypes.BridgeExit, 0, len(recoverable))
	for i, status := range recoverable {
		bridge := makeBridge(uint32(30+i), 10)
		bridges = append(bridges, bridge)
		insertStoredRequest(t, ctx, storage, bridge, status)
	}

	require.NoError(t, claimer.Recover(ctx))

	for _, bridge := range bridges {
		require.Equal(t, autoclaimtypes.RequestStatusConfirmed, storage.mustRequest(t, bridge).Status)
	}
	require.Equal(t, len(recoverable), sender.submitCalls)
}

func TestDisabledClaimerDoesNotEnqueueOrSend(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	claimer := newTestClaimer(
		t,
		storage,
		approvedPolicy(),
		readyProof(),
		sender,
		WithEnabled(false),
	)

	err := claimer.Enqueue(ctx, makeBridge(8, 10))
	require.ErrorIs(t, err, ErrDisabled)
	require.Empty(t, storage.requests)
	require.Equal(t, 0, sender.submitCalls)
	require.ErrorIs(t, claimer.Recover(ctx), ErrDisabled)
}

func TestDestinationNetworkRouting(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	claimer10 := newTestClaimer(t, storage, approvedPolicy(), readyProof(), sender)
	claimer11 := newTestClaimerForTarget(
		t,
		makeTarget(11),
		storage,
		approvedPolicy(),
		readyProof(),
		&fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed},
	)
	registry, err := NewRegistry(claimer10, claimer11)
	require.NoError(t, err)

	resolved, ok, err := registry.ClaimerForDestination(ctx, 10)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint32(10), resolved.Target().DestinationNetwork)

	err = claimer10.Enqueue(ctx, makeBridge(9, 11))
	require.ErrorIs(t, err, ErrDestinationMismatch)
	require.Empty(t, storage.requests)
	require.Equal(t, 0, sender.submitCalls)
}

func TestStartRunsWithAPIDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	storage := newMemoryStorage()
	sender := &fakeSender{storage: storage, finalStatus: autoclaimtypes.RequestStatusConfirmed}
	claimer := newTestClaimer(t, storage, approvedPolicy(), readyProof(), sender, WithPollPeriod(time.Millisecond))
	bridge := makeBridge(10, 10)
	request := autoclaimtypes.NewRequestFromBridgeExit(bridge, testNow)
	request.Status = autoclaimtypes.RequestStatusQueued
	_, inserted, err := storage.EnqueueRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, inserted)

	done := make(chan struct{})
	go func() {
		defer close(done)
		claimer.Start(ctx)
	}()
	require.Eventually(t, func() bool {
		return storage.mustRequest(t, bridge).Status == autoclaimtypes.RequestStatusConfirmed
	}, time.Second, time.Millisecond)
	cancel()
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
}

func newTestClaimer(
	t *testing.T,
	storage *memoryStorage,
	policy autoclaimtypes.Policy,
	proof autoclaimtypes.ProofPreparer,
	sender *fakeSender,
	options ...Option,
) *Claimer {
	t.Helper()

	return newTestClaimerForTarget(t, makeTarget(10), storage, policy, proof, sender, options...)
}

func newTestClaimerForTarget(
	t *testing.T,
	target autoclaimtypes.ClaimerTarget,
	storage *memoryStorage,
	policy autoclaimtypes.Policy,
	proof autoclaimtypes.ProofPreparer,
	sender *fakeSender,
	options ...Option,
) *Claimer {
	t.Helper()

	options = append([]Option{WithNow(func() time.Time { return testNow })}, options...)
	claimer, err := New(target, storage, policy, proof, sender, options...)
	require.NoError(t, err)
	return claimer
}

func makeTarget(destination uint32) autoclaimtypes.ClaimerTarget {
	return autoclaimtypes.ClaimerTarget{
		ID:                 fmt.Sprintf("claimer-%d", destination),
		DestinationNetwork: destination,
		NetworkType:        "evm",
		BridgeAddr:         common.HexToAddress("0x5000000000000000000000000000000000000005"),
		WaitPeriod:         time.Millisecond,
		RetryAfter:         time.Millisecond,
		MaxRetries:         2,
	}
}

func makeBridge(depositCount uint32, destination uint32) autoclaimtypes.BridgeExit {
	return autoclaimtypes.BridgeExit{
		BlockNum:           100 + uint64(depositCount),
		BlockPos:           uint64(depositCount),
		TxHash:             common.BigToHash(big.NewInt(int64(depositCount))),
		LeafType:           bridgesynctypes.LeafTypeAsset,
		OriginNetwork:      autoclaimtypes.L1OriginNetwork,
		OriginAddress:      common.HexToAddress("0x1000000000000000000000000000000000000001"),
		DestinationNetwork: destination,
		DestinationAddress: common.HexToAddress("0x2000000000000000000000000000000000000002"),
		Amount:             big.NewInt(1000 + int64(depositCount)),
		Metadata:           []byte{byte(depositCount)},
		DepositCount:       depositCount,
		TxnSender:          common.HexToAddress("0x3000000000000000000000000000000000000003"),
		ToAddress:          common.HexToAddress("0x4000000000000000000000000000000000000004"),
		GlobalIndex:        autoclaimtypes.DeriveGlobalIndex(autoclaimtypes.L1OriginNetwork, depositCount),
	}
}

func insertStoredRequest(
	t *testing.T,
	ctx context.Context,
	storage *memoryStorage,
	bridge autoclaimtypes.BridgeExit,
	status autoclaimtypes.RequestStatus,
) {
	t.Helper()

	request := autoclaimtypes.NewRequestFromBridgeExit(bridge, testNow)
	request.Status = status
	request.MaxRetries = 2
	_, inserted, err := storage.EnqueueRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, inserted)
}

func approvedPolicy() autoclaimtypes.Policy {
	return fakePolicy{result: autoclaimtypes.PolicyResultApproved}
}

func rejectedPolicy() autoclaimtypes.Policy {
	return fakePolicy{result: autoclaimtypes.PolicyResultRejected}
}

func manualPolicy() autoclaimtypes.Policy {
	return fakePolicy{result: autoclaimtypes.PolicyResultManual}
}

func requireClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	require.Eventually(t, func() bool {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
}

type fakePolicy struct {
	result autoclaimtypes.PolicyResult
	err    error
}

func (p fakePolicy) Evaluate(
	_ context.Context,
	_ autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.PolicyDecision, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &autoclaimtypes.PolicyDecision{
		PolicyName: "test-policy",
		Result:     p.result,
		Reason:     p.result.String(),
		CreatedAt:  testNow,
		UpdatedAt:  testNow,
	}, nil
}

type proofRequiredPolicy fakePolicy

func (p proofRequiredPolicy) RequiresPreparedProof() bool {
	return true
}

func (p proofRequiredPolicy) Evaluate(
	ctx context.Context,
	request autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.PolicyDecision, error) {
	return fakePolicy(p).Evaluate(ctx, request)
}

type blockingPolicy struct {
	result autoclaimtypes.PolicyResult
	err    error
}

func (p *blockingPolicy) Evaluate(
	_ context.Context,
	_ autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.PolicyDecision, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &autoclaimtypes.PolicyDecision{
		PolicyName: "blocking-policy",
		Result:     p.result,
		Reason:     p.result.String(),
		CreatedAt:  testNow,
		UpdatedAt:  testNow,
	}, nil
}

func readyProof() autoclaimtypes.ProofPreparer {
	return fakeProofPreparer{proof: &autoclaimtypes.ClaimProof{
		L1InfoTreeIndex: 1,
		L1InfoTreeLeaf:  &l1infotreesync.L1InfoTreeLeaf{BlockNumber: 1_000_000},
		MainnetExitRoot: common.HexToHash("0x100"),
		GlobalExitRoot:  common.HexToHash("0x102"),
		PreparedAt:      testNow,
	}}
}

func pendingProof() autoclaimtypes.ProofPreparer {
	return fakeProofPreparer{}
}

type fakeProofPreparer struct {
	proof *autoclaimtypes.ClaimProof
	err   error
}

func (p fakeProofPreparer) PrepareProof(
	_ context.Context,
	_ autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.ClaimProof, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.proof, nil
}

type fakeSender struct {
	storage     *memoryStorage
	finalStatus autoclaimtypes.RequestStatus
	err         error
	onSubmit    func(context.Context, autoclaimtypes.AutoClaimRequest)
	submitCalls int
	lastTarget  autoclaimtypes.ClaimerTarget
}

func (s *fakeSender) SubmitClaim(
	ctx context.Context,
	request autoclaimtypes.AutoClaimRequest,
	_ autoclaimtypes.ClaimProof,
	target autoclaimtypes.ClaimerTarget,
) (*autoclaimtypes.TransactionAttempt, error) {
	s.submitCalls++
	s.lastTarget = target
	if s.onSubmit != nil {
		s.onSubmit(ctx, request)
	}
	if s.err != nil {
		return nil, s.err
	}
	if s.finalStatus == autoclaimtypes.RequestStatusConfirmed {
		latest, err := s.storage.GetRequest(ctx, request.Key)
		if err != nil {
			return nil, err
		}
		if latest.Status == autoclaimtypes.RequestStatusSending {
			_, err = s.storage.TransitionRequest(ctx, request.Key, latest.Status, autoclaimtypes.RequestStatusSent, testNow)
			if err != nil {
				return nil, err
			}
			latest, err = s.storage.GetRequest(ctx, request.Key)
			if err != nil {
				return nil, err
			}
		}
		if latest.Status == autoclaimtypes.RequestStatusSent {
			_, err = s.storage.TransitionRequest(ctx, request.Key, latest.Status, autoclaimtypes.RequestStatusConfirmed, testNow)
			if err != nil {
				return nil, err
			}
		}
	}
	return &autoclaimtypes.TransactionAttempt{RequestKey: request.Key, ClaimerID: target.ID}, nil
}

func (s *fakeSender) EthTxManager() aggoracletypes.EthTxManager {
	return nil
}

type memoryStorage struct {
	mu                sync.Mutex
	requests          map[autoclaimtypes.RequestKey]autoclaimtypes.AutoClaimRequest
	enqueueInsertions int
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{requests: make(map[autoclaimtypes.RequestKey]autoclaimtypes.AutoClaimRequest)}
}

func (s *memoryStorage) EnqueueRequest(
	_ context.Context,
	request autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.AutoClaimRequest, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if request.Key == "" {
		request.Key = autoclaimtypes.DeriveRequestKey(
			request.Bridge.OriginNetwork,
			request.Bridge.DestinationNetwork,
			request.Bridge.DepositCount,
		)
	}
	if existing, ok := s.requests[request.Key]; ok {
		return copyRequest(existing), false, nil
	}
	if request.Status == "" {
		request.Status = autoclaimtypes.RequestStatusDetected
	}
	if request.GlobalIndex == nil {
		request.GlobalIndex = autoclaimtypes.DeriveGlobalIndex(request.Bridge.OriginNetwork, request.Bridge.DepositCount)
	}
	s.requests[request.Key] = request
	s.enqueueInsertions++
	return copyRequest(request), true, nil
}

func (s *memoryStorage) GetRequest(
	_ context.Context,
	key autoclaimtypes.RequestKey,
) (*autoclaimtypes.AutoClaimRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	request, ok := s.requests[key]
	if !ok {
		return nil, fmt.Errorf("missing request %s", key)
	}
	return copyRequest(request), nil
}

func (s *memoryStorage) ListRequests(
	_ context.Context,
	_ autoclaimtypes.RequestFilter,
) (*autoclaimtypes.RequestPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	requests := make([]*autoclaimtypes.AutoClaimRequest, 0, len(s.requests))
	for _, request := range s.requests {
		requests = append(requests, copyRequest(request))
	}
	return &autoclaimtypes.RequestPage{Requests: requests, Count: len(requests)}, nil
}

func (s *memoryStorage) ListRecoverableRequests(
	_ context.Context,
	filter autoclaimtypes.RecoveryFilter,
) (*autoclaimtypes.RequestPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	statuses := make(map[autoclaimtypes.RequestStatus]struct{}, len(filter.Statuses))
	for _, status := range filter.Statuses {
		statuses[status] = struct{}{}
	}
	pageSize := filter.PageSize
	if pageSize == 0 {
		pageSize = 100
	}
	offset := int(filter.PageNumber * pageSize)
	matches := make([]*autoclaimtypes.AutoClaimRequest, 0)
	for _, request := range s.requests {
		if filter.DestinationNetwork != nil && request.Bridge.DestinationNetwork != *filter.DestinationNetwork {
			continue
		}
		if _, ok := statuses[request.Status]; !ok {
			continue
		}
		matches = append(matches, copyRequest(request))
	}
	slices.SortFunc(matches, func(a, b *autoclaimtypes.AutoClaimRequest) int {
		return cmp.Compare(a.Key, b.Key)
	})
	if offset >= len(matches) {
		return &autoclaimtypes.RequestPage{Requests: []*autoclaimtypes.AutoClaimRequest{}, Count: len(matches)}, nil
	}
	end := min(offset+int(pageSize), len(matches))
	return &autoclaimtypes.RequestPage{Requests: matches[offset:end], Count: len(matches)}, nil
}

func (s *memoryStorage) RecordPolicyDecision(
	_ context.Context,
	key autoclaimtypes.RequestKey,
	decision autoclaimtypes.PolicyDecision,
) error {
	return s.update(key, func(request *autoclaimtypes.AutoClaimRequest) {
		request.PolicyDecision = &decision
	})
}

func (s *memoryStorage) RecordManualDecision(
	_ context.Context,
	key autoclaimtypes.RequestKey,
	decision autoclaimtypes.PolicyDecision,
) error {
	return s.update(key, func(request *autoclaimtypes.AutoClaimRequest) {
		request.ManualDecision = &decision
	})
}

func (s *memoryStorage) SaveProof(
	_ context.Context,
	key autoclaimtypes.RequestKey,
	proof autoclaimtypes.ClaimProof,
) error {
	return s.update(key, func(request *autoclaimtypes.AutoClaimRequest) {
		request.Proof = &proof
		request.L1InfoTreeIndex = &proof.L1InfoTreeIndex
	})
}

func (s *memoryStorage) RecordTransactionAttempt(
	_ context.Context,
	key autoclaimtypes.RequestKey,
	attempt autoclaimtypes.TransactionAttempt,
) error {
	return s.update(key, func(request *autoclaimtypes.AutoClaimRequest) {
		request.RetryCount = attempt.RetryCount
		request.MaxRetries = attempt.MaxRetries
		if attempt.ClaimTxHash != (common.Hash{}) {
			request.ClaimTxHash = &attempt.ClaimTxHash
		}
		if attempt.TxManagerID != (common.Hash{}) {
			request.TxManagerID = &attempt.TxManagerID
		}
		request.LastError = attempt.LastError
	})
}

func (s *memoryStorage) TransitionRequest(
	_ context.Context,
	key autoclaimtypes.RequestKey,
	from autoclaimtypes.RequestStatus,
	to autoclaimtypes.RequestStatus,
	now time.Time,
) (*autoclaimtypes.AutoClaimRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	request, ok := s.requests[key]
	if !ok {
		return nil, fmt.Errorf("missing request %s", key)
	}
	if request.Status != from {
		return nil, fmt.Errorf("precondition failed: got %s want %s", request.Status, from)
	}
	if !from.CanTransitionTo(to) {
		return nil, fmt.Errorf("invalid transition: %s to %s", from, to)
	}
	request.Status = to
	request.UpdatedAt = now
	s.requests[key] = request
	return copyRequest(request), nil
}

func (s *memoryStorage) UpdateLastError(
	_ context.Context,
	key autoclaimtypes.RequestKey,
	lastError string,
	now time.Time,
) error {
	return s.update(key, func(request *autoclaimtypes.AutoClaimRequest) {
		request.LastError = lastError
		request.UpdatedAt = now
	})
}

func (s *memoryStorage) mustRequest(t *testing.T, bridge autoclaimtypes.BridgeExit) autoclaimtypes.AutoClaimRequest {
	t.Helper()

	key := autoclaimtypes.DeriveRequestKey(bridge.OriginNetwork, bridge.DestinationNetwork, bridge.DepositCount)
	request, err := s.GetRequest(context.Background(), key)
	require.NoError(t, err)
	return *request
}

func (s *memoryStorage) update(
	key autoclaimtypes.RequestKey,
	fn func(*autoclaimtypes.AutoClaimRequest),
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	request, ok := s.requests[key]
	if !ok {
		return fmt.Errorf("missing request %s", key)
	}
	fn(&request)
	s.requests[key] = request
	return nil
}

func copyRequest(request autoclaimtypes.AutoClaimRequest) *autoclaimtypes.AutoClaimRequest {
	copied := request
	if request.GlobalIndex != nil {
		copied.GlobalIndex = new(big.Int).Set(request.GlobalIndex)
	}
	if request.L1InfoTreeIndex != nil {
		index := *request.L1InfoTreeIndex
		copied.L1InfoTreeIndex = &index
	}
	if request.Bridge.GlobalIndex != nil {
		copied.Bridge.GlobalIndex = new(big.Int).Set(request.Bridge.GlobalIndex)
	}
	if request.Bridge.L1InfoTreeIndex != nil {
		index := *request.Bridge.L1InfoTreeIndex
		copied.Bridge.L1InfoTreeIndex = &index
	}
	if request.PolicyDecision != nil {
		decision := *request.PolicyDecision
		copied.PolicyDecision = &decision
	}
	if request.ManualDecision != nil {
		decision := *request.ManualDecision
		copied.ManualDecision = &decision
	}
	if request.Proof != nil {
		proof := *request.Proof
		copied.Proof = &proof
	}
	if request.ClaimTxHash != nil {
		hash := *request.ClaimTxHash
		copied.ClaimTxHash = &hash
	}
	if request.TxManagerID != nil {
		hash := *request.TxManagerID
		copied.TxManagerID = &hash
	}
	return &copied
}
