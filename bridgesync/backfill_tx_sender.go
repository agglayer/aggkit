package bridgesync

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

const (
	// Batch size for processing records
	batchSize = 100
	// Constants for SQL parameter calculations
	paramsPerUpdate = 3
	paramsPerWhere  = 2
	// Parameter offsets for SQL queries
	blockNumOffset  = 1
	blockPosOffset  = 2
	txnSenderOffset = 3
)

// BackfillTxnSender handles the backfilling of txn_sender field for bridge records
type BackfillTxnSender struct {
	db         *sql.DB
	log        *log.Logger
	client     types.EthClienter
	bridgeAddr common.Address
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
	}, nil
}

// BackfillAll processes bridge table to backfill txn_sender field
func (b *BackfillTxnSender) BackfillAll(ctx context.Context) error {
	b.log.Info("Starting txn_sender backfilling process")

	// Process bridge table
	if err := b.backfillTable(ctx, "bridge"); err != nil {
		return fmt.Errorf("failed to backfill bridge table: %w", err)
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

	b.log.Infof("Found %d records in %s table that need txn_sender backfilling", totalCount, tableName)

	// Process records in batches
	offset := 0
	for offset < totalCount {
		records, err := b.getRecordsNeedingBackfill(ctx, tableName, offset, batchSize)
		if err != nil {
			return fmt.Errorf("failed to get records for backfilling: %w", err)
		}

		if len(records) == 0 {
			break
		}

		b.processBatch(ctx, tableName, records)

		offset += len(records)
		b.log.Infof("Processed %d/%d records in %s table", offset, totalCount, tableName)
	}

	b.log.Infof("Completed backfilling for %s table", tableName)
	return nil
}

// RecordToBackfill represents a record that needs txn_sender backfilling
type RecordToBackfill struct {
	BlockNum  uint64
	BlockPos  uint64
	TxHash    common.Hash
	TxnSender common.Address
}

// RecordUpdate represents a record update with txn_sender data
type RecordUpdate struct {
	BlockNum  uint64
	BlockPos  uint64
	TxnSender common.Address
}

// getRecordsNeedingBackfillCount returns the count of records that need txn_sender backfilling
func (b *BackfillTxnSender) getRecordsNeedingBackfillCount(ctx context.Context, tableName string) (int, error) {
	//nolint:gosec
	query := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM %s
		WHERE txn_sender = '' OR txn_sender IS NULL
	`, tableName)

	var count int
	err := b.db.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count records needing backfill: %w", err)
	}

	return count, nil
}

// getRecordsNeedingBackfill retrieves records that need txn_sender backfilling
func (b *BackfillTxnSender) getRecordsNeedingBackfill(
	ctx context.Context,
	tableName string,
	offset, limit int,
) ([]RecordToBackfill, error) {
	//nolint:gosec
	query := fmt.Sprintf(`
		SELECT block_num, block_pos, tx_hash
		FROM %s
		WHERE txn_sender = '' OR txn_sender IS NULL
		ORDER BY block_num, block_pos
		LIMIT $1 OFFSET $2
	`, tableName)

	rows, err := b.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query records needing backfill: %w", err)
	}
	defer rows.Close()

	var records []RecordToBackfill
	for rows.Next() {
		var record RecordToBackfill
		var txHashStr string

		err := rows.Scan(&record.BlockNum, &record.BlockPos, &txHashStr)
		if err != nil {
			return nil, fmt.Errorf("failed to scan record: %w", err)
		}

		record.TxHash = common.HexToHash(txHashStr)
		records = append(records, record)
	}

	return records, nil
}

// processBatch processes a batch of records to backfill txn_sender
func (b *BackfillTxnSender) processBatch(
	ctx context.Context,
	tableName string,
	records []RecordToBackfill,
) {
	// First, extract all txn_sender data
	updates := make([]RecordUpdate, 0, len(records))
	var failedExtractions []string

	for _, record := range records {
		// Extract txn_sender from transaction hash
		txnSender, err := b.extractTxnSender(record.TxHash)
		if err != nil {
			b.log.Errorf("Failed to extract txn_sender for tx %s: %v", record.TxHash.Hex(), err)
			failedExtractions = append(failedExtractions, record.TxHash.Hex())
			continue
		}

		updates = append(updates, RecordUpdate{
			BlockNum:  record.BlockNum,
			BlockPos:  record.BlockPos,
			TxnSender: txnSender,
		})
	}

	// Perform bulk update if we have any successful extractions
	if len(updates) > 0 {
		if err := b.bulkUpdateTxnSender(ctx, tableName, updates); err != nil {
			b.log.Errorf("Failed to bulk update txn_sender for %d records: %v", len(updates), err)
		} else {
			b.log.Infof("Successfully bulk updated txn_sender for %d records", len(updates))
		}
	}

	// Log failed extractions
	if len(failedExtractions) > 0 {
		b.log.Errorf("Failed to extract txn_sender for %d transactions: %v", len(failedExtractions), failedExtractions)
	}
}

// extractTxnSender extracts the transaction sender from a transaction hash
func (b *BackfillTxnSender) extractTxnSender(txHash common.Hash) (common.Address, error) {
	// Use the existing extractRootCall function to get the transaction sender
	rootCall, err := extractRootCall(b.client, b.bridgeAddr, txHash)
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to extract root call: %w", err)
	}

	return rootCall.From, nil
}

// bulkUpdateTxnSender performs a bulk update of txn_sender for multiple records
func (b *BackfillTxnSender) bulkUpdateTxnSender(
	ctx context.Context,
	tableName string,
	updates []RecordUpdate,
) error {
	if len(updates) == 0 {
		return nil
	}

	// Build a CASE WHEN statement for bulk update
	//nolint:gosec
	query := fmt.Sprintf("UPDATE %s SET txn_sender = CASE ", tableName)
	args := make([]interface{}, 0, len(updates)*paramsPerUpdate)

	for i, update := range updates {
		query += fmt.Sprintf("WHEN block_num = $%d AND block_pos = $%d THEN $%d ",
			i*paramsPerUpdate+blockNumOffset, i*paramsPerUpdate+blockPosOffset, i*paramsPerUpdate+txnSenderOffset)
		args = append(args, update.BlockNum, update.BlockPos, update.TxnSender.Hex())
	}

	// Add WHERE clause to limit the update to our specific records
	query += "ELSE txn_sender END WHERE ("
	for i, update := range updates {
		if i > 0 {
			query += " OR "
		}
		query += fmt.Sprintf("(block_num = $%d AND block_pos = $%d)",
			len(updates)*paramsPerUpdate+i*paramsPerWhere+blockNumOffset,
			len(updates)*paramsPerUpdate+i*paramsPerWhere+blockPosOffset,
		)
		args = append(args, update.BlockNum, update.BlockPos)
	}
	query += ")"

	_, err := b.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to bulk update txn_sender: %w", err)
	}

	return nil
}

// Close closes the database connection
func (b *BackfillTxnSender) Close() error {
	return b.db.Close()
}
