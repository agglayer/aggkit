// Package runarchive archives the output directory of an exit_certificate run in a write-once object
// store (a GCS bucket) and restores it: the delta mode reads its previous run from there and the
// exit_certificate_claimer its run data.
//
// A run is stored under its own prefix: every file of the output directory as an object
// "<prefix>/<relative path>", plus a manifest (ManifestFile) listing each file with its size, SHA-256 and
// CRC32C. The manifest is uploaded last, so a prefix without it is an incomplete upload. Objects are only
// ever created, never overwritten: the bucket's retention policy forbids it anyway.
package runarchive

import (
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// URIScheme is the scheme of an archive location: gs://<bucket>/<prefix>.
const URIScheme = "gs://"

// Location is a parsed archive URI.
type Location struct {
	Bucket string
	// Prefix is the run's prefix inside the bucket, without leading or trailing slash.
	Prefix string
}

// String returns the location as a gs:// URI.
func (l Location) String() string {
	return URIScheme + l.Bucket + "/" + l.Prefix
}

// ObjectName returns the object name of the file at the slash-separated relative path rel.
func (l Location) ObjectName(rel string) string {
	return l.Prefix + "/" + rel
}

// heightDigits is the zero-padded width of the height segment of a run prefix, so runs sort by height.
const heightDigits = 10

// Run returns the location of the run of a network's certificate at height, under the base location l:
// <base>/<networkID>/<height, zero-padded>. One certificate height is one run, so the write-once bucket
// cannot hold two different runs for the same height.
func (l Location) Run(networkID uint32, height uint64) Location {
	return Location{Bucket: l.Bucket, Prefix: fmt.Sprintf("%s/%d/%0*d", l.Prefix, networkID, heightDigits, height)}
}

// RunParts splits the location of a run into its archive base, network ID and certificate height. ok is
// false when l is not a run location, that is, when its prefix does not end with
// /<networkID>/<height as exactly 10 digits> after a non-empty base prefix: such a location is an archive
// base (the prefix runs are stored under). It is the inverse of Run.
func (l Location) RunParts() (base Location, networkID uint32, height uint64, ok bool) {
	segs := strings.Split(l.Prefix, "/")
	const minSegments = 3 // <base>/<networkID>/<height>
	if len(segs) < minSegments {
		return Location{}, 0, 0, false
	}
	netSeg, heightSeg := segs[len(segs)-2], segs[len(segs)-1]
	if len(heightSeg) != heightDigits || !isDecimal(heightSeg) || !isDecimal(netSeg) ||
		(len(netSeg) > 1 && netSeg[0] == '0') {
		return Location{}, 0, 0, false
	}
	net, err := strconv.ParseUint(netSeg, 10, 32)
	if err != nil {
		return Location{}, 0, 0, false
	}
	h, err := strconv.ParseUint(heightSeg, 10, 64)
	if err != nil {
		return Location{}, 0, 0, false
	}
	base = Location{Bucket: l.Bucket, Prefix: strings.Join(segs[:len(segs)-2], "/")}
	return base, uint32(net), h, true
}

// isDecimal reports whether s is a non-empty string of ASCII digits.
func isDecimal(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// IsURI reports whether s is an archive URI (gs://...) rather than a local path.
func IsURI(s string) bool {
	return strings.HasPrefix(s, URIScheme)
}

// ParseURI parses gs://<bucket>/<prefix>. The prefix is required (a run never lives at the bucket root)
// and must not contain empty, "." or ".." segments.
func ParseURI(uri string) (Location, error) {
	if !IsURI(uri) {
		return Location{}, fmt.Errorf("invalid archive URI %q: must start with %s", uri, URIScheme)
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(uri, URIScheme), "/")
	bucket, prefix, found := strings.Cut(rest, "/")
	if bucket == "" {
		return Location{}, fmt.Errorf("invalid archive URI %q: missing bucket", uri)
	}
	if !found || prefix == "" {
		return Location{}, fmt.Errorf("invalid archive URI %q: missing run prefix (gs://<bucket>/<prefix>)", uri)
	}
	for _, seg := range strings.Split(prefix, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return Location{}, fmt.Errorf("invalid archive URI %q: empty, \".\" or \"..\" segment in the prefix", uri)
		}
	}
	return Location{Bucket: bucket, Prefix: prefix}, nil
}

// CacheDir returns the local directory a run downloaded from loc is kept in, under baseDir:
// <baseDir>/<bucket>/<prefix>. Each run gets its own directory, so files of different runs never mix.
func CacheDir(baseDir string, loc Location) string {
	return filepath.Join(baseDir, loc.Bucket, filepath.FromSlash(path.Clean(loc.Prefix)))
}
