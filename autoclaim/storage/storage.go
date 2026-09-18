package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/agglayer/aggkit/autoclaim/storage/migrations"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

var (
	// ErrInvalidTransition is returned when a stored request cannot move from one status to another.
	ErrInvalidTransition = errors.New("invalid autoclaim request transition")
	// ErrPreconditionFailed is returned when an atomic update precondition does not match the stored row.
	ErrPreconditionFailed = errors.New("autoclaim request precondition failed")
)

const (
	recoveryFilterClauseCapacity = 2
	requestFilterClauseCapacity  = 9
	base10                       = 10
)

var _ autoclaimtypes.Storage = (*Storage)(nil)

// Storage persists Auto Claim requests and transaction attempts in SQLite.
type Storage struct {
	database       *sql.DB
	log            aggkitcommon.Logger
	dbQueryTimeout time.Duration
}

type requestRow struct {
	RequestKey           string         `meddler:"request_key"`
	SourceNetwork        uint32         `meddler:"source_network"`
	OriginNetwork        uint32         `meddler:"origin_network"`
	DestinationNetwork   uint32         `meddler:"destination_network"`
	DepositCount         uint32         `meddler:"deposit_count"`
	Status               string         `meddler:"status"`
	PolicyResult         sql.NullString `meddler:"policy_result"`
	BridgeTxHash         string         `meddler:"bridge_tx_hash"`
	ClaimTxHash          sql.NullString `meddler:"claim_tx_hash"`
	TxManagerID          sql.NullString `meddler:"tx_manager_id"`
	BlockNum             uint64         `meddler:"block_num"`
	BlockPos             uint64         `meddler:"block_pos"`
	GlobalIndex          sql.NullString `meddler:"global_index"`
	L1InfoTreeIndex      sql.NullInt64  `meddler:"l1_info_tree_index"`
	RetryCount           uint64         `meddler:"retry_count"`
	MaxRetries           uint64         `meddler:"max_retries"`
	LastObservedSendAt   sql.NullTime   `meddler:"last_observed_send_at"`
	LastObservedResultAt sql.NullTime   `meddler:"last_observed_result_at"`
	CreatedAt            time.Time      `meddler:"created_at"`
	UpdatedAt            time.Time      `meddler:"updated_at"`
	LastError            string         `meddler:"last_error"`
	BridgeJSON           []byte         `meddler:"bridge_json"`
	ProofJSON            []byte         `meddler:"proof_json"`
	PolicyDecisionJSON   []byte         `meddler:"policy_decision_json"`
	ManualDecisionJSON   []byte         `meddler:"manual_decision_json"`
	LER                  sql.NullString `meddler:"ler"`
	VerifyBlockNum       uint64         `meddler:"verify_block_num"`
}

type bridgeCursorRow struct {
	Name      string    `meddler:"cursor_name"`
	FromBlock uint64    `meddler:"from_block"`
	ToBlock   uint64    `meddler:"to_block"`
	BlockNum  uint64    `meddler:"block_num"`
	BlockPos  uint64    `meddler:"block_pos"`
	UpdatedAt time.Time `meddler:"updated_at"`
}

type lerCursorRow struct {
	SourceNetwork      uint32    `meddler:"source_network"`
	DestinationNetwork uint32    `meddler:"destination_network"`
	LastLER            string    `meddler:"last_ler"`
	LastVerifyBlockNum uint64    `meddler:"last_verify_block_num"`
	UpdatedAt          time.Time `meddler:"updated_at"`
}

// NewStandalone opens a SQLite database, runs Auto Claim migrations, and returns storage.
func NewStandalone(
	logger aggkitcommon.Logger,
	dbPath string,
	dbQueryTimeout time.Duration,
) (*Storage, error) {
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("autoclaim storage: open sqlite DB at %s: %w", dbPath, err)
	}

	if err := migrations.RunMigrations(logger, database); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("autoclaim storage: run migrations: %w", err)
	}

	return New(logger, database, dbQueryTimeout), nil
}

// New creates storage with an already-open migrated database.
func New(logger aggkitcommon.Logger, database *sql.DB, dbQueryTimeout time.Duration) *Storage {
	return &Storage{
		database:       database,
		log:            logger,
		dbQueryTimeout: dbQueryTimeout,
	}
}

// Close closes the underlying database connection.
func (s *Storage) Close() error {
	return s.database.Close()
}

// GetBridgeCursor returns the durable bridge-discovery cursor by name.
func (s *Storage) GetBridgeCursor(
	ctx context.Context,
	name string,
) (*autoclaimtypes.BridgeCursor, bool, error) {
	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	rows, err := s.database.QueryContext(dbCtx, `
		SELECT cursor_name, from_block, to_block, block_num, block_pos, updated_at
		FROM autoclaim_bridge_cursor
		WHERE cursor_name = ?`,
		name,
	)
	if err != nil {
		return nil, false, fmt.Errorf("get autoclaim bridge cursor %s: %w", name, err)
	}

	row := &bridgeCursorRow{}
	if err := meddler.ScanRow(rows, row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get autoclaim bridge cursor %s: %w", name, err)
	}

	cursor := autoclaimtypes.BridgeCursor{
		FromBlock: row.FromBlock,
		ToBlock:   row.ToBlock,
		BlockNum:  row.BlockNum,
		BlockPos:  row.BlockPos,
	}

	return &cursor, true, nil
}

