package runarchive_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive/archivetest"
	"github.com/stretchr/testify/require"
)

// TestGCSStoreEmulator exercises the real GCS client against an emulator (fake-gcs-server). It only runs when
// STORAGE_EMULATOR_HOST is set, e.g.:
//
//	docker run -d --rm -p 127.0.0.1:4443:4443 fsouza/fake-gcs-server:1.52.2 -scheme http -port 4443 \
//	  -public-host 127.0.0.1:4443 -backend memory
//	STORAGE_EMULATOR_HOST=127.0.0.1:4443 go test -run TestGCSStoreEmulator ./tools/exit_certificate/runarchive/
func TestGCSStoreEmulator(t *testing.T) {
	if os.Getenv("STORAGE_EMULATOR_HOST") == "" {
		t.Skip("STORAGE_EMULATOR_HOST not set")
	}
	ctx := context.Background()
	bucket := fmt.Sprintf("let-archive-%d", time.Now().UnixNano())
	client, err := storage.NewClient(ctx)
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.Bucket(bucket).Create(ctx, "test-project", nil))

	store, closeStore, err := runarchive.Open(ctx, bucket)
	require.NoError(t, err)
	defer func() { require.NoError(t, closeStore()) }()

	_, err = store.Attrs(ctx, "missing")
	require.ErrorIs(t, err, runarchive.ErrObjectNotExist)
	_, err = store.NewReader(ctx, "missing")
	require.ErrorIs(t, err, runarchive.ErrObjectNotExist)

	// A run with a file bigger than one resumable-upload chunk.
	dir := t.TempDir()
	big := make([]byte, 40<<20)
	_, err = rand.Read(big)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "step-g-l2bridgesyncerlite.sqlite"), big, 0o600))
	writeFile(t, dir, "exit-certificate-signed.json", `{"height":1}`)
	loc := runarchive.Location{Bucket: bucket, Prefix: "runs/2026-11-24-emulator"}
	files, err := runarchive.CollectFiles(dir, nil)
	require.NoError(t, err)

	res, err := runarchive.Upload(ctx, store, loc, dir, files, 1, map[string]string{"mode": "full"}, t.TempDir())
	require.NoError(t, err)
	require.Equal(t, 2, res.Uploaded)

	// The object is compressed and carries the original's sha256 as metadata (what a resumed upload matches).
	entry, ok := res.Manifest.File("step-g-l2bridgesyncerlite.sqlite")
	require.True(t, ok)
	attrs, err := store.Attrs(ctx, loc.ObjectName(entry.Object))
	require.NoError(t, err)
	require.Equal(t, entry.SHA256, attrs.Metadata[runarchive.MetadataSourceSHA256])
	require.Equal(t, entry.ObjectSize, attrs.Size)
	require.Equal(t, entry.ObjectCRC32C, attrs.CRC32C)

	// Write-once: a second Create of the same name is refused, the object is unchanged.
	err = store.Create(ctx, loc.ObjectName("exit-certificate-signed.json.zst"), bytes.NewReader([]byte("other")),
		archivetest.CRC32C([]byte("other")), nil)
	require.ErrorIs(t, err, runarchive.ErrObjectExists)

	res, err = runarchive.Upload(ctx, store, loc, dir, files, 1, nil, t.TempDir())
	require.NoError(t, err)
	require.True(t, res.AlreadyComplete)

	dest := t.TempDir()
	dl, err := runarchive.Download(ctx, store, loc, dest, nil)
	require.NoError(t, err)
	require.Equal(t, 2, dl.Downloaded)
	got, err := os.ReadFile(filepath.Join(dest, "step-g-l2bridgesyncerlite.sqlite"))
	require.NoError(t, err)
	require.True(t, bytes.Equal(big, got))

	// Looking for the run of a height is one read of its manifest: no listing of the bucket.
	base := runarchive.Location{Bucket: bucket, Prefix: "runs"}
	_, err = runarchive.ReadManifest(ctx, store, base.Run(1, 5))
	require.ErrorIs(t, err, runarchive.ErrIncompleteRun, "nothing archived at that height")
	small := t.TempDir()
	writeFile(t, small, "exit-certificate-signed.json", `{"height":5}`)
	// An upload that did not complete: a file of the run without its manifest.
	require.NoError(t, store.Create(ctx, base.Run(1, 5).ObjectName("exit-certificate-signed.json.zst"),
		bytes.NewReader([]byte("partial")), archivetest.CRC32C([]byte("partial")), nil))
	_, err = runarchive.ReadManifest(ctx, store, base.Run(1, 5))
	require.ErrorIs(t, err, runarchive.ErrIncompleteRun, "a prefix without manifest is not a run")
	_, err = runarchive.Upload(ctx, store, base.Run(1, 6), small, []string{"exit-certificate-signed.json"}, 2, nil,
		t.TempDir())
	require.NoError(t, err)
	m, err := runarchive.ReadManifest(ctx, store, base.Run(1, 6))
	require.NoError(t, err)
	require.Equal(t, 2, m.DataVersion)
	require.ErrorIs(t, runarchive.CheckDataVersion(m, runarchive.VersionRange{Min: 1, Max: 1}),
		runarchive.ErrUnsupportedVersion)
}
