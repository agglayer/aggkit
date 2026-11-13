package bridgesync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	"github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	coretypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/russross/meddler"
)

const (
	// Batch size for processing records
	batchSize = 100
	dbTimeout = 2 * time.Minute
	// Number of workers for parallel txn sender extraction
	numWorkers = 5
)

// ErrBackfillBackendRequired is returned when a transaction provider is configured without the
// multidownloader backend that supplies the canonical bridge logs. Without the backend the exact
// original log cannot be resolved without falling back to a direct RPC FilterLogs, which the
// provider path deliberately forbids.
var ErrBackfillBackendRequired = errors.New(
	"backfill: a download backend is required when a transaction provider is set")

// logAvailabilityChecker is the optional capability exposed by a multidownloader backend to report
// whether a log query is already covered by the synced data. When implemented it is used as a
// pre-check so the backfill never blocks indefinitely waiting for coverage that will not advance.
type logAvailabilityChecker interface {
	IsAvailable(query mdrtypes.LogQuery) bool
}

// BackfillTxnSender handles the backfilling of txn_sender field for bridge records
type BackfillTxnSender struct {
	db                *sql.DB
	log               *log.Logger
	client            types.EthClienter
	bridgeAddr        common.Address
	dbTimeout         time.Duration
	syncFromInBridges bool
	// provider, when set, resolves transactions through the shared multidownloader transaction
	// provider instead of eth_getTransactionByHash. It is optional: a nil provider keeps the legacy
	// behaviour.
	provider types.TransactionProvider
	// backend, when set, is the multidownloader used to read the canonical bridge logs for the
	// records being backfilled. It is required whenever provider is set so the exact original log
	// (never a synthesized one, never a direct RPC FilterLogs) is passed to the provider.
	backend types.MultiDownloaderLegacy
}

// BackfillOption customizes BackfillTxnSender construction.
type BackfillOption func(*BackfillTxnSender)

// WithTransactionProvider makes the backfill resolve the transactions through the shared
// multidownloader transaction provider. The exact original log is looked up through the configured
// download backend (FilterLogs on the record's block) and passed to the provider, which resolves
// the transaction index. There is no fallback to eth_getTransactionByHash once a provider is set.
// A backend must also be supplied with WithDownloadBackend.
func WithTransactionProvider(provider types.TransactionProvider) BackfillOption {
	return func(b *BackfillTxnSender) {
		b.provider = provider
	}
}

// WithDownloadBackend sets the multidownloader backend used to read the canonical bridge logs. It
// is required together with WithTransactionProvider so the backfill never issues a direct
// FilterLogs RPC to the node: the exact original log is read from the shared multidownloader and
// passed to the transaction provider.
func WithDownloadBackend(backend types.MultiDownloaderLegacy) BackfillOption {
	return func(b *BackfillTxnSender) {
		b.backend = backend
	}
}

// NewBackfillTxnSender creates a new instance of BackfillTxnSender
func NewBackfillTxnSender(
	dbPath string,
	client types.EthClienter,
	bridgeAddr common.Address,
	syncFromInBridges bool,
	logger *log.Logger,
	opts ...BackfillOption,
) (*BackfillTxnSender, error) {
	backfiller := &BackfillTxnSender{
		log:               logger,
		client:            client,
		bridgeAddr:        bridgeAddr,
		dbTimeout:         dbTimeout,
		syncFromInBridges: syncFromInBridges,
	}
	for _, opt := range opts {
		opt(backfiller)
	}
	// Validate the options before opening the database so a misconfigured provider/backend pair does
	// not leak a database handle.
	if err := backfiller.validateConfiguration(); err != nil {
		return nil, err
	}

	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}
	backfiller.db = database
	return backfiller, nil
}

// validateConfiguration rejects option combinations that cannot work: a transaction provider always
// needs the download backend that supplies the exact original logs.
func (b *BackfillTxnSender) validateConfiguration() error {
	if b.provider != nil && b.backend == nil {
		return ErrBackfillBackendRequired
	}
	return nil
}

// BackfillAll processes bridge table to backfill txn_sender field
func (b *BackfillTxnSender) BackfillAll(ctx context.Context) error {
	b.log.Info("Starting txn_sender backfilling process")

	// Process bridge table
	if err := b.backfillTable(ctx, bridgeTableName); err != nil {
		return fmt.Errorf("failed to backfill %s table: %w", bridgeTableName, err)
	}

	b.log.Infof("Backfilling completed")
	return nil
}

