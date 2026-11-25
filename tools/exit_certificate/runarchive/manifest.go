package runarchive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path"
	"strings"
)

const (
	// ManifestFile is the name of the manifest, both as an object (<prefix>/archive-manifest.json) and as
	// the local copy kept next to the downloaded files. It is the only object stored uncompressed.
	ManifestFile = "archive-manifest.json"
	// ManifestVersion is the version of the manifest format.
	ManifestVersion = 1
	// CompressionZstd is the only compression supported: every file is stored as <path>.zst.
	CompressionZstd = "zstd"
	// objectSuffix is appended to a file's path to name its compressed object.
	objectSuffix = ".zst"
	// maxManifestSize bounds the manifest read from the store (a run has a few dozen files).
	maxManifestSize = 16 << 20
)

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// FileEntry describes one archived file: the original file (Path, Size, SHA256) and the compressed object
// it is stored as (Object, ObjectSize, ObjectCRC32C).
type FileEntry struct {
	// Path is the file's path relative to the run directory, slash-separated.
	Path string `json:"path"`
	// Size and SHA256 are those of the original (uncompressed) file.
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// Object is the name of the compressed object relative to the run prefix (Path + ".zst"), and
	// ObjectSize / ObjectCRC32C its size and CRC32C as stored.
	Object       string `json:"object"`
	ObjectSize   int64  `json:"objectSize"`
	ObjectCRC32C uint32 `json:"objectCrc32c"`
}

// sameSource reports whether both entries describe the same original file.
func (e FileEntry) sameSource(other FileEntry) bool {
	return e.Path == other.Path && e.Size == other.Size && e.SHA256 == other.SHA256
}

// Manifest lists the files of an archived run. It is uploaded last: a prefix without it is incomplete.
type Manifest struct {
	Version     int    `json:"version"`
	Compression string `json:"compression"`
	// Info is free-form metadata about the run (tool version, mode, height, certificate hash...). It is
	// not compared when deciding whether a prefix already holds the same run.
	Info  map[string]string `json:"info,omitempty"`
	Files []FileEntry       `json:"files"`
}

// File returns the entry of the file at rel, if any.
func (m *Manifest) File(rel string) (FileEntry, bool) {
	for _, f := range m.Files {
		if f.Path == rel {
			return f, true
		}
	}
	return FileEntry{}, false
}

// sameFiles reports whether both manifests list exactly the same original files.
func (m *Manifest) sameFiles(other *Manifest) bool {
	if len(m.Files) != len(other.Files) {
		return false
	}
	for i := range m.Files {
		if !m.Files[i].sameSource(other.Files[i]) {
			return false
		}
	}
	return true
}

func (m *Manifest) encode() ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", ManifestFile, err)
	}
	return data, nil
}

// parseManifest decodes and validates a manifest: known version and compression, safe and unique relative
// paths, and object names derived from them.
func parseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ManifestFile, err)
	}
	if m.Version != ManifestVersion {
		return nil, fmt.Errorf("%s: unsupported version %d (want %d)", ManifestFile, m.Version, ManifestVersion)
	}
	if m.Compression != CompressionZstd {
		return nil, fmt.Errorf("%s: unsupported compression %q (want %q)", ManifestFile, m.Compression, CompressionZstd)
	}
	seen := make(map[string]struct{}, len(m.Files))
	for _, f := range m.Files {
		if err := validateRelPath(f.Path); err != nil {
			return nil, fmt.Errorf("%s: %w", ManifestFile, err)
		}
		if _, dup := seen[f.Path]; dup {
			return nil, fmt.Errorf("%s: duplicated file %q", ManifestFile, f.Path)
		}
		seen[f.Path] = struct{}{}
		if f.Object != objectName(f.Path) {
			return nil, fmt.Errorf("%s: object of %q must be %q, got %q", ManifestFile, f.Path, objectName(f.Path), f.Object)
		}
		if f.Size < 0 || f.ObjectSize < 0 || len(f.SHA256) != sha256.Size*2 {
			return nil, fmt.Errorf("%s: invalid size or sha256 for %q", ManifestFile, f.Path)
		}
	}
	return &m, nil
}

// objectName is the name, relative to the run prefix, of the compressed object of the file at rel.
func objectName(rel string) string {
	return rel + objectSuffix
}

// validateRelPath rejects anything but a clean, relative, slash-separated path inside the run directory,
// so a (tampered) manifest cannot make a download write outside it.
func validateRelPath(rel string) error {
	if rel == "" || rel == ManifestFile || path.IsAbs(rel) || strings.Contains(rel, "\\") ||
		path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("invalid file path %q", rel)
	}
	return nil
}

// hashFile returns the source fields (Path, Size, SHA256) of the FileEntry of the local file at localPath.
func hashFile(localPath, rel string) (FileEntry, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return FileEntry{}, fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()
	sha := sha256.New()
	size, err := io.Copy(sha, f)
	if err != nil {
		return FileEntry{}, fmt.Errorf("hash %s: %w", localPath, err)
	}
	return FileEntry{Path: rel, Size: size, SHA256: hex.EncodeToString(sha.Sum(nil)), Object: objectName(rel)}, nil
}

// checksum returns the CRC32C of data.
func checksum(data []byte) uint32 {
	return crc32.Checksum(data, crc32cTable)
}

// sha256Hex returns the hex SHA-256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// countingWriter counts the bytes written through it.
type countingWriter struct{ n int64 }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}
