package claimer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/log"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive/archivetest"
	"github.com/stretchr/testify/require"
)

const testRunSource = "gs://let-archive/runs/1/0000000005"

func TestLoadConfigRunSource(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, "config.toml", `
runSource = "`+testRunSource+`"
l1InfoTreeDBPath = "L1InfoTreeSync.sqlite"
`)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, testRunSource, cfg.RunSource)
	// runDataDir defaults to archive-cache next to the config.
	require.Equal(t, filepath.Join(filepath.Dir(path), defaultRunDataDir), cfg.RunDataDir)
	require.Empty(t, cfg.SignedCertificatePath, "filled in by FetchRunSource")

	_, err = LoadConfig(writeConfig(t, "config.toml", `
runSource = "`+testRunSource+`"
signedCertificatePath = "exit-certificate-signed.json"
l1InfoTreeDBPath = "L1InfoTreeSync.sqlite"
`))
	require.ErrorContains(t, err, "leave signedCertificatePath")

	_, err = LoadConfig(writeConfig(t, "config.toml", `
runSource = "gs://bucket"
l1InfoTreeDBPath = "L1InfoTreeSync.sqlite"
`))
	require.ErrorContains(t, err, "invalid runSource")

	_, err = LoadConfig(writeConfig(t, "config.toml", `
runSource = "`+testRunSource+`"
`))
	require.ErrorContains(t, err, "l1InfoTreeDBPath is required")
}

// archiveRun uploads the files (name → content) as the run at testRunSource and returns the objects.
func archiveRun(t *testing.T, files map[string]string) *archivetest.Objects {
	t.Helper()
	dir := t.TempDir()
	rels := make([]string, 0, len(files))
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
		rels = append(rels, name)
	}
	loc, err := runarchive.ParseURI(testRunSource)
	require.NoError(t, err)
	objs := &archivetest.Objects{}
	_, err = runarchive.Upload(context.Background(), archivetest.NewStore(t, objs), loc, dir, rels, 1, nil, t.TempDir())
	require.NoError(t, err)
	return objs
}

func TestFetchRunSource(t *testing.T) {
	t.Parallel()
	logger := log.WithFields("test", t.Name())

	t.Run("delta run: full certificate and pending bridges", func(t *testing.T) {
		t.Parallel()
		objs := archiveRun(t, map[string]string{
			signedCertificateFile:                  "signed",
			fullCertificateFile:                    "full",
			localExitTreeDBFile:                    "db",
			stepWaitResultFile:                     "wait",
			exitcertificate.FileStepPendingBridges: "pending",
			"step-b-eoa-balances.json":             "not needed by the claimer",
		})
		cfg := &Config{RunSource: testRunSource, RunDataDir: t.TempDir(), L1InfoTreeDBPath: "l1.sqlite"}
		require.NoError(t, fetchRunSource(context.Background(), cfg, logger, archivetest.Opener(t, objs)))

		loc, err := runarchive.ParseURI(testRunSource)
		require.NoError(t, err)
		dir := runarchive.CacheDir(cfg.RunDataDir, loc)
		require.Equal(t, filepath.Join(dir, fullCertificateFile), cfg.SignedCertificatePath)
		require.Equal(t, filepath.Join(dir, localExitTreeDBFile), cfg.LocalExitTreeDBPath)
		require.Equal(t, filepath.Join(dir, stepWaitResultFile), cfg.StepWaitResultPath)
		require.Equal(t, filepath.Join(dir, exitcertificate.FileStepPendingBridges), cfg.PendingBridgesPath)
		require.FileExists(t, cfg.SignedCertificatePath)
		require.NoFileExists(t, filepath.Join(dir, signedCertificateFile))
		require.NoFileExists(t, filepath.Join(dir, "step-b-eoa-balances.json"))
	})

	t.Run("regular run: signed certificate, no pending bridges", func(t *testing.T) {
		t.Parallel()
		objs := archiveRun(t, map[string]string{
			signedCertificateFile: "signed", localExitTreeDBFile: "db", stepWaitResultFile: "wait",
		})
		cfg := &Config{RunSource: testRunSource, RunDataDir: t.TempDir(), L1InfoTreeDBPath: "l1.sqlite"}
		require.NoError(t, fetchRunSource(context.Background(), cfg, logger, archivetest.Opener(t, objs)))
		require.Equal(t, signedCertificateFile, filepath.Base(cfg.SignedCertificatePath))
		require.Empty(t, cfg.PendingBridgesPath)
	})

	t.Run("archived run without the local exit tree", func(t *testing.T) {
		t.Parallel()
		objs := archiveRun(t, map[string]string{signedCertificateFile: "signed", stepWaitResultFile: "wait"})
		cfg := &Config{RunSource: testRunSource, RunDataDir: t.TempDir(), L1InfoTreeDBPath: "l1.sqlite"}
		err := fetchRunSource(context.Background(), cfg, logger, archivetest.Opener(t, objs))
		require.ErrorContains(t, err, localExitTreeDBFile)
	})

	t.Run("nothing archived at the prefix", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{RunSource: testRunSource, RunDataDir: t.TempDir(), L1InfoTreeDBPath: "l1.sqlite"}
		err := fetchRunSource(context.Background(), cfg, logger, archivetest.Opener(t, &archivetest.Objects{}))
		require.ErrorIs(t, err, runarchive.ErrIncompleteRun)
	})
}

func TestUseRunSource(t *testing.T) {
	t.Parallel()
	ec := &exitcertificate.Config{Options: exitcertificate.Options{OutputDir: "/runs/output-delta-1"}}
	cfg := &Config{
		SignedCertificatePath: "/runs/output-delta-1/exit-certificate-full.json",
		LocalExitTreeDBPath:   "/runs/output-delta-1/" + localExitTreeDBFile,
		StepWaitResultPath:    "/runs/output-delta-1/" + stepWaitResultFile,
		PendingBridgesPath:    "/runs/output-delta-1/" + exitcertificate.FileStepPendingBridges,
		L1InfoTreeDBPath:      "/runs/output-delta-1/" + exitcertificate.FileClaimerL1InfoTreeDB,
	}
	require.NoError(t, cfg.UseRunSource(testRunSource, ec))
	require.Equal(t, testRunSource, cfg.RunSource)
	require.Equal(t, "/runs/archive-cache", cfg.RunDataDir)
	require.Empty(t, cfg.SignedCertificatePath)
	require.Empty(t, cfg.PendingBridgesPath)
	require.Equal(t, "/runs/output-delta-1/"+exitcertificate.FileClaimerL1InfoTreeDB, cfg.L1InfoTreeDBPath)

	require.ErrorContains(t, cfg.UseRunSource("gs://bucket", ec), "--run-source")
}