// SaveBridgeCursor upserts the durable bridge-discovery cursor.
func (s *Storage) SaveBridgeCursor(
	ctx context.Context,
	name string,
	cursor autoclaimtypes.BridgeCursor,
	now time.Time,
) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	result, err := s.database.ExecContext(dbCtx, `
		INSERT INTO autoclaim_bridge_cursor (
			cursor_name,
			from_block,
			to_block,
			block_num,
			block_pos,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(cursor_name) DO UPDATE SET
			from_block = excluded.from_block,
			to_block = excluded.to_block,
			block_num = excluded.block_num,
			block_pos = excluded.block_pos,
			updated_at = excluded.updated_at`,
		name,
		cursor.FromBlock,
		cursor.ToBlock,
		cursor.BlockNum,
		cursor.BlockPos,
		now,
	)
	if err != nil {
		return fmt.Errorf("save autoclaim bridge cursor %s: %w", name, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("save autoclaim bridge cursor %s rows affected: %w", name, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("save autoclaim bridge cursor %s: no rows affected", name)
	}

	return nil
}

// GetLERCursor returns the durable local-exit-root discovery cursor for a (source, destination) pair.
func (s *Storage) GetLERCursor(
	ctx context.Context,
	sourceNetwork, destinationNetwork uint32,
) (*autoclaimtypes.LERCursor, bool, error) {
	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	rows, err := s.database.QueryContext(dbCtx, `
		SELECT source_network, destination_network, last_ler, last_verify_block_num, updated_at
		FROM autoclaim_ler_cursor
		WHERE source_network = ? AND destination_network = ?`,
		sourceNetwork, destinationNetwork,
	)
	if err != nil {
		return nil, false, fmt.Errorf(
			"get autoclaim ler cursor %d->%d: %w", sourceNetwork, destinationNetwork, err)
	}

	row := &lerCursorRow{}
	if err := meddler.ScanRow(rows, row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf(
			"get autoclaim ler cursor %d->%d: %w", sourceNetwork, destinationNetwork, err)
	}

	cursor := autoclaimtypes.LERCursor{
		SourceNetwork:      row.SourceNetwork,
		DestinationNetwork: row.DestinationNetwork,
		LastLER:            common.HexToHash(row.LastLER),
		LastVerifyBlockNum: row.LastVerifyBlockNum,
	}

	return &cursor, true, nil
}

// SaveLERCursor upserts the durable local-exit-root discovery cursor for a (source, destination) pair.
func (s *Storage) SaveLERCursor(
	ctx context.Context,
	sourceNetwork, destinationNetwork uint32,
	cursor autoclaimtypes.LERCursor,
	now time.Time,
) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	result, err := s.database.ExecContext(dbCtx, `
		INSERT INTO autoclaim_ler_cursor (
			source_network,
			destination_network,
			last_ler,
			last_verify_block_num,
			updated_at
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(source_network, destination_network) DO UPDATE SET
			last_ler = excluded.last_ler,
			last_verify_block_num = excluded.last_verify_block_num,
			updated_at = excluded.updated_at`,
		sourceNetwork,
		destinationNetwork,
		cursor.LastLER.Hex(),
		cursor.LastVerifyBlockNum,
		now,
	)
	if err != nil {
		return fmt.Errorf("save autoclaim ler cursor %d->%d: %w", sourceNetwork, destinationNetwork, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"save autoclaim ler cursor %d->%d rows affected: %w", sourceNetwork, destinationNetwork, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("save autoclaim ler cursor %d->%d: no rows affected", sourceNetwork, destinationNetwork)
	}

	return nil
}

// SeedLERCursorsFromLegacy fans a pre-autoclaim0003 per-source LER cursor, parked in
// autoclaim_ler_cursor_legacy by the autoclaim0003 migration, out to the given destination networks.
// It is transactional: if no legacy row exists for sourceNetwork, it commits a no-op and returns
// (false, nil). Otherwise it inserts one autoclaim_ler_cursor row per destination (ON CONFLICT DO
// NOTHING, so a pair already seeded or advanced by a concurrent/earlier attempt is never rolled
// backwards), deletes the legacy row, and returns (true, nil). The delete happening in the same
// transaction as the inserts is what makes the seed idempotent: a crash before commit leaves the legacy
// row intact and the whole seed is retried; a crash after commit finds no legacy row and returns
// (false, nil) on the next attempt.
func (s *Storage) SeedLERCursorsFromLegacy(
	ctx context.Context,
	sourceNetwork uint32,
	destinationNetworks []uint32,
	now time.Time,
) (seeded bool, err error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	// An empty destination set must never consume the legacy row: today processSource
	// (l2_to_lx.go) never calls this with no destinations for sourceNetwork, but this is an
	// exported method with no other guard against it, and doing so here would delete the source's
	// only resumption point while seeding nothing to replace it -- an unrecoverable data loss with
	// no caller-visible signal. Treat it the same as "nothing to seed" rather than opening a
	// transaction at all.
	if len(destinationNetworks) == 0 {
		return false, nil
	}

	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	// Cheap, lock-free existence check before ever opening a write transaction. db.NewSQLiteDB sets
	// _txlock=immediate, so every BeginTx below issues BEGIN IMMEDIATE and takes a write (RESERVED)
	// lock -- including in the overwhelmingly common case (a fresh deployment, or any source that
	// has already been seeded/never had a legacy row) where the transaction would only run one
	// SELECT that finds nothing and commits a no-op. This is called once per source on every poll
	// (see l2_to_lx.go's processSource), so avoiding that write-lock acquisition in the common case
	// matters: it stops this from contending, every poll forever, with the write lock the claim
	// pipeline itself needs on the same database.
	//
	// This does not weaken correctness under concurrency: if a legacy row exists here but is gone by
	// the time the transaction below re-reads it, that re-read (already required, for atomicity with
	// the delete) returns sql.ErrNoRows and the transaction commits as a no-op -- exactly the result
	// this pre-check would have produced had it observed the row's absence a moment earlier. The
	// converse (no row here, but one appears before the transaction starts) cannot cause a double
	// seed or a lost row either: the write path is unchanged and still transactional, and its INSERTs
	// are ON CONFLICT DO NOTHING against the same row the pre-check would have raced with anyway.
	var probeExists int
	switch probeErr := s.database.QueryRowContext(dbCtx,
		"SELECT 1 FROM autoclaim_ler_cursor_legacy WHERE source_network = ? LIMIT 1", sourceNetwork,
	).Scan(&probeExists); {
	case errors.Is(probeErr, sql.ErrNoRows):
		return false, nil
	case probeErr != nil:
		return false, fmt.Errorf(
			"seed autoclaim ler cursors from legacy for source %d: probe legacy row: %w",
			sourceNetwork, probeErr)
	}

	tx, err := s.database.BeginTx(dbCtx, nil)
	if err != nil {
		return false, fmt.Errorf("seed autoclaim ler cursors from legacy for source %d: begin tx: %w",
			sourceNetwork, err)
	}
	committed := false
	defer func() {
		if !committed {
			rollbackErr := tx.Rollback()
			if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = errors.Join(
					err,
					fmt.Errorf("seed autoclaim ler cursors from legacy for source %d: rollback: %w",
						sourceNetwork, rollbackErr),
				)
			}
		}
	}()

	var legacyLER string
	var legacyVerifyBlockNum uint64
	row := tx.QueryRowContext(dbCtx, `
		SELECT last_ler, last_verify_block_num
		FROM autoclaim_ler_cursor_legacy
		WHERE source_network = ?`,
		sourceNetwork,
	)
	if err := row.Scan(&legacyLER, &legacyVerifyBlockNum); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if commitErr := tx.Commit(); commitErr != nil {
				return false, fmt.Errorf("seed autoclaim ler cursors from legacy for source %d: commit: %w",
					sourceNetwork, commitErr)
			}
			committed = true
			return false, nil
		}
		return false, fmt.Errorf("seed autoclaim ler cursors from legacy for source %d: read legacy row: %w",
			sourceNetwork, err)
	}

	for _, destinationNetwork := range destinationNetworks {
		if _, err := tx.ExecContext(dbCtx, `
			INSERT INTO autoclaim_ler_cursor (
				source_network,
				destination_network,
				last_ler,
				last_verify_block_num,
				updated_at
			) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(source_network, destination_network) DO NOTHING`,
			sourceNetwork, destinationNetwork, legacyLER, legacyVerifyBlockNum, now,
		); err != nil {
			return false, fmt.Errorf(
				"seed autoclaim ler cursors from legacy for source %d->%d: %w",
				sourceNetwork, destinationNetwork, err)
		}
	}

	if _, err := tx.ExecContext(dbCtx,
		"DELETE FROM autoclaim_ler_cursor_legacy WHERE source_network = ?", sourceNetwork,
	); err != nil {
		return false, fmt.Errorf("seed autoclaim ler cursors from legacy for source %d: delete legacy row: %w",
			sourceNetwork, err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("seed autoclaim ler cursors from legacy for source %d: commit: %w",
			sourceNetwork, err)
	}
	committed = true

	// Visible-in-logs record of exactly which destinations inherited the legacy cursor: a claimer
	// added in the same restart as the autoclaim0003 upgrade is indistinguishable, at seed time,
	// from one that was already claiming from this source before the upgrade (see docs/autoclaim.md
	// "Legacy upgrade seed") -- it silently will not backfill anything before this point. This log
	// line is the only place that fact becomes diagnosable after the fact.
	s.log.Warnf(
		"seeded autoclaim LER cursor for source %d from legacy cursor (ler=%s, verify_block=%d) "+
			"to destinations %v; any of these added in this same restart will not backfill "+
			"history before this point (see docs/autoclaim.md 'Legacy upgrade seed')",
		sourceNetwork, legacyLER, legacyVerifyBlockNum, destinationNetworks,
	)

	return true, nil
}

