package exit_certificate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive/archivetest"
	"github.com/stretchr/testify/require"
)

const (
	// testArchiveURI is the archive base (options.archiveURI); a run lives at <base>/<network>/<height>.
	testArchiveURI = "gs://let-archive/runs"
	// testRunURI is the run writePreviousRun's certificate (network 7, height 3) is archived at.
	testRunURI = testArchiveURI + "/7/0000000003"
)

func TestArchiveConfig(t *testing.T) {
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

	cfg, err := load(t, `,"archiveURI":"`+testArchiveURI+`","archiveCacheDir":"cache"`)
	require.NoError(t, err)
	require.Equal(t, testArchiveURI, cfg.Options.ArchiveURI)
	require.True(t, filepath.IsAbs(cfg.Options.ArchiveCacheDir))
	require.Equal(t, "cache", filepath.Base(cfg.Options.ArchiveCacheDir))

	_, err = load(t, `,"archiveURI":"gs://bucket-only"`)
	require.ErrorContains(t, err, "invalid options.archiveURI")
	_, err = load(t, `,"archiveURI":"/local/dir"`)
	require.ErrorContains(t, err, "invalid options.archiveURI")

	// A gs:// previousRun is kept as is (not resolved as a path) and validated.
	cfg, err = load(t, `,"deltaMode":true,"previousRun":"`+testRunURI+`","deltaExitsFile":"exits.json"`)
	require.NoError(t, err)
	require.Equal(t, testRunURI, cfg.Options.PreviousRun)
	_, err = load(t, `,"deltaMode":true,"previousRun":"gs://bucket/","deltaExitsFile":"exits.json"`)
	require.ErrorContains(t, err, "invalid options.previousRun")

	// The default cache sits next to the output dir, not inside it.
	require.Equal(t, filepath.Join("/runs", defaultArchiveCacheDirName),
		ArchiveCacheDir(Options{OutputDir: "/runs/output-1"}))
	require.Equal(t, "/elsewhere", ArchiveCacheDir(Options{OutputDir: "/runs/output-1", ArchiveCacheDir: "/elsewhere"}))
}

func TestSelectPreviousRunFiles(t *testing.T) {
	t.Parallel()
	manifest := func(paths ...string) *runarchive.Manifest {
		m := &runarchive.Manifest{}
		for _, p := range paths {
			m.Files = append(m.Files, runarchive.FileEntry{Path: p})
		}
		return m
	}

	files, err := selectPreviousRunFiles(manifest(fileSignedCertificate, fileFullCertificate, fileStepWaitResult,
		fileStepGLiteDB, fileDeltaManifest, fileStepCSCLockedValues, "recoveries/delta-exits.json",
		fileStepBEOABalances, FileClaimerL1InfoTreeDB))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{fileFullCertificate, fileStepWaitResult, fileStepGLiteDB, fileDeltaManifest,
		fileStepCSCLockedValues, "recoveries/delta-exits.json"}, files)

	files, err = selectPreviousRunFiles(manifest(fileSignedCertificate, fileStepWaitResult, fileStepGLiteDB,
		fileStep0LBT, fileStepFChecks))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{fileSignedCertificate, fileStepWaitResult, fileStepGLiteDB, fileStep0LBT,
		fileStepFChecks}, files)

	_, err = selectPreviousRunFiles(manifest(fileSignedCertificate, fileStepWaitResult))
	require.ErrorContains(t, err, fileStepGLiteDB)
}

