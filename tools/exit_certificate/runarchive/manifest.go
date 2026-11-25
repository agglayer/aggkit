package runarchive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
)

const (
	// ManifestFile is the name of the manifest, both as an object (<prefix>/archive-manifest.json) and as
	// the local copy kept next to the downloaded files. It is the only object stored uncompressed.
	ManifestFile = "archive-manifest.json"
	// ManifestVersion is the version of the manifest format.
	ManifestVersion = 1
	// DataVersionLegacy is the data version of a manifest that does not record one: the format of the
	// first archived runs, written before the field existed.
	DataVersionLegacy = 1
	// CompressionZstd is the only compression supported: every file is stored as <path>.zst.
	CompressionZstd = "zstd"
	// objectSuffix is appended to a file's path to name its compressed object.
	objectSuffix = ".zst"
	// maxManifestSize bounds the manifest read from the store (a run has a few dozen files).
	maxManifestSize = 16 << 20
)

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// ErrUnsupportedVersion is returned when an archived run has a manifest version this code does not know
// (parsing it, so by ReadManifest, Download and Upload) or a data version outside the range its reader
// supports (CheckDataVersion). The run is not corrupted: it was written by a newer tool, and reading it
// needs a newer reader.
var ErrUnsupportedVersion = errors.New("unsupported archive version")

// VersionRange is the inclusive range of data versions a reader of archived runs supports.
type VersionRange struct {
	Min int
	Max int
}

// Supports reports whether v is inside the range.
func (r VersionRange) Supports(v int) bool {
	return v >= r.Min && v <= r.Max
}

// String formats the range for error messages.
func (r VersionRange) String() string {
	if r.Min == r.Max {
		return strconv.Itoa(r.Min)
	}
	return fmt.Sprintf("%d to %d", r.Min, r.Max)
}

// CheckDataVersion returns an ErrUnsupportedVersion error when the data version of the archived run is
// outside the range the caller supports.
func CheckDataVersion(m *Manifest, supported VersionRange) error {
	if !supported.Supports(m.DataVersion) {
		return fmt.Errorf("the archived run has data version %d, this tool supports %s (use a newer version "+
			"of the tool): %w", m.DataVersion, supported, ErrUnsupportedVersion)
	}
	return nil
}

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
	// Version is the version of the manifest format itself (ManifestVersion).
	Version int `json:"version"`
	// DataVersion is the version of the format of the archived files, as their readers see it: the
	// certificate JSON, the lite syncer DB schema, step-wait-result.json, step-pending-l2-bridges.json and
	// the recoveries/ state. The tool that uploads the run sets it and bumps it whenever a change would
	// break an older reader; each reader checks it against the range it supports (CheckDataVersion). A
	// manifest without it is read as DataVersionLegacy.
	DataVersion int    `json:"dataVersion,omitempty"`
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
// paths, and object names derived from them. A manifest of another version is an ErrUnsupportedVersion
// (whatever else it holds: a newer format may add fields), and a missing data version is read as
// DataVersionLegacy.
func parseManifest(data []byte) (*Manifest, error) {
	var head struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ManifestFile, err)
	}
	if head.Version != nil && *head.Version != ManifestVersion {
		return nil, fmt.Errorf("%s: manifest version %d, this tool supports %d (use a newer version of the "+
			"tool): %w", ManifestFile, *head.Version, ManifestVersion, ErrUnsupportedVersion)
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ManifestFile, err)
	}
	if m.Version != ManifestVersion {
		return nil, fmt.Errorf("%s: missing version (want %d)", ManifestFile, ManifestVersion)
	}
	if m.DataVersion < 0 {
		return nil, fmt.Errorf("%s: invalid dataVersion %d", ManifestFile, m.DataVersion)
	}
	if m.DataVersion == 0 {
		m.DataVersion = DataVersionLegacy
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
