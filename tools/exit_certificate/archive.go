package exit_certificate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	aggkit "github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
)

// stepUpload is the explicit-only step that archives the output directory in options.archiveURI.
const stepUpload = "upload"

// defaultArchiveCacheDirName is the default options.archiveCacheDir, created next to options.outputDir.
const defaultArchiveCacheDirName = "archive-cache"

// Files the exit_certificate_claimer keeps in the output directory: its own L1 Info Tree databases. They are
// not part of the run and are never archived (exported so the claimer uses the same names).
const (
	FileClaimerL1InfoTreeDB    = "L1InfoTreeSync.sqlite"
	FileClaimerMultidownloadDB = "l1infotree_multidownloader.sqlite"
)

// archiveOpener opens the bucket of an archive location (runarchive.Open; tests pass a mock-backed one).
type archiveOpener func(ctx context.Context, bucket string) (runarchive.ObjectStore, func() error, error)

// ArchiveCacheDir returns the effective options.archiveCacheDir: the configured one, or "archive-cache"
// next to the output directory. The claimer derives its run data dir from it too.
func ArchiveCacheDir(opts Options) string {
	if opts.ArchiveCacheDir != "" {
		return opts.ArchiveCacheDir
	}
	return filepath.Join(filepath.Dir(filepath.Clean(opts.OutputDir)), defaultArchiveCacheDirName)
}

// resolvePreviousRun downloads options.previousRun when it is an archive URI and points the option at the
// local copy, so the rest of the delta pipeline only deals with a directory. Only the files a delta reads
// are downloaded (see selectPreviousRunFiles); each one is verified against the archive manifest.
func resolvePreviousRun(ctx context.Context, cfg *Config, open archiveOpener) error {
	uri := cfg.Options.PreviousRun
	if !runarchive.IsURI(uri) {
		return nil
	}
	loc, err := runarchive.ParseURI(uri)
	if err != nil {
		return fmt.Errorf("options.previousRun: %w", err)
	}
	store, closeStore, err := open(ctx, loc.Bucket)
	if err != nil {
		return fmt.Errorf("open the archive of options.previousRun: %w", err)
	}
	defer func() {
		if err := closeStore(); err != nil {
			log.Warnf("close archive store: %v", err)
		}
	}()

	dir := runarchive.CacheDir(ArchiveCacheDir(cfg.Options), loc)
	log.Infof("Previous run: downloading %s into %s", loc, dir)
	res, err := runarchive.Download(ctx, store, loc, dir, selectPreviousRunFiles)
	if err != nil {
		return fmt.Errorf("download options.previousRun: %w", err)
	}
	cfg.Options.PreviousRun = dir
	cfg.Options.PreviousRunArchive = loc.String()
	cfg.Options.PreviousRunArchiveManifestSHA256 = res.ManifestSHA256
	return nil
}

// selectPreviousRunFiles picks the files of an archived run that a delta (and --step recoveries) reads:
// the certificate loadPreviousRun uses, the settlement record, the exit-tree DB, and the balance state
// recoveriesStateDir looks for.
func selectPreviousRunFiles(m *runarchive.Manifest) ([]string, error) {
	cert := fileFullCertificate
	if _, ok := m.File(cert); !ok {
		cert = fileSignedCertificate
	}
	required := []string{cert, fileStepWaitResult, fileStepGLiteDB}
	for _, f := range required {
		if _, ok := m.File(f); !ok {
			return nil, fmt.Errorf("the archived run has no %s: it cannot be the previous run of a delta", f)
		}
	}
	files := required
	for _, f := range []string{fileDeltaManifest, fileStep0LBT, fileStepCSCLockedValues, fileStepFChecks} {
		if _, ok := m.File(f); ok {
			files = append(files, f)
		}
	}
	for _, f := range m.Files {
		if strings.HasPrefix(f.Path, dirRecoveries+"/") {
			files = append(files, f.Path)
		}
	}
	return files, nil
}