// TestArchiveRoundTrip archives a settled run with --step upload and uses the archive as the previousRun of
// a delta: loadPreviousRun reads the downloaded copy exactly as it would read the original directory.
func TestArchiveRoundTrip(t *testing.T) {
	t.Parallel()
	dir, root := writePreviousRun(t, fileSignedCertificate, 3, nil)
	// Files that must not be archived: the claimer's L1 Info Tree DBs and SQLite side files.
	require.NoError(t, os.WriteFile(filepath.Join(dir, FileClaimerL1InfoTreeDB), []byte("l1"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, FileClaimerMultidownloadDB), []byte("md"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, FileClaimerL1InfoTreeDB+"-shm"), nil, 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, dirRecoveries), 0o755))
	require.NoError(t, saveJSON(filepath.Join(dir, dirRecoveries), fileRecoveriesDelta, map[string]any{}))

	objs := &archivetest.Objects{}
	open := archivetest.Opener(t, objs)
	upCfg := &Config{L2NetworkID: deltaTestNetworkID, Options: Options{
		OutputDir: dir, ArchiveURI: testArchiveURI, ArchiveCacheDir: t.TempDir(),
	}}
	require.NoError(t, runUpload(context.Background(), upCfg, open))

	// The run goes to <base>/<network>/<height>: the certificate's network 7 and height 3.
	loc, err := runarchive.ParseURI(testRunURI)
	require.NoError(t, err)
	raw, ok := objs.Get(loc.ObjectName(runarchive.ManifestFile))
	require.True(t, ok)
	var m runarchive.Manifest
	require.NoError(t, json.Unmarshal(raw, &m))
	paths := make([]string, 0, len(m.Files))
	for _, f := range m.Files {
		paths = append(paths, f.Path)
	}
	require.ElementsMatch(t, []string{fileSignedCertificate, fileStepWaitResult, fileStepGLiteDB,
		dirRecoveries + "/" + fileRecoveriesDelta}, paths)
	require.Equal(t, "full", m.Info["mode"])
	require.Equal(t, "3", m.Info["height"])
	require.Equal(t, root.Hex(), m.Info["newLocalExitRoot"])
	require.Equal(t, runarchive.CompressionZstd, m.Compression)
	_, ok = objs.Get(loc.ObjectName(fileStepGLiteDB + ".zst"))
	require.True(t, ok, "files are stored compressed")
	require.Equal(t, "0x0000000000000000000000000000000000000000000000000000000000000099", m.Info["certificateHash"])
	require.FileExists(t, filepath.Join(dir, runarchive.ManifestFile), "local copy of the archived manifest")

	// Uploading the same output dir again is a no-op (the local manifest copy is not part of the run).
	require.NoError(t, runUpload(context.Background(), upCfg, open))

	cache := t.TempDir()
	deltaCfg := &Config{Options: Options{
		DeltaMode: true, PreviousRun: testRunURI, OutputDir: t.TempDir(), ArchiveCacheDir: cache,
	}}
	require.NoError(t, resolvePreviousRun(context.Background(), deltaCfg, open))
	require.Equal(t, runarchive.CacheDir(cache, loc), deltaCfg.Options.PreviousRun)
	require.Equal(t, loc.String(), deltaCfg.Options.PreviousRunArchive)
	require.NotEmpty(t, deltaCfg.Options.PreviousRunArchiveManifestSHA256)

	prev, err := loadPreviousRun(deltaCfg.Options.PreviousRun)
	require.NoError(t, err)
	require.Equal(t, root, prev.Certificate.NewLocalExitRoot)
	stateDir, err := recoveriesStateDir(deltaCfg.Options.PreviousRun)
	require.NoError(t, err)
	require.Equal(t, deltaCfg.Options.PreviousRun, stateDir)

	// A local previousRun is left untouched (nothing to download).
	localCfg := &Config{Options: Options{PreviousRun: dir}}
	require.NoError(t, resolvePreviousRun(context.Background(), localCfg, open))
	require.Equal(t, dir, localCfg.Options.PreviousRun)
}

func TestRunUploadRequiresSettledRun(t *testing.T) {
	t.Parallel()
	open := archivetest.Opener(t, &archivetest.Objects{})

	err := runUpload(context.Background(), &Config{Options: Options{OutputDir: t.TempDir()}}, open)
	require.ErrorContains(t, err, "requires options.archiveURI")

	dir := t.TempDir()
	cfg := &Config{L2NetworkID: deltaTestNetworkID, Options: Options{OutputDir: dir, ArchiveURI: testArchiveURI}}
	err = runUpload(context.Background(), cfg, open)
	require.ErrorContains(t, err, "archives a settled run only")

	require.NoError(t, saveJSON(dir, fileStepWaitResult, &StepWaitResult{FinalStatus: agglayertypes.InError}))
	err = runUpload(context.Background(), cfg, open)
	require.ErrorContains(t, err, "archives a settled run only")

	require.NoError(t, saveJSON(dir, fileStepWaitResult, &StepWaitResult{FinalStatus: agglayertypes.Settled}))
	err = runUpload(context.Background(), cfg, open)
	require.ErrorContains(t, err, fileSignedCertificate+" not found")

	cfg.Options.DeltaMode = true
	err = runUpload(context.Background(), cfg, open)
	require.ErrorContains(t, err, fileFullCertificate+" not found")
}

func TestRunUploadRefusesAnotherRunAtTheSameHeight(t *testing.T) {
	t.Parallel()
	objs := &archivetest.Objects{}
	open := archivetest.Opener(t, objs)
	first, _ := writePreviousRun(t, fileSignedCertificate, 2, nil)
	require.NoError(t, runUpload(context.Background(),
		&Config{L2NetworkID: deltaTestNetworkID, Options: Options{
			OutputDir: first, ArchiveURI: testArchiveURI, ArchiveCacheDir: t.TempDir(),
		}}, open))

	second, _ := writePreviousRun(t, fileSignedCertificate, 5, nil)
	// Same network and height (3): the write-once bucket holds one run per certificate height.
	err := runUpload(context.Background(), &Config{L2NetworkID: deltaTestNetworkID, Options: Options{
		OutputDir: second, ArchiveURI: testArchiveURI, ArchiveCacheDir: t.TempDir(),
	}}, open)
	require.ErrorIs(t, err, runarchive.ErrRunExists)
}

