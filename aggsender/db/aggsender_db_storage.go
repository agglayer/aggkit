package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db/migrations"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/russross/meddler"
)

type tableName = string
type DeleteFlag = bool

const (
	errWhileRollbackFormat  = "error while rolling back tx: %w"
	nonAcceptedCertKey      = "non_accepted_cert"
	nonAcceptedCertFilename = "last_rejected_cert.json"

	tableCertificate        tableName = "certificate_info"
	tableCertificateHistory tableName = "certificate_info_history"

	MustDelete  DeleteFlag = true  // the delete action must affect at least one row
	MaybeDelete DeleteFlag = false // the delete action may affect zero rows
)

var (
	newTxer          = db.NewTx
	ErrNoCertDeleted = errors.New("no certificates deleted")
)

type RuntimeData struct {
	NetworkID uint32
}

func (r RuntimeData) String() string {
	return fmt.Sprintf("NetworkID: %d", r.NetworkID)
}

func (r RuntimeData) IsCompatible(storage RuntimeData) (*RuntimeData, error) {
	if r.NetworkID != storage.NetworkID {
		return nil, fmt.Errorf("network ID mismatch: %d != %d", r.NetworkID, storage.NetworkID)
	}
	return nil, nil
}

type AggSenderStorageMaintainer interface {
	// Move to certificate_info_history the certificate identified by CertificateKey
	MoveCertificateToHistory(tx dbtypes.Querier, height uint64) error
	// Delete from certificate_info and certificate_info_history the certificate identified by CertificateKey
	DeleteCertificate(tx dbtypes.Querier, height uint64, mustDelete DeleteFlag) error
	// Delete from certificate_info and certificate_info_history all certificates older than olderThanHeight
	DeleteOldCertificates(tx dbtypes.Querier, olderThanHeight uint64) error
}

// AggSenderStorage is the interface that defines the methods to interact with the storage
type AggSenderStorage interface {
	AggSenderStorageMaintainer

	// GetCertificateByHeight returns a certificate by its height
	GetCertificateByHeight(height uint64) (*types.Certificate, error)
	// GetLastSentCertificate returns the last certificate sent to the aggLayer
	GetLastSentCertificate() (*types.Certificate, error)
	// SaveLastSentCertificate saves the last certificate sent to the aggLayer
	SaveLastSentCertificate(ctx context.Context, certificate types.Certificate) error
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
	// GetLastSentCertificateHeaderWithProofIfInError returns the last certificate header sent to the aggLayer
	// and the aggchain proof if the certificate is in error
	GetLastSentCertificateHeaderWithProofIfInError(
		ctx context.Context) (*types.CertificateHeader, *types.AggchainProof, error)
	// SaveNonAcceptedCertificate saves a non-accepted certificate in the storage
	SaveNonAcceptedCertificate(ctx context.Context, nonAcceptedCert *NonAcceptedCertificate) error
	// GetNonAcceptedCertificate returns the last non-accepted certificate
	GetNonAcceptedCertificate() (*NonAcceptedCertificate, error)
	// SaveOrUpdateCertificate saves or updates a certificate in the storage
	SaveOrUpdateCertificate(ctx context.Context, certificate types.Certificate) error
	// GetLastSettledCertificate returns the last settled certificate from the storage
	GetLastSettledCertificate() (*types.CertificateHeader, error)
	// GetCertificateBridgeExits returns the bridge exits for the signed certificate at the given height
	GetCertificateBridgeExits(height uint64) ([]*agglayertypes.BridgeExit, error)
}

var _ AggSenderStorage = (*AggSenderSQLStorage)(nil)

// AggSenderSQLStorageConfig is the configuration for the AggSenderSQLStorage
type AggSenderSQLStorageConfig struct {
	DBPath                   string
	CertificatesDir          string
	RetainCertificatesPolicy StorageRetainCertificatesPolicy
}

// AggSenderSQLStorage is the struct that implements the AggSenderStorage interface
type AggSenderSQLStorage struct {
	dbtypes.KeyValueStorager
	logger       aggkitcommon.Logger
	db           *sql.DB
	cfg          AggSenderSQLStorageConfig
	retainPolicy StorageRetainCertificatesPolicier
}

