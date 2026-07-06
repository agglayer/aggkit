package storage

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/db"
	logger "github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func newTestStorage(t *testing.T) (*Storage, *sql.DB) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "autoclaim.sqlite")
	storage, err := NewStandalone(logger.GetDefaultLogger(), dbPath, 30*time.Second)
	require.NoError(t, err)

	return storage, storage.database
}

func makeRequest(
	depositCount uint32,
	destinationNetwork uint32,
	status autoclaimtypes.RequestStatus,
) autoclaimtypes.AutoClaimRequest {
	now := time.Date(2026, 6, 3, 12, 0, int(depositCount), 0, time.UTC)
	bridge := autoclaimtypes.BridgeExit{
		BlockNum:           100 + uint64(depositCount),
		BlockPos:           uint64(depositCount),
		TxHash:             common.BigToHash(big.NewInt(int64(depositCount))),
		OriginNetwork:      autoclaimtypes.L1OriginNetwork,
		OriginAddress:      common.HexToAddress("0x1000000000000000000000000000000000000001"),
		DestinationNetwork: destinationNetwork,
		DestinationAddress: common.HexToAddress("0x2000000000000000000000000000000000000002"),
		Amount:             big.NewInt(1000 + int64(depositCount)),
		Metadata:           []byte{byte(depositCount)},
		DepositCount:       depositCount,
		TxnSender:          common.HexToAddress("0x3000000000000000000000000000000000000003"),
		ToAddress:          common.HexToAddress("0x4000000000000000000000000000000000000004"),
		GlobalIndex:        autoclaimtypes.DeriveGlobalIndex(autoclaimtypes.L1OriginNetwork, depositCount),
	}

	return autoclaimtypes.AutoClaimRequest{
		Key:         autoclaimtypes.DeriveRequestKey(bridge.OriginNetwork, bridge.DestinationNetwork, bridge.DepositCount),
		Status:      status,
		Bridge:      bridge,
		GlobalIndex: new(big.Int).Set(bridge.GlobalIndex),
		MaxRetries:  4,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func enqueueRequest(t *testing.T, ctx context.Context, storage *Storage, request autoclaimtypes.AutoClaimRequest) {
	t.Helper()

	_, inserted, err := storage.EnqueueRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, inserted)
}

func TestMigrationCreatesExpectedSchema(t *testing.T) {
	storage, database := newTestStorage(t)
	defer storage.Close()

	for _, table := range []string{
		"autoclaim_request",
		"autoclaim_transaction_attempt",
		"autoclaim_bridge_cursor",
	} {
		var name string
		err := database.QueryRow(
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&name)
		require.NoError(t, err)
		require.Equal(t, table, name)
	}
}

func TestBridgeCursorPersistence(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	cursor := autoclaimtypes.BridgeCursor{
		FromBlock: 10,
		ToBlock:   20,
		BlockNum:  20,
		BlockPos:  3,
	}
	require.NoError(t, storage.SaveBridgeCursor(ctx, "l1-to-l2", cursor, time.Now().UTC()))

	stored, found, err := storage.GetBridgeCursor(ctx, "l1-to-l2")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, cursor, *stored)

	cursor.ToBlock = 25
	cursor.BlockNum = 25
	require.NoError(t, storage.SaveBridgeCursor(ctx, "l1-to-l2", cursor, time.Now().UTC()))

	stored, found, err = storage.GetBridgeCursor(ctx, "l1-to-l2")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, cursor, *stored)

	_, found, err = storage.GetBridgeCursor(ctx, "missing")
	require.NoError(t, err)
	require.False(t, found)
}

func TestEnqueueRequestIsIdempotentAndDetectsDuplicates(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	request := makeRequest(1, 10, autoclaimtypes.RequestStatusDetected)

	first, inserted, err := storage.EnqueueRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, inserted)
	require.Equal(t, request.Key, first.Key)

	duplicate := request
	duplicate.LastError = "must not overwrite"
	second, inserted, err := storage.EnqueueRequest(ctx, duplicate)
	require.NoError(t, err)
	require.False(t, inserted)
	require.Equal(t, request.Key, second.Key)
	require.Empty(t, second.LastError)

	page, err := storage.ListRequests(ctx, autoclaimtypes.RequestFilter{})
	require.NoError(t, err)
	require.Equal(t, 1, page.Count)
	require.Len(t, page.Requests, 1)
}

