package runarchive_test

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

const (
	sqliteFile       = "step-g-l2bridgesyncerlite.sqlite"
	holdSQLiteEnvVar = "RUNARCHIVE_TEST_HOLD_SQLITE"
)

// sqliteRunDir builds an output directory holding a real SQLite database in WAL mode, closed (no side files).
func sqliteRunDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(dir, sqliteFile))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		"PRAGMA journal_mode=WAL",
		"CREATE TABLE bridge (deposit_count INTEGER PRIMARY KEY)",
		"INSERT INTO bridge (deposit_count) VALUES (0)",
	} {
		_, err := db.Exec(stmt)
		require.NoError(t, err)
	}
	require.NoError(t, db.Close())
	require.NoFileExists(t, filepath.Join(dir, sqliteFile+"-wal"))
	return dir
}

func TestDownloadStaleSQLiteSideFiles(t *testing.T) {
	objs := &objects{}
	_, err := upload(t, newStore(t, objs), sqliteRunDir(t))
	require.NoError(t, err)
	dest := t.TempDir()
	_, err = runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest, nil)
	require.NoError(t, err)
	db := filepath.Join(dest, sqliteFile)

	// A verified database is reused, but the side files a crashed process left next to it are removed:
	// SQLite would replay them onto it.
	for _, s := range []string{"-wal", "-shm", "-journal"} {
		writeFile(t, dest, sqliteFile+s, "stale")
	}
	res, err := runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest, nil)
	require.NoError(t, err)
	require.Equal(t, 0, res.Downloaded)
	require.Equal(t, 1, res.Reused)
	for _, s := range []string{"-wal", "-shm", "-journal"} {
		require.NoFileExists(t, db+s)
	}
}

func TestDownloadKeepsSideFilesOfDatabaseInUse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-use detection relies on fcntl locks")
	}
	objs := &objects{}
	_, err := upload(t, newStore(t, objs), sqliteRunDir(t))
	require.NoError(t, err)
	dest := t.TempDir()
	_, err = runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest, nil)
	require.NoError(t, err)
	db := filepath.Join(dest, sqliteFile)

	// Another process opens the downloaded database and writes to it: the change sits in its WAL, the main
	// file still matches the archive.
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldSQLite$")
	cmd.Env = append(os.Environ(), holdSQLiteEnvVar+"="+db)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ready\n", line)
	require.FileExists(t, db+"-wal")

	_, err = runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest, nil)
	require.ErrorIs(t, err, runarchive.ErrFileInUse)
	require.FileExists(t, db+"-wal")

	// Once it closes the database (checkpointing its write into the main file), the copy no longer matches
	// and is downloaded again.
	require.NoError(t, stdin.Close())
	go func() { _, _ = io.Copy(io.Discard, stdout) }()
	require.NoError(t, cmd.Wait())
	res, err := runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest, nil)
	require.NoError(t, err)
	require.Equal(t, 1, res.Downloaded)
}

// TestHelperHoldSQLite is not a test: TestDownloadKeepsSideFilesOfDatabaseInUse runs it in a child process to
// hold the database open, with a write in its WAL, until its stdin is closed.
func TestHelperHoldSQLite(t *testing.T) {
	path := os.Getenv(holdSQLiteEnvVar)
	if path == "" {
		t.Skip("helper process for TestDownloadKeepsSideFilesOfDatabaseInUse")
	}
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_journal_mode=WAL", path))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	_, err = db.Exec("INSERT INTO bridge (deposit_count) VALUES (1)")
	require.NoError(t, err)
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
	require.NoError(t, db.Close())
}