// NewAggSenderSQLStorage creates a new AggSenderSQLStorage
func NewAggSenderSQLStorage(logger aggkitcommon.Logger, cfg AggSenderSQLStorageConfig) (*AggSenderSQLStorage, error) {
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
		retainPolicy:     &cfg.RetainCertificatesPolicy}, nil
}

// GetCertificateHeadersByStatus returns a list of certificate headers by their status.
// If statuses is nil or empty, all certificates are returned.
func (a *AggSenderSQLStorage) GetCertificateHeadersByStatus(
	statuses []agglayertypes.CertificateStatus) ([]*types.CertificateHeader, error) {
	whereClause := ""
	args := make([]any, len(statuses))

	if len(statuses) > 0 {
		placeholders := make([]string, len(statuses))
		for i := range statuses {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			args[i] = statuses[i]
		}
		whereClause = "status IN (" + strings.Join(placeholders, ", ") + ")"
	}

	return a.getCerts(nil, tableCertificate, whereClause, "ORDER BY height ASC", args)
}

func (a *AggSenderSQLStorage) getCerts(tx dbtypes.Querier, table tableName,
	whereClause string, suffix string, args []any) ([]*types.CertificateHeader, error) {
	if tx == nil {
		tx = a.db
	}
	query := fmt.Sprintf("SELECT * FROM %s", table)
	if whereClause != "" {
		query += " WHERE " + whereClause
	}
	if suffix != "" {
		query += " " + suffix
	}
	var certificates []*types.CertificateHeader
	if err := meddler.QueryAll(tx, &certificates, query, args...); err != nil {
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

	return certInfo.toCertificate()
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
func getCertificateByHeight(db dbtypes.Querier,
	height uint64) (*certificateInfo, error) {
	var certificateInfo certificateInfo
	if err := meddler.QueryRow(db, &certificateInfo,
		"SELECT * FROM certificate_info WHERE height = $1;", height); err != nil {
		return nil, getSelectQueryError(height, err)
	}
	return &certificateInfo, nil
}

// getCertificatesByHeight returns a certificates in the required table
func getCertificatesByHeight(db dbtypes.Querier, table tableName,
	height uint64) ([]*certificateInfo, error) {
	var certificates []*certificateInfo
	if err := meddler.QueryAll(db, &certificates,
		fmt.Sprintf("SELECT * FROM %s WHERE height = $1;", table), height); err != nil {
		return nil, getSelectQueryError(height, err)
	}
	return certificates, nil
}

// getCertificatesHeightOlderThanHeight returns a list of certificate heights older than the provided height
func getCertificatesHeightOlderThanHeight(db dbtypes.Querier, table tableName,
	olderThanHeight uint64) ([]string, error) {
	type signedCertificateRow struct {
		SignedCertificate string `meddler:"signed_certificate"`
	}
	var rows []*signedCertificateRow
	if err := meddler.QueryAll(db, &rows,
		fmt.Sprintf("SELECT signed_certificate FROM %s WHERE height < $1;", table), olderThanHeight); err != nil {
		return nil, err
	}
	res := make([]string, len(rows))
	for i, row := range rows {
		res[i] = row.SignedCertificate
	}
	return res, nil
}

func deleteCertificatesOlderThanHeight(tx dbtypes.Querier, olderThanHeight uint64) error {
	if _, err := tx.Exec(`DELETE FROM certificate_info WHERE height < $1;
	DELETE FROM certificate_info_history WHERE height < $2;`, olderThanHeight, olderThanHeight); err != nil {
		return fmt.Errorf("error deleting old certificates: %w", err)
	}
	return nil
}

// GetLastSentCertificate returns the last certificate sent to the aggLayer
func (a *AggSenderSQLStorage) GetLastSentCertificate() (*types.Certificate, error) {
	var certificateInfo certificateInfo
	if err := meddler.QueryRow(a.db, &certificateInfo,
		"SELECT * FROM certificate_info ORDER BY height DESC LIMIT 1;"); err != nil {
		return nil, getSelectQueryError(0, err)
	}

	return certificateInfo.toCertificate()
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

// GetLastSettledCertificate returns the last settled certificate from the storage
func (a *AggSenderSQLStorage) GetLastSettledCertificate() (*types.CertificateHeader, error) {
	var certificateHeader types.CertificateHeader
	if err := meddler.QueryRow(a.db, &certificateHeader,
		fmt.Sprintf("%s WHERE status = $1 ORDER BY height DESC LIMIT 1;", selectQueryCertificateHeader),
		agglayertypes.Settled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, fmt.Errorf("error getting last settled certificate: %w", err)
	}
	return &certificateHeader, nil
}

// GetCertificateBridgeExits returns the bridge exits for the signed certificate at the given height.
// Returns nil if no certificate exists at that height, the certificate has no signed certificate data,
// or the certificate was recovered from agglayer (no locally-stored signed cert data available).
func (a *AggSenderSQLStorage) GetCertificateBridgeExits(height uint64) ([]*agglayertypes.BridgeExit, error) {
	cert, err := a.GetCertificateByHeight(height)
	if err != nil {
		return nil, err
	}
	if cert == nil || cert.SignedCertificate == nil {
		return nil, nil
	}
	// Certs recovered from agglayer use a placeholder signed certificate ("na/agglayer header").
	// We don't have the actual signed cert data for these certs, so return nil bridge exits.
	if cert.Header != nil && cert.Header.CertSource == types.CertificateSourceAggLayer {
		return nil, nil
	}
	var agglayerCert agglayertypes.Certificate
	if err := json.Unmarshal([]byte(*cert.SignedCertificate), &agglayerCert); err != nil {
		return nil, fmt.Errorf("GetCertificateBridgeExits: failed to unmarshal certificate at height %d: %w", height, err)
	}
	return agglayerCert.BridgeExits, nil
}

// SaveOrUpdateCertificate saves the certificate in the storage
// It will insert a new certificate or update the existing one if it has the same height and certificate ID
func (a *AggSenderSQLStorage) SaveOrUpdateCertificate(ctx context.Context, certificate types.Certificate) error {
	tx, err := newTxer(ctx, a.db)
	if err != nil {
		return fmt.Errorf("saveOrUpdateCertificate NewTx. Err: %w", err)
	}
	shouldRollback := true
	defer func() {
		if shouldRollback {
			if errRllbck := tx.Rollback(); errRllbck != nil {
				a.logger.Errorf(errWhileRollbackFormat, errRllbck)
			}
		}
	}()

	certInfo, err := convertCertificateToCertificateInfo(&certificate)
	if err != nil {
		return fmt.Errorf("error converting certificate to certificate info: %w", err)
	}
	if err := a.handleCertificateFile(certInfo); err != nil {
		return err
	}
	var count int
	err = tx.QueryRow(`SELECT COUNT(*) FROM certificate_info WHERE height = $1;`, certInfo.Height).Scan(&count)
	if err != nil {
		return fmt.Errorf("error checking if certificate exists: %w", err)
	}

	// meddler does not support upsert, so we need to do this manually by checking if the certificate exists
	if count == 0 {
		// insert new certificate if it does not exist
		if err = meddler.Insert(tx, "certificate_info", certInfo); err != nil {
			return fmt.Errorf("error inserting certificate info: %w", err)
		}
	} else {
		// if the certificate exists, we need to update it
		if err = updateCertStatus(tx, certInfo.CertificateID, certInfo.Status, certInfo.UpdatedAt); err != nil {
			return fmt.Errorf("error updating certificate status: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("saveOrUpdateCertificate commit. Err: %w", err)
	}
	shouldRollback = false

	action := "inserted"
	if count > 0 {
		action = "updated"
	}

	a.logger.Debugf("%s certificate - Height: %d. Hash: %s",
		action, certInfo.Height, certInfo.CertificateID)

	return nil
}

// saveSignedCertificateToFile saves the signed certificate content to a file in the configured certificate directory
// and returns the file path
func (a *AggSenderSQLStorage) saveSignedCertificateToFile(
	fileName string,
	signedCertContent string) (string, error) {
	// Use the configured certificate directory
	certDir := a.cfg.CertificatesDir
	if err := os.MkdirAll(certDir, 0o755); err != nil { //nolint:mnd
		return "", fmt.Errorf("failed to create certificates directory %s: %w", certDir, err)
	}

	filePath := filepath.Join(certDir, fileName)

	// Write the signed certificate content to the file. Use 0644 (world-readable) so that
	// external tools (e.g. test helpers running as a different UID on the bind-mounted volume)
	// can read these files. The certs contain already-public data submitted to the agglayer.
	err := os.WriteFile(filePath, []byte(signedCertContent), 0o644) //nolint:mnd
	if err != nil {
		return "", fmt.Errorf("failed to write signed certificate to file %s: %w", filePath, err)
	}

	a.logger.Debugf("Saved signed certificate to file: %s", filePath)
	return filePath, nil
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

	certInfo, err := convertCertificateToCertificateInfo(&certificate)
	if err != nil {
		return fmt.Errorf("error converting certificate to certificate info: %w", err)
	}
	if err := a.handleCertificateFile(certInfo); err != nil {
		return err
	}

	if err := a.retainPolicy.OnNewCert(tx, a, CertificateKey{certInfo.Height, certInfo.RetryCount}); err != nil {
		return fmt.Errorf("saveLastSentCertificate error applying retain policy: %w", err)
	}

	if err = meddler.Insert(tx, "certificate_info", certInfo); err != nil {
		return fmt.Errorf("error inserting certificate info: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("saveLastSentCertificate commit. Err: %w", err)
	}
	shouldRollback = false

	a.logger.Debugf("inserted certificate - Height: %d. Hash: %s",
		certInfo.Height, certInfo.CertificateID)

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

	if err = updateCertStatus(tx, certificateID, newStatus, updatedAt); err != nil {
		return fmt.Errorf("error updating certificate status: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return err
	}
	shouldRollback = false

	a.logger.Debugf("updated certificate status - CertificateID: %s", certificateID)

	return nil
}

func updateCertStatus(tx dbtypes.Querier,
	certificateID common.Hash,
	newStatus agglayertypes.CertificateStatus,
	updatedAt uint32) error {
	if _, err := tx.Exec(`UPDATE certificate_info SET status = $1, updated_at = $2 WHERE certificate_id = $3;`,
		newStatus, updatedAt, certificateID.String()); err != nil {
		return err
	}

	return nil
}

// GetLastSentCertificateHeaderWithProofIfInError returns the last certificate header sent to the aggLayer
// and the aggchain proof if the certificate is in error
func (a *AggSenderSQLStorage) GetLastSentCertificateHeaderWithProofIfInError(
	ctx context.Context) (*types.CertificateHeader, *types.AggchainProof, error) {
	tx, err := db.NewTx(context.Background(), a.db)
	if err != nil {
		return nil, nil, fmt.Errorf("GetLastSentCertificateHeaderWithProofIfInError NewTx. Err: %w", err)
	}

	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			a.logger.Errorf("error rolling back transaction: %v", rollbackErr)
		}
	}()

	var certificateHeader types.CertificateHeader
	if err := meddler.QueryRow(a.db, &certificateHeader,
		fmt.Sprintf("%s ORDER BY height DESC LIMIT 1;", selectQueryCertificateHeader)); err != nil {
		return nil, nil, getSelectQueryError(0, err)
	}

	if certificateHeader.Status.IsInError() {
		var certWithOnlyProof types.Certificate
		if err := meddler.QueryRow(tx, &certWithOnlyProof,
			"SELECT aggchain_proof FROM certificate_info WHERE height = $1;",
			certificateHeader.Height); err != nil {
			// this has to exist since we where getting the certificate header
			// for the same height from the same table
			return nil, nil, err
		}

		return &certificateHeader, certWithOnlyProof.AggchainProof, nil
	}

	return &certificateHeader, nil, nil
}

// SaveNonAcceptedCertificate saves a non-accepted certificate in the storage in the key-value table
// since we are only saving the last non-accepted certificate
// This is used to keep track of the last non-accepted certificate
// and to allow for debugging and analysis of why they were not accepted.
func (a *AggSenderSQLStorage) SaveNonAcceptedCertificate(
	ctx context.Context, nonAcceptedCert *NonAcceptedCertificate) error {
	if nonAcceptedCert == nil {
		return fmt.Errorf("saveNonAcceptedCertificate param nonAcceptedCert is nil")
	}
	tx, err := newTxer(ctx, a.db)
	if err != nil {
		return fmt.Errorf("failed to create db transaction for non-accepted certificate persistence: %w", err)
	}

	shouldRollback := true
	defer func() {
		if shouldRollback {
			a.logger.Infof("saveNonAcceptedCertificate Rolling back transaction")
			if errRllbck := tx.Rollback(); errRllbck != nil {
				a.logger.Errorf(errWhileRollbackFormat, errRllbck)
			}
		}
	}()
	filename := nonAcceptedCertFilename
	fullPathFilename, err := a.saveSignedCertificateToFile(filename, nonAcceptedCert.SignedCertificate)
	if err != nil {
		return fmt.Errorf("saveNonAcceptedCertificate: failed to save signed certificate to file: %w", err)
	}
	entry := &NonAcceptedCertificate{
		Height:            nonAcceptedCert.Height,
		CreatedAt:         nonAcceptedCert.CreatedAt,
		Error:             nonAcceptedCert.Error,
		SignedCertificate: PrefixFilename + fullPathFilename,
		CertificateHash:   crypto.Keccak256Hash([]byte(nonAcceptedCert.SignedCertificate)),
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal non-accepted certificate struct: %w", err)
	}

	// if the value already exists, the db will update it, if not, it will insert it
	// it is all handled in the UpdateValue function
	if err := a.UpdateValue(tx, aggkitcommon.AGGSENDER, nonAcceptedCertKey, string(raw)); err != nil {
		return fmt.Errorf("failed to update non-accepted certificate value: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit db transaction for non-accepted certificate: %w", err)
	}

	shouldRollback = false

	a.logger.Debugf("inserted non-accepted certificate - Height: %d. CreatedAt: %s Filename: %s",
		nonAcceptedCert.Height, time.Unix(int64(nonAcceptedCert.CreatedAt), 0), fullPathFilename)

	return nil
}

// GetNonAcceptedCertificates returns a list of non-accepted certificates
func (a *AggSenderSQLStorage) GetNonAcceptedCertificate() (*NonAcceptedCertificate, error) {
	val, err := a.GetValue(a.db, aggkitcommon.AGGSENDER, nonAcceptedCertKey)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, nil // no non-accepted certificate found
		}
		return nil, fmt.Errorf("failed to get non-accepted certificate: %w", err)
	}

	var nonAcceptedCert NonAcceptedCertificate
	if err := json.Unmarshal([]byte(val), &nonAcceptedCert); err != nil {
		return nil, fmt.Errorf("failed to unmarshal non-accepted certificate: %w", err)
	}
	if strings.HasPrefix(nonAcceptedCert.SignedCertificate, PrefixFilename) {
		// The content is pointing to a file
		certificateFilePath := nonAcceptedCert.SignedCertificate[1:]
		data, err := os.ReadFile(certificateFilePath)
		if err != nil {
			return nil, fmt.Errorf("getNonAcceptedCertificate: failed to read signed certificate file %s: %w",
				certificateFilePath, err)
		}
		certHash := crypto.Keccak256Hash(data)
		if certHash != nonAcceptedCert.CertificateHash {
			return nil, fmt.Errorf("getNonAcceptedCertificate: certificate hash mismatch: expected %s, got %s (file: %s)",
				nonAcceptedCert.CertificateHash.String(), certHash.String(), certificateFilePath)
		}
		nonAcceptedCert.SignedCertificate = string(data)
	}

	return &nonAcceptedCert, nil
}

// Move to certificate_info_history the certificate identified by CertificateKey
func (a *AggSenderSQLStorage) MoveCertificateToHistory(tx dbtypes.Querier, height uint64) error {
	a.logger.Debugf("moving certificate to history - height: %d", height)
	if _, err := tx.Exec(`INSERT INTO certificate_info_history SELECT * FROM certificate_info WHERE height = $1;`,
		height); err != nil {
		return fmt.Errorf("error moving certificate height: %d to history: %w", height, err)
	}
	return a.deleteCertificates(tx, tableCertificate, height)
}

// Delete from certificate_info and certificate_info_history the certificate CertificateKey
// if you don't need a tx just pass nil
// It required to be in certificate_info table, if not found it returns ErrNoCertDeleted error
func (a *AggSenderSQLStorage) DeleteCertificate(tx dbtypes.Querier, height uint64, mustDelete DeleteFlag) error {
	if tx == nil {
		tx = a.db
	}
	// If there are no certificates in cert table, we return an error
	if err := a.deleteCertificates(tx, tableCertificate, height); err != nil {
		// If we allow no rows affected we ignore the error ErrNoCertDeleted
		if !errors.Is(err, ErrNoCertDeleted) || mustDelete {
			return fmt.Errorf("error deleting certificate height %d from cert table: %w", height, err)
		}
	}
	// If there are no certificates in history table, we ignore the error
	if err := a.deleteCertificates(tx, tableCertificateHistory, height); err != nil && !errors.Is(err, ErrNoCertDeleted) {
		return fmt.Errorf("error deleting certificate height %d from cert history table: %w", height, err)
	}
	return nil
}

// deleteCertificateHistory deletes the certificate history for the given height
func (a *AggSenderSQLStorage) deleteCertificates(tx dbtypes.Querier, table tableName, height uint64) error {
	certInfos, err := getCertificatesByHeight(tx, table, height)
	if err != nil {
		return fmt.Errorf("error deleting certificate history height %d when reading it: %w", height, err)
	}
	if len(certInfos) == 0 {
		return fmt.Errorf("deleteCertificates no certificates found for height %d in table %s: %w", height, table,
			ErrNoCertDeleted)
	}
	for _, certInfo := range certInfos {
		a.deleteCertificateFile(certInfo.SignedCertificateFilename())
	}
	if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE height = $1;`, table), height); err != nil {
		return fmt.Errorf("error deleting certificate history height %d: %w", height, err)
	}
	return nil
}

// Delete from certificate_info and certificate_info_history all certificates older than maxHeight
func (a *AggSenderSQLStorage) DeleteOldCertificates(tx dbtypes.Querier, maxHeight uint64) error {
	// We get list of signedCertificates from certificate_info table
	// and also from certificate_info_history table
	certs, err := getCertificatesHeightOlderThanHeight(tx, tableCertificate, maxHeight)
	if err != nil {
		return fmt.Errorf("error getting old certificate from table %s: %w", tableCertificate, err)
	}
	certsHistory, err := getCertificatesHeightOlderThanHeight(tx, tableCertificateHistory, maxHeight)
	if err != nil {
		return fmt.Errorf("error getting old certificate from table %s: %w", tableCertificateHistory, err)
	}
	certs = append(certs, certsHistory...)

	for _, cert := range certs {
		certInfo := certificateInfo{
			SignedCertificate: &cert,
		}
		filename := certInfo.SignedCertificateFilename()
		if filename != nil {
			a.logger.Infof("deleting old certificate file: %s", *filename)
			a.deleteCertificateFile(filename)
		}
	}
	return deleteCertificatesOlderThanHeight(tx, maxHeight)
}

// handleCertificateFile Handle signed certificate file storage before database operations
func (a *AggSenderSQLStorage) handleCertificateFile(certificate *certificateInfo) error {
	if certificate.SignedCertificate != nil && *certificate.SignedCertificate != "" {
		fileName := fmt.Sprintf("signed_cert_%d_%s_%d.json", certificate.Height,
			certificate.CertificateID, certificate.RetryCount)
		filePath, err := a.saveSignedCertificateToFile(
			fileName,
			*certificate.SignedCertificate,
		)
		if err != nil {
			return fmt.Errorf("error saving signed certificate to file: %w", err)
		}
		// Update the certificate to store the file path instead of the content
		certificate.SetSignedCertificateFilename(filePath)
	}

	return nil
}

// deleteFile deletes the certificate file if it exists,
// the case that doesn't exists is not an error
func (a *AggSenderSQLStorage) deleteCertificateFile(filename *string) {
	if filename == nil {
		return
	}
	// Try to delete the file
	if err := os.Remove(*filename); err != nil {
		a.logger.Warnf("error deleting certificate file %s: %w", *filename, err)
		return
	}
	a.logger.Debugf("deleted certificate file - Filename: %s", *filename)
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
