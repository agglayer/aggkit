package bridgesync

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/agglayer/aggkit/bridgesync/migrations"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

const (
	// Batch size for processing records
	batchSize = 100
)

// BackfillTxSender handles the backfilling of tx_sender field for bridge and claim records
type BackfillTxSender struct {
	db             *sql.DB
	log            *log.Logger
	client         types.EthClienter
	bridgeAddr     common.Address
	processedCount int
	errorCount     int
}

// NewBackfillTxSender creates a new instance of BackfillTxSender
func NewBackfillTxSender(
	dbPath string,
	client types.EthClienter,
	bridgeAddr common.Address,
	logger *log.Logger,
) (*BackfillTxSender, error) {
	// Run migrations to ensure database schema is up to date
	if err := migrations.RunMigrations(dbPath); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return &BackfillTxSender{
		db:         database,
		log:        logger,
		client:     client,
		bridgeAddr: bridgeAddr,
	}, nil
}

// BackfillAll processes both bridge and claim tables to backfill tx_sender field
func (b *BackfillTxSender) BackfillAll(ctx context.Context) error {
	b.log.Info("Starting tx_sender backfilling process")

	// Process bridge table
	if err := b.backfillTable(ctx, "bridge"); err != nil {
		return fmt.Errorf("failed to backfill bridge table: %w", err)
	}

	// Process claim table
	if err := b.backfillTable(ctx, "claim"); err != nil {
		return fmt.Errorf("failed to backfill claim table: %w", err)
	}

	b.log.Infof("Backfilling completed. Processed: %d, Errors: %d", b.processedCount, b.errorCount)
	return nil
}

// backfillTable processes a specific table to backfill tx_sender field
func (b *BackfillTxSender) backfillTable(ctx context.Context, tableName string) error {
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

	b.log.Infof("Found %d records in %s table that need tx_sender backfilling", totalCount, tableName)

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

		if err := b.processBatch(ctx, tableName, records); err != nil {
			return fmt.Errorf("failed to process batch: %w", err)
		}

		offset += len(records)
		b.log.Infof("Processed %d/%d records in %s table", offset, totalCount, tableName)
	}

	b.log.Infof("Completed backfilling for %s table", tableName)
	return nil
}

// RecordToBackfill represents a record that needs tx_sender backfilling
type RecordToBackfill struct {
	BlockNum uint64
	BlockPos uint64
	TxHash   common.Hash
	TxSender common.Address
}

// getRecordsNeedingBackfillCount returns the count of records that need tx_sender backfilling
func (b *BackfillTxSender) getRecordsNeedingBackfillCount(ctx context.Context, tableName string) (int, error) {
	query := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM %s
		WHERE tx_sender = '' OR tx_sender IS NULL
	`, tableName)

	var count int
	err := b.db.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count records needing backfill: %w", err)
	}

	return count, nil
}

// getRecordsNeedingBackfill retrieves records that need tx_sender backfilling
func (b *BackfillTxSender) getRecordsNeedingBackfill(ctx context.Context, tableName string, offset, limit int) ([]RecordToBackfill, error) {
	query := fmt.Sprintf(`
		SELECT block_num, block_pos, tx_hash
		FROM %s
		WHERE tx_sender = '' OR tx_sender IS NULL
		ORDER BY block_num, block_pos
		LIMIT %d OFFSET %d
	`, tableName, limit, offset)

	rows, err := b.db.QueryContext(ctx, query)
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

// processBatch processes a batch of records to backfill tx_sender
func (b *BackfillTxSender) processBatch(ctx context.Context, tableName string, records []RecordToBackfill) error {
	for _, record := range records {
		// Extract tx_sender from transaction hash
		txSender, err := b.extractTxSender(ctx, record.TxHash)
		if err != nil {
			b.log.Errorf("Failed to extract tx_sender for tx %s: %v", record.TxHash.Hex(), err)
			b.errorCount++
			continue
		}

		// Update the record with the tx_sender
		if err := b.updateRecordTxSender(ctx, tableName, record.BlockNum, record.BlockPos, txSender); err != nil {
			b.log.Errorf("Failed to update tx_sender for record (block_num=%d, block_pos=%d): %v",
				record.BlockNum, record.BlockPos, err)
			b.errorCount++
			continue
		}

		b.processedCount++
	}

	return nil
}

// extractTxSender extracts the transaction sender from a transaction hash
func (b *BackfillTxSender) extractTxSender(ctx context.Context, txHash common.Hash) (common.Address, error) {
	// Use the existing extractRootCall function to get the transaction sender
	rootCall, err := extractRootCall(b.client, b.bridgeAddr, txHash)
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to extract root call: %w", err)
	}

	return rootCall.From, nil
}

// updateRecordTxSender updates a specific record with the tx_sender value
func (b *BackfillTxSender) updateRecordTxSender(ctx context.Context, tableName string, blockNum, blockPos uint64, txSender common.Address) error {
	query := fmt.Sprintf(`
		UPDATE %s
		SET tx_sender = $1
		WHERE block_num = $2 AND block_pos = $3
	`, tableName)

	_, err := b.db.ExecContext(ctx, query, txSender.Hex(), blockNum, blockPos)
	if err != nil {
		return fmt.Errorf("failed to update tx_sender: %w", err)
	}

	return nil
}

// Close closes the database connection
func (b *BackfillTxSender) Close() error {
	return b.db.Close()
}

// GetStats returns the current processing statistics
func (b *BackfillTxSender) GetStats() (processed, errors int) {
	return b.processedCount, b.errorCount
}