// TestCheckpointSQLiteFiles: a database with pending WAL frames is checkpointed into its main file before
// it is archived (the -wal file itself is never archived).
func TestCheckpointSQLiteFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "data.sqlite")
	writer, err := db.NewSQLiteDB(path)
	require.NoError(t, err)
	_, err = writer.Exec(`CREATE TABLE t (v INTEGER); INSERT INTO t VALUES (42);`)
	require.NoError(t, err)
	// Keep the writer open: its frames stay in data.sqlite-wal until a checkpoint.
	wal, err := os.Stat(path + "-wal")
	require.NoError(t, err)
	require.Positive(t, wal.Size())

	require.NoError(t, checkpointSQLiteFiles(context.Background(), dir, []string{"data.sqlite", "other.json"}))
	wal, err = os.Stat(path + "-wal")
	require.NoError(t, err)
	require.Zero(t, wal.Size())
	require.NoError(t, writer.Close())

	// The main file alone (as archived) holds the data.
	copyPath := filepath.Join(t.TempDir(), "copy.sqlite")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(copyPath, content, 0o600))
	reader, err := db.NewSQLiteDB(copyPath)
	require.NoError(t, err)
	defer reader.Close()
	var v int
	require.NoError(t, reader.QueryRow(`SELECT v FROM t`).Scan(&v))
	require.Equal(t, 42, v)
}

func TestUploadSkip(t *testing.T) {
	t.Parallel()
	skip := uploadSkip(&Config{Options: Options{OutputDir: "/runs/out", ArchiveCacheDir: "/runs/out/cache"}})
	require.True(t, skip("cache", true))
	require.False(t, skip("recoveries", true))
	require.True(t, skip(FileClaimerL1InfoTreeDB, false))
	require.True(t, skip(FileClaimerMultidownloadDB, false))
	require.False(t, skip(fileStepGLiteDB, false))

	// The default cache is outside the output dir: no directory is skipped.
	skip = uploadSkip(&Config{Options: Options{OutputDir: "/runs/out"}})
	require.False(t, skip(defaultArchiveCacheDirName, true))
}

func TestRunDeltaStepsAllowsUpload(t *testing.T) {
	t.Parallel()
	err := runDeltaSteps(context.Background(), &Config{Options: Options{OutputDir: t.TempDir()}}, stepUpload)
	require.ErrorContains(t, err, "requires options.archiveURI")
}

func TestRunCertificateHeader(t *testing.T) {
	t.Parallel()
	dir, root := writePreviousRun(t, fileSignedCertificate, 2, nil)
	cfg := &Config{L2NetworkID: deltaTestNetworkID}

	header, err := runCertificateHeader(cfg, dir, fileSignedCertificate)
	require.NoError(t, err)
	require.Equal(t, certificateHeader{NetworkID: deltaTestNetworkID, Height: 3, NewLocalExitRoot: root}, *header)

	_, err = runCertificateHeader(&Config{L2NetworkID: 99}, dir, fileSignedCertificate)
	require.ErrorContains(t, err, "is for network 7")

	require.NoError(t, saveJSON(dir, fileStepHPreviousLocalExitRoot, &StepHResult{Height: 4}))
	_, err = runCertificateHeader(cfg, dir, fileSignedCertificate)
	require.ErrorContains(t, err, "has height 3 but")

	require.NoError(t, saveJSON(dir, fileStepHPreviousLocalExitRoot, &StepHResult{Height: 3}))
	_, err = runCertificateHeader(cfg, dir, fileSignedCertificate)
	require.NoError(t, err)
}

func TestReadCertificateHeaderStopsBeforeTheExits(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "cert.json")
	// The exits after the header are not valid JSON: reading must stop once the header is complete.
	content := `{"network_id":1,"height":5,"prev_local_exit_root":"0x` + strings.Repeat("11", 32) +
		`","new_local_exit_root":"0x` + strings.Repeat("22", 32) + `","bridge_exits":[NOT JSON`
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	header, err := readCertificateHeader(p)
	require.NoError(t, err)
	require.Equal(t, uint64(5), header.Height)
	require.Equal(t, uint32(1), header.NetworkID)

	require.NoError(t, os.WriteFile(p, []byte(`{"network_id":1}`), 0o600))
	_, err = readCertificateHeader(p)
	require.ErrorContains(t, err, "header incomplete")
}
