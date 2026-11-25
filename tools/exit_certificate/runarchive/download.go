package runarchive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	"github.com/agglayer/aggkit/log"
	"github.com/klauspost/compress/zstd"
)

var (
	// ErrIncompleteRun is returned by Download when the prefix has no manifest: nothing was archived there,
	// or the upload did not complete.
	ErrIncompleteRun = errors.New("no complete run archived at this prefix")
	// ErrChecksumMismatch is returned by Download when a downloaded file does not match the manifest.
	ErrChecksumMismatch = errors.New("downloaded file does not match the archive manifest")
	// ErrCacheDirMismatch is returned by Download when the local directory holds files of another run.
	ErrCacheDirMismatch = errors.New("the local directory does not hold this archived run")
)

const (
	dirPermissions  = 0o755
	filePermissions = 0o600
)

// Select picks the files of an archived run to download (paths as listed in the manifest).
type Select func(m *Manifest) ([]string, error)

// SelectAll downloads every file of the run.
func SelectAll(m *Manifest) ([]string, error) {
	files := make([]string, 0, len(m.Files))
	for _, f := range m.Files {
		files = append(files, f.Path)
	}
	return files, nil
}

// DownloadResult is the outcome of Download.
type DownloadResult struct {
	// Dir is the local directory holding the run.
	Dir      string
	Manifest *Manifest
	// ManifestSHA256 is the hex SHA-256 of the manifest object.
	ManifestSHA256 string
	// Files are the selected files, now present in Dir and verified against the manifest.
	Files []string
	// Downloaded / Reused count the files fetched now and the ones already in Dir with the right content.
	Downloaded int
	Reused     int
}

// Download fetches the files of the run archived at loc that sel picks into dir, verifying each one's size,
// SHA-256 and CRC32C against the manifest. A file already in dir with the right content is not fetched
// again; one with other content is replaced. dir is meant to hold this run only (see CacheDir): a copy of
// the manifest is kept there, and a different manifest or a file the manifest does not list is an
// ErrCacheDirMismatch.
func Download(ctx context.Context, store ObjectStore, loc Location, dir string, sel Select) (*DownloadResult, error) {
	manifest, raw, err := readManifest(ctx, store, loc)
	if errors.Is(err, ErrObjectNotExist) {
		return nil, fmt.Errorf("%s has no %s (nothing archived there, or the upload did not complete): %w",
			loc, ManifestFile, ErrIncompleteRun)
	}
	if err != nil {
		return nil, err
	}
	if err := prepareDir(dir, manifest, raw); err != nil {
		return nil, err
	}
	files, err := selectFiles(manifest, sel)
	if err != nil {
		return nil, err
	}

	result := &DownloadResult{Dir: dir, Manifest: manifest, ManifestSHA256: sha256Hex(raw), Files: files}
	for _, rel := range files {
		entry, _ := manifest.File(rel)
		fetched, err := ensureFile(ctx, store, loc, dir, entry)
		if err != nil {
			return nil, err
		}
		if fetched {
			result.Downloaded++
		} else {
			result.Reused++
		}
	}
	log.Infof("archive: %s ready in %s (%d files downloaded, %d already there)",
		loc, dir, result.Downloaded, result.Reused)
	return result, nil
}

// prepareDir creates dir, checks that it holds nothing but files of this run and stores the manifest copy.
func prepareDir(dir string, manifest *Manifest, raw []byte) error {
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	localManifest := filepath.Join(dir, ManifestFile)
	existing, err := os.ReadFile(localManifest)
	switch {
	case err == nil:
		if !bytes.Equal(existing, raw) {
			return fmt.Errorf("%s holds the manifest of another run: %w (remove the directory)", dir, ErrCacheDirMismatch)
		}
	case errors.Is(err, os.ErrNotExist):
		if err := os.WriteFile(localManifest, raw, filePermissions); err != nil {
			return fmt.Errorf("write %s: %w", localManifest, err)
		}
	default:
		return fmt.Errorf("read %s: %w", localManifest, err)
	}

	present, err := CollectFiles(dir, nil)
	if err != nil {
		return err
	}
	for _, rel := range present {
		if _, ok := manifest.File(rel); !ok {
			return fmt.Errorf("%s holds %s, which the archived run does not list: %w (remove the directory)",
				dir, rel, ErrCacheDirMismatch)
		}
	}
	return nil
}

