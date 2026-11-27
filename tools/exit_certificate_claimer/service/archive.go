package claimer

import (
	"context"
	"errors"
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

// SupportedDataVersions is the range of data versions (runarchive.Manifest.DataVersion) of an archived run
// this claimer can serve. A run outside it was written by a newer exit_certificate whose files this claimer
// may not understand: it is refused at startup and never switched to in follow mode.
var SupportedDataVersions = runarchive.VersionRange{Min: 1, Max: 1}

// archiveOpener opens the bucket of an archive location (runarchive.Open; tests pass a mock-backed one).
type archiveOpener func(ctx context.Context, bucket string) (runarchive.ObjectStore, func() error, error)

// runFiles are the local files of a run the claimer serves and, for an archived run, where it comes from.
type runFiles struct {
	// Certificate is the full certificate of a delta run, else the signed one.
	Certificate     string
	LocalExitTreeDB string
	StepWaitResult  string
	// PendingBridges is empty when the run has no pending bridges file.
	PendingBridges string

	// Source is the archive location of the run and Dir its local copy (both unset for local files).
	Source *runarchive.Location
	Dir    string
	// DataVersion is the data version in the archive manifest (0 for local files).
	DataVersion int
}

// FetchRunSource downloads the run cfg.RunSource points at (see Config.runRef) into its directory under
// cfg.RunDataDir, verifying every file against the archive manifest, and points the run data paths of cfg
// at the local copies. Only the files the claimer reads are downloaded: the certificate (the full one of a
// delta run, else the signed one), the local exit tree DB, the WAIT result and, when archived, the pending
// bridges. A run with a manifest or data version this claimer does not support is refused
// (runarchive.ErrUnsupportedVersion).
func FetchRunSource(ctx context.Context, cfg *Config, logger *log.Logger) error {
	return fetchRunSource(ctx, cfg, logger, runarchive.Open)
}

// fetchRunSource is FetchRunSource with the function that opens the bucket (tests pass a mock-backed one).
func fetchRunSource(ctx context.Context, cfg *Config, logger *log.Logger, open archiveOpener) error {
	ref, err := cfg.runRef()
	if err != nil {
		return err
	}
	store, closeStore, err := open(ctx, ref.Location.Bucket)
	if err != nil {
		return fmt.Errorf("open the archive of runSource: %w", err)
	}
	defer func() {
		if err := closeStore(); err != nil {
			logger.Warnf("close archive store: %v", err)
		}
	}()

	files, err := fetchRun(ctx, store, ref.Location, cfg.RunDataDir, logger)
	switch {
	case errors.Is(err, runarchive.ErrUnsupportedVersion):
		return fmt.Errorf("runSource %s was archived by a newer exit_certificate than this claimer supports "+
			"(upgrade the claimer, or serve an older run): %w", ref.Location, err)
	case errors.Is(err, runarchive.ErrIncompleteRun) && !ref.InLayout:
		return fmt.Errorf("download runSource (a run is gs://<bucket>/<base>/<networkID>/<height, 10 digits>; "+
			"to give the archive base, set runSourceHeight too): %w", err)
	case err != nil:
		return fmt.Errorf("download runSource: %w", err)
	}

	cfg.SignedCertificatePath = files.Certificate
	cfg.LocalExitTreeDBPath = files.LocalExitTreeDB
	cfg.StepWaitResultPath = files.StepWaitResult
	cfg.PendingBridgesPath = files.PendingBridges
	cfg.servedRun = files
	return cfg.validate()
}

// fetchRun downloads the files the claimer reads of the run archived at loc into its directory under
// runDataDir, verified against the archive manifest, after checking that the claimer supports the run's
// data version.
func fetchRun(
	ctx context.Context, store runarchive.ObjectStore, loc runarchive.Location, runDataDir string, logger *log.Logger,
) (*runFiles, error) {
	dir := runarchive.CacheDir(runDataDir, loc)
	logger.Infof("fetching run data from %s into %s", loc, dir)
	res, err := runarchive.Download(ctx, store, loc, dir, func(m *runarchive.Manifest) ([]string, error) {
		if err := runarchive.CheckDataVersion(m, SupportedDataVersions); err != nil {
			return nil, err
		}
		return selectClaimerFiles(m)
	})
	if err != nil {
		return nil, err
	}
	logger.Infof("run data ready (manifest sha256 %s, data version %d, %d files downloaded, %d already there)",
		res.ManifestSHA256, res.Manifest.DataVersion, res.Downloaded, res.Reused)

	files := &runFiles{
		Certificate:     filepath.Join(dir, claimerCertificateFile(res.Manifest)),
		LocalExitTreeDB: filepath.Join(dir, localExitTreeDBFile),
		StepWaitResult:  filepath.Join(dir, stepWaitResultFile),
		Source:          &loc,
		Dir:             dir,
		DataVersion:     res.Manifest.DataVersion,
	}
	if _, ok := res.Manifest.File(exitcertificate.FileStepPendingBridges); ok {
		files.PendingBridges = filepath.Join(dir, exitcertificate.FileStepPendingBridges)
	}
	return files, nil
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
