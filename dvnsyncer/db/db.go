// Package db provides the SQLite-backed storage layer for the dvnsyncer.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"

	aggkitdb "github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/dvnsyncer/db/migrations"
)

// PacketRecord represents a LayerZero PacketSent event stored in the lz_packet table.
type PacketRecord struct {
	ChainID     uint64
	BlockNum    uint64
	TxHash      string
	LogIndex    uint64
	SrcEid      uint32
	Sender      string
	DstEid      uint32
	Receiver    string
	Nonce       uint64
	GUID        string   // hex
	Message     []byte
	PayloadHash string   // hex
	GlobalIndex *big.Int // nil if not AggLayer OFT
	OFTSendTo   string
	OFTAmountSD uint64
}

// JobAssignedRecord represents an AggLayerDVN JobAssigned event stored in the dvn_job_assigned table.
type JobAssignedRecord struct {
	ChainID       uint64
	BlockNum      uint64
	TxHash        string
	LogIndex      uint64
	PayloadHash   string
	DstEid        uint32
	Sender        string
	Fee           *big.Int
	Confirmations uint64
}

// DB wraps a sqlite database handle and exposes dvnsyncer-specific query methods.
type DB struct {
	db *sql.DB
}

// New runs migrations and opens the SQLite database at dbPath.
func New(dbPath string) (*DB, error) {
	if err := migrations.RunMigrations(dbPath); err != nil {
		return nil, fmt.Errorf("dvnsyncer/db: failed to run migrations: %w", err)
	}

	sqlDB, err := aggkitdb.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("dvnsyncer/db: failed to open database: %w", err)
	}

	return &DB{db: sqlDB}, nil
}

// Close releases the underlying database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// InsertPacket inserts a PacketRecord into the lz_packet table.
// Duplicate (chain_id, tx_hash, log_index) tuples are silently ignored (INSERT OR IGNORE).
func (d *DB) InsertPacket(ctx context.Context, r PacketRecord) error {
	var globalIndex *string
	if r.GlobalIndex != nil {
		s := r.GlobalIndex.String()
		globalIndex = &s
	}

	var oftSendTo *string
	if r.OFTSendTo != "" {
		oftSendTo = &r.OFTSendTo
	}

	var oftAmountSD *uint64
	if r.OFTAmountSD != 0 || oftSendTo != nil {
		oftAmountSD = &r.OFTAmountSD
	}

	_, err := d.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO lz_packet
			(chain_id, block_num, tx_hash, log_index,
			 src_eid, sender, dst_eid, receiver, nonce,
			 guid, message, payload_hash,
			 global_index, oft_send_to, oft_amount_sd)
		VALUES
			($1, $2, $3, $4,
			 $5, $6, $7, $8, $9,
			 $10, $11, $12,
			 $13, $14, $15)`,
		r.ChainID, r.BlockNum, r.TxHash, r.LogIndex,
		r.SrcEid, r.Sender, r.DstEid, r.Receiver, r.Nonce,
		r.GUID, r.Message, r.PayloadHash,
		globalIndex, oftSendTo, oftAmountSD,
	)
	if err != nil {
		return fmt.Errorf("dvnsyncer/db: InsertPacket: %w", err)
	}
	return nil
}

// InsertJobAssigned inserts a JobAssignedRecord into the dvn_job_assigned table.
// Duplicate (chain_id, tx_hash, log_index) tuples are silently ignored (INSERT OR IGNORE).
func (d *DB) InsertJobAssigned(ctx context.Context, r JobAssignedRecord) error {
	feeStr := "0"
	if r.Fee != nil {
		feeStr = r.Fee.String()
	}

	_, err := d.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO dvn_job_assigned
			(chain_id, block_num, tx_hash, log_index,
			 payload_hash, dst_eid, sender, fee, confirmations)
		VALUES
			($1, $2, $3, $4,
			 $5, $6, $7, $8, $9)`,
		r.ChainID, r.BlockNum, r.TxHash, r.LogIndex,
		r.PayloadHash, r.DstEid, r.Sender, feeStr, r.Confirmations,
	)
	if err != nil {
		return fmt.Errorf("dvnsyncer/db: InsertJobAssigned: %w", err)
	}
	return nil
}

// GetPacketByHash returns the first PacketRecord with the given payloadHash on chainID,
// or nil if none exists.
func (d *DB) GetPacketByHash(ctx context.Context, chainID uint64, payloadHash string) (*PacketRecord, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT chain_id, block_num, tx_hash, log_index,
		       src_eid, sender, dst_eid, receiver, nonce,
		       guid, message, payload_hash,
		       global_index, oft_send_to, oft_amount_sd
		FROM lz_packet
		WHERE chain_id = $1 AND payload_hash = $2
		LIMIT 1`,
		chainID, payloadHash,
	)

	return scanPacketRow(row)
}

// GetJobAssigned returns the first JobAssignedRecord with the given payloadHash on chainID,
// or nil if none exists.
func (d *DB) GetJobAssigned(ctx context.Context, chainID uint64, payloadHash string) (*JobAssignedRecord, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT chain_id, block_num, tx_hash, log_index,
		       payload_hash, dst_eid, sender, fee, confirmations
		FROM dvn_job_assigned
		WHERE chain_id = $1 AND payload_hash = $2
		LIMIT 1`,
		chainID, payloadHash,
	)

	return scanJobRow(row)
}