func selectFiles(manifest *Manifest, sel Select) ([]string, error) {
	if sel == nil {
		sel = SelectAll
	}
	files, err := sel(manifest)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(files))
	out := make([]string, 0, len(files))
	for _, rel := range files {
		if _, ok := manifest.File(rel); !ok {
			return nil, fmt.Errorf("%s is not in the archived run", rel)
		}
		if _, dup := seen[rel]; dup {
			continue
		}
		seen[rel] = struct{}{}
		out = append(out, rel)
	}
	return out, nil
}

// ensureFile makes dir/entry.Path hold the entry's content, downloading it when missing or different. It
// reports whether the file was downloaded.
func ensureFile(ctx context.Context, store ObjectStore, loc Location, dir string, entry FileEntry) (bool, error) {
	dest := filepath.Join(dir, filepath.FromSlash(entry.Path))
	if _, err := os.Stat(dest); err == nil {
		local, err := hashFile(dest, entry.Path)
		if err != nil {
			return false, err
		}
		if local.sameSource(entry) {
			return false, nil
		}
		log.Warnf("archive: %s differs from the archived file, downloading it again", dest)
	}
	if err := downloadFile(ctx, store, loc.ObjectName(entry.Object), dest, entry); err != nil {
		return false, err
	}
	return true, nil
}

// downloadFile streams the object into dest.partial, verifies it and renames it to dest. Stale SQLite side
// files of dest are removed first: they would be replayed onto the new database.
func downloadFile(ctx context.Context, store ObjectStore, name, dest string, entry FileEntry) error {
	if err := os.MkdirAll(filepath.Dir(dest), dirPermissions); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dest), err)
	}
	for _, s := range sqliteSideSuffixes {
		if err := os.Remove(dest + s); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale %s: %w", dest+s, err)
		}
	}
	tmp := dest + partialSuffix
	if err := fetchVerified(ctx, store, name, tmp, entry); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	return nil
}

// fetchVerified streams the compressed object into tmp, decompressing it on the fly, and checks both the
// object (size, CRC32C) and the decompressed file (size, SHA-256) against the manifest entry.
func fetchVerified(ctx context.Context, store ObjectStore, name, tmp string, entry FileEntry) error {
	r, err := store.NewReader(ctx, name)
	if err != nil {
		return err
	}
	defer r.Close()
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, filePermissions)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	defer f.Close()

	log.Infof("archive: downloading %s (%d bytes compressed, %d bytes)", entry.Object, entry.ObjectSize, entry.Size)
	objCRC := crc32.New(crc32cTable)
	objSize := &countingWriter{}
	compressed := io.TeeReader(r, io.MultiWriter(objCRC, objSize))
	dec, err := zstd.NewReader(compressed)
	if err != nil {
		return fmt.Errorf("decompress %s: %w", name, err)
	}
	defer dec.Close()
	sha := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, sha), dec)
	if err != nil {
		return fmt.Errorf("decompress %s: %w: %w", name, ErrChecksumMismatch, err)
	}
	// Count any bytes the decoder did not need, so the object checksum covers the whole object.
	if _, err := io.Copy(io.Discard, compressed); err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}
	if objSize.n != entry.ObjectSize || objCRC.Sum32() != entry.ObjectCRC32C {
		return fmt.Errorf("%s: object size %d crc32c %d, manifest says size %d crc32c %d: %w",
			name, objSize.n, objCRC.Sum32(), entry.ObjectSize, entry.ObjectCRC32C, ErrChecksumMismatch)
	}
	if got := hex.EncodeToString(sha.Sum(nil)); size != entry.Size || got != entry.SHA256 {
		return fmt.Errorf("%s: decompressed size %d sha256 %s, manifest says size %d sha256 %s: %w",
			name, size, got, entry.Size, entry.SHA256, ErrChecksumMismatch)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	return f.Close()
}
