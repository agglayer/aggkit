package runarchive_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive/archivetest"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive/mocks"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

var testLoc = runarchive.Location{Bucket: "bucket", Prefix: "runs/2026-11-24-test"}

type objects = archivetest.Objects

func newStore(t *testing.T, objs *objects) runarchive.ObjectStore {
	t.Helper()
	return archivetest.NewStore(t, objs)
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
}

// runDir builds a small output directory.
func runDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "exit-certificate-signed.json", `{"height":1}`)
	writeFile(t, dir, "step-g-l2bridgesyncerlite.sqlite", "sqlite-content")
	writeFile(t, dir, "recoveries/delta-exits.json", `{"bridge_exits":[]}`)
	return dir
}

func TestParseURI(t *testing.T) {
	loc, err := runarchive.ParseURI("gs://my-bucket/runs/2026-11-24-full/")
	require.NoError(t, err)
	require.Equal(t, runarchive.Location{Bucket: "my-bucket", Prefix: "runs/2026-11-24-full"}, loc)
	require.Equal(t, "gs://my-bucket/runs/2026-11-24-full", loc.String())
	require.Equal(t, "runs/2026-11-24-full/a/b.json", loc.ObjectName("a/b.json"))
	require.Equal(t, filepath.Join("/cache", "my-bucket", "runs", "2026-11-24-full"), runarchive.CacheDir("/cache", loc))

	for _, bad := range []string{
		"", "/local/dir", "s3://b/p", "gs://", "gs://bucket", "gs://bucket/", "gs:///prefix",
		"gs://bucket/a//b", "gs://bucket/../x", "gs://bucket/a/./b",
	} {
		_, err := runarchive.ParseURI(bad)
		require.Error(t, err, bad)
	}
	require.True(t, runarchive.IsURI("gs://b/p"))

	base, err := runarchive.ParseURI("gs://let-archive/runs")
	require.NoError(t, err)
	require.Equal(t, "gs://let-archive/runs/1/0000000042", base.Run(1, 42).String())
	require.False(t, runarchive.IsURI("./output"))
}

func TestCollectFiles(t *testing.T) {
	dir := runDir(t)
	writeFile(t, dir, "step-g-l2bridgesyncerlite.sqlite-wal", "")
	writeFile(t, dir, "step-g-l2bridgesyncerlite.sqlite-shm", "")
	writeFile(t, dir, runarchive.ManifestFile, "{}")
	writeFile(t, dir, "x.json.partial", "")
	writeFile(t, dir, "L1InfoTreeSync.sqlite", "skipped")
	writeFile(t, dir, "cache/other.json", "skipped dir")

	files, err := runarchive.CollectFiles(dir, func(rel string, isDir bool) bool {
		return rel == "L1InfoTreeSync.sqlite" || (isDir && rel == "cache")
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"exit-certificate-signed.json", "recoveries/delta-exits.json", "step-g-l2bridgesyncerlite.sqlite",
	}, files)

	require.NoError(t, os.Symlink(filepath.Join(dir, "exit-certificate-signed.json"), filepath.Join(dir, "link.json")))
	_, err = runarchive.CollectFiles(dir, nil)
	require.ErrorContains(t, err, "not a regular file")
}

func upload(t *testing.T, store runarchive.ObjectStore, dir string) (*runarchive.UploadResult, error) {
	t.Helper()
	files, err := runarchive.CollectFiles(dir, nil)
	require.NoError(t, err)
	return runarchive.Upload(context.Background(), store, testLoc, dir, files, 1, map[string]string{"mode": "full"},
		t.TempDir())
}

