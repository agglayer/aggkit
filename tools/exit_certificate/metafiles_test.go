package exit_certificate

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive/archivetest"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

// readMetaFile decompresses a metafiles/<name>.zst copy.
func readMetaFile(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	dec, err := zstd.NewReader(f)
	require.NoError(t, err)
	defer dec.Close()
	data, err := io.ReadAll(dec)
	require.NoError(t, err)
	return string(data)
}

func TestArchiveMetaFilesConfig(t *testing.T) {
	t.Parallel()
	base := `"l2RpcUrl":"http://x","l2BridgeAddress":"0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",` +
		`"targetBlock":"100","exitAddress":"0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe"`
	load := func(t *testing.T, opts string) (*Config, error) {
		t.Helper()
		p := filepath.Join(t.TempDir(), "cfg.json")
		content := `{` + base + `,"options":{"useAgglayerAdminToStepFCheck":false` + opts + `}}`
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
		return LoadConfig(p)
	}

	cfg, err := load(t, `,"archiveMetaFiles":["notes.md","/abs/params.toml"]`)
	require.NoError(t, err)
	require.Len(t, cfg.Options.ArchiveMetaFiles, 2)
	require.True(t, filepath.IsAbs(cfg.Options.ArchiveMetaFiles[0]), "relative paths resolve from the config dir")
	require.Equal(t, "notes.md", filepath.Base(cfg.Options.ArchiveMetaFiles[0]))
	require.Equal(t, "/abs/params.toml", cfg.Options.ArchiveMetaFiles[1])

	_, err = load(t, `,"archiveMetaFiles":["a/notes.md","b/notes.md"]`)
	require.ErrorContains(t, err, "same file name")
	_, err = load(t, `,"archiveMetaFiles":[""]`)
	require.ErrorContains(t, err, "empty path")
}

func TestCopyMetaFiles(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	notes := filepath.Join(src, "notes.md")
	params := filepath.Join(src, "params.toml")
	require.NoError(t, os.WriteFile(notes, []byte("# run notes"), 0o600))
	require.NoError(t, os.WriteFile(params, []byte("x = 1"), 0o600))
	out := t.TempDir()

	// No meta files: nothing is created.
	require.NoError(t, copyMetaFiles(Options{OutputDir: out}))
	require.NoDirExists(t, filepath.Join(out, dirMetaFiles))

	require.NoError(t, copyMetaFiles(Options{OutputDir: out, ArchiveMetaFiles: []string{notes, params}}))
	require.Equal(t, "# run notes", readMetaFile(t, filepath.Join(out, dirMetaFiles, "notes.md.zst")))
	require.Equal(t, "x = 1", readMetaFile(t, filepath.Join(out, dirMetaFiles, "params.toml.zst")))

	// A re-run refreshes the copies and drops the ones no longer listed.
	require.NoError(t, os.WriteFile(notes, []byte("# updated"), 0o600))
	require.NoError(t, copyMetaFiles(Options{OutputDir: out, ArchiveMetaFiles: []string{notes}}))
	require.Equal(t, "# updated", readMetaFile(t, filepath.Join(out, dirMetaFiles, "notes.md.zst")))
	require.NoFileExists(t, filepath.Join(out, dirMetaFiles, "params.toml.zst"))

	// Missing files and directories are errors; no .partial is left behind.
	err := copyMetaFiles(Options{OutputDir: out, ArchiveMetaFiles: []string{filepath.Join(src, "missing")}})
	require.ErrorContains(t, err, "options.archiveMetaFiles")
	err = copyMetaFiles(Options{OutputDir: out, ArchiveMetaFiles: []string{src}})
	require.ErrorContains(t, err, "not a regular file")
	entries, err := os.ReadDir(filepath.Join(out, dirMetaFiles))
	require.NoError(t, err)
	for _, e := range entries {
		require.NotContains(t, e.Name(), ".partial")
	}

	// An already archived output dir is left untouched (the archive is write-once).
	require.NoError(t, os.WriteFile(filepath.Join(out, runarchive.ManifestFile), []byte("{}"), 0o600))
	require.NoError(t, os.WriteFile(notes, []byte("# after upload"), 0o600))
	require.NoError(t, copyMetaFiles(Options{OutputDir: out, ArchiveMetaFiles: []string{notes}}))
	require.Equal(t, "# updated", readMetaFile(t, filepath.Join(out, dirMetaFiles, "notes.md.zst")))
}

// TestUploadArchivesMetaFiles checks that the metafiles/ copies are archived with the run.
func TestUploadArchivesMetaFiles(t *testing.T) {
	t.Parallel()
	dir, _ := writePreviousRun(t, fileSignedCertificate, 3, nil)
	notes := filepath.Join(t.TempDir(), "notes.md")
	require.NoError(t, os.WriteFile(notes, []byte("# run notes"), 0o600))

	objs := &archivetest.Objects{}
	upCfg := &Config{L2NetworkID: deltaTestNetworkID, Options: Options{
		OutputDir: dir, ArchiveURI: testArchiveURI, ArchiveCacheDir: t.TempDir(),
		ArchiveMetaFiles: []string{notes},
	}}
	require.NoError(t, copyMetaFiles(upCfg.Options))
	require.NoError(t, runUpload(context.Background(), upCfg, archivetest.Opener(t, objs)))

	loc, err := runarchive.ParseURI(testRunURI)
	require.NoError(t, err)
	raw, ok := objs.Get(loc.ObjectName(runarchive.ManifestFile))
	require.True(t, ok)
	var m runarchive.Manifest
	require.NoError(t, json.Unmarshal(raw, &m))
	_, ok = m.File(dirMetaFiles + "/notes.md" + metaFileExt)
	require.True(t, ok, "metafiles/ is part of the archived run")
}
