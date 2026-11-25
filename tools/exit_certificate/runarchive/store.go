package runarchive

import (
	"context"
	"errors"
	"io"
)

var (
	// ErrObjectNotExist is returned by ObjectStore when the requested object does not exist.
	ErrObjectNotExist = errors.New("object does not exist")
	// ErrObjectExists is returned by ObjectStore.Create when an object already exists under that name.
	ErrObjectExists = errors.New("object already exists")
)

// MetadataSourceSHA256 is the object metadata key holding the hex SHA-256 of the original (uncompressed)
// file the object was made from. It identifies an already uploaded file even if compressing it again would
// not give the same bytes.
const MetadataSourceSHA256 = "source-sha256"

// ObjectAttrs are the attributes of a stored object used to compare it with a local file.
type ObjectAttrs struct {
	Size int64
	// CRC32C is the object's CRC32C (Castagnoli) checksum.
	CRC32C uint32
	// Metadata is the object's custom metadata (see MetadataSourceSHA256).
	Metadata map[string]string
}

// ObjectStore is the minimal write-once object store the archive needs. Names are full object names
// (Location.ObjectName).
type ObjectStore interface {
	// Attrs returns the attributes of an object, or ErrObjectNotExist.
	Attrs(ctx context.Context, name string) (ObjectAttrs, error)
	// NewReader opens an object for reading, or returns ErrObjectNotExist.
	NewReader(ctx context.Context, name string) (io.ReadCloser, error)
	// Create stores the content of r as a NEW object with the given custom metadata: it never overwrites,
	// and returns ErrObjectExists when the name is taken. crc32c is the expected checksum of the content,
	// checked by the store.
	Create(ctx context.Context, name string, r io.Reader, crc32c uint32, metadata map[string]string) error
}
