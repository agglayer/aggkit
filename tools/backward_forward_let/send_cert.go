package backward_forward_let

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggsenderdb "github.com/agglayer/aggkit/aggsender/db"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v2"
)

// agglayerSender is the subset of agglayer.AgglayerClientInterface used by RunSendCert.
// Defined as an interface to allow mocking in tests.
type agglayerSender interface {
	SendCertificate(ctx context.Context, certificate *agglayertypes.Certificate) (common.Hash, error)
}

// certStorager is the subset of aggsenderdb.AggSenderStorage used by RunSendCert.
type certStorager interface {
	SaveLastSentCertificate(ctx context.Context, certificate aggsendertypes.Certificate) error
	// GetCertificateHeaderByHeight returns the certificate header at the given height,
	// used to derive the correct FromBlock for the stored certificate.
	GetCertificateHeaderByHeight(height uint64) (*aggsendertypes.CertificateHeader, error)
}

// RunSendCert is the CLI action for the send-cert subcommand.
// It reads a certificate from JSON (--cert-json or --cert-file), sends it to the agglayer,
// and optionally stores it in the aggsender SQLite DB.
func RunSendCert(c *cli.Context) error {
	// Load config.
	cfg, err := LoadConfig(c)
	if err != nil {
		return err
	}

	// Parse certificate JSON.
	certJSON, err := readCertJSON(c)
	if err != nil {
		return err
	}
	var cert agglayertypes.Certificate
	if err := json.Unmarshal([]byte(certJSON), &cert); err != nil {
		return fmt.Errorf("parse certificate JSON: %w", err)
	}

	// Create agglayer client.
	logger := log.GetDefaultLogger()
	agglayerClient, err := agglayer.NewAgglayerClient(cfg.AgglayerClient, logger)
	if err != nil {
		return fmt.Errorf("create agglayer client: %w", err)
	}

	var storage certStorager
	if !c.Bool("no-db") {
		dbPath := c.String("db-path")
		storage, err = openAggsenderStorage(logger, dbPath)
		if err != nil {
			return err
		}
	}

	return sendCertificate(c.Context, cert, certJSON, agglayerClient, storage)
}

// sendCertificate sends the certificate to agglayer and stores it in the DB.
// Separated from RunSendCert so tests can inject mocks.
func sendCertificate(
	ctx context.Context,
	cert agglayertypes.Certificate,
	certJSON string,
	client agglayerSender,
	storage certStorager,
) error {
	// Send to agglayer.
	certHash, err := client.SendCertificate(ctx, &cert)
	if err != nil {
		return fmt.Errorf("send certificate to agglayer: %w", err)
	}
	fmt.Printf("Certificate sent. Hash: %s\n", certHash.Hex())

	if storage == nil {
		fmt.Println("Skipping aggsender DB storage (--no-db).")
		return nil
	}

	// Derive FromBlock from the previous certificate so that aggsender's retry
	// verification (verifyRetryCertStartingBlock) passes when this cert goes InError.
	// getLastSentBlockAndRetryCount computes: lastSentBlock = cert.FromBlock - 1 (if > 0),
	// so it must match the computed next fromBlock = prevCert.ToBlock + 1.
	var fromBlock uint64
	if cert.Height > 0 {
		prevHeader, prevErr := storage.GetCertificateHeaderByHeight(cert.Height - 1)
		if prevErr == nil && prevHeader != nil && prevHeader.ToBlock > 0 {
			fromBlock = prevHeader.ToBlock + 1
		}
	}

	// Build aggsender certificate record.
	now := uint32(time.Now().Unix())
	prevLER := common.BytesToHash(cert.PrevLocalExitRoot[:])
	certType := aggsenderCertTypeFromAggchainData(cert.AggchainData)
	record := aggsendertypes.Certificate{
		Header: &aggsendertypes.CertificateHeader{
			Height:                cert.Height,
			CertificateID:         certHash,
			NewLocalExitRoot:      cert.NewLocalExitRoot,
			PreviousLocalExitRoot: &prevLER,
			L1InfoTreeLeafCount:   cert.L1InfoTreeLeafCount,
			CertType:              certType,
			Status:                agglayertypes.Pending,
			CreatedAt:             now,
			UpdatedAt:             now,
			CertSource:            aggsendertypes.CertificateSourceLocal,
			FromBlock:             fromBlock,
		},
		SignedCertificate: &certJSON,
	}

	// Store in DB.
	if err := storage.SaveLastSentCertificate(ctx, record); err != nil {
		return fmt.Errorf("store certificate in aggsender DB: %w", err)
	}
	fmt.Printf("Certificate stored in aggsender DB at height %d.\n", cert.Height)
	return nil
}

// readCertJSON returns the certificate JSON from --cert-json or --cert-file.
func readCertJSON(c *cli.Context) (string, error) {
	if certJSON := c.String("cert-json"); certJSON != "" {
		return certJSON, nil
	}
	certFile := c.String("cert-file")
	if certFile == "" {
		return "", fmt.Errorf("one of --cert-json or --cert-file is required")
	}
	data, err := os.ReadFile(filepath.Clean(certFile))
	if err != nil {
		return "", fmt.Errorf("read cert file %s: %w", certFile, err)
	}
	return string(data), nil
}

// openAggsenderStorage opens the aggsender SQLite storage at dbPath.
func openAggsenderStorage(logger aggkitcommon.Logger, dbPath string) (certStorager, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("--db-path is required")
	}
	dbDir := filepath.Dir(dbPath)
	storage, err := aggsenderdb.NewAggSenderSQLStorage(logger, aggsenderdb.AggSenderSQLStorageConfig{
		DBPath:          dbPath,
		CertificatesDir: filepath.Join(dbDir, "certificates"),
	})
	if err != nil {
		return nil, fmt.Errorf("open aggsender DB at %s: %w", dbPath, err)
	}
	return storage, nil
}

// aggsenderCertTypeFromAggchainData infers the aggsender certificate type from the AggchainData variant.
func aggsenderCertTypeFromAggchainData(data agglayertypes.AggchainData) aggsendertypes.CertificateType {
	switch data.(type) {
	case *agglayertypes.AggchainDataProof, *agglayertypes.AggchainDataMultisigWithProof:
		return aggsendertypes.CertificateTypeFEP
	default:
		return aggsendertypes.CertificateTypePP
	}
}
