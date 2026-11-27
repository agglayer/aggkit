package exit_certificate

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	aggkit "github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	"github.com/ethereum/go-ethereum/common"
)

// stepUpload is the explicit-only step that archives the output directory in options.archiveURI.
const stepUpload = "upload"

// uploadTmpDirName is the directory, inside the archive cache, holding each compressed file while it is
// uploaded. Every upload works in its own subdirectory (removed when it returns), so concurrent uploads
// sharing a cache dir never touch each other's files.
const uploadTmpDirName = ".upload-tmp"

// staleUploadTmpAge is how old an entry of uploadTmpDirName must be to count as the leftover of an upload
// that was killed (SIGKILL, crash) before it could clean up; runUpload removes those at start. It is far
// longer than any upload, so the files of an upload still running in another process are left alone.
const staleUploadTmpAge = 24 * time.Hour

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

// runUpload is the `--step upload` entry point: it archives options.outputDir under
// <options.archiveURI>/<networkID>/<height>, the run of the certificate's height (zero-padded, see
// runarchive.Location.Run), every file compressed with zstd.
// Only a settled run is archived (it is what a later delta or the claimer needs), and the claimer's own L1
// Info Tree databases are left out. Run every other step on this output dir (pending_claims included)
// before: the archive is write-once and the prefix is fixed by the certificate height, so files added
// afterwards are never archived.
func runUpload(ctx context.Context, cfg *Config, open archiveOpener) error {
	if cfg.Options.ArchiveURI == "" {
		return fmt.Errorf("step %s requires options.archiveURI (gs://<bucket>/<base prefix>)", stepUpload)
	}
	base, err := runarchive.ParseURI(cfg.Options.ArchiveURI)
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
	header, err := runCertificateHeader(cfg, dir, certFile)
	if err != nil {
		return err
	}
	loc := base.Run(header.NetworkID, header.Height)

	// The meta files join the run here, right before it is archived. Once uploaded (local manifest copy
	// present) they are not copied again: the archive is write-once and holds the copies it was made with.
	if fileExists(filepath.Join(dir, runarchive.ManifestFile)) {
		if len(cfg.Options.ArchiveMetaFiles) > 0 {
			log.Warnf("⚠️  %s was already archived: options.archiveMetaFiles are not copied again", dir)
		}
	} else if err := copyMetaFiles(cfg.Options); err != nil {
		return err
	}

	files, err := runarchive.CollectFiles(dir, uploadSkip(cfg))
	if err != nil {
		return err
	}
	if err := checkpointSQLiteFiles(ctx, dir, files); err != nil {
		return err
	}

	info := map[string]string{
		"tool":              "exit_certificate",
		"toolVersion":       aggkit.Version,
		"mode":              mode,
		"l2NetworkId":       strconv.FormatUint(uint64(cfg.L2NetworkID), decimalBase),
		"certificateHash":   wait.CertificateHash.Hex(),
		"height":            strconv.FormatUint(header.Height, decimalBase),
		"prevLocalExitRoot": header.PrevLocalExitRoot.Hex(),
		"newLocalExitRoot":  header.NewLocalExitRoot.Hex(),
		"createdAt":         time.Now().UTC().Format(time.RFC3339),
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
	tmpDir, cleanupTmp, err := prepareUploadTmpDir(filepath.Join(ArchiveCacheDir(cfg.Options), uploadTmpDirName))
	if err != nil {
		return err
	}
	defer cleanupTmp()
	log.Infof("Archiving %s (%d files, certificate height %d) into %s", dir, len(files), header.Height, loc)
	res, err := runarchive.Upload(ctx, store, loc, dir, files, info, tmpDir)
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

// certificateHeader is the head of a certificate JSON file: the fields that identify the run.
type certificateHeader struct {
	NetworkID         uint32      `json:"network_id"`
	Height            uint64      `json:"height"`
	PrevLocalExitRoot common.Hash `json:"prev_local_exit_root"`
	NewLocalExitRoot  common.Hash `json:"new_local_exit_root"`
}

// runCertificateHeader reads the header of the run's certificate and checks it against the config's network
// and, when present, the height Step H computed (step-h-previous-local-exit-root.json).
func runCertificateHeader(cfg *Config, dir, certFile string) (*certificateHeader, error) {
	header, err := readCertificateHeader(filepath.Join(dir, certFile))
	if err != nil {
		return nil, err
	}
	if header.NetworkID != cfg.L2NetworkID {
		return nil, fmt.Errorf("%s is for network %d, the config's l2NetworkId is %d",
			certFile, header.NetworkID, cfg.L2NetworkID)
	}
	if header.NewLocalExitRoot == (common.Hash{}) {
		return nil, fmt.Errorf("%s has no new_local_exit_root", certFile)
	}
	var h StepHResult
	if err := loadOptionalJSON(dir, fileStepHPreviousLocalExitRoot, &h); err != nil {
		return nil, err
	}
	if fileExists(filepath.Join(dir, fileStepHPreviousLocalExitRoot)) && h.Height != header.Height {
		return nil, fmt.Errorf("%s has height %d but %s says %d", certFile, header.Height,
			fileStepHPreviousLocalExitRoot, h.Height)
	}
	return header, nil
}

// readCertificateHeader decodes the top-level header fields of a certificate JSON file without loading its
// bridge exits: they come first in the file, so reading stops as soon as all of them are found (a mainnet
// certificate is hundreds of MB).
// It trusts the writer's field order (the tool always writes the header first): any field before the header
// is decoded into a json.RawMessage, i.e. buffered whole.
func readCertificateHeader(path string) (*certificateHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	dec := json.NewDecoder(bufio.NewReader(f))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("%s: not a JSON object", path)
	}
	var header certificateHeader
	targets := map[string]any{
		"network_id": &header.NetworkID, "height": &header.Height,
		"prev_local_exit_root": &header.PrevLocalExitRoot, "new_local_exit_root": &header.NewLocalExitRoot,
	}
	for len(targets) > 0 && dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		key, _ := tok.(string)
		target, ok := targets[key]
		if !ok {
			target = &json.RawMessage{}
		}
		if err := dec.Decode(target); err != nil {
			return nil, fmt.Errorf("read %s field %q: %w", path, key, err)
		}
		delete(targets, key)
	}
	if len(targets) > 0 {
		return nil, fmt.Errorf("%s: certificate header incomplete (missing %d fields)", path, len(targets))
	}
	return &header, nil
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

// prepareUploadTmpDir creates base if needed, removes the entries in it older than staleUploadTmpAge (left
// by an upload that was killed before cleaning up: compressed copies of the largest files, GBs on mainnet,
// that no later run would ever look at) and returns a fresh per-upload subdirectory plus the function that
// removes it.
func prepareUploadTmpDir(base string) (string, func(), error) {
	if err := os.MkdirAll(base, dirPermissions); err != nil {
		return "", nil, fmt.Errorf("create %s: %w", base, err)
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", nil, fmt.Errorf("read %s: %w", base, err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < staleUploadTmpAge {
			continue
		}
		p := filepath.Join(base, e.Name())
		log.Warnf("archive: removing stale upload leftover %s", p)
		if err := os.RemoveAll(p); err != nil {
			log.Warnf("archive: remove %s: %v", p, err)
		}
	}
	dir, err := os.MkdirTemp(base, "upload-*")
	if err != nil {
		return "", nil, fmt.Errorf("create upload temp dir in %s: %w", base, err)
	}
	return dir, func() {
		if err := os.RemoveAll(dir); err != nil {
			log.Warnf("archive: remove %s: %v", dir, err)
		}
	}, nil
}
