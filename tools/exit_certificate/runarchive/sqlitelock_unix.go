//go:build unix

package runarchive

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Byte ranges that SQLite's unix VFS locks with fcntl (os_unix.c). Every connection to a database in WAL mode
// holds a read lock on the DMS byte of the -shm file for as long as it is open. In rollback-journal mode, a
// connection inside a transaction locks bytes of the database file from PENDING_BYTE on.
const (
	// sqliteShmDMSOffset is UNIX_SHM_DMS: (22 + SQLITE_SHM_NLOCK) * 4 + SQLITE_SHM_NLOCK, with SQLITE_SHM_NLOCK 8.
	sqliteShmDMSOffset = 128
	// sqlitePendingByte is PENDING_BYTE.
	sqlitePendingByte = 0x40000000
	// sqliteLockBytes covers PENDING_BYTE, RESERVED_BYTE and the SHARED range (SHARED_FIRST = PENDING_BYTE + 2,
	// SHARED_SIZE 510).
	sqliteLockBytes = 512
)

// sqliteOpenElsewhere reports whether another process has the SQLite database at dbPath open (in WAL mode) or
// inside a transaction (in rollback-journal mode), going by the fcntl locks SQLite takes. Locks held by this
// process are not seen.
func sqliteOpenElsewhere(dbPath string) (bool, error) {
	locked, err := lockedByOtherProcess(dbPath+"-shm", sqliteShmDMSOffset, 1)
	if err != nil || locked {
		return locked, err
	}
	return lockedByOtherProcess(dbPath, sqlitePendingByte, sqliteLockBytes)
}

// lockedByOtherProcess reports whether another process holds an fcntl lock on [start, start+length) of path.
func lockedByOtherProcess(path string, start, length int64) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	lk := unix.Flock_t{Type: unix.F_WRLCK, Whence: io.SeekStart, Start: start, Len: length}
	if err := unix.FcntlFlock(f.Fd(), unix.F_GETLK, &lk); err != nil {
		return false, err
	}
	return lk.Type != unix.F_UNLCK, nil
}
