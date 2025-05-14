package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db/migrations"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

const errWhileRollbackFormat = "error while rolling back tx: %w"

type RuntimeData struct {
	NetworkID uint32
}

func (r RuntimeData) String() string {
	return fmt.Sprintf("NetworkID: %d", r.NetworkID)
}

func (r RuntimeData) IsCompatible(storage RuntimeData) error {
	if r.NetworkID != storage.NetworkID {
		return fmt.Errorf("network ID mismatch: %d != %d", r.NetworkID, storage.NetworkID)
	}
	return nil
}

// AggSenderStorage is the interface that defines the methods to interact with the storage
type AggSenderStorage interface {
	// GetCertificateByHeight returns a certificate by its height
	GetCertificateByHeight(height uint64) (*types.Certificate, error)
	// GetLastSentCertificate returns the last certificate sent to the aggLayer
	GetLastSentCertificate() (*types.Certificate, error)
	// SaveLastSentCertificate saves the last certificate sent to the aggLayer
	SaveLastSentCertificate(ctx context.Context, certificate types.Certificate) error
	// DeleteCertificate deletes a certificate from the storage
	DeleteCertificate(ctx context.Context, certificateID common.Hash) error
	// GetCertificateHeadersByStatus returns a list of certificate headers by their status
	GetCertificateHeadersByStatus(status []agglayertypes.CertificateStatus) ([]*types.CertificateHeader, error)
	// UpdateCertificateStatus updates certificate status in db
	UpdateCertificateStatus(
		ctx context.Context,
		certificateID common.Hash,
		newStatus agglayertypes.CertificateStatus,
		updatedAt uint32) error
	// GetLastSentCertificateHeader returns the last certificate header sent to the aggLayer
	GetLastSentCertificateHeader() (*types.CertificateHeader, error)
	// GetCertificateHeaderByHeight returns a certificate header by its height
	GetCertificateHeaderByHeight(height uint64) (*types.CertificateHeader, error)
	// IsLastSentCertificateInError checks if the last sent certificate is in error
	IsLastSentCertificateInError() (bool, error)
}

var _ AggSenderStorage = (*AggSenderSQLStorage)(nil)

// AggSenderSQLStorageConfig is the configuration for the AggSenderSQLStorage
type AggSenderSQLStorageConfig struct {
	DBPath                  string
	KeepCertificatesHistory bool
}

// AggSenderSQLStorage is the struct that implements the AggSenderStorage interface
type AggSenderSQLStorage struct {
	compatibility.KeyValueStorager
	logger *log.Logger
	db     *sql.DB
	cfg    AggSenderSQLStorageConfig
}