func TestUpload(t *testing.T) {
	dir := runDir(t)
	objs := &objects{}
	res, err := upload(t, newStore(t, objs), dir)
	require.NoError(t, err)
	require.Equal(t, 3, res.Uploaded)
	require.False(t, res.AlreadyComplete)
	// the manifest goes last, so a prefix without it is an incomplete upload
	require.Equal(t, testLoc.ObjectName(runarchive.ManifestFile), objs.Creates[len(objs.Creates)-1])
	// Every file is stored compressed as <path>.zst, with the original's sha256 in its metadata.
	object := testLoc.ObjectName("step-g-l2bridgesyncerlite.sqlite.zst")
	require.Equal(t, []byte("sqlite-content"), unzstd(t, objs.Data[object]))
	require.Equal(t, sha256Hex([]byte("sqlite-content")), objs.Metadata[object][runarchive.MetadataSourceSHA256])
	require.NotContains(t, objs.Data, testLoc.ObjectName("step-g-l2bridgesyncerlite.sqlite"))

	var m runarchive.Manifest
	require.NoError(t, json.Unmarshal(objs.Data[testLoc.ObjectName(runarchive.ManifestFile)], &m))
	require.Equal(t, runarchive.ManifestVersion, m.Version)
	require.Equal(t, runarchive.CompressionZstd, m.Compression)
	entry, ok := m.File("step-g-l2bridgesyncerlite.sqlite")
	require.True(t, ok)
	require.Equal(t, "step-g-l2bridgesyncerlite.sqlite.zst", entry.Object)
	require.Equal(t, int64(len("sqlite-content")), entry.Size)
	require.Equal(t, int64(len(objs.Data[object])), entry.ObjectSize)
	require.Equal(t, archivetest.CRC32C(objs.Data[object]), entry.ObjectCRC32C)
	require.Equal(t, "full", m.Info["mode"])
	require.Len(t, m.Files, 3)

	t.Run("same run again is a no-op", func(t *testing.T) {
		creates := len(objs.Creates)
		res, err := upload(t, newStore(t, objs), dir)
		require.NoError(t, err)
		require.True(t, res.AlreadyComplete)
		require.Len(t, objs.Creates, creates)
	})

	t.Run("prefix already holds another run", func(t *testing.T) {
		writeFile(t, dir, "exit-certificate-signed.json", `{"height":2}`)
		_, err := upload(t, newStore(t, objs), dir)
		require.ErrorIs(t, err, runarchive.ErrRunExists)
	})
}

// seeded returns objects holding one object for exit-certificate-signed.json, made from content.
func seeded(t *testing.T, content string) *objects {
	t.Helper()
	name := testLoc.ObjectName("exit-certificate-signed.json.zst")
	return &objects{
		Data:     map[string][]byte{name: zstdBytes(t, []byte(content))},
		Metadata: map[string]map[string]string{name: {runarchive.MetadataSourceSHA256: sha256Hex([]byte(content))}},
	}
}

func TestUploadResumesPartialUpload(t *testing.T) {
	dir := runDir(t)
	// An object of an earlier, interrupted upload of the same file is kept even if compressing it again
	// gave other bytes: it is matched by the original's sha256.
	objs := seeded(t, `{"height":1}`)
	res, err := upload(t, newStore(t, objs), dir)
	require.NoError(t, err)
	require.Equal(t, 2, res.Uploaded)
	require.Equal(t, 1, res.Reused)
	require.Contains(t, objs.Data, testLoc.ObjectName(runarchive.ManifestFile))
}

func TestUploadConflict(t *testing.T) {
	dir := runDir(t)
	objs := seeded(t, `{"height":999}`)
	_, err := upload(t, newStore(t, objs), dir)
	require.ErrorIs(t, err, runarchive.ErrObjectConflict)
	require.NotContains(t, objs.Data, testLoc.ObjectName(runarchive.ManifestFile))
}

func TestUploadRejectsUnsafePath(t *testing.T) {
	_, err := runarchive.Upload(context.Background(), mocks.NewObjectStore(t), testLoc, t.TempDir(),
		[]string{"../escape"}, 1, nil, t.TempDir())
	require.ErrorContains(t, err, "invalid file path")
}

// archived uploads runDir and returns the objects.
func archived(t *testing.T) *objects {
	t.Helper()
	objs := &objects{}
	_, err := upload(t, newStore(t, objs), runDir(t))
	require.NoError(t, err)
	return objs
}

func TestDownload(t *testing.T) {
	objs := archived(t)
	dest := t.TempDir()
	res, err := runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest, nil)
	require.NoError(t, err)
	require.Equal(t, 3, res.Downloaded)
	require.NotEmpty(t, res.ManifestSHA256)
	got, err := os.ReadFile(filepath.Join(dest, "recoveries", "delta-exits.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"bridge_exits":[]}`, string(got))
	require.FileExists(t, filepath.Join(dest, runarchive.ManifestFile))

	t.Run("second download reuses the verified files", func(t *testing.T) {
		res, err := runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest, nil)
		require.NoError(t, err)
		require.Equal(t, 0, res.Downloaded)
		require.Equal(t, 3, res.Reused)
	})

	t.Run("a modified local file is downloaded again and its stale WAL removed", func(t *testing.T) {
		writeFile(t, dest, "step-g-l2bridgesyncerlite.sqlite", "tampered")
		writeFile(t, dest, "step-g-l2bridgesyncerlite.sqlite-wal", "stale")
		res, err := runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest, nil)
		require.NoError(t, err)
		require.Equal(t, 1, res.Downloaded)
		got, err := os.ReadFile(filepath.Join(dest, "step-g-l2bridgesyncerlite.sqlite"))
		require.NoError(t, err)
		require.Equal(t, "sqlite-content", string(got))
		require.NoFileExists(t, filepath.Join(dest, "step-g-l2bridgesyncerlite.sqlite-wal"))
	})

	t.Run("a file the run does not list is refused", func(t *testing.T) {
		writeFile(t, dest, "foreign.json", "{}")
		_, err := runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest, nil)
		require.ErrorIs(t, err, runarchive.ErrCacheDirMismatch)
	})
}