// EnqueueRequest inserts a request once per origin, destination, and deposit count.
func (s *Storage) EnqueueRequest(
	ctx context.Context,
	request autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.AutoClaimRequest, bool, error) {
	if request.Key == "" {
		request.Key = autoclaimtypes.DeriveRequestKey(
			request.Bridge.SourceNetwork,
			request.Bridge.DestinationNetwork,
			request.Bridge.DepositCount,
		)
	}
	if request.Status == "" {
		request.Status = autoclaimtypes.RequestStatusDetected
	}
	now := time.Now().UTC()
	if request.CreatedAt.IsZero() {
		request.CreatedAt = now
	}
	if request.UpdatedAt.IsZero() {
		request.UpdatedAt = request.CreatedAt
	}
	if request.GlobalIndex == nil {
		request.GlobalIndex = autoclaimtypes.DeriveGlobalIndexForSource(
			request.Bridge.SourceNetwork,
			request.Bridge.DepositCount,
		)
	}

	row, err := makeRequestRow(request)
	if err != nil {
		return nil, false, err
	}

	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	result, err := s.database.ExecContext(dbCtx, `
		INSERT OR IGNORE INTO autoclaim_request (
			request_key,
			source_network,
			origin_network,
			destination_network,
			deposit_count,
			status,
			policy_result,
			bridge_tx_hash,
			claim_tx_hash,
			tx_manager_id,
			block_num,
			block_pos,
			global_index,
			l1_info_tree_index,
			retry_count,
			max_retries,
			last_observed_send_at,
			last_observed_result_at,
			created_at,
			updated_at,
			last_error,
			bridge_json,
			proof_json,
			policy_decision_json,
			manual_decision_json,
			ler,
			verify_block_num
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.RequestKey,
		row.SourceNetwork,
		row.OriginNetwork,
		row.DestinationNetwork,
		row.DepositCount,
		row.Status,
		row.PolicyResult,
		row.BridgeTxHash,
		row.ClaimTxHash,
		row.TxManagerID,
		row.BlockNum,
		row.BlockPos,
		row.GlobalIndex,
		row.L1InfoTreeIndex,
		row.RetryCount,
		row.MaxRetries,
		row.LastObservedSendAt,
		row.LastObservedResultAt,
		row.CreatedAt,
		row.UpdatedAt,
		row.LastError,
		row.BridgeJSON,
		nullBytes(row.ProofJSON),
		nullBytes(row.PolicyDecisionJSON),
		nullBytes(row.ManualDecisionJSON),
		row.LER,
		row.VerifyBlockNum,
	)
	if err != nil {
		return nil, false, fmt.Errorf("enqueue autoclaim request %s: %w", request.Key, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, false, fmt.Errorf("enqueue autoclaim request %s rows affected: %w", request.Key, err)
	}

	stored, err := s.GetRequest(ctx, request.Key)
	if err != nil {
		return nil, false, err
	}

	return stored, rowsAffected == 1, nil
}

// GetRequest returns a stored request by key.
func (s *Storage) GetRequest(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
) (*autoclaimtypes.AutoClaimRequest, error) {
	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	rows, err := s.database.QueryContext(dbCtx, selectRequestSQL()+" WHERE request_key = ?", key)
	if err != nil {
		return nil, fmt.Errorf("get autoclaim request %s: %w", key, err)
	}

	row := &requestRow{}
	if err := meddler.ScanRow(rows, row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("get autoclaim request %s: %w", key, db.ErrNotFound)
		}
		return nil, fmt.Errorf("get autoclaim request %s: %w", key, err)
	}

	request, err := row.toRequest()
	if err != nil {
		return nil, fmt.Errorf("get autoclaim request %s: %w", key, err)
	}

	return request, nil
}

// ListRequests returns a filtered, paginated request list ordered by newest bridge block first.
func (s *Storage) ListRequests(
	ctx context.Context,
	filter autoclaimtypes.RequestFilter,
) (*autoclaimtypes.RequestPage, error) {
	where, args := buildRequestWhereClause(filter)
	return s.listRequests(ctx, where, args, filter.PageNumber, filter.PageSize)
}

// ListRecoverableRequests returns requests a claimer should re-check after process restart.
func (s *Storage) ListRecoverableRequests(
	ctx context.Context,
	filter autoclaimtypes.RecoveryFilter,
) (*autoclaimtypes.RequestPage, error) {
	statuses := filter.Statuses
	if len(statuses) == 0 {
		statuses = []autoclaimtypes.RequestStatus{
			autoclaimtypes.RequestStatusQueued,
			autoclaimtypes.RequestStatusSending,
			autoclaimtypes.RequestStatusSent,
		}
	}

	clauses := make([]string, 0, recoveryFilterClauseCapacity)
	args := make([]any, 0, len(statuses)+1)
	if filter.DestinationNetwork != nil {
		clauses = append(clauses, "destination_network = ?")
		args = append(args, *filter.DestinationNetwork)
	}

	placeholders := make([]string, 0, len(statuses))
	for _, status := range statuses {
		placeholders = append(placeholders, "?")
		args = append(args, status.String())
	}
	clauses = append(clauses, fmt.Sprintf("status IN (%s)", strings.Join(placeholders, ", ")))

	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}

	return s.listRequests(ctx, where, args, filter.PageNumber, filter.PageSize)
}

// RecordPolicyDecision persists the automatic policy decision for a request.
func (s *Storage) RecordPolicyDecision(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	decision autoclaimtypes.PolicyDecision,
) error {
	return s.recordDecision(ctx, key, decision, "policy_decision_json")
}

// RecordManualDecision persists an operator decision for a request.
func (s *Storage) RecordManualDecision(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	decision autoclaimtypes.PolicyDecision,
) error {
	return s.recordDecision(ctx, key, decision, "manual_decision_json")
}

// ApproveManualRequest atomically records an approval and releases a manually gated request.
func (s *Storage) ApproveManualRequest(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	decision autoclaimtypes.PolicyDecision,
	now time.Time,
) (*autoclaimtypes.AutoClaimRequest, error) {
	decision.Result = autoclaimtypes.PolicyResultApproved
	return s.transitionManualDecision(
		ctx,
		key,
		decision,
		autoclaimtypes.RequestStatusPolicyApproved,
		now,
	)
}

// RejectManualRequest atomically records a rejection and stops a manually gated request.
func (s *Storage) RejectManualRequest(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	decision autoclaimtypes.PolicyDecision,
	now time.Time,
) (*autoclaimtypes.AutoClaimRequest, error) {
	decision.Result = autoclaimtypes.PolicyResultRejected
	return s.transitionManualDecision(
		ctx,
		key,
		decision,
		autoclaimtypes.RequestStatusPolicyRejected,
		now,
	)
}

// SaveProof stores the claim proof selected for a request.
func (s *Storage) SaveProof(ctx context.Context, key autoclaimtypes.RequestKey, proof autoclaimtypes.ClaimProof) error {
	proofJSON, err := json.Marshal(proof)
	if err != nil {
		return fmt.Errorf("marshal proof for autoclaim request %s: %w", key, err)
	}
	now := time.Now().UTC()

	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	result, err := s.database.ExecContext(dbCtx, `
		UPDATE autoclaim_request
		SET proof_json = ?, l1_info_tree_index = ?, updated_at = ?
		WHERE request_key = ?`,
		proofJSON, proof.L1InfoTreeIndex, now, key,
	)
	if err != nil {
		return fmt.Errorf("save proof for autoclaim request %s: %w", key, err)
	}

	return requireUpdated(result, "save proof", key)
}

// RecordTransactionAttempt stores one transaction attempt and updates request retry/hash metadata.
func (s *Storage) RecordTransactionAttempt(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	attempt autoclaimtypes.TransactionAttempt,
) (err error) {
	attempt.RequestKey = key
	now := time.Now().UTC()
	if attempt.CreatedAt.IsZero() {
		attempt.CreatedAt = now
	}
	if attempt.UpdatedAt.IsZero() {
		attempt.UpdatedAt = now
	}

	attemptJSON, err := json.Marshal(attempt)
	if err != nil {
		return fmt.Errorf("marshal transaction attempt for autoclaim request %s: %w", key, err)
	}

	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	tx, err := s.database.BeginTx(dbCtx, nil)
	if err != nil {
		return fmt.Errorf("record transaction attempt for autoclaim request %s: begin tx: %w", key, err)
	}
	committed := false
	defer func() {
		if !committed {
			rollbackErr := tx.Rollback()
			if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = errors.Join(
					err,
					fmt.Errorf("record transaction attempt for autoclaim request %s: rollback: %w", key, rollbackErr),
				)
			}
		}
	}()

	result, err := tx.ExecContext(dbCtx, `
		INSERT INTO autoclaim_transaction_attempt (
			request_key,
			attempt_number,
			claimer_id,
			tx_manager_id,
			claim_tx_hash,
			status,
			status_reason,
			retry_count,
			max_retries,
			sent_at,
			confirmed_at,
			last_observed_at,
			created_at,
			updated_at,
			last_error,
			transaction_data,
			target_bridge_addr,
			attempt_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(request_key, attempt_number) DO UPDATE SET
			claimer_id = excluded.claimer_id,
			tx_manager_id = excluded.tx_manager_id,
			claim_tx_hash = excluded.claim_tx_hash,
			status = excluded.status,
			status_reason = excluded.status_reason,
			retry_count = excluded.retry_count,
			max_retries = excluded.max_retries,
			sent_at = excluded.sent_at,
			confirmed_at = excluded.confirmed_at,
			last_observed_at = excluded.last_observed_at,
			updated_at = excluded.updated_at,
			last_error = excluded.last_error,
			transaction_data = excluded.transaction_data,
			target_bridge_addr = excluded.target_bridge_addr,
			attempt_json = excluded.attempt_json`,
		key,
		attempt.AttemptNumber,
		attempt.ClaimerID,
		attempt.TxManagerID.Hex(),
		attempt.ClaimTxHash.Hex(),
		attempt.Status.String(),
		attempt.StatusReason,
		attempt.RetryCount,
		attempt.MaxRetries,
		nullTime(attempt.SentAt),
		nullTime(attempt.ConfirmedAt),
		nullTime(attempt.LastObservedAt),
		attempt.CreatedAt,
		attempt.UpdatedAt,
		attempt.LastError,
		attempt.TransactionData,
		attempt.TargetBridgeAddr.Hex(),
		attemptJSON,
	)
	if err != nil {
		return fmt.Errorf("record transaction attempt for autoclaim request %s: insert attempt: %w", key, err)
	}
	if err := requireUpdated(result, "record transaction attempt", key); err != nil {
		return err
	}

	result, err = tx.ExecContext(dbCtx, `
		UPDATE autoclaim_request
		SET claim_tx_hash = ?,
			tx_manager_id = ?,
			retry_count = ?,
			max_retries = ?,
			last_observed_send_at = COALESCE(?, last_observed_send_at),
			last_observed_result_at = COALESCE(?, last_observed_result_at),
			last_error = ?,
			updated_at = ?
		WHERE request_key = ?`,
		attempt.ClaimTxHash.Hex(),
		attempt.TxManagerID.Hex(),
		attempt.RetryCount,
		attempt.MaxRetries,
		nullTime(attempt.SentAt),
		nullTime(attempt.LastObservedAt),
		attempt.LastError,
		attempt.UpdatedAt,
		key,
	)
	if err != nil {
		return fmt.Errorf("record transaction attempt for autoclaim request %s: update request: %w", key, err)
	}
	if err := requireUpdated(result, "record transaction attempt request update", key); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("record transaction attempt for autoclaim request %s: commit: %w", key, err)
	}
	committed = true

	return nil
}

// TransitionRequest atomically changes request state when both status preconditions are satisfied.
func (s *Storage) TransitionRequest(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	from autoclaimtypes.RequestStatus,
	to autoclaimtypes.RequestStatus,
	now time.Time,
) (*autoclaimtypes.AutoClaimRequest, error) {
	if !from.CanTransitionTo(to) {
		return nil, fmt.Errorf("%w: %s to %s", ErrInvalidTransition, from, to)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	result, err := s.database.ExecContext(dbCtx, `
		UPDATE autoclaim_request
		SET status = ?, updated_at = ?
		WHERE request_key = ? AND status = ?`,
		to.String(), now, key, from.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("transition autoclaim request %s from %s to %s: %w", key, from, to, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("transition autoclaim request %s rows affected: %w", key, err)
	}
	if rowsAffected == 0 {
		return nil, fmt.Errorf("%w: %s expected %s", ErrPreconditionFailed, key, from)
	}

	return s.GetRequest(ctx, key)
}

// UpdateLastError records the latest request-level error.
func (s *Storage) UpdateLastError(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	lastError string,
	now time.Time,
) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	result, err := s.database.ExecContext(dbCtx, `
		UPDATE autoclaim_request
		SET last_error = ?, updated_at = ?
		WHERE request_key = ?`,
		lastError, now, key,
	)
	if err != nil {
		return fmt.Errorf("update last error for autoclaim request %s: %w", key, err)
	}

	return requireUpdated(result, "update last error", key)
}

func (s *Storage) recordDecision(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	decision autoclaimtypes.PolicyDecision,
	column string,
) error {
	if decision.CreatedAt.IsZero() {
		decision.CreatedAt = time.Now().UTC()
	}
	if decision.UpdatedAt.IsZero() {
		decision.UpdatedAt = decision.CreatedAt
	}

	decisionJSON, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("marshal decision for autoclaim request %s: %w", key, err)
	}
	now := time.Now().UTC()

	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	var query string
	switch column {
	case "policy_decision_json":
		query = `
			UPDATE autoclaim_request
			SET policy_decision_json = ?, policy_result = ?, updated_at = ?
			WHERE request_key = ?`
	case "manual_decision_json":
		query = `
			UPDATE autoclaim_request
			SET manual_decision_json = ?, policy_result = ?, updated_at = ?
			WHERE request_key = ?`
	default:
		return fmt.Errorf("record decision for autoclaim request %s: unsupported decision column %s", key, column)
	}

	result, err := s.database.ExecContext(dbCtx, query, decisionJSON, decision.Result.String(), now, key)
	if err != nil {
		return fmt.Errorf("record decision for autoclaim request %s: %w", key, err)
	}

	return requireUpdated(result, "record decision", key)
}

func (s *Storage) transitionManualDecision(
	ctx context.Context,
	key autoclaimtypes.RequestKey,
	decision autoclaimtypes.PolicyDecision,
	to autoclaimtypes.RequestStatus,
	now time.Time,
) (*autoclaimtypes.AutoClaimRequest, error) {
	from := autoclaimtypes.RequestStatusManualApprovalRequired
	if !from.CanTransitionTo(to) {
		return nil, fmt.Errorf("%w: %s to %s", ErrInvalidTransition, from, to)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if decision.CreatedAt.IsZero() {
		decision.CreatedAt = now
	}
	decision.UpdatedAt = now

	decisionJSON, err := json.Marshal(decision)
	if err != nil {
		return nil, fmt.Errorf("marshal manual decision for autoclaim request %s: %w", key, err)
	}

	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	result, err := s.database.ExecContext(dbCtx, `
		UPDATE autoclaim_request
		SET manual_decision_json = ?,
			policy_result = ?,
			status = ?,
			updated_at = ?
		WHERE request_key = ? AND status = ?`,
		decisionJSON,
		decision.Result.String(),
		to.String(),
		now,
		key,
		from.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("manual transition autoclaim request %s from %s to %s: %w", key, from, to, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("manual transition autoclaim request %s rows affected: %w", key, err)
	}
	if rowsAffected == 0 {
		return nil, fmt.Errorf("%w: %s expected %s", ErrPreconditionFailed, key, from)
	}

	return s.GetRequest(ctx, key)
}

func (s *Storage) listRequests(
	ctx context.Context,
	where string,
	args []any,
	pageNumber uint32,
	pageSize uint32,
) (*autoclaimtypes.RequestPage, error) {
	pageSize, err := validatePageSize(pageSize)
	if err != nil {
		return nil, err
	}
	offset := int64(pageNumber) * int64(pageSize)

	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	var count int
	countArgs := append([]any(nil), args...)
	// The WHERE fragment is built only by the helpers below and must contain placeholders only.
	countQuery := "SELECT COUNT(*) FROM autoclaim_request" + where
	if err := s.database.QueryRowContext(dbCtx, countQuery, countArgs...).Scan(&count); err != nil {
		return nil, fmt.Errorf("count autoclaim requests: %w", err)
	}
	if count == 0 {
		return &autoclaimtypes.RequestPage{Requests: []*autoclaimtypes.AutoClaimRequest{}, Count: 0}, nil
	}

	// The WHERE fragment is built only by the helpers below and must contain placeholders only.
	query := selectRequestSQL() + where + " ORDER BY block_num DESC, block_pos DESC, created_at DESC LIMIT ? OFFSET ?"
	queryArgs := append(append([]any(nil), args...), pageSize, offset)
	rows, err := s.database.QueryContext(dbCtx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("list autoclaim requests: %w", err)
	}

	var requestRows []*requestRow
	if err := meddler.ScanAll(rows, &requestRows); err != nil {
		return nil, fmt.Errorf("scan autoclaim request rows: %w", err)
	}

	requests := make([]*autoclaimtypes.AutoClaimRequest, 0, len(requestRows))
	for _, row := range requestRows {
		request, err := row.toRequest()
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}

	return &autoclaimtypes.RequestPage{Requests: requests, Count: count}, nil
}

func validatePageSize(pageSize uint32) (uint32, error) {
	if pageSize == 0 {
		return autoclaimtypes.DefaultRequestPageSize, nil
	}
	if pageSize > autoclaimtypes.MaxRequestPageSize {
		return 0, fmt.Errorf("autoclaim request page size %d exceeds maximum %d",
			pageSize, autoclaimtypes.MaxRequestPageSize)
	}
	return pageSize, nil
}

func buildRequestWhereClause(filter autoclaimtypes.RequestFilter) (string, []any) {
	clauses := make([]string, 0, requestFilterClauseCapacity)
	args := make([]any, 0, requestFilterClauseCapacity)

	if filter.SourceNetwork != nil {
		clauses = append(clauses, "source_network = ?")
		args = append(args, *filter.SourceNetwork)
	}
	if filter.OriginNetwork != nil {
		clauses = append(clauses, "origin_network = ?")
		args = append(args, *filter.OriginNetwork)
	}
	if filter.DestinationNetwork != nil {
		clauses = append(clauses, "destination_network = ?")
		args = append(args, *filter.DestinationNetwork)
	}
	if filter.Status != nil {
		clauses = append(clauses, "status = ?")
		args = append(args, filter.Status.String())
	}
	if filter.PolicyResult != nil {
		clauses = append(clauses, "policy_result = ?")
		args = append(args, filter.PolicyResult.String())
	}
	if filter.BridgeTxHash != nil {
		clauses = append(clauses, "bridge_tx_hash = ?")
		args = append(args, filter.BridgeTxHash.Hex())
	}
	if filter.ClaimTxHash != nil {
		clauses = append(clauses, "claim_tx_hash = ?")
		args = append(args, filter.ClaimTxHash.Hex())
	}
	if filter.FromBlock != nil {
		clauses = append(clauses, "block_num >= ?")
		args = append(args, *filter.FromBlock)
	}
	if filter.ToBlock != nil {
		clauses = append(clauses, "block_num <= ?")
		args = append(args, *filter.ToBlock)
	}

	if len(clauses) == 0 {
		return "", args
	}

	return " WHERE " + strings.Join(clauses, " AND "), args
}

func makeRequestRow(request autoclaimtypes.AutoClaimRequest) (*requestRow, error) {
	bridgeJSON, err := json.Marshal(request.Bridge)
	if err != nil {
		return nil, fmt.Errorf("marshal bridge for autoclaim request %s: %w", request.Key, err)
	}

	proofJSON, err := marshalOptional(request.Proof)
	if err != nil {
		return nil, fmt.Errorf("marshal proof for autoclaim request %s: %w", request.Key, err)
	}
	policyDecisionJSON, err := marshalOptional(request.PolicyDecision)
	if err != nil {
		return nil, fmt.Errorf("marshal policy decision for autoclaim request %s: %w", request.Key, err)
	}
	manualDecisionJSON, err := marshalOptional(request.ManualDecision)
	if err != nil {
		return nil, fmt.Errorf("marshal manual decision for autoclaim request %s: %w", request.Key, err)
	}

	row := &requestRow{
		RequestKey:           string(request.Key),
		SourceNetwork:        request.Bridge.SourceNetwork,
		OriginNetwork:        request.Bridge.OriginNetwork,
		DestinationNetwork:   request.Bridge.DestinationNetwork,
		DepositCount:         request.Bridge.DepositCount,
		Status:               request.Status.String(),
		BridgeTxHash:         request.Bridge.TxHash.Hex(),
		BlockNum:             request.Bridge.BlockNum,
		BlockPos:             request.Bridge.BlockPos,
		RetryCount:           request.RetryCount,
		MaxRetries:           request.MaxRetries,
		LastObservedSendAt:   nullTime(request.LastObservedSendAt),
		LastObservedResultAt: nullTime(request.LastObservedResultAt),
		CreatedAt:            request.CreatedAt,
		UpdatedAt:            request.UpdatedAt,
		LastError:            request.LastError,
		BridgeJSON:           bridgeJSON,
		ProofJSON:            proofJSON,
		PolicyDecisionJSON:   policyDecisionJSON,
		ManualDecisionJSON:   manualDecisionJSON,
		VerifyBlockNum:       request.VerifyBlockNum,
	}

	if request.LER != (common.Hash{}) {
		row.LER = sql.NullString{String: request.LER.Hex(), Valid: true}
	}

	if request.PolicyDecision != nil {
		row.PolicyResult = sql.NullString{String: request.PolicyDecision.Result.String(), Valid: true}
	}
	if request.ClaimTxHash != nil {
		row.ClaimTxHash = sql.NullString{String: request.ClaimTxHash.Hex(), Valid: true}
	}
	if request.TxManagerID != nil {
		row.TxManagerID = sql.NullString{String: request.TxManagerID.Hex(), Valid: true}
	}
	if request.GlobalIndex != nil {
		row.GlobalIndex = sql.NullString{String: request.GlobalIndex.String(), Valid: true}
	}
	if request.L1InfoTreeIndex != nil {
		row.L1InfoTreeIndex = sql.NullInt64{Int64: int64(*request.L1InfoTreeIndex), Valid: true}
	}

	return row, nil
}

func (r *requestRow) toRequest() (*autoclaimtypes.AutoClaimRequest, error) {
	var bridge autoclaimtypes.BridgeExit
	if err := json.Unmarshal(r.BridgeJSON, &bridge); err != nil {
		return nil, fmt.Errorf("unmarshal autoclaim bridge %s: %w", r.RequestKey, err)
	}
	// The source_network column is authoritative: older rows predate the field in bridge_json.
	bridge.SourceNetwork = r.SourceNetwork

	request := &autoclaimtypes.AutoClaimRequest{
		Key:            autoclaimtypes.RequestKey(r.RequestKey),
		Status:         autoclaimtypes.RequestStatus(r.Status),
		Bridge:         bridge,
		RetryCount:     r.RetryCount,
		MaxRetries:     r.MaxRetries,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
		LastError:      r.LastError,
		VerifyBlockNum: r.VerifyBlockNum,
	}
	if r.LER.Valid {
		request.LER = common.HexToHash(r.LER.String)
	}

	if r.GlobalIndex.Valid {
		globalIndex, ok := parseBigInt(r.GlobalIndex.String)
		if !ok {
			return nil, fmt.Errorf("parse autoclaim global index %s for %s", r.GlobalIndex.String, r.RequestKey)
		}
		request.GlobalIndex = globalIndex
	}
	if r.L1InfoTreeIndex.Valid {
		index := uint32(r.L1InfoTreeIndex.Int64)
		request.L1InfoTreeIndex = &index
	}
	proof, err := unmarshalOptionalJSON[autoclaimtypes.ClaimProof](r.ProofJSON, "proof "+r.RequestKey)
	if err != nil {
		return nil, err
	}
	request.Proof = proof

	policyDecision, err := unmarshalOptionalJSON[autoclaimtypes.PolicyDecision](
		r.PolicyDecisionJSON, "decision "+r.RequestKey)
	if err != nil {
		return nil, err
	}
	request.PolicyDecision = policyDecision

	manualDecision, err := unmarshalOptionalJSON[autoclaimtypes.PolicyDecision](
		r.ManualDecisionJSON, "manual decision "+r.RequestKey)
	if err != nil {
		return nil, err
	}
	request.ManualDecision = manualDecision
	if r.ClaimTxHash.Valid {
		hash := common.HexToHash(r.ClaimTxHash.String)
		request.ClaimTxHash = &hash
	}
	if r.TxManagerID.Valid {
		hash := common.HexToHash(r.TxManagerID.String)
		request.TxManagerID = &hash
	}
	if r.LastObservedSendAt.Valid {
		request.LastObservedSendAt = &r.LastObservedSendAt.Time
	}
	if r.LastObservedResultAt.Valid {
		request.LastObservedResultAt = &r.LastObservedResultAt.Time
	}

	return request, nil
}

func selectRequestSQL() string {
	return `SELECT
		request_key,
		source_network,
		origin_network,
		destination_network,
		deposit_count,
		status,
		policy_result,
		bridge_tx_hash,
		claim_tx_hash,
		tx_manager_id,
		block_num,
		block_pos,
		global_index,
		l1_info_tree_index,
		retry_count,
		max_retries,
		last_observed_send_at,
		last_observed_result_at,
		created_at,
		updated_at,
		last_error,
		bridge_json,
		proof_json,
		policy_decision_json,
		manual_decision_json,
		ler,
		verify_block_num
	FROM autoclaim_request`
}

func marshalOptional(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}

func hasOptionalJSONValue(value []byte) bool {
	return len(value) > 0 && string(value) != "null"
}

// unmarshalOptionalJSON unmarshals raw JSON into a newly-allocated *T when the
// data is present (non-nil, non-"null").  Returns (nil, nil) when absent.
func unmarshalOptionalJSON[T any](raw []byte, label string) (*T, error) {
	if !hasOptionalJSONValue(raw) {
		return nil, nil
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("unmarshal autoclaim %s: %w", label, err)
	}
	return &v, nil
}

func nullBytes(value []byte) any {
	if value == nil {
		return nil
	}
	return value
}

func nullTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}

func parseBigInt(value string) (*big.Int, bool) {
	return new(big.Int).SetString(value, base10)
}

func requireUpdated(result sql.Result, action string, key autoclaimtypes.RequestKey) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s for autoclaim request %s rows affected: %w", action, key, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%s for autoclaim request %s: %w", action, key, db.ErrNotFound)
	}
	return nil
}

const defaultDBQueryTimeout = 30 * time.Second

func (s *Storage) withDatabaseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := s.dbQueryTimeout
	if timeout == 0 {
		timeout = defaultDBQueryTimeout
	}
	return context.WithTimeout(ctx, timeout)
}
