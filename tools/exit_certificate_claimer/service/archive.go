package claimer

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/agglayer/aggkit/log"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
)

// Run data files the claimer reads from an archived run (the exit_certificate output directory names).
const (
	localExitTreeDBFile = "step-g-l2bridgesyncerlite.sqlite"
	stepWaitResultFile  = "step-wait-result.json"
)

// FetchRunSource downloads the run archived at cfg.RunSource into its directory under cfg.RunDataDir,
// verifying every file against the archive manifest, and points the run data paths of cfg at the local
// copies. Only the files the claimer reads are downloaded: the certificate (the full one of a delta run,
// else the signed one), the local exit tree DB, the WAIT result and, when archived, the pending bridges.
func FetchRunSource(ctx context.Context, cfg *Config, logger *log.Logger) error {
	return fetchRunSource(ctx, cfg, logger, runarchive.Open)
}

// fetchRunSource is FetchRunSource with the function that opens the bucket (tests pass a mock-backed one).
func fetchRunSource(
	ctx context.Context, cfg *Config, logger *log.Logger,
	open func(context.Context, string) (runarchive.ObjectStore, func() error, error),
) error {
	loc, err := runarchive.ParseURI(cfg.RunSource)
	if err != nil {
		return fmt.Errorf("runSource: %w", err)
	}
	store, closeStore, err := open(ctx, loc.Bucket)
	if err != nil {
		return fmt.Errorf("open the archive of runSource: %w", err)
	}
	defer func() {
		if err := closeStore(); err != nil {
			logger.Warnf("close archive store: %v", err)
		}
	}()

	dir := runarchive.CacheDir(cfg.RunDataDir, loc)
	logger.Infof("fetching run data from %s into %s", loc, dir)
	res, err := runarchive.Download(ctx, store, loc, dir, selectClaimerFiles)
	if err != nil {
		return fmt.Errorf("download runSource: %w", err)
	}
	logger.Infof("run data ready (manifest sha256 %s, %d files downloaded, %d already there)",
		res.ManifestSHA256, res.Downloaded, res.Reused)

	cfg.SignedCertificatePath = filepath.Join(dir, claimerCertificateFile(res.Manifest))
	cfg.LocalExitTreeDBPath = filepath.Join(dir, localExitTreeDBFile)
	cfg.StepWaitResultPath = filepath.Join(dir, stepWaitResultFile)
	if _, ok := res.Manifest.File(exitcertificate.FileStepPendingBridges); ok {
		cfg.PendingBridgesPath = filepath.Join(dir, exitcertificate.FileStepPendingBridges)
	}
	return cfg.validate()
}

// claimerCertificateFile is the certificate the claimer serves from an archived run: the full one of a delta
// run (every exit, latest root), else the signed one.
func claimerCertificateFile(m *runarchive.Manifest) string {
	if _, ok := m.File(fullCertificateFile); ok {
		return fullCertificateFile
	}
	return signedCertificateFile
}

// selectClaimerFiles picks the files of an archived run the claimer reads.
func selectClaimerFiles(m *runarchive.Manifest) ([]string, error) {
	files := []string{claimerCertificateFile(m), localExitTreeDBFile, stepWaitResultFile}
	for _, f := range files {
		if _, ok := m.File(f); !ok {
			return nil, fmt.Errorf("the archived run has no %s: the claimer cannot serve it", f)
		}
	}
	if _, ok := m.File(exitcertificate.FileStepPendingBridges); ok {
		files = append(files, exitcertificate.FileStepPendingBridges)
	}
	return files, nil
}
