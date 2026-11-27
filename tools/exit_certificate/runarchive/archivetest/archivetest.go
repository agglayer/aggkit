// Package archivetest holds test helpers for code that uses a runarchive.ObjectStore.
package archivetest

import (
	"bytes"
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"sync"
	"testing"

	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive/mocks"
	"github.com/stretchr/testify/mock"
)

// Objects is the content behind a store built by NewStore: object name → bytes, plus the names created
// through the store, in order.
type Objects struct {
	mu       sync.Mutex
	Data     map[string][]byte
	Metadata map[string]map[string]string
	Creates  []string
}

// Get returns the content of an object.
func (o *Objects) Get(name string) ([]byte, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	d, ok := o.Data[name]
	return d, ok
}

// NewStore returns an ObjectStore mock whose calls read and write objs, with the write-once semantics of
// the real store (Create never overwrites) and its CRC32C check on upload.
func NewStore(t *testing.T, objs *Objects) *mocks.ObjectStore {
	t.Helper()
	if objs.Data == nil {
		objs.Data = map[string][]byte{}
	}
	if objs.Metadata == nil {
		objs.Metadata = map[string]map[string]string{}
	}
	store := mocks.NewObjectStore(t)
	store.EXPECT().Attrs(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, name string) (runarchive.ObjectAttrs, error) {
			d, ok := objs.Get(name)
			if !ok {
				return runarchive.ObjectAttrs{}, runarchive.ErrObjectNotExist
			}
			objs.mu.Lock()
			defer objs.mu.Unlock()
			return runarchive.ObjectAttrs{Size: int64(len(d)), CRC32C: CRC32C(d), Metadata: objs.Metadata[name]}, nil
		}).Maybe()
	store.EXPECT().NewReader(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, name string) (io.ReadCloser, error) {
			d, ok := objs.Get(name)
			if !ok {
				return nil, runarchive.ErrObjectNotExist
			}
			return io.NopCloser(bytes.NewReader(d)), nil
		}).Maybe()
	store.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, name string, r io.Reader, crc uint32, metadata map[string]string) error {
			d, err := io.ReadAll(r)
			if err != nil {
				return err
			}
			if CRC32C(d) != crc {
				return fmt.Errorf("crc32c mismatch for %s", name)
			}
			objs.mu.Lock()
			defer objs.mu.Unlock()
			if _, ok := objs.Data[name]; ok {
				return runarchive.ErrObjectExists
			}
			objs.Data[name] = d
			objs.Metadata[name] = metadata
			objs.Creates = append(objs.Creates, name)
			return nil
		}).Maybe()
	return store
}

// Opener returns an open function (the signature of runarchive.Open) that hands out stores backed by objs.
func Opener(t *testing.T, objs *Objects) func(context.Context, string) (runarchive.ObjectStore, func() error, error) {
	t.Helper()
	return func(context.Context, string) (runarchive.ObjectStore, func() error, error) {
		return NewStore(t, objs), func() error { return nil }, nil
	}
}

// CRC32C returns the CRC32C (Castagnoli) checksum of d.
func CRC32C(d []byte) uint32 {
	return crc32.Checksum(d, crc32.MakeTable(crc32.Castagnoli))
}
