package runarchive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agglayer/aggkit/log"
)

var (
	// ErrRunExists is returned by Upload when the prefix already holds a complete run with other files.
	ErrRunExists = errors.New("the archive prefix already holds a different run")
	// ErrObjectConflict is returned by Upload when an object of the run already exists with other content.
	ErrObjectConflict = errors.New("an archived object differs from the local file")
)

// partialSuffix is the suffix of a file being downloaded.
const partialSuffix = ".partial"

// sqliteSideSuffixes are the SQLite side files (WAL, shared memory, rollback journal) that are never
// archived: the database must be checkpointed into its main file first (see Upload).
var sqliteSideSuffixes = []string{"-wal", "-shm", "-journal"}

// isTransientFile reports whether rel is a file never archived nor expected in a run directory: SQLite side
// files, partial downloads and the local copy of the manifest.
func isTransientFile(rel string) bool {
	if rel == ManifestFile || strings.HasSuffix(rel, partialSuffix) {
		return true
	}
	for _, s := range sqliteSideSuffixes {
		if strings.HasSuffix(rel, s) {
			return true
		}
	}
	return false
}

// CollectFiles walks dir and returns the slash-separated relative paths of the files of the run, sorted.
// SQLite side files, partial downloads and the manifest are always left out; skip (optional) excludes more
// files or whole directories. A symlink or any other non-regular file is an error: an archived run must
// be self-contained.
func CollectFiles(dir string, skip func(rel string, isDir bool) bool) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		relOS, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(relOS)
		if skip != nil && skip(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file (symlinks are not archived)", p)
		}
		if isTransientFile(rel) {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list files of %s: %w", dir, err)
	}
	sort.Strings(files)
	return files, nil
}

// UploadResult is the outcome of Upload.
type UploadResult struct {
	Manifest *Manifest
	// ManifestSHA256 is the hex SHA-256 of the manifest object as stored.
	ManifestSHA256 string
	// Uploaded / Reused count the files created now and the ones found already uploaded (same content).
	Uploaded int
	Reused   int
	// AlreadyComplete is true when the prefix already held this very run (same files): nothing was uploaded.
	AlreadyComplete bool
}

// Upload archives the files (relative to dir, see CollectFiles) under loc and uploads the manifest last.
// It never overwrites: an object that already exists with the same size and CRC32C is kept (so a retry
// after a partial upload resumes), one with different content fails with ErrObjectConflict. If loc already
// holds a complete run, Upload succeeds without uploading when it lists the same files and fails with
// ErrRunExists otherwise.
func Upload(
	ctx context.Context, store ObjectStore, loc Location, dir string, files []string, info map[string]string,
) (*UploadResult, error) {
	manifest := &Manifest{Version: ManifestVersion, Info: info, Files: make([]FileEntry, 0, len(files))}
	for _, rel := range files {
		if err := validateRelPath(rel); err != nil {
			return nil, err
		}
		log.Infof("archive: hashing %s", rel)
		entry, err := hashFile(filepath.Join(dir, filepath.FromSlash(rel)), rel)
		if err != nil {
			return nil, err
		}
		manifest.Files = append(manifest.Files, entry)
	}

	if done, err := checkExistingRun(ctx, store, loc, manifest); done != nil || err != nil {
		return done, err
	}

	result := &UploadResult{Manifest: manifest}
	for _, entry := range manifest.Files {
		created, err := uploadFile(ctx, store, loc, dir, entry)
		if err != nil {
			return nil, err
		}
		if created {
			result.Uploaded++
		} else {
			result.Reused++
		}
	}

	data, err := manifest.encode()
	if err != nil {
		return nil, err
	}
	if err := store.Create(ctx, loc.ObjectName(ManifestFile), bytes.NewReader(data), checksum(data)); err != nil {
		if errors.Is(err, ErrObjectExists) { // another upload completed meanwhile: accept it only if identical
			return checkExistingRun(ctx, store, loc, manifest)
		}
		return nil, fmt.Errorf("upload %s: %w", ManifestFile, err)
	}
	result.ManifestSHA256 = sha256Hex(data)
	log.Infof("archive: %s complete (%d files uploaded, %d already there)", loc, result.Uploaded, result.Reused)
	return result, nil
}

// checkExistingRun returns a non-nil result when loc already holds a complete run with the same files, an
// ErrRunExists error when it holds a different one, and (nil, nil) when it has no manifest yet.
func checkExistingRun(ctx context.Context, store ObjectStore, loc Location, manifest *Manifest) (*UploadResult, error) {
	existing, raw, err := readManifest(ctx, store, loc)
	if errors.Is(err, ErrObjectNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !existing.sameFiles(manifest) {
		return nil, fmt.Errorf("%s already has a %s listing other files: %w (use a new prefix)",
			loc, ManifestFile, ErrRunExists)
	}
	log.Infof("archive: %s already holds this run (same %d files), nothing to upload", loc, len(manifest.Files))
	return &UploadResult{Manifest: existing, ManifestSHA256: sha256Hex(raw), AlreadyComplete: true}, nil
}

// uploadFile uploads one file unless the object already exists with the same content. It reports whether
// the object was created.
func uploadFile(ctx context.Context, store ObjectStore, loc Location, dir string, entry FileEntry) (bool, error) {
	name := loc.ObjectName(entry.Path)
	if reused, err := matchExisting(ctx, store, name, entry); err != nil || reused {
		return false, err
	}
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(entry.Path)))
	if err != nil {
		return false, fmt.Errorf("open %s: %w", entry.Path, err)
	}
	defer f.Close()
	log.Infof("archive: uploading %s (%d bytes)", entry.Path, entry.Size)
	err = store.Create(ctx, name, f, entry.CRC32C)
	if errors.Is(err, ErrObjectExists) { // created by a concurrent upload: same rule as before
		reused, mErr := matchExisting(ctx, store, name, entry)
		if mErr != nil {
			return false, mErr
		}
		if reused {
			return false, nil
		}
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// matchExisting reports whether the object exists with the entry's size and CRC32C; an object with other
// content is an ErrObjectConflict.
func matchExisting(ctx context.Context, store ObjectStore, name string, entry FileEntry) (bool, error) {
	attrs, err := store.Attrs(ctx, name)
	if errors.Is(err, ErrObjectNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if attrs.Size != entry.Size || attrs.CRC32C != entry.CRC32C {
		return false, fmt.Errorf("%s (size %d, crc32c %d; local file: size %d, crc32c %d): %w",
			name, attrs.Size, attrs.CRC32C, entry.Size, entry.CRC32C, ErrObjectConflict)
	}
	log.Infof("archive: %s already uploaded, skipping", entry.Path)
	return true, nil
}

// readManifest reads and validates the manifest of loc, returning it with its raw bytes.
func readManifest(ctx context.Context, store ObjectStore, loc Location) (*Manifest, []byte, error) {
	r, err := store.NewReader(ctx, loc.ObjectName(ManifestFile))
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	raw, err := io.ReadAll(io.LimitReader(r, maxManifestSize+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", loc.ObjectName(ManifestFile), err)
	}
	if len(raw) > maxManifestSize {
		return nil, nil, fmt.Errorf("%s is larger than %d bytes", loc.ObjectName(ManifestFile), maxManifestSize)
	}
	m, err := parseManifest(raw)
	if err != nil {
		return nil, nil, err
	}
	return m, raw, nil
}