func TestDownloadSelect(t *testing.T) {
	objs := archived(t)
	dest := t.TempDir()
	res, err := runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest,
		func(*runarchive.Manifest) ([]string, error) { return []string{"exit-certificate-signed.json"}, nil })
	require.NoError(t, err)
	require.Equal(t, []string{"exit-certificate-signed.json"}, res.Files)
	require.NoFileExists(t, filepath.Join(dest, "step-g-l2bridgesyncerlite.sqlite"))

	_, err = runarchive.Download(context.Background(), newStore(t, objs), testLoc, t.TempDir(),
		func(*runarchive.Manifest) ([]string, error) { return []string{"missing.json"}, nil })
	require.ErrorContains(t, err, "not in the archived run")
}

func TestDownloadIncompleteRun(t *testing.T) {
	objs := archived(t)
	delete(objs.Data, testLoc.ObjectName(runarchive.ManifestFile))
	_, err := runarchive.Download(context.Background(), newStore(t, objs), testLoc, t.TempDir(), nil)
	require.ErrorIs(t, err, runarchive.ErrIncompleteRun)
}

func TestDownloadChecksumMismatch(t *testing.T) {
	objs := archived(t)
	objs.Data[testLoc.ObjectName("step-g-l2bridgesyncerlite.sqlite.zst")] = []byte("corrupted!!!!!")
	dest := t.TempDir()
	_, err := runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest, nil)
	require.ErrorIs(t, err, runarchive.ErrChecksumMismatch)
	require.NoFileExists(t, filepath.Join(dest, "step-g-l2bridgesyncerlite.sqlite"))
	require.NoFileExists(t, filepath.Join(dest, "step-g-l2bridgesyncerlite.sqlite.partial"))
}

func TestDownloadOtherRunInDir(t *testing.T) {
	objs := archived(t)
	dest := t.TempDir()
	writeFile(t, dest, runarchive.ManifestFile, `{"version":1,"compression":"zstd","files":[]}`)
	_, err := runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest, nil)
	require.ErrorIs(t, err, runarchive.ErrCacheDirMismatch)
}

func TestDownloadRejectsTamperedManifest(t *testing.T) {
	sha := fmt.Sprintf("%064x", 0)
	file := func(path, object string) string {
		return fmt.Sprintf(`{"path":%q,"size":1,"sha256":%q,"object":%q,"objectSize":1,"objectCrc32c":0}`,
			path, sha, object)
	}
	manifest := func(head string, files ...string) string {
		return `{` + head + `,"files":[` + strings.Join(files, ",") + `]}`
	}
	const v1 = `"version":1,"compression":"zstd"`
	for name, tc := range map[string]struct{ manifest, want string }{
		"path escape":         {manifest(v1, file("../x", "../x.zst")), "invalid file path"},
		"absolute path":       {manifest(v1, file("/etc/x", "/etc/x.zst")), "invalid file path"},
		"duplicated file":     {manifest(v1, file("a", "a.zst"), file("a", "a.zst")), "duplicated file"},
		"unknown version":     {manifest(`"version":2,"compression":"zstd"`, file("a", "a.zst")), "manifest version 2"},
		"unknown compression": {manifest(`"version":1,"compression":"gzip"`, file("a", "a.zst")), "unsupported compression"},
		"unknown field":       {manifest(v1+`,"extra":true`, file("a", "a.zst")), "unknown field"},
		"object not derived":  {manifest(v1, file("a", "../b.zst")), "object of"},
	} {
		t.Run(name, func(t *testing.T) {
			objs := &objects{Data: map[string][]byte{
				testLoc.ObjectName(runarchive.ManifestFile): []byte(tc.manifest),
			}}
			_, err := runarchive.Download(context.Background(), newStore(t, objs), testLoc, t.TempDir(), nil)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func zstdBytes(t *testing.T, d []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	defer enc.Close()
	return enc.EncodeAll(d, nil)
}

func unzstd(t *testing.T, d []byte) []byte {
	t.Helper()
	dec, err := zstd.NewReader(nil)
	require.NoError(t, err)
	defer dec.Close()
	out, err := dec.DecodeAll(d, nil)
	require.NoError(t, err)
	return out
}

func sha256Hex(d []byte) string {
	sum := sha256.Sum256(d)
	return hex.EncodeToString(sum[:])
}