// backfillTable processes a specific table to backfill txn_sender field
//
//nolint:unparam // tableName is kept for future extensibility
func (b *BackfillTxnSender) backfillTable(ctx context.Context, tableName string) error {
	b.log.Infof("Starting backfill for %s table, syncFromInBridges: %v", tableName, b.syncFromInBridges)

	// Get total count of records that need backfilling
	totalCount, err := b.getRecordsNeedingBackfillCount(ctx, tableName)
	if err != nil {
		return fmt.Errorf("failed to get count of records needing backfill: %w", err)
	}

	if totalCount == 0 {
		b.log.Infof("No records need backfilling in %s table", tableName)
		return nil
	}

	b.log.Infof("Found %d records in %s table that need txn_sender or from_address backfilling", totalCount, tableName)
	pending := totalCount
	var aggErr error
	// Process records in batches
	for {
		// Check if context is cancelled before processing next batch
		select {
		case <-ctx.Done():
			b.log.Info("backfill process cancelled, stopping gracefully")
			return ctx.Err()
		default:
		}

		records, err := b.getRecordsNeedingBackfill(ctx, tableName, batchSize)
		if err != nil {
			return fmt.Errorf("failed to get records for backfilling: %w", err)
		}
		if len(records) == 0 {
			break
		}

		processed, batchErr := b.processBatch(ctx, tableName, records)
		pending -= len(records)
		b.log.Infof("%d records remaining to backfill in %s table", pending, tableName)
		if batchErr != nil {
			// Successful updates were already persisted by processBatch. Do not loop over the same
			// rows again: the failed records stay unchanged and the caller can rerun once the cause
			// (for example missing multidownloader coverage) is resolved.
			if ctx.Err() != nil {
				return batchErr
			}
			aggErr = errors.Join(aggErr,
				fmt.Errorf("%d/%d records backfilled in %s: %w", processed, len(records), tableName, batchErr))
			break
		}
	}

	if aggErr != nil {
		return aggErr
	}

	b.log.Infof("Completed backfilling for %s table", tableName)
	return nil
}

// RecordToBackfill represents a record that needs txn_sender backfilling
type RecordToBackfill struct {
	BlockNum           uint64         `meddler:"block_num"`
	BlockPos           uint64         `meddler:"block_pos"`
	FromAddress        *string        `meddler:"from_address"`
	TxHash             common.Hash    `meddler:"tx_hash,hash"`
	BlockTimestamp     uint64         `meddler:"block_timestamp"`
	LeafType           uint8          `meddler:"leaf_type"`
	OriginNetwork      uint32         `meddler:"origin_network"`
	OriginAddress      common.Address `meddler:"origin_address"`
	DestinationNetwork uint32         `meddler:"destination_network"`
	DestinationAddress common.Address `meddler:"destination_address"`
	Amount             *big.Int       `meddler:"amount,bigint"`
	Metadata           []byte         `meddler:"metadata"`
	DepositCount       uint32         `meddler:"deposit_count"`
	TxnSender          *string        `meddler:"txn_sender"`
}

// RecordUpdate represents a record update with txn_sender data
type RecordUpdate struct {
	BlockNum  uint64
	BlockPos  uint64
	TxnSender common.Address
	FromAddr  *common.Address // nil means not available (e.g. syncFromInBridges=false for asset events)
	ToAddr    common.Address
}

func (r *RecordUpdate) String() string {
	return fmt.Sprintf("BlockNum: %d, BlockPos: %d, TxnSender: %s",
		r.BlockNum, r.BlockPos, r.TxnSender.Hex())
}

// TxnSenderJob represents a job for extracting transaction sender
type TxnSenderJob struct {
	Record RecordToBackfill
}

// TxnSenderResult represents the result of extracting transaction sender
type TxnSenderResult struct {
	Update RecordUpdate
	Error  error
}

// missingFieldsCondition returns the SQL condition for records that still need backfilling.
// When syncFromInBridges is false, from_address will intentionally remain NULL for asset events
// sent through a router contract, so we only require txn_sender to be filled.
// When syncFromInBridges is true, both txn_sender and from_address must be present.
func (b *BackfillTxnSender) missingFieldsCondition() string {
	if b.syncFromInBridges {
		return "txn_sender = '' OR txn_sender IS NULL OR from_address = '' OR from_address IS NULL"
	}
	return "txn_sender = '' OR txn_sender IS NULL"
}

