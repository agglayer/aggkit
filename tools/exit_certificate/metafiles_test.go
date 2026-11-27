package exit_certificate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive/archivetest"
	"github.com/stretchr/testify/require"
)

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
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(out, dirMetaFiles, name))
		require.NoError(t, err)
		return string(data)
	}

	// No meta files: nothing is created.
	require.NoError(t, copyMetaFiles(Options{OutputDir: out}))
	require.NoDirExists(t, filepath.Join(out, dirMetaFiles))

	// Copied as is: the upload compresses them, once, like any other file.
	require.NoError(t, copyMetaFiles(Options{OutputDir: out, ArchiveMetaFiles: []string{notes, params}}))
	require.Equal(t, "# run notes", read("notes.md"))
	require.Equal(t, "x = 1", read("params.toml"))

	// A re-run refreshes the copies and drops the ones no longer listed.
	require.NoError(t, os.WriteFile(notes, []byte("# updated"), 0o600))
	require.NoError(t, copyMetaFiles(Options{OutputDir: out, ArchiveMetaFiles: []string{notes}}))
	require.Equal(t, "# updated", read("notes.md"))
	require.NoFileExists(t, filepath.Join(out, dirMetaFiles, "params.toml"))

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
}

// TestUploadArchivesMetaFiles checks that --step upload copies the meta files into metafiles/ and archives
// them compressed once (an already compressed file is compressed again, as any other file).
func TestUploadArchivesMetaFiles(t *testing.T) {
	t.Parallel()
	dir, _ := writePreviousRun(t, fileSignedCertificate, 3, nil)
	srcDir := t.TempDir()
	notes := filepath.Join(srcDir, "notes.md")
	kit := filepath.Join(srcDir, "recoveries.tgz")
	require.NoError(t, os.WriteFile(notes, []byte("# run notes"), 0o600))
	require.NoError(t, os.WriteFile(kit, []byte("tgz bytes"), 0o600))

	objs := &archivetest.Objects{}
	open := archivetest.Opener(t, objs)
	upCfg := &Config{L2NetworkID: deltaTestNetworkID, Options: Options{
		OutputDir: dir, ArchiveURI: testArchiveURI, ArchiveCacheDir: t.TempDir(),
		ArchiveMetaFiles: []string{notes, kit},
	}}
	require.NoError(t, runUpload(context.Background(), upCfg, open))
	require.FileExists(t, filepath.Join(dir, dirMetaFiles, "notes.md"), "copied, uncompressed, by the upload")

	loc, err := runarchive.ParseURI(testRunURI)
	require.NoError(t, err)
	raw, ok := objs.Get(loc.ObjectName(runarchive.ManifestFile))
	require.True(t, ok)
	var m runarchive.Manifest
	require.NoError(t, json.Unmarshal(raw, &m))
	for _, name := range []string{"notes.md", "recoveries.tgz"} {
		entry, ok := m.File(dirMetaFiles + "/" + name)
		require.True(t, ok, "metafiles/%s is part of the archived run", name)
		require.Equal(t, dirMetaFiles+"/"+name+".zst", entry.Object, "compressed once")
	}

	// Once archived, a changed meta file is not copied again: the re-run stays a no-op.
	require.NoError(t, os.WriteFile(notes, []byte("# changed after upload"), 0o600))
	require.NoError(t, runUpload(context.Background(), upCfg, open))
	data, err := os.ReadFile(filepath.Join(dir, dirMetaFiles, "notes.md"))
	require.NoError(t, err)
	require.Equal(t, "# run notes", string(data))
}
