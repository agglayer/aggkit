package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/db/migrations"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

const errWhileRollbackFormat = "error while rolling back tx: %w"

// AggSenderStorage is the interface that defines the methods to interact with the storage
type AggSenderStorage interface {
	// GetCertificateByHeight returns a certificate by its height
	GetCertificateByHeight(height uint64) (*types.CertificateInfo, error)
	// GetLastSentCertificate returns the last certificate sent to the aggLayer
	GetLastSentCertificate() (*types.CertificateInfo, error)
	// SaveLastSentCertificate saves the last certificate sent to the aggLayer
	SaveLastSentCertificate(ctx context.Context, certificate types.CertificateInfo) error
	// DeleteCertificate deletes a certificate from the storage
	DeleteCertificate(ctx context.Context, certificateID common.Hash) error
	// GetCertificatesByStatus returns a list of certificates by their status
	GetCertificatesByStatus(status []agglayer.CertificateStatus) ([]*types.CertificateInfo, error)
	// UpdateCertificate updates certificate in db
	UpdateCertificate(ctx context.Context, certificate types.CertificateInfo) error
}

var _ AggSenderStorage = (*AggSenderSQLStorage)(nil)

// AggSenderSQLStorageConfig is the configuration for the AggSenderSQLStorage
type AggSenderSQLStorageConfig struct {
	DBPath                  string
	KeepCertificatesHistory bool
}

// AggSenderSQLStorage is the struct that implements the AggSenderStorage interface
type AggSenderSQLStorage struct {
	logger *log.Logger
	db     *sql.DB
	cfg    AggSenderSQLStorageConfig
}

// NewAggSenderSQLStorage creates a new AggSenderSQLStorage
func NewAggSenderSQLStorage(logger *log.Logger, cfg AggSenderSQLStorageConfig) (*AggSenderSQLStorage, error) {
	db, err := db.NewSQLiteDB(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := migrations.RunMigrations(logger, db); err != nil {
		return nil, err
	}

	return &AggSenderSQLStorage{
		db:     db,
		logger: logger,
		cfg:    cfg,
	}, nil
}

func (a *AggSenderSQLStorage) GetCertificatesByStatus(
	statuses []agglayer.CertificateStatus) ([]*types.CertificateInfo, error) {
	query := "SELECT * FROM certificate_info"
	args := make([]interface{}, len(statuses))

	if len(statuses) > 0 {
		placeholders := make([]string, len(statuses))
		// Build the WHERE clause for status filtering
		for i := range statuses {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			args[i] = statuses[i]
		}

		// Build the WHERE clause with the joined placeholders
		query += " WHERE status IN (" + strings.Join(placeholders, ", ") + ")"
	}

	// Add ordering by creation date (oldest first)
	query += " ORDER BY height ASC"

	var certificates []*types.CertificateInfo
	if err := meddler.QueryAll(a.db, &certificates, query, args...); err != nil {
		return nil, err
	}

	return certificates, nil
}

// GetCertificateByHeight returns a certificate by its height
func (a *AggSenderSQLStorage) GetCertificateByHeight(height uint64) (*types.CertificateInfo, error) {
	return getCertificateByHeight(a.db, height)
}

// getCertificateByHeight returns a certificate by its height using the provided db
func getCertificateByHeight(db db.Querier,
	height uint64) (*types.CertificateInfo, error) {
	var certificateInfo types.CertificateInfo
	if err := meddler.QueryRow(db, &certificateInfo,
		"SELECT * FROM certificate_info WHERE height = $1;", height); err != nil {
		return nil, getSelectQueryError(height, err)
	}

	return &certificateInfo, nil
}

// GetLastSentCertificate returns the last certificate sent to the aggLayer
func (a *AggSenderSQLStorage) GetLastSentCertificate() (*types.CertificateInfo, error) {
	var certificateInfo types.CertificateInfo
	if err := meddler.QueryRow(a.db, &certificateInfo,
		"SELECT * FROM certificate_info ORDER BY height DESC LIMIT 1;"); err != nil {
		return nil, getSelectQueryError(0, err)
	}

	return &certificateInfo, nil
}

// SaveLastSentCertificate saves the last certificate sent to the aggLayer
func (a *AggSenderSQLStorage) SaveLastSentCertificate(ctx context.Context, certificate types.CertificateInfo) error {
	tx, err := db.NewTx(ctx, a.db)
	if err != nil {
		return fmt.Errorf("saveLastSentCertificate NewTx. Err: %w", err)
	}
	shouldRollback := true
	defer func() {
		if shouldRollback {
			if errRllbck := tx.Rollback(); errRllbck != nil {
				a.logger.Errorf(errWhileRollbackFormat, errRllbck)
			}
		}
	}()

	cert, err := getCertificateByHeight(tx, certificate.Height)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return fmt.Errorf("saveLastSentCertificate getCertificateByHeight. Err: %w", err)
	}

	if cert != nil {
		// we already have a certificate with this height
		// we need to delete it before inserting the new one
		if err = a.moveCertificateToHistoryOrDelete(tx, cert); err != nil {
			return fmt.Errorf("saveLastSentCertificate moveCertificateToHistory Err: %w", err)
		}
	}

	if err = meddler.Insert(tx, "certificate_info", &certificate); err != nil {
		return fmt.Errorf("error inserting certificate info: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("saveLastSentCertificate commit. Err: %w", err)
	}
	shouldRollback = false

	a.logger.Debugf("inserted certificate - Height: %d. Hash: %s", certificate.Height, certificate.CertificateID)

	return nil
}

func (a *AggSenderSQLStorage) moveCertificateToHistoryOrDelete(tx db.Querier,
	certificate *types.CertificateInfo) error {
	if a.cfg.KeepCertificatesHistory {
		a.logger.Debugf("moving certificate to history - new CertificateID: %s", certificate.ID())
		if _, err := tx.Exec(`INSERT INTO certificate_info_history SELECT * FROM certificate_info WHERE height = $1;`,
			certificate.Height); err != nil {
			return fmt.Errorf("error moving certificate to history: %w", err)
		}
	}
	a.logger.Debugf("deleting certificate - CertificateID: %s", certificate.ID())
	if err := deleteCertificate(tx, certificate.CertificateID); err != nil {
		return fmt.Errorf("deleteCertificate %s . Error: %w", certificate.ID(), err)
	}

	return nil
}

// DeleteCertificate deletes a certificate from the storage
func (a *AggSenderSQLStorage) DeleteCertificate(ctx context.Context, certificateID common.Hash) error {
	tx, err := db.NewTx(ctx, a.db)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			if errRllbck := tx.Rollback(); errRllbck != nil {
				a.logger.Errorf(errWhileRollbackFormat, errRllbck)
			}
		}
	}()

	if err = deleteCertificate(tx, certificateID); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return err
	}
	a.logger.Debugf("deleted certificate - CertificateID: %s", certificateID)
	return nil
}

