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

	res, err := runarchive.Upload(ctx, store, loc, dir, files, map[string]string{"mode": "full"})
	require.NoError(t, err)
	require.Equal(t, 2, res.Uploaded)

	// Write-once: a second Create of the same name is refused, the object is unchanged.
	err = store.Create(ctx, loc.ObjectName("exit-certificate-signed.json"), bytes.NewReader([]byte("other")),
		archivetest.CRC32C([]byte("other")))
	require.ErrorIs(t, err, runarchive.ErrObjectExists)

	res, err = runarchive.Upload(ctx, store, loc, dir, files, nil)
	require.NoError(t, err)
	require.True(t, res.AlreadyComplete)

	dest := t.TempDir()
	dl, err := runarchive.Download(ctx, store, loc, dest, nil)
	require.NoError(t, err)
	require.Equal(t, 2, dl.Downloaded)
	got, err := os.ReadFile(filepath.Join(dest, "step-g-l2bridgesyncerlite.sqlite"))
	require.NoError(t, err)
	require.True(t, bytes.Equal(big, got))
}