// ListPendingJobs returns all JobAssignedRecords for chainID where block_num >= sinceBlock
// and the stored confirmations value matches the given confirmations parameter.
// Callers may pass confirmations=0 to retrieve all jobs regardless of confirmation depth.
func (d *DB) ListPendingJobs(ctx context.Context, chainID uint64, sinceBlock, confirmations uint64) ([]JobAssignedRecord, error) {
	var (
		rows *sql.Rows
		err  error
	)

	if confirmations == 0 {
		rows, err = d.db.QueryContext(ctx, `
			SELECT chain_id, block_num, tx_hash, log_index,
			       payload_hash, dst_eid, sender, fee, confirmations
			FROM dvn_job_assigned
			WHERE chain_id = $1 AND block_num >= $2
			ORDER BY block_num ASC, log_index ASC`,
			chainID, sinceBlock,
		)
	} else {
		rows, err = d.db.QueryContext(ctx, `
			SELECT chain_id, block_num, tx_hash, log_index,
			       payload_hash, dst_eid, sender, fee, confirmations
			FROM dvn_job_assigned
			WHERE chain_id = $1 AND block_num >= $2 AND confirmations = $3
			ORDER BY block_num ASC, log_index ASC`,
			chainID, sinceBlock, confirmations,
		)
	}

	if err != nil {
		return nil, fmt.Errorf("dvnsyncer/db: ListPendingJobs: %w", err)
	}
	defer rows.Close()

	var result []JobAssignedRecord
	for rows.Next() {
		r, err := scanJobRowsNext(rows)
		if err != nil {
			return nil, fmt.Errorf("dvnsyncer/db: ListPendingJobs scan: %w", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dvnsyncer/db: ListPendingJobs rows.Err: %w", err)
	}
	return result, nil
}

// DeleteFromBlock removes all lz_packet and dvn_job_assigned rows for chainID
// where block_num >= fromBlock. This is used for reorg handling.
func (d *DB) DeleteFromBlock(ctx context.Context, chainID uint64, fromBlock uint64) error {
	if _, err := d.db.ExecContext(ctx,
		`DELETE FROM lz_packet WHERE chain_id = $1 AND block_num >= $2`,
		chainID, fromBlock,
	); err != nil {
		return fmt.Errorf("dvnsyncer/db: DeleteFromBlock lz_packet: %w", err)
	}

	if _, err := d.db.ExecContext(ctx,
		`DELETE FROM dvn_job_assigned WHERE chain_id = $1 AND block_num >= $2`,
		chainID, fromBlock,
	); err != nil {
		return fmt.Errorf("dvnsyncer/db: DeleteFromBlock dvn_job_assigned: %w", err)
	}

	return nil
}

// scanPacketRow scans a single *sql.Row into a *PacketRecord.
// Returns nil (not an error) when no row is found.
func scanPacketRow(row *sql.Row) (*PacketRecord, error) {
	var r PacketRecord
	var globalIndex sql.NullString
	var oftSendTo sql.NullString
	var oftAmountSD sql.NullInt64

	err := row.Scan(
		&r.ChainID, &r.BlockNum, &r.TxHash, &r.LogIndex,
		&r.SrcEid, &r.Sender, &r.DstEid, &r.Receiver, &r.Nonce,
		&r.GUID, &r.Message, &r.PayloadHash,
		&globalIndex, &oftSendTo, &oftAmountSD,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanPacketRow: %w", err)
	}

	if globalIndex.Valid {
		gi := new(big.Int)
		if _, ok := gi.SetString(globalIndex.String, 10); !ok {
			return nil, fmt.Errorf("scanPacketRow: invalid global_index %q", globalIndex.String)
		}
		r.GlobalIndex = gi
	}
	if oftSendTo.Valid {
		r.OFTSendTo = oftSendTo.String
	}
	if oftAmountSD.Valid {
		r.OFTAmountSD = uint64(oftAmountSD.Int64) //nolint:gosec
	}

	return &r, nil
}

// scanJobRow scans a single *sql.Row into a *JobAssignedRecord.
// Returns nil (not an error) when no row is found.
func scanJobRow(row *sql.Row) (*JobAssignedRecord, error) {
	var r JobAssignedRecord
	var feeStr string

	err := row.Scan(
		&r.ChainID, &r.BlockNum, &r.TxHash, &r.LogIndex,
		&r.PayloadHash, &r.DstEid, &r.Sender, &feeStr, &r.Confirmations,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanJobRow: %w", err)
	}

	fee := new(big.Int)
	if _, ok := fee.SetString(feeStr, 10); !ok {
		return nil, fmt.Errorf("scanJobRow: invalid fee %q", feeStr)
	}
	r.Fee = fee

	return &r, nil
}

// scanJobRowsNext scans the current row of an *sql.Rows into a JobAssignedRecord.
func scanJobRowsNext(rows *sql.Rows) (JobAssignedRecord, error) {
	var r JobAssignedRecord
	var feeStr string

	if err := rows.Scan(
		&r.ChainID, &r.BlockNum, &r.TxHash, &r.LogIndex,
		&r.PayloadHash, &r.DstEid, &r.Sender, &feeStr, &r.Confirmations,
	); err != nil {
		return JobAssignedRecord{}, err
	}

	fee := new(big.Int)
	if _, ok := fee.SetString(feeStr, 10); !ok {
		return JobAssignedRecord{}, fmt.Errorf("invalid fee %q", feeStr)
	}
	r.Fee = fee

	return r, nil
}