// deleteCertificate deletes a certificate from the storage using the provided db
func deleteCertificate(tx db.Querier, certificateID common.Hash) error {
	if _, err := tx.Exec(`DELETE FROM certificate_info WHERE certificate_id = $1;`, certificateID.String()); err != nil {
		return fmt.Errorf("error deleting certificate info: %w", err)
	}

	return nil
}

// UpdateCertificate updates a certificate
func (a *AggSenderSQLStorage) UpdateCertificate(ctx context.Context, certificate types.CertificateInfo) error {
	tx, err := db.NewTx(ctx, a.db)
	if err != nil {
		return err
	}
	shouldRollback := true
	defer func() {
		if shouldRollback {
			if errRllbck := tx.Rollback(); errRllbck != nil {
				a.logger.Errorf(errWhileRollbackFormat, errRllbck)
			}
		}
	}()

	if _, err = tx.Exec(`UPDATE certificate_info SET status = $1, updated_at = $2 WHERE certificate_id = $3;`,
		certificate.Status, certificate.UpdatedAt, certificate.CertificateID.String()); err != nil {
		return fmt.Errorf("error updating certificate info: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	shouldRollback = false

	a.logger.Debugf("updated certificate status - CertificateID: %s", certificate.CertificateID)

	return nil
}

func getSelectQueryError(height uint64, err error) error {
	errToReturn := err
	if errors.Is(err, sql.ErrNoRows) {
		if height == 0 {
			// height 0 is never sent to the aggLayer
			// so we don't return an error in this case
			errToReturn = nil
		} else {
			errToReturn = db.ErrNotFound
		}
	}

	return errToReturn
}