// NewAggSenderSQLStorage creates a new AggSenderSQLStorage
func NewAggSenderSQLStorage(logger *log.Logger, cfg AggSenderSQLStorageConfig) (*AggSenderSQLStorage, error) {
	database, err := db.NewSQLiteDB(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := migrations.RunMigrations(logger, database); err != nil {
		return nil, err
	}

	return &AggSenderSQLStorage{
		db:               database,
		logger:           logger,
		cfg:              cfg,
		KeyValueStorager: db.NewKeyValueStorage(database),
	}, nil
}

// GetCertificateHeadersByStatus returns a list of certificate headers by their status
func (a *AggSenderSQLStorage) GetCertificateHeadersByStatus(
	statuses []agglayertypes.CertificateStatus) ([]*types.CertificateHeader, error) {
	query := selectQueryCertificateHeader

	args := make([]any, len(statuses))

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

	var certificates []*types.CertificateHeader
	if err := meddler.QueryAll(a.db, &certificates, query, args...); err != nil {
		return nil, err
	}

	return certificates, nil
}

// GetCertificateByHeight returns a certificate by its height
func (a *AggSenderSQLStorage) GetCertificateByHeight(height uint64) (*types.Certificate, error) {
	certInfo, err := getCertificateByHeight(a.db, height)
	if err != nil {
		return nil, err
	}

	if certInfo == nil {
		return nil, nil
	}

	return certInfo.toCertificate(), nil
}

// GetCertificateHeaderByHeight returns a certificate by its height
func (a *AggSenderSQLStorage) GetCertificateHeaderByHeight(height uint64) (*types.CertificateHeader, error) {
	var certificateHeader types.CertificateHeader
	if err := meddler.QueryRow(a.db, &certificateHeader,
		fmt.Sprintf("%s WHERE height = $1;", selectQueryCertificateHeader), height); err != nil {
		return nil, getSelectQueryError(height, err)
	}
	return &certificateHeader, nil
}

// getCertificateByHeight returns a certificate by its height using the provided db
func getCertificateByHeight(db db.Querier,
	height uint64) (*certificateInfo, error) {
	var certificateInfo certificateInfo
	if err := meddler.QueryRow(db, &certificateInfo,
		"SELECT * FROM certificate_info WHERE height = $1;", height); err != nil {
		return nil, getSelectQueryError(height, err)
	}

	return &certificateInfo, nil
}

// GetLastSentCertificate returns the last certificate sent to the aggLayer
func (a *AggSenderSQLStorage) GetLastSentCertificate() (*types.Certificate, error) {
	var certificateInfo certificateInfo
	if err := meddler.QueryRow(a.db, &certificateInfo,
		"SELECT * FROM certificate_info ORDER BY height DESC LIMIT 1;"); err != nil {
		return nil, getSelectQueryError(0, err)
	}

	return certificateInfo.toCertificate(), nil
}

// GetLastSentCertificateHeader returns the last certificate header sent to the aggLayer
func (a *AggSenderSQLStorage) GetLastSentCertificateHeader() (*types.CertificateHeader, error) {
	var certificateHeader types.CertificateHeader
	if err := meddler.QueryRow(a.db, &certificateHeader,
		fmt.Sprintf("%s ORDER BY height DESC LIMIT 1;", selectQueryCertificateHeader)); err != nil {
		return nil, getSelectQueryError(0, err)
	}
	return &certificateHeader, nil
}

// SaveLastSentCertificate saves the last certificate sent to the aggLayer
func (a *AggSenderSQLStorage) SaveLastSentCertificate(ctx context.Context, certificate types.Certificate) error {
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

	certificateInfo, err := convertCertificateToCertificateInfo(&certificate)
	if err != nil {
		return fmt.Errorf("error converting certificate to certificate info: %w", err)
	}

	cert, err := getCertificateByHeight(tx, certificateInfo.Height)
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

	if err = meddler.Insert(tx, "certificate_info", certificateInfo); err != nil {
		return fmt.Errorf("error inserting certificate info: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("saveLastSentCertificate commit. Err: %w", err)
	}
	shouldRollback = false

	a.logger.Debugf("inserted certificate - Height: %d. Hash: %s",
		certificateInfo.Height, certificateInfo.CertificateID)

	return nil
}

func (a *AggSenderSQLStorage) moveCertificateToHistoryOrDelete(tx db.Querier,
	certificate *certificateInfo) error {
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

// UpdateCertificateStatus updates a certificate status in the storage
func (a *AggSenderSQLStorage) UpdateCertificateStatus(
	ctx context.Context,
	certificateID common.Hash,
	newStatus agglayertypes.CertificateStatus,
	updatedAt uint32) error {
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
		newStatus, updatedAt, certificateID.String()); err != nil {
		return fmt.Errorf("error updating certificate info: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	shouldRollback = false

	a.logger.Debugf("updated certificate status - CertificateID: %s", certificateID)

	return nil
}

// IsLastSentCertificateInError checks if the last sent certificate is in error
func (a *AggSenderSQLStorage) IsLastSentCertificateInError() (bool, error) {
	var status agglayertypes.CertificateStatus
	err := a.db.QueryRow(`
		SELECT status 
		FROM certificate_info 
		ORDER BY height DESC 
		LIMIT 1;
	`).Scan(&status)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil // No certificates found
		}
		return false, fmt.Errorf("error querying last sent certificate status: %w", err)
	}

	return status.IsInError(), nil
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