func TestListRequestsFiltersAndPagination(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	requests := []autoclaimtypes.AutoClaimRequest{
		makeRequest(1, 10, autoclaimtypes.RequestStatusDetected),
		makeRequest(2, 10, autoclaimtypes.RequestStatusDetected),
		makeRequest(3, 11, autoclaimtypes.RequestStatusDetected),
	}
	for _, request := range requests {
		enqueueRequest(t, ctx, storage, request)
	}

	approved := autoclaimtypes.PolicyDecision{
		PolicyName: "allow-all",
		Result:     autoclaimtypes.PolicyResultApproved,
		Reason:     "test",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	require.NoError(t, storage.RecordPolicyDecision(ctx, requests[1].Key, approved))

	sentAt := time.Now().UTC()
	attempt := autoclaimtypes.TransactionAttempt{
		RequestKey:       requests[1].Key,
		AttemptNumber:    1,
		TxManagerID:      common.HexToHash("0x1234"),
		ClaimTxHash:      common.HexToHash("0x5678"),
		Status:           ethtxtypes.MonitoredTxStatusSent,
		RetryCount:       1,
		MaxRetries:       4,
		SentAt:           &sentAt,
		CreatedAt:        sentAt,
		UpdatedAt:        sentAt,
		TargetBridgeAddr: common.HexToAddress("0x5000000000000000000000000000000000000005"),
	}
	require.NoError(t, storage.RecordTransactionAttempt(ctx, requests[1].Key, attempt))

	destinationNetwork := uint32(10)
	status := autoclaimtypes.RequestStatusDetected
	policyResult := autoclaimtypes.PolicyResultApproved
	bridgeTxHash := requests[1].Bridge.TxHash
	claimTxHash := attempt.ClaimTxHash
	fromBlock := uint64(101)
	toBlock := uint64(103)

	page, err := storage.ListRequests(ctx, autoclaimtypes.RequestFilter{
		DestinationNetwork: &destinationNetwork,
		Status:             &status,
		PolicyResult:       &policyResult,
		BridgeTxHash:       &bridgeTxHash,
		ClaimTxHash:        &claimTxHash,
		FromBlock:          &fromBlock,
		ToBlock:            &toBlock,
		PageSize:           1,
	})
	require.NoError(t, err)
	require.Equal(t, 1, page.Count)
	require.Len(t, page.Requests, 1)
	require.Equal(t, requests[1].Key, page.Requests[0].Key)
	require.Equal(t, autoclaimtypes.PolicyResultApproved, page.Requests[0].PolicyDecision.Result)
	require.Equal(t, attempt.ClaimTxHash, *page.Requests[0].ClaimTxHash)

	originNetwork := autoclaimtypes.L1OriginNetwork
	page, err = storage.ListRequests(ctx, autoclaimtypes.RequestFilter{
		OriginNetwork: &originNetwork,
		PageSize:      2,
	})
	require.NoError(t, err)
	require.Equal(t, 3, page.Count)
	require.Len(t, page.Requests, 2)

	page, err = storage.ListRequests(ctx, autoclaimtypes.RequestFilter{
		OriginNetwork: &originNetwork,
		PageNumber:    1,
		PageSize:      2,
	})
	require.NoError(t, err)
	require.Equal(t, 3, page.Count)
	require.Len(t, page.Requests, 1)
}

func TestListRequestsRejectsOversizedPageSize(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()

	_, err := storage.ListRequests(context.Background(), autoclaimtypes.RequestFilter{
		PageSize: autoclaimtypes.MaxRequestPageSize + 1,
	})

	require.ErrorContains(t, err, "exceeds maximum")
}

func TestTransitionRequestPreconditions(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	request := makeRequest(1, 10, autoclaimtypes.RequestStatusDetected)
	enqueueRequest(t, ctx, storage, request)

	now := time.Now().UTC().Add(time.Minute)
	transitioned, err := storage.TransitionRequest(
		ctx,
		request.Key,
		autoclaimtypes.RequestStatusDetected,
		autoclaimtypes.RequestStatusPolicyApproved,
		now,
	)
	require.NoError(t, err)
	require.Equal(t, autoclaimtypes.RequestStatusPolicyApproved, transitioned.Status)
	require.Equal(t, now, transitioned.UpdatedAt)

	_, err = storage.TransitionRequest(
		ctx,
		request.Key,
		autoclaimtypes.RequestStatusDetected,
		autoclaimtypes.RequestStatusQueued,
		time.Now().UTC(),
	)
	require.ErrorIs(t, err, ErrInvalidTransition)

	_, err = storage.TransitionRequest(
		ctx,
		request.Key,
		autoclaimtypes.RequestStatusDetected,
		autoclaimtypes.RequestStatusPolicyRejected,
		time.Now().UTC(),
	)
	require.ErrorIs(t, err, ErrPreconditionFailed)
}

func TestRecordTransactionAttemptAndTimestampUpdates(t *testing.T) {
	storage, database := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	request := makeRequest(1, 10, autoclaimtypes.RequestStatusQueued)
	request.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
	request.UpdatedAt = request.CreatedAt
	enqueueRequest(t, ctx, storage, request)

	before := request.UpdatedAt
	sentAt := before.Add(time.Hour)
	attempt := autoclaimtypes.TransactionAttempt{
		RequestKey:       request.Key,
		ClaimerID:        "claimer-10",
		AttemptNumber:    1,
		TxManagerID:      common.HexToHash("0x1111"),
		ClaimTxHash:      common.HexToHash("0x2222"),
		Status:           ethtxtypes.MonitoredTxStatusSent,
		StatusReason:     "submitted",
		RetryCount:       2,
		MaxRetries:       4,
		SentAt:           &sentAt,
		LastObservedAt:   &sentAt,
		CreatedAt:        sentAt,
		UpdatedAt:        sentAt,
		LastError:        "previous underpriced",
		TransactionData:  []byte{0xca, 0xfe},
		TargetBridgeAddr: common.HexToAddress("0x5000000000000000000000000000000000000005"),
	}

	require.NoError(t, storage.RecordTransactionAttempt(ctx, request.Key, attempt))

	stored, err := storage.GetRequest(ctx, request.Key)
	require.NoError(t, err)
	require.Equal(t, attempt.ClaimTxHash, *stored.ClaimTxHash)
	require.Equal(t, attempt.TxManagerID, *stored.TxManagerID)
	require.Equal(t, attempt.RetryCount, stored.RetryCount)
	require.Equal(t, attempt.LastError, stored.LastError)
	require.True(t, stored.UpdatedAt.After(before))

	var count int
	err = database.QueryRow(
		"SELECT COUNT(*) FROM autoclaim_transaction_attempt WHERE request_key = ? AND attempt_number = ?",
		request.Key,
		attempt.AttemptNumber,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	later := sentAt.Add(time.Minute)
	proof := autoclaimtypes.ClaimProof{L1InfoTreeIndex: 7, PreparedAt: later}
	require.NoError(t, storage.SaveProof(ctx, request.Key, proof))
	stored, err = storage.GetRequest(ctx, request.Key)
	require.NoError(t, err)
	require.NotNil(t, stored.Proof)
	require.Equal(t, uint32(7), stored.Proof.L1InfoTreeIndex)
	require.True(t, stored.UpdatedAt.After(attempt.UpdatedAt))

	require.NoError(t, storage.UpdateLastError(ctx, request.Key, "new error", later.Add(time.Minute)))
	stored, err = storage.GetRequest(ctx, request.Key)
	require.NoError(t, err)
	require.Equal(t, "new error", stored.LastError)
	require.Equal(t, later.Add(time.Minute), stored.UpdatedAt)
}

func TestGetRequestTreatsJSONNullOptionalFieldsAsNil(t *testing.T) {
	storage, database := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	request := makeRequest(11, 1, autoclaimtypes.RequestStatusQueued)
	stored, inserted, err := storage.EnqueueRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, inserted)
	require.Nil(t, stored.Proof)
	require.Nil(t, stored.PolicyDecision)
	require.Nil(t, stored.ManualDecision)

	_, err = database.ExecContext(ctx, `
		UPDATE autoclaim_request
		SET proof_json = 'null',
			policy_decision_json = 'null',
			manual_decision_json = 'null'
		WHERE request_key = ?`,
		request.Key,
	)
	require.NoError(t, err)

	stored, err = storage.GetRequest(ctx, request.Key)
	require.NoError(t, err)
	require.Nil(t, stored.Proof)
	require.Nil(t, stored.PolicyDecision)
	require.Nil(t, stored.ManualDecision)
}

func TestListRecoverableRequests(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	for depositCount, status := range map[uint32]autoclaimtypes.RequestStatus{
		1: autoclaimtypes.RequestStatusQueued,
		2: autoclaimtypes.RequestStatusSending,
		3: autoclaimtypes.RequestStatusSent,
		4: autoclaimtypes.RequestStatusConfirmed,
		5: autoclaimtypes.RequestStatusFailed,
	} {
		enqueueRequest(t, ctx, storage, makeRequest(depositCount, 10, status))
	}
	enqueueRequest(t, ctx, storage, makeRequest(6, 11, autoclaimtypes.RequestStatusQueued))

	destinationNetwork := uint32(10)
	page, err := storage.ListRecoverableRequests(ctx, autoclaimtypes.RecoveryFilter{
		DestinationNetwork: &destinationNetwork,
		PageSize:           10,
	})
	require.NoError(t, err)
	require.Equal(t, 3, page.Count)
	require.Len(t, page.Requests, 3)
	for _, request := range page.Requests {
		require.Equal(t, destinationNetwork, request.Bridge.DestinationNetwork)
		require.Contains(t, []autoclaimtypes.RequestStatus{
			autoclaimtypes.RequestStatusQueued,
			autoclaimtypes.RequestStatusSending,
			autoclaimtypes.RequestStatusSent,
		}, request.Status)
	}

	page, err = storage.ListRecoverableRequests(ctx, autoclaimtypes.RecoveryFilter{
		DestinationNetwork: &destinationNetwork,
		Statuses:           []autoclaimtypes.RequestStatus{autoclaimtypes.RequestStatusSent},
		PageSize:           10,
	})
	require.NoError(t, err)
	require.Equal(t, 1, page.Count)
	require.Equal(t, autoclaimtypes.RequestStatusSent, page.Requests[0].Status)
}

func TestMissingRequestReturnsNotFound(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()

	_, err := storage.GetRequest(context.Background(), autoclaimtypes.RequestKey("missing"))
	require.True(t, errors.Is(err, db.ErrNotFound))
}