// getRecordsNeedingBackfillCount returns the count of records that need txn_sender backfilling
func (b *BackfillTxnSender) getRecordsNeedingBackfillCount(ctx context.Context, tableName string) (int, error) {
	missingFieldsCond := b.missingFieldsCondition()
	//nolint:gosec
	query := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM %s
		WHERE (%s)
		AND (source IS NULL OR (source != $1 AND source != $2))
	`, tableName, missingFieldsCond)

	var count int
	dbCtx, cancel := context.WithTimeout(ctx, b.dbTimeout)
	defer cancel()

	err := b.db.QueryRowContext(dbCtx, query,
		BridgeSourceBackwardLET, BridgeSourceForwardLET).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count records needing backfill: %w", err)
	}

	return count, nil
}

// getRecordsNeedingBackfill retrieves records that need txn_sender backfilling
func (b *BackfillTxnSender) getRecordsNeedingBackfill(
	ctx context.Context,
	tableName string,
	limit int,
) ([]RecordToBackfill, error) {
	missingFieldsCond := b.missingFieldsCondition()
	//nolint:gosec
	query := fmt.Sprintf(`
		SELECT *
		FROM %s
		WHERE (%s)
		AND (source IS NULL OR (source != $1 AND source != $2))
		LIMIT $3
	`, tableName, missingFieldsCond)

	dbCtx, cancel := context.WithTimeout(ctx, b.dbTimeout)
	defer cancel()
	rows, err := b.db.QueryContext(dbCtx, query,
		BridgeSourceBackwardLET, BridgeSourceForwardLET, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query records needing backfill: %w", err)
	}
	defer rows.Close()
	var recordsPtr []*RecordToBackfill
	if err = meddler.ScanAll(rows, &recordsPtr); err != nil {
		return nil, fmt.Errorf("meddler.ScanAll failed to scan records: %w", err)
	}
	recordsIface := db.SlicePtrsToSlice(recordsPtr)
	records, ok := recordsIface.([]RecordToBackfill)
	if !ok {
		return nil, errors.New("failed to convert")
	}
	return records, nil
}

// processBatch processes a batch of records to backfill txn_sender using a worker pool.
// It returns the number of records whose update was successfully persisted and an aggregate error
// describing the records that failed. Successful updates are persisted (bulkUpdate) before the
// error is returned, so a caller can propagate the failure without losing the successful work and
// without retrying the same failed rows forever.
func (b *BackfillTxnSender) processBatch(
	ctx context.Context,
	tableName string,
	records []RecordToBackfill,
) (int, error) {
	// Create channels for job distribution and result collection
	jobChan := make(chan TxnSenderJob, len(records))
	resultChan := make(chan TxnSenderResult, len(records))

	// Start worker pool
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go b.worker(ctx, i, jobChan, resultChan, &wg)
	}

	// Send jobs to workers
	go func() {
		defer close(jobChan)
		for _, record := range records {
			select {
			case <-ctx.Done():
				b.log.Info("backfill process cancelled during job distribution, stopping gracefully")
				return
			case jobChan <- TxnSenderJob{Record: record}:
			}
		}
	}()

	// Close result channel when all workers are done
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	updates := make([]RecordUpdate, 0, len(records))
	var recordErrs []error
	for result := range resultChan {
		if result.Error != nil {
			b.log.Errorf("Failed to extract txn_sender for tx %s: %v",
				result.Update.String(), result.Error)
			recordErrs = append(recordErrs, result.Error)
			continue
		}
		updates = append(updates, result.Update)
	}

	// Check if context is cancelled before performing bulk update
	select {
	case <-ctx.Done():
		b.log.Info("backfill process cancelled before bulk update, stopping gracefully")
		return 0, ctx.Err()
	default:
	}

	// Perform bulk update if we have any successful extractions
	if len(updates) > 0 {
		if err := b.bulkUpdate(ctx, tableName, updates); err != nil {
			b.log.Errorf("Failed to bulk update txn_sender for %d records: %v", len(updates), err)
			return 0, fmt.Errorf("failed to bulk update txn_sender for %d records: %w", len(updates), err)
		}
		b.log.Infof("Successfully bulk updated txn_sender for %d records", len(updates))
	}

	if len(recordErrs) > 0 {
		return len(updates), errors.Join(recordErrs...)
	}
	return len(updates), nil
}

// worker processes jobs from the job channel and sends results to the result channel
func (b *BackfillTxnSender) worker(
	ctx context.Context,
	workerID int,
	jobChan <-chan TxnSenderJob,
	resultChan chan<- TxnSenderResult,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	for job := range jobChan {
		// Check if context is cancelled before processing each job
		select {
		case <-ctx.Done():
			b.log.Infof("Worker %d stopping due to context cancellation", workerID)
			return
		default:
		}
		logEvent := &agglayerbridge.AgglayerbridgeBridgeEvent{
			LeafType:           job.Record.LeafType,
			DestinationNetwork: job.Record.DestinationNetwork,
			DestinationAddress: job.Record.DestinationAddress,
			OriginAddress:      job.Record.OriginAddress,
			OriginNetwork:      job.Record.OriginNetwork,
			DepositCount:       job.Record.DepositCount,
			Metadata:           job.Record.Metadata,
			Amount:             job.Record.Amount,
		}
		// Extract txn_sender: through the shared provider when configured, otherwise by hash.
		var (
			txnSender common.Address
			fromAddr  *common.Address
			toAddr    common.Address
			err       error
		)
		if b.provider != nil {
			txnSender, fromAddr, toAddr, err = b.extractDataViaProvider(ctx, job.Record, logEvent)
		} else {
			txnSender, fromAddr, toAddr, err = b.extractData(ctx, job.Record.TxHash, logEvent)
		}

		result := TxnSenderResult{
			Update: RecordUpdate{
				BlockNum:  job.Record.BlockNum,
				BlockPos:  job.Record.BlockPos,
				TxnSender: txnSender,
				FromAddr:  fromAddr,
				ToAddr:    toAddr,
			},
			Error: err,
		}

		// Send result back
		select {
		case <-ctx.Done():
			b.log.Infof("Worker %d stopping due to context cancellation", workerID)
			return
		case resultChan <- result:
		}
	}
}

// extractData extracts the transaction txn_sender, from_address and to_address
func (b *BackfillTxnSender) extractData(ctx context.Context,
	txHash common.Hash,
	logEvent *agglayerbridge.AgglayerbridgeBridgeEvent) (txnSender common.Address,
	fromAddr *common.Address, toAddr common.Address, err error) {
	// Check if context is cancelled before making network call
	select {
	case <-ctx.Done():
		return common.Address{}, nil, common.Address{}, ctx.Err()
	default:
	}
	txnSender, fromAddr, toAddr, err = ExtractTxnAddresses(ctx, b.client, b.bridgeAddr, txHash,
		logEvent, b.log, b.syncFromInBridges)
	return txnSender, fromAddr, toAddr, err
}

// extractDataViaProvider resolves the transaction through the shared multidownloader provider. It
// looks up the exact original log of the record (so the provider can resolve the real transaction
// index) and passes it to TransactionByLog. It never falls back to eth_getTransactionByHash: when
// the log or the transaction is not covered by the provider a bounded, explicit error is returned.
func (b *BackfillTxnSender) extractDataViaProvider(ctx context.Context,
	record RecordToBackfill,
	logEvent *agglayerbridge.AgglayerbridgeBridgeEvent) (txnSender common.Address,
	fromAddr *common.Address, toAddr common.Address, err error) {
	select {
	case <-ctx.Done():
		return common.Address{}, nil, common.Address{}, ctx.Err()
	default:
	}

	log, err := b.findBridgeLogForRecord(ctx, record)
	if err != nil {
		return common.Address{}, nil, common.Address{}, err
	}

	tx, err := b.provider.TransactionByLog(ctx, *log)
	if err != nil {
		return common.Address{}, nil, common.Address{},
			fmt.Errorf("backfill: failed to resolve transaction for block %d tx %s: %w",
				record.BlockNum, record.TxHash.Hex(), err)
	}
	if tx == nil {
		// Defensive: a provider must never signal "not covered yet" with a nil transaction and a
		// nil error. Wrap it in the shared sentinel so callers retry once coverage advances.
		return common.Address{}, nil, common.Address{},
			fmt.Errorf("%w: transaction not covered by the multidownloader for block %d tx %s",
				types.ErrTransactionUnavailable, record.BlockNum, record.TxHash.Hex())
	}

	return extractTxnAddressesWithTx(ctx, b.client, b.bridgeAddr, record.TxHash, tx, logEvent,
		b.log, b.syncFromInBridges)
}

// findBridgeLogForRecord finds the exact bridge log emitted in the record's block for the record's
// transaction. It reads the canonical logs from the configured download backend (never a direct RPC
// FilterLogs) for a single block, and matches the BridgeEvent topic locally so a backend that does
// not apply topic filters upstream cannot silently drop the log. The returned log carries the real
// block hash, number, log index and transaction index so the provider can validate canonical
// inclusion instead of relying on a synthesized log.
func (b *BackfillTxnSender) findBridgeLogForRecord(
	ctx context.Context, record RecordToBackfill,
) (*coretypes.Log, error) {
	if b.backend == nil {
		return nil, fmt.Errorf("%w: no download backend configured for block %d tx %s",
			types.ErrTransactionUnavailable, record.BlockNum, record.TxHash.Hex())
	}

	logQuery := mdrtypes.NewLogQuery(record.BlockNum, record.BlockNum, []common.Address{b.bridgeAddr})
	if checker, ok := b.backend.(logAvailabilityChecker); ok && !checker.IsAvailable(logQuery) {
		// The backend reports the range as not covered yet. Fail fast with the shared
		// "unavailable" sentinel instead of blocking until the bounded request timeout.
		return nil, fmt.Errorf("%w: bridge log block %d address %s is not covered yet",
			types.ErrTransactionUnavailable, record.BlockNum, b.bridgeAddr.Hex())
	}

	// Query by range and address only: no topic filter is sent upstream. The BridgeEvent topic0 is
	// matched locally below.
	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(record.BlockNum),
		ToBlock:   new(big.Int).SetUint64(record.BlockNum),
		Addresses: []common.Address{b.bridgeAddr},
	}
	// Bound the request so an uncovered range can never hang the backfill forever.
	reqCtx, cancel := context.WithTimeout(ctx, b.dbTimeout)
	defer cancel()
	logs, err := b.backend.FilterLogs(reqCtx, query)
	if err != nil {
		return nil, fmt.Errorf("backfill: failed to filter bridge logs for block %d: %w", record.BlockNum, err)
	}
	for i := range logs {
		lg := logs[i]
		if lg.TxHash != record.TxHash {
			continue
		}
		if len(lg.Topics) == 0 || lg.Topics[0] != bridgeEventSignature {
			continue
		}
		return &logs[i], nil
	}
	return nil, fmt.Errorf("%w: bridge log not found in block %d for tx %s",
		types.ErrTransactionUnavailable, record.BlockNum, record.TxHash.Hex())
}

// bulkUpdate performs a bulk update of multiple records
func (b *BackfillTxnSender) bulkUpdate(
	ctx context.Context,
	tableName string,
	updates []RecordUpdate,
) error {
	if len(updates) == 0 {
		return nil
	}

	dbCtx, cancel := context.WithTimeout(ctx, b.dbTimeout)
	defer cancel()
	tx, err := b.db.BeginTx(dbCtx, nil)
	if err != nil {
		return fmt.Errorf("failed to bulk update txn_sender: %w", err)
	}

	shouldRollback := true

	defer func() {
		if shouldRollback {
			b.log.Errorf("transaction rollback due to an error")
			if errRollback := tx.Rollback(); errRollback != nil {
				b.log.Errorf("error while rolling back tx %v", errRollback)
			}
		}
	}()

	stmtWithFrom, err := tx.PrepareContext(dbCtx, fmt.Sprintf(`
		UPDATE %s
		SET
			txn_sender = COALESCE(NULLIF(txn_sender, ''), ?),
			from_address = COALESCE(NULLIF(from_address, ''), ?),
			to_address = COALESCE(NULLIF(to_address, ''), ?)
		WHERE block_num = ? AND block_pos = ?;
	`, tableName))
	if err != nil {
		return fmt.Errorf("failed to prepare statement (with from_address): %w", err)
	}
	defer stmtWithFrom.Close()

	stmtWithoutFrom, err := tx.PrepareContext(dbCtx, fmt.Sprintf(`
		UPDATE %s
		SET txn_sender = COALESCE(NULLIF(txn_sender, ''), ?),
		    to_address = COALESCE(NULLIF(to_address, ''), ?)
		WHERE block_num = ? AND block_pos = ?;
	`, tableName))
	if err != nil {
		return fmt.Errorf("failed to prepare statement (without from_address): %w", err)
	}
	defer stmtWithoutFrom.Close()

	for _, update := range updates {
		var execErr error
		if update.FromAddr != nil {
			_, execErr = stmtWithFrom.ExecContext(dbCtx,
				update.TxnSender.Hex(), update.FromAddr.Hex(), update.ToAddr.Hex(), update.BlockNum, update.BlockPos)
		} else {
			_, execErr = stmtWithoutFrom.ExecContext(dbCtx,
				update.TxnSender.Hex(), update.ToAddr.Hex(), update.BlockNum, update.BlockPos)
		}
		if execErr != nil {
			return fmt.Errorf("failed to execute update for block %d pos %d: %w",
				update.BlockNum, update.BlockPos, execErr)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	shouldRollback = false // Commit was successful, no need to rollback
	return nil
}

// Close closes the database connection
func (b *BackfillTxnSender) Close() error {
	return b.db.Close()
}
