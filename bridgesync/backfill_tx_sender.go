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
	"github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

const (
	// Batch size for processing records
	batchSize = 100
	dbTimeout = 2 * time.Minute
	// Number of workers for parallel txn sender extraction
	numWorkers = 5
)

// BackfillTxnSender handles the backfilling of txn_sender field for bridge records
type BackfillTxnSender struct {
	db         *sql.DB
	log        *log.Logger
	client     types.EthClienter
	bridgeAddr common.Address
	dbTimeout  time.Duration
}

// NewBackfillTxnSender creates a new instance of BackfillTxnSender
func NewBackfillTxnSender(
	dbPath string,
	client types.EthClienter,
	bridgeAddr common.Address,
	logger *log.Logger,
) (*BackfillTxnSender, error) {
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return &BackfillTxnSender{
		db:         database,
		log:        logger,
		client:     client,
		bridgeAddr: bridgeAddr,
		dbTimeout:  dbTimeout,
	}, nil
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
	b.log.Infof("Starting backfill for %s table", tableName)

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
			b.log.Errorf("failed to get records for backfilling: %w", err)
			continue
		}
		if len(records) == 0 {
			break
		}

		b.processBatch(ctx, tableName, records)
		pending -= len(records)
		b.log.Infof("%d records remaining to backfill in %s table", pending, tableName)
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
	OriginAddress      common.Address `meddler:"origin_address,address"`
	DestinationNetwork uint32         `meddler:"destination_network"`
	DestinationAddress common.Address `meddler:"destination_address,address"`
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
	FromAddr  common.Address
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

// getRecordsNeedingBackfillCount returns the count of records that need txn_sender backfilling
func (b *BackfillTxnSender) getRecordsNeedingBackfillCount(ctx context.Context, tableName string) (int, error) {
	//nolint:gosec
	query := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM %s
		WHERE (txn_sender = '' OR txn_sender IS NULL OR from_address = '' OR from_address IS NULL)
		AND (source IS NULL OR (source != $1 AND source != $2))
	`, tableName)

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
	//nolint:gosec
	query := fmt.Sprintf(`
		SELECT *
		FROM %s
		WHERE (txn_sender = '' OR txn_sender IS NULL OR from_address = '' OR from_address IS NULL)
		AND (source IS NULL OR (source != $1 AND source != $2))
		LIMIT $3
	`, tableName)

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

// processBatch processes a batch of records to backfill txn_sender using a worker pool
func (b *BackfillTxnSender) processBatch(
	ctx context.Context,
	tableName string,
	records []RecordToBackfill,
) {
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
	for result := range resultChan {
		if result.Error != nil {
			b.log.Errorf("Failed to extract txn_sender for tx %s: %v",
				result.Update.String(), result.Error)
			continue
		}
		updates = append(updates, result.Update)
	}

	// Check if context is cancelled before performing bulk update
	select {
	case <-ctx.Done():
		b.log.Info("backfill process cancelled before bulk update, stopping gracefully")
		return
	default:
	}

	// Perform bulk update if we have any successful extractions
	if len(updates) > 0 {
		if err := b.bulkUpdate(ctx, tableName, updates); err != nil {
			b.log.Errorf("Failed to bulk update txn_sender for %d records: %v", len(updates), err)
		} else {
			b.log.Infof("Successfully bulk updated txn_sender for %d records", len(updates))
		}
	}
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
		// Extract txn_sender from transaction hash
		txnSender, fromAddr, err := b.extractData(ctx, job.Record.TxHash, logEvent)

		result := TxnSenderResult{
			Update: RecordUpdate{
				BlockNum:  job.Record.BlockNum,
				BlockPos:  job.Record.BlockPos,
				TxnSender: txnSender,
				FromAddr:  fromAddr,
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

// extractData extracts the transaction txn_sender and from_address
func (b *BackfillTxnSender) extractData(ctx context.Context,
	txHash common.Hash,
	logEvent *agglayerbridge.AgglayerbridgeBridgeEvent) (txnSender common.Address, fromAddr common.Address, err error) {
	// Check if context is cancelled before making network call
	select {
	case <-ctx.Done():
		return common.Address{}, common.Address{}, ctx.Err()
	default:
	}
	txnSender, fromAddr, _, err = ExtractTxnAddresses(ctx, b.client, b.bridgeAddr, txHash, logEvent, b.log)
	return txnSender, fromAddr, err
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

	stmt, err := tx.PrepareContext(dbCtx, fmt.Sprintf(`
		UPDATE %s
		SET
			txn_sender = COALESCE(NULLIF(txn_sender, ''), ?),
			from_address = COALESCE(NULLIF(from_address, ''), ?)
		WHERE block_num = ? AND block_pos = ?;
	`, tableName))
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, update := range updates {
		_, err := stmt.ExecContext(dbCtx, update.TxnSender.Hex(), update.FromAddr.Hex(), update.BlockNum, update.BlockPos)
		if err != nil {
			return fmt.Errorf("failed to execute update for block %d pos %d: %w",
				update.BlockNum, update.BlockPos, err)
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