// runUpload is the `--step upload` entry point: it archives options.outputDir under options.archiveURI.
// Only a settled run is archived (it is what a later delta or the claimer needs), and the claimer's own L1
// Info Tree databases are left out. Run every other step on this output dir (pending_claims included)
// before: the archive is write-once, so files added afterwards can only go to a new prefix.
func runUpload(ctx context.Context, cfg *Config, open archiveOpener) error {
	if cfg.Options.ArchiveURI == "" {
		return fmt.Errorf("step %s requires options.archiveURI (gs://<bucket>/<prefix>)", stepUpload)
	}
	loc, err := runarchive.ParseURI(cfg.Options.ArchiveURI)
	if err != nil {
		return fmt.Errorf("options.archiveURI: %w", err)
	}
	dir := cfg.Options.OutputDir

	var wait StepWaitResult
	if err := loadJSON(dir, fileStepWaitResult, &wait); err != nil {
		return fmt.Errorf("step %s archives a settled run only (run --step submit,wait first): %w", stepUpload, err)
	}
	if !wait.FinalStatus.IsSettled() {
		return fmt.Errorf("step %s archives a settled run only: certificate %s is %s",
			stepUpload, wait.CertificateHash.Hex(), wait.FinalStatus)
	}
	mode := "full"
	certFile := fileSignedCertificate
	if cfg.Options.DeltaMode {
		mode, certFile = "delta", fileFullCertificate
	}
	if !fileExists(filepath.Join(dir, certFile)) {
		return fmt.Errorf("%s not found in %s: nothing to archive", certFile, dir)
	}

	files, err := runarchive.CollectFiles(dir, uploadSkip(cfg))
	if err != nil {
		return err
	}
	if err := checkpointSQLiteFiles(ctx, dir, files); err != nil {
		return err
	}

	info := map[string]string{
		"tool":            "exit_certificate",
		"toolVersion":     aggkit.Version,
		"mode":            mode,
		"l2NetworkId":     strconv.FormatUint(uint64(cfg.L2NetworkID), decimalBase),
		"certificateHash": wait.CertificateHash.Hex(),
	}
	if cfg.Options.DeltaMode {
		var manifest DeltaManifest
		if err := loadOptionalJSON(dir, fileDeltaManifest, &manifest); err != nil {
			return err
		}
		if manifest.PreviousRunArchive != "" {
			info["previousRunArchive"] = manifest.PreviousRunArchive
		}
	}

	store, closeStore, err := open(ctx, loc.Bucket)
	if err != nil {
		return fmt.Errorf("open the archive of options.archiveURI: %w", err)
	}
	defer func() {
		if err := closeStore(); err != nil {
			log.Warnf("close archive store: %v", err)
		}
	}()
	log.Infof("Archiving %s (%d files) into %s", dir, len(files), loc)
	res, err := runarchive.Upload(ctx, store, loc, dir, files, info)
	if err != nil {
		return fmt.Errorf("step %s: %w", stepUpload, err)
	}
	// Local copy of what was archived (never archived itself): it marks the output dir as uploaded.
	if err := saveJSON(dir, runarchive.ManifestFile, res.Manifest); err != nil {
		return err
	}
	log.Infof("Archived run: %s (manifest sha256 %s). Use it as options.previousRun of the next delta.",
		loc, res.ManifestSHA256)
	return nil
}

// uploadSkip leaves out of the archive the claimer's L1 Info Tree databases and, when it lives inside the
// output dir, the cache of downloaded runs.
func uploadSkip(cfg *Config) func(rel string, isDir bool) bool {
	cacheRel := ""
	if rel, err := filepath.Rel(cfg.Options.OutputDir, ArchiveCacheDir(cfg.Options)); err == nil &&
		rel != "." && !strings.HasPrefix(rel, "..") {
		cacheRel = filepath.ToSlash(rel)
	}
	return func(rel string, isDir bool) bool {
		if isDir {
			return cacheRel != "" && rel == cacheRel
		}
		return rel == FileClaimerL1InfoTreeDB || rel == FileClaimerMultidownloadDB
	}
}

// checkpointSQLiteFiles moves the WAL of every SQLite database of the run into its main file: the side
// files are not archived, so a database with pending WAL frames would be archived incomplete. It fails if a
// WAL cannot be fully checkpointed (another process still writing it).
func checkpointSQLiteFiles(ctx context.Context, dir string, files []string) error {
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".sqlite") {
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if info, err := os.Stat(p + "-wal"); err != nil || info.Size() == 0 {
			continue
		}
		if err := checkpointSQLite(ctx, p); err != nil {
			return fmt.Errorf("checkpoint %s before archiving it (is a process still using it?): %w", p, err)
		}
	}
	return nil
}

func checkpointSQLite(ctx context.Context, path string) error {
	database, err := db.NewSQLiteDB(path)
	if err != nil {
		return err
	}
	defer database.Close()
	var busy, logFrames, checkpointed int
	row := database.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	if err := row.Scan(&busy, &logFrames, &checkpointed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("wal_checkpoint returned no row")
		}
		return err
	}
	if busy != 0 {
		return fmt.Errorf("database is busy (%d of %d WAL frames checkpointed)", checkpointed, logFrames)
	}
	return nil
}
