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
	// the local copy kept next to the downloaded files.
	ManifestFile = "archive-manifest.json"
	// ManifestVersion is the version of the manifest format.
	ManifestVersion = 1
	// maxManifestSize bounds the manifest read from the store (a run has a few dozen files).
	maxManifestSize = 16 << 20
)

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// FileEntry describes one archived file.
type FileEntry struct {
	// Path is the file's path relative to the run directory, slash-separated.
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	CRC32C uint32 `json:"crc32c"`
}

// Manifest lists the files of an archived run. It is uploaded last: a prefix without it is incomplete.
type Manifest struct {
	Version int `json:"version"`
	// Info is free-form metadata about the run (tool version, mode, certificate hash...). It is not
	// compared when deciding whether a prefix already holds the same run.
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

// sameFiles reports whether both manifests list exactly the same files with the same content.
func (m *Manifest) sameFiles(other *Manifest) bool {
	if len(m.Files) != len(other.Files) {
		return false
	}
	for i := range m.Files {
		if m.Files[i] != other.Files[i] {
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

// parseManifest decodes and validates a manifest: known version, safe and unique relative paths.
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
	seen := make(map[string]struct{}, len(m.Files))
	for _, f := range m.Files {
		if err := validateRelPath(f.Path); err != nil {
			return nil, fmt.Errorf("%s: %w", ManifestFile, err)
		}
		if _, dup := seen[f.Path]; dup {
			return nil, fmt.Errorf("%s: duplicated file %q", ManifestFile, f.Path)
		}
		seen[f.Path] = struct{}{}
		if f.Size < 0 || len(f.SHA256) != sha256.Size*2 {
			return nil, fmt.Errorf("%s: invalid size or sha256 for %q", ManifestFile, f.Path)
		}
	}
	return &m, nil
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

// hashReader reads r to the end and returns its size, hex SHA-256 and CRC32C.
func hashReader(r io.Reader) (int64, string, uint32, error) {
	sha := sha256.New()
	crc := crc32.New(crc32cTable)
	n, err := io.Copy(io.MultiWriter(sha, crc), r)
	if err != nil {
		return 0, "", 0, err
	}
	return n, hex.EncodeToString(sha.Sum(nil)), crc.Sum32(), nil
}

// hashFile returns the FileEntry of the local file at localPath, stored as rel.
func hashFile(localPath, rel string) (FileEntry, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return FileEntry{}, fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()
	size, sha, crc, err := hashReader(f)
	if err != nil {
		return FileEntry{}, fmt.Errorf("hash %s: %w", localPath, err)
	}
	return FileEntry{Path: rel, Size: size, SHA256: sha, CRC32C: crc}, nil
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
