package runarchive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
)

// gcsChunkSize is the size of each chunk of a resumable upload: a failed chunk is retried, not the
// whole (multi-GB) object.
const gcsChunkSize = 32 << 20 // 32 MiB

// GCSStore is an ObjectStore backed by a Google Cloud Storage bucket.
type GCSStore struct {
	client *storage.Client
	bucket *storage.BucketHandle
}

// NewGCSStore opens a bucket with Application Default Credentials (gcloud auth application-default login,
// a workload identity or GOOGLE_APPLICATION_CREDENTIALS). STORAGE_EMULATOR_HOST, when set, points the
// client at an emulator instead. Close it when done.
func NewGCSStore(ctx context.Context, bucket string) (*GCSStore, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create GCS client (Application Default Credentials): %w", err)
	}
	return &GCSStore{client: client, bucket: client.Bucket(bucket)}, nil
}

// Open opens the GCS bucket as an ObjectStore and returns it with its close function.
func Open(ctx context.Context, bucket string) (ObjectStore, func() error, error) {
	store, err := NewGCSStore(ctx, bucket)
	if err != nil {
		return nil, nil, err
	}
	return store, store.Close, nil
}

// Close releases the client.
func (s *GCSStore) Close() error {
	return s.client.Close()
}

// Attrs implements ObjectStore.
func (s *GCSStore) Attrs(ctx context.Context, name string) (ObjectAttrs, error) {
	attrs, err := s.bucket.Object(name).Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return ObjectAttrs{}, fmt.Errorf("%s: %w", name, ErrObjectNotExist)
		}
		return ObjectAttrs{}, fmt.Errorf("stat %s: %w", name, err)
	}
	return ObjectAttrs{Size: attrs.Size, CRC32C: attrs.CRC32C, Metadata: attrs.Metadata}, nil
}

// NewReader implements ObjectStore. The client verifies the CRC32C of a full read.
func (s *GCSStore) NewReader(ctx context.Context, name string) (io.ReadCloser, error) {
	r, err := s.bucket.Object(name).NewReader(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, fmt.Errorf("%s: %w", name, ErrObjectNotExist)
		}
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return r, nil
}

// Create implements ObjectStore with a DoesNotExist precondition, so an existing object is never replaced
// (and the upload is idempotent for the client's retries).
func (s *GCSStore) Create(
	ctx context.Context, name string, r io.Reader, crc32c uint32, metadata map[string]string,
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // aborts the upload if we return before Close
	w := s.bucket.Object(name).If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	w.ChunkSize = gcsChunkSize
	w.CRC32C = crc32c
	w.SendCRC32C = true
	w.Metadata = metadata
	// No Content-Encoding on purpose: GCS would then decompress on download (decompressive transcoding)
	// and the bytes read would no longer match the stored CRC32C.
	w.ContentType = contentTypeFor(name)
	if _, err := io.Copy(w, r); err != nil {
		_ = w.Close()
		return mapCreateError(name, err)
	}
	if err := w.Close(); err != nil {
		return mapCreateError(name, err)
	}
	return nil
}

// contentTypeFor is the Content-Type of an archived object: zstd for the compressed data, JSON for the
// manifest.
func contentTypeFor(name string) string {
	if strings.HasSuffix(name, objectSuffix) {
		return "application/zstd"
	}
	return "application/json"
}

func mapCreateError(name string, err error) error {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) && gerr.Code == http.StatusPreconditionFailed {
		return fmt.Errorf("%s: %w", name, ErrObjectExists)
	}
	return fmt.Errorf("upload %s: %w", name, err)
}
